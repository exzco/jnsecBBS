package api

import (
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/exzco/bbs/internal/auth"
	"github.com/exzco/bbs/internal/markdown"
	"github.com/exzco/bbs/internal/models"
	"github.com/gin-gonic/gin"
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
		Title string    `json:"title"`
		URL   string    `json:"url"`
		At    time.Time `json:"at"`
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
		var d models.Discussion
		err := s.visibleDiscussions(u).Joins("JOIN flarum_discussion_tag dt ON dt.discussion_id = flarum_discussions.id").
			Where("dt.tag_id IN ? AND flarum_discussions.hidden_at IS NULL AND flarum_discussions.is_approved = 1", ids).
			Order("flarum_discussions.last_posted_at DESC").
			Preload("User").Preload("Tags").
			First(&d).Error
		if err == nil {
			cd.Latest = &latestInfo{
				Title: d.Title, URL: discussionURL(&d), At: d.LastPostedAtOrZero(),
			}
		}
		cards = append(cards, cd)
	}
	c.JSON(http.StatusOK, gin.H{"cards": cards})
}

// recent 全站最新讨论流（首页主栏与右侧最新栏共用）。
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
	base.Session(&gorm.Session{}).
		Preload("User").Preload("Tags").Preload("LastPostUser").
		Order("flarum_discussions.is_sticky DESC, flarum_discussions.last_posted_at DESC").
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
	pages := paginate(int(total), page, perPageDiscussions)
	var discs []models.Discussion
	base.Session(&gorm.Session{}).
		Preload("User").Preload("Tags").Preload("LastPostUser").
		Distinct("flarum_discussions.*").
		Order("flarum_discussions.is_sticky DESC, flarum_discussions.last_posted_at DESC").
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

	page := pageParam(c)
	pages := paginate(d.CommentCount, page, perPagePosts)

	// 未传 page 参数（默认直接点开帖子）：定位到未读回复页或周报末页，避免从最旧楼层往下翻
	if c.Query("page") == "" {
		if hasRead && du.LastReadPostNumber != nil && *du.LastReadPostNumber > 0 {
			lastRead := *du.LastReadPostNumber
			lastPostNum := d.CommentCount
			if d.LastPostNumber != nil && *d.LastPostNumber > 0 {
				lastPostNum = *d.LastPostNumber
			}
			if lastRead < lastPostNum {
				var countBefore int64
				s.db.Model(&models.Post{}).
					Where("discussion_id = ? AND type = 'comment' AND is_approved = 1 AND is_private = 0 AND number <= ?", d.ID, lastRead).
					Count(&countBefore)
				page = int(countBefore/perPagePosts) + 1
			} else if isWeekly && pages > 1 {
				page = pages
			}
		} else if isWeekly && pages > 1 {
			page = pages
		}
	}

	var posts []models.Post
	s.db.Where("discussion_id = ? AND type = ? AND is_approved = ? AND is_private = ?", d.ID, "comment", true, false).Order("number ASC").
		Limit(perPagePosts).Offset((page - 1) * perPagePosts).Preload("User").Find(&posts)

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
			lastPostNum := d.CommentCount
			if d.LastPostNumber != nil && *d.LastPostNumber > 0 {
				lastPostNum = *d.LastPostNumber
			}
			// 若当前访问为最后一页或周报，则直接同步已读至最新楼层
			if page >= pages && lastPostNum > maxNumber {
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

	liked := s.loadLikes(posts, u)

	isMod := u != nil && u.IsModerator()
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
	c.JSON(http.StatusOK, gin.H{
		"discussion": summary,
		"posts":      views,
		"page":       PageDTO{Page: page, Pages: pages, Total: d.CommentCount},
		"canReply":   canReply,
		"isMod":      isMod && !s.cfg.ReadOnly,
		"editorTags": editorTags,
	})
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
		if err := tx.Model(&models.Post{}).
			Where("discussion_id = ? AND type = ? AND is_approved = ? AND is_private = ?", d.ID, "comment", true, false).
			Count(&position).Error; err != nil {
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
		for _, name := range markdown.MentionedUsernames(body.Content, s.mentionResolver()) {
			var mid uint
			tx.Model(&models.User{}).Select("id").Where("lower(username) = lower(?)", name).Scan(&mid)
			notify(tx, mid, u.ID, "mention", d.ID, post.ID)
		}
		notifyDiscussionParticipants(tx, &d, u.ID, "reply", post.ID)
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

// moderate 版主操作：置顶/锁定/隐藏/改标题与标签。
func (s *Server) moderate(c *gin.Context) {
	u := s.currentUser(c)
	if !u.IsModerator() {
		fail(c, http.StatusForbidden, "该操作需要版主权限。")
		return
	}
	id, _ := parseUint(c.Param("id"))
	var d models.Discussion
	if err := s.db.First(&d, id).Error; err != nil {
		fail(c, http.StatusNotFound, "该帖子不存在。")
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
	if _, _, ok := s.loadDiscussion(c, p.DiscussionID); !ok {
		return nil, nil, false
	}
	if p.IsPrivate || p.Type != "comment" || (!p.IsApproved && !u.IsModerator()) {
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
	if p.HiddenAt != nil {
		s.db.Model(p).Update("hidden_at", nil)
	} else {
		s.db.Model(p).Update("hidden_at", time.Now())
	}
	c.JSON(http.StatusOK, gin.H{"hidden": p.HiddenAt != nil})
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
	c.JSON(http.StatusOK, gin.H{"liked": liked, "likeCount": n})
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
