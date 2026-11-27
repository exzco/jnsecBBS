package db_test

import (
	"testing"
	"time"

	"github.com/exzco/bbs/internal/infra/db"
	"github.com/exzco/bbs/internal/models"
	"github.com/exzco/bbs/tests/testdb"
)

// Fresh installations must use the same join-column names as Flarum. These
// associations previously queried a nonexistent `id` column in production.
func TestFreshDatabaseAssociations(t *testing.T) {
	g, err := db.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := g.DB()
	sqlDB.SetMaxOpenConns(1)
	defer sqlDB.Close()
	if err = testdb.InitializeEmpty(g); err != nil {
		t.Fatal(err)
	}
	if err = testdb.InitializeEmpty(g); err == nil {
		t.Fatal("must refuse existing tables")
	}
	if !g.Migrator().HasTable(&models.GroupPermission{}) {
		t.Fatal("missing permission table")
	}
	user := models.User{Username: "test", Email: "test@example.org", Groups: []models.Group{{ID: models.GroupAdmin}}}
	if err = g.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	var loaded models.User
	if err = g.Preload("Groups").First(&loaded, user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !loaded.IsAdmin() {
		t.Fatal("admin association not loaded")
	}
	var tag models.Tag
	if err = g.First(&tag).Error; err != nil {
		t.Fatal(err)
	}
	discussion := models.Discussion{Title: "test", Tags: []models.Tag{tag}}
	if err = g.Create(&discussion).Error; err != nil {
		t.Fatal(err)
	}
	var loadedDiscussion models.Discussion
	if err = g.Preload("Tags").First(&loadedDiscussion, discussion.ID).Error; err != nil {
		t.Fatal(err)
	}
	if len(loadedDiscussion.Tags) != 1 {
		t.Fatal("tag association not loaded")
	}
	if !g.Migrator().HasColumn("flarum_discussion_tag", "tag_id") || !g.Migrator().HasColumn("flarum_group_user", "group_id") {
		t.Fatal("wrong legacy column names")
	}
}

func TestSyncHiddenFirstPostDiscussions(t *testing.T) {
	g, err := db.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := g.DB()
	sqlDB.SetMaxOpenConns(1)
	defer sqlDB.Close()
	if err = testdb.InitializeEmpty(g); err != nil {
		t.Fatal(err)
	}
	user := models.User{Username: "author", Email: "author@example.org"}
	if err = g.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	num := 1
	now := time.Now().Truncate(time.Second)
	post := models.Post{
		Number:       &num,
		UserID:       &user.ID,
		Type:         "comment",
		Content:      "首帖",
		HiddenAt:     &now,
		HiddenUserID: &user.ID,
	}
	if err = g.Create(&post).Error; err != nil {
		t.Fatal(err)
	}
	disc := models.Discussion{
		Title:       "讨论",
		UserID:      &user.ID,
		FirstPostID: &post.ID,
		HiddenAt:    nil,
	}
	if err = g.Create(&disc).Error; err != nil {
		t.Fatal(err)
	}
	if err = db.SyncHiddenFirstPostDiscussions(g); err != nil {
		t.Fatalf("sync failed: %v", err)
	}
	var loaded models.Discussion
	if err = g.First(&loaded, disc.ID).Error; err != nil {
		t.Fatal(err)
	}
	if loaded.HiddenAt == nil {
		t.Fatal("expected discussion hidden_at to be populated by sync")
	}
	if loaded.HiddenUserID == nil || *loaded.HiddenUserID != user.ID {
		t.Fatalf("expected hidden_user_id %d, got %v", user.ID, loaded.HiddenUserID)
	}
}
