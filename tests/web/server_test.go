package web_test

import (
	"net/http/httptest"
	"testing"

	"github.com/exzco/bbs/internal/config"
	"github.com/exzco/bbs/internal/web"
)

func TestMissingResourcesDoNotBecomeHTML(t *testing.T) {
	s, err := web.New(&config.Config{DataDir: t.TempDir(), AssetsDir: t.TempDir()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := s.Handler()
	for _, path := range []string{"/avatars/missing.png", "/assets/avatars/missing.png", "/api/missing", "/static/missing", "/static/missing.js"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 404 {
			t.Errorf("%s: got %d", path, w.Code)
		}
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/d/1-title", nil))
	if w.Code != 200 {
		t.Fatalf("SPA route broken: %d", w.Code)
	}
}
