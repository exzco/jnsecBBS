package handler_test

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/exzco/bbs/internal/config"
	"github.com/exzco/bbs/internal/handler"
	"github.com/exzco/bbs/internal/infra/db"
	"github.com/exzco/bbs/internal/middleware"
	"github.com/exzco/bbs/internal/models"
	"github.com/exzco/bbs/internal/service"
	"github.com/exzco/bbs/tests/testdb"
	"github.com/labstack/echo/v4"
)

func TestAnyLoggedInUserCanReadAndReplyAcrossTags(t *testing.T) {
	g, err := db.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := g.DB()
	sqlDB.SetMaxOpenConns(1)
	defer sqlDB.Close()
	if err = testdb.InitializeEmpty(g); err != nil {
		t.Fatal(err)
	}
	user := models.User{Username: "reader", Email: "reader@example.org"}
	if err = g.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	var public models.Tag
	g.First(&public)
	restricted := models.Tag{Name: "secret", Slug: "secret", IsRestricted: true}
	if err = g.Create(&restricted).Error; err != nil {
		t.Fatal(err)
	}
	disc := models.Discussion{Title: "secret-needle", UserID: &user.ID, IsApproved: true, Tags: []models.Tag{public, restricted}}
	if err = g.Create(&disc).Error; err != nil {
		t.Fatal(err)
	}
	post := models.Post{DiscussionID: disc.ID, UserID: &user.ID, Type: "comment", Content: "secret-needle", IsApproved: true}
	if err = g.Create(&post).Error; err != nil {
		t.Fatal(err)
	}
	r := echo.New()
	r.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error { c.Set("user", &user); return next(c) }
	})
	cfg := &config.Config{}
	middleware.Register(r, cfg, g, handler.New(cfg, g))

	for _, path := range []string{"/api/home", "/api/search?q=needle", "/api/users/reader", "/api/tags/general/discussions", "/api/tags/secret", fmt.Sprintf("/api/discussions/%d", disc.ID)} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 {
			t.Errorf("%s: status=%d body=%s", path, w.Code, w.Body.String())
		}
	}
	wReply := httptest.NewRecorder()
	reqReply := httptest.NewRequest("POST", fmt.Sprintf("/api/discussions/%d/reply", disc.ID), strings.NewReader(`{"content":"any-member-reply"}`))
	reqReply.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(wReply, reqReply)
	if wReply.Code != 200 {
		t.Fatalf("reply on restricted tag failed: status=%d body=%s", wReply.Code, wReply.Body.String())
	}
}

func TestAuthorCanPhysicallyDeleteOwnDiscussionAndCannotTouchOthersReplies(t *testing.T) {
	g, err := db.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := g.DB()
	sqlDB.SetMaxOpenConns(1)
	defer sqlDB.Close()
	if err = testdb.InitializeEmpty(g); err != nil {
		t.Fatal(err)
	}

	author := models.User{Username: "author", Email: "author@example.org"}
	other := models.User{Username: "other", Email: "other@example.org"}
	if err = g.Create(&author).Error; err != nil {
		t.Fatal(err)
	}
	if err = g.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	var tag models.Tag
	g.First(&tag)

	svc := service.New(&config.Config{}, g)
	created, serr := svc.CreateDiscussion(&author, "作者待删除帖子", "正文内容", []uint{tag.ID})
	if serr != nil {
		t.Fatalf("CreateDiscussion failed: %s", serr.Message)
	}
	replyRes, serr := svc.Reply(&other, created.ID, "其他人的回复")
	if serr != nil {
		t.Fatalf("Reply failed: %s", serr.Message)
	}

	// Author cannot edit another user's reply.
	if _, errEdit := svc.EditPost(&author, replyRes.PostID, "篡改"); errEdit == nil || errEdit.Status != 403 {
		t.Fatalf("expected 403 when author edits someone else's reply, got %+v", errEdit)
	}
	// Non-author cannot delete someone else's discussion.
	if errDel := svc.DeleteDiscussion(&other, created.ID); errDel == nil || errDel.Status != 403 {
		t.Fatalf("expected 403 when non-author deletes discussion, got %+v", errDel)
	}

	// Author physically deletes their own discussion.
	r := echo.New()
	r.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error { c.Set("user", &author); return next(c) }
	})
	cfg := &config.Config{}
	middleware.Register(r, cfg, g, handler.New(cfg, g))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", fmt.Sprintf("/api/discussions/%d/delete", created.ID), nil))
	if w.Code != 200 {
		t.Fatalf("author delete failed: status=%d body=%s", w.Code, w.Body.String())
	}

	var discCount, postCount int64
	g.Model(&models.Discussion{}).Where("id = ?", created.ID).Count(&discCount)
	g.Model(&models.Post{}).Where("discussion_id = ?", created.ID).Count(&postCount)
	if discCount != 0 || postCount != 0 {
		t.Fatalf("expected physical delete of discussion and posts, got disc=%d post=%d", discCount, postCount)
	}
}

func TestPostFormAndPostingWithoutChildTag(t *testing.T) {
	g, err := db.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := g.DB()
	sqlDB.SetMaxOpenConns(1)
	defer sqlDB.Close()
	if err = testdb.InitializeEmpty(g); err != nil {
		t.Fatal(err)
	}

	user := models.User{Username: "author", Email: "author@example.org"}
	if err = g.Create(&user).Error; err != nil {
		t.Fatal(err)
	}

	pos1, pos2, pos3 := 1, 2, 1
	parentTech := models.Tag{Name: "技术讨论", Slug: "tech", Position: &pos1}
	if err = g.Create(&parentTech).Error; err != nil {
		t.Fatal(err)
	}
	childFE := models.Tag{Name: "前端开发", Slug: "frontend", ParentID: &parentTech.ID, Position: &pos3}
	if err = g.Create(&childFE).Error; err != nil {
		t.Fatal(err)
	}
	parentNotice := models.Tag{Name: "公告版块", Slug: "notice", Position: &pos2}
	if err = g.Create(&parentNotice).Error; err != nil {
		t.Fatal(err)
	}

	r := echo.New()
	r.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error { c.Set("user", &user); return next(c) }
	})
	cfg := &config.Config{}
	server := handler.New(cfg, g)
	middleware.Register(r, cfg, g, server)
	svc := service.New(cfg, g)

	// 1. 测试 PostForm：有子版块的父版块 children 仅包含子标签；无子版块的父版块 children 为空
	groups := svc.PostForm(&user)
	var foundTech, foundNotice bool
	for _, grp := range groups {
		if grp.Parent.Slug == "tech" {
			foundTech = true
			if len(grp.Children) != 1 || grp.Children[0].Slug != "frontend" {
				t.Fatalf("tech group children mismatch: got %+v", grp.Children)
			}
		}
		if grp.Parent.Slug == "notice" {
			foundNotice = true
			if len(grp.Children) != 0 {
				t.Fatalf("notice group children should be empty, got %+v", grp.Children)
			}
		}
	}
	if !foundTech || !foundNotice {
		t.Fatalf("missing groups in PostForm: foundTech=%v foundNotice=%v", foundTech, foundNotice)
	}

	// 2. 测试发帖：允许不选择任何一个子标签，仅选择父版块 tech 发帖
	resOnlyParent, errSvc := svc.CreateDiscussion(&user, "仅选择父版块发帖测试", "正文内容无需子标签", []uint{parentTech.ID})
	if errSvc != nil {
		t.Fatalf("create discussion with only parent tag failed: %v", errSvc.Message)
	}
	if resOnlyParent.ID == 0 {
		t.Fatal("expected valid discussion ID")
	}

	// 3. 验证讨论在父版块 tech 下可见
	listResult, errList := svc.TagDiscussions(&user, "tech", 1, false)
	if errList != nil {
		t.Fatalf("failed to query tag discussions: %v", errList.Message)
	}
	if len(listResult.Items) == 0 || listResult.Items[0].ID != resOnlyParent.ID {
		t.Fatalf("discussion not found in parent tag discussions: %+v", listResult.Items)
	}
}
