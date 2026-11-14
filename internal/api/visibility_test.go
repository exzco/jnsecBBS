package api

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/exzco/bbs/internal/cache"
	"github.com/exzco/bbs/internal/config"
	"github.com/exzco/bbs/internal/db"
	"github.com/exzco/bbs/internal/models"
	"github.com/labstack/echo/v4"
)

func TestRestrictedContentCannotLeakAcrossEndpoints(t *testing.T) {
	g, err := db.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := g.DB()
	sqlDB.SetMaxOpenConns(1)
	defer sqlDB.Close()
	if err = db.InitializeEmpty(g); err != nil {
		t.Fatal(err)
	}
	user := models.User{Username: "reader", Email: "reader@example.org"}
	if err = g.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	var public models.Tag
	g.First(&public)
	private := models.Tag{Name: "secret", Slug: "secret", IsRestricted: true}
	if err = g.Create(&private).Error; err != nil {
		t.Fatal(err)
	}
	disc := models.Discussion{Title: "secret-needle", UserID: &user.ID, IsApproved: true, Tags: []models.Tag{public, private}}
	if err = g.Create(&disc).Error; err != nil {
		t.Fatal(err)
	}
	post := models.Post{DiscussionID: disc.ID, UserID: &user.ID, Type: "comment", Content: "secret-needle", IsApproved: true}
	if err = g.Create(&post).Error; err != nil {
		t.Fatal(err)
	}
	r := echo.New()
	resetCache()
	r.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error { c.Set("user", &user); return next(c) }
	})
	New(&config.Config{}, g).Register(r)
	for _, path := range []string{"/api/home", "/api/search?q=needle", "/api/users/reader", "/api/tags/general/discussions"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 || strings.Contains(w.Body.String(), "secret-needle") {
			t.Errorf("%s: status=%d body=%s", path, w.Code, w.Body.String())
		}
	}
	for _, tc := range []struct{ method, path string }{
		{"GET", fmt.Sprintf("/api/discussions/%d", disc.ID)},
		{"GET", "/api/tags/secret"},
		{"GET", fmt.Sprintf("/api/posts/%d/edit", post.ID)},
		{"POST", fmt.Sprintf("/api/discussions/%d/reply", disc.ID)},
		{"POST", fmt.Sprintf("/api/posts/%d/like", post.ID)},
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{"content":"test"}`)))
		if w.Code != 403 && w.Code != 404 {
			t.Errorf("%s %s: got %d", tc.method, tc.path, w.Code)
		}
	}
	// The built-in member group is implicit for signed-in users, matching Flarum.
	if err = g.Create(&models.GroupPermission{GroupID: models.GroupMember, Permission: fmt.Sprintf("tag%d.viewDiscussions", private.ID)}).Error; err != nil {
		t.Fatal(err)
	}
	// 直写权限表绕过了 API 的失效路径，手动失效 tag:perms:all。
	cache.Default.Delete(cache.TagPermsKey())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", fmt.Sprintf("/api/discussions/%d", disc.ID), nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "secret-needle") {
		t.Fatalf("granted member cannot read: %d %s", w.Code, w.Body.String())
	}
}
