package db

import (
	"fmt"

	"github.com/exzco/bbs/internal/models"
	"gorm.io/gorm"
)

// CleanupJoinTableDuplicates removes duplicate rows from join tables and ensures
// unique indexes exist so SQLite mirrors don't accumulate duplicates.
func CleanupJoinTableDuplicates(g *gorm.DB) error {
	if g == nil || g.Dialector == nil {
		return nil
	}
	if g.Dialector.Name() == "sqlite" {
		if !g.Migrator().HasTable(models.TableDiscussionTag) || !g.Migrator().HasTable(models.TableGroupUser) {
			return nil
		}
		_ = g.Exec(fmt.Sprintf(`DELETE FROM %s WHERE rowid NOT IN (
			SELECT MIN(rowid) FROM %s GROUP BY discussion_id, tag_id
		)`, models.TableDiscussionTag, models.TableDiscussionTag)).Error
		_ = g.Exec(fmt.Sprintf(`CREATE UNIQUE INDEX IF NOT EXISTS uq_flarum_discussion_tag ON %s(discussion_id, tag_id)`, models.TableDiscussionTag)).Error
		_ = g.Exec(fmt.Sprintf(`DELETE FROM %s WHERE rowid NOT IN (
			SELECT MIN(rowid) FROM %s GROUP BY user_id, group_id
		)`, models.TableGroupUser, models.TableGroupUser)).Error
		_ = g.Exec(fmt.Sprintf(`CREATE UNIQUE INDEX IF NOT EXISTS uq_flarum_group_user ON %s(user_id, group_id)`, models.TableGroupUser)).Error
	}
	return nil
}
