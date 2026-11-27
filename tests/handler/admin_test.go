package handler_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/exzco/bbs/internal/models"
	"github.com/exzco/bbs/internal/service"
)

func TestAdminUsersPaginationAndSearch(t *testing.T) {
	g, admin, router := profileTestServer(t)

	// Make user admin
	adminGroup := models.Group{ID: models.GroupAdmin}
	if err := g.Model(admin).Association("Groups").Append(&adminGroup); err != nil {
		t.Fatal(err)
	}
	admin.Groups = []models.Group{adminGroup}

	// Create 25 additional users (so total is 26)
	for i := 1; i <= 25; i++ {
		u := models.User{
			Username: fmt.Sprintf("testuser%02d", i),
			Email:    fmt.Sprintf("user%02d@example.org", i),
		}
		if err := g.Create(&u).Error; err != nil {
			t.Fatal(err)
		}
	}

	type response struct {
		Users []service.AdminUserRow `json:"users"`
		Page  service.PageDTO        `json:"page"`
	}

	// 1. Page 1 with per_page=10
	{
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/admin/users?page=1&per_page=10", nil)
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		var res response
		if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
			t.Fatal(err)
		}
		if res.Page.Total != 26 {
			t.Fatalf("expected 26 total users, got %d", res.Page.Total)
		}
		if res.Page.Pages != 3 {
			t.Fatalf("expected 3 pages, got %d", res.Page.Pages)
		}
		if len(res.Users) != 10 {
			t.Fatalf("expected 10 users on page 1, got %d", len(res.Users))
		}
		// First user should be admin (ID 1, ascending order by default)
		if res.Users[0].User.ID != 1 {
			t.Fatalf("expected first user to have ID 1, got %d", res.Users[0].User.ID)
		}
	}

	// 2. Page 3 with per_page=10 (last page with remaining 6 users)
	{
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/admin/users?page=3&per_page=10", nil)
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		var res response
		if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
			t.Fatal(err)
		}
		if len(res.Users) != 6 {
			t.Fatalf("expected 6 users on page 3, got %d", len(res.Users))
		}
	}

	// 3. Sort descending
	{
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/admin/users?page=1&per_page=10&sort=desc", nil)
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		var res response
		if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
			t.Fatal(err)
		}
		if res.Users[0].User.ID < res.Users[1].User.ID {
			t.Fatalf("expected descending order, but first ID %d < second ID %d", res.Users[0].User.ID, res.Users[1].User.ID)
		}
	}

	// 4. Search by username keyword
	{
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/admin/users?q=testuser05", nil)
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		var res response
		if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
			t.Fatal(err)
		}
		if res.Page.Total != 1 || len(res.Users) != 1 || res.Users[0].User.Username != "testuser05" {
			t.Fatalf("expected 1 search result for testuser05, got total=%d, users=%d", res.Page.Total, len(res.Users))
		}
	}

	// 5. Search by ID
	{
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/admin/users?q=1", nil)
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		var res response
		if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
			t.Fatal(err)
		}
		foundID1 := false
		for _, u := range res.Users {
			if u.User.ID == 1 {
				foundID1 = true
				break
			}
		}
		if !foundID1 {
			t.Fatalf("expected to find user ID 1 when searching for '1'")
		}
	}
}
