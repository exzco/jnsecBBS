package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/exzco/bbs/internal/models"
)

func TestWeeklyDiscussionDefaultsToLastPageAndTracksRead(t *testing.T) {
	g, user, router := profileTestServer(t)

	// Create weekly tag
	weeklyTag := models.Tag{Name: "周报", Slug: "weekly"}
	if err := g.Create(&weeklyTag).Error; err != nil {
		t.Fatal(err)
	}

	// Create a weekly discussion with 20 posts (so 2 pages with perPagePosts = 15)
	disc := models.Discussion{
		Title:        "2026年度周报合集",
		SlugCol:      "weekly-2026",
		UserID:       &user.ID,
		IsApproved:   true,
		CommentCount: 20,
		Tags:         []models.Tag{weeklyTag},
	}
	lastNum := 20
	disc.LastPostNumber = &lastNum
	if err := g.Create(&disc).Error; err != nil {
		t.Fatal(err)
	}

	for i := 1; i <= 20; i++ {
		num := i
		p := models.Post{
			DiscussionID: disc.ID,
			Number:       &num,
			UserID:       &user.ID,
			Type:         "comment",
			Content:      fmt.Sprintf("第 %d 楼周报", i),
			IsApproved:   true,
			CreatedAt:    time.Now().Add(time.Duration(i) * time.Hour),
		}
		if err := g.Create(&p).Error; err != nil {
			t.Fatal(err)
		}
	}

	// Request without ?page=
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/discussions/%d", disc.ID), nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var res struct {
		Discussion struct {
			HasUnread bool `json:"hasUnread"`
		} `json:"discussion"`
		Page struct {
			Page  int `json:"page"`
			Pages int `json:"pages"`
			Total int `json:"total"`
		} `json:"page"`
		Posts []struct {
			Number int `json:"number"`
		} `json:"posts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}

	if res.Page.Page != 2 {
		t.Fatalf("expected weekly discussion to default to page 2 (last page), got page %d", res.Page.Page)
	}
	if len(res.Posts) == 0 || res.Posts[len(res.Posts)-1].Number != 20 {
		t.Fatalf("expected latest post (floor 20) to be in posts, got %v", res.Posts)
	}
	if res.Discussion.HasUnread {
		t.Fatalf("expected viewing discussion to mark HasUnread=false, got true")
	}

	// Verify flarum_discussion_user record was saved
	var du models.DiscussionUser
	if err := g.Where("user_id = ? AND discussion_id = ?", user.ID, disc.ID).First(&du).Error; err != nil {
		t.Fatalf("expected read progress to be recorded: %v", err)
	}
	if du.LastReadPostNumber == nil || *du.LastReadPostNumber != 20 {
		t.Fatalf("expected last read post number 20, got %v", du.LastReadPostNumber)
	}

	// Verify subsequent /api/recent returns hasUnread = false
	wRecent := httptest.NewRecorder()
	reqRecent := httptest.NewRequest(http.MethodGet, "/api/recent", nil)
	router.ServeHTTP(wRecent, reqRecent)
	if wRecent.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", wRecent.Code)
	}
	var recentRes struct {
		Items []struct {
			ID        uint `json:"id"`
			HasUnread bool `json:"hasUnread"`
		} `json:"items"`
	}
	if err := json.Unmarshal(wRecent.Body.Bytes(), &recentRes); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range recentRes.Items {
		if item.ID == disc.ID {
			found = true
			if item.HasUnread {
				t.Fatalf("expected read discussion to have HasUnread=false in recent stream, got true")
			}
		}
	}
	if !found {
		t.Fatalf("expected disc %d to be in recent items", disc.ID)
	}
}
