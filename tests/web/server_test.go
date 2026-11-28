package web_test

import (
	"net/http/httptest"
	"testing"

	"github.com/exzco/bbs/internal/config"
	"github.com/exzco/bbs/internal/handler"
	"github.com/exzco/bbs/internal/middleware"
	"github.com/labstack/echo/v4"
)

func TestMissingResourcesDoNotBecomeHTML(t *testing.T) {
	cfg := &config.Config{DataDir: t.TempDir(), AssetsDir: t.TempDir()}
	e := echo.New()
	h := handler.New(cfg, nil)
	middleware.Register(e, cfg, nil, h)
	for _, path := range []string{"/avatars/missing.png", "/assets/avatars/missing.png", "/api/missing", "/static/missing", "/static/missing.js"} {
		w := httptest.NewRecorder()
		e.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 404 {
			t.Errorf("%s: got %d", path, w.Code)
		}
	}
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httptest.NewRequest("GET", "/d/1-title", nil))
	if w.Code != 200 {
		t.Fatalf("SPA route broken: %d", w.Code)
	}
}
