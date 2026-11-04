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

func TestWeeklyDiscussionRecentAndFromStart(t *testing.T) {
	g, user, router := profileTestServer(t)

	// Create weekly tag
	weeklyTag := models.Tag{Name: "周报", Slug: "weekly"}
	if err := g.Create(&weeklyTag).Error; err != nil {
		t.Fatal(err)
	}

	// Create a weekly discussion
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

	now := time.Now()
	// 楼层 1..5: 100 天前
	// 楼层 6..10: 45 天前
	// 楼层 11..20: 近 10 天内
	for i := 1; i <= 20; i++ {
		num := i
		var createdAt time.Time
		if i <= 5 {
			createdAt = now.AddDate(0, 0, -100).Add(time.Duration(i) * time.Hour)
		} else if i <= 10 {
			createdAt = now.AddDate(0, 0, -45).Add(time.Duration(i) * time.Hour)
		} else {
			createdAt = now.AddDate(0, 0, -10).Add(time.Duration(i) * time.Hour)
		}

		p := models.Post{
			DiscussionID: disc.ID,
			Number:       &num,
			UserID:       &user.ID,
			Type:         "comment",
			Content:      fmt.Sprintf("第 %d 楼周报", i),
			IsApproved:   true,
			CreatedAt:    createdAt,
		}
		if err := g.Create(&p).Error; err != nil {
			t.Fatal(err)
		}
	}

	// 1. 点开周报详情：无需去数据库查询帖子所有数据，查询近30天的即可 (应只包含 11..20 楼)
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
		WindowDays int  `json:"windowDays"`
		IsWeekly   bool `json:"isWeekly"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}

	if !res.IsWeekly {
		t.Fatalf("expected isWeekly to be true")
	}
	if res.WindowDays != 30 {
		t.Fatalf("expected windowDays to be 30, got %d", res.WindowDays)
	}
	if len(res.Posts) != 10 {
		t.Fatalf("expected 10 posts in last 30 days (floors 11..20), got %d posts", len(res.Posts))
	}
	if res.Posts[0].Number != 11 || res.Posts[len(res.Posts)-1].Number != 20 {
		t.Fatalf("expected posts 11..20, got first=%d last=%d", res.Posts[0].Number, res.Posts[len(res.Posts)-1].Number)
	}

	// 2. 测试一键置顶后从头查询最开始的数据 (from_start=1&page=1)
	wStart := httptest.NewRecorder()
	reqStart := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/discussions/%d?from_start=1&page=1&per_page=5", disc.ID), nil)
	router.ServeHTTP(wStart, reqStart)
	if wStart.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", wStart.Code, wStart.Body.String())
	}
	var resStart struct {
		Page struct {
			Page  int `json:"page"`
			Pages int `json:"pages"`
			Total int `json:"total"`
		} `json:"page"`
		Posts []struct {
			Number int `json:"number"`
		} `json:"posts"`
	}
	if err := json.Unmarshal(wStart.Body.Bytes(), &resStart); err != nil {
		t.Fatal(err)
	}
	if resStart.Page.Page != 1 || resStart.Page.Pages != 4 || resStart.Page.Total != 20 {
		t.Fatalf("expected page 1 of 4 (total 20), got %+v", resStart.Page)
	}
	if len(resStart.Posts) != 5 || resStart.Posts[0].Number != 1 || resStart.Posts[4].Number != 5 {
		t.Fatalf("expected posts 1..5 for from_start page 1, got %+v", resStart.Posts)
	}

	// 3. 测试往下滑动逐步加载数据 (from_start=1&page=2)
	wPage2 := httptest.NewRecorder()
	reqPage2 := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/discussions/%d?from_start=1&page=2&per_page=5", disc.ID), nil)
	router.ServeHTTP(wPage2, reqPage2)
	if wPage2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", wPage2.Code)
	}
	var resPage2 struct {
		Page struct {
			Page int `json:"page"`
		} `json:"page"`
		Posts []struct {
			Number int `json:"number"`
		} `json:"posts"`
	}
	if err := json.Unmarshal(wPage2.Body.Bytes(), &resPage2); err != nil {
		t.Fatal(err)
	}
	if resPage2.Page.Page != 2 || len(resPage2.Posts) != 5 || resPage2.Posts[0].Number != 6 {
		t.Fatalf("expected page 2 with posts 6..10, got %+v", resPage2)
	}

	// 4. 测试往上翻时向上加载更早数据 (before_number=11&per_page=5)
	wBefore := httptest.NewRecorder()
	reqBefore := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/discussions/%d?before_number=11&per_page=5", disc.ID), nil)
	router.ServeHTTP(wBefore, reqBefore)
	if wBefore.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", wBefore.Code)
	}
	var resBefore struct {
		Posts []struct {
			Number int `json:"number"`
		} `json:"posts"`
	}
	if err := json.Unmarshal(wBefore.Body.Bytes(), &resBefore); err != nil {
		t.Fatal(err)
	}
	if len(resBefore.Posts) != 5 || resBefore.Posts[0].Number != 6 || resBefore.Posts[4].Number != 10 {
		t.Fatalf("expected older posts 6..10 for before_number=11, got %+v", resBefore.Posts)
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

func TestWeeklyDiscussionPaginationWithHiddenPosts(t *testing.T) {
	g, user, router := profileTestServer(t)

	weeklyTag := models.Tag{Name: "周报", Slug: "weekly"}
	if err := g.Create(&weeklyTag).Error; err != nil {
		t.Fatal(err)
	}

	disc := models.Discussion{
		Title:        "带隐藏楼层的周报",
		SlugCol:      "weekly-with-hidden",
		UserID:       &user.ID,
		IsApproved:   true,
		CommentCount: 14,
		Tags:         []models.Tag{weeklyTag},
	}
	lastNum := 20
	disc.LastPostNumber = &lastNum
	if err := g.Create(&disc).Error; err != nil {
		t.Fatal(err)
	}

	// 20 posts total; posts 5..10 are hidden (6 hidden, 14 visible)
	for i := 1; i <= 20; i++ {
		num := i
		p := models.Post{
			DiscussionID: disc.ID,
			Number:       &num,
			UserID:       &user.ID,
			Type:         "comment",
			Content:      fmt.Sprintf("第 %d 楼内容", i),
			IsApproved:   true,
			CreatedAt:    time.Now().Add(time.Duration(i) * time.Hour),
		}
		if i >= 5 && i <= 10 {
			now := time.Now()
			p.HiddenAt = &now
			p.HiddenUserID = &user.ID
		}
		if err := g.Create(&p).Error; err != nil {
			t.Fatal(err)
		}
	}

	// Non-moderator request without page -> should go to last page of visible posts
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/discussions/%d", disc.ID), nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var res struct {
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

	// 14 visible posts with perPagePosts = 15 => 1 page with exactly 14 posts
	if res.Page.Total != 14 {
		t.Fatalf("expected total visible comments 14, got %d", res.Page.Total)
	}
	if res.Page.Pages != 1 {
		t.Fatalf("expected 1 page, got %d", res.Page.Pages)
	}
	if len(res.Posts) != 14 {
		t.Fatalf("expected 14 visible posts on page 1, got %d", len(res.Posts))
	}
	if res.Posts[len(res.Posts)-1].Number != 20 {
		t.Fatalf("expected latest post (number 20) to be visible on page 1, got number %d", res.Posts[len(res.Posts)-1].Number)
	}
}

func TestHideFirstPostSynchronizesDiscussionHidden(t *testing.T) {
	g, user, router := profileTestServer(t)

	disc := models.Discussion{
		Title:        "首帖隐藏测试",
		SlugCol:      "first-post-hide-test",
		UserID:       &user.ID,
		IsApproved:   true,
		CommentCount: 1,
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
		Content:      "主楼内容",
		IsApproved:   true,
	}
	if err := g.Create(&post).Error; err != nil {
		t.Fatal(err)
	}
	disc.FirstPostID = &post.ID
	g.Save(&disc)

	// Hide the first post
	wHide := httptest.NewRecorder()
	reqHide := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/posts/%d/hide", post.ID), nil)
	router.ServeHTTP(wHide, reqHide)
	if wHide.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", wHide.Code, wHide.Body.String())
	}

	// Verify discussion in DB is now hidden
	var dInDB models.Discussion
	if err := g.First(&dInDB, disc.ID).Error; err != nil {
		t.Fatal(err)
	}
	if dInDB.HiddenAt == nil {
		t.Fatalf("expected discussion.HiddenAt to be non-nil after hiding first post")
	}

	// Verify accessing discussion directly returns 404 for normal user
	wDetail := httptest.NewRecorder()
	reqDetail := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/discussions/%d", disc.ID), nil)
	router.ServeHTTP(wDetail, reqDetail)
	if wDetail.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for discussion whose first post is hidden, got %d", wDetail.Code)
	}

	// Unhide the first post
	wUnhide := httptest.NewRecorder()
	reqUnhide := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/posts/%d/hide", post.ID), nil)
	router.ServeHTTP(wUnhide, reqUnhide)
	if wUnhide.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", wUnhide.Code, wUnhide.Body.String())
	}

	// Verify discussion in DB is unhidden
	var dFresh models.Discussion
	if err := g.First(&dFresh, disc.ID).Error; err != nil {
		t.Fatal(err)
	}
	if dFresh.HiddenAt != nil {
		t.Fatalf("expected discussion.HiddenAt to be nil after unhiding first post")
	}

	// Verify accessing discussion returns 200
	wDetail2 := httptest.NewRecorder()
	reqDetail2 := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/discussions/%d", disc.ID), nil)
	router.ServeHTTP(wDetail2, reqDetail2)
	if wDetail2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", wDetail2.Code, wDetail2.Body.String())
	}
}

func TestWeeklyDiscussionFallback60Days(t *testing.T) {
	g, user, router := profileTestServer(t)

	weeklyTag := models.Tag{Name: "周报", Slug: "weekly"}
	if err := g.Create(&weeklyTag).Error; err != nil {
		t.Fatal(err)
	}

	disc := models.Discussion{
		Title:        "停更测试周报",
		SlugCol:      "weekly-fallback-test",
		UserID:       &user.ID,
		IsApproved:   true,
		CommentCount: 3,
		Tags:         []models.Tag{weeklyTag},
	}
	lastNum := 3
	disc.LastPostNumber = &lastNum
	if err := g.Create(&disc).Error; err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	// 所有帖子都在 45 天前（近 30 天内 0 条）
	for i := 1; i <= 3; i++ {
		num := i
		p := models.Post{
			DiscussionID: disc.ID,
			Number:       &num,
			UserID:       &user.ID,
			Type:         "comment",
			Content:      fmt.Sprintf("45天前第 %d 楼", i),
			IsApproved:   true,
			CreatedAt:    now.AddDate(0, 0, -45).Add(time.Duration(i) * time.Hour),
		}
		if err := g.Create(&p).Error; err != nil {
			t.Fatal(err)
		}
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/discussions/%d", disc.ID), nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var res struct {
		WindowDays int `json:"windowDays"`
		Posts      []struct {
			Number int `json:"number"`
		} `json:"posts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}

	// 近 30 天无数据，近 60 天有数据 -> windowDays 应该是 60
	if res.WindowDays != 60 {
		t.Fatalf("expected fallback to 60 days, got %d", res.WindowDays)
	}
	if len(res.Posts) != 3 {
		t.Fatalf("expected 3 posts from 60 days range, got %d", len(res.Posts))
	}
}
