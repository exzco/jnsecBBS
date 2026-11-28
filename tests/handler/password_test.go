package handler_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/exzco/bbs/internal/config"
	"github.com/exzco/bbs/internal/models"
	"github.com/exzco/bbs/internal/service"
	"github.com/labstack/echo/v4"
)

func TestPasswordChangeContractAndSessionRevocation(t *testing.T) {
	g, user, router := profileTestServer(t)
	if err := user.SetPassword("old-password"); err != nil {
		t.Fatal(err)
	}
	if err := g.Model(user).Update("password", user.Password).Error; err != nil {
		t.Fatal(err)
	}
	otherUser := models.User{Username: "other-password-user", Email: "other-password@example.org"}
	if err := g.Create(&otherUser).Error; err != nil {
		t.Fatal(err)
	}
	currentToken, otherToken, unrelatedToken := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	now := time.Now()
	tokens := []models.AccessToken{
		{Token: currentToken, UserID: user.ID, Type: "session", CreatedAt: now, LastActivityAt: now},
		{Token: otherToken, UserID: user.ID, Type: "session", CreatedAt: now, LastActivityAt: now},
		{Token: unrelatedToken, UserID: otherUser.ID, Type: "session", CreatedAt: now, LastActivityAt: now},
	}
	if err := g.Create(&tokens).Error; err != nil {
		t.Fatal(err)
	}
	router.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Set("token", currentToken)
			return next(c)
		}
	})
	svc := service.New(&config.Config{}, g)
	if loggedIn, _ := svc.UserByToken(otherToken); loggedIn == nil {
		t.Fatal("other device must be logged in before the password change")
	}

	for _, body := range []string{
		`{"current":"old-password","next":"new-password","confirm":"new-password"}`,
		`{"current":"wrong-password","new":"new-password","confirm":"new-password"}`,
		`{"current":"old-password","new":"short","confirm":"short"}`,
		`{"current":"old-password","new":"new-password","confirm":"different-password"}`,
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/me/password", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("invalid password request: status=%d body=%s", w.Code, w.Body.String())
		}
	}
	var unchanged models.User
	if err := g.First(&unchanged, user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !unchanged.CheckPassword("old-password") {
		t.Fatal("rejected requests changed the password")
	}
	var count int64
	if err := g.Model(&models.AccessToken{}).Count(&count).Error; err != nil || count != 3 {
		t.Fatalf("rejected requests revoked sessions: count=%d err=%v", count, err)
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/me/password", strings.NewReader(`{"current":"old-password","new":"new-password","confirm":"new-password"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("password change: status=%d body=%s", w.Code, w.Body.String())
	}
	var updated models.User
	if err := g.First(&updated, user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !updated.CheckPassword("new-password") || updated.CheckPassword("old-password") {
		t.Fatal("password change did not replace the stored password")
	}
	var remaining []models.AccessToken
	if err := g.Order("id").Find(&remaining).Error; err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 2 || remaining[0].Token != currentToken || remaining[1].Token != unrelatedToken {
		t.Fatal("password change must retain the current session and unrelated users' sessions")
	}
	if loggedIn, _ := svc.UserByToken(otherToken); loggedIn != nil {
		t.Fatal("revoked device still authenticated")
	}
}
