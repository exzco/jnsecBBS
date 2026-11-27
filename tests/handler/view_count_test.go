package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/exzco/bbs/internal/models"
	"github.com/exzco/bbs/internal/service"
)

func TestDiscussionViewCountAndViewers(t *testing.T) {
	g, user, router := profileTestServer(t)

	// Create a test discussion
	disc := models.Discussion{
		Title:        "测试浏览量与浏览者历史",
		SlugCol:      "test-view-count",
		UserID:       &user.ID,
		IsApproved:   true,
		CommentCount: 1,
		ViewCount:    5,
		CreatedAt:    time.Now().Add(-1 * time.Hour),
	}
	if err := g.Create(&disc).Error; err != nil {
		t.Fatal(err)
	}

	num := 1
	post := models.Post{
		DiscussionID: disc.ID,
		Number:       &num,
		UserID:       &user.ID,
		Type:         "comment",
		Content:      "首楼内容",
		IsApproved:   true,
		CreatedAt:    time.Now().Add(-1 * time.Hour),
	}
	if err := g.Create(&post).Error; err != nil {
		t.Fatal(err)
	}

	// 1. 发起请求访问讨论详情
	req := httptest.NewRequest(http.MethodGet, "/api/discussions/1", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var res struct {
		Discussion service.DiscussionSummaryDTO  `json:"discussion"`
		Viewers    []service.DiscussionViewerDTO `json:"viewers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal response failed: %v", err)
	}

	// 浏览量应自增 1: 5 -> 6
	if res.Discussion.ViewCount != 6 {
		t.Fatalf("expected viewCount 6, got %d", res.Discussion.ViewCount)
	}

	// 浏览者列表中应包含当前访问用户
	if len(res.Viewers) != 1 {
		t.Fatalf("expected 1 viewer, got %d", len(res.Viewers))
	}
	if res.Viewers[0].User.Username != user.Username {
		t.Fatalf("expected viewer username %s, got %s", user.Username, res.Viewers[0].User.Username)
	}

	// 2. 5分钟内重复访问，不应重复递增浏览量
	req2 := httptest.NewRequest(http.MethodGet, "/api/discussions/1", nil)
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w2.Code)
	}

	var res2 struct {
		Discussion service.DiscussionSummaryDTO  `json:"discussion"`
		Viewers    []service.DiscussionViewerDTO `json:"viewers"`
	}
	if err := json.Unmarshal(w2.Body.Bytes(), &res2); err != nil {
		t.Fatalf("unmarshal response failed: %v", err)
	}
	if res2.Discussion.ViewCount != 6 {
		t.Fatalf("expected viewCount to stay 6 within 5min, got %d", res2.Discussion.ViewCount)
	}
}
