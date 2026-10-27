// Package web 组装 HTTP 服务：会话、静态端点、REST API 与 SPA 静态资源。
package web

import (
	"fmt"
	"hash/fnv"
	"html"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/dchest/captcha"
	"github.com/exzco/bbs/internal/api"
	"github.com/exzco/bbs/internal/auth"
	"github.com/exzco/bbs/internal/config"
	"github.com/exzco/bbs/internal/db"
	"github.com/exzco/bbs/internal/frontend"
	"github.com/gin-gonic/gin"
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
		_ = db.CleanupJoinTableDuplicates(g)
	}
	return &Server{cfg: cfg, db: g}, nil
}

func (s *Server) Handler() http.Handler {
	if s.cfg.LogLevel != "debug" {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	r.Use(gin.Recovery())
	if s.cfg.TrustProxy {
		_ = r.SetTrustedProxies([]string{"0.0.0.0/0"})
	} else {
		_ = r.SetTrustedProxies(nil)
	}
	r.MaxMultipartMemory = 4 << 20
	// Readiness must not depend on a browser session or expose database errors.
	r.GET("/api/health", s.health)
	r.Use(s.sessionMiddleware())

	// REST API
	api.New(s.cfg, s.db).Register(r)

	// 旧 Flarum 静态资源（/assets/* 头像与附件，随导入保留可访问）
	if s.cfg.AssetsDir != "" {
		r.Static("/assets", s.cfg.AssetsDir)
	}

	// 上传头像（磁盘目录）
	r.Static("/avatars", filepath.Join(s.cfg.DataDir, "avatars"))
	// 字环头像（确定性 SVG，可长缓存）
	r.GET("/avatar/:name", s.letterAvatar)
	// 注册验证码图片（/captcha/{id}.png，?reload= 换一张）
	r.GET("/captcha/:id", s.captchaImage)

	// SPA：打包产物挂在 /static/（见 vite base），/assets/* 留给旧 Flarum 资源；
	// 其余路径回退 index.html（React Router 接管）
	r.NoRoute(func(c *gin.Context) {
		path := strings.TrimPrefix(c.Request.URL.Path, "/")
		// Missing API/media URLs must not fall back to the SPA with status 200.
		if strings.HasPrefix(path, "api/") || path == "api" {
			c.JSON(http.StatusNotFound, gin.H{"error": "接口不存在。"})
			return
		}
		if strings.HasPrefix(path, "assets/") || strings.HasPrefix(path, "avatars/") {
			c.Status(http.StatusNotFound)
			return
		}
		if path == "" {
			path = "index.html"
		}
		// vite base=/static/：URL 带 /static 前缀，但产物在 dist 根下
		distPath := strings.TrimPrefix(path, "static/")
		// index.html 直接输出内容（http.FileServer 会把 /index.html 301 到 ./）
		if distPath == "index.html" || (!strings.HasPrefix(path, "static/") && !strings.Contains(distPath, ".")) {
			s.serveIndex(c)
			return
		}
		distFS, _ := fs.Sub(frontend.Dist, "dist")
		if f, err := distFS.Open(distPath); err == nil {
			st, err := f.Stat()
			_ = f.Close()
			if err == nil && !st.IsDir() {
				// vite 产物文件名带内容哈希，可 immutable 长缓存
				if strings.HasPrefix(path, "static/assets/") {
					c.Header("Cache-Control", "public, max-age=31536000, immutable")
				}
				c.FileFromFS(distPath, http.FS(distFS))
				return
			}
		}
		// /static/ 下带扩展名的未知路径按 404 处理：不能把 index.html 当
		// CSS/JS 返回，否则改版后旧 HTML 引用旧哈希文件名时会出现 MIME 不符
		if strings.HasPrefix(path, "static/") {
			c.Status(http.StatusNotFound)
			return
		}
		s.serveIndex(c)
	})
	return r
}

// serveIndex 输出 SPA 入口。必须 no-cache：浏览器/CDN 缓存的旧 HTML 会引用
// 已不存在的哈希资源文件，页面会整体塌掉。
func (s *Server) serveIndex(c *gin.Context) {
	index, err := fs.ReadFile(frontend.Dist, "dist/index.html")
	if err != nil {
		c.String(http.StatusInternalServerError, "前端未构建：请在 frontend/ 执行 bun run build 后重新编译。")
		return
	}
	c.Header("Cache-Control", "no-cache")
	c.Data(http.StatusOK, "text/html; charset=utf-8", index)
}

// ---------- 会话中间件 ----------

func (s *Server) sessionMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		u, token := auth.UserFromRequest(s.db, c.Request)
		c.Set("user", u)
		if token != nil {
			c.Set("token", token.Token)
		}
		c.Next()
	}
}

// ---------- 静态小端点 ----------

var avatarPalette = []string{"#1e88e5", "#43a047", "#e53935", "#8e24aa", "#f4511e", "#00897b", "#3949ab", "#d81b60", "#6d4c41", "#7cb342"}

// letterAvatar 生成用户名首字的 SVG 头像（确定性颜色，可长缓存）。
func (s *Server) letterAvatar(c *gin.Context) {
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
	c.Header("Cache-Control", "public, max-age=86400")
	c.Data(http.StatusOK, "image/svg+xml", []byte(svg))
}

// captchaImage 输出注册验证码图片。
func (s *Server) captchaImage(c *gin.Context) {
	id := strings.TrimSuffix(strings.TrimPrefix(c.Param("id"), "/"), ".png")
	if c.Query("reload") != "" {
		captcha.Reload(id)
	}
	c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
	c.Header("Pragma", "no-cache")
	c.Header("Content-Type", "image/png")
	if err := captcha.WriteImage(c.Writer, id, captcha.StdWidth*2, captcha.StdHeight*2); err != nil {
		c.Status(http.StatusNotFound)
	}
}
