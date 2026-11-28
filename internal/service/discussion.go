// 讨论主题域：列表、版块、用户页、搜索、详情分页、发帖、回帖与版主操作。
package service

import (
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/exzco/bbs/internal/models"
	"github.com/exzco/bbs/internal/util"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	perPageDiscussions = 20
	// PerPagePosts 讨论详情每页楼层数（测试与列表分页共用）。
	PerPagePosts = 15
)

// PageDTO 分页信息。
type PageDTO struct {
	Page  int `json:"page"`
	Pages int `json:"pages"`
	Total int `json:"total"`
}

// Paginate 计算总页数并把越界页码收敛到最后一页。
func Paginate(total, page, perPage int) (pages int) {
	pages = (total + perPage - 1) / perPage
	if pages < 1 {
		pages = 1
	}
	if page > pages {
		page = pages
	}
	return pages
}

// canViewHidden 仅管理员可查看已删除（hidden_at IS NOT NULL）的讨论。
func canViewHidden(u *models.User) bool {
	return u != nil && u.IsAdmin()
}

// visibleDiscussions 返回对当前用户可见的讨论查询（非管理员过滤已删除讨论）。
func (s *Service) visibleDiscussions(u *models.User) *gorm.DB {
	q := s.db.Model(&models.Discussion{})
	if !canViewHidden(u) {
		q = q.Where("flarum_discussions.hidden_at IS NULL")
	}
	return q
}

// visibleDiscussionPosts 返回归属讨论对当前用户可见的帖子查询（非管理员过滤已删除讨论下的帖子）。
func (s *Service) visibleDiscussionPosts(u *models.User) *gorm.DB {
	q := s.db.Model(&models.Post{})
	if !canViewHidden(u) {
		q = q.Where("EXISTS (SELECT 1 FROM flarum_discussions d WHERE d.id = flarum_posts.discussion_id AND d.hidden_at IS NULL)")
	}
	return q
}

// canDeleteDiscussion 仅讨论作者本人或版主可删除。
func canDeleteDiscussion(u *models.User, d *models.Discussion) bool {
	if u == nil {
		return false
	}
	return u.IsModerator() || (d.UserID != nil && *d.UserID == u.ID)
}

// ---------- 版块与讨论视图 ----------

type TagDTO struct {
	ID               uint     `json:"id"`
	Name             string   `json:"name"`
	Slug             string   `json:"slug"`
	Description      string   `json:"description"`
	Color            string   `json:"color"`
	Position         int      `json:"position"`
	ParentID         *uint    `json:"parentId"`
	DiscussionsCount int      `json:"discussionsCount"`
	CanPost          bool     `json:"canPost"`
	CanView          bool     `json:"canView"`
	Children         []TagDTO `json:"children,omitempty"`
}

// TagDTO 组装版块视图。
func (s *Service) TagDTO(t *models.Tag, _ *models.User) TagDTO {
	return TagDTO{
		ID: t.ID, Name: t.Name, Slug: t.Slug, Description: t.Description,
		Color: t.Color, Position: derefInt(t.Position), ParentID: t.ParentID,
		DiscussionsCount: t.DiscussionsCount,
		CanPost:          !s.cfg.ReadOnly,
		CanView:          true,
	}
}

type DiscussionSummaryDTO struct {
	ID           uint      `json:"id"`
	Title        string    `json:"title"`
	Slug         string    `json:"slug"`
	Tags         []TagDTO  `json:"tags"`
	Author       *UserDTO  `json:"author"`
	CommentCount int       `json:"commentCount"`
	ViewCount    int       `json:"viewCount"`
	CreatedAt    time.Time `json:"createdAt"`
	LastPostedAt time.Time `json:"lastPostedAt"`
	LastPoster   string    `json:"lastPoster,omitempty"`
	IsSticky     bool      `json:"isSticky"`
	IsLocked     bool      `json:"isLocked"`
	IsHidden     bool      `json:"isHidden"`
	HasUnread    bool      `json:"hasUnread"`
}

type DiscussionViewerDTO struct {
	User      UserDTO   `json:"user"`
	VisitedAt time.Time `json:"visitedAt"`
}

// DiscussionSummary 组装讨论摘要视图。
func (s *Service) DiscussionSummary(d *models.Discussion, u *models.User) DiscussionSummaryDTO {
	tags := make([]TagDTO, 0, len(d.Tags))
	seenTag := make(map[uint]bool, len(d.Tags))
	for i := range d.Tags {
		if !seenTag[d.Tags[i].ID] {
			seenTag[d.Tags[i].ID] = true
			tags = append(tags, s.TagDTO(&d.Tags[i], u))
		}
	}
	dto := DiscussionSummaryDTO{
		ID: d.ID, Title: d.Title, Slug: d.Slug(), Tags: tags,
		Author:       s.UserDTO(&d.User),
		CommentCount: d.CommentCount,
		ViewCount:    d.ViewCount,
		CreatedAt:    d.CreatedAt, LastPostedAt: d.LastPostedAtOrZero(),
		IsSticky: d.IsSticky, IsLocked: d.IsLocked, IsHidden: d.HiddenAt != nil,
	}
	if d.LastPostUser != nil {
		dto.LastPoster = d.LastPostUser.Username
	} else if d.User.Username != "" {
		dto.LastPoster = d.User.Username
	}
	return dto
}

// Summaries 批量组装讨论摘要视图。
func (s *Service) Summaries(discs []models.Discussion, u *models.User) []DiscussionSummaryDTO {
	out := make([]DiscussionSummaryDTO, 0, len(discs))
	for i := range discs {
		out = append(out, s.DiscussionSummary(&discs[i], u))
	}
	return out
}

func (s *Service) loadReadMap(discIDs []uint, u *models.User) map[uint]int {
	if u == nil || len(discIDs) == 0 {
		return nil
	}
	var rows []models.DiscussionUser
	s.db.Where("user_id = ? AND discussion_id IN ?", u.ID, discIDs).Find(&rows)
	m := make(map[uint]int, len(rows))
	for _, r := range rows {
		if r.LastReadPostNumber != nil {
			m[r.DiscussionID] = *r.LastReadPostNumber
		}
	}
	return m
}

func (s *Service) withReadState(discs []models.Discussion, u *models.User) []DiscussionSummaryDTO {
	discIDs := make([]uint, 0, len(discs))
	for i := range discs {
		discIDs = append(discIDs, discs[i].ID)
	}
	readMap := s.loadReadMap(discIDs, u)
	items := make([]DiscussionSummaryDTO, 0, len(discs))
	for i := range discs {
		dto := s.DiscussionSummary(&discs[i], u)
		if u != nil && readMap != nil {
			lastPostNum := discs[i].CommentCount
			if discs[i].LastPostNumber != nil && *discs[i].LastPostNumber > 0 {
				lastPostNum = *discs[i].LastPostNumber
			}
			if lastRead, hasRead := readMap[discs[i].ID]; !hasRead || lastRead < lastPostNum {
				dto.HasUnread = true
			}
		}
		items = append(items, dto)
	}
	return items
}

// tagDiscussionCounts 各版块讨论数（实时统计，Flarum tags 无可靠计数列）。
func (s *Service) tagDiscussionCounts(u *models.User) map[uint]int {
	type row struct {
		TagID uint
		N     int
	}
	var rows []row
	q := s.db.Model(&models.DiscussionTag{}).
		Joins("JOIN flarum_discussions d ON d.id = " + models.TableDiscussionTag + ".discussion_id")
	if !canViewHidden(u) {
		q = q.Where("d.hidden_at IS NULL")
	}
	q.Select("tag_id, COUNT(DISTINCT discussion_id) as n").
		Group("tag_id").Scan(&rows)
	counts := make(map[uint]int, len(rows))
	for _, r := range rows {
		counts[r.TagID] = r.N
	}
	return counts
}

// PostableTags 全部可选版块（所有登录用户均可发帖）。
func (s *Service) PostableTags() []models.Tag {
	var tags []models.Tag
	s.db.Order("position ASC, id ASC").Find(&tags)
	return tags
}

// groupTags 按父版块分组并返回顶级版块（保持原顺序）。
func groupTags(tags []models.Tag) (tops []models.Tag, byParent map[uint][]models.Tag) {
	byParent = map[uint][]models.Tag{}
	for _, t := range tags {
		if t.ParentID == nil {
			tops = append(tops, t)
		} else {
			byParent[*t.ParentID] = append(byParent[*t.ParentID], t)
		}
	}
	return tops, byParent
}

func discussionURL(d *models.Discussion) string {
	idStr := strconv.FormatUint(uint64(d.ID), 10)
	if s := d.Slug(); s != "" {
		return "/d/" + idStr + "-" + s
	}
	return "/d/" + idStr
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

func (s *Service) loadDiscussionViewers(discussionID uint) []DiscussionViewerDTO {
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
	userMap := s.usersByIDs(userIDs)

	viewers := make([]DiscussionViewerDTO, 0, len(rawViewers))
	for _, v := range rawViewers {
		if targetUser, ok := userMap[v.UserID]; ok {
			dto := s.UserDTO(targetUser)
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

// ---------- 首页 / 列表 / 版块 / 用户页 / 搜索 ----------

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
	counts := s.tagDiscussionCounts(u)
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
		err := s.visibleDiscussions(u).Joins("JOIN "+models.TableDiscussionTag+" dt ON dt.discussion_id = flarum_discussions.id").
			Where("dt.tag_id IN ?", ids).
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
	var excluded []string
	for _, slug := range q.ExcludeSlugs {
		if slug = strings.TrimSpace(slug); slug != "" && len(slug) <= 100 && len(excluded) < 10 {
			excluded = append(excluded, slug)
		}
	}
	base := s.visibleDiscussions(u)
	if q.Sidebar {
		base = base.Where(
			"NOT EXISTS (SELECT 1 FROM " + models.TableDiscussionTag + " sdt JOIN flarum_tags st ON st.id = sdt.tag_id " +
				"WHERE sdt.discussion_id = flarum_discussions.id AND st.name LIKE '%周报%')")
	}
	if len(excluded) > 0 {
		base = base.Where(
			"NOT EXISTS (SELECT 1 FROM "+models.TableDiscussionTag+" exdt JOIN flarum_tags ext ON ext.id = exdt.tag_id "+
				"WHERE exdt.discussion_id = flarum_discussions.id AND ext.slug IN ?)", excluded)
	}
	var total int64
	base.Session(&gorm.Session{}).Count(&total)

	pages := Paginate(int(total), q.Page, perPageDiscussions)
	var discs []models.Discussion
	order := "flarum_discussions.last_posted_at DESC, flarum_discussions.id DESC"
	if q.StickyFirst && !q.Sidebar {
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
func (s *Service) UserActivity(_ *models.User, username string, year int) (ActivityData, *Error) {
	now := time.Now()
	if year < 2000 || year > now.Year() {
		year = now.Year()
	}
	var p models.User
	if err := s.db.Where("lower(username) = lower(?)", username).First(&p).Error; err != nil {
		return ActivityData{}, Err(http.StatusNotFound, "该用户不存在。")
	}
	now = now.In(cst)
	start := time.Date(year, 1, 1, 0, 0, 0, 0, cst).UTC()
	end := time.Date(year+1, 1, 1, 0, 0, 0, 0, cst).UTC()

	dq := s.db.Model(&models.Discussion{}).
		Where("flarum_discussions.user_id = ? AND flarum_discussions.created_at >= ? AND flarum_discussions.created_at < ?", p.ID, start, end)
	pq := s.db.Model(&models.Post{}).
		Where("flarum_posts.user_id = ? AND flarum_posts.created_at >= ? AND flarum_posts.created_at < ? AND flarum_posts.type = ?", p.ID, start, end, "comment").
		Where("NOT EXISTS (SELECT 1 FROM flarum_discussions activity_discussion WHERE activity_discussion.id = flarum_posts.discussion_id AND (activity_discussion.first_post_id = flarum_posts.id OR (COALESCE(activity_discussion.first_post_id, 0) = 0 AND flarum_posts.number = 1)))")
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

func (s *Service) loadTag(u *models.User, slugParam string) (*models.Tag, []models.Tag, *Error) {
	var tag models.Tag
	if err := s.db.Where("slug = ?", slugParam).First(&tag).Error; err != nil {
		return nil, nil, Err(http.StatusNotFound, "版块不存在或已被删除。")
	}
	var children []models.Tag
	s.db.Where("parent_id = ?", tag.ID).Order("position ASC, id ASC").Find(&children)
	counts := s.tagDiscussionCounts(u)
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
	return TagDetailData{Tag: t, CanPost: !s.cfg.ReadOnly}, nil
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
	base := s.visibleDiscussions(u).Joins("JOIN "+models.TableDiscussionTag+" dt ON dt.discussion_id = flarum_discussions.id").
		Where("dt.tag_id IN ?", ids)
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
	tags := s.PostableTags()
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
func (s *Service) Preview(source string) string {
	html, _ := util.Render(source, nil)
	return html
}

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
	dQuery := s.visibleDiscussions(viewer).Where("flarum_discussions.user_id = ?", p.ID)
	var total int64
	dQuery.Session(&gorm.Session{}).Count(&total)
	var discs []models.Discussion
	dQuery.Session(&gorm.Session{}).Preload("User").Preload("Tags").
		Order("flarum_discussions.last_posted_at DESC").Limit(10).Find(&discs)

	var posts []models.Post
	s.visibleDiscussionPosts(viewer).Where("user_id = ? AND type = ?", p.ID, "comment").
		Preload("Discussion").Preload("User").
		Order("created_at DESC").Limit(10).Find(&posts)
	rows := make([]ProfilePostRow, 0, len(posts))
	for i := range posts {
		po := &posts[i]
		if po.Discussion == nil {
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
	q = strings.TrimSpace(q)
	res := SearchData{Q: q}
	if len([]rune(q)) < 2 {
		return res
	}
	res.Found = true
	like := "%" + q + "%"
	var discs []models.Discussion
	s.visibleDiscussions(viewer).Where("title LIKE ?", like).
		Preload("User").Preload("Tags").
		Order("last_posted_at DESC").Limit(20).Find(&discs)
	var posts []models.Post
	s.visibleDiscussionPosts(viewer).Where("content LIKE ? AND type = ?", like, "comment").
		Preload("User").Preload("Discussion").
		Order("created_at DESC").Limit(20).Find(&posts)
	res.Discussions = s.Summaries(discs, viewer)
	for i := range posts {
		po := &posts[i]
		if po.Discussion == nil {
			continue
		}
		sum := s.DiscussionSummary(po.Discussion, viewer)
		res.Posts = append(res.Posts, ProfilePostRow{Post: s.postDTO(po, nil, viewer), Discussion: &sum})
	}
	return res
}

// ---------- 讨论详情 / 已读进度 ----------

func (s *Service) loadDiscussion(u *models.User, id uint) (*models.Discussion, *Error) {
	var d models.Discussion
	if err := s.db.Preload("User").Preload("Tags").First(&d, id).Error; err != nil {
		return nil, Err(http.StatusNotFound, "该帖子不存在。")
	}
	if d.IsHidden() && !canViewHidden(u) {
		return nil, Err(http.StatusNotFound, "该帖子不存在。")
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

// ReadProgressDTO 账户级帖子阅读楼层进度。
type ReadProgressDTO struct {
	PostID     uint      `json:"postId"`
	PostNumber int       `json:"postNumber"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// DetailData 讨论详情响应数据。
type DetailData struct {
	Discussion   DiscussionSummaryDTO  `json:"discussion"`
	Posts        []PostDTO             `json:"posts"`
	Page         PageDTO               `json:"page"`
	CanReply     bool                  `json:"canReply"`
	CanDelete    bool                  `json:"canDelete"`
	IsMod        bool                  `json:"isMod"`
	EditorTags   []TagDTO              `json:"editorTags"`
	Viewers      []DiscussionViewerDTO `json:"viewers"`
	IsWeekly     bool                  `json:"isWeekly"`
	WindowDays   int                   `json:"windowDays"`
	ReadProgress *ReadProgressDTO      `json:"readProgress,omitempty"`
}

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

func loadReadPosition(g *gorm.DB, userID, discussionID uint) *ReadProgressDTO {
	var pos models.DiscussionReadPosition
	if err := g.Where("user_id = ? AND discussion_id = ?", userID, discussionID).First(&pos).Error; err != nil {
		return nil
	}
	if pos.PostID == 0 && pos.PostNumber == 0 {
		return nil
	}
	return &ReadProgressDTO{
		PostID:     pos.PostID,
		PostNumber: pos.PostNumber,
		UpdatedAt:  pos.UpdatedAt,
	}
}

func saveReadPosition(g *gorm.DB, userID, discussionID, postID uint, postNumber int) (time.Time, error) {
	now := time.Now()
	if postNumber <= 0 && postID == 0 {
		return now, nil
	}
	pos := models.DiscussionReadPosition{
		UserID:       userID,
		DiscussionID: discussionID,
		PostID:       postID,
		PostNumber:   postNumber,
		UpdatedAt:    now,
	}
	err := g.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}, {Name: "discussion_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"post_id", "post_number", "updated_at"}),
	}).Create(&pos).Error
	return now, err
}

// SaveReadPosition 按账户保存用户在某讨论当前阅读停留的楼层。
func (s *Service) SaveReadPosition(u *models.User, discussionID, postID uint, postNumber int) (ReadProgressDTO, *Error) {
	d, err := s.loadDiscussion(u, discussionID)
	if err != nil {
		return ReadProgressDTO{}, err
	}
	var post models.Post
	q := s.db.Model(&models.Post{}).Where("discussion_id = ? AND type = ?", d.ID, "comment")
	switch {
	case postID > 0:
		q = q.Where("id = ?", postID)
	case postNumber > 0:
		q = q.Where("number = ?", postNumber)
	default:
		return ReadProgressDTO{}, Err(http.StatusBadRequest, "缺少有效的楼层标识。")
	}
	if err := q.First(&post).Error; err != nil {
		return ReadProgressDTO{}, Err(http.StatusNotFound, "该楼层不存在。")
	}
	num := post.PostNumber()
	var savedAt time.Time
	if txErr := s.db.Transaction(func(tx *gorm.DB) error {
		var err error
		savedAt, err = saveReadPosition(tx, u.ID, d.ID, post.ID, num)
		if err != nil {
			return err
		}
		if num > 0 {
			return saveReadProgress(tx, u.ID, d.ID, num)
		}
		return nil
	}); txErr != nil {
		log.Printf("[service] saveReadPosition 失败 discussion=%d user=%d: %v", d.ID, u.ID, txErr)
		return ReadProgressDTO{}, Err(http.StatusInternalServerError, "保存阅读进度失败，请重试。")
	}
	return ReadProgressDTO{PostID: post.ID, PostNumber: num, UpdatedAt: savedAt}, nil
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
	readProgress := loadReadPosition(s.db, u.ID, d.ID)
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
	postQuery := s.db.Model(&models.Post{}).Where("discussion_id = ? AND type = ?", d.ID, "comment")

	var totalComments int64
	postQuery.Session(&gorm.Session{}).Model(&models.Post{}).Count(&totalComments)

	page := q.Page
	pageSize := PerPagePosts
	if q.PerPage > 0 && q.PerPage <= 100 {
		pageSize = q.PerPage
	}

	isWeeklyRecent := isWeekly && !q.FromStart && !q.HasPage && q.BeforeNumber <= 0

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
				cond := postQuery.Session(&gorm.Session{})
				lastPostNum := int(totalComments)
				if d.LastPostNumber != nil && *d.LastPostNumber > 0 {
					lastPostNum = *d.LastPostNumber
				}
				if readProgress != nil && readProgress.PostNumber > 0 && lastPostNum-readProgress.PostNumber <= 150 {
					startNum := max(1, readProgress.PostNumber-2)
					cond = cond.Where("created_at >= ? OR number >= ?", now.AddDate(0, 0, -days), startNum)
				} else {
					cond = cond.Where("created_at >= ?", now.AddDate(0, 0, -days))
				}
				cond.Order("number ASC").
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
			if (page >= pages || q.LoadAll || isWeekly) && lastPostNum > maxNumber {
				maxNumber = lastPostNum
			}
			if err := saveReadProgress(s.db, u.ID, d.ID, maxNumber); err != nil {
				return DetailData{}, Err(http.StatusInternalServerError, "保存失败，请重试。")
			}
		}
	}

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
		dto := s.postDTO(&posts[i], liked, u)
		dto.Discussion.Slug = d.Slug()
		views = append(views, dto)
	}
	canReply := !s.cfg.ReadOnly && !d.IsLocked
	editorTags := make([]TagDTO, 0)
	if isMod {
		for _, t := range s.PostableTags() {
			editorTags = append(editorTags, s.TagDTO(&t, u))
		}
	}
	summary := s.DiscussionSummary(d, u)
	summary.HasUnread = false
	return DetailData{
		Discussion:   summary,
		Posts:        views,
		Page:         PageDTO{Page: page, Pages: pages, Total: int(totalComments)},
		CanReply:     canReply,
		CanDelete:    !s.cfg.ReadOnly && canDeleteDiscussion(u, d),
		IsMod:        isMod && !s.cfg.ReadOnly,
		EditorTags:   editorTags,
		Viewers:      s.loadDiscussionViewers(d.ID),
		IsWeekly:     isWeekly,
		WindowDays:   windowDays,
		ReadProgress: readProgress,
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
func (s *Service) CreateDiscussion(u *models.User, title, source string, tagIDs []uint) (CreateDiscussionResult, *Error) {
	title = strings.TrimSpace(title)
	source = strings.TrimSpace(source)

	postable := s.PostableTags()
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
	if source == "" {
		return CreateDiscussionResult{}, Err(http.StatusBadRequest, "正文不能为空。")
	}
	if len(chosen) == 0 {
		return CreateDiscussionResult{}, Err(http.StatusBadRequest, "请至少选择一个版块。")
	}
	now := time.Now()
	one := 1
	uid := u.ID
	d := models.Discussion{
		Title: title, CommentCount: 1, ParticipantCount: 1, PostNumberIndex: 1,
		CreatedAt: now, LastPostedAt: &now, LastPostNumber: &one,
		SlugCol: util.MakeSlug(title),
		UserID:  &uid, LastPostUserID: &uid, IsApproved: true,
	}
	post := models.Post{Number: &one, UserID: &uid, CreatedAt: now, Type: "comment", IsApproved: true}
	post.SetMarkdown(source)
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
		if err := saveReadProgress(tx, u.ID, d.ID, 1); err != nil {
			return err
		}
		if _, err := saveReadPosition(tx, u.ID, d.ID, post.ID, 1); err != nil {
			return err
		}
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
		if _, err := s.notifyMentions(tx, source, u.ID, d.ID, post.ID); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		log.Printf("[service] createDiscussion 事务失败 user=%s: %v", u.Username, err)
		return CreateDiscussionResult{}, Err(http.StatusInternalServerError, "保存失败，请重试。")
	}
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
	if strings.TrimSpace(content) == "" {
		return ReplyResult{}, Err(http.StatusBadRequest, "回复内容不能为空。")
	}
	d, err := s.loadDiscussion(u, discussionID)
	if err != nil {
		return ReplyResult{}, err
	}
	if d.IsLocked {
		return ReplyResult{}, Err(http.StatusBadRequest, "该帖子已被锁定，无法回复。")
	}
	uid := u.ID
	post := models.Post{
		DiscussionID: d.ID, UserID: &uid, Type: "comment", IsApproved: true,
	}
	post.SetMarkdown(content)
	var position int64
	if txErr := s.db.Transaction(func(tx *gorm.DB) error {
		var current models.Discussion
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("id", "post_number_index").First(&current, d.ID).Error; err != nil {
			return err
		}
		var maxNumber int
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
		if err := tx.Model(&models.Post{}).Where("discussion_id = ? AND type = ?", d.ID, "comment").Count(&position).Error; err != nil {
			return err
		}
		if err := tx.Model(&models.User{}).Where("id = ?", u.ID).
			UpdateColumn("comment_count", gorm.Expr("comment_count + 1")).Error; err != nil {
			return err
		}
		if err := saveReadProgress(tx, u.ID, d.ID, nextNumber); err != nil {
			return err
		}
		if _, err := saveReadPosition(tx, u.ID, d.ID, post.ID, nextNumber); err != nil {
			return err
		}
		for _, t := range d.Tags {
			if err := tx.Model(&models.Tag{}).Where("id = ?", t.ID).Updates(map[string]any{
				"last_posted_at":            now,
				"last_posted_discussion_id": d.ID,
				"last_posted_user_id":       uid,
			}).Error; err != nil {
				return err
			}
		}
		mentionedIDs, err := s.notifyMentions(tx, content, u.ID, d.ID, post.ID)
		if err != nil {
			return err
		}
		if d.UserID != nil && *d.UserID != u.ID && !mentionedIDs[*d.UserID] {
			if err := notify(tx, *d.UserID, u.ID, "reply", d.ID, post.ID); err != nil {
				return err
			}
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
	if d.IsHidden() && !canViewHidden(u) {
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
			updates["slug"] = util.MakeSlug(title)
		}
		if len(tagIDs) > 0 {
			postable := s.PostableTags()
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
					if err := tx.Where("discussion_id = ?", d.ID).Delete(&models.DiscussionTag{}).Error; err != nil {
						return err
					}
					rows := make([]models.DiscussionTag, 0, len(cleanTagIDs))
					for _, tid := range cleanTagIDs {
						rows = append(rows, models.DiscussionTag{DiscussionID: d.ID, TagID: tid})
					}
					return tx.Create(&rows).Error
				})
				if err != nil {
					return Err(http.StatusInternalServerError, "标签更新失败。")
				}
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

// DeleteDiscussion 物理删除讨论及其全部回复和关联记录（仅作者本人或版主可操作）。
func (s *Service) DeleteDiscussion(u *models.User, id uint) *Error {
	var d models.Discussion
	if err := s.db.Preload("Tags").First(&d, id).Error; err != nil {
		return Err(http.StatusNotFound, "该帖子不存在。")
	}
	if d.IsHidden() && !canViewHidden(u) {
		return Err(http.StatusNotFound, "该帖子不存在。")
	}
	if !canDeleteDiscussion(u, &d) {
		return Err(http.StatusForbidden, "您没有权限删除该帖子。")
	}

	var posts []models.Post
	if err := s.db.Select("id, user_id, type").Where("discussion_id = ?", d.ID).Find(&posts).Error; err != nil {
		return Err(http.StatusInternalServerError, "删除失败，请重试。")
	}
	postIDs := make([]uint, 0, len(posts))
	replyCounts := map[uint]int{}
	for _, p := range posts {
		postIDs = append(postIDs, p.ID)
		if p.Type == "comment" && p.UserID != nil && (d.FirstPostID == nil || p.ID != *d.FirstPostID) {
			replyCounts[*p.UserID]++
		}
	}

	err := s.db.Transaction(func(tx *gorm.DB) error {
		if len(postIDs) > 0 {
			if err := tx.Where("post_id IN ?", postIDs).Delete(&models.PostLike{}).Error; err != nil {
				return err
			}
		}
		for _, model := range []any{
			&models.Notification{},
			&models.DiscussionReadPosition{},
			&models.DiscussionUser{},
			&models.DiscussionView{},
			&models.DiscussionTag{},
		} {
			if err := tx.Where("discussion_id = ?", d.ID).Delete(model).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&models.Discussion{}).Where("id = ?", d.ID).
			Updates(map[string]any{"first_post_id": nil, "last_post_id": nil}).Error; err != nil {
			return err
		}
		if err := tx.Where("discussion_id = ?", d.ID).Delete(&models.Post{}).Error; err != nil {
			return err
		}
		if err := tx.Model(&models.Tag{}).Where("last_posted_discussion_id = ?", d.ID).
			Update("last_posted_discussion_id", nil).Error; err != nil {
			return err
		}
		for _, t := range d.Tags {
			if err := tx.Model(&models.Tag{}).Where("id = ?", t.ID).
				UpdateColumn("discussion_count", gorm.Expr("CASE WHEN discussion_count > 0 THEN discussion_count - 1 ELSE 0 END")).Error; err != nil {
				return err
			}
		}
		if d.UserID != nil {
			if err := tx.Model(&models.User{}).Where("id = ?", *d.UserID).
				UpdateColumn("discussion_count", gorm.Expr("CASE WHEN discussion_count > 0 THEN discussion_count - 1 ELSE 0 END")).Error; err != nil {
				return err
			}
		}
		for uid, n := range replyCounts {
			if err := tx.Model(&models.User{}).Where("id = ?", uid).
				UpdateColumn("comment_count", gorm.Expr("CASE WHEN comment_count >= ? THEN comment_count - ? ELSE 0 END", n, n)).Error; err != nil {
				return err
			}
		}
		return tx.Delete(&models.Discussion{}, d.ID).Error
	})
	if err != nil {
		log.Printf("[service] deleteDiscussion 事务失败 discussion=%d user=%s: %v", d.ID, u.Username, err)
		return Err(http.StatusInternalServerError, "删除失败，请重试。")
	}
	return nil
}
