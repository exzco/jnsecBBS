package models

import "time"

// UserProfile 是 Go 专属的用户扩展资料（实名、博客链接）。
// bbs_user_profiles belongs to Go; its schema is managed by the operator.
type UserProfile struct {
	UserID    uint      `gorm:"column:user_id;primaryKey"`
	RealName  string    `gorm:"column:real_name;type:varchar(100)"`
	BlogURL   string    `gorm:"column:blog_url;type:varchar(255)"`
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

func (UserProfile) TableName() string { return "bbs_user_profiles" }
