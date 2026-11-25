package db

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/exzco/bbs/internal/models"
	"gorm.io/gorm"
)

func migrationTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	g, err := Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := g.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { sqlDB.Close() })
	return g
}

func TestExplicitMigrationsPreserveLegacySchemaAndData(t *testing.T) {
	g := migrationTestDB(t)
	if err := InitializeEmpty(g); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"DROP TABLE bbs_schema_migrations",
		"DROP TABLE bbs_user_profiles",
		"DROP TABLE bbs_notifications",
		"DROP TABLE bbs_invite_codes",
		"ALTER TABLE flarum_users ADD COLUMN legacy_extension TEXT",
		"CREATE TABLE flarum_plugin_state (id integer PRIMARY KEY, payload TEXT)",
		"INSERT INTO flarum_plugin_state VALUES (42, 'preserve legacy payload')",
	} {
		if err := g.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	type schemaRow struct{ Name, SQL string }
	var before, after []schemaRow
	const legacySchema = "SELECT name, sql FROM sqlite_master WHERE name LIKE 'flarum_%' ORDER BY name"
	if err := g.Raw(legacySchema).Scan(&before).Error; err != nil {
		t.Fatal(err)
	}
	if err := Migrate(g); err != nil {
		t.Fatal(err)
	}
	if err := CheckWritable(g); err != nil {
		t.Fatal(err)
	}
	if err := g.Raw(legacySchema).Scan(&after).Error; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("legacy schema changed: before=%+v after=%+v", before, after)
	}
	var payload string
	if err := g.Raw("SELECT payload FROM flarum_plugin_state WHERE id = 42").Scan(&payload).Error; err != nil || payload != "preserve legacy payload" {
		t.Fatalf("legacy payload=%q err=%v", payload, err)
	}
	history, err := migrationHistory(g)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != len(migrations) {
		t.Fatalf("migration history=%+v", history)
	}
	if !g.Migrator().HasTable("bbs_discussion_read_positions") {
		t.Fatal("missing bbs_discussion_read_positions table after Migrate")
	}
	for _, row := range history {
		if row.AppliedAt.IsZero() {
			t.Fatal("migration timestamp missing")
		}
	}
	profile := models.UserProfile{UserID: 42, RealName: "本地资料", BlogURL: "https://example.org", UpdatedAt: time.Now()}
	if err := g.Create(&profile).Error; err != nil {
		t.Fatal(err)
	}
	if err := Migrate(g); err != nil {
		t.Fatal(err)
	}
	again, err := migrationHistory(g)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(history, again) {
		t.Fatalf("repeated migration changed version history: before=%+v after=%+v", history, again)
	}
	var preserved models.UserProfile
	if err := g.First(&preserved, "user_id = ?", profile.UserID).Error; err != nil || preserved.RealName != profile.RealName || preserved.BlogURL != profile.BlogURL {
		t.Fatalf("local profile changed: %+v err=%v", preserved, err)
	}
}

func TestMigrationFailureDoesNotAdvanceVersionAndCanRetry(t *testing.T) {
	g := migrationTestDB(t)
	if err := InitializeEmpty(g); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"DELETE FROM bbs_schema_migrations WHERE version >= 2",
		"DROP TABLE bbs_user_profiles",
		"CREATE TRIGGER reject_migration BEFORE INSERT ON bbs_schema_migrations WHEN NEW.version >= 2 BEGIN SELECT RAISE(ABORT, 'version write failed'); END",
	} {
		if err := g.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := Migrate(g); err == nil {
		t.Fatal("migration must report a version write failure")
	}
	history, err := migrationHistory(g)
	if err != nil || len(history) != 1 || g.Migrator().HasTable("bbs_user_profiles") {
		t.Fatalf("failed migration partially committed: history=%+v err=%v", history, err)
	}
	if err := g.Exec("DROP TRIGGER reject_migration").Error; err != nil {
		t.Fatal(err)
	}
	if err := Migrate(g); err != nil {
		t.Fatal(err)
	}
	if err := CheckWritable(g); err != nil {
		t.Fatal(err)
	}
}

func TestStartupChecksNeedNoWritesAndAllowUnversionedReadOnlyMirror(t *testing.T) {
	g := migrationTestDB(t)
	if err := InitializeEmpty(g); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"DROP TABLE bbs_schema_migrations",
		"DROP TABLE bbs_user_profiles",
		"DELETE FROM flarum_groups",
		"DELETE FROM flarum_tags",
		"PRAGMA query_only = ON",
	} {
		if err := g.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := CheckReadOnly(g); err != nil {
		t.Fatalf("legacy read-only mirror rejected: %v", err)
	}
	if err := CheckWritable(g); err == nil || !strings.Contains(err.Error(), "migrate-db") {
		t.Fatalf("writable startup must request explicit migration: %v", err)
	}
	if g.Migrator().HasTable("bbs_schema_migrations") || g.Migrator().HasTable("bbs_user_profiles") {
		t.Fatal("startup checks created missing tables")
	}
	if err := g.Exec("PRAGMA query_only = OFF").Error; err != nil {
		t.Fatal(err)
	}
	if err := Migrate(g); err != nil {
		t.Fatal(err)
	}
	if err := g.Exec("PRAGMA query_only = ON").Error; err != nil {
		t.Fatal(err)
	}
	if err := CheckReadOnly(g); err != nil {
		t.Fatal(err)
	}
	if err := CheckWritable(g); err != nil {
		t.Fatalf("fully migrated startup attempted writes: %v", err)
	}
	for _, table := range []string{"flarum_groups", "flarum_tags"} {
		var count int64
		if err := g.Table(table).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("startup or migration seeded %s: count=%d err=%v", table, count, err)
		}
	}
}

func TestMigrateRefusesEmptyDatabaseAndUnknownHistory(t *testing.T) {
	g := migrationTestDB(t)
	if err := Migrate(g); err == nil {
		t.Fatal("empty database must use init-db")
	}
	if g.Migrator().HasTable("bbs_schema_migrations") {
		t.Fatal("invalid database was modified")
	}
	if err := InitializeEmpty(g); err != nil {
		t.Fatal(err)
	}
	if err := g.Create(&schemaMigration{Version: 99, Name: "future_schema", AppliedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := Migrate(g); err == nil || !strings.Contains(err.Error(), "unsupported migration history") {
		t.Fatalf("unknown migration version accepted: %v", err)
	}
	if err := CheckWritable(g); err == nil {
		t.Fatal("writable startup accepted unknown migration history")
	}
}
