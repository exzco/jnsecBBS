package web_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/exzco/bbs/internal/config"
	"github.com/exzco/bbs/internal/handler"
	"github.com/exzco/bbs/internal/infra/db"
	"github.com/exzco/bbs/internal/middleware"
	"github.com/exzco/bbs/tests/testdb"
	"github.com/labstack/echo/v4"
)

func newCSRFTestHandler(t *testing.T, cfg *config.Config) http.Handler {
	t.Helper()
	g, err := db.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := g.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := testdb.InitializeEmpty(g); err != nil {
		t.Fatal(err)
	}
	cfg.DataDir = t.TempDir()
	if cfg.SessionSecret == "" {
		cfg.SessionSecret = "test-session-secret-0123456789abcdef"
	}
	e := echo.New()
	h := handler.New(cfg, g)
	e.Pre(middleware.CSRF(cfg))
	e.Use(middleware.Session(h.Svc()))
	middleware.Register(e, cfg, g, h)
	return e
}

func findCookie(cookies []*http.Cookie, name string) *http.Cookie {
	for _, c := range cookies {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestCSRFCookiePathMaxAgeAndNoStore(t *testing.T) {
	h := newCSRFTestHandler(t, &config.Config{
		CookieSecure:   true,
		TrustedOrigins: []string{"https://bbs.jnsec.org"},
	})

	// 1. Visiting a deep SPA route must still issue jnsec_csrf_token with Path=/ and 30d MaxAge.
	w1 := httptest.NewRecorder()
	h.ServeHTTP(w1, httptest.NewRequest(http.MethodGet, "/d/123-hello", nil))
	if w1.Code != http.StatusOK {
		t.Fatalf("SPA GET status=%d", w1.Code)
	}
	csrfCookie := findCookie(w1.Result().Cookies(), "jnsec_csrf_token")
	if csrfCookie == nil {
		t.Fatal("missing jnsec_csrf_token cookie")
	}
	if csrfCookie.Path != "/" {
		t.Fatalf("expected Path=/, got %q", csrfCookie.Path)
	}
	if csrfCookie.MaxAge != handler.SessionMaxAge {
		t.Fatalf("expected MaxAge=%d, got %d", handler.SessionMaxAge, csrfCookie.MaxAge)
	}
	if !csrfCookie.Secure || !csrfCookie.HttpOnly {
		t.Fatalf("expected Secure & HttpOnly cookie, got %+v", csrfCookie)
	}

	// 2. /api/me with that cookie must return Cache-Control: no-store and a usable csrfToken.
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	r2.AddCookie(csrfCookie)
	h.ServeHTTP(w2, r2)
	if w2.Code != http.StatusOK {
		t.Fatalf("/api/me status=%d body=%s", w2.Code, w2.Body.String())
	}
	if cc := w2.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("expected Cache-Control=no-store, got %q", cc)
	}
	var me struct {
		CSRFToken string `json:"csrfToken"`
	}
	if err := json.Unmarshal(w2.Body.Bytes(), &me); err != nil || me.CSRFToken == "" {
		t.Fatalf("invalid /api/me payload: err=%v body=%s", err, w2.Body.String())
	}

	// 3. POST with valid cookie, token, and normalized TrustedOrigins must succeed.
	w3 := httptest.NewRecorder()
	r3 := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	r3.Host = "internal:8080"
	r3.Header.Set("Origin", "https://bbs.jnsec.org")
	r3.Header.Set("X-CSRF-Token", me.CSRFToken)
	r3.AddCookie(csrfCookie)
	// 浏览器可能同时发送旧版不同 Path 的同名 Cookie，不能影响新 Token。
	r3.AddCookie(&http.Cookie{Name: "jnsec_csrf", Value: "stale-specific-path-cookie"})
	r3.AddCookie(&http.Cookie{Name: "jnsec_csrf", Value: "stale-api-cookie"})
	h.ServeHTTP(w3, r3)
	if w3.Code != http.StatusOK {
		t.Fatalf("POST logout status=%d body=%s", w3.Code, w3.Body.String())
	}

	// 有效 Token 也不能绕过来源校验；此类 403 不应触发前端刷新重试。
	w4 := httptest.NewRecorder()
	r4 := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	r4.Header.Set("Origin", "https://untrusted.example")
	r4.Header.Set("X-CSRF-Token", me.CSRFToken)
	r4.AddCookie(csrfCookie)
	h.ServeHTTP(w4, r4)
	var rejected map[string]string
	if err := json.Unmarshal(w4.Body.Bytes(), &rejected); err != nil {
		t.Fatal(err)
	}
	if w4.Code != http.StatusForbidden || rejected["code"] != "csrf_failed" {
		t.Fatalf("untrusted origin must remain rejected: status=%d body=%s", w4.Code, w4.Body.String())
	}
}

func TestCSRFErrorReturnsJSONAndFreshCookieAllowsRetry(t *testing.T) {
	h := newCSRFTestHandler(t, &config.Config{CookieSecure: false})

	// 页面仍持有合法格式的旧 Token，但 Cookie 已过期或被其他标签页替换。
	initial := httptest.NewRecorder()
	h.ServeHTTP(initial, httptest.NewRequest(http.MethodGet, "/api/me", nil))
	var staleSession struct {
		CSRFToken string `json:"csrfToken"`
	}
	if err := json.Unmarshal(initial.Body.Bytes(), &staleSession); err != nil || staleSession.CSRFToken == "" {
		t.Fatalf("initial session failed: status=%d body=%s", initial.Code, initial.Body.String())
	}
	w1 := httptest.NewRecorder()
	r1 := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	r1.Header.Set("X-CSRF-Token", staleSession.CSRFToken)
	h.ServeHTTP(w1, r1)
	if w1.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w1.Code)
	}
	var errBody map[string]string
	if err := json.Unmarshal(w1.Body.Bytes(), &errBody); err != nil {
		t.Fatalf("expected JSON error body, got %q: %v", w1.Body.String(), err)
	}
	if errBody["code"] != "csrf_invalid" || errBody["error"] == "" {
		t.Fatalf("unexpected error payload: %+v", errBody)
	}
	freshCookie := findCookie(w1.Result().Cookies(), "jnsec_csrf_token")
	if freshCookie == nil || freshCookie.Path != "/" {
		t.Fatalf("expected fresh Path=/ jnsec_csrf_token cookie on 403, got %+v", freshCookie)
	}

	// Client refreshes /api/me with the fresh cookie and retries POST -> succeeds.
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	r2.AddCookie(freshCookie)
	h.ServeHTTP(w2, r2)
	var me struct {
		CSRFToken string `json:"csrfToken"`
	}
	if err := json.Unmarshal(w2.Body.Bytes(), &me); err != nil || me.CSRFToken == "" {
		t.Fatalf("invalid /api/me body: %s", w2.Body.String())
	}

	w3 := httptest.NewRecorder()
	r3 := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	r3.AddCookie(freshCookie)
	r3.Header.Set("X-CSRF-Token", me.CSRFToken)
	h.ServeHTTP(w3, r3)
	if w3.Code != http.StatusOK {
		t.Fatalf("retry after refresh failed: status=%d body=%s", w3.Code, w3.Body.String())
	}
}
