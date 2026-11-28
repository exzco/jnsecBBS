package models

import "time"

// 字段类型按生产库 information_schema 实测对齐（2026-10 采集）。

// AccessToken 映射 flarum_access_tokens（与 PHP 共享；Go 写入 session 类型 token，
// 理论上 Flarum 的 API 客户端同样可使用）。
type AccessToken struct {
	ID             uint      `gorm:"column:id;primaryKey"`
	Token          string    `gorm:"column:token;type:varchar(40);uniqueIndex"`
	UserID         uint      `gorm:"column:user_id"`
	LastActivityAt time.Time `gorm:"column:last_activity_at"`
	CreatedAt      time.Time `gorm:"column:created_at"`
	Type           string    `gorm:"column:type;type:varchar(100)"`
	Title          string    `gorm:"column:title;type:varchar(150)"`
	LastIPAddress  string    `gorm:"column:last_ip_address;type:varchar(45)"`
	LastUserAgent  string    `gorm:"column:last_user_agent;type:varchar(255)"`
}

func (AccessToken) TableName() string { return "flarum_access_tokens" }

// InviteCode 存储于 Go 专属表 bbs_invite_codes（替代 fof/doorman 的 doorkeys）。
type InviteCode struct {
	Code      string     `gorm:"column:code;primaryKey;type:varchar(20)"`
	CreatedBy uint       `gorm:"column:created_by"`
	UsedBy    *uint      `gorm:"column:used_by"`
	UsedAt    *time.Time `gorm:"column:used_at"`
	ExpiresAt *time.Time `gorm:"column:expires_at"`
	CreatedAt time.Time  `gorm:"column:created_at;autoCreateTime"`
}

func (InviteCode) TableName() string { return "bbs_invite_codes" }

// CreateInvitesInput 批量生成邀请码请求。
type CreateInvitesInput struct {
	Count       int `json:"count"`
	ExpiresDays int `json:"expiresDays"`
}
