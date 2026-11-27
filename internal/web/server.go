// Package web 组装 HTTP 服务：会话、静态端点、REST API 与 SPA 静态资源。
package web

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"html"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/dchest/captcha"
	"github.com/exzco/bbs/internal/auth"
	"github.com/exzco/bbs/internal/config"
	"github.com/exzco/bbs/internal/frontend"
	"github.com/exzco/bbs/internal/handler"
	"github.com/exzco/bbs/internal/infra/db"
	"github.com/gorilla/csrf"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"gorm.io/gorm"
)

type Server struct {
	cfg *config.Config
	db  *gorm.DB
}

func New(cfg *config.Config, g *gorm.DB) (*Server, error) {
	if err := os.MkdirAll(filepath.Join(cfg.DataDir, "avatars"), 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	if !cfg.ReadOnly {
		if err := db.CleanupJoinTableDuplicates(g); err != nil {
			return nil, fmt.Errorf("cleanup join table duplicates: %w", err)
		}
		if err := db.SyncHiddenFirstPostDiscussions(g); err != nil {
			return nil, fmt.Errorf("sync hidden first post discussions: %w", err)
		}
	}
	return &Server{cfg: cfg, db: g}, nil
}

// Handler 返回 HTTP 处理器（Echo 引擎实现 http.Handler，供标准库互操作与测试使用）。
func (s *Server) Handler() http.Handler {
	return s.Engine()
}

// Engine 组装 Echo 引擎：CSRF 校验、会话、REST API、静态资源与 SPA 兜底。
func (s *Server) Engine() *echo.Echo {
	e := echo.New()
	e.HideBanner, e.HidePort = true, true
	// 信任代理时从 X-Forwarded-For 取客户端 IP（限速、浏览量去重依赖）。
	if s.cfg.TrustProxy {
		e.IPExtractor = echo.ExtractIPFromXFFHeader()
	} else {
		e.IPExtractor = echo.ExtractIPDirect()
	}
	s.attachCSRF(e)
	e.Use(middleware.Recover())
	// Readiness must not depend on a browser session or expose database errors.
	e.GET("/api/health", s.health)
	e.Use(s.session())

	// REST API
	handler.New(s.cfg, s.db).Register(e)

	// 磁盘目录伺服：/assets/* 头像与附件（优先 DataDir/assets，回退 AssetsDir），
	// /avatars/* 上传头像。
	serveDir := func(dirs []string) echo.HandlerFunc {
		return func(c echo.Context) error {
			clean := filepath.Clean("/" + c.Param("*"))
			for _, dir := range dirs {
				local := filepath.Join(dir, clean)
				if st, err := os.Stat(local); err == nil && !st.IsDir() {
					return c.File(local)
				}
			}
			return c.NoContent(http.StatusNotFound)
		}
	}
	assetsDirs := []string{filepath.Join(s.cfg.DataDir, "assets")}
	if s.cfg.AssetsDir != "" {
		assetsDirs = append(assetsDirs, s.cfg.AssetsDir)
	}
	e.GET("/assets/*", serveDir(assetsDirs))
	e.HEAD("/assets/*", serveDir(assetsDirs))
	e.GET("/avatars/*", serveDir([]string{filepath.Join(s.cfg.DataDir, "avatars")}))
	e.HEAD("/avatars/*", serveDir([]string{filepath.Join(s.cfg.DataDir, "avatars")}))

	// 字环头像（确定性 SVG，可长缓存）
	e.GET("/avatar/:name", s.letterAvatar)
	// 注册验证码图片（/captcha/{id}.png，?reload= 换一张）
	e.GET("/captcha/:id", s.captchaImage)

	// SPA：打包产物挂在 /static/（见 vite base），/assets/* 留给旧 Flarum 资源；
	// 其余路径回退 index.html（React Router 接管）
	e.GET("/*", s.serveSPA)
	e.HEAD("/*", s.serveSPA)
	return e
}

// ---------- 会话中间件 ----------

func (s *Server) session() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			u, token := auth.UserFromRequest(s.db, c.Request())
			c.Set("user", u)
			if token != nil {
				c.Set("token", token.Token)
			}
			return next(c)
		}
	}
}

// serveSPA 兜底路由：/static/ 下输出打包产物，其余回退 index.html。
func (s *Server) serveSPA(c echo.Context) error {
	path := strings.TrimPrefix(c.Request().URL.Path, "/")
	// Missing API/media URLs must not fall back to the SPA with status 200.
	if strings.HasPrefix(path, "api/") || path == "api" {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "接口不存在。"})
	}
	if strings.HasPrefix(path, "assets/") || strings.HasPrefix(path, "avatars/") {
		return c.NoContent(http.StatusNotFound)
	}
	if path == "" {
		path = "index.html"
	}
	// vite base=/static/：URL 带 /static 前缀，但产物在 dist 根下
	distPath := strings.TrimPrefix(path, "static/")
	// index.html 直接输出内容（文件伺服会把 /index.html 301 到 ./）
	if distPath == "index.html" || (!strings.HasPrefix(path, "static/") && !strings.Contains(distPath, ".")) {
		return s.serveIndex(c)
	}
	distFS, _ := fs.Sub(frontend.Dist, "dist")
	if st, err := fs.Stat(distFS, distPath); err == nil && !st.IsDir() {
		// vite 产物文件名带内容哈希，可 immutable 长缓存
		if strings.HasPrefix(path, "static/assets/") {
			c.Response().Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		http.ServeFileFS(c.Response(), c.Request(), distFS, distPath)
		return nil
	}
	// /static/ 下带扩展名的未知路径按 404 处理：不能把 index.html 当
	// CSS/JS 返回，否则改版后旧 HTML 引用旧哈希文件名时会出现 MIME 不符
	if strings.HasPrefix(path, "static/") {
		return c.NoContent(http.StatusNotFound)
	}
	return s.serveIndex(c)
}

// serveIndex 输出 SPA 入口。必须 no-cache：浏览器/CDN 缓存的旧 HTML 会引用
// 已不存在的哈希资源文件，页面会整体塌掉。
func (s *Server) serveIndex(c echo.Context) error {
	index, err := fs.ReadFile(frontend.Dist, "dist/index.html")
	if err != nil {
		return c.String(http.StatusInternalServerError, "前端未构建：请在 frontend/ 执行 bun run build 后重新编译。")
	}
	c.Response().Header().Set("Cache-Control", "no-cache")
	return c.Blob(http.StatusOK, "text/html; charset=utf-8", index)
}

// ---------- 静态小端点 ----------

var avatarPalette = []string{"#1e88e5", "#43a047", "#e53935", "#8e24aa", "#f4511e", "#00897b", "#3949ab", "#d81b60", "#6d4c41", "#7cb342"}

// letterAvatar 生成用户名首字的 SVG 头像（确定性颜色，可长缓存）。
func (s *Server) letterAvatar(c echo.Context) error {
	name := strings.TrimSuffix(c.Param("name"), ".svg")
	name = strings.TrimPrefix(name, "/")
	first := "?"
	for _, r := range name {
		first = string(r)
		break
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	color := avatarPalette[int(h.Sum32())%len(avatarPalette)]
	svg := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="128" height="128" viewBox="0 0 128 128">`+
		`<rect width="128" height="128" rx="64" fill="%s"/>`+
		`<text x="64" y="64" dy="0.36em" text-anchor="middle" font-family="system-ui, sans-serif" font-size="56" fill="#fff">%s</text></svg>`,
		color, html.EscapeString(first))
	c.Response().Header().Set("Cache-Control", "public, max-age=86400")
	return c.Blob(http.StatusOK, "image/svg+xml", []byte(svg))
}

// captchaImage 输出注册验证码图片。
func (s *Server) captchaImage(c echo.Context) error {
	id := strings.TrimSuffix(strings.TrimPrefix(c.Param("id"), "/"), ".png")
	if c.QueryParam("reload") != "" {
		captcha.Reload(id)
	}
	c.Response().Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	c.Response().Header().Set("Pragma", "no-cache")
	c.Response().Header().Set("Content-Type", "image/png")
	if err := captcha.WriteImage(c.Response(), id, captcha.StdWidth*2, captcha.StdHeight*2); err != nil {
		return c.NoContent(http.StatusNotFound)
	}
	return nil
}

// csrfKey 保证 32 字节：过短时用 SHA-256 派生。
func csrfKey(secret string) []byte {
	if len(secret) >= 32 {
		return []byte(secret)
	}
	sum := sha256.Sum256([]byte("jnsec-bbs-csrf:" + secret))
	return sum[:]
}

// normalizeTrustedOrigins 提取 host（兼容带协议头的配置项，匹配 gorilla/csrf 的 host 比对）。
func normalizeTrustedOrigins(origins []string) []string {
	out := make([]string, 0, len(origins))
	for _, raw := range origins {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		if strings.Contains(s, "://") {
			if u, err := url.Parse(s); err == nil && u.Host != "" {
				s = u.Host
			}
		}
		out = append(out, strings.TrimSuffix(s, "/"))
	}
	return out
}

// attachCSRF 把 gorilla/csrf 校验挂到引擎最外层（pre-router），与迁移前 csrf 包裹整个引擎的顺序一致；
// 明文 HTTP 部署先在请求上打标放宽 Origin/Referer 比对，否则写请求会全部 403。
func (s *Server) attachCSRF(e *echo.Echo) {
	opts := []csrf.Option{
		csrf.Secure(s.cfg.CookieSecure),
		csrf.Path("/"),
		csrf.MaxAge(auth.SessionMaxAge),
		// 旧名称可能留有多个不同 Path 的 Cookie，换名避免浏览器继续优先发送旧值。
		csrf.CookieName("jnsec_csrf_token"),
		csrf.FieldName("_csrf"),
		csrf.RequestHeader("X-CSRF-Token"),
	}
	if origins := normalizeTrustedOrigins(s.cfg.TrustedOrigins); len(origins) > 0 {
		opts = append(opts, csrf.TrustedOrigins(origins))
	}
	opts = append(opts, csrf.ErrorHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reason := csrf.FailureReason(r)
		_, cookieErr := r.Cookie("jnsec_csrf_token")
		log.Printf("[csrf] 校验失败 method=%q path=%q host=%q origin=%q proto=%q hasCookie=%v reason=%q cf_ray=%q",
			r.Method, r.URL.Path, r.Host, r.Header.Get("Origin"),
			r.Header.Get("X-Forwarded-Proto"), cookieErr == nil, reason, r.Header.Get("CF-Ray"))
		code := "csrf_failed"
		if errors.Is(reason, csrf.ErrBadToken) || errors.Is(reason, csrf.ErrNoToken) {
			code = "csrf_invalid"
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusForbidden)
		if err := json.NewEncoder(w).Encode(map[string]string{
			"error": "CSRF 校验失败，请刷新页面重试。",
			"code":  code,
		}); err != nil {
			log.Printf("[csrf] 响应写入失败: %v", err)
		}
	})))
	protect := csrf.Protect(csrfKey(s.cfg.SessionSecret), opts...)
	e.Pre(echo.WrapMiddleware(func(next http.Handler) http.Handler {
		if s.cfg.CookieSecure {
			return protect(next)
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			protect(next).ServeHTTP(w, csrf.PlaintextHTTPRequest(r))
		})
	}))
}
