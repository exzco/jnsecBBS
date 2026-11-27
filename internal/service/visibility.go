package service

import (
	"github.com/exzco/bbs/internal/auth"
	"github.com/exzco/bbs/internal/models"
	"gorm.io/gorm"
)

// visibleDiscussions applies visibility before pagination and aggregation, so
// multi-tag discussions cannot leak via a public tag, search, or user profile.
func visibleDiscussions(g *gorm.DB, u *models.User) *gorm.DB {
	query := g.Model(&models.Discussion{}).Where("flarum_discussions.is_private = ?", false)
	// LoadTagPerms 自带缓存（tag:perms:all）；拒绝集直接由缓存内的全量版块
	// 推导，不再额外整表扫描 flarum_tags。
	tp := auth.LoadTagPerms(g)
	if tp.Failed {
		return query.Where("1 = 0")
	}
	denied := []uint{}
	for id, tag := range tp.Tags {
		t := tag
		auth.FillTagPerm(g, &t, tp)
		if !auth.CanViewTag(u, &t) {
			denied = append(denied, id)
		}
	}
	if len(denied) > 0 {
		query = query.Where("NOT EXISTS (SELECT 1 FROM flarum_discussion_tag access_tag WHERE access_tag.discussion_id = flarum_discussions.id AND access_tag.tag_id IN ?)", denied)
	}
	return query
}

func visiblePosts(g *gorm.DB, u *models.User) *gorm.DB {
	ids := visibleDiscussions(g, u).Select("flarum_discussions.id").Where("flarum_discussions.hidden_at IS NULL AND flarum_discussions.is_approved = ?", true)
	return g.Model(&models.Post{}).Where("discussion_id IN (?) AND is_private = ?", ids, false)
}
