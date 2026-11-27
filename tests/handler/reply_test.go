package handler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/exzco/bbs/internal/config"
	"github.com/exzco/bbs/internal/handler"
	"github.com/exzco/bbs/internal/infra/db"
	"github.com/exzco/bbs/internal/models"
	"github.com/exzco/bbs/internal/service"
	"github.com/exzco/bbs/tests/testdb"
	mysql "github.com/go-sql-driver/mysql"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

type replyResult struct {
	PostID uint `json:"postId"`
	Number int  `json:"number"`
	Page   int  `json:"page"`
}

func requestReply(router *echo.Echo, discussionID uint) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/discussions/%d/reply", discussionID), strings.NewReader(`{"content":"回归测试回复"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	return w
}

func decodeReply(t *testing.T, w *httptest.ResponseRecorder) replyResult {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var result replyResult
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func seedReplyDiscussion(t *testing.T, g *gorm.DB, user *models.User, numbers []int, index int) models.Discussion {
	t.Helper()
	d := models.Discussion{Title: "历史回复", UserID: &user.ID, IsApproved: true, CommentCount: len(numbers), PostNumberIndex: index}
	if err := g.Create(&d).Error; err != nil {
		t.Fatal(err)
	}
	for _, number := range numbers {
		p := models.Post{DiscussionID: d.ID, Number: &number, UserID: &user.ID, CreatedAt: time.Now(), Type: "comment", Content: "原文", IsApproved: true}
		if err := g.Create(&p).Error; err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func addPostNumberConstraint(t *testing.T, g *gorm.DB) {
	t.Helper()
	// 与旧站的唯一索引一致，使重复楼层必定失败。
	if err := g.Exec("CREATE UNIQUE INDEX flarum_posts_discussion_id_number_unique ON flarum_posts (discussion_id, number)").Error; err != nil {
		t.Fatal(err)
	}
}

func TestReplyAllocatesAfterHistoricalFloors(t *testing.T) {
	for _, tc := range []struct {
		name        string
		extras      bool
		index, want int
	}{
		{name: "production_gap", index: 143, want: 145},
		{name: "all_post_types_reserve_floors", index: 143, extras: true, want: 201},
		{name: "deleted_highest_floor", index: 250, want: 251},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g, user, router := profileTestServer(t)
			addPostNumberConstraint(t, g)
			numbers := make([]int, 143)
			for i := range numbers {
				numbers[i] = i + 1
			}
			numbers[142] = 144 // 143 条评论已有第 144 楼；旧实现再次分配 144。
			d := seedReplyDiscussion(t, g, user, numbers, tc.index)
			if tc.extras {
				for i, kind := range []string{"event", "hidden", "private", "unapproved", "unnumbered"} {
					number := 197 + i
					p := models.Post{DiscussionID: d.ID, Number: &number, UserID: &user.ID, Type: "comment", IsApproved: kind != "unapproved", IsPrivate: kind == "private", CreatedAt: time.Now()}
					switch kind {
					case "event", "unnumbered":
						p.Type = "discussionRenamed"
						if kind == "unnumbered" {
							p.Number = nil
						}
					case "hidden":
						now := time.Now()
						p.HiddenAt = &now
					}
					if err := g.Create(&p).Error; err != nil {
						t.Fatal(err)
					}
				}
			}
			result := decodeReply(t, requestReply(router, d.ID))
			if result.Number != tc.want || result.Page != 10 {
				t.Fatalf("unexpected reply: %+v; want number=%d page=10", result, tc.want)
			}
			var stored models.Discussion
			if err := g.First(&stored, d.ID).Error; err != nil {
				t.Fatal(err)
			}
			if stored.PostNumberIndex != tc.want || stored.CommentCount != 144 || stored.LastPostNumber == nil || *stored.LastPostNumber != tc.want || stored.LastPostID == nil || *stored.LastPostID != result.PostID {
				t.Fatalf("incorrect discussion counters: %+v", stored)
			}
			var post models.Post
			if err := g.First(&post, result.PostID).Error; err != nil || post.PostNumber() != tc.want || post.Markdown() != "回归测试回复" {
				t.Fatalf("reply not saved correctly: %+v err=%v", post, err)
			}
		})
	}
}

func TestReplyRollsBackAllocatedNumberAndCounters(t *testing.T) {
	g, user, router := profileTestServer(t)
	addPostNumberConstraint(t, g)
	d := seedReplyDiscussion(t, g, user, []int{144}, 200)
	if err := g.Exec("CREATE TRIGGER reject_reply_count BEFORE UPDATE OF comment_count ON flarum_users BEGIN SELECT RAISE(ABORT, 'write unavailable'); END").Error; err != nil {
		t.Fatal(err)
	}
	w := requestReply(router, d.ID)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var stored models.Discussion
	if err := g.First(&stored, d.ID).Error; err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := g.Model(&models.Post{}).Where("discussion_id = ?", d.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 || stored.CommentCount != 1 || stored.PostNumberIndex != 200 || stored.LastPostID != nil {
		t.Fatalf("failed reply left changes: posts=%d discussion=%+v", count, stored)
	}
	if err := g.Exec("DROP TRIGGER reject_reply_count").Error; err != nil {
		t.Fatal(err)
	}
	if result := decodeReply(t, requestReply(router, d.ID)); result.Number != 201 || result.Page != 1 {
		t.Fatalf("retry skipped a rolled-back floor: %+v", result)
	}
}

// BBS_REPLY_TEST_DSN must point to an expendable MySQL server with CREATE/DROP
// DATABASE permission. The test only creates and drops its uniquely named DB.
func TestReplyConcurrentMySQL(t *testing.T) {
	dsn := os.Getenv("BBS_REPLY_TEST_DSN")
	if dsn == "" {
		t.Skip("set BBS_REPLY_TEST_DSN to run real MySQL concurrent reply regression")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid reply test MySQL DSN")
	}
	cfg.DBName, cfg.ParseTime, cfg.MultiStatements = "", true, false
	admin, err := db.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	adminSQL, err := admin.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { adminSQL.Close() })
	name := fmt.Sprintf("bbs_reply_test_%d", time.Now().UnixNano())
	if err := admin.Exec("CREATE DATABASE `" + name + "` CHARACTER SET utf8mb4").Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := admin.Exec("DROP DATABASE `" + name + "`").Error; err != nil {
			t.Error(err)
		}
	})
	cfg.DBName = name
	g, err := db.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := g.DB()
	if err != nil {
		t.Fatal(err)
	}
	const workers = 20
	sqlDB.SetMaxOpenConns(workers + 4)
	t.Cleanup(func() { sqlDB.Close() })
	if err := testdb.InitializeEmpty(g); err != nil {
		t.Fatal(err)
	}
	addPostNumberConstraint(t, g)
	user := &models.User{Username: "reply-user", Email: "reply@example.org"}
	if err := g.Create(user).Error; err != nil {
		t.Fatal(err)
	}
	d := seedReplyDiscussion(t, g, user, []int{100}, 100)
	router := echo.New()
	resetCache()
	router.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error { c.Set("user", user); return next(c) }
	})
	s := handler.New(&config.Config{}, g)
	// Use the production routes with a dedicated IP so other tests do not consume
	// this test's rate-limit allowance; all 20 workers fit within its burst.
	router.IPExtractor = func(*http.Request) string { return "198.51.100.2" }
	s.Register(router)

	// Force every request to preload the same stale discussion before any starts
	// its transaction. A single connection or sequential requests hide this bug.
	arrived := make(chan struct{}, workers)
	barrier, release := context.WithTimeout(context.Background(), 15*time.Second)
	defer release()
	const callback = "test:reply_preload_barrier"
	if err := g.Callback().Query().After("gorm:after_query").Register(callback, func(query *gorm.DB) {
		if barrier.Err() != nil || query.Error != nil || query.Statement.Table != "flarum_discussions" {
			return
		}
		if _, inTransaction := query.Statement.ConnPool.(gorm.TxCommitter); inTransaction {
			return
		}
		arrived <- struct{}{}
		<-barrier.Done()
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = g.Callback().Query().Remove(callback) })
	results := make(chan *httptest.ResponseRecorder, workers)
	for i := 0; i < workers; i++ {
		go func() { results <- requestReply(router, d.ID) }()
	}
	for i := 0; i < workers; i++ {
		select {
		case <-arrived:
		case <-barrier.Done():
			t.Fatal("concurrent requests did not reach the preload barrier")
		}
	}
	release()
	responses := make([]*httptest.ResponseRecorder, 0, workers)
	for i := 0; i < workers; i++ {
		select {
		case w := <-results:
			responses = append(responses, w)
		case <-time.After(15 * time.Second):
			t.Fatal("concurrent replies did not finish")
		}
	}
	seen := make(map[int]uint, workers)
	for _, w := range responses {
		result := decodeReply(t, w)
		if result.Number < 101 || result.Number > 100+workers || result.PostID == 0 || seen[result.Number] != 0 || result.Page != (result.Number-100)/service.PerPagePosts+1 {
			t.Fatalf("duplicate/incorrect concurrent reply: %+v", result)
		}
		seen[result.Number] = result.PostID
	}
	var stored models.Discussion
	if err := g.First(&stored, d.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.CommentCount != workers+1 || stored.PostNumberIndex != 100+workers || stored.LastPostNumber == nil || *stored.LastPostNumber != 100+workers || stored.LastPostID == nil || *stored.LastPostID != seen[100+workers] {
		t.Fatalf("lost concurrent counter updates: %+v", stored)
	}
	var storedUser models.User
	if err := g.First(&storedUser, user.ID).Error; err != nil || storedUser.PostsCount != workers {
		t.Fatalf("lost user counter updates: user=%+v err=%v", storedUser, err)
	}
}
