package models

import "time"

// EmailOutbox 待发/已发邮件记录（bbs_email_outbox）。
type EmailOutbox struct {
	ID        uint       `gorm:"column:id;primaryKey"`
	Recipient string     `gorm:"column:recipient"`
	Subject   string     `gorm:"column:subject"`
	Body      string     `gorm:"column:body"`
	Attempts  int        `gorm:"column:attempts"`
	SentAt    *time.Time `gorm:"column:sent_at"`
	LastError *string    `gorm:"column:last_error"`
	CreatedAt time.Time  `gorm:"column:created_at"`
}

func (EmailOutbox) TableName() string { return "bbs_email_outbox" }

// EmailBind 用户邮箱绑定记录（bbs_email_bind）：行存在 = 已绑定，回帖邮件通知仅发给已绑定用户。
// 存量用户由迁移回填；新注册用户须在设置页显式绑定。
type EmailBind struct {
	UserID  uint      `gorm:"column:user_id;primaryKey"`
	BoundAt time.Time `gorm:"column:bound_at"`
}

func (EmailBind) TableName() string { return "bbs_email_bind" }
