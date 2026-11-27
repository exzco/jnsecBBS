// Package testdb creates the legacy-compatible schema used by isolated tests.
package testdb

import (
	"fmt"

	"github.com/exzco/bbs/internal/infra/db"
	"github.com/exzco/bbs/internal/models"
	"gorm.io/gorm"
)

func InitializeEmpty(g *gorm.DB) error {
	tables, err := g.Migrator().GetTables()
	if err != nil {
		return err
	}
	if len(tables) != 0 {
		return fmt.Errorf("test initialization requires an empty database; found %d tables", len(tables))
	}
	if err := g.AutoMigrate(
		&models.User{}, &models.Group{}, &models.Discussion{}, &models.Post{},
		&models.PostLike{}, &models.Tag{}, &models.Notification{}, &models.AccessToken{},
		&models.InviteCode{}, &models.Setting{}, &models.GroupPermission{}, &models.UserProfile{},
		&models.DiscussionUser{}, &models.DiscussionView{},
	); err != nil {
		return err
	}
	for _, create := range []func(*gorm.DB) error{
		createNotificationsAndInvites, createUserProfiles, createEmailOutbox,
		createEmailBind, createDiscussionReadPositions,
	} {
		if err := create(g); err != nil {
			return err
		}
	}
	if err := db.CleanupJoinTableDuplicates(g); err != nil {
		return err
	}
	if err := db.SyncHiddenFirstPostDiscussions(g); err != nil {
		return err
	}
	groups := []models.Group{
		{ID: models.GroupAdmin, NameSingular: "Admin", NamePlural: "Admin", Color: "#e53935", Icon: "fas fa-wrench"},
		{ID: models.GroupGuest, NameSingular: "Guest", NamePlural: "Guest", Color: "#9e9e9e", Icon: "fas fa-reply", IsHidden: true},
		{ID: models.GroupMember, NameSingular: "成员", NamePlural: "正式成员", Color: "#1e88e5", Icon: "fas fa-user"},
		{ID: models.GroupMod, NameSingular: "版主", NamePlural: "版主", Color: "#43a047", Icon: "fas fa-bolt"},
	}
	if err := g.Create(&groups).Error; err != nil {
		return err
	}
	position := 1
	return g.Create(&models.Tag{Name: "综合讨论", Slug: "general", Color: "#1e88e5", Position: &position}).Error
}
