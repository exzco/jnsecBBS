package api

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/exzco/bbs/internal/auth"
	"github.com/exzco/bbs/internal/models"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9\-]*$`)
var colorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// adminMeta 管理页公共数据：组列表 + 大版块列表。
func (s *Server) adminMeta(c *gin.Context) {
	var groups []models.Group
	s.db.Where("is_hidden = ?", false).Order("id").Find(&groups)
	var tops []models.Tag
	s.db.Where("parent_id IS NULL").Order("position ASC, id ASC").Find(&tops)
	gd := make([]GroupDTO, 0, len(groups))
	for _, g := range groups {
		gd = append(gd, GroupDTO{ID: g.ID, Name: g.Name(), Color: g.Color})
	}
	td := make([]TagDTO, 0, len(tops))
	for i := range tops {
		td = append(td, s.tagDTO(&tops[i], nil))
	}
	c.JSON(http.StatusOK, gin.H{"groups": gd, "topTags": td})
}

type adminTagNode struct {
	Tag      TagDTO         `json:"tag"`
	ViewIDs  []uint         `json:"viewIds"`
	PostIDs  []uint         `json:"postIds"`
	Children []adminTagNode `json:"children"`
}

func (s *Server) adminTags(c *gin.Context) {
	var tags []models.Tag
	s.db.Order("position ASC, id ASC").Find(&tags)
	s.fillTagPerms(tags)
	tp := auth.LoadTagPerms(s.db)
	auth.FillTagPerms(s.db, tags, tp)

	var tops []models.Tag
	byParent := map[uint][]models.Tag{}
	for _, t := range tags {
		if t.ParentID == nil {
			tops = append(tops, t)
		} else {
			byParent[*t.ParentID] = append(byParent[*t.ParentID], t)
		}
	}
	node := func(t models.Tag) adminTagNode {
		return adminTagNode{
			Tag:      s.tagDTO(&t, nil),
			ViewIDs:  tp.View[t.ID],
			PostIDs:  tp.Post[t.ID],
			Children: []adminTagNode{},
		}
	}
	nodes := make([]adminTagNode, 0, len(tops))
	for _, t := range tops {
		n := node(t)
		for _, ch := range byParent[t.ID] {
			n.Children = append(n.Children, node(ch))
		}
		nodes = append(nodes, n)
	}
	c.JSON(http.StatusOK, gin.H{"nodes": nodes})
}

func (s *Server) adminTagSave(c *gin.Context) {
	var body struct {
		ID          uint   `json:"id"`
		Name        string `json:"name"`
		Slug        string `json:"slug"`
		Color       string `json:"color"`
		Position    int    `json:"position"`
		ParentID    uint   `json:"parentId"`
		Description string `json:"description"`
		ViewGroups  []uint `json:"viewGroups"`
		PostGroups  []uint `json:"postGroups"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		fail(c, http.StatusBadRequest, "请求格式错误。")
		return
	}
	name := strings.TrimSpace(body.Name)
	slugIn := strings.ToLower(strings.TrimSpace(body.Slug))
	color := strings.TrimSpace(body.Color)
	if colorRe.MatchString(color) == false {
		color = ""
	}
	if name == "" {
		fail(c, http.StatusBadRequest, "版块名称不能为空。")
		return
	}
	if slugIn == "" {
		slugIn = slugify(name)
	}
	if !slugRe.MatchString(slugIn) {
		fail(c, http.StatusBadRequest, "URL 标识需为字母、数字或连字符（中文留空可自动生成）。")
		return
	}
	var parentID *uint
	if body.ParentID > 0 && body.ParentID != body.ID {
		parentID = &body.ParentID
	}
	position := body.Position
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if body.ID == 0 {
			t := models.Tag{
				Name: name, Slug: slugIn, Description: strings.TrimSpace(body.Description),
				Color: color, Position: &position, ParentID: parentID,
			}
			if err := tx.Create(&t).Error; err != nil {
				return err
			}
			return auth.SaveTagPerms(tx, t.ID, body.ViewGroups, body.PostGroups)
		}
		t := models.Tag{ID: body.ID}
		if err := tx.First(&t).Error; err != nil {
			return err
		}
		if err := tx.Model(&t).Updates(map[string]any{
			"name": name, "slug": slugIn, "description": strings.TrimSpace(body.Description),
			"color": color, "position": body.Position, "parent_id": parentID,
		}).Error; err != nil {
			return err
		}
		return auth.SaveTagPerms(tx, body.ID, body.ViewGroups, body.PostGroups)
	})
	if err != nil {
		fail(c, http.StatusBadRequest, "保存失败："+err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) adminTagDelete(c *gin.Context) {
	id, _ := parseUint(c.Param("id"))
	var t models.Tag
	if err := s.db.First(&t, id).Error; err != nil {
		fail(c, http.StatusNotFound, "版块不存在。")
		return
	}
	var children int64
	s.db.Model(&models.Tag{}).Where("parent_id = ?", id).Count(&children)
	if children > 0 {
		fail(c, http.StatusBadRequest, "请先删除其子版块。")
		return
	}
	var used int64
	s.db.Model(&models.Discussion{}).Joins("JOIN flarum_discussion_tag dt ON dt.discussion_id = flarum_discussions.id").
		Where("dt.tag_id = ?", id).Count(&used)
	if used > 0 {
		fail(c, http.StatusBadRequest, "该版块下仍有讨论，无法删除。")
		return
	}
	s.db.Delete(&t)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

type adminUserRow struct {
	User     *UserDTO `json:"user"`
	Email    string   `json:"email"`
	GroupIDs []uint   `json:"groupIds"`
}

func (s *Server) adminUsers(c *gin.Context) {
	q := strings.TrimSpace(c.Query("q"))
	base := s.db.Model(&models.User{})
	if q != "" {
		if uid, err := strconv.ParseUint(q, 10, 32); err == nil {
			base = base.Where("id = ? OR username LIKE ? OR email LIKE ?", uid, "%"+q+"%", "%"+q+"%")
		} else {
			base = base.Where("username LIKE ? OR email LIKE ?", "%"+q+"%", "%"+q+"%")
		}
	}

	var total int64
	base.Session(&gorm.Session{}).Count(&total)

	perPage := perPageUsers
	if pp, err := strconv.Atoi(c.Query("per_page")); err == nil && pp > 0 && pp <= 100 {
		perPage = pp
	}

	page := pageParam(c)
	pages := paginate(int(total), page, perPage)
	if page > pages {
		page = pages
	}

	orderClause := "id ASC"
	if c.Query("sort") == "desc" || c.Query("order") == "desc" {
		orderClause = "id DESC"
	}

	offset := (page - 1) * perPage
	if offset < 0 {
		offset = 0
	}

	var users []models.User
	base.Session(&gorm.Session{}).
		Preload("Groups").
		Order(orderClause).
		Limit(perPage).Offset(offset).
		Find(&users)

	rows := make([]adminUserRow, 0, len(users))
	for i := range users {
		u := &users[i]
		rows = append(rows, adminUserRow{User: userDTO(u), Email: u.Email, GroupIDs: u.GroupIDs()})
	}
	c.JSON(http.StatusOK, gin.H{
		"users": rows,
		"page":  PageDTO{Page: page, Pages: pages, Total: int(total)},
	})
}

func (s *Server) adminUserSave(c *gin.Context) {
	id, _ := parseUint(c.Param("id"))
	var u models.User
	if err := s.db.First(&u, id).Error; err != nil {
		fail(c, http.StatusNotFound, "用户不存在。")
		return
	}
	viewer := s.currentUser(c)
	var body struct {
		Action string `json:"action"`
		Groups []uint `json:"groups"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		fail(c, http.StatusBadRequest, "请求格式错误。")
		return
	}
	switch body.Action {
	case "ban":
		// flarum/suspend 语义：禁言到未来时间（真实列名 suspended_until）
		until := time.Now().Add(100 * 365 * 24 * time.Hour)
		s.db.Model(&u).Updates(map[string]any{
			"suspended_until": until,
		})
		s.db.Where("user_id = ?", u.ID).Delete(&models.AccessToken{})
	case "unban":
		s.db.Model(&u).Updates(map[string]any{"suspended_until": nil})
	case "groups":
		var groups []models.Group
		for _, gid := range body.Groups {
			if gid == models.GroupGuest {
				continue
			}
			// 编辑自己时不允许移除自己的管理员组（防自锁）
			if u.ID == viewer.ID && gid != models.GroupAdmin {
				continue
			}
			groups = append(groups, models.Group{ID: gid})
		}
		err := s.db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&u).Association("Groups").Replace(groups); err != nil {
				return err
			}
			if u.ID == viewer.ID {
				return tx.Model(&u).Association("Groups").Append(&models.Group{ID: models.GroupAdmin})
			}
			return nil
		})
		if err != nil {
			fail(c, http.StatusInternalServerError, "保存失败。")
			return
		}
	default:
		fail(c, http.StatusBadRequest, "未知操作。")
		return
	}
	s.db.Preload("Groups").First(&u, id)
	c.JSON(http.StatusOK, gin.H{"ok": true, "user": userDTO(&u), "groupIds": u.GroupIDs()})
}

type inviteDTO struct {
	Code      string     `json:"code"`
	CreatedAt time.Time  `json:"createdAt"`
	ExpiresAt *time.Time `json:"expiresAt"`
	UsedAt    *time.Time `json:"usedAt"`
	Expired   bool       `json:"expired"`
}

func (s *Server) adminInvites(c *gin.Context) {
	var invites []models.InviteCode
	s.db.Order("created_at DESC").Limit(100).Find(&invites)
	now := time.Now()
	out := make([]inviteDTO, 0, len(invites))
	for _, inv := range invites {
		out = append(out, inviteDTO{
			Code: inv.Code, CreatedAt: inv.CreatedAt,
			ExpiresAt: inv.ExpiresAt, UsedAt: inv.UsedAt,
			Expired: inv.ExpiresAt != nil && inv.ExpiresAt.Before(now),
		})
	}
	c.JSON(http.StatusOK, gin.H{"invites": out})
}

func (s *Server) adminInviteCreate(c *gin.Context) {
	viewer := s.currentUser(c)
	var body struct {
		Count       int `json:"count"`
		ExpiresDays int `json:"expiresDays"`
	}
	_ = c.ShouldBindJSON(&body)
	n := body.Count
	if n <= 0 || n > 50 {
		n = 1
	}
	now := time.Now()
	var expires *time.Time
	if body.ExpiresDays > 0 {
		t := now.Add(time.Duration(body.ExpiresDays) * 24 * time.Hour)
		expires = &t
	}
	codes := make([]string, 0, n)
	for i := 0; i < n; i++ {
		code, _ := randHex(5)
		code = strings.ToUpper(code)[:10]
		inv := models.InviteCode{Code: code, CreatedBy: viewer.ID, ExpiresAt: expires, CreatedAt: now}
		if err := s.db.Create(&inv).Error; err != nil {
			fail(c, http.StatusInternalServerError, "生成失败。")
			return
		}
		codes = append(codes, code)
	}
	c.JSON(http.StatusOK, gin.H{"codes": codes})
}

func (s *Server) adminHidden(c *gin.Context) {
	var discs []models.Discussion
	s.db.Where("hidden_at IS NOT NULL").Preload("User").Preload("Tags").
		Order("hidden_at DESC").Limit(100).Find(&discs)
	c.JSON(http.StatusOK, gin.H{"items": s.summaries(discs, s.currentUser(c))})
}

func (s *Server) adminRestore(c *gin.Context) {
	id, _ := parseUint(c.Param("id"))
	s.db.Model(&models.Discussion{}).Where("id = ?", id).Update("hidden_at", nil)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) adminSettingsGet(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"siteName":     s.settingValue(models.SettingKeySiteName),
		"registration": s.settingValue(models.SettingKeyRegistration),
		"welcomeText":  s.settingValue(models.SettingKeyWelcomeText),
	})
}

func (s *Server) adminSettingsSave(c *gin.Context) {
	var body struct {
		SiteName     string `json:"siteName"`
		Registration string `json:"registration"`
		WelcomeText  string `json:"welcomeText"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		fail(c, http.StatusBadRequest, "请求格式错误。")
		return
	}
	registration := body.Registration
	if registration != "open" && registration != "invite" {
		registration = "invite"
	}
	s.db.Save(&models.Setting{Key: models.SettingKeySiteName, Value: strings.TrimSpace(body.SiteName)})
	s.db.Save(&models.Setting{Key: models.SettingKeyRegistration, Value: registration})
	s.db.Save(&models.Setting{Key: models.SettingKeyWelcomeText, Value: strings.TrimSpace(body.WelcomeText)})
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
