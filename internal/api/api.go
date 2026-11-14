package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dchest/captcha"
	"github.com/exzco/bbs/internal/config"
	"github.com/exzco/bbs/internal/models"
	"github.com/exzco/bbs/internal/service"
	"github.com/labstack/echo/v4"
	"golang.org/x/time/rate"
	"gorm.io/gorm"
)

const perPageUsers = 20

// 以下别名供既有回归测试引用；实际定义随业务下沉到了 service。
type adminUserRow = service.AdminUserRow
type UploadFile = service.UploadFile

// Server API 层：请求绑定、参数提取与 JSON 输出；业务逻辑全部在 service。
type Server struct {
	cfg *config.Config
	db  *gorm.DB
	svc *service.Service
}

func New(cfg *config.Config, g *gorm.DB) *Server {
	return &Server{cfg: cfg, db: g, svc: service.New(cfg, g)}
}

// Register 挂载全部 API 路由。简单接口用闭包取参后直调 service，
// 多步的（会话、上传、编辑）保留命名方法。
// 注意：不在 /api 组上挂 requireUser——Echo 的 Group.Use 会为该组注册带
// 中间件的 404 兜底，导致未匹配的 /api/* 请求被拦成 401 而非 404。
func (s *Server) Register(e *echo.Echo) {
	// 公开接口：会话、注册与管理（登录/登出不受只读拦截，见 readOnly）。
	pub := e.Group("/api", s.readOnly())
	pub.GET("/me", s.h(s.me))
	pub.GET("/auth/captcha", s.h(func(c echo.Context) (any, *service.Error) { return echo.Map{"id": captcha.New()}, nil }))
	pub.POST("/auth/login", s.h(s.login), s.limitWith(authLimiter))
	pub.POST("/auth/register", s.h(s.register), s.limitWith(authLimiter))
	pub.POST("/auth/logout", s.h(s.logout))

	// 内容类接口全部要求登录（内部论坛：所有内容请登录后查看）；
	// 未登录仅开放会话与注册相关接口。
	pub.POST("/me/profile", s.ha(s.updateProfile))
	pub.POST("/me/password", s.ha(s.updatePassword))
	pub.POST("/me/avatar", s.ha(s.updateAvatar))
	pub.POST("/me/email", s.ha(s.updateEmail))
	pub.GET("/me/notifications", s.ha(func(c echo.Context) (any, *service.Error) {
		items, unread := s.svc.Notifications(s.currentUser(c))
		return echo.Map{"items": items, "unread": unread}, nil
	}))
	pub.POST("/me/notifications/read", s.ha(func(c echo.Context) (any, *service.Error) {
		if err := s.svc.MarkNotificationsRead(s.currentUser(c)); err != nil {
			return nil, service.Err(http.StatusInternalServerError, "操作失败，请重试。")
		}
		return echo.Map{"ok": true}, nil
	}))
	pub.GET("/home", s.ha(func(c echo.Context) (any, *service.Error) {
		return echo.Map{"cards": s.svc.Home(s.currentUser(c))}, nil
	}))
	pub.GET("/recent", s.ha(s.recent))
	pub.GET("/groups", s.ha(func(c echo.Context) (any, *service.Error) { return echo.Map{"groups": s.svc.ListGroups()}, nil }))
	pub.GET("/tags/:slug", s.ha(func(c echo.Context) (any, *service.Error) { return s.svc.TagDetail(s.currentUser(c), c.Param("slug")) }))
	pub.GET("/tags/:slug/discussions", s.ha(func(c echo.Context) (any, *service.Error) {
		return s.svc.TagDiscussions(s.currentUser(c), c.Param("slug"), pageParam(c), c.QueryParam("all") == "1")
	}))
	pub.GET("/post-form", s.ha(func(c echo.Context) (any, *service.Error) {
		return echo.Map{"groups": s.svc.PostForm(s.currentUser(c))}, nil
	}))
	pub.POST("/preview", s.ha(s.preview))
	pub.POST("/upload", s.ha(s.upload))
	pub.GET("/fof/download/:uuid", s.ha(s.downloadFile))
	pub.HEAD("/fof/download/:uuid", s.ha(s.downloadFile))
	pub.GET("/search", s.ha(s.search))
	pub.GET("/users/:username", s.ha(func(c echo.Context) (any, *service.Error) {
		return s.svc.Profile(s.currentUser(c), c.Param("username"))
	}))
	pub.GET("/users/:username/activity", s.ha(s.userActivity))

	d := pub.Group("/discussions")
	d.GET("/:id", s.ha(s.discussionDetail))
	d.POST("", s.ha(s.createDiscussion), s.limitWith(postLimiter))
	d.POST("/:id/reply", s.ha(s.reply), s.limitWith(postLimiter))
	d.POST("/:id/mod", s.ha(s.moderate))
	p := pub.Group("/posts")
	p.GET("/:id/edit", s.ha(s.postEditGet))
	p.POST("/:id/edit", s.ha(s.editPost))
	p.POST("/:id/hide", s.ha(s.hidePost))
	p.POST("/:id/like", s.ha(s.toggleLike))

	pub.GET("/admin/meta", s.h(func(c echo.Context) (any, *service.Error) { return s.svc.AdminMeta(), nil }), s.requireAdmin())
	pub.GET("/admin/tags", s.h(func(c echo.Context) (any, *service.Error) { return echo.Map{"nodes": s.svc.AdminTags()}, nil }), s.requireAdmin())
	pub.POST("/admin/tags", s.h(func(c echo.Context) (any, *service.Error) {
		body, err := bindJSON[service.TagInput](c)
		if err != nil {
			return nil, err
		}
		return echo.Map{"ok": true}, s.svc.SaveTag(*body)
	}), s.requireAdmin())
	pub.POST("/admin/tags/:id/delete", s.h(func(c echo.Context) (any, *service.Error) {
		id, _ := parseUint(c.Param("id"))
		return echo.Map{"ok": true}, s.svc.DeleteTag(id)
	}), s.requireAdmin())
	pub.GET("/admin/users", s.h(func(c echo.Context) (any, *service.Error) {
		pp := perPageUsers
		if n, err := strconv.Atoi(c.QueryParam("per_page")); err == nil && n > 0 && n <= 1000 {
			pp = n
		}
		rows, page := s.svc.AdminUsers(
			strings.TrimSpace(c.QueryParam("q")), pageParam(c), pp,
			c.QueryParam("all") == "1" || c.QueryParam("all") == "true", c.QueryParam("sort") == "desc" || c.QueryParam("order") == "desc")
		return echo.Map{"users": rows, "page": page}, nil
	}), s.requireAdmin())
	pub.POST("/admin/users/:id", s.h(func(c echo.Context) (any, *service.Error) {
		body, err := bindJSON[struct {
			Action string `json:"action"`
			Groups []uint `json:"groups"`
		}](c)
		if err != nil {
			return nil, err
		}
		id, _ := parseUint(c.Param("id"))
		user, ids, serr := s.svc.AdminUserSave(s.currentUser(c), id, body.Action, body.Groups)
		if serr != nil {
			return nil, serr
		}
		return echo.Map{"ok": true, "user": user, "groupIds": ids}, nil
	}), s.requireAdmin())
	pub.GET("/admin/invites", s.h(func(c echo.Context) (any, *service.Error) { return echo.Map{"invites": s.svc.ListInvites()}, nil }), s.requireAdmin())
	pub.POST("/admin/invites", s.h(func(c echo.Context) (any, *service.Error) {
		body, err := bindJSON[struct {
			Count       int `json:"count"`
			ExpiresDays int `json:"expiresDays"`
		}](c)
		if err != nil {
			return nil, err
		}
		codes, serr := s.svc.CreateInvites(s.currentUser(c), body.Count, body.ExpiresDays)
		if serr != nil {
			return nil, serr
		}
		return echo.Map{"codes": codes}, nil
	}), s.requireAdmin())
	pub.GET("/admin/hidden", s.h(func(c echo.Context) (any, *service.Error) {
		return echo.Map{"items": s.svc.AdminHidden(s.currentUser(c))}, nil
	}), s.requireAdmin())
	pub.POST("/admin/discussions/:id/restore", s.h(func(c echo.Context) (any, *service.Error) {
		id, _ := parseUint(c.Param("id"))
		if err := s.svc.AdminRestore(id); err != nil {
			return nil, service.Err(http.StatusInternalServerError, "恢复失败，请重试。")
		}
		return echo.Map{"ok": true}, nil
	}), s.requireAdmin())
	pub.GET("/admin/settings", s.h(func(c echo.Context) (any, *service.Error) {
		return echo.Map{
			"siteName":     s.svc.SettingValue(models.SettingKeySiteName),
			"registration": s.svc.SettingValue(models.SettingKeyRegistration),
			"welcomeText":  s.svc.SettingValue(models.SettingKeyWelcomeText),
		}, nil
	}), s.requireAdmin())
	pub.POST("/admin/settings", s.h(func(c echo.Context) (any, *service.Error) {
		body, err := bindJSON[service.SettingsInput](c)
		if err != nil {
			return nil, err
		}
		return echo.Map{"ok": true}, s.svc.SaveSettings(*body)
	}), s.requireAdmin())
}

// ---------- 通用辅助 ----------

// h 统一适配：业务函数返回 (数据, *service.Error)，错误映射为对应状态码 JSON。
// 文件下载等已直接写出响应的 handler 通过 Response.Committed 跳过 JSON 输出。
func (s *Server) h(fn func(c echo.Context) (any, *service.Error)) echo.HandlerFunc {
	return func(c echo.Context) error {
		res, err := fn(c)
		if err != nil {
			return fail(c, err.Status, err.Message)
		}
		if c.Response().Committed {
			return nil
		}
		return c.JSON(http.StatusOK, res)
	}
}

// ha 登录后接口适配：requireUser + h。
func (s *Server) ha(fn func(c echo.Context) (any, *service.Error)) echo.HandlerFunc {
	return s.requireUser()(s.h(fn))
}

// bindJSON 解析 JSON 请求体（与 gin ShouldBindJSON 语义一致：不做 Content-Type 协商）；
// 空 body 解析为零值结构（如生成邀请码可不带 body）。解析失败统一 400。
func bindJSON[T any](c echo.Context) (*T, *service.Error) {
	var v T
	if c.Request().Body == nil || c.Request().ContentLength == 0 {
		return &v, nil
	}
	if err := json.NewDecoder(c.Request().Body).Decode(&v); err != nil {
		return nil, service.Err(http.StatusBadRequest, "请求格式错误。")
	}
	return &v, nil
}

func fail(c echo.Context, code int, msg string) error {
	return c.JSON(code, echo.Map{"error": msg})
}

func pageParam(c echo.Context) int {
	p, _ := strconv.Atoi(c.QueryParam("page"))
	if p < 1 {
		p = 1
	}
	return p
}

func parseUint(s string) (uint, error) {
	v, err := strconv.ParseUint(s, 10, 32)
	return uint(v), err
}

func (s *Server) currentUser(c echo.Context) *models.User {
	if u, ok := c.Get("user").(*models.User); ok && u != nil && !u.IsBanned() {
		return u
	}
	return nil
}

// token 当前会话 token（未登录为空）。
func token(c echo.Context) string {
	t, _ := c.Get("token").(string)
	return t
}

// ---------- 中间件 ----------

// readOnlySafeMethods / readOnlyFreePaths 只读模式放行集：无副作用方法与登录、登出。
var readOnlySafeMethods = map[string]bool{
	http.MethodGet: true, http.MethodHead: true, http.MethodOptions: true,
}

var readOnlyFreePaths = map[string]bool{
	"/api/auth/login": true, "/api/auth/logout": true,
}

// readOnly 迁移期只读拦截。
func (s *Server) readOnly() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if s.cfg.ReadOnly && !readOnlySafeMethods[c.Request().Method] && !readOnlyFreePaths[c.Request().URL.Path] {
				return c.JSON(http.StatusForbidden, echo.Map{"error": "迁移期间新站只读，请前往旧站进行修改。", "code": "read_only"})
			}
			return next(c)
		}
	}
}

func (s *Server) requireUser() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if s.currentUser(c) == nil {
				return fail(c, http.StatusUnauthorized, "请先登录。")
			}
			return next(c)
		}
	}
}

func (s *Server) requireAdmin() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			u := s.currentUser(c)
			if u == nil {
				return fail(c, http.StatusUnauthorized, "请先登录。")
			}
			if !u.IsAdmin() {
				return fail(c, http.StatusForbidden, "需要管理员权限。")
			}
			return next(c)
		}
	}
}

// ---------- 限速（按 IP + 路由类别） ----------

type rateLimiter struct {
	mu       sync.Mutex
	visitors map[string]*entry
	perMin   int
	burst    int
	sweepAt  time.Time
}

type entry struct {
	lim  *rate.Limiter
	last time.Time
}

func newRateLimiter(perMinute, burst int) *rateLimiter {
	return &rateLimiter{
		visitors: map[string]*entry{},
		perMin:   perMinute,
		burst:    burst,
		sweepAt:  time.Now().Add(10 * time.Minute),
	}
}

func (rl *rateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	// 到期顺带清理 1 小时未活跃的 IP，无需常驻清理协程。
	now := time.Now()
	if now.After(rl.sweepAt) {
		for k, v := range rl.visitors {
			if now.Sub(v.last) > time.Hour {
				delete(rl.visitors, k)
			}
		}
		rl.sweepAt = now.Add(10 * time.Minute)
	}
	e, ok := rl.visitors[ip]
	if !ok {
		e = &entry{lim: rate.NewLimiter(rate.Every(time.Minute/time.Duration(rl.perMin)), rl.burst)}
		rl.visitors[ip] = e
	}
	e.last = now
	return e.lim.Allow()
}

var (
	authLimiter = newRateLimiter(10, 10)
	postLimiter = newRateLimiter(30, 30)
)

func (s *Server) limitWith(rl *rateLimiter) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if !rl.allow(c.RealIP()) {
				return fail(c, http.StatusTooManyRequests, "操作过于频繁，请稍后再试。")
			}
			return next(c)
		}
	}
}
