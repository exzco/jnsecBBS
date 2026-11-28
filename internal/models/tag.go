package models

import "time"

// Tag 映射 flarum_tags（含 discussion_count 计数列与 last_posted_* 冗余列）。
type Tag struct {
	ID               uint       `gorm:"column:id;primaryKey"`
	Name             string     `gorm:"column:name;type:varchar(100)"`
	Slug             string     `gorm:"column:slug;type:varchar(100);uniqueIndex"`
	Description      string     `gorm:"column:description;type:text"`
	Color            string     `gorm:"column:color;type:varchar(50)"`
	BackgroundPath   string     `gorm:"column:background_path;type:varchar(100)"`
	BackgroundMode   string     `gorm:"column:background_mode;type:varchar(100)"`
	Position         *int       `gorm:"column:position"`
	ParentID         *uint      `gorm:"column:parent_id"`
	DefaultSort      string     `gorm:"column:default_sort;type:varchar(50)"`
	IsRestricted     bool       `gorm:"column:is_restricted"`
	IsHidden         bool       `gorm:"column:is_hidden"`
	DiscussionsCount int        `gorm:"column:discussion_count;type:int unsigned"`
	LastPostedAt     *time.Time `gorm:"column:last_posted_at"`
	LastPostedDiscID *uint      `gorm:"column:last_posted_discussion_id"`
	LastPostedUserID *uint      `gorm:"column:last_posted_user_id"`
	Icon             string     `gorm:"column:icon;type:varchar(100)"`
}

func (Tag) TableName() string { return "flarum_tags" }

// TagInput 版块保存请求。
type TagInput struct {
	ID          uint   `json:"id"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Color       string `json:"color"`
	Position    int    `json:"position"`
	ParentID    uint   `json:"parentId"`
	Description string `json:"description"`
}
