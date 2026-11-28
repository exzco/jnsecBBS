package handler_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

func TestHiddenWeeklyDiscussionsFilteredForNonAdmins(t *testing.T) {
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

	admin := models.User{Username: "admin", Email: "admin@example.org", Groups: []models.Group{{ID: models.GroupAdmin}}}
	mod := models.User{Username: "mod", Email: "mod@example.org", Groups: []models.Group{{ID: models.GroupMod}}}
	member := models.User{Username: "member", Email: "member@example.org", Groups: []models.Group{{ID: models.GroupMember}}}
	for _, u := range []*models.User{&admin, &mod, &member} {
		if err = g.Create(u).Error; err != nil {
			t.Fatal(err)
		}
	}

	weekly := models.Tag{Name: "周报", Slug: "weekly"}
	if err = g.Create(&weekly).Error; err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	older := now.Add(-time.Hour)
	one := 1
	visibleDisc := models.Discussion{
		Title: "公开周报 26/4/24", UserID: &member.ID, IsApproved: true,
		CommentCount: 1, LastPostedAt: &older, LastPostNumber: &one, Tags: []models.Tag{weekly},
	}
	hiddenDisc := models.Discussion{
		Title: "罗谢慈 周报26/4/25", UserID: &member.ID, IsApproved: true,
		CommentCount: 1, LastPostedAt: &now, LastPostNumber: &one, HiddenAt: &now, Tags: []models.Tag{weekly},
	}
	if err = g.Create(&visibleDisc).Error; err != nil {
		t.Fatal(err)
	}
	if err = g.Create(&hiddenDisc).Error; err != nil {
		t.Fatal(err)
	}
	visiblePost := models.Post{DiscussionID: visibleDisc.ID, Number: &one, UserID: &member.ID, Type: "comment", Content: "周报公开内容", IsApproved: true}
	hiddenPost := models.Post{DiscussionID: hiddenDisc.ID, Number: &one, UserID: &member.ID, Type: "comment", Content: "周报已删除内容", IsApproved: true}
	if err = g.Create(&visiblePost).Error; err != nil {
		t.Fatal(err)
	}
	if err = g.Create(&hiddenPost).Error; err != nil {
		t.Fatal(err)
	}
	if err = g.Create(&models.Notification{
		UserID: member.ID, SenderID: admin.ID, Type: "reply",
		DiscussionID: hiddenDisc.ID, PostID: hiddenPost.ID,
	}).Error; err != nil {
		t.Fatal(err)
	}

	newRouter := func(u *models.User) *echo.Echo {
		r := echo.New()
		r.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c echo.Context) error { c.Set("user", u); return next(c) }
		})
		cfg := &config.Config{}
		middleware.Register(r, cfg, g, handler.New(cfg, g))
		return r
	}
	doReq := func(r *echo.Echo, method, path, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		var reader *strings.Reader
		if body != "" {
			reader = strings.NewReader(body)
		} else {
			reader = strings.NewReader("")
		}
		req := httptest.NewRequest(method, path, reader)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		r.ServeHTTP(w, req)
		return w
	}

	// 非管理员（普通成员与非管理员版主）完全看不到已删除周报
	for _, viewer := range []*models.User{&member, &mod} {
		r := newRouter(viewer)

		// 1. 首页卡片统计与最新话题
		w := doReq(r, "GET", "/api/home", "")
		if w.Code != http.StatusOK {
			t.Fatalf("%s /api/home: %d %s", viewer.Username, w.Code, w.Body.String())
		}
		var homeRes struct {
			Cards []struct {
				Tag struct {
					Slug             string `json:"slug"`
					DiscussionsCount int    `json:"discussionsCount"`
				} `json:"tag"`
				Latest *struct {
					ID uint `json:"id"`
				} `json:"latest"`
			} `json:"cards"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &homeRes); err != nil {
			t.Fatal(err)
		}
		for _, c := range homeRes.Cards {
			if c.Tag.Slug == "weekly" {
				if c.Tag.DiscussionsCount != 1 || c.Latest == nil || c.Latest.ID != visibleDisc.ID {
					t.Fatalf("%s home weekly card leaked hidden discussion: %+v", viewer.Username, c)
				}
			}
		}

		// 2. 全站最新、周报版块列表与版块详情
		for _, path := range []string{"/api/recent", "/api/tags/weekly/discussions"} {
			wList := doReq(r, "GET", path, "")
			if wList.Code != http.StatusOK {
				t.Fatalf("%s %s: %d %s", viewer.Username, path, wList.Code, wList.Body.String())
			}
			var listRes struct {
				Items []struct {
					ID uint `json:"id"`
				} `json:"items"`
				Page struct {
					Total int `json:"total"`
				} `json:"page"`
			}
			if err := json.Unmarshal(wList.Body.Bytes(), &listRes); err != nil {
				t.Fatal(err)
			}
			if listRes.Page.Total != 1 || len(listRes.Items) != 1 || listRes.Items[0].ID != visibleDisc.ID {
				t.Fatalf("%s %s leaked hidden discussion: %+v", viewer.Username, path, listRes)
			}
		}

		wTag := doReq(r, "GET", "/api/tags/weekly", "")
		if wTag.Code != http.StatusOK || !strings.Contains(wTag.Body.String(), `"discussionsCount":1`) {
			t.Fatalf("%s /api/tags/weekly count mismatch: %d %s", viewer.Username, wTag.Code, wTag.Body.String())
		}

		// 3. 搜索与个人主页
		wSearch := doReq(r, "GET", "/api/search?q=周报", "")
		if strings.Contains(wSearch.Body.String(), hiddenDisc.Title) || strings.Contains(wSearch.Body.String(), hiddenPost.Content) {
			t.Fatalf("%s /api/search leaked hidden discussion: %s", viewer.Username, wSearch.Body.String())
		}
		wProf := doReq(r, "GET", "/api/users/member", "")
		if strings.Contains(wProf.Body.String(), hiddenDisc.Title) || strings.Contains(wProf.Body.String(), hiddenPost.Content) {
			t.Fatalf("%s /api/users/member leaked hidden discussion: %s", viewer.Username, wProf.Body.String())
		}

		// 4. 详情、回复、阅读进度、点赞、编辑均返回 404
		for _, tc := range []struct{ method, path, body string }{
			{"GET", fmt.Sprintf("/api/discussions/%d", hiddenDisc.ID), ""},
			{"POST", fmt.Sprintf("/api/discussions/%d/reply", hiddenDisc.ID), `{"content":"test"}`},
			{"POST", fmt.Sprintf("/api/discussions/%d/read-progress", hiddenDisc.ID), fmt.Sprintf(`{"postId":%d,"postNumber":1}`, hiddenPost.ID)},
			{"POST", fmt.Sprintf("/api/discussions/%d/mod", hiddenDisc.ID), `{"action":"edit_meta","title":"x"}`},
			{"POST", fmt.Sprintf("/api/discussions/%d/delete", hiddenDisc.ID), ""},
			{"GET", fmt.Sprintf("/api/posts/%d/edit", hiddenPost.ID), ""},
			{"POST", fmt.Sprintf("/api/posts/%d/edit", hiddenPost.ID), `{"content":"x"}`},
			{"POST", fmt.Sprintf("/api/posts/%d/like", hiddenPost.ID), ""},
		} {
			wAct := doReq(r, tc.method, tc.path, tc.body)
			if wAct.Code != http.StatusNotFound {
				t.Fatalf("%s %s %s: want 404, got %d (%s)", viewer.Username, tc.method, tc.path, wAct.Code, wAct.Body.String())
			}
		}

		// 5. 通知不泄露已删除讨论
		if viewer.ID == member.ID {
			wNotif := doReq(r, "GET", "/api/me/notifications", "")
			if strings.Contains(wNotif.Body.String(), hiddenDisc.Title) {
				t.Fatalf("notifications leaked hidden discussion: %s", wNotif.Body.String())
			}
			wMe := doReq(r, "GET", "/api/me", "")
			if !strings.Contains(wMe.Body.String(), `"unread":0`) {
				t.Fatalf("expected unread=0 when only notification is on hidden discussion: %s", wMe.Body.String())
			}
		}
	}

	// 管理员仍可看到已删除周报及其详情（用于后台管理与恢复）
	rAdmin := newRouter(&admin)
	for _, path := range []string{"/api/recent", "/api/tags/weekly/discussions"} {
		wList := doReq(rAdmin, "GET", path, "")
		if wList.Code != http.StatusOK || !strings.Contains(wList.Body.String(), hiddenDisc.Title) {
			t.Fatalf("admin %s missing hidden discussion: %d %s", path, wList.Code, wList.Body.String())
		}
	}
	wDetail := doReq(rAdmin, "GET", fmt.Sprintf("/api/discussions/%d", hiddenDisc.ID), "")
	if wDetail.Code != http.StatusOK || !strings.Contains(wDetail.Body.String(), `"isHidden":true`) {
		t.Fatalf("admin detail failed for hidden discussion: %d %s", wDetail.Code, wDetail.Body.String())
	}
}
