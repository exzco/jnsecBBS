package web_test

import (
	"net/http/httptest"
	"testing"

	"github.com/exzco/bbs/internal/config"
	"github.com/exzco/bbs/internal/infra/db"
	"github.com/exzco/bbs/internal/web"
)

func TestHealthDetectsClosedDatabase(t *testing.T) {
	g, err := db.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	connection, err := g.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	s, err := web.New(&config.Config{DataDir: t.TempDir()}, g)
	if err != nil {
		t.Fatal(err)
	}
	handler := s.Handler()
	for _, want := range []int{200, 503} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/api/health", nil)
		// Health bypasses session lookup, including when the DB is unavailable.
		r.Header.Set("Cookie", "jnsec_session=invalid")
		handler.ServeHTTP(w, r)
		if w.Code != want || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("health: status=%d body=%s", w.Code, w.Body.String())
		}
		_ = connection.Close()
	}
}
