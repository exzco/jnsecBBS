// 管理后台业务：版块维护、用户管理、邀请码与站点设置。
package service

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/exzco/bbs/internal/auth"
	"github.com/exzco/bbs/internal/cache"
	"github.com/exzco/bbs/internal/models"
	"github.com/exzco/bbs/internal/slug"
	"gorm.io/gorm"
)

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9\-]*$`)
var colorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// AdminMeta 管理页公共数据：组列表 + 大版块列表。
type AdminMeta struct {
	Groups  []GroupDTO `json:"groups"`
	TopTags []TagDTO   `json:"topTags"`
}

// AdminMeta 组装管理页公共数据。
func (s *Service) AdminMeta() AdminMeta {
	var groups []models.Group
	s.db.Where("is_hidden = ?", false).Order("id").Find(&groups)
	var tops []models.Tag
	s.db.Where("parent_id IS NULL").Order("position ASC, id ASC").Find(&tops)
	td := make([]TagDTO, 0, len(tops))
	for i := range tops {
		td = append(td, s.TagDTO(&tops[i], nil))
	}
	return AdminMeta{Groups: groupDTOs(groups), TopTags: td}
}

// AdminTagNode 管理页版块树节点。
type AdminTagNode struct {
	Tag      TagDTO         `json:"tag"`
	ViewIDs  []uint         `json:"viewIds"`
	PostIDs  []uint         `json:"postIds"`
	Children []AdminTagNode `json:"children"`
}

// AdminTags 全量版块树（含权限组配置）。
func (s *Service) AdminTags() []AdminTagNode {
	var tags []models.Tag
	s.db.Order("position ASC, id ASC").Find(&tags)
	tp := s.FillTagPerms(tags)

	tops, byParent := groupTags(tags)
	node := func(t models.Tag) AdminTagNode {
		return AdminTagNode{
			Tag:      s.TagDTO(&t, nil),
			ViewIDs:  tp.View[t.ID],
			PostIDs:  tp.Post[t.ID],
			Children: []AdminTagNode{},
		}
	}
	nodes := make([]AdminTagNode, 0, len(tops))
	for _, t := range tops {
		n := node(t)
		for _, ch := range byParent[t.ID] {
			n.Children = append(n.Children, node(ch))
		}
		nodes = append(nodes, n)
	}
	return nodes
}

// TagInput 版块保存请求。
type TagInput struct {
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

// SaveTag 新建/更新版块及其权限（单事务）。
func (s *Service) SaveTag(in TagInput) *Error {
	name := strings.TrimSpace(in.Name)
	slugIn := strings.ToLower(strings.TrimSpace(in.Slug))
	color := strings.TrimSpace(in.Color)
	if !colorRe.MatchString(color) {
		color = ""
	}
	if name == "" {
		return Err(http.StatusBadRequest, "版块名称不能为空。")
	}
	if slugIn == "" {
		slugIn = slug.Make(name)
	}
	if !slugRe.MatchString(slugIn) {
		return Err(http.StatusBadRequest, "URL 标识需为字母、数字或连字符（中文留空可自动生成）。")
	}
	var parentID *uint
	if in.ParentID > 0 && in.ParentID != in.ID {
		parentID = &in.ParentID
	}
	position := in.Position
	err := s.db.Transaction(func(tx *gorm.DB) error {
		id := in.ID
		if in.ID == 0 {
			t := models.Tag{
				Name: name, Slug: slugIn, Description: strings.TrimSpace(in.Description),
				Color: color, Position: &position, ParentID: parentID,
			}
			if err := tx.Create(&t).Error; err != nil {
				return err
			}
			id = t.ID
		} else {
			t := models.Tag{ID: in.ID}
			if err := tx.First(&t).Error; err != nil {
				return err
			}
			if err := tx.Model(&t).Updates(map[string]any{
				"name": name, "slug": slugIn, "description": strings.TrimSpace(in.Description),
				"color": color, "position": in.Position, "parent_id": parentID,
			}).Error; err != nil {
				return err
			}
		}
		return auth.SaveTagPerms(tx, id, in.ViewGroups, in.PostGroups)
	})
	if err != nil {
		return Err(http.StatusBadRequest, "保存失败："+err.Error())
	}
	// 版块行与授权行已变更，显式失效 tag:perms:all（Cache-Aside 写路径）。
	cache.Default.Delete(cache.TagPermsKey())
	return nil
}

// DeleteTag 删除空版块（无子版块且无讨论）。
func (s *Service) DeleteTag(id uint) *Error {
	var t models.Tag
	if err := s.db.First(&t, id).Error; err != nil {
		return Err(http.StatusNotFound, "版块不存在。")
	}
	var children int64
	s.db.Model(&models.Tag{}).Where("parent_id = ?", id).Count(&children)
	if children > 0 {
		return Err(http.StatusBadRequest, "请先删除其子版块。")
	}
	var used int64
	s.db.Model(&models.Discussion{}).Joins("JOIN flarum_discussion_tag dt ON dt.discussion_id = flarum_discussions.id").
		Where("dt.tag_id = ?", id).Count(&used)
	if used > 0 {
		return Err(http.StatusBadRequest, "该版块下仍有讨论，无法删除。")
	}
	if err := s.db.Delete(&t).Error; err != nil {
		return Err(http.StatusInternalServerError, "删除版块失败。")
	}
	// 版块行已删除，显式失效 tag:perms:all（Cache-Aside 写路径）。
	cache.Default.Delete(cache.TagPermsKey())
	return nil
}

// AdminUserRow 管理页用户行。
type AdminUserRow struct {
	User     *UserDTO `json:"user"`
	Email    string   `json:"email"`
	GroupIDs []uint   `json:"groupIds"`
}

// AdminUsers 管理页用户列表（搜索、分页、排序；loadAll 一次取全量）。
func (s *Service) AdminUsers(q string, page, perPage int, loadAll, desc bool) ([]AdminUserRow, PageDTO) {
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

	pages := Paginate(int(total), page, perPage)
	if page > pages {
		page = pages
	}
	orderClause := "id ASC"
	if desc {
		orderClause = "id DESC"
	}
	offset := (page - 1) * perPage
	if loadAll {
		// 全量：单页容纳全部行
		if total == 0 {
			total = 1
		}
		page, pages, perPage, offset = 1, 1, int(total), 0
	}

	var users []models.User
	base.Session(&gorm.Session{}).
		Preload("Groups").
		Order(orderClause).
		Limit(perPage).Offset(offset).
		Find(&users)

	rows := make([]AdminUserRow, 0, len(users))
	for i := range users {
		u := &users[i]
		rows = append(rows, AdminUserRow{User: s.UserDTO(u), Email: u.Email, GroupIDs: u.GroupIDs()})
	}
	return rows, PageDTO{Page: page, Pages: pages, Total: int(total)}
}

// adminUserActions 管理端用户操作分发表；新增操作实现同签名方法后追加一行。
var adminUserActions = map[string]func(*Service, *models.User, *models.User, []uint) *Error{
	"ban":    (*Service).banUser,
	"unban":  (*Service).unbanUser,
	"groups": (*Service).setUserGroups,
}

// AdminUserSave 管理员用户操作入口。
func (s *Service) AdminUserSave(viewer *models.User, id uint, action string, groupIDs []uint) (*UserDTO, []uint, *Error) {
	var u models.User
	if err := s.db.First(&u, id).Error; err != nil {
		return nil, nil, Err(http.StatusNotFound, "用户不存在。")
	}
	act, ok := adminUserActions[action]
	if !ok {
		return nil, nil, Err(http.StatusBadRequest, "未知操作。")
	}
	if err := act(s, viewer, &u, groupIDs); err != nil {
		return nil, nil, err
	}
	s.db.Preload("Groups").First(&u, id)
	return s.UserDTO(&u), u.GroupIDs(), nil
}

// banUser 封禁：禁言到未来时间并踢掉全部会话（flarum/suspend 语义，真实列名 suspended_until）。
func (s *Service) banUser(_, u *models.User, _ []uint) *Error {
	until := time.Now().Add(100 * 365 * 24 * time.Hour)
	if err := s.db.Model(u).Updates(map[string]any{"suspended_until": until}).Error; err != nil {
		return Err(http.StatusInternalServerError, "操作失败，请重试。")
	}
	if err := s.db.Where("user_id = ?", u.ID).Delete(&models.AccessToken{}).Error; err != nil {
		return Err(http.StatusInternalServerError, "操作失败，请重试。")
	}
	// 用户资料与全部会话均已变更（token 行被删），显式失效 user:id 与全会话缓存。
	cache.Default.Delete(cache.UserIDKey(u.ID))
	cache.Default.DeletePrefix(cache.SessionsPrefix())
	return nil
}

// unbanUser 解除封禁。
func (s *Service) unbanUser(_, u *models.User, _ []uint) *Error {
	if err := s.db.Model(u).Updates(map[string]any{"suspended_until": nil}).Error; err != nil {
		return Err(http.StatusInternalServerError, "操作失败，请重试。")
	}
	// 用户资料已变更，显式失效 user:id（Cache-Aside 写路径）。
	cache.Default.Delete(cache.UserIDKey(u.ID))
	return nil
}

// setUserGroups 修改用户组；编辑自己时不允许移除自己的管理员组（防自锁）。
func (s *Service) setUserGroups(viewer, u *models.User, groupIDs []uint) *Error {
	var groups []models.Group
	for _, gid := range groupIDs {
		if gid == models.GroupGuest {
			continue
		}
		if u.ID == viewer.ID && gid != models.GroupAdmin {
			continue
		}
		groups = append(groups, models.Group{ID: gid})
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(u).Association("Groups").Replace(groups); err != nil {
			return err
		}
		if u.ID == viewer.ID {
			return tx.Model(u).Association("Groups").Append(&models.Group{ID: models.GroupAdmin})
		}
		return nil
	})
	if err != nil {
		return Err(http.StatusInternalServerError, "保存失败。")
	}
	// 用户组已变更，显式失效 user:id（Cache-Aside 写路径）。
	cache.Default.Delete(cache.UserIDKey(u.ID))
	return nil
}

// InviteDTO 邀请码行。
type InviteDTO struct {
	Code      string     `json:"code"`
	CreatedAt time.Time  `json:"createdAt"`
	ExpiresAt *time.Time `json:"expiresAt"`
	UsedAt    *time.Time `json:"usedAt"`
	Expired   bool       `json:"expired"`
}

// ListInvites 最近 100 个邀请码。
func (s *Service) ListInvites() []InviteDTO {
	var invites []models.InviteCode
	s.db.Order("created_at DESC").Limit(100).Find(&invites)
	now := time.Now()
	out := make([]InviteDTO, 0, len(invites))
	for _, inv := range invites {
		out = append(out, InviteDTO{
			Code: inv.Code, CreatedAt: inv.CreatedAt,
			ExpiresAt: inv.ExpiresAt, UsedAt: inv.UsedAt,
			Expired: inv.ExpiresAt != nil && inv.ExpiresAt.Before(now),
		})
	}
	return out
}

// CreateInvites 批量生成邀请码（1-50 个）。
func (s *Service) CreateInvites(viewer *models.User, count, expiresDays int) ([]string, *Error) {
	n := count
	if n <= 0 || n > 50 {
		n = 1
	}
	now := time.Now()
	var expires *time.Time
	if expiresDays > 0 {
		t := now.Add(time.Duration(expiresDays) * 24 * time.Hour)
		expires = &t
	}
	codes := make([]string, 0, n)
	for i := 0; i < n; i++ {
		code, _ := randHex(5)
		code = strings.ToUpper(code)[:10]
		inv := models.InviteCode{Code: code, CreatedBy: viewer.ID, ExpiresAt: expires, CreatedAt: now}
		if err := s.db.Create(&inv).Error; err != nil {
			return nil, Err(http.StatusInternalServerError, "生成失败。")
		}
		codes = append(codes, code)
	}
	return codes, nil
}

// AdminHidden 最近隐藏的 100 个讨论。
func (s *Service) AdminHidden(viewer *models.User) []DiscussionSummaryDTO {
	var discs []models.Discussion
	s.db.Where("hidden_at IS NOT NULL").Preload("User").Preload("Tags").
		Order("hidden_at DESC").Limit(100).Find(&discs)
	return s.Summaries(discs, viewer)
}

// AdminRestore 恢复隐藏讨论。
func (s *Service) AdminRestore(id uint) error {
	return s.db.Model(&models.Discussion{}).Where("id = ?", id).Update("hidden_at", nil).Error
}

// SettingsInput 站点设置保存请求。
type SettingsInput struct {
	SiteName     string `json:"siteName"`
	Registration string `json:"registration"`
	WelcomeText  string `json:"welcomeText"`
}

// SaveSettings 保存站点设置并失效对应缓存。
func (s *Service) SaveSettings(in SettingsInput) *Error {
	registration := in.Registration
	if registration != "open" && registration != "invite" {
		registration = "invite"
	}
	settings := []models.Setting{
		{Key: models.SettingKeySiteName, Value: strings.TrimSpace(in.SiteName)},
		{Key: models.SettingKeyRegistration, Value: registration},
		{Key: models.SettingKeyWelcomeText, Value: strings.TrimSpace(in.WelcomeText)},
	}
	for i := range settings {
		if err := s.db.Save(&settings[i]).Error; err != nil {
			return Err(http.StatusInternalServerError, "保存失败，请重试。")
		}
	}
	// 设置行已变更，显式失效对应 setting:{key} 缓存（Cache-Aside 写路径）。
	cache.Default.Delete(cache.SettingKey(models.SettingKeySiteName))
	cache.Default.Delete(cache.SettingKey(models.SettingKeyRegistration))
	cache.Default.Delete(cache.SettingKey(models.SettingKeyWelcomeText))
	return nil
}
