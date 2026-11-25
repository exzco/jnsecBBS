package db

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

type schemaMigration struct {
	Version   int       `gorm:"column:version;primaryKey;autoIncrement:false"`
	Name      string    `gorm:"column:name"`
	AppliedAt time.Time `gorm:"column:applied_at"`
}

func (schemaMigration) TableName() string { return "bbs_schema_migrations" }

type migration struct {
	version int
	name    string
	apply   func(*gorm.DB) error
}

var migrations = []migration{
	{1, "go_notifications_and_invites", createNotificationsAndInvites},
	{2, "go_user_profiles", createUserProfiles},
	{3, "go_email_outbox", createEmailOutbox},
	{4, "go_email_bind", createEmailBind},
	{5, "go_discussion_read_positions", createDiscussionReadPositions},
}

// Migrate explicitly upgrades Go-owned tables in an existing Flarum mirror.
// Never use AutoMigrate here: it follows model associations into legacy tables.
// MySQL DDL commits implicitly, so each step is idempotent and gets a version
// only after all of its statements succeed. An interrupted command can be retried.
func Migrate(g *gorm.DB) error {
	if err := checkFlarumTables(g); err != nil {
		return err
	}
	suffix := ""
	if g.Dialector.Name() == "mysql" {
		suffix = " ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci"
	}
	if err := g.Exec(`CREATE TABLE IF NOT EXISTS bbs_schema_migrations (
		version integer NOT NULL PRIMARY KEY,
		name varchar(100) NOT NULL,
		applied_at datetime NOT NULL
	)` + suffix).Error; err != nil {
		return fmt.Errorf("create migration history: %w", err)
	}
	applied, err := migrationHistory(g)
	if err != nil {
		return err
	}
	for _, m := range migrations[len(applied):] {
		apply := func(tx *gorm.DB) error {
			if err := m.apply(tx); err != nil {
				return err
			}
			return tx.Create(&schemaMigration{Version: m.version, Name: m.name, AppliedAt: time.Now().UTC()}).Error
		}
		if g.Dialector.Name() == "sqlite" {
			err = g.Transaction(apply)
		} else {
			err = apply(g)
		}
		if err != nil {
			return fmt.Errorf("migration %d (%s): %w", m.version, m.name, err)
		}
	}
	return nil
}

func migrationHistory(g *gorm.DB) ([]schemaMigration, error) {
	var applied []schemaMigration
	if err := g.Order("version").Find(&applied).Error; err != nil {
		return nil, err
	}
	for i, row := range applied {
		if i >= len(migrations) || row.Version != migrations[i].version || row.Name != migrations[i].name {
			return nil, fmt.Errorf("unsupported migration history at version %d (%s)", row.Version, row.Name)
		}
	}
	return applied, nil
}

func createNotificationsAndInvites(g *gorm.DB) error {
	var statements []string
	if g.Dialector.Name() == "mysql" {
		statements = []string{
			`CREATE TABLE IF NOT EXISTS bbs_notifications (
				id int unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY,
				user_id int unsigned NOT NULL,
				sender_id int unsigned NOT NULL,
				type varchar(100) NOT NULL,
				discussion_id int unsigned NOT NULL,
				post_id int unsigned NOT NULL,
				read_at datetime DEFAULT NULL,
				created_at datetime NOT NULL,
				KEY idx_bbs_notif (user_id, type)
			) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
			`CREATE TABLE IF NOT EXISTS bbs_invite_codes (
				code varchar(20) NOT NULL PRIMARY KEY,
				created_by int unsigned NOT NULL,
				used_by int unsigned DEFAULT NULL,
				used_at datetime DEFAULT NULL,
				expires_at datetime DEFAULT NULL,
				created_at datetime NOT NULL
			) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
		}
	} else {
		statements = []string{
			`CREATE TABLE IF NOT EXISTS bbs_notifications (
				id integer PRIMARY KEY AUTOINCREMENT,
				user_id integer NOT NULL,
				sender_id integer NOT NULL,
				type varchar(100) NOT NULL,
				discussion_id integer NOT NULL,
				post_id integer NOT NULL,
				read_at datetime DEFAULT NULL,
				created_at datetime NOT NULL
			)`,
			`CREATE INDEX IF NOT EXISTS idx_bbs_notif ON bbs_notifications (user_id, type)`,
			`CREATE TABLE IF NOT EXISTS bbs_invite_codes (
				code varchar(20) NOT NULL PRIMARY KEY,
				created_by integer NOT NULL,
				used_by integer DEFAULT NULL,
				used_at datetime DEFAULT NULL,
				expires_at datetime DEFAULT NULL,
				created_at datetime NOT NULL
			)`,
		}
	}
	for _, statement := range statements {
		if err := g.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

func createUserProfiles(g *gorm.DB) error {
	idType, suffix := "integer", ""
	if g.Dialector.Name() == "mysql" {
		idType = "int unsigned"
		suffix = " ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci"
	}
	return g.Exec(`CREATE TABLE IF NOT EXISTS bbs_user_profiles (
		user_id ` + idType + ` NOT NULL PRIMARY KEY,
		real_name varchar(100) NOT NULL DEFAULT '',
		blog_url varchar(255) NOT NULL DEFAULT '',
		updated_at datetime NOT NULL
	)` + suffix).Error
}

func createEmailOutbox(g *gorm.DB) error {
	suffix := ""
	idColumn := "id integer PRIMARY KEY AUTOINCREMENT"
	attemptsColumn := "attempts integer NOT NULL DEFAULT 0"
	if g.Dialector.Name() == "mysql" {
		suffix = " ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci"
		idColumn = "id int unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY"
		attemptsColumn = "attempts int unsigned NOT NULL DEFAULT 0"
	}
	statements := []string{
		`CREATE TABLE IF NOT EXISTS bbs_email_outbox (
			` + idColumn + `,
			recipient varchar(255) NOT NULL,
			subject varchar(255) NOT NULL,
			body text NOT NULL,
			` + attemptsColumn + `,
			sent_at datetime DEFAULT NULL,
			last_error varchar(500) DEFAULT NULL,
			created_at datetime NOT NULL
		)` + suffix,
	}
	if g.Dialector.Name() != "mysql" {
		statements = append(statements, `CREATE INDEX IF NOT EXISTS idx_bbs_email_outbox ON bbs_email_outbox (sent_at, id)`)
	}
	for _, statement := range statements {
		if err := g.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

// createEmailBind 建邮箱绑定表，并把存量用户（注册邮箱非空）回填为已绑定——
// 绑定通知规则仅对上线后的新注册用户生效，老用户保持原有接收行为。
func createEmailBind(g *gorm.DB) error {
	idType, suffix := "integer", ""
	if g.Dialector.Name() == "mysql" {
		idType = "int unsigned"
		suffix = " ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci"
	}
	statements := []string{
		`CREATE TABLE IF NOT EXISTS bbs_email_bind (
			user_id ` + idType + ` NOT NULL PRIMARY KEY,
			bound_at datetime NOT NULL
		)` + suffix,
		`INSERT INTO bbs_email_bind (user_id, bound_at)
			SELECT id, CURRENT_TIMESTAMP FROM flarum_users
			WHERE email <> '' AND id NOT IN (SELECT user_id FROM bbs_email_bind)`,
	}
	for _, statement := range statements {
		if err := g.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

// createDiscussionReadPositions 建帖子阅读楼层进度表（按账户跨端同步“读到哪一楼”）。
func createDiscussionReadPositions(g *gorm.DB) error {
	idType, numType, suffix := "integer", "integer", ""
	if g.Dialector.Name() == "mysql" {
		idType = "int unsigned"
		numType = "int unsigned"
		suffix = " ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci"
	}
	return g.Exec(`CREATE TABLE IF NOT EXISTS bbs_discussion_read_positions (
		user_id ` + idType + ` NOT NULL,
		discussion_id ` + idType + ` NOT NULL,
		post_id ` + idType + ` NOT NULL DEFAULT 0,
		post_number ` + numType + ` NOT NULL DEFAULT 0,
		updated_at datetime NOT NULL,
		PRIMARY KEY (user_id, discussion_id)
	)` + suffix).Error
}
