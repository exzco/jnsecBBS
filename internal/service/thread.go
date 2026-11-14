// 讨论主题域：详情（楼层分页、已读进度、浏览量）、发帖、回帖与版主操作。
package service

import (
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/exzco/bbs/internal/auth"
	"github.com/exzco/bbs/internal/cache"
	"github.com/exzco/bbs/internal/models"
	"github.com/exzco/bbs/internal/slug"
	"github.com/exzco/bbs/internal/store"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ---------- 讨论详情 ----------

// loadDiscussion 加载讨论并做可见性检查。
func (s *Service) loadDiscussion(u *models.User, id uint) (*models.Discussion, *Error) {
	var d models.Discussion
	if err := store.VisibleDiscussions(s.db, u).Preload("User").Preload("Tags").First(&d, id).Error; err != nil {
		return nil, Err(http.StatusNotFound, "该帖子不存在。")
	}
	s.FillTagPerms(d.Tags)
	for _, t := range d.Tags {
		if !auth.CanViewTag(u, &t) {
			return nil, Err(http.StatusForbidden, "你没有查看该帖子的权限。")
		}
	}
	isMod := u.IsModerator()
	if d.HiddenAt != nil && !isMod {
		return nil, Err(http.StatusNotFound, "该帖子不存在或已被删除。")
	}
	// 首帖已被隐藏的主题对非版主不可见
	if !isMod && d.FirstPostID != nil {
		var hiddenCount int64
		s.db.Model(&models.Post{}).Where("id = ? AND hidden_at IS NOT NULL", *d.FirstPostID).Count(&hiddenCount)
		if hiddenCount > 0 {
			return nil, Err(http.StatusNotFound, "该帖子不存在或已被删除。")
		}
	}
	// flarum/approval：未审核帖仅版主可见
	if !d.IsApproved && !isMod {
		return nil, Err(http.StatusNotFound, "该帖子不存在或已被删除。")
	}
	return &d, nil
}

// DetailQuery 讨论详情参数（handler 自 path/query 提取）。
type DetailQuery struct {
	BeforeNumber int    // 向前翻页：加载 number 小于该值的楼层
	PerPage      int    // 每页楼层数（0 = 默认 15）
	Page         int    // 页码（1 起）
	HasPage      bool   // 请求是否携带 page 参数
	FromStart    bool   // from_start=1/true：从头加载
	LoadAll      bool   // all=1/true：全量加载
	ViewerIP     string // 浏览量去重用
}

// DetailData 讨论详情响应数据。
type DetailData struct {
	Discussion DiscussionSummaryDTO  `json:"discussion"`
	Posts      []PostDTO             `json:"posts"`
	Page       PageDTO               `json:"page"`
	CanReply   bool                  `json:"canReply"`
	IsMod      bool                  `json:"isMod"`
	EditorTags []TagDTO              `json:"editorTags"`
	Viewers    []DiscussionViewerDTO `json:"viewers"`
	IsWeekly   bool                  `json:"isWeekly"`
	WindowDays int                   `json:"windowDays"`
}

// saveReadProgress 单调推进用户在讨论中的已读楼层并刷新已读时间（g 可为事务句柄）。
func saveReadProgress(g *gorm.DB, userID, discussionID uint, number int) error {
	now := time.Now()
	var du models.DiscussionUser
	if err := g.Where("user_id = ? AND discussion_id = ?", userID, discussionID).First(&du).Error; err == nil {
		if du.LastReadPostNumber == nil || number > *du.LastReadPostNumber {
			du.LastReadPostNumber = &number
		}
		du.LastReadAt = &now
		return g.Save(&du).Error
	}
	du = models.DiscussionUser{
		UserID:             userID,
		DiscussionID:       discussionID,
		LastReadAt:         &now,
		LastReadPostNumber: &number,
	}
	return g.Save(&du).Error
}

// visibleComments 某讨论下对 u 可见的评论流（排除系统事件/待审/私密帖，隐藏楼层仅版主可见）。
func visibleComments(g *gorm.DB, u *models.User, discussionID uint) *gorm.DB {
	q := g.Model(&models.Post{}).Where("discussion_id = ? AND type = ? AND is_approved = ? AND is_private = ?", discussionID, "comment", true, false)
	if !u.IsModerator() {
		q = q.Where("hidden_at IS NULL")
	}
	return q
}

// DiscussionDetail 讨论详情：楼层分页、已读进度、浏览量与在线 viewers。
func (s *Service) DiscussionDetail(u *models.User, id uint, q DetailQuery) (DetailData, *Error) {
	d, err := s.loadDiscussion(u, id)
	if err != nil {
		return DetailData{}, err
	}
	var du models.DiscussionUser
	hasRead := false
	if err := s.db.Where("user_id = ? AND discussion_id = ?", u.ID, d.ID).First(&du).Error; err == nil {
		hasRead = true
	}
	isWeekly := false
	for _, t := range d.Tags {
		if strings.Contains(t.Name, "周报") || t.Slug == "weekly" {
			isWeekly = true
			break
		}
	}
	if strings.Contains(d.Title, "周报") {
		isWeekly = true
	}

	isMod := u.IsModerator()

	postQuery := visibleComments(s.db, u, d.ID)

	var totalComments int64
	postQuery.Session(&gorm.Session{}).Model(&models.Post{}).Count(&totalComments)

	page := q.Page
	pageSize := PerPagePosts
	if q.PerPage > 0 && q.PerPage <= 100 {
		pageSize = q.PerPage
	}

	// 周报初次点开：无需去数据库查询帖子所有数据，查询近30天的即可，近30天没有就查询近60天，直到有数据
	isWeeklyRecent := isWeekly && !q.FromStart && !q.HasPage && q.BeforeNumber <= 0

	// 楼层加载模式按序匹配：向前翻页 > 周报近窗 > 全量 > 常规分页（含已读定位）。
	// run 返回（楼层, 页码, 总页数, 周报窗口天数）。
	modes := []struct {
		match func() bool
		run   func() ([]models.Post, int, int, int)
	}{
		{
			match: func() bool { return q.BeforeNumber > 0 },
			run: func() ([]models.Post, int, int, int) {
				var olderPosts []models.Post
				postQuery.Session(&gorm.Session{}).
					Where("number < ?", q.BeforeNumber).
					Order("number DESC").
					Limit(pageSize).
					Preload("User").
					Find(&olderPosts)
				posts := make([]models.Post, len(olderPosts))
				for i, p := range olderPosts {
					posts[len(olderPosts)-1-i] = p
				}
				return posts, 1, 1, 0
			},
		},
		{
			match: func() bool { return isWeeklyRecent },
			run: func() ([]models.Post, int, int, int) {
				now := time.Now()
				days := 30
				var latestPost models.Post
				if err := postQuery.Session(&gorm.Session{}).Order("created_at DESC").First(&latestPost).Error; err != nil {
					return nil, 1, 1, days
				}
				if diffDays := int(now.Sub(latestPost.CreatedAt).Hours() / 24); diffDays >= 30 {
					days = (diffDays/30 + 1) * 30
				}
				var posts []models.Post
				postQuery.Session(&gorm.Session{}).
					Where("created_at >= ?", now.AddDate(0, 0, -days)).
					Order("number ASC").
					Preload("User").
					Find(&posts)
				return posts, 1, 1, days
			},
		},
		{
			match: func() bool { return q.LoadAll && !isWeekly },
			run: func() ([]models.Post, int, int, int) {
				var posts []models.Post
				postQuery.Session(&gorm.Session{}).Order("number ASC").Preload("User").Find(&posts)
				return posts, 1, 1, 0
			},
		},
		{
			match: func() bool { return true },
			run: func() ([]models.Post, int, int, int) {
				pg := q.Page
				pgs := Paginate(int(totalComments), pg, pageSize)
				// 未携带 page 参数时按已读进度定位应展示的页
				if !q.HasPage && !q.FromStart {
					if hasRead && du.LastReadPostNumber != nil && *du.LastReadPostNumber > 0 {
						lastRead := *du.LastReadPostNumber
						lastPostNum := int(totalComments)
						if d.LastPostNumber != nil && *d.LastPostNumber > 0 {
							lastPostNum = *d.LastPostNumber
						}
						if lastRead < lastPostNum {
							var countBefore int64
							postQuery.Session(&gorm.Session{}).Model(&models.Post{}).Where("number <= ?", lastRead).
								Count(&countBefore)
							pg = int(countBefore)/pageSize + 1
						} else if isWeekly && pgs > 1 {
							pg = pgs
						}
					} else if isWeekly && pgs > 1 {
						pg = pgs
					}
				}
				var posts []models.Post
				postQuery.Session(&gorm.Session{}).Order("number ASC").
					Limit(pageSize).Offset((pg - 1) * pageSize).Preload("User").Find(&posts)
				return posts, pg, pgs, 0
			},
		},
	}
	var posts []models.Post
	pages, windowDays := 1, 0
	for _, mode := range modes {
		if !mode.match() {
			continue
		}
		posts, page, pages, windowDays = mode.run()
		break
	}

	// 记录已读进度
	if len(posts) > 0 {
		maxNumber := 0
		for _, p := range posts {
			if p.Number != nil && *p.Number > maxNumber {
				maxNumber = *p.Number
			}
		}
		if maxNumber > 0 {
			lastPostNum := int(totalComments)
			if d.LastPostNumber != nil && *d.LastPostNumber > 0 {
				lastPostNum = *d.LastPostNumber
			}
			// 若当前访问为最后一页、全量加载或周报，则直接同步已读至最新楼层，杜绝隐藏楼层差导致的未读蓝点残留
			if (page >= pages || q.LoadAll || isWeekly) && lastPostNum > maxNumber {
				maxNumber = lastPostNum
			}
			if err := saveReadProgress(s.db, u.ID, d.ID, maxNumber); err != nil {
				return DetailData{}, Err(http.StatusInternalServerError, "保存失败，请重试。")
			}
		}
	}

	// 记录浏览量与浏览历史（非只读模式下）
	if !s.cfg.ReadOnly {
		now := time.Now()
		threshold := now.Add(-5 * time.Minute)
		clientIP := q.ViewerIP
		if len(clientIP) > 16 {
			clientIP = clientIP[:16]
		}
		var recentCount int64
		if err := s.db.Model(&models.DiscussionView{}).Where("discussion_id = ? AND visited_at >= ? AND user_id = ?", d.ID, threshold, u.ID).
			Count(&recentCount).Error; err != nil {
			return DetailData{}, Err(http.StatusInternalServerError, "操作失败，请重试。")
		}

		if recentCount == 0 {
			uid := &u.ID
			if err := s.db.Create(&models.DiscussionView{
				UserID:       uid,
				DiscussionID: d.ID,
				IP:           clientIP,
				VisitedAt:    now,
			}).Error; err != nil {
				return DetailData{}, Err(http.StatusInternalServerError, "操作失败，请重试。")
			}
			if err := s.db.Model(&models.Discussion{}).Where("id = ?", d.ID).UpdateColumn("view_count", gorm.Expr("view_count + 1")).Error; err != nil {
				return DetailData{}, Err(http.StatusInternalServerError, "操作失败，请重试。")
			}
			d.ViewCount++
		} else if err := s.db.Model(&models.DiscussionView{}).
			Where("discussion_id = ? AND user_id = ?", d.ID, u.ID).
			Order("id DESC").Limit(1).
			Update("visited_at", now).Error; err != nil {
			return DetailData{}, Err(http.StatusInternalServerError, "操作失败，请重试。")
		}
	}

	liked := s.loadLikes(posts, u)

	views := make([]PostDTO, 0, len(posts))
	for i := range posts {
		if posts[i].HiddenAt != nil && !isMod {
			continue
		}
		dto := s.postDTO(&posts[i], liked, u)
		dto.Discussion.Slug = d.Slug()
		views = append(views, dto)
	}
	canReply := !s.cfg.ReadOnly && !d.IsLocked
	for _, t := range d.Tags {
		if !auth.CanReplyTag(u, &t) {
			canReply = false
		}
	}
	editorTags := make([]TagDTO, 0)
	if isMod {
		for _, t := range s.PostableTags(u) {
			editorTags = append(editorTags, s.TagDTO(&t, u))
		}
	}
	summary := s.DiscussionSummary(d, u)
	summary.HasUnread = false
	return DetailData{
		Discussion: summary,
		Posts:      views,
		Page:       PageDTO{Page: page, Pages: pages, Total: int(totalComments)},
		CanReply:   canReply,
		IsMod:      isMod && !s.cfg.ReadOnly,
		EditorTags: editorTags,
		Viewers:    s.loadDiscussionViewers(d.ID),
		IsWeekly:   isWeekly,
		WindowDays: windowDays,
	}, nil
}

// ---------- 发帖 / 回复 ----------

// CreateDiscussionResult 发帖成功返回。
type CreateDiscussionResult struct {
	ID   uint   `json:"id"`
	Slug string `json:"slug"`
	URL  string `json:"url"`
}

// CreateDiscussion 建讨论与首帖（单事务），并同步冗余计数与 @提及 通知。
func (s *Service) CreateDiscussion(u *models.User, title, content string, tagIDs []uint) (CreateDiscussionResult, *Error) {
	title = strings.TrimSpace(title)
	content = strings.TrimSpace(content)

	postable := s.PostableTags(u)
	okIDs := map[uint]models.Tag{}
	for _, t := range postable {
		okIDs[t.ID] = t
	}
	var chosen []models.Tag
	seenChosen := map[uint]bool{}
	for _, id := range tagIDs {
		if t, ok := okIDs[id]; ok && !seenChosen[id] && len(chosen) < 5 {
			seenChosen[id] = true
			chosen = append(chosen, t)
		}
	}
	if len([]rune(title)) < 3 || len([]rune(title)) > 200 {
		return CreateDiscussionResult{}, Err(http.StatusBadRequest, "标题长度需在 3-200 字之间。")
	}
	if content == "" {
		return CreateDiscussionResult{}, Err(http.StatusBadRequest, "正文不能为空。")
	}
	if len(chosen) == 0 {
		return CreateDiscussionResult{}, Err(http.StatusBadRequest, "请至少选择一个你有发帖权限的版块。")
	}
	now := time.Now()
	one := 1
	uid := u.ID
	// 不内嵌 Tags（GORM Create 会跟随关联对 flarum_tags 做隐式 UPSERT），
	// 改为建帖后用 Association 显式写 flarum_discussion_tag。
	d := models.Discussion{
		Title: title, CommentCount: 1, ParticipantCount: 1, PostNumberIndex: 1,
		CreatedAt: now, LastPostedAt: &now, LastPostNumber: &one,
		SlugCol: slug.Make(title),
		UserID:  &uid, LastPostUserID: &uid, IsApproved: true,
	}
	post := models.Post{Number: &one, UserID: &uid, CreatedAt: now, Type: "comment", IsApproved: true}
	post.SetMarkdown(content)
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&d).Error; err != nil {
			return err
		}
		post.DiscussionID = d.ID
		if err := tx.Create(&post).Error; err != nil {
			return err
		}
		if err := tx.Model(&d).Association("Tags").Append(&chosen); err != nil {
			return err
		}
		if err := tx.Model(&models.Discussion{}).Where("id = ?", d.ID).
			Updates(map[string]any{"first_post_id": post.ID, "last_post_id": post.ID}).Error; err != nil {
			return err
		}
		if err := tx.Model(&models.User{}).Where("id = ?", u.ID).
			UpdateColumn("discussion_count", gorm.Expr("discussion_count + 1")).Error; err != nil {
			return err
		}
		// 同步版块冗余计数（PHP 端语义）
		for _, t := range chosen {
			if err := tx.Model(&models.Tag{}).Where("id = ?", t.ID).Updates(map[string]any{
				"discussion_count":          gorm.Expr("discussion_count + 1"),
				"last_posted_at":            now,
				"last_posted_discussion_id": d.ID,
				"last_posted_user_id":       uid,
			}).Error; err != nil {
				return err
			}
		}
		if _, err := s.notifyMentions(tx, content, u.ID, d.ID, post.ID); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		log.Printf("[service] createDiscussion 事务失败 user=%s: %v", u.Username, err)
		return CreateDiscussionResult{}, Err(http.StatusInternalServerError, "保存失败，请重试。")
	}
	// 新讨论改动版块讨论计数，显式失效 tag:counts:all（Cache-Aside 写路径）。
	cache.Default.Delete(cache.TagCountsKey())
	return CreateDiscussionResult{ID: d.ID, Slug: d.Slug(), URL: discussionURL(&d)}, nil
}

// ReplyResult 回帖成功返回。
type ReplyResult struct {
	PostID uint `json:"postId"`
	Number int  `json:"number"`
	Page   int  `json:"page"`
}

// Reply 回帖：锁定主题行串行分配楼层号，单事务维护计数与通知。
func (s *Service) Reply(u *models.User, discussionID uint, content string) (ReplyResult, *Error) {
	d, err := s.loadDiscussion(u, discussionID)
	if err != nil {
		return ReplyResult{}, err
	}
	if d.IsLocked {
		return ReplyResult{}, Err(http.StatusBadRequest, "该帖子已被锁定，无法回复。")
	}
	if d.HiddenAt != nil {
		return ReplyResult{}, Err(http.StatusBadRequest, "该帖子已被隐藏。")
	}
	if !d.IsApproved {
		return ReplyResult{}, Err(http.StatusBadRequest, "该帖子尚未通过审核。")
	}
	for _, t := range d.Tags {
		if !auth.CanReplyTag(u, &t) {
			return ReplyResult{}, Err(http.StatusForbidden, "你没有在该版块回帖的权限。")
		}
	}
	uid := u.ID
	post := models.Post{
		DiscussionID: d.ID, UserID: &uid, Type: "comment", IsApproved: true,
	}
	post.SetMarkdown(content)
	var position int64
	if txErr := s.db.Transaction(func(tx *gorm.DB) error {
		// 先锁主题再读取楼层，串行分配同一主题的回复；请求预加载的计数可能已过期。
		var current models.Discussion
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("id", "post_number_index").First(&current, d.ID).Error; err != nil {
			return err
		}
		var maxNumber int
		// 系统事件、隐藏/待审/私密帖也会占用唯一楼层号，不能按评论数量分配。
		if err := tx.Model(&models.Post{}).Where("discussion_id = ?", d.ID).
			Select("COALESCE(MAX(number), 0)").Scan(&maxNumber).Error; err != nil {
			return err
		}
		nextNumber := max(current.PostNumberIndex, maxNumber) + 1
		now := time.Now()
		post.Number, post.CreatedAt = &nextNumber, now
		if err := tx.Create(&post).Error; err != nil {
			return err
		}
		// 评论计数与楼层索引分开维护；保留已删除楼层，且修复落后的历史索引。
		if err := tx.Model(&models.Discussion{}).Where("id = ?", d.ID).Updates(map[string]any{
			"comment_count":       gorm.Expr("comment_count + 1"),
			"post_number_index":   nextNumber,
			"last_posted_at":      now,
			"last_post_id":        post.ID,
			"last_post_number":    nextNumber,
			"last_posted_user_id": uid,
		}).Error; err != nil {
			return err
		}
		// 分页按详情接口实际查询的评论位置计算，楼层空隙不会生成空白页。
		posQ := visibleComments(tx, u, d.ID)
		if err := posQ.Count(&position).Error; err != nil {
			return err
		}
		if err := tx.Model(&models.User{}).Where("id = ?", u.ID).
			UpdateColumn("comment_count", gorm.Expr("comment_count + 1")).Error; err != nil {
			return err
		}
		// 已读楼层推进在事务内落盘，失败随事务回滚。
		if err := saveReadProgress(tx, u.ID, d.ID, nextNumber); err != nil {
			return err
		}
		// 同步版块冗余计数
		for _, t := range d.Tags {
			if err := tx.Model(&models.Tag{}).Where("id = ?", t.ID).Updates(map[string]any{
				"last_posted_at":            now,
				"last_posted_discussion_id": d.ID,
				"last_posted_user_id":       uid,
			}).Error; err != nil {
				return err
			}
		}
		// 1. 发送 @ 提及通知（含 @"用户名"#p123 楼层引用及普通 @用户名）
		mentionedIDs, err := s.notifyMentions(tx, content, u.ID, d.ID, post.ID)
		if err != nil {
			return err
		}

		// 2. 仅向楼主发送 reply 通知（楼主不是回帖者本人且未收到过 mention 通知；不再全量广播给历史参与者）
		if d.UserID != nil && *d.UserID != u.ID && !mentionedIDs[*d.UserID] {
			if err := notify(tx, *d.UserID, u.ID, "reply", d.ID, post.ID); err != nil {
				return err
			}
			// 邮件通知楼主：outbox 行与本事务同库落库，由后台任务投递。
			if err := s.enqueueReplyEmail(tx, u, d, nextNumber, content); err != nil {
				return err
			}
		}
		return nil
	}); txErr != nil {
		log.Printf("[service] reply 事务失败 discussion=%d user=%s: %v", d.ID, u.Username, txErr)
		return ReplyResult{}, Err(http.StatusInternalServerError, "保存失败，请重试。")
	}
	return ReplyResult{
		PostID: post.ID, Number: post.PostNumber(),
		Page: (int(position)-1)/PerPagePosts + 1,
	}, nil
}

// ---------- 版主 / 作者操作 ----------

// moderateToggles 版主开关操作分发表：按当前状态翻转列值；新增开关只需追加一行。
var moderateToggles = map[string]func(*models.Discussion) (column string, value any, failMsg string){
	"toggle_sticky": func(d *models.Discussion) (string, any, string) {
		return "is_sticky", !d.IsSticky, "更新置顶状态失败。"
	},
	"toggle_lock": func(d *models.Discussion) (string, any, string) {
		return "is_locked", !d.IsLocked, "更新锁定状态失败。"
	},
	"toggle_hidden": func(d *models.Discussion) (string, any, string) {
		if d.HiddenAt == nil {
			return "hidden_at", time.Now(), "更新隐藏状态失败。"
		}
		return "hidden_at", nil, "更新隐藏状态失败。"
	},
}

// Moderate 版主/作者操作：版主支持置顶/锁定/隐藏；讨论作者或版主支持修改标题与标签。
func (s *Service) Moderate(u *models.User, id uint, action, title string, tagIDs []uint) *Error {
	var d models.Discussion
	if err := s.db.First(&d, id).Error; err != nil {
		return Err(http.StatusNotFound, "该帖子不存在。")
	}
	isOwner := d.UserID != nil && *d.UserID == u.ID
	isMod := u.IsModerator()
	if !isMod && !isOwner {
		return Err(http.StatusForbidden, "您没有权限修改该讨论。")
	}
	if !isMod && action != "edit_meta" {
		return Err(http.StatusForbidden, "普通用户仅支持修改标题与标签。")
	}
	if toggle, ok := moderateToggles[action]; ok {
		column, value, failMsg := toggle(&d)
		if err := s.db.Model(&models.Discussion{}).Where("id = ?", d.ID).Update(column, value).Error; err != nil {
			return Err(http.StatusInternalServerError, failMsg)
		}
		return nil
	}
	if action == "edit_meta" {
		updates := map[string]any{}
		if title = strings.TrimSpace(title); title != "" {
			updates["title"] = title
			updates["slug"] = slug.Make(title)
		}
		if len(tagIDs) > 0 {
			postable := s.PostableTags(u)
			okIDs := map[uint]bool{}
			for _, t := range postable {
				okIDs[t.ID] = true
			}
			var cleanTagIDs []uint
			seen := map[uint]bool{}
			for _, tid := range tagIDs {
				if okIDs[tid] && !seen[tid] {
					seen[tid] = true
					cleanTagIDs = append(cleanTagIDs, tid)
				}
			}
			if len(cleanTagIDs) > 0 {
				err := s.db.Transaction(func(tx *gorm.DB) error {
					if err := tx.Exec("DELETE FROM flarum_discussion_tag WHERE discussion_id = ?", d.ID).Error; err != nil {
						return err
					}
					for _, tid := range cleanTagIDs {
						if err := tx.Exec("INSERT INTO flarum_discussion_tag (discussion_id, tag_id) VALUES (?, ?)", d.ID, tid).Error; err != nil {
							return err
						}
					}
					return nil
				})
				if err != nil {
					return Err(http.StatusInternalServerError, "标签更新失败。")
				}
				// 标签归属改动版块讨论计数，显式失效 tag:counts:all（Cache-Aside 写路径）。
				cache.Default.Delete(cache.TagCountsKey())
			}
		}
		if len(updates) > 0 {
			if err := s.db.Model(&models.Discussion{}).Where("id = ?", d.ID).Updates(updates).Error; err != nil {
				return Err(http.StatusInternalServerError, "更新标题失败。")
			}
		}
		return nil
	}
	return Err(http.StatusBadRequest, "未知操作。")
}
