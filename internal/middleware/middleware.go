package middleware

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/exzco/bbs/internal/config"
	"github.com/exzco/bbs/internal/handler"
	"github.com/exzco/bbs/internal/models"
	"github.com/exzco/bbs/internal/service"
	"github.com/gorilla/csrf"
	"github.com/labstack/echo/v4"
	"golang.org/x/time/rate"
)

func activeUser(c echo.Context) *models.User {
	if u, ok := c.Get("user").(*models.User); ok && u != nil && !u.IsBanned() {
		return u
	}
	return nil
}

// Session 注入当前请求会话用户与 token 到 Echo 上下文。
func Session(svc *service.Service) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if ck, err := c.Cookie(handler.SessionCookie); err == nil && ck.Value != "" {
				u, tok := svc.UserByToken(ck.Value)
				c.Set("user", u)
				if tok != nil {
					c.Set("token", tok.Token)
				}
			}
			return next(c)
		}
	}
}

// NoStore 禁止浏览器或 CDN 缓存 API 响应
func NoStore() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Response().Header().Set("Cache-Control", "no-store")
			return next(c)
		}
	}
}

var readOnlySafeMethods = map[string]bool{
	http.MethodGet: true, http.MethodHead: true, http.MethodOptions: true,
}

var readOnlyFreePaths = map[string]bool{
	"/api/auth/login": true, "/api/auth/logout": true,
}

// ReadOnly 迁移期只读拦截。
func ReadOnly(cfg *config.Config) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if cfg.ReadOnly && !readOnlySafeMethods[c.Request().Method] && !readOnlyFreePaths[c.Request().URL.Path] {
				return c.JSON(http.StatusForbidden, echo.Map{"error": "迁移期间新站只读，请前往旧站进行修改。", "code": "read_only"})
			}
			return next(c)
		}
	}
}

// RequireUser 要求请求已登录且未被封禁。
func RequireUser() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if activeUser(c) == nil {
				return c.JSON(http.StatusUnauthorized, echo.Map{"error": "请先登录。"})
			}
			return next(c)
		}
	}
}

// RequireAdmin 要求请求用户为管理员。
func RequireAdmin() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			u := activeUser(c)
			if u == nil {
				return c.JSON(http.StatusUnauthorized, echo.Map{"error": "请先登录。"})
			}
			if !u.IsAdmin() {
				return c.JSON(http.StatusForbidden, echo.Map{"error": "需要管理员权限。"})
			}
			return next(c)
		}
	}
}

// ---------- 限速（按 IP + 路由类别） ----------

type RateLimiter struct {
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

func NewRateLimiter(perMinute, burst int) *RateLimiter {
	return &RateLimiter{
		visitors: map[string]*entry{},
		perMin:   perMinute,
		burst:    burst,
		sweepAt:  time.Now().Add(10 * time.Minute),
	}
}

func (rl *RateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
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
	AuthLimiter = NewRateLimiter(10, 10)
	PostLimiter = NewRateLimiter(30, 30)
)

// LimitWith 按给定限流器限制单 IP 请求频次。
func LimitWith(rl *RateLimiter) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if !rl.allow(c.RealIP()) {
				return c.JSON(http.StatusTooManyRequests, echo.Map{"error": "操作过于频繁，请稍后再试。"})
			}
			return next(c)
		}
	}
}

// ---------- CSRF ----------

func csrfKey(secret string) []byte {
	if len(secret) >= 32 {
		return []byte(secret)
	}
	sum := sha256.Sum256([]byte("jnsec-bbs-csrf:" + secret))
	return sum[:]
}

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

// CSRF 返回挂在 Echo Pre 阶段的 gorilla/csrf 校验中间件。
func CSRF(cfg *config.Config) echo.MiddlewareFunc {
	opts := []csrf.Option{
		csrf.Secure(cfg.CookieSecure),
		csrf.Path("/"),
		csrf.MaxAge(handler.SessionMaxAge),
		csrf.CookieName("jnsec_csrf_token"),
		csrf.FieldName("_csrf"),
		csrf.RequestHeader("X-CSRF-Token"),
	}
	if origins := normalizeTrustedOrigins(cfg.TrustedOrigins); len(origins) > 0 {
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
	protect := csrf.Protect(csrfKey(cfg.SessionSecret), opts...)
	return echo.WrapMiddleware(func(next http.Handler) http.Handler {
		if cfg.CookieSecure {
			return protect(next)
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			protect(next).ServeHTTP(w, csrf.PlaintextHTTPRequest(r))
		})
	})
}
