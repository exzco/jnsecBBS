// Package service 承载论坛业务逻辑与事务编排（查询组装、权限判定、通知写入）。
// 严禁依赖任何 Web 框架上下文：HTTP 绑定、参数提取与 JSON 输出由 api（handler）层负责。
package service

import (
	"hash/fnv"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/exzco/bbs/internal/auth"
	"github.com/exzco/bbs/internal/cache"
	"github.com/exzco/bbs/internal/config"
	"github.com/exzco/bbs/internal/mail"
	"github.com/exzco/bbs/internal/markdown"
	"github.com/exzco/bbs/internal/models"
	"gorm.io/gorm"
)

const (
	perPageDiscussions = 20
	// PerPagePosts 讨论详情每页楼层数（测试与列表分页共用）。
	PerPagePosts = 15
)

// Service 内容业务层。
type Service struct {
	cfg  *config.Config
	db   *gorm.DB
	mail *mail.Sender // SMTP 发信客户端；nil = 未启用邮件通知

	renderCache sync.Map // postID -> renderEntry{hash, html}（正文实时渲染缓存）
}

type renderEntry struct {
	hash uint64
	html string
}

func New(cfg *config.Config, g *gorm.DB) *Service {
	return &Service{cfg: cfg, db: g, mail: newMailSender(cfg)}
}

// Error 业务错误：由 handler 映射为 HTTP 状态码与 JSON 输出。
type Error struct {
	Status  int
	Message string
}

// Err 构造业务错误。
func Err(status int, msg string) *Error { return &Error{Status: status, Message: msg} }

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

// ---------- 通用查询 ----------

// SettingValue 读取 flarum_settings（与 PHP 共享；forum_title 即站点名）。
// Cache-Aside：先查 setting:{key}，未命中查库（含配置兜底）并回写（TTL 带抖动）；
// adminSettingsSave 写路径显式失效。
func (s *Service) SettingValue(key string) string {
	if v, ok := cache.Default.Get(cache.SettingKey(key)); ok {
		if value, ok := v.(string); ok {
			return value
		}
	}
	var st models.Setting
	value := ""
	if err := s.db.Where("`key` = ?", key).First(&st).Error; err == nil && st.Value != "" {
		value = st.Value
	} else {
		switch key {
		case models.SettingKeySiteName:
			value = s.cfg.SiteName
		case models.SettingKeyRegistration:
			value = s.cfg.Registration
		}
	}
	cache.Default.Set(cache.SettingKey(key), value, cache.TTL(10*time.Minute, 2*time.Minute))
	return value
}

// mentionedUserID 解析 @提及 的用户 ID。Cache-Aside 仅缓存正向命中
// （user:name:{lower}）：用户名不可变更、ID 不回收，正向映射恒真，无需失效；
// 未命中（id=0）不缓存，避免注册同名用户后拿到过期的负向结果。
func (s *Service) mentionedUserID(name string) (uint, bool) {
	lower := strings.ToLower(name)
	if v, ok := cache.Default.Get(cache.UserNameKey(lower)); ok {
		if id, ok := v.(uint); ok && id > 0 {
			return id, true
		}
	}
	var id uint
	s.db.Model(&models.User{}).Select("id").
		Where("lower(username) = lower(?)", name).Scan(&id)
	if id > 0 {
		cache.Default.Set(cache.UserNameKey(lower), id, cache.TTL(10*time.Minute, 2*time.Minute))
	}
	return id, id > 0
}

// LoadUserProfileExtra 读取用户扩展资料；只读镜像可能尚未建表，缺表降级为空。
func (s *Service) LoadUserProfileExtra(userID uint) (realName, blogURL string) {
	var extra models.UserProfile
	if err := s.db.Where("user_id = ?", userID).First(&extra).Error; err == nil {
		return extra.RealName, extra.BlogURL
	}
	return "", ""
}

// FillTagPerms 把 flarum_group_permission 的版块权限填充到版块切片并返回权限表。
func (s *Service) FillTagPerms(tags []models.Tag) auth.TagPerms {
	tp := auth.LoadTagPerms(s.db)
	auth.FillTagPerms(s.db, tags, tp)
	return tp
}

// tagDiscussionCounts 各版块讨论数（实时统计，Flarum tags 无可靠计数列）。
// Cache-Aside：tag:counts:all 未命中查库并回写（TTL 带抖动）；
// CreateDiscussion 与 Moderate-edit_meta 改动 flarum_discussion_tag 后显式失效。
func (s *Service) tagDiscussionCounts() map[uint]int {
	if v, ok := cache.Default.Get(cache.TagCountsKey()); ok {
		if m, ok := v.(map[uint]int); ok {
			return m
		}
	}
	type row struct {
		TagID uint
		N     int
	}
	var rows []row
	s.db.Table("flarum_discussion_tag").
		Select("tag_id, COUNT(DISTINCT discussion_id) as n").
		Group("tag_id").Scan(&rows)
	counts := make(map[uint]int, len(rows))
	for _, r := range rows {
		counts[r.TagID] = r.N
	}
	cache.Default.Set(cache.TagCountsKey(), counts, cache.TTL(time.Minute, 15*time.Second))
	return counts
}

// PostableTags 用户可见且可发帖的版块（权限由查询层填充）。
func (s *Service) PostableTags(u *models.User) []models.Tag {
	var tags []models.Tag
	s.db.Order("position ASC, id ASC").Find(&tags)
	s.FillTagPerms(tags)
	out := tags[:0:0]
	for _, t := range tags {
		if auth.CanViewTag(u, &t) && auth.CanPostTag(u, &t) {
			out = append(out, t)
		}
	}
	return out
}

// loadReadMap 读取当前用户对各讨论的已读楼层。
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

// notify 创建通知（bbs_notifications，Go 专属表；不给自己发）。
func notify(g *gorm.DB, userID, senderID uint, typ string, discussionID, postID uint) error {
	if userID == 0 || userID == senderID {
		return nil
	}
	return g.Create(&models.Notification{
		UserID: userID, SenderID: senderID, Type: typ,
		DiscussionID: discussionID, PostID: postID,
	}).Error
}

// notifyMentions 给正文 @提及 的用户发 mention 通知并返回被提及 ID 集
// （MentionedUsernames 已去重且仅含正向解析名，此处再次解析走缓存；供调用方抑制对楼主的重复通知）。
func (s *Service) notifyMentions(g *gorm.DB, content string, senderID, discussionID, postID uint) (map[uint]bool, error) {
	mentioned := map[uint]bool{}
	for _, name := range markdown.MentionedUsernames(content, s.mentionedUserID) {
		mid, ok := s.mentionedUserID(name)
		if !ok || mid == senderID {
			continue
		}
		if err := notify(g, mid, senderID, "mention", discussionID, postID); err != nil {
			return nil, err
		}
		mentioned[mid] = true
	}
	return mentioned, nil
}

// ---------- DTO ----------

type GroupDTO struct {
	ID    uint   `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

// groupDTOs 批量组装用户组视图。
func groupDTOs(groups []models.Group) []GroupDTO {
	out := make([]GroupDTO, 0, len(groups))
	for i := range groups {
		out = append(out, GroupDTO{ID: groups[i].ID, Name: groups[i].Name(), Color: groups[i].Color})
	}
	return out
}

type UserDTO struct {
	ID               uint       `json:"id"`
	Username         string     `json:"username"`
	Bio              string     `json:"bio"`
	Avatar           string     `json:"avatar"`
	Groups           []GroupDTO `json:"groups"`
	IsAdmin          bool       `json:"isAdmin"`
	IsMod            bool       `json:"isMod"`
	IsBanned         bool       `json:"isBanned"`
	DiscussionsCount int        `json:"discussionsCount"`
	PostsCount       int        `json:"postsCount"`
	CreatedAt        time.Time  `json:"createdAt"`
	LastSeenAt       *time.Time `json:"lastSeenAt"`
}

// UserDTO 组装用户视图。
func (s *Service) UserDTO(u *models.User) *UserDTO {
	if u == nil {
		return nil
	}
	return &UserDTO{
		ID: u.ID, Username: u.Username, Bio: u.Bio,
		Avatar: s.AvatarURL(u), Groups: groupDTOs(u.Groups),
		IsAdmin: u.IsAdmin(), IsMod: u.IsModerator(), IsBanned: u.IsBanned(),
		DiscussionsCount: u.DiscussionsCount, PostsCount: u.PostsCount,
		CreatedAt: u.JoinedAtOrZero(), LastSeenAt: u.LastSeenAt,
	}
}

// AvatarURL：flarum avatar_url 原样（相对 /assets 路径由资产挂载伺服）；
// 为空则用字环头像。
func (s *Service) AvatarURL(u *models.User) string {
	if u == nil {
		return "/avatar/_unknown.svg"
	}
	name := "_unknown"
	if u.Username != "" {
		name = u.Username
	}
	if u.AvatarURL != "" {
		value := u.AvatarURL
		if strings.HasPrefix(value, "/assets/") || strings.HasPrefix(value, "/avatars/") {
			return value
		}
		if parsed, err := url.Parse(value); err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Host != "" {
			return value
		}
		// Flarum persists a basename, not the public avatar URL.
		if !strings.ContainsAny(value, "/\\:") && value != "." && value != ".." {
			return "/assets/avatars/" + url.PathEscape(value)
		}
	}
	return "/avatar/" + url.PathEscape(name) + ".svg"
}

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

// TagDTO 组装版块视图（含当前用户的版块权限）。
func (s *Service) TagDTO(t *models.Tag, u *models.User) TagDTO {
	return TagDTO{
		ID: t.ID, Name: t.Name, Slug: t.Slug, Description: t.Description,
		Color: t.Color, Position: derefInt(t.Position), ParentID: t.ParentID,
		DiscussionsCount: t.DiscussionsCount,
		CanPost:          !s.cfg.ReadOnly && auth.CanPostTag(u, t),
		CanView:          auth.CanViewTag(u, t),
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

// withReadState 组装列表项并按已读进度标记未读。
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

type PostDTO struct {
	ID         uint       `json:"id"`
	Number     int        `json:"number"`
	Author     *UserDTO   `json:"author"`
	Content    string     `json:"content"` // Markdown 原文
	HTML       string     `json:"html"`    // 实时渲染（消毒后）
	LikeCount  int        `json:"likeCount"`
	Liked      bool       `json:"liked"`
	Likers     []UserDTO  `json:"likers"`
	CreatedAt  time.Time  `json:"createdAt"`
	EditedAt   *time.Time `json:"editedAt"`
	Hidden     bool       `json:"hidden"`
	CanEdit    bool       `json:"canEdit"`
	CanHide    bool       `json:"canHide"`
	Discussion struct {
		ID   uint   `json:"id"`
		Slug string `json:"slug"`
	} `json:"discussion"`
}

func (s *Service) postDTO(p *models.Post, likedSet map[uint]bool, u *models.User) PostDTO {
	likers := make([]UserDTO, 0, len(p.Likers))
	for i := range p.Likers {
		if d := s.UserDTO(&p.Likers[i]); d != nil {
			likers = append(likers, *d)
		}
	}
	dto := PostDTO{
		ID: p.ID, Number: p.PostNumber(), Author: s.UserDTO(&p.User),
		Content: p.Markdown(), HTML: s.renderPostHTML(p),
		LikeCount: p.LikeCount, Liked: likedSet[p.ID], Likers: likers,
		CreatedAt: p.CreatedAt, EditedAt: p.EditedAt, Hidden: p.HiddenAt != nil,
		CanEdit: !s.cfg.ReadOnly && auth.CanEditPost(u, p),
		CanHide: !s.cfg.ReadOnly && u != nil && (u.IsModerator() || (p.UserID != nil && *p.UserID == u.ID)),
	}
	dto.Discussion.ID = p.DiscussionID
	return dto
}

// renderPostHTML 实时渲染正文（带缓存；无 rendered_html 列——与 PHP 共享表，零侵入）。
func (s *Service) renderPostHTML(p *models.Post) string {
	h := fnv64a(p.Content)
	if v, ok := s.renderCache.Load(p.ID); ok {
		if e, ok := v.(renderEntry); ok && e.hash == h {
			return e.html
		}
	}
	html, _ := markdown.Render(p.Markdown(), s.mentionedUserID)
	s.renderCache.Store(p.ID, renderEntry{hash: h, html: html})
	return html
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

// usersByIDs 批量加载用户（含组）并按 ID 建索引。
func (s *Service) usersByIDs(ids []uint) map[uint]*models.User {
	m := make(map[uint]*models.User, len(ids))
	if len(ids) == 0 {
		return m
	}
	var users []models.User
	s.db.Where("id IN ?", ids).Preload("Groups").Find(&users)
	for i := range users {
		m[users[i].ID] = &users[i]
	}
	return m
}

// loadLikes 批量填充帖子的点赞数、点赞用户列表（每帖最近 20 条）与当前用户是否已赞。
// 计数与列表共用一次按时间倒序的全量读取。
func (s *Service) loadLikes(posts []models.Post, viewer *models.User) map[uint]bool {
	liked := map[uint]bool{}
	if len(posts) == 0 {
		return liked
	}
	ids := make([]uint, 0, len(posts))
	for i := range posts {
		ids = append(ids, posts[i].ID)
	}
	var likes []models.PostLike
	s.db.Where("post_id IN ?", ids).Order("created_at DESC, user_id DESC").Find(&likes)
	counts := map[uint]int{}
	likers := map[uint][]uint{}
	userIDs := []uint{}
	seen := map[uint]bool{}
	for _, l := range likes {
		counts[l.PostID]++
		if len(likers[l.PostID]) < 20 {
			likers[l.PostID] = append(likers[l.PostID], l.UserID)
			if !seen[l.UserID] {
				seen[l.UserID] = true
				userIDs = append(userIDs, l.UserID)
			}
		}
	}
	userMap := s.usersByIDs(userIDs)
	for i := range posts {
		posts[i].LikeCount = counts[posts[i].ID]
		posts[i].Likers = []models.User{}
		for _, uid := range likers[posts[i].ID] {
			if u, ok := userMap[uid]; ok {
				posts[i].Likers = append(posts[i].Likers, *u)
			}
		}
	}
	if viewer != nil {
		var mine []models.PostLike
		s.db.Where("post_id IN ? AND user_id = ?", ids, viewer.ID).Find(&mine)
		for _, l := range mine {
			liked[l.PostID] = true
		}
	}
	return liked
}

// loadPostLikers 获取单帖最近点赞用户列表（用于点赞切换后即时返回）。
func (s *Service) loadPostLikers(postID uint) []UserDTO {
	var likes []models.PostLike
	if err := s.db.Where("post_id = ?", postID).Order("created_at DESC, user_id DESC").Limit(20).Find(&likes).Error; err != nil || len(likes) == 0 {
		return []UserDTO{}
	}
	userIDs := make([]uint, 0, len(likes))
	for _, l := range likes {
		userIDs = append(userIDs, l.UserID)
	}
	userMap := s.usersByIDs(userIDs)
	likers := make([]UserDTO, 0, len(likes))
	for _, l := range likes {
		if u, ok := userMap[l.UserID]; ok {
			if dto := s.UserDTO(u); dto != nil {
				likers = append(likers, *dto)
			}
		}
	}
	return likers
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

// loadDiscussionViewers 近期浏览过该讨论的用户列表。
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

func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func fnv64a(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}

// discussionURL 讨论 URL（旧站链接格式 /d/{id}-{slug}）。
func discussionURL(d *models.Discussion) string {
	idStr := strconv.FormatUint(uint64(d.ID), 10)
	if s := d.Slug(); s != "" {
		return "/d/" + idStr + "-" + s
	}
	return "/d/" + idStr
}
