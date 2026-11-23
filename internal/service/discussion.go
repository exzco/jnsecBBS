// 讨论列表、版块与用户页（首页、最新流、版块详情、贡献热力图、主页、搜索）。
package service

import (
	"log"
	"net/http"
	"time"

	"github.com/exzco/bbs/internal/auth"
	"github.com/exzco/bbs/internal/markdown"
	"github.com/exzco/bbs/internal/models"
	"github.com/exzco/bbs/internal/store"
	"gorm.io/gorm"
)

// ---------- 列表与版块 ----------

// LatestInfo 版块卡片内最后活跃话题。
type LatestInfo struct {
	ID           uint      `json:"id"`
	Title        string    `json:"title"`
	URL          string    `json:"url"`
	LastPostedAt time.Time `json:"lastPostedAt"`
	At           time.Time `json:"at"`
}

// HomeCard 首页大版块卡片（版块 + 子版块 + 最后活跃话题）。
type HomeCard struct {
	Tag      TagDTO      `json:"tag"`
	Children []TagDTO    `json:"children"`
	Latest   *LatestInfo `json:"latest"`
}

// Home 首页：大版块卡片（版块 + 子版块 + 最后活跃话题）。
func (s *Service) Home(u *models.User) []HomeCard {
	var tags []models.Tag
	s.db.Order("position ASC, id ASC").Find(&tags)
	s.FillTagPerms(tags)
	tags = auth.FilterVisibleTags(u, tags)
	counts := s.tagDiscussionCounts()
	for i := range tags {
		tags[i].DiscussionsCount = counts[tags[i].ID]
	}

	tops, byParent := groupTags(tags)
	cards := make([]HomeCard, 0, len(tops))
	for _, top := range tops {
		cd := HomeCard{Tag: s.TagDTO(&top, u), Children: []TagDTO{}}
		ids := []uint{top.ID}
		for _, ch := range byParent[top.ID] {
			cd.Children = append(cd.Children, s.TagDTO(&ch, u))
			ids = append(ids, ch.ID)
		}
		var discs []models.Discussion
		err := store.VisibleDiscussions(s.db, u).Joins("JOIN flarum_discussion_tag dt ON dt.discussion_id = flarum_discussions.id").
			Where("dt.tag_id IN ? AND flarum_discussions.hidden_at IS NULL AND flarum_discussions.is_approved = 1", ids).
			Order("flarum_discussions.last_posted_at DESC, flarum_discussions.id DESC").
			Preload("User").Preload("Tags").
			Limit(1).Find(&discs).Error
		if err == nil && len(discs) > 0 {
			d := discs[0]
			lastPosted := d.LastPostedAtOrZero()
			cd.Latest = &LatestInfo{
				ID:           d.ID,
				Title:        d.Title,
				URL:          discussionURL(&d),
				LastPostedAt: lastPosted,
				At:           lastPosted,
			}
		}
		cards = append(cards, cd)
	}
	return cards
}

// ListResult 讨论列表通用返回。
type ListResult struct {
	Items []DiscussionSummaryDTO `json:"items"`
	Page  PageDTO                `json:"page"`
}

// RecentQuery 讨论列表参数（已由 handler 从 query 提取）。
type RecentQuery struct {
	Sidebar      bool // sidebar=1：服务端排除周报版块
	StickyFirst  bool // 置顶优先（sidebar=1 或 sticky=0 时关闭）
	ExcludeSlugs []string
	Page         int
}

// Recent 全站最新讨论流。
func (s *Service) Recent(u *models.User, q RecentQuery) ListResult {
	base := store.VisibleDiscussions(s.db, u).
		Where("flarum_discussions.hidden_at IS NULL AND flarum_discussions.is_approved = 1")
	if q.Sidebar {
		base = base.Where(
			"NOT EXISTS (SELECT 1 FROM flarum_discussion_tag sdt JOIN flarum_tags st ON st.id = sdt.tag_id " +
				"WHERE sdt.discussion_id = flarum_discussions.id AND st.name LIKE '%周报%')")
	}
	if len(q.ExcludeSlugs) > 0 {
		base = base.Where(
			"NOT EXISTS (SELECT 1 FROM flarum_discussion_tag exdt JOIN flarum_tags ext ON ext.id = exdt.tag_id "+
				"WHERE exdt.discussion_id = flarum_discussions.id AND ext.slug IN ?)", q.ExcludeSlugs)
	}
	var total int64
	base.Session(&gorm.Session{}).Count(&total)

	pages := Paginate(int(total), q.Page, perPageDiscussions)
	var discs []models.Discussion
	order := "flarum_discussions.last_posted_at DESC, flarum_discussions.id DESC"
	if q.StickyFirst {
		order = "flarum_discussions.is_sticky DESC, " + order
	}
	base.Session(&gorm.Session{}).
		Preload("User").Preload("Tags").Preload("LastPostUser").
		Order(order).
		Limit(perPageDiscussions).Offset((q.Page - 1) * perPageDiscussions).Find(&discs)

	return ListResult{
		Items: s.withReadState(discs, u),
		Page:  PageDTO{Page: q.Page, Pages: pages, Total: int(total)},
	}
}

// cst 站点受众时区：贡献热力图按北京时间分桶（Flarum 库内以 UTC 存 DATETIME）。
var cst = time.FixedZone("Asia/Shanghai", 8*3600)

// ActivityDay 贡献热力图单日计数。
type ActivityDay struct {
	Date  string `json:"date"`
	Count int    `json:"count"`
}

// ActivityData 贡献热力图数据。
type ActivityData struct {
	Year  int           `json:"year"`
	Total int           `json:"total"`
	Days  []ActivityDay `json:"days"`
	Years []int         `json:"years"`
}

// UserActivity 某用户一年内发帖 + 非首帖回复按日计数。
// 年份边界按北京时间计算后转 UTC 再过滤，避免 16:00-24:00 的活动跨日错桶。
func (s *Service) UserActivity(viewer *models.User, username string, year int) (ActivityData, *Error) {
	var p models.User
	if err := s.db.Where("lower(username) = lower(?)", username).First(&p).Error; err != nil {
		return ActivityData{}, Err(http.StatusNotFound, "该用户不存在。")
	}
	now := time.Now().In(cst)
	start := time.Date(year, 1, 1, 0, 0, 0, 0, cst).UTC()
	end := time.Date(year+1, 1, 1, 0, 0, 0, 0, cst).UTC()

	canSeeHidden := viewer.IsModerator()
	dq := store.VisibleDiscussions(s.db, viewer).
		Where("flarum_discussions.user_id = ? AND flarum_discussions.created_at >= ? AND flarum_discussions.created_at < ?", p.ID, start, end)
	pq := store.VisiblePosts(s.db, viewer).
		Where("flarum_posts.user_id = ? AND flarum_posts.created_at >= ? AND flarum_posts.created_at < ? AND flarum_posts.hidden_at IS NULL AND flarum_posts.type = ? AND flarum_posts.is_approved = 1", p.ID, start, end, "comment").
		Where("NOT EXISTS (SELECT 1 FROM flarum_discussions activity_discussion WHERE activity_discussion.id = flarum_posts.discussion_id AND (activity_discussion.first_post_id = flarum_posts.id OR (COALESCE(activity_discussion.first_post_id, 0) = 0 AND flarum_posts.number = 1)))")
	if !canSeeHidden {
		dq = dq.Where("flarum_discussions.hidden_at IS NULL AND flarum_discussions.is_approved = 1")
	}
	var discussionTimes, replyTimes []time.Time
	if err := dq.Pluck("flarum_discussions.created_at", &discussionTimes).Error; err != nil {
		log.Printf("[service] userActivity 讨论查询失败 user=%d: %v", p.ID, err)
		return ActivityData{}, Err(http.StatusInternalServerError, "活动记录加载失败，请重试。")
	}
	if err := pq.Pluck("flarum_posts.created_at", &replyTimes).Error; err != nil {
		log.Printf("[service] userActivity 回复查询失败 user=%d: %v", p.ID, err)
		return ActivityData{}, Err(http.StatusInternalServerError, "活动记录加载失败，请重试。")
	}

	counts := map[string]int{}
	for _, t := range append(discussionTimes, replyTimes...) {
		counts[t.In(cst).Format("2006-01-02")]++
	}
	days := make([]ActivityDay, 0, 366)
	total := 0
	for d := time.Date(year, 1, 1, 0, 0, 0, 0, cst); d.Year() == year; d = d.AddDate(0, 0, 1) {
		key := d.Format("2006-01-02")
		n := counts[key]
		total += n
		days = append(days, ActivityDay{Date: key, Count: n})
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
	return ActivityData{Year: year, Total: total, Days: days, Years: years}, nil
}

// ListGroups 展示的组列表（含管理员/版主组）。
func (s *Service) ListGroups() []GroupDTO {
	var groups []models.Group
	s.db.Where("is_hidden = ? OR id IN ?", false, []uint{models.GroupAdmin, models.GroupMod}).
		Order("id").Find(&groups)
	return groupDTOs(groups)
}

// loadTag 按 slug 加载版块（含可见性检查）。
func (s *Service) loadTag(u *models.User, slugParam string) (*models.Tag, []models.Tag, *Error) {
	var tag models.Tag
	if err := s.db.Where("slug = ?", slugParam).First(&tag).Error; err != nil {
		return nil, nil, Err(http.StatusNotFound, "版块不存在或已被删除。")
	}
	auth.FillTagPerm(s.db, &tag, auth.LoadTagPerms(s.db))
	if !auth.CanViewTag(u, &tag) {
		return nil, nil, Err(http.StatusForbidden, "你没有查看该版块的权限。")
	}
	var children []models.Tag
	s.db.Where("parent_id = ?", tag.ID).Order("position ASC, id ASC").Find(&children)
	s.FillTagPerms(children)
	children = auth.FilterVisibleTags(u, children)
	counts := s.tagDiscussionCounts()
	tag.DiscussionsCount = counts[tag.ID]
	for i := range children {
		children[i].DiscussionsCount = counts[children[i].ID]
	}
	return &tag, children, nil
}

// TagDetailData 版块详情。
type TagDetailData struct {
	Tag     TagDTO `json:"tag"`
	CanPost bool   `json:"canPost"`
}

func (s *Service) TagDetail(u *models.User, slugParam string) (TagDetailData, *Error) {
	tag, children, err := s.loadTag(u, slugParam)
	if err != nil {
		return TagDetailData{}, err
	}
	t := s.TagDTO(tag, u)
	t.Children = make([]TagDTO, 0, len(children))
	for i := range children {
		t.Children = append(t.Children, s.TagDTO(&children[i], u))
	}
	return TagDetailData{Tag: t, CanPost: !s.cfg.ReadOnly && auth.CanPostTag(u, tag)}, nil
}

// TagDiscussions 版块内讨论列表（含子版块）。
func (s *Service) TagDiscussions(u *models.User, slugParam string, page int, all bool) (ListResult, *Error) {
	tag, children, err := s.loadTag(u, slugParam)
	if err != nil {
		return ListResult{}, err
	}
	ids := []uint{tag.ID}
	for _, ch := range children {
		ids = append(ids, ch.ID)
	}
	base := store.VisibleDiscussions(s.db, u).Joins("JOIN flarum_discussion_tag dt ON dt.discussion_id = flarum_discussions.id").
		Where("dt.tag_id IN ? AND flarum_discussions.hidden_at IS NULL AND flarum_discussions.is_approved = 1", ids)
	var total int64
	base.Session(&gorm.Session{}).Distinct("flarum_discussions.id").Count(&total)

	var pages int
	var discs []models.Discussion
	q := base.Session(&gorm.Session{}).
		Preload("User").Preload("Tags").Preload("LastPostUser").
		Distinct("flarum_discussions.*").
		Order("flarum_discussions.is_sticky DESC, flarum_discussions.last_posted_at DESC")

	if all {
		page = 1
		pages = 1
		if err := q.Find(&discs).Error; err != nil {
			return ListResult{}, Err(http.StatusInternalServerError, "获取讨论列表失败。")
		}
	} else {
		pages = Paginate(int(total), page, perPageDiscussions)
		if err := q.Limit(perPageDiscussions).Offset((page - 1) * perPageDiscussions).Find(&discs).Error; err != nil {
			return ListResult{}, Err(http.StatusInternalServerError, "获取讨论列表失败。")
		}
	}
	return ListResult{
		Items: s.withReadState(discs, u),
		Page:  PageDTO{Page: page, Pages: pages, Total: int(total)},
	}, nil
}

// PostFormGroup 发帖表单分组（父版块 + 可发子版块）。
type PostFormGroup struct {
	Parent   TagDTO   `json:"parent"`
	Children []TagDTO `json:"children"`
}

// PostForm 发帖表单：按父版块分组的可发版块。
func (s *Service) PostForm(u *models.User) []PostFormGroup {
	tags := s.PostableTags(u)
	tops, byParent := groupTags(tags)
	groups := make([]PostFormGroup, 0, len(tops))
	for _, top := range tops {
		g := PostFormGroup{Parent: s.TagDTO(&top, u), Children: []TagDTO{}}
		for _, ch := range byParent[top.ID] {
			g.Children = append(g.Children, s.TagDTO(&ch, u))
		}
		groups = append(groups, g)
	}
	return groups
}

// Preview 渲染 Markdown 预览（与发帖同一条消毒管线）。
func (s *Service) Preview(content string) string {
	html, _ := markdown.Render(content, nil)
	return html
}

// ---------- 用户主页 / 搜索 ----------

// ProfilePostRow 用户主页/搜索中的帖子行。
type ProfilePostRow struct {
	Post       PostDTO               `json:"post"`
	Discussion *DiscussionSummaryDTO `json:"discussion"`
}

// ProfileData 用户主页数据。
type ProfileData struct {
	User        *UserDTO               `json:"user"`
	RealName    string                 `json:"realName"`
	BlogURL     string                 `json:"blogUrl"`
	DiscTotal   int64                  `json:"discTotal"`
	Discussions []DiscussionSummaryDTO `json:"discussions"`
	Posts       []ProfilePostRow       `json:"posts"`
}

// Profile 用户主页：最近讨论、最近回复与扩展资料。
func (s *Service) Profile(viewer *models.User, username string) (ProfileData, *Error) {
	var p models.User
	if err := s.db.Preload("Groups").Where("lower(username) = lower(?)", username).First(&p).Error; err != nil {
		return ProfileData{}, Err(http.StatusNotFound, "该用户不存在。")
	}
	canSeeHidden := viewer.IsModerator()
	dQuery := store.VisibleDiscussions(s.db, viewer).Where("flarum_discussions.user_id = ?", p.ID)
	if !canSeeHidden {
		dQuery = dQuery.Where("flarum_discussions.hidden_at IS NULL AND flarum_discussions.is_approved = 1")
	}
	var total int64
	dQuery.Session(&gorm.Session{}).Count(&total)
	var discs []models.Discussion
	dQuery.Session(&gorm.Session{}).Preload("User").Preload("Tags").
		Order("flarum_discussions.last_posted_at DESC").Limit(10).Find(&discs)

	var posts []models.Post
	store.VisiblePosts(s.db, viewer).Where("user_id = ? AND hidden_at IS NULL AND type = ? AND is_approved = ?", p.ID, "comment", true).
		Preload("Discussion").Preload("User").
		Order("created_at DESC").Limit(10).Find(&posts)
	rows := make([]ProfilePostRow, 0, len(posts))
	for i := range posts {
		po := &posts[i]
		if po.Discussion == nil || po.Discussion.HiddenAt != nil {
			continue
		}
		sum := s.DiscussionSummary(po.Discussion, viewer)
		rows = append(rows, ProfilePostRow{Post: s.postDTO(po, nil, viewer), Discussion: &sum})
	}
	realName, blogURL := s.LoadUserProfileExtra(p.ID)
	return ProfileData{
		User:        s.UserDTO(&p),
		RealName:    realName,
		BlogURL:     blogURL,
		DiscTotal:   total,
		Discussions: s.Summaries(discs, viewer),
		Posts:       rows,
	}, nil
}

// SearchData 站内搜索结果。
type SearchData struct {
	Found       bool // 查询词达到 2 字才执行检索
	Q           string
	Discussions []DiscussionSummaryDTO
	Posts       []ProfilePostRow
}

// Search 站内搜索：标题与正文各取最近 20 条。
func (s *Service) Search(viewer *models.User, q string) SearchData {
	res := SearchData{Q: q}
	if len([]rune(q)) < 2 {
		return res
	}
	res.Found = true
	like := "%" + q + "%"
	var discs []models.Discussion
	store.VisibleDiscussions(s.db, viewer).Where("hidden_at IS NULL AND is_approved = 1 AND title LIKE ?", like).
		Preload("User").Preload("Tags").
		Order("last_posted_at DESC").Limit(20).Find(&discs)
	var posts []models.Post
	store.VisiblePosts(s.db, viewer).Where("hidden_at IS NULL AND is_approved = 1 AND content LIKE ? AND type = ?", like, "comment").
		Preload("User").Preload("Discussion").
		Order("created_at DESC").Limit(20).Find(&posts)
	res.Discussions = s.Summaries(discs, viewer)
	for i := range posts {
		po := &posts[i]
		if po.Discussion == nil || po.Discussion.HiddenAt != nil {
			continue
		}
		sum := s.DiscussionSummary(po.Discussion, viewer)
		res.Posts = append(res.Posts, ProfilePostRow{Post: s.postDTO(po, nil, viewer), Discussion: &sum})
	}
	return res
}
