package api

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/exzco/bbs/internal/config"
	"github.com/labstack/echo/v4"
)

func TestReadOnlyRejectsAllForumWrites(t *testing.T) {
	r := echo.New()
	New(&config.Config{ReadOnly: true}, nil).Register(r)
	for _, path := range []string{"/api/discussions", "/api/discussions/1/reply", "/api/posts/1/edit", "/api/posts/1/like", "/api/auth/register", "/api/me/profile", "/api/me/avatar", "/api/me/password", "/api/admin/tags", "/api/admin/settings", "/api/me/notifications/read"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("POST", path, strings.NewReader(`{}`)))
		if w.Code != 403 || !strings.Contains(w.Body.String(), `"code":"read_only"`) {
			t.Errorf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
	// Malformed login reaches validation rather than the read-only guard.
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{}`)))
	if w.Code != 400 {
		t.Fatalf("login incorrectly blocked: %d", w.Code)
	}
}
