package db

import (
	"fmt"

	"github.com/exzco/bbs/internal/models"
	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Open only connects. Schema changes require init-db or migrate-db explicitly.
func Open(driver, dsn string) (*gorm.DB, error) {
	var dialector gorm.Dialector
	switch driver {
	case "mysql":
		dialector = mysql.Open(dsn)
	case "sqlite":
		dialector = sqlite.Open(dsn)
	default:
		return nil, fmt.Errorf("unsupported database driver: %s", driver)
	}
	return gorm.Open(dialector, &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
}

// checkFlarumTables confirms that the legacy schema has already been installed.
// 缺表（如 DSN 指到空库、或扩展未安装）时给出可读的报错，而不是在请求半路崩。
func checkFlarumTables(g *gorm.DB) error {
	required := []string{
		"flarum_users", "flarum_groups", "flarum_group_user",
		"flarum_discussions", "flarum_posts", "flarum_tags",
		"flarum_discussion_tag", "flarum_post_likes",
		"flarum_access_tokens", "flarum_settings", "flarum_group_permission",
	}
	var missing []string
	for _, t := range required {
		if err := g.Exec("SELECT 1 FROM `" + t + "` LIMIT 1").Error; err != nil {
			missing = append(missing, t)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("数据库缺少所需 Flarum 表（空库请先运行 bbs init-db）: %v", missing)
	}
	return nil
}

// SeedDefaults is used only by explicit standalone initialization.
func SeedDefaults(g *gorm.DB) error {
	var n int64
	if err := g.Model(&models.Group{}).Count(&n).Error; err != nil {
		return err
	}
	if n == 0 {
		groups := []models.Group{
			{ID: models.GroupAdmin, NameSingular: "Admin", NamePlural: "Admin", Color: "#e53935", Icon: "fas fa-wrench"},
			{ID: models.GroupGuest, NameSingular: "Guest", NamePlural: "Guest", Color: "#9e9e9e", Icon: "fas fa-reply", IsHidden: true},
			{ID: models.GroupMember, NameSingular: "成员", NamePlural: "正式成员", Color: "#1e88e5", Icon: "fas fa-user"},
			{ID: models.GroupMod, NameSingular: "版主", NamePlural: "版主", Color: "#43a047", Icon: "fas fa-bolt"},
		}
		if err := g.Create(&groups).Error; err != nil {
			return err
		}
	}

	return SeedDefaultTags(g)
}

// SeedDefaultTags gives new installations a neutral board. Existing forum
// taxonomy and site title remain owned by the administrator.
func SeedDefaultTags(g *gorm.DB) error {
	var n int64
	if err := g.Model(&models.Tag{}).Count(&n).Error; err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	position := 1
	return g.Create(&models.Tag{Name: "综合讨论", Slug: "general", Color: "#1e88e5", Position: &position}).Error
}

// InitializeEmpty explicitly initializes a standalone database. Never run
// AutoMigrate against an existing Flarum schema (including partial installs).
func InitializeEmpty(g *gorm.DB) error {
	tables, err := g.Migrator().GetTables()
	if err != nil {
		return err
	}
	if len(tables) != 0 {
		return fmt.Errorf("init-db requires an empty database; found %d tables", len(tables))
	}
	if err := migrateModels(g); err != nil {
		return err
	}
	if err := Migrate(g); err != nil {
		return err
	}
	_ = CleanupJoinTableDuplicates(g)
	return SeedDefaults(g)
}

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

func migrateModels(g *gorm.DB) error {
	if err := g.AutoMigrate(
		&models.User{}, &models.Group{}, &models.Discussion{}, &models.Post{},
		&models.PostLike{}, &models.Tag{}, &models.Notification{}, &models.AccessToken{},
		&models.InviteCode{}, &models.Setting{}, &models.GroupPermission{}, &models.UserProfile{},
		&models.DiscussionUser{},
	); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

// CheckReadOnly verifies an initialized mirror without DDL or seed writes.
func CheckReadOnly(g *gorm.DB) error {
	if err := checkFlarumTables(g); err != nil {
		return err
	}
	for _, table := range []string{"bbs_notifications", "bbs_invite_codes"} {
		if err := g.Exec("SELECT 1 FROM `" + table + "` LIMIT 1").Error; err != nil {
			return fmt.Errorf("database missing %s; run bbs migrate-db explicitly", table)
		}
	}
	return nil
}

// CheckWritable validates an upgraded database without DDL or seed writes.
func CheckWritable(g *gorm.DB) error {
	if err := CheckReadOnly(g); err != nil {
		return err
	}
	if err := g.Exec("SELECT user_id, real_name, blog_url, updated_at FROM bbs_user_profiles LIMIT 0").Error; err != nil {
		return fmt.Errorf("user profile schema unavailable; run bbs migrate-db: %w", err)
	}
	applied, err := migrationHistory(g)
	if err != nil {
		return fmt.Errorf("migration history unavailable; run bbs migrate-db: %w", err)
	}
	if len(applied) != len(migrations) {
		return fmt.Errorf("database has pending migrations; run bbs migrate-db")
	}
	return nil
}
