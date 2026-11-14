package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/exzco/bbs/internal/config"
	"github.com/exzco/bbs/internal/db"
	"github.com/exzco/bbs/internal/models"
	"github.com/exzco/bbs/internal/service"
	"github.com/labstack/echo/v4"
)

func TestPostLikeBubbles(t *testing.T) {
	g, user, router := profileTestServer(t)

	// 创建第二位用户
	user2 := models.User{
		Username:         "liker2",
		Email:            "liker2@example.org",
		IsEmailConfirmed: true,
	}
	if err := g.Create(&user2).Error; err != nil {
		t.Fatal(err)
	}

	disc := models.Discussion{
		Title:        "测试点赞与人物气泡",
		SlugCol:      "test-like-bubbles",
		UserID:       &user.ID,
		IsApproved:   true,
		CommentCount: 1,
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
		Content:      "这是一条测试帖，用来测试点赞气泡。",
		IsApproved:   true,
		CreatedAt:    time.Now().Add(-1 * time.Hour),
	}
	if err := g.Create(&post).Error; err != nil {
		t.Fatal(err)
	}

	// 1. 未点赞前获取讨论，likers 应为空
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/discussions/%d", disc.ID), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var res struct {
		Posts []service.PostDTO `json:"posts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Posts) != 1 {
		t.Fatalf("expected 1 post, got %d", len(res.Posts))
	}
	if res.Posts[0].LikeCount != 0 {
		t.Fatalf("expected likeCount 0, got %d", res.Posts[0].LikeCount)
	}
	if len(res.Posts[0].Likers) != 0 {
		t.Fatalf("expected 0 likers, got %d", len(res.Posts[0].Likers))
	}

	// 2. 当前用户 (user) 点赞 post
	likeReq := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/posts/%d/like", post.ID), nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, likeReq)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 on like, got %d: %s", w.Code, w.Body.String())
	}
	var likeRes struct {
		Liked     bool              `json:"liked"`
		LikeCount int               `json:"likeCount"`
		Likers    []service.UserDTO `json:"likers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &likeRes); err != nil {
		t.Fatal(err)
	}
	if !likeRes.Liked || likeRes.LikeCount != 1 {
		t.Fatalf("expected liked=true, count=1, got liked=%v, count=%d", likeRes.Liked, likeRes.LikeCount)
	}
	if len(likeRes.Likers) != 1 || likeRes.Likers[0].Username != user.Username {
		t.Fatalf("expected liker %s, got %+v", user.Username, likeRes.Likers)
	}

	// 3. 用户 2 也点赞该 post（直接在表中插入以模拟并发/第二人点赞）
	if err := g.Create(&models.PostLike{PostID: post.ID, UserID: user2.ID, CreatedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}

	// 再次查询讨论详情，PostDTO.Likers 应该包含两位用户，且 LikeCount 为 2
	req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/discussions/%d", disc.ID), nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	res = struct {
		Posts []service.PostDTO `json:"posts"`
	}{}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Posts[0].LikeCount != 2 {
		t.Fatalf("expected likeCount 2, got %d", res.Posts[0].LikeCount)
	}
	if len(res.Posts[0].Likers) != 2 {
		t.Fatalf("expected 2 likers, got %d", len(res.Posts[0].Likers))
	}
	likerNames := []string{res.Posts[0].Likers[0].Username, res.Posts[0].Likers[1].Username}
	if (likerNames[0] != user.Username && likerNames[1] != user.Username) ||
		(likerNames[0] != user2.Username && likerNames[1] != user2.Username) {
		t.Fatalf("expected likers to contain %s and %s, got %v", user.Username, user2.Username, likerNames)
	}

	// 4. 当前用户取消点赞 (toggle off)
	likeReq = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/posts/%d/like", post.ID), nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, likeReq)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 on unlike, got %d: %s", w.Code, w.Body.String())
	}
	likeRes = struct {
		Liked     bool              `json:"liked"`
		LikeCount int               `json:"likeCount"`
		Likers    []service.UserDTO `json:"likers"`
	}{}
	if err := json.Unmarshal(w.Body.Bytes(), &likeRes); err != nil {
		t.Fatal(err)
	}
	if likeRes.Liked || likeRes.LikeCount != 1 {
		t.Fatalf("expected liked=false, count=1, got liked=%v, count=%d", likeRes.Liked, likeRes.LikeCount)
	}
	if len(likeRes.Likers) != 1 || likeRes.Likers[0].Username != user2.Username {
		t.Fatalf("expected remaining liker %s, got %+v", user2.Username, likeRes.Likers)
	}

	// 5. 模拟批量点赞超过 20 人，验证 Likers 最多返回 20 人且 LikeCount 准确
	for i := 3; i <= 26; i++ {
		extraUser := models.User{
			Username:         fmt.Sprintf("extra_liker_%02d", i),
			Email:            fmt.Sprintf("liker_%02d@example.org", i),
			IsEmailConfirmed: true,
		}
		if err := g.Create(&extraUser).Error; err != nil {
			t.Fatal(err)
		}
		if err := g.Create(&models.PostLike{
			PostID:    post.ID,
			UserID:    extraUser.ID,
			CreatedAt: time.Now().Add(time.Duration(i) * time.Minute),
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	// 当前共有 1 (user2) + 24 (extra_liker_03..26) = 25 个赞
	req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/discussions/%d", disc.ID), nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	res = struct {
		Posts []service.PostDTO `json:"posts"`
	}{}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Posts[0].LikeCount != 25 {
		t.Fatalf("expected likeCount 25, got %d", res.Posts[0].LikeCount)
	}
	if len(res.Posts[0].Likers) != 20 {
		t.Fatalf("expected capped 20 likers, got %d", len(res.Posts[0].Likers))
	}
	// 验证倒序：最新点赞的 extra_liker_26 应排在第一位
	if res.Posts[0].Likers[0].Username != "extra_liker_26" {
		t.Fatalf("expected first liker to be extra_liker_26, got %s", res.Posts[0].Likers[0].Username)
	}
}

func TestPostLikeUnauthorized(t *testing.T) {
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
	router := echo.New()
	resetCache()
	New(&config.Config{}, g).Register(router)

	req := httptest.NewRequest(http.MethodPost, "/api/posts/1/like", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 unauthorized, got %d: %s", w.Code, w.Body.String())
	}
}
