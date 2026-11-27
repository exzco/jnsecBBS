package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/exzco/bbs/internal/models"
)

func TestActivityCountsDiscussionsAndRepliesOnceAtCSTBoundaries(t *testing.T) {
	g, user, router := profileTestServer(t)
	createDiscussion := func(at string) models.Discussion {
		t.Helper()
		created, err := time.Parse(time.RFC3339, at)
		if err != nil {
			t.Fatal(err)
		}
		d := models.Discussion{Title: "activity", UserID: &user.ID, CreatedAt: created, IsApproved: true}
		if err := g.Create(&d).Error; err != nil {
			t.Fatal(err)
		}
		return d
	}
	createPost := func(d models.Discussion, at string, number int) models.Post {
		t.Helper()
		created, err := time.Parse(time.RFC3339, at)
		if err != nil {
			t.Fatal(err)
		}
		p := models.Post{DiscussionID: d.ID, UserID: &user.ID, Number: &number, CreatedAt: created, Type: "comment", IsApproved: true}
		if err := g.Create(&p).Error; err != nil {
			t.Fatal(err)
		}
		return p
	}
	d := createDiscussion("2023-12-31T16:00:00Z")
	// A first_post_id also wins when a historical post has an unexpected number.
	first := createPost(d, "2023-12-31T16:00:00Z", 3)
	if err := g.Model(&d).Update("first_post_id", first.ID).Error; err != nil {
		t.Fatal(err)
	}
	createPost(d, "2024-01-01T16:00:00Z", 4)
	createPost(d, "2023-12-31T15:59:59Z", 5) // before the selected year in CST
	createPost(d, "2024-12-31T16:00:00Z", 6) // next year in CST
	createDiscussion("2024-12-31T15:59:59Z") // survives the second Pluck
	legacy := createDiscussion("2024-03-04T16:00:00Z")
	createPost(legacy, "2024-03-04T16:00:00Z", 1) // no first_post_id: number 1 is the fallback
	createPost(legacy, "2024-03-04T16:00:00Z", 2)

	// Activity must apply the same privacy and moderation boundaries as content.
	for _, attrs := range []map[string]any{
		{"is_private": true},
		{"hidden_at": time.Now()},
		{"is_approved": false},
	} {
		hidden := createDiscussion("2024-03-04T16:00:00Z")
		if err := g.Model(&hidden).Updates(attrs).Error; err != nil {
			t.Fatal(err)
		}
		createPost(hidden, "2024-03-04T16:00:00Z", 2)
	}
	privateTag := models.Tag{Name: "private", Slug: "private", IsRestricted: true}
	if err := g.Create(&privateTag).Error; err != nil {
		t.Fatal(err)
	}
	restricted := createDiscussion("2024-03-04T16:00:00Z")
	if err := g.Model(&restricted).Association("Tags").Append(&privateTag); err != nil {
		t.Fatal(err)
	}
	createPost(restricted, "2024-03-04T16:00:00Z", 2)
	for _, attrs := range []map[string]any{
		{"is_private": true},
		{"hidden_at": time.Now()},
		{"is_approved": false},
		{"type": "discussionRenamed"},
	} {
		hidden := createPost(d, "2024-03-04T16:00:00Z", 7)
		if err := g.Model(&hidden).Updates(attrs).Error; err != nil {
			t.Fatal(err)
		}
	}

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/users/PROFILE-USER/activity?year=2024", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var response struct {
		Year  int `json:"year"`
		Total int `json:"total"`
		Days  []struct {
			Date  string `json:"date"`
			Count int    `json:"count"`
		} `json:"days"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Year != 2024 || response.Total != 5 || len(response.Days) != 366 {
		t.Fatalf("unexpected year, total or leap-year day count: %+v", response)
	}
	want := map[string]int{"2024-01-01": 1, "2024-01-02": 1, "2024-03-05": 2, "2024-12-31": 1}
	for _, day := range response.Days {
		if day.Count != want[day.Date] {
			t.Errorf("%s: got %d want %d", day.Date, day.Count, want[day.Date])
		}
	}
}

func TestActivityQueryFailureDoesNotReturnPartialCounts(t *testing.T) {
	for _, table := range []string{"flarum_discussions", "flarum_posts"} {
		t.Run(table, func(t *testing.T) {
			g, _, router := profileTestServer(t)
			if err := g.Exec("DROP TABLE " + table).Error; err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/users/profile-user/activity?year=2024", nil))
			if w.Code != http.StatusInternalServerError {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}
