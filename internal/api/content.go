package api

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/exzco/bbs/internal/auth"
	"github.com/exzco/bbs/internal/markdown"
	"github.com/exzco/bbs/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// home 首页：大版块卡片（版块 + 子版块 + 最后活跃话题）。
func (s *Server) home(c *gin.Context) {
	u := s.currentUser(c)
	var tags []models.Tag
	s.db.Order("position ASC, id ASC").Find(&tags)
	s.fillTagPerms(tags)
	tags = auth.FilterVisibleTags(u, tags)
	s.fillTagCounts(tags)

	byParent := map[uint][]models.Tag{}
	var tops []models.Tag
	for _, t := range tags {
		if t.ParentID == nil {
			tops = append(tops, t)
		} else {
			byParent[*t.ParentID] = append(byParent[*t.ParentID], t)
		}
	}
	type latestInfo struct {
		ID           uint      `json:"id"`
		Title        string    `json:"title"`
		URL          string    `json:"url"`
		LastPostedAt time.Time `json:"lastPostedAt"`
		At           time.Time `json:"at"`
	}
	type card struct {
		Tag      TagDTO      `json:"tag"`
		Children []TagDTO    `json:"children"`
		Latest   *latestInfo `json:"latest"`
	}
	cards := make([]card, 0, len(tops))
	for _, top := range tops {
		cd := card{Tag: s.tagDTO(&top, u), Children: []TagDTO{}}
		ids := []uint{top.ID}
		for _, ch := range byParent[top.ID] {
			cd.Children = append(cd.Children, s.tagDTO(&ch, u))
			ids = append(ids, ch.ID)
		}
		var discs []models.Discussion
		err := s.visibleDiscussions(u).Joins("JOIN flarum_discussion_tag dt ON dt.discussion_id = flarum_discussions.id").
			Where("dt.tag_id IN ? AND flarum_discussions.hidden_at IS NULL AND flarum_discussions.is_approved = 1", ids).
			Order("flarum_discussions.last_posted_at DESC, flarum_discussions.id DESC").
			Preload("User").Preload("Tags").
			Limit(1).Find(&discs).Error
		if err == nil && len(discs) > 0 {
			d := discs[0]
			lastPosted := d.LastPostedAtOrZero()
			cd.Latest = &latestInfo{
				ID:           d.ID,
				Title:        d.Title,
				URL:          discussionURL(&d),
				LastPostedAt: lastPosted,
				At:           lastPosted,
			}
		}
		cards = append(cards, cd)
	}
	c.JSON(http.StatusOK, gin.H{"cards": cards})
}

// recent 全站最新讨论流
// recent 讨论列表：支持 sidebar=1 自动在服务端排除周报版块，亦兼容客户端传入 exclude=slug1,slug2。
func (s *Server) recent(c *gin.Context) {
	u := s.currentUser(c)
	base := s.visibleDiscussions(u).
		Where("flarum_discussions.hidden_at IS NULL AND flarum_discussions.is_approved = 1")
	if c.Query("sidebar") == "1" {
		base = base.Where(
			"NOT EXISTS (SELECT 1 FROM flarum_discussion_tag sdt JOIN flarum_tags st ON st.id = sdt.tag_id " +
				"WHERE sdt.discussion_id = flarum_discussions.id AND st.name LIKE '%周报%')")
	}
	if ex := strings.Split(c.Query("exclude"), ","); len(ex) > 0 {
		slugs := make([]string, 0, len(ex))
		for _, s := range ex {
			if s = strings.TrimSpace(s); s != "" && len(s) <= 100 && len(slugs) < 10 {
				slugs = append(slugs, s)
			}
		}
		if len(slugs) > 0 {
			base = base.Where(
				"NOT EXISTS (SELECT 1 FROM flarum_discussion_tag exdt JOIN flarum_tags ext ON ext.id = exdt.tag_id "+
					"WHERE exdt.discussion_id = flarum_discussions.id AND ext.slug IN ?)", slugs)
		}
	}
	var total int64
	base.Session(&gorm.Session{}).Count(&total)

	page := pageParam(c)
	pages := paginate(int(total), page, perPageDiscussions)
	var discs []models.Discussion
	order := "flarum_discussions.last_posted_at DESC, flarum_discussions.id DESC"
	if c.Query("sidebar") != "1" && c.Query("sticky") != "0" {
		order = "flarum_discussions.is_sticky DESC, " + order
	}
	base.Session(&gorm.Session{}).
		Preload("User").Preload("Tags").Preload("LastPostUser").
		Order(order).
		Limit(perPageDiscussions).Offset((page - 1) * perPageDiscussions).Find(&discs)

	discIDs := make([]uint, 0, len(discs))
	for i := range discs {
		discIDs = append(discIDs, discs[i].ID)
	}
	readMap := s.loadReadMap(discIDs, u)

	items := make([]DiscussionSummaryDTO, 0, len(discs))
	for i := range discs {
		items = append(items, s.discussionSummaryWithRead(&discs[i], u, readMap))
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "page": PageDTO{Page: page, Pages: pages, Total: int(total)}})
}

// cst 站点受众时区：贡献热力图按北京时间分桶（Flarum 库内以 UTC 存 DATETIME）。
var cst = time.FixedZone("Asia/Shanghai", 8*3600)

// userActivity 贡献热力图数据：某用户一年内发帖 + 非首帖回复按日计数。
// 年份边界按北京时间计算后转 UTC 再过滤，避免 16:00-24:00 的活动跨日错桶。
func (s *Server) userActivity(c *gin.Context) {
	var p models.User
	if err := s.db.Where("lower(username) = lower(?)", c.Param("username")).First(&p).Error; err != nil {
		fail(c, http.StatusNotFound, "该用户不存在。")
		return
	}
	viewer := s.currentUser(c)
	now := time.Now().In(cst)
	year := now.Year()
	if v := c.Query("year"); v != "" {
		if n, err := parseUint(v); err == nil && n >= 2000 && int(n) <= now.Year() {
			year = int(n)
		}
	}
	start := time.Date(year, 1, 1, 0, 0, 0, 0, cst).UTC()
	end := time.Date(year+1, 1, 1, 0, 0, 0, 0, cst).UTC()

	canSeeHidden := viewer != nil && viewer.IsModerator()
	dq := s.visibleDiscussions(viewer).
		Where("flarum_discussions.user_id = ? AND flarum_discussions.created_at >= ? AND flarum_discussions.created_at < ?", p.ID, start, end)
	pq := s.visiblePosts(viewer).
		Where("flarum_posts.user_id = ? AND flarum_posts.created_at >= ? AND flarum_posts.created_at < ? AND flarum_posts.hidden_at IS NULL AND flarum_posts.type = ? AND flarum_posts.is_approved = 1", p.ID, start, end, "comment").
		Where("NOT EXISTS (SELECT 1 FROM flarum_discussions activity_discussion WHERE activity_discussion.id = flarum_posts.discussion_id AND (activity_discussion.first_post_id = flarum_posts.id OR (COALESCE(activity_discussion.first_post_id, 0) = 0 AND flarum_posts.number = 1)))")
	if !canSeeHidden {
		dq = dq.Where("flarum_discussions.hidden_at IS NULL AND flarum_discussions.is_approved = 1")
	}
	var discussionTimes, replyTimes []time.Time
	if err := dq.Pluck("flarum_discussions.created_at", &discussionTimes).Error; err != nil {
		log.Printf("[api] userActivity 讨论查询失败 user=%d: %v", p.ID, err)
		fail(c, http.StatusInternalServerError, "活动记录加载失败，请重试。")
		return
	}
	if err := pq.Pluck("flarum_posts.created_at", &replyTimes).Error; err != nil {
		log.Printf("[api] userActivity 回复查询失败 user=%d: %v", p.ID, err)
		fail(c, http.StatusInternalServerError, "活动记录加载失败，请重试。")
		return
	}

	counts := map[string]int{}
	for _, t := range append(discussionTimes, replyTimes...) {
		counts[t.In(cst).Format("2006-01-02")]++
	}
	type day struct {
		Date  string `json:"date"`
		Count int    `json:"count"`
	}
	days := make([]day, 0, 366)
	total := 0
	for d := time.Date(year, 1, 1, 0, 0, 0, 0, cst); d.Year() == year; d = d.AddDate(0, 0, 1) {
		key := d.Format("2006-01-02")
		n := counts[key]
		total += n
		days = append(days, day{Date: key, Count: n})
	}
	// 可选年份：注册年（北京时间）至今
	years := []int{}
	if j := p.JoinedAtOrZero(); !j.IsZero() {
		for y := j.In(cst).Year(); y <= now.Year(); y++ {
			if y >= 2000 {
				years = append(years, y)
			}
		}
	}
	if len(years) == 0 {
		years = []int{now.Year()}
	}
	c.JSON(http.StatusOK, gin.H{"year": year, "total": total, "days": days, "years": years})
}

// loadUserProfileExtra 读取用户扩展资料；只读镜像可能尚未建表，缺表降级为空。
func (s *Server) loadUserProfileExtra(userID uint) (realName, blogURL string) {
	var extra models.UserProfile
	if err := s.db.Where("user_id = ?", userID).First(&extra).Error; err == nil {
		return extra.RealName, extra.BlogURL
	}
	return "", ""
}

func discussionURL(d *models.Discussion) string {
	idStr := strconv.FormatUint(uint64(d.ID), 10)
	if s := d.Slug(); s != "" {
		return "/d/" + idStr + "-" + s
	}
	return "/d/" + idStr
}

// fillTagCounts 实时统计各版块的讨论数（Flarum tags 无计数列）。
func (s *Server) fillTagCounts(tags []models.Tag) {
	type row struct {
		TagID uint
		N     int
	}
	var rows []row
	s.db.Table("flarum_discussion_tag").
		Select("tag_id, COUNT(DISTINCT discussion_id) as n").
		Group("tag_id").Scan(&rows)
	m := map[uint]int{}
	for _, r := range rows {
		m[r.TagID] = r.N
	}
	for i := range tags {
		tags[i].DiscussionsCount = m[tags[i].ID]
	}
}

func (s *Server) listGroups(c *gin.Context) {
	var groups []models.Group
	s.db.Where("is_hidden = ? OR id IN ?", false, []uint{models.GroupAdmin, models.GroupMod}).
		Order("id").Find(&groups)
	out := make([]GroupDTO, 0, len(groups))
	for _, g := range groups {
		out = append(out, GroupDTO{ID: g.ID, Name: g.Name(), Color: g.Color})
	}
	c.JSON(http.StatusOK, gin.H{"groups": out})
}

// loadTag 按 slug 加载版块（含可见性检查）。
func (s *Server) loadTag(c *gin.Context) (*models.Tag, []models.Tag, *models.User, bool) {
	var tag models.Tag
	if err := s.db.Where("slug = ?", c.Param("slug")).First(&tag).Error; err != nil {
		fail(c, http.StatusNotFound, "版块不存在或已被删除。")
		return nil, nil, nil, false
	}
	u := s.currentUser(c)
	auth.FillTagPerm(s.db, &tag, auth.LoadTagPerms(s.db))
	if !auth.CanViewTag(u, &tag) {
		fail(c, http.StatusForbidden, "你没有查看该版块的权限。")
		return nil, nil, nil, false
	}
	var children []models.Tag
	s.db.Where("parent_id = ?", tag.ID).Order("position ASC, id ASC").Find(&children)
	s.fillTagPerms(children)
	children = auth.FilterVisibleTags(u, children)
	s.fillTagCounts(append([]models.Tag{tag}, children...))
	return &tag, children, u, true
}

func (s *Server) tagDetail(c *gin.Context) {
	tag, children, u, ok := s.loadTag(c)
	if !ok {
		return
	}
	t := s.tagDTO(tag, u)
	t.Children = make([]TagDTO, 0, len(children))
	for i := range children {
		t.Children = append(t.Children, s.tagDTO(&children[i], u))
	}
	c.JSON(http.StatusOK, gin.H{
		"tag":     t,
		"canPost": !s.cfg.ReadOnly && auth.CanPostTag(u, tag),
	})
}

func (s *Server) tagDiscussions(c *gin.Context) {
	tag, children, u, ok := s.loadTag(c)
	if !ok {
		return
	}
	ids := []uint{tag.ID}
	for _, ch := range children {
		ids = append(ids, ch.ID)
	}
	s.fillTagCounts(append([]models.Tag{*tag}, children...))
	base := s.visibleDiscussions(u).Joins("JOIN flarum_discussion_tag dt ON dt.discussion_id = flarum_discussions.id").
		Where("dt.tag_id IN ? AND flarum_discussions.hidden_at IS NULL AND flarum_discussions.is_approved = 1", ids)
	var total int64
	base.Session(&gorm.Session{}).Distinct("flarum_discussions.id").Count(&total)

	page := pageParam(c)
	isAll := c.Query("all") == "1"
	var pages int
	var discs []models.Discussion
	q := base.Session(&gorm.Session{}).
		Preload("User").Preload("Tags").Preload("LastPostUser").
		Distinct("flarum_discussions.*").
		Order("flarum_discussions.is_sticky DESC, flarum_discussions.last_posted_at DESC")

	if isAll {
		pages = 1
		page = 1
		if err := q.Find(&discs).Error; err != nil {
			fail(c, http.StatusInternalServerError, "获取讨论列表失败。")
			return
		}
	} else {
		pages = paginate(int(total), page, perPageDiscussions)
		if err := q.Limit(perPageDiscussions).Offset((page - 1) * perPageDiscussions).Find(&discs).Error; err != nil {
			fail(c, http.StatusInternalServerError, "获取讨论列表失败。")
			return
		}
	}

	discIDs := make([]uint, 0, len(discs))
	for i := range discs {
		discIDs = append(discIDs, discs[i].ID)
	}
	readMap := s.loadReadMap(discIDs, u)

	items := make([]DiscussionSummaryDTO, 0, len(discs))
	for i := range discs {
		items = append(items, s.discussionSummaryWithRead(&discs[i], u, readMap))
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "page": PageDTO{Page: page, Pages: pages, Total: int(total)}})
}

// postForm 发帖表单：按父版块分组的可发版块。
func (s *Server) postForm(c *gin.Context) {
	u := s.currentUser(c)
	tags := s.postableTags(u)
	var tops []models.Tag
	byParent := map[uint][]models.Tag{}
	for _, t := range tags {
		if t.ParentID == nil {
			tops = append(tops, t)
		} else {
			byParent[*t.ParentID] = append(byParent[*t.ParentID], t)
		}
	}
	type group struct {
		Parent   TagDTO   `json:"parent"`
		Children []TagDTO `json:"children"`
	}
	groups := make([]group, 0, len(tops))
	for _, top := range tops {
		g := group{Parent: s.tagDTO(&top, u), Children: []TagDTO{}}
		for _, ch := range byParent[top.ID] {
			g.Children = append(g.Children, s.tagDTO(&ch, u))
		}
		if len(g.Children) == 0 {
			g.Children = append(g.Children, s.tagDTO(&top, u))
		}
		groups = append(groups, g)
	}
	c.JSON(http.StatusOK, gin.H{"groups": groups})
}

// preview 渲染 Markdown 预览（与发帖同一条消毒管线）。
func (s *Server) preview(c *gin.Context) {
	var body struct {
		Content string `json:"content"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		fail(c, http.StatusBadRequest, "请求格式错误。")
		return
	}
	html, _ := markdown.Render(body.Content, nil)
	c.JSON(http.StatusOK, gin.H{"html": html})
}

// loadDiscussion 加载讨论并做可见性检查。
func (s *Server) loadDiscussion(c *gin.Context, id uint) (*models.Discussion, *models.User, bool) {
	var d models.Discussion
	if err := s.visibleDiscussions(s.currentUser(c)).Preload("User").Preload("Tags").First(&d, id).Error; err != nil {
		fail(c, http.StatusNotFound, "该帖子不存在。")
		return nil, nil, false
	}
	u := s.currentUser(c)
	s.fillTagPerms(d.Tags)
	for _, t := range d.Tags {
		if !auth.CanViewTag(u, &t) {
			fail(c, http.StatusForbidden, "你没有查看该帖子的权限。")
			return nil, nil, false
		}
	}
	isMod := u != nil && u.IsModerator()
	if d.HiddenAt != nil && !isMod {
		fail(c, http.StatusNotFound, "该帖子不存在或已被删除。")
		return nil, nil, false
	}
	// 首帖已被隐藏的主题对非版主不可见
	if !isMod && d.FirstPostID != nil {
		var hiddenCount int64
		s.db.Model(&models.Post{}).Where("id = ? AND hidden_at IS NOT NULL", *d.FirstPostID).Count(&hiddenCount)
		if hiddenCount > 0 {
			fail(c, http.StatusNotFound, "该帖子不存在或已被删除。")
			return nil, nil, false
		}
	}
	// flarum/approval：未审核帖仅版主可见
	if !d.IsApproved && !isMod {
		fail(c, http.StatusNotFound, "该帖子不存在或已被删除。")
		return nil, nil, false
	}
	return &d, u, true
}

func (s *Server) discussionDetail(c *gin.Context) {
	id, _ := parseUint(c.Param("id"))
	if id == 0 {
		fail(c, http.StatusNotFound, "该帖子不存在。")
		return
	}
	d, u, ok := s.loadDiscussion(c, id)
	if !ok {
		return
	}
	var du models.DiscussionUser
	hasRead := false
	if u != nil {
		if err := s.db.Where("user_id = ? AND discussion_id = ?", u.ID, d.ID).First(&du).Error; err == nil {
			hasRead = true
		}
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

	isMod := u != nil && u.IsModerator()

	postQuery := s.db.Where("discussion_id = ? AND type = ? AND is_approved = ? AND is_private = ?", d.ID, "comment", true, false)
	if !isMod {
		postQuery = postQuery.Where("hidden_at IS NULL")
	}

	var totalComments int64
	postQuery.Session(&gorm.Session{}).Model(&models.Post{}).Count(&totalComments)

	fromStart := c.Query("from_start") == "1" || c.Query("from_start") == "true"
	loadAll := c.Query("all") == "1" || c.Query("all") == "true"
	page := pageParam(c)

	// 周报初次点开：无需去数据库查询帖子所有数据，查询近30天的即可，近30天没有就查询近60天，直到有数据
	isWeeklyRecent := isWeekly && !fromStart && c.Query("page") == ""

	var posts []models.Post
	windowDays := 0
	pages := 1

	if isWeeklyRecent {
		days := 30
		now := time.Now()
		for {
			cutoff := now.AddDate(0, 0, -days)
			var countInRange int64
			postQuery.Session(&gorm.Session{}).Model(&models.Post{}).
				Where("created_at >= ?", cutoff).Count(&countInRange)
			if countInRange > 0 {
				windowDays = days
				postQuery.Session(&gorm.Session{}).
					Where("created_at >= ?", cutoff).
					Order("number ASC").
					Preload("User").
					Find(&posts)
				break
			}

			// 如果近 days 天内没有数据，检查是否已超出该讨论中最早一条有效回复的时间
			var oldestPost models.Post
			if err := postQuery.Session(&gorm.Session{}).Order("created_at ASC").First(&oldestPost).Error; err != nil {
				// 讨论内无评论帖子
				break
			}
			if cutoff.Before(oldestPost.CreatedAt) || days >= 3650 {
				// 已经回溯至最旧帖子或达到最大范围上限
				windowDays = days
				postQuery.Session(&gorm.Session{}).
					Order("number ASC").
					Preload("User").
					Find(&posts)
				break
			}
			days += 30
		}
		page = 1
		pages = 1
	} else if loadAll && !isWeekly {
		page = 1
		pages = 1
		postQuery.Session(&gorm.Session{}).Order("number ASC").Preload("User").Find(&posts)
	} else {
		pageSize := perPagePosts
		if pp, _ := strconv.Atoi(c.Query("per_page")); pp > 0 && pp <= 100 {
			pageSize = pp
		}
		pages = paginate(int(totalComments), page, pageSize)

		if c.Query("page") == "" && !fromStart {
			if hasRead && du.LastReadPostNumber != nil && *du.LastReadPostNumber > 0 {
				lastRead := *du.LastReadPostNumber
				lastPostNum := int(totalComments)
				if d.LastPostNumber != nil && *d.LastPostNumber > 0 {
					lastPostNum = *d.LastPostNumber
				}
				if lastRead < lastPostNum {
					var countBefore int64
					countQ := postQuery.Session(&gorm.Session{}).Model(&models.Post{}).Where("number <= ?", lastRead)
					countQ.Count(&countBefore)
					page = int(countBefore)/pageSize + 1
				} else if isWeekly && pages > 1 {
					page = pages
				}
			} else if isWeekly && pages > 1 {
				page = pages
			}
		}

		postQuery.Session(&gorm.Session{}).Order("number ASC").
			Limit(pageSize).Offset((page - 1) * pageSize).Preload("User").Find(&posts)
	}

	// 记录已读进度
	if u != nil && len(posts) > 0 {
		maxNumber := 0
		for _, p := range posts {
			if p.Number != nil && *p.Number > maxNumber {
				maxNumber = *p.Number
			}
		}
		if maxNumber > 0 {
			now := time.Now()
			lastPostNum := int(totalComments)
			if d.LastPostNumber != nil && *d.LastPostNumber > 0 {
				lastPostNum = *d.LastPostNumber
			}
			// 若当前访问为最后一页、全量加载或周报，则直接同步已读至最新楼层，杜绝隐藏楼层差导致的未读蓝点残留
			if (page >= pages || loadAll || isWeekly) && lastPostNum > maxNumber {
				maxNumber = lastPostNum
			}
			if hasRead {
				if du.LastReadPostNumber == nil || maxNumber > *du.LastReadPostNumber {
					du.LastReadPostNumber = &maxNumber
				}
				du.LastReadAt = &now
				s.db.Save(&du)
			} else {
				du = models.DiscussionUser{
					UserID:             u.ID,
					DiscussionID:       d.ID,
					LastReadAt:         &now,
					LastReadPostNumber: &maxNumber,
				}
				s.db.Save(&du)
			}
		}
	}

	// 记录浏览量与浏览历史（非只读模式下）
	if !s.cfg.ReadOnly {
		now := time.Now()
		threshold := now.Add(-5 * time.Minute)
		clientIP := c.ClientIP()
		if len(clientIP) > 16 {
			clientIP = clientIP[:16]
		}
		var recentCount int64
		q := s.db.Model(&models.DiscussionView{}).Where("discussion_id = ? AND visited_at >= ?", d.ID, threshold)
		if u != nil {
			q = q.Where("user_id = ?", u.ID)
		} else {
			q = q.Where("user_id IS NULL AND ip = ?", clientIP)
		}
		_ = q.Count(&recentCount).Error

		if recentCount == 0 {
			var uid *uint
			if u != nil {
				uid = &u.ID
			}
			_ = s.db.Create(&models.DiscussionView{
				UserID:       uid,
				DiscussionID: d.ID,
				IP:           clientIP,
				VisitedAt:    now,
			}).Error
			_ = s.db.Model(&models.Discussion{}).Where("id = ?", d.ID).UpdateColumn("view_count", gorm.Expr("view_count + 1")).Error
			d.ViewCount++
		} else if u != nil {
			_ = s.db.Model(&models.DiscussionView{}).
				Where("discussion_id = ? AND user_id = ?", d.ID, u.ID).
				Order("id DESC").Limit(1).
				Update("visited_at", now).Error
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
	canReply := !s.cfg.ReadOnly && u != nil && !d.IsLocked
	for _, t := range d.Tags {
		if !auth.CanReplyTag(u, &t) {
			canReply = false
		}
	}
	var editorTags []TagDTO
	if isMod {
		for _, t := range s.postableTags(u) {
			editorTags = append(editorTags, s.tagDTO(&t, u))
		}
	}
	summary := s.discussionSummaryDTO(d, u)
	summary.HasUnread = false
	viewers := s.loadDiscussionViewers(d.ID)
	c.JSON(http.StatusOK, gin.H{
		"discussion": summary,
		"posts":      views,
		"page":       PageDTO{Page: page, Pages: pages, Total: int(totalComments)},
		"canReply":   canReply,
		"isMod":      isMod && !s.cfg.ReadOnly,
		"editorTags": editorTags,
		"viewers":    viewers,
		"isWeekly":   isWeekly,
		"windowDays": windowDays,
	})
}

type viewerResult struct {
	UserID    uint   `gorm:"column:user_id"`
	VisitedAt string `gorm:"column:visited_at"`
}

func parseVisitedAt(v string) time.Time {
	for _, layout := range []string{
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
		time.RFC3339Nano,
		time.RFC3339,
	} {
		if t, err := time.Parse(layout, v); err == nil {
			return t
		}
	}
	return time.Now()
}

func (s *Server) loadDiscussionViewers(discussionID uint) []DiscussionViewerDTO {
	var rawViewers []viewerResult
	if err := s.db.Model(&models.DiscussionView{}).
		Select("user_id, MAX(visited_at) as visited_at").
		Where("discussion_id = ? AND user_id IS NOT NULL", discussionID).
		Group("user_id").
		Order("visited_at DESC").
		Limit(20).
		Scan(&rawViewers).Error; err != nil || len(rawViewers) == 0 {
		return []DiscussionViewerDTO{}
	}

	userIDs := make([]uint, len(rawViewers))
	for i, v := range rawViewers {
		userIDs[i] = v.UserID
	}
	var users []models.User
	s.db.Where("id IN ?", userIDs).Preload("Groups").Find(&users)
	userMap := make(map[uint]*models.User, len(users))
	for i := range users {
		userMap[users[i].ID] = &users[i]
	}

	viewers := make([]DiscussionViewerDTO, 0, len(rawViewers))
	for _, v := range rawViewers {
		if targetUser, ok := userMap[v.UserID]; ok {
			dto := userDTO(targetUser)
			if dto != nil {
				viewers = append(viewers, DiscussionViewerDTO{
					User:      *dto,
					VisitedAt: parseVisitedAt(v.VisitedAt),
				})
			}
		}
	}
	return viewers
}

func (s *Server) createDiscussion(c *gin.Context) {
	u := s.currentUser(c)
	var body struct {
		Title   string `json:"title"`
		Content string `json:"content"`
		TagIDs  []uint `json:"tagIds"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		fail(c, http.StatusBadRequest, "请求格式错误。")
		return
	}
	title := strings.TrimSpace(body.Title)
	content := strings.TrimSpace(body.Content)

	postable := s.postableTags(u)
	okIDs := map[uint]models.Tag{}
	for _, t := range postable {
		okIDs[t.ID] = t
	}
	var chosen []models.Tag
	seenChosen := map[uint]bool{}
	for _, id := range body.TagIDs {
		if t, ok := okIDs[id]; ok && !seenChosen[id] && len(chosen) < 5 {
			seenChosen[id] = true
			chosen = append(chosen, t)
		}
	}
	if len([]rune(title)) < 3 || len([]rune(title)) > 200 {
		fail(c, http.StatusBadRequest, "标题长度需在 3-200 字之间。")
		return
	}
	if content == "" {
		fail(c, http.StatusBadRequest, "正文不能为空。")
		return
	}
	if len(chosen) == 0 {
		fail(c, http.StatusBadRequest, "请至少选择一个你有发帖权限的版块。")
		return
	}
	now := time.Now()
	one := 1
	uid := u.ID
	// 不内嵌 Tags（GORM Create 会跟随关联对 flarum_tags 做隐式 UPSERT），
	// 改为建帖后用 Association 显式写 flarum_discussion_tag。
	d := models.Discussion{
		Title: title, CommentCount: 1, ParticipantCount: 1, PostNumberIndex: 1,
		CreatedAt: now, LastPostedAt: &now, LastPostNumber: &one,
		SlugCol: slugify(title),
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
		for _, name := range markdown.MentionedUsernames(content, s.mentionResolver()) {
			var mid uint
			tx.Model(&models.User{}).Select("id").Where("lower(username) = lower(?)", name).Scan(&mid)
			notify(tx, mid, u.ID, "mention", d.ID, post.ID)
		}
		return nil
	})
	if err != nil {
		log.Printf("[api] createDiscussion 事务失败 user=%s: %v", u.Username, err)
		fail(c, http.StatusInternalServerError, "保存失败，请重试。")
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": d.ID, "slug": d.Slug(), "url": discussionURL(&d)})
}

func (s *Server) reply(c *gin.Context) {
	id, _ := parseUint(c.Param("id"))
	loaded, u, ok := s.loadDiscussion(c, id)
	if !ok {
		return
	}
	d := *loaded
	if d.IsLocked {
		fail(c, http.StatusBadRequest, "该帖子已被锁定，无法回复。")
		return
	}
	if d.HiddenAt != nil {
		fail(c, http.StatusBadRequest, "该帖子已被隐藏。")
		return
	}
	if !d.IsApproved {
		fail(c, http.StatusBadRequest, "该帖子尚未通过审核。")
		return
	}
	for _, t := range d.Tags {
		if !auth.CanReplyTag(u, &t) {
			fail(c, http.StatusForbidden, "你没有在该版块回帖的权限。")
			return
		}
	}
	var body struct {
		Content string `json:"content"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Content) == "" {
		fail(c, http.StatusBadRequest, "回复内容不能为空。")
		return
	}
	uid := u.ID
	post := models.Post{
		DiscussionID: d.ID, UserID: &uid, Type: "comment", IsApproved: true,
	}
	post.SetMarkdown(body.Content)
	var position int64
	err := s.db.Transaction(func(tx *gorm.DB) error {
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
		posQ := tx.Model(&models.Post{}).
			Where("discussion_id = ? AND type = ? AND is_approved = ? AND is_private = ?", d.ID, "comment", true, false)
		if !u.IsModerator() {
			posQ = posQ.Where("hidden_at IS NULL")
		}
		if err := posQ.Count(&position).Error; err != nil {
			return err
		}
		if err := tx.Model(&models.User{}).Where("id = ?", u.ID).
			UpdateColumn("comment_count", gorm.Expr("comment_count + 1")).Error; err != nil {
			return err
		}
		var du models.DiscussionUser
		if err := tx.Where("user_id = ? AND discussion_id = ?", u.ID, d.ID).First(&du).Error; err == nil {
			du.LastReadPostNumber = &nextNumber
			du.LastReadAt = &now
			_ = tx.Save(&du).Error
		} else {
			du = models.DiscussionUser{
				UserID:             u.ID,
				DiscussionID:       d.ID,
				LastReadAt:         &now,
				LastReadPostNumber: &nextNumber,
			}
			_ = tx.Save(&du).Error
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
		mentionedIDs := map[uint]bool{}
		for _, name := range markdown.MentionedUsernames(body.Content, s.mentionResolver()) {
			var mid uint
			tx.Model(&models.User{}).Select("id").Where("lower(username) = lower(?)", name).Scan(&mid)
			if mid != 0 && mid != u.ID {
				mentionedIDs[mid] = true
				notify(tx, mid, u.ID, "mention", d.ID, post.ID)
			}
		}

		// 2. 仅向楼主发送 reply 通知（楼主不是回帖者本人且未收到过 mention 通知；不再全量广播给历史参与者）
		if d.UserID != nil && *d.UserID != u.ID && !mentionedIDs[*d.UserID] {
			notify(tx, *d.UserID, u.ID, "reply", d.ID, post.ID)
		}
		return nil
	})
	if err != nil {
		log.Printf("[api] reply 事务失败 discussion=%d user=%s: %v", d.ID, u.Username, err)
		fail(c, http.StatusInternalServerError, "保存失败，请重试。")
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"postId": post.ID, "number": post.PostNumber(),
		"page": (position-1)/perPagePosts + 1,
	})
}

// moderate 版主/作者操作：版主支持置顶/锁定/隐藏；讨论作者或版主支持修改标题与标签。
func (s *Server) moderate(c *gin.Context) {
	u := s.currentUser(c)
	id, _ := parseUint(c.Param("id"))
	var d models.Discussion
	if err := s.db.First(&d, id).Error; err != nil {
		fail(c, http.StatusNotFound, "该帖子不存在。")
		return
	}
	isOwner := d.UserID != nil && *d.UserID == u.ID
	isMod := u.IsModerator()
	if !isMod && !isOwner {
		fail(c, http.StatusForbidden, "您没有权限修改该讨论。")
		return
	}
	var body struct {
		Action string `json:"action"`
		Title  string `json:"title"`
		TagIDs []uint `json:"tagIds"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		fail(c, http.StatusBadRequest, "请求格式错误。")
		return
	}
	if !isMod && body.Action != "edit_meta" {
		fail(c, http.StatusForbidden, "普通用户仅支持修改标题与标签。")
		return
	}
	switch body.Action {
	case "toggle_sticky":
		if err := s.db.Model(&models.Discussion{}).Where("id = ?", d.ID).Update("is_sticky", !d.IsSticky).Error; err != nil {
			fail(c, http.StatusInternalServerError, "更新置顶状态失败。")
			return
		}
	case "toggle_lock":
		if err := s.db.Model(&models.Discussion{}).Where("id = ?", d.ID).Update("is_locked", !d.IsLocked).Error; err != nil {
			fail(c, http.StatusInternalServerError, "更新锁定状态失败。")
			return
		}
	case "toggle_hidden":
		var hiddenAt *time.Time
		if d.HiddenAt == nil {
			now := time.Now()
			hiddenAt = &now
		}
		if err := s.db.Model(&models.Discussion{}).Where("id = ?", d.ID).Update("hidden_at", hiddenAt).Error; err != nil {
			fail(c, http.StatusInternalServerError, "更新隐藏状态失败。")
			return
		}
	case "edit_meta":
		updates := map[string]any{}
		if title := strings.TrimSpace(body.Title); title != "" {
			updates["title"] = title
			updates["slug"] = slugify(title)
		}
		if len(body.TagIDs) > 0 {
			postable := s.postableTags(u)
			okIDs := map[uint]bool{}
			for _, t := range postable {
				okIDs[t.ID] = true
			}
			var cleanTagIDs []uint
			seen := map[uint]bool{}
			for _, tid := range body.TagIDs {
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
					fail(c, http.StatusInternalServerError, "标签更新失败。")
					return
				}
			}
		}
		if len(updates) > 0 {
			if err := s.db.Model(&models.Discussion{}).Where("id = ?", d.ID).Updates(updates).Error; err != nil {
				fail(c, http.StatusInternalServerError, "更新标题失败。")
				return
			}
		}
	default:
		fail(c, http.StatusBadRequest, "未知操作。")
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ---------- 帖子级操作 ----------

func (s *Server) loadPost(c *gin.Context) (*models.Post, *models.User, bool) {
	id, _ := parseUint(c.Param("id"))
	var p models.Post
	if err := s.db.First(&p, id).Error; err != nil {
		fail(c, http.StatusNotFound, "该帖子不存在。")
		return nil, nil, false
	}
	u := s.currentUser(c)
	if u == nil {
		fail(c, http.StatusUnauthorized, "请先登录。")
		return nil, nil, false
	}
	var d models.Discussion
	if err := s.visibleDiscussions(u).Preload("Tags").First(&d, p.DiscussionID).Error; err != nil {
		fail(c, http.StatusNotFound, "该帖子不存在。")
		return nil, nil, false
	}
	s.fillTagPerms(d.Tags)
	for _, t := range d.Tags {
		if !auth.CanViewTag(u, &t) {
			fail(c, http.StatusForbidden, "你没有查看该帖子的权限。")
			return nil, nil, false
		}
	}
	isMod := u.IsModerator()
	isAuthor := p.UserID != nil && *p.UserID == u.ID
	if d.HiddenAt != nil && !isMod && !isAuthor {
		fail(c, http.StatusNotFound, "该帖子不存在或已被删除。")
		return nil, nil, false
	}
	if p.IsPrivate || p.Type != "comment" || (!p.IsApproved && !isMod) {
		fail(c, http.StatusNotFound, "该帖子不存在。")
		return nil, nil, false
	}
	return &p, u, true
}

func (s *Server) postEditGet(c *gin.Context) {
	p, _, ok := s.loadPost(c)
	if !ok {
		return
	}
	u := s.currentUser(c)
	if !auth.CanEditPost(u, p) {
		fail(c, http.StatusForbidden, "你只能编辑自己的回复。")
		return
	}
	var d models.Discussion
	if err := s.db.Select("id, slug, title").First(&d, p.DiscussionID).Error; err != nil {
		fail(c, http.StatusInternalServerError, "加载失败，请重试。")
		return
	}
	// 楼层号包含系统事件和已移除的回复，不能直接用楼层号推算分页。
	var position int64
	query := s.db.Model(&models.Post{}).
		Where("discussion_id = ? AND type = ? AND is_approved = ? AND is_private = ?", d.ID, "comment", true, false)
	if !u.IsModerator() {
		query = query.Where("hidden_at IS NULL")
	}
	if p.Number != nil {
		query = query.Where("number <= ? OR number IS NULL", *p.Number)
	} else {
		query = query.Where("number IS NULL")
	}
	if err := query.Count(&position).Error; err != nil {
		fail(c, http.StatusInternalServerError, "加载失败，请重试。")
		return
	}
	page := 1
	if position > 0 {
		page = int((position-1)/perPagePosts) + 1
	}
	c.JSON(http.StatusOK, gin.H{
		"id":         p.ID,
		"page":       page,
		"content":    p.Markdown(),
		"discussion": gin.H{"id": d.ID, "slug": d.Slug(), "title": d.Title},
	})
}

func (s *Server) editPost(c *gin.Context) {
	p, _, ok := s.loadPost(c)
	if !ok {
		return
	}
	u := s.currentUser(c)
	if !auth.CanEditPost(u, p) {
		fail(c, http.StatusForbidden, "你只能编辑自己的回复。")
		return
	}
	var body struct {
		Content string `json:"content"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Content) == "" {
		fail(c, http.StatusBadRequest, "内容不能为空。")
		return
	}
	now := time.Now()
	edited := now
	p.SetMarkdown(body.Content)
	if err := s.db.Model(p).Updates(map[string]any{"edited_at": edited, "edited_user_id": u.ID, "content": p.Content}).Error; err != nil {
		fail(c, http.StatusInternalServerError, "保存失败，请重试。")
		return
	}
	authorID := uint(0)
	if p.UserID != nil {
		authorID = *p.UserID
	}
	for _, name := range markdown.MentionedUsernames(body.Content, s.mentionResolver()) {
		var uid uint
		s.db.Model(&models.User{}).Select("id").Where("lower(username) = lower(?)", name).Scan(&uid)
		notify(s.db, uid, authorID, "mention", p.DiscussionID, p.ID)
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "editedAt": edited})
}

func (s *Server) hidePost(c *gin.Context) {
	p, u, ok := s.loadPost(c)
	if !ok {
		return
	}
	if (p.UserID == nil || *p.UserID != u.ID) && !u.IsModerator() {
		fail(c, http.StatusForbidden, "无权隐藏该回复。")
		return
	}
	var newHiddenAt *time.Time
	if p.HiddenAt != nil {
		s.db.Model(&models.Post{}).Where("id = ?", p.ID).Select("hidden_at", "hidden_user_id").Updates(map[string]any{
			"hidden_at":      nil,
			"hidden_user_id": nil,
		})
	} else {
		now := time.Now()
		newHiddenAt = &now
		s.db.Model(&models.Post{}).Where("id = ?", p.ID).Select("hidden_at", "hidden_user_id").Updates(map[string]any{
			"hidden_at":      &now,
			"hidden_user_id": u.ID,
		})
	}

	// 若操作的是主楼/首帖，同步设置讨论的隐藏状态
	var d models.Discussion
	if err := s.db.Select("id", "first_post_id").First(&d, p.DiscussionID).Error; err == nil {
		if (p.Number != nil && *p.Number == 1) || (d.FirstPostID != nil && p.ID == *d.FirstPostID) {
			if newHiddenAt != nil {
				s.db.Model(&models.Discussion{}).Where("id = ?", d.ID).Select("hidden_at", "hidden_user_id").Updates(map[string]any{
					"hidden_at":      newHiddenAt,
					"hidden_user_id": u.ID,
				})
			} else {
				s.db.Model(&models.Discussion{}).Where("id = ?", d.ID).Select("hidden_at", "hidden_user_id").Updates(map[string]any{
					"hidden_at":      nil,
					"hidden_user_id": nil,
				})
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{"hidden": newHiddenAt != nil})
}

func (s *Server) toggleLike(c *gin.Context) {
	p, u, ok := s.loadPost(c)
	if !ok {
		return
	}
	var like models.PostLike
	liked := false
	if err := s.db.Where("post_id = ? AND user_id = ?", p.ID, u.ID).First(&like).Error; err == nil {
		s.db.Delete(&like)
	} else {
		if err := s.db.Create(&models.PostLike{PostID: p.ID, UserID: u.ID}).Error; err != nil {
			fail(c, http.StatusInternalServerError, "操作失败。")
			return
		}
		liked = true
		if p.UserID != nil {
			notify(s.db, *p.UserID, u.ID, "like", p.DiscussionID, p.ID)
		}
	}
	var n int64
	s.db.Model(&models.PostLike{}).Where("post_id = ?", p.ID).Count(&n)
	likers := s.loadPostLikers(p.ID)
	c.JSON(http.StatusOK, gin.H{"liked": liked, "likeCount": n, "likers": likers})
}

// ---------- 用户主页 / 搜索 ----------

func (s *Server) profile(c *gin.Context) {
	var p models.User
	if err := s.db.Preload("Groups").Where("lower(username) = lower(?)", c.Param("username")).First(&p).Error; err != nil {
		fail(c, http.StatusNotFound, "该用户不存在。")
		return
	}
	viewer := s.currentUser(c)
	canSeeHidden := viewer != nil && viewer.IsModerator()
	dQuery := s.visibleDiscussions(viewer).Where("flarum_discussions.user_id = ?", p.ID)
	if !canSeeHidden {
		dQuery = dQuery.Where("flarum_discussions.hidden_at IS NULL AND flarum_discussions.is_approved = 1")
	}
	var total int64
	dQuery.Session(&gorm.Session{}).Count(&total)
	var discs []models.Discussion
	dQuery.Session(&gorm.Session{}).Preload("User").Preload("Tags").
		Order("flarum_discussions.last_posted_at DESC").Limit(10).Find(&discs)

	var posts []models.Post
	s.visiblePosts(viewer).Where("user_id = ? AND hidden_at IS NULL AND type = ? AND is_approved = ?", p.ID, "comment", true).
		Preload("Discussion").Preload("User").
		Order("created_at DESC").Limit(10).Find(&posts)
	type postRow struct {
		Post       PostDTO               `json:"post"`
		Discussion *DiscussionSummaryDTO `json:"discussion"`
	}
	rows := make([]postRow, 0, len(posts))
	for i := range posts {
		po := &posts[i]
		if po.Discussion == nil || po.Discussion.HiddenAt != nil {
			continue
		}
		rows = append(rows, postRow{Post: s.postDTO(po, nil, viewer), Discussion: s.discussionSummaryPTR(po.Discussion, viewer)})
	}
	realName, blogURL := s.loadUserProfileExtra(p.ID)
	c.JSON(http.StatusOK, gin.H{
		"user":        userDTO(&p),
		"realName":    realName,
		"blogUrl":     blogURL,
		"discTotal":   total,
		"discussions": s.summaries(discs, viewer),
		"posts":       rows,
	})
}

func (s *Server) summaries(discs []models.Discussion, u *models.User) []DiscussionSummaryDTO {
	out := make([]DiscussionSummaryDTO, 0, len(discs))
	for i := range discs {
		out = append(out, s.discussionSummaryDTO(&discs[i], u))
	}
	return out
}

func (s *Server) discussionSummaryPTR(d *models.Discussion, u *models.User) *DiscussionSummaryDTO {
	dto := s.discussionSummaryDTO(d, u)
	return &dto
}

func (s *Server) search(c *gin.Context) {
	q := strings.TrimSpace(c.Query("q"))
	data := gin.H{"q": q}
	if len([]rune(q)) >= 2 {
		like := "%" + q + "%"
		var discs []models.Discussion
		s.visibleDiscussions(s.currentUser(c)).Where("hidden_at IS NULL AND is_approved = 1 AND title LIKE ?", like).
			Preload("User").Preload("Tags").
			Order("last_posted_at DESC").Limit(20).Find(&discs)
		var posts []models.Post
		s.visiblePosts(s.currentUser(c)).Where("hidden_at IS NULL AND is_approved = 1 AND content LIKE ? AND type = ?", like, "comment").
			Preload("User").Preload("Discussion").
			Order("created_at DESC").Limit(20).Find(&posts)
		data["discussions"] = s.summaries(discs, s.currentUser(c))
		var rows []gin.H
		for i := range posts {
			po := &posts[i]
			if po.Discussion == nil || po.Discussion.HiddenAt != nil {
				continue
			}
			rows = append(rows, gin.H{
				"post":       s.postDTO(po, nil, s.currentUser(c)),
				"discussion": s.discussionSummaryPTR(po.Discussion, s.currentUser(c)),
			})
		}
		data["posts"] = rows
	}
	c.JSON(http.StatusOK, data)
}

// UploadFile 与 flarum_fof_upload_files 兼容的文件记录模型
type UploadFile struct {
	ID           uint      `gorm:"column:id;primaryKey"`
	ActorID      uint      `gorm:"column:actor_id"`
	BaseName     string    `gorm:"column:base_name"`
	Path         string    `gorm:"column:path"`
	URL          string    `gorm:"column:url"`
	Type         string    `gorm:"column:type"`
	Size         int64     `gorm:"column:size"`
	UploadMethod string    `gorm:"column:upload_method"`
	CreatedAt    time.Time `gorm:"column:created_at"`
	UUID         string    `gorm:"column:uuid"`
	Tag          string    `gorm:"column:tag"`
}

func (UploadFile) TableName() string { return "flarum_fof_upload_files" }

// upload 处理论坛图片与附件上传
func (s *Server) upload(c *gin.Context) {
	if s.cfg.ReadOnly {
		fail(c, http.StatusForbidden, "只读模式，暂不能上传文件。")
		return
	}
	u := s.currentUser(c)
	if u == nil {
		fail(c, http.StatusUnauthorized, "请先登录。")
		return
	}
	fh, err := c.FormFile("file")
	if err != nil {
		fail(c, http.StatusBadRequest, "请选择要上传的文件。")
		return
	}
	if fh.Size > 25<<20 {
		fail(c, http.StatusBadRequest, "文件大小不能超过 25MB。")
		return
	}
	ext := strings.ToLower(filepath.Ext(fh.Filename))
	blocked := map[string]bool{
		".exe": true, ".bat": true, ".cmd": true, ".sh": true,
		".php": true, ".phtml": true, ".jsp": true, ".asp": true,
		".aspx": true, ".cgi": true, ".py": true, ".pl": true,
	}
	if blocked[ext] {
		fail(c, http.StatusBadRequest, "出于安全原因，禁止上传可执行或脚本文件。")
		return
	}

	dateDir := time.Now().Format("2006-01-02")
	destDir := filepath.Join(s.cfg.DataDir, "assets", "files", dateDir)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		fail(c, http.StatusInternalServerError, "创建存储目录失败。")
		return
	}
	hexPart, _ := randHex(6)
	savedName := fmt.Sprintf("%d-%s%s", time.Now().Unix(), hexPart, ext)
	diskPath := filepath.Join(destDir, savedName)
	if err := c.SaveUploadedFile(fh, diskPath); err != nil {
		fail(c, http.StatusInternalServerError, "保存文件失败。")
		return
	}

	relPath := dateDir + "/" + savedName
	publicURL := "/assets/files/" + relPath
	fileUUID := uuid.New().String()

	isImage := ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".gif" || ext == ".webp" || ext == ".svg" || ext == ".bmp"
	tag := "file"
	if isImage {
		tag = "image-preview"
	}

	rec := UploadFile{
		ActorID:      u.ID,
		BaseName:     fh.Filename,
		Path:         relPath,
		URL:          publicURL,
		Type:         fh.Header.Get("Content-Type"),
		Size:         fh.Size,
		UploadMethod: "local",
		CreatedAt:    time.Now(),
		UUID:         fileUUID,
		Tag:          tag,
	}
	_ = s.db.Create(&rec)

	var md string
	if isImage {
		md = fmt.Sprintf("![%s](%s)", fh.Filename, publicURL)
	} else {
		md = fmt.Sprintf("[📎 %s (%s)](%s)", fh.Filename, formatBytes(fh.Size), publicURL)
	}

	c.JSON(http.StatusOK, gin.H{
		"url":      publicURL,
		"uuid":     fileUUID,
		"name":     fh.Filename,
		"size":     fh.Size,
		"isImage":  isImage,
		"markdown": md,
	})
}

// downloadFile 兼容 Flarum fof/upload 的 UUID 附件下载
func (s *Server) downloadFile(c *gin.Context) {
	uuidParam := c.Param("uuid")
	var rec UploadFile
	if err := s.db.Where("uuid = ?", uuidParam).First(&rec).Error; err != nil {
		fail(c, http.StatusNotFound, "文件不存在。")
		return
	}
	cleanRel := filepath.Clean("/" + rec.Path)
	// 优先从 DataDir/assets/files/ 读取
	primaryPath := filepath.Join(s.cfg.DataDir, "assets", "files", cleanRel)
	if st, err := os.Stat(primaryPath); err == nil && !st.IsDir() {
		c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", rec.BaseName))
		c.File(primaryPath)
		return
	}
	// 回退至 AssetsDir/files/
	if s.cfg.AssetsDir != "" {
		fallbackPath := filepath.Join(s.cfg.AssetsDir, "files", cleanRel)
		if st, err := os.Stat(fallbackPath); err == nil && !st.IsDir() {
			c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", rec.BaseName))
			c.File(fallbackPath)
			return
		}
	}
	fail(c, http.StatusNotFound, "文件未找到。")
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
