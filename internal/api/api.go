package api

import (
	"hash/fnv"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/exzco/bbs/internal/auth"
	"github.com/exzco/bbs/internal/config"
	"github.com/exzco/bbs/internal/markdown"
	"github.com/exzco/bbs/internal/models"
	"github.com/exzco/bbs/internal/slug"
	"github.com/exzco/bbs/internal/store"
	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
	"gorm.io/gorm"
)

const (
	perPageDiscussions = 20
	perPagePosts       = 15
	perPageUsers       = 20
)

type Server struct {
	cfg *config.Config
	db  *gorm.DB

	renderCache sync.Map // postID -> renderEntry{hash, html}（正文实时渲染缓存）
}

type renderEntry struct {
	hash uint64
	html string
}

func New(cfg *config.Config, g *gorm.DB) *Server {
	return &Server{cfg: cfg, db: g}
}

// Register 挂载全部 API 路由。
func (s *Server) Register(r gin.IRouter) {
	g := r.Group("/api")
	g.Use(func(c *gin.Context) {
		if s.cfg.ReadOnly && c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead && c.Request.Method != http.MethodOptions && c.Request.URL.Path != "/api/auth/login" && c.Request.URL.Path != "/api/auth/logout" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "迁移期间新站只读，请前往旧站进行修改。", "code": "read_only"})
			return
		}
		c.Next()
	})
	g.GET("/me", s.me)
	g.GET("/auth/captcha", s.captchaNew)
	g.POST("/auth/login", s.limitWith(authLimiter), s.login)
	g.POST("/auth/register", s.limitWith(authLimiter), s.register)
	g.POST("/auth/logout", s.logout)

	me := g.Group("/me", s.requireUser())
	me.POST("/profile", s.updateProfile)
	me.POST("/password", s.updatePassword)
	me.POST("/avatar", s.updateAvatar)
	me.GET("/notifications", s.notifications)
	me.POST("/notifications/read", s.notificationsRead)

	// 内容类接口全部要求登录（内部论坛：所有内容请登录后查看）；
	// 未登录仅开放会话与注册相关接口。
	authed := g.Group("", s.requireUser())
	authed.GET("/home", s.home)
	authed.GET("/recent", s.recent)
	authed.GET("/groups", s.listGroups)
	authed.GET("/tags/:slug", s.tagDetail)
	authed.GET("/tags/:slug/discussions", s.tagDiscussions)
	authed.GET("/post-form", s.postForm)
	authed.POST("/preview", s.preview)
	authed.GET("/search", s.search)
	authed.GET("/users/:username", s.profile)
	authed.GET("/users/:username/activity", s.userActivity)

	d := authed.Group("/discussions")
	d.GET("/:id", s.discussionDetail)
	d.POST("", s.limitWith(postLimiter), s.createDiscussion)
	d.POST("/:id/reply", s.limitWith(postLimiter), s.reply)
	d.POST("/:id/mod", s.moderate)
	p := authed.Group("/posts")
	p.GET("/:id/edit", s.postEditGet)
	p.POST("/:id/edit", s.editPost)
	p.POST("/:id/hide", s.hidePost)
	p.POST("/:id/like", s.toggleLike)

	g.GET("/admin/meta", s.requireAdmin(), s.adminMeta)
	g.GET("/admin/tags", s.requireAdmin(), s.adminTags)
	g.POST("/admin/tags", s.requireAdmin(), s.adminTagSave)
	g.POST("/admin/tags/:id/delete", s.requireAdmin(), s.adminTagDelete)
	g.GET("/admin/users", s.requireAdmin(), s.adminUsers)
	g.POST("/admin/users/:id", s.requireAdmin(), s.adminUserSave)
	g.GET("/admin/invites", s.requireAdmin(), s.adminInvites)
	g.POST("/admin/invites", s.requireAdmin(), s.adminInviteCreate)
	g.GET("/admin/hidden", s.requireAdmin(), s.adminHidden)
	g.POST("/admin/discussions/:id/restore", s.requireAdmin(), s.adminRestore)
	g.GET("/admin/settings", s.requireAdmin(), s.adminSettingsGet)
	g.POST("/admin/settings", s.requireAdmin(), s.adminSettingsSave)
}

// ---------- 通用辅助 ----------

func (s *Server) currentUser(c *gin.Context) *models.User {
	if v, ok := c.Get("user"); ok {
		if u, ok := v.(*models.User); ok && u != nil && !u.IsBanned() {
			return u
		}
	}
	return nil
}

func (s *Server) requireUser() gin.HandlerFunc {
	return func(c *gin.Context) {
		if s.currentUser(c) == nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录。"})
			c.Abort()
			return
		}
		c.Next()
	}
}

func (s *Server) requireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		u := s.currentUser(c)
		if u == nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录。"})
			c.Abort()
			return
		}
		if !u.IsAdmin() {
			c.JSON(http.StatusForbidden, gin.H{"error": "需要管理员权限。"})
			c.Abort()
			return
		}
		c.Next()
	}
}

func fail(c *gin.Context, code int, msg string) {
	c.JSON(code, gin.H{"error": msg})
}

func pageParam(c *gin.Context) int {
	p, _ := strconv.Atoi(c.Query("page"))
	if p < 1 {
		p = 1
	}
	return p
}

func paginate(total, page, perPage int) (pages int) {
	pages = (total + perPage - 1) / perPage
	if pages < 1 {
		pages = 1
	}
	if page > pages {
		page = pages
	}
	return pages
}

func parseUint(s string) (uint, error) {
	v, err := strconv.ParseUint(s, 10, 32)
	return uint(v), err
}

// mentionResolver 解析 @提及 的用户 ID。
func (s *Server) mentionResolver() func(string) (uint, bool) {
	return func(name string) (uint, bool) {
		var id uint
		s.db.Model(&models.User{}).Select("id").
			Where("lower(username) = lower(?)", name).Scan(&id)
		return id, id > 0
	}
}

// settingValue 读取 flarum_settings（与 PHP 共享；forum_title 即站点名）。
func (s *Server) settingValue(key string) string {
	var st models.Setting
	if err := s.db.Where("`key` = ?", key).First(&st).Error; err == nil && st.Value != "" {
		return st.Value
	}
	switch key {
	case models.SettingKeySiteName:
		return s.cfg.SiteName
	case models.SettingKeyRegistration:
		return s.cfg.Registration
	}
	return ""
}

// ---------- DTO ----------

type GroupDTO struct {
	ID    uint   `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
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

func userDTO(u *models.User) *UserDTO {
	if u == nil {
		return nil
	}
	groups := make([]GroupDTO, 0, len(u.Groups))
	for _, g := range u.Groups {
		groups = append(groups, GroupDTO{ID: g.ID, Name: g.Name(), Color: g.Color})
	}
	return &UserDTO{
		ID: u.ID, Username: u.Username, Bio: u.Bio,
		Avatar: avatarURL(u), Groups: groups,
		IsAdmin: u.IsAdmin(), IsMod: u.IsModerator(), IsBanned: u.IsBanned(),
		DiscussionsCount: u.DiscussionsCount, PostsCount: u.PostsCount,
		CreatedAt: u.JoinedAtOrZero(), LastSeenAt: u.LastSeenAt,
	}
}

// avatarURL：flarum avatar_url 原样（相对 /assets 路径由资产挂载伺服）；
// 为空则用字环头像。
func avatarURL(u *models.User) string {
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

func (s *Server) tagDTO(t *models.Tag, u *models.User) TagDTO {
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
	CreatedAt    time.Time `json:"createdAt"`
	LastPostedAt time.Time `json:"lastPostedAt"`
	LastPoster   string    `json:"lastPoster,omitempty"`
	IsSticky     bool      `json:"isSticky"`
	IsLocked     bool      `json:"isLocked"`
	IsHidden     bool      `json:"isHidden"`
	HasUnread    bool      `json:"hasUnread"`
}

func (s *Server) loadReadMap(discIDs []uint, u *models.User) map[uint]int {
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

func (s *Server) discussionSummaryWithRead(d *models.Discussion, u *models.User, readMap map[uint]int) DiscussionSummaryDTO {
	dto := s.discussionSummaryDTO(d, u)
	if u != nil && readMap != nil {
		lastPostNum := d.CommentCount
		if d.LastPostNumber != nil && *d.LastPostNumber > 0 {
			lastPostNum = *d.LastPostNumber
		}
		lastRead, hasRead := readMap[d.ID]
		if !hasRead || lastRead < lastPostNum {
			dto.HasUnread = true
		}
	}
	return dto
}

func (s *Server) discussionSummaryDTO(d *models.Discussion, u *models.User) DiscussionSummaryDTO {
	tags := make([]TagDTO, 0, len(d.Tags))
	seenTag := make(map[uint]bool, len(d.Tags))
	for i := range d.Tags {
		if !seenTag[d.Tags[i].ID] {
			seenTag[d.Tags[i].ID] = true
			tags = append(tags, s.tagDTO(&d.Tags[i], u))
		}
	}
	dto := DiscussionSummaryDTO{
		ID: d.ID, Title: d.Title, Slug: d.Slug(), Tags: tags,
		Author:       userDTO(&d.User),
		CommentCount: d.CommentCount,
		CreatedAt:    d.CreatedAt, LastPostedAt: d.LastPostedAtOrZero(),
		IsSticky: d.IsSticky, IsLocked: d.IsLocked, IsHidden: d.HiddenAt != nil,
	}
	if d.LastPostUser != nil {
		dto.LastPoster = d.LastPostUser.Username
	}
	return dto
}

type PostDTO struct {
	ID         uint       `json:"id"`
	Number     int        `json:"number"`
	Author     *UserDTO   `json:"author"`
	Content    string     `json:"content"` // Markdown 原文
	HTML       string     `json:"html"`    // 实时渲染（消毒后）
	LikeCount  int        `json:"likeCount"`
	Liked      bool       `json:"liked"`
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

func (s *Server) postDTO(p *models.Post, likedSet map[uint]bool, u *models.User) PostDTO {
	dto := PostDTO{
		ID: p.ID, Number: p.PostNumber(), Author: userDTO(&p.User),
		Content: p.Markdown(), HTML: s.renderPostHTML(p),
		LikeCount: p.LikeCount, Liked: likedSet[p.ID],
		CreatedAt: p.CreatedAt, EditedAt: p.EditedAt, Hidden: p.HiddenAt != nil,
		CanEdit: !s.cfg.ReadOnly && auth.CanEditPost(u, p),
		CanHide: !s.cfg.ReadOnly && u != nil && (u.IsModerator() || (p.UserID != nil && *p.UserID == u.ID)),
	}
	dto.Discussion.ID = p.DiscussionID
	return dto
}

// renderPostHTML 实时渲染正文（带缓存；无 rendered_html 列——与 PHP 共享表，零侵入）。
func (s *Server) renderPostHTML(p *models.Post) string {
	h := fnv64a(p.Content)
	if v, ok := s.renderCache.Load(p.ID); ok {
		if e, ok := v.(renderEntry); ok && e.hash == h {
			return e.html
		}
	}
	html, _ := markdown.Render(p.Markdown(), s.mentionResolver())
	s.renderCache.Store(p.ID, renderEntry{hash: h, html: html})
	return html
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

// PageDTO 分页信息。
type PageDTO struct {
	Page  int `json:"page"`
	Pages int `json:"pages"`
	Total int `json:"total"`
}

// loadLikes 批量填充帖子的点赞数与当前用户是否已赞。
func (s *Server) loadLikes(posts []models.Post, viewer *models.User) map[uint]bool {
	liked := map[uint]bool{}
	if len(posts) == 0 {
		return liked
	}
	ids := make([]uint, 0, len(posts))
	for i := range posts {
		ids = append(ids, posts[i].ID)
	}
	type likeRow struct {
		PostID uint
		N      int
	}
	var counts []likeRow
	s.db.Table("flarum_post_likes").
		Select("post_id, COUNT(*) as n").
		Where("post_id IN ?", ids).
		Group("post_id").Scan(&counts)
	countMap := map[uint]int{}
	for _, c := range counts {
		countMap[c.PostID] = c.N
	}
	for i := range posts {
		posts[i].LikeCount = countMap[posts[i].ID]
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

// fillTagPerms 把 flarum_group_permission 的版块权限填充到版块切片。
func (s *Server) fillTagPerms(tags []models.Tag) auth.TagPerms {
	tp := auth.LoadTagPerms(s.db)
	auth.FillTagPerms(s.db, tags, tp)
	return tp
}

// notify 创建通知（bbs_notifications，Go 专属表；不给自己发）。
func notify(g *gorm.DB, userID, senderID uint, typ string, discussionID, postID uint) {
	if userID == 0 || userID == senderID {
		return
	}
	g.Create(&models.Notification{
		UserID: userID, SenderID: senderID, Type: typ,
		DiscussionID: discussionID, PostID: postID,
	})
}

// notifyDiscussionParticipants 通知讨论里发过言的人（含楼主）。
func notifyDiscussionParticipants(g *gorm.DB, d *models.Discussion, senderID uint, typ string, postID uint) {
	var ids []uint
	g.Model(&models.Post{}).Where("discussion_id = ? AND user_id <> ?", d.ID, senderID).
		Distinct().Limit(100).Pluck("user_id", &ids)
	seen := map[uint]bool{}
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			notify(g, id, senderID, typ, d.ID, postID)
		}
	}
}

// slugify 生成 URL slug。
func slugify(title string) string { return slug.Make(title) }

// postableTags 用户可见且可发帖的版块（权限由查询层填充）。
func (s *Server) postableTags(u *models.User) []models.Tag {
	var tags []models.Tag
	s.db.Order("position ASC, id ASC").Find(&tags)
	s.fillTagPerms(tags)
	out := tags[:0:0]
	for _, t := range tags {
		if auth.CanViewTag(u, &t) && auth.CanPostTag(u, &t) {
			out = append(out, t)
		}
	}
	return out
}

var usernameRe = regexp.MustCompile(`^[A-Za-z0-9_\-]{2,20}$`)

// ---------- 限速（按 IP + 路由类别） ----------

type rateLimiter struct {
	mu       sync.Mutex
	visitors map[string]*entry
	perMin   int
	burst    int
	once     sync.Once
}

type entry struct {
	lim  *rate.Limiter
	last time.Time
}

func newRateLimiter(perMinute, burst int) *rateLimiter {
	return &rateLimiter{
		visitors: map[string]*entry{},
		perMin:   perMinute,
		burst:    burst,
	}
}

func (rl *rateLimiter) allow(ip string) bool {
	rl.once.Do(func() {
		go func() {
			for range time.Tick(10 * time.Minute) {
				rl.mu.Lock()
				for k, v := range rl.visitors {
					if time.Since(v.last) > time.Hour {
						delete(rl.visitors, k)
					}
				}
				rl.mu.Unlock()
			}
		}()
	})
	rl.mu.Lock()
	defer rl.mu.Unlock()
	e, ok := rl.visitors[ip]
	if !ok {
		e = &entry{lim: rate.NewLimiter(rate.Every(time.Minute/time.Duration(rl.perMin)), rl.burst)}
		rl.visitors[ip] = e
	}
	e.last = time.Now()
	return e.lim.Allow()
}

var (
	authLimiter = newRateLimiter(10, 10)
	postLimiter = newRateLimiter(30, 30)
)

func (s *Server) limitWith(rl *rateLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !rl.allow(c.ClientIP()) {
			fail(c, http.StatusTooManyRequests, "操作过于频繁，请稍后再试。")
			c.Abort()
			return
		}
		c.Next()
	}
}

// Shared scopes keep list, search, profile and notification visibility aligned.
func (s *Server) visibleDiscussions(u *models.User) *gorm.DB {
	return store.VisibleDiscussions(s.db, u)
}

func (s *Server) visiblePosts(u *models.User) *gorm.DB {
	return store.VisiblePosts(s.db, u)
}
