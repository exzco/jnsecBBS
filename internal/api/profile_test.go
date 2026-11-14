package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/exzco/bbs/internal/cache"
	"github.com/exzco/bbs/internal/config"
	"github.com/exzco/bbs/internal/db"
	"github.com/exzco/bbs/internal/models"
	"github.com/exzco/bbs/internal/service"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

// resetCache 每个用例都是独立数据库且可能直写权限表（绕过 API 失效路径），
// 而缓存是进程级单例，必须按用例清空，避免跨用例/跨库串味。
func resetCache() {
	for _, p := range []string{"tag:", "setting:", "user:", "session:"} {
		cache.Default.DeletePrefix(p)
	}
}

func profileTestServer(t *testing.T) (*gorm.DB, *models.User, *echo.Echo) {
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
	t.Cleanup(func() { sqlDB.Close() })
	if err := db.InitializeEmpty(g); err != nil {
		t.Fatal(err)
	}
	resetCache()
	user := &models.User{Username: "profile-user", Email: "profile@example.org", Bio: "old bio"}
	if err := g.Create(user).Error; err != nil {
		t.Fatal(err)
	}
	if err := g.Create(&models.UserProfile{UserID: user.ID, RealName: "原名", BlogURL: "https://old.example.org", UpdatedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	r := echo.New()
	r.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error { c.Set("user", user); return next(c) }
	})
	New(&config.Config{}, g).Register(r)
	return g, user, r
}

func requestProfile(r *echo.Echo, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/me/profile", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

func assertOriginalProfile(t *testing.T, g *gorm.DB, sessionUser *models.User) {
	t.Helper()
	var user models.User
	if err := g.First(&user, sessionUser.ID).Error; err != nil {
		t.Fatal(err)
	}
	if user.Bio != "old bio" || sessionUser.Bio != "old bio" {
		t.Fatalf("bio changed after failed update: stored=%q session=%q", user.Bio, sessionUser.Bio)
	}
	var extra models.UserProfile
	if err := g.First(&extra, "user_id = ?", user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if extra.RealName != "原名" || extra.BlogURL != "https://old.example.org" {
		t.Fatalf("profile changed after failed update: %+v", extra)
	}
}

func TestProfileValidatesEveryFieldBeforeWriting(t *testing.T) {
	for _, field := range []string{
		`"realName":"` + strings.Repeat("名", 31) + `"`,
		`"realName":"新名字","blogUrl":"javascript:alert(1)"`,
	} {
		t.Run(field, func(t *testing.T) {
			g, user, router := profileTestServer(t)
			w := requestProfile(router, `{"bio":"new bio",`+field+`}`)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			assertOriginalProfile(t, g, user)
		})
	}
}

func TestProfileRollsBackEitherWriteFailure(t *testing.T) {
	for _, table := range []string{"flarum_users", "bbs_user_profiles"} {
		t.Run(table, func(t *testing.T) {
			g, user, router := profileTestServer(t)
			if err := g.Exec("CREATE TRIGGER reject_profile_update BEFORE UPDATE ON " + table + " BEGIN SELECT RAISE(ABORT, 'write unavailable'); END").Error; err != nil {
				t.Fatal(err)
			}
			w := requestProfile(router, `{"bio":"new bio","realName":"新名","blogUrl":"https://new.example.org"}`)
			if w.Code != http.StatusInternalServerError {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			assertOriginalProfile(t, g, user)
		})
	}
}

func TestProfileReadFailureRollsBackBio(t *testing.T) {
	g, user, router := profileTestServer(t)
	if err := g.Exec("DROP TABLE bbs_user_profiles").Error; err != nil {
		t.Fatal(err)
	}
	w := requestProfile(router, `{"bio":"new bio"}`)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var stored models.User
	if err := g.First(&stored, user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Bio != "old bio" || user.Bio != "old bio" {
		t.Fatalf("bio changed after failed read: stored=%q session=%q", stored.Bio, user.Bio)
	}
}

func TestProfileCommitsAndPreservesOmittedFields(t *testing.T) {
	g, user, router := profileTestServer(t)
	for _, tc := range []struct {
		body, realName, blogURL string
	}{
		{`{"bio":" new bio ","realName":" 新名 "}`, "新名", "https://old.example.org"},
		{`{"bio":"new bio","blogUrl":"https://new.example.org"}`, "新名", "https://new.example.org"},
		{`{"bio":"new bio","realName":"","blogUrl":""}`, "", ""},
	} {
		w := requestProfile(router, tc.body)
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		var response struct {
			User     service.UserDTO `json:"user"`
			RealName string          `json:"realName"`
			BlogURL  string          `json:"blogUrl"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		var stored models.User
		if err := g.First(&stored, user.ID).Error; err != nil {
			t.Fatal(err)
		}
		var extra models.UserProfile
		if err := g.First(&extra, "user_id = ?", user.ID).Error; err != nil {
			t.Fatal(err)
		}
		if stored.Bio != "new bio" || user.Bio != "new bio" || response.User.Bio != stored.Bio ||
			extra.RealName != tc.realName || response.RealName != extra.RealName ||
			extra.BlogURL != tc.blogURL || response.BlogURL != extra.BlogURL {
			t.Fatalf("inconsistent profile response=%+v stored=%+v extra=%+v", response, stored, extra)
		}
	}
}
