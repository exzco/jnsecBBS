package db

import "gorm.io/gorm"

// CleanupJoinTableDuplicates removes duplicate rows from join tables and ensures
// unique indexes exist so SQLite mirrors don't accumulate duplicates.
func CleanupJoinTableDuplicates(g *gorm.DB) error {
	if g == nil || g.Dialector == nil {
		return nil
	}
	if g.Dialector.Name() == "sqlite" {
		if !g.Migrator().HasTable("flarum_discussion_tag") || !g.Migrator().HasTable("flarum_group_user") {
			return nil
		}
		_ = g.Exec(`DELETE FROM flarum_discussion_tag WHERE rowid NOT IN (
			SELECT MIN(rowid) FROM flarum_discussion_tag GROUP BY discussion_id, tag_id
		)`).Error
		_ = g.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uq_flarum_discussion_tag ON flarum_discussion_tag(discussion_id, tag_id)`).Error
		_ = g.Exec(`DELETE FROM flarum_group_user WHERE rowid NOT IN (
			SELECT MIN(rowid) FROM flarum_group_user GROUP BY user_id, group_id
		)`).Error
		_ = g.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uq_flarum_group_user ON flarum_group_user(user_id, group_id)`).Error
	}
	return nil
}

// SyncHiddenFirstPostDiscussions synchronizes flarum_discussions.hidden_at for any
// discussions whose first post was hidden (e.g. legacy Flarum data or direct post hiding).
func SyncHiddenFirstPostDiscussions(g *gorm.DB) error {
	if g == nil || g.Dialector == nil {
		return nil
	}
	if !g.Migrator().HasTable("flarum_discussions") || !g.Migrator().HasTable("flarum_posts") {
		return nil
	}
	return g.Exec(`
		UPDATE flarum_discussions
		SET hidden_at = (SELECT hidden_at FROM flarum_posts WHERE flarum_posts.id = flarum_discussions.first_post_id),
		    hidden_user_id = (SELECT hidden_user_id FROM flarum_posts WHERE flarum_posts.id = flarum_discussions.first_post_id)
		WHERE hidden_at IS NULL AND EXISTS (
		    SELECT 1 FROM flarum_posts WHERE flarum_posts.id = flarum_discussions.first_post_id AND flarum_posts.hidden_at IS NOT NULL
		)
	`).Error
}
