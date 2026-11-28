package models

import "time"

// Notification 存储于 Go 专属表 bbs_notifications（不写 flarum_notifications——
// 其 subject/data 结构为 PHP 序列化格式，跨写有兼容风险）。
type Notification struct {
	ID           uint        `gorm:"column:id;primaryKey"`
	UserID       uint        `gorm:"column:user_id;index:idx_bbs_notif"`
	SenderID     uint        `gorm:"column:sender_id"`
	Sender       User        `gorm:"foreignKey:SenderID"`
	Type         string      `gorm:"column:type;type:varchar(100);index:idx_bbs_notif"` // reply | mention | like
	DiscussionID uint        `gorm:"column:discussion_id"`
	Discussion   *Discussion `gorm:"foreignKey:DiscussionID"`
	PostID       uint        `gorm:"column:post_id"`
	ReadAt       *time.Time  `gorm:"column:read_at"`
	CreatedAt    time.Time   `gorm:"column:created_at;autoCreateTime"`
}

func (Notification) TableName() string { return "bbs_notifications" }
