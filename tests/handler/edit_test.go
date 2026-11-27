package handler_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/exzco/bbs/internal/models"
)

func TestLegacyEditLinkLocatesPageByCommentsNotFloorNumber(t *testing.T) {
	g, user, router := profileTestServer(t)
	d := models.Discussion{Title: "编辑定位", SlugCol: "edit-location", UserID: &user.ID, IsApproved: true}
	if err := g.Create(&d).Error; err != nil {
		t.Fatal(err)
	}
	var last models.Post
	for i := 1; i <= 16; i++ {
		number := i
		if i == 16 {
			number = 100 // 旧站的系统事件和删除会留下楼层空隙。
		}
		last = models.Post{DiscussionID: d.ID, Number: &number, UserID: &user.ID, Type: "comment", Content: "原文", IsApproved: true, CreatedAt: time.Now()}
		if err := g.Create(&last).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, kind := range []string{"event", "private", "unapproved"} {
		number := 90
		p := models.Post{DiscussionID: d.ID, Number: &number, UserID: &user.ID, Type: "comment", IsApproved: kind != "unapproved", IsPrivate: kind == "private", CreatedAt: time.Now()}
		if kind == "event" {
			p.Type = "discussionRenamed"
		}
		if err := g.Create(&p).Error; err != nil {
			t.Fatal(err)
		}
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/posts/%d/edit", last.ID), nil))
	var result struct {
		ID         uint `json:"id"`
		Page       int  `json:"page"`
		Discussion struct {
			ID    uint   `json:"id"`
			Title string `json:"title"`
		} `json:"discussion"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || result.Page != 2 || result.ID != last.ID || result.Discussion.ID != d.ID || result.Discussion.Title != d.Title {
		t.Fatalf("status=%d response=%s", w.Code, w.Body.String())
	}
}
