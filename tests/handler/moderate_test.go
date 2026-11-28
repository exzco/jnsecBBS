package handler_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/exzco/bbs/internal/models"
)

func TestModerateDoesNotDuplicateTags(t *testing.T) {
	g, admin, router := profileTestServer(t)

	// Make user moderator/admin
	adminGroup := models.Group{ID: models.GroupAdmin}
	if err := g.Model(admin).Association("Groups").Append(&adminGroup); err != nil {
		t.Fatal(err)
	}
	admin.Groups = []models.Group{adminGroup}

	tag1 := models.Tag{Name: "周报", Slug: "weekly"}
	if err := g.Create(&tag1).Error; err != nil {
		t.Fatal(err)
	}
	tag2 := models.Tag{Name: "硬件与物联网", Slug: "hardware"}
	if err := g.Create(&tag2).Error; err != nil {
		t.Fatal(err)
	}

	d := models.Discussion{
		Title:        "第 40 期周报（10 月第一周）",
		UserID:       &admin.ID,
		IsApproved:   true,
		CommentCount: 1,
		Tags:         []models.Tag{tag1, tag2},
	}
	if err := g.Create(&d).Error; err != nil {
		t.Fatal(err)
	}

	postMod := func(action string, extra map[string]any) {
		t.Helper()
		payload := map[string]any{"action": action}
		for k, v := range extra {
			payload[k] = v
		}
		body, _ := json.Marshal(payload)
		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("/api/discussions/%d/mod", d.ID), bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("mod %s failed with code %d: %s", action, w.Code, w.Body.String())
		}
	}

	// Repeatedly toggle sticky, lock, and hidden
	for i := 0; i < 5; i++ {
		postMod("toggle_sticky", nil)
		postMod("toggle_lock", nil)
		postMod("toggle_hidden", nil)
	}

	// Check table rows in flarum_discussion_tag
	var count int64
	if err := g.Model(&models.DiscussionTag{}).Where("discussion_id = ?", d.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("expected 2 rows in flarum_discussion_tag, got %d", count)
	}

	// Test edit_meta
	postMod("edit_meta", map[string]any{
		"title":  "第 40 期周报（更新版）",
		"tagIds": []uint{tag1.ID, tag2.ID, tag1.ID}, // intentionally send duplicate ID
	})

	if err := g.Model(&models.DiscussionTag{}).Where("discussion_id = ?", d.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("expected 2 rows in flarum_discussion_tag after edit_meta, got %d", count)
	}

	// Check discussion detail API response tags
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("/api/discussions/%d", d.ID), nil)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get discussion failed: %d %s", w.Code, w.Body.String())
	}
	var resp struct {
		Discussion struct {
			Tags []struct {
				ID   uint   `json:"id"`
				Name string `json:"name"`
			} `json:"tags"`
		} `json:"discussion"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Discussion.Tags) != 2 {
		t.Fatalf("expected 2 unique tags in API response, got %d", len(resp.Discussion.Tags))
	}
}
