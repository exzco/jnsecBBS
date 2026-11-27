package auth

import (
	"regexp"
	"strconv"
	"time"

	"github.com/exzco/bbs/internal/infra/cache"
	"github.com/exzco/bbs/internal/models"
	"gorm.io/gorm"
)

// 版块权限：PHP 与 Go 共享 flarum_group_permission 表。
// Flarum/tags 的 permission 格式为 tag{ID}.viewDiscussions / tag{ID}.startDiscussion
// / tag{ID}.replyToDiscussions，每行一个 (group_id, permission)。
// 无授权行仅对非受限版块使用公开默认；受限版块和读取失败均拒绝。

var tagPermRe = regexp.MustCompile(`^tag(\d+)\.(viewDiscussions|startDiscussion|replyToDiscussions)$`)

type TagPerms struct {
	Failed bool
	Tags   map[uint]models.Tag
	Reply  map[uint][]uint
	View   map[uint][]uint // tagID -> 允许查看的组 ID 列表
	Post   map[uint][]uint // tagID -> 允许发帖/回帖的组 ID 列表
}

// LoadTagPerms 一次性加载全部版块权限。Cache-Aside：先查 tag:perms:all，
// 未命中查库并回写（TTL 带抖动）；版块/授权写路径显式失效该 Key。
// 加载失败（Failed）不缓存，避免瞬时 DB 错误被固定。
func LoadTagPerms(g *gorm.DB) TagPerms {
	if v, ok := cache.Default.Get(cache.TagPermsKey()); ok {
		if tp, ok := v.(TagPerms); ok {
			return tp
		}
	}
	tp := TagPerms{Tags: map[uint]models.Tag{}, Reply: map[uint][]uint{}, View: map[uint][]uint{}, Post: map[uint][]uint{}}
	var tags []models.Tag
	if err := g.Find(&tags).Error; err != nil {
		tp.Failed = true
		return tp
	}
	for _, tag := range tags {
		tp.Tags[tag.ID] = tag
	}
	var rows []models.GroupPermission
	if err := g.Find(&rows).Error; err != nil {
		tp.Failed = true
		return tp
	}
	for _, r := range rows {
		m := tagPermRe.FindStringSubmatch(r.Permission)
		if m == nil {
			continue // 非版块权限（其他扩展的全局权限），跳过
		}
		tagID, _ := strconv.ParseUint(m[1], 10, 32)
		switch m[2] {
		case "viewDiscussions":
			tp.View[uint(tagID)] = append(tp.View[uint(tagID)], r.GroupID)
		case "replyToDiscussions":
			tp.Reply[uint(tagID)] = append(tp.Reply[uint(tagID)], r.GroupID)
		case "startDiscussion":
			tp.Post[uint(tagID)] = append(tp.Post[uint(tagID)], r.GroupID)
		}
	}
	cache.Default.Set(cache.TagPermsKey(), tp, cache.TTL(time.Minute, 15*time.Second))
	return tp
}

// fillOwnTagPerm 把权限填充到单个版块。
func fillOwnTagPerm(tag *models.Tag, tp TagPerms) {
	tag.ViewGroups = tp.View[tag.ID]
	tag.PostGroups = tp.Post[tag.ID]
	tag.ReplyGroups = tp.Reply[tag.ID]
	if tp.Failed || (tag.IsRestricted && len(tag.ViewGroups) == 0) {
		tag.ViewGroups = []uint{0}
	}
	if tp.Failed || (tag.IsRestricted && len(tag.PostGroups) == 0) {
		tag.PostGroups = []uint{0}
	}
	if tp.Failed || (tag.IsRestricted && len(tag.ReplyGroups) == 0) {
		tag.ReplyGroups = []uint{0}
	}
}

func FillTagPerm(g *gorm.DB, tag *models.Tag, tp TagPerms) {
	fillOwnTagPerm(tag, tp)
	tag.Ancestors = nil
	seen := map[uint]bool{tag.ID: true}
	parent := tag.ParentID
	for parent != nil {
		ancestor, ok := tp.Tags[*parent]
		if !ok || seen[*parent] {
			tag.ViewGroups = []uint{0}
			tag.PostGroups = []uint{0}
			tag.ReplyGroups = []uint{0}
			break
		}
		seen[*parent] = true
		fillOwnTagPerm(&ancestor, tp)
		tag.Ancestors = append(tag.Ancestors, ancestor)
		parent = ancestor.ParentID
	}
}

// FillTagPerms 批量填充。
func FillTagPerms(g *gorm.DB, tags []models.Tag, tp TagPerms) {
	for i := range tags {
		FillTagPerm(g, &tags[i], tp)
	}
}

// CanViewTag 版块可见性：无授权行 = 任何人（含游客）可见。
func CanViewTag(u *models.User, tag *models.Tag) bool {
	for i := range tag.Ancestors {
		if !CanViewTag(u, &tag.Ancestors[i]) {
			return false
		}
	}
	if u != nil && u.IsAdmin() {
		return true
	}
	for _, group := range tag.ViewGroups {
		if group == models.GroupGuest || (group == models.GroupMember && u != nil && !u.IsBanned()) {
			return true
		}
	}
	if len(tag.ViewGroups) == 0 {
		return true
	}
	if u == nil {
		return false
	}
	return u.HasGroup(tag.ViewGroups...)
}

// CanPostTag 发帖/回帖权限：无授权行 = 任何登录用户。
func CanPostTag(u *models.User, tag *models.Tag) bool {
	for i := range tag.Ancestors {
		if !CanPostTag(u, &tag.Ancestors[i]) {
			return false
		}
	}
	if u == nil || u.IsBanned() {
		return false
	}
	if u.IsAdmin() {
		return true
	}
	for _, group := range tag.PostGroups {
		if group == models.GroupGuest || group == models.GroupMember {
			return true
		}
	}
	if len(tag.PostGroups) == 0 {
		return true
	}
	return u.HasGroup(tag.PostGroups...)
}

// SaveTagPerms 把管理后台设置的可见/发帖组写入 flarum_group_permission
// （先清除该版块的相关行，再插入；与 PHP 端格式一致）。
func SaveTagPerms(g *gorm.DB, tagID uint, viewGroups, postGroups []uint) error {
	verbs := []string{
		"tag" + strconv.FormatUint(uint64(tagID), 10) + ".viewDiscussions",
		"tag" + strconv.FormatUint(uint64(tagID), 10) + ".startDiscussion",
		"tag" + strconv.FormatUint(uint64(tagID), 10) + ".replyToDiscussions",
	}
	if err := g.Where("permission IN ?", verbs).Delete(&models.GroupPermission{}).Error; err != nil {
		return err
	}
	rows := make([]models.GroupPermission, 0, len(viewGroups)+len(postGroups)*2)
	for _, gid := range viewGroups {
		if gid == models.GroupGuest {
			continue // 游客为默认公开语义，不落行
		}
		rows = append(rows, models.GroupPermission{GroupID: gid, Permission: verbs[0]})
	}
	for _, gid := range postGroups {
		if gid == models.GroupGuest {
			continue
		}
		rows = append(rows, models.GroupPermission{GroupID: gid, Permission: verbs[1]})
		rows = append(rows, models.GroupPermission{GroupID: gid, Permission: verbs[2]})
	}
	if len(rows) > 0 {
		return g.Create(&rows).Error
	}
	return nil
}

// FilterVisibleTags 返回用户可见的版块。
func FilterVisibleTags(u *models.User, tags []models.Tag) []models.Tag {
	out := tags[:0:0]
	for i := range tags {
		if CanViewTag(u, &tags[i]) {
			out = append(out, tags[i])
		}
	}
	return out
}

// CanEditPost 本人、版主可编辑。
func CanEditPost(u *models.User, p *models.Post) bool {
	if u == nil {
		return false
	}
	return u.IsModerator() || (p.UserID != nil && *p.UserID == u.ID)
}

func CanReplyTag(u *models.User, tag *models.Tag) bool {
	copy := *tag
	copy.PostGroups = tag.ReplyGroups
	copy.Ancestors = nil
	if !CanPostTag(u, &copy) {
		return false
	}
	for i := range tag.Ancestors {
		if !CanReplyTag(u, &tag.Ancestors[i]) {
			return false
		}
	}
	return true
}
