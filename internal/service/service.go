// Package service 论坛业务逻辑与事务编排（查询组装、权限判定、通知写入）。
package service

import (
	"github.com/exzco/bbs/internal/config"
	"github.com/exzco/bbs/internal/infra/mail"
	"gorm.io/gorm"
)

// Service 内容业务层。
type Service struct {
	cfg  *config.Config
	db   *gorm.DB
	mail *mail.Sender
}

func New(cfg *config.Config, g *gorm.DB) *Service {
	return &Service{cfg: cfg, db: g, mail: newMailSender(cfg)}
}

// Error 业务错误：由 handler 映射为 HTTP 状态码与 JSON 输出。
type Error struct {
	Status  int
	Message string
}

// Err 构造业务错误。
func Err(status int, msg string) *Error { return &Error{Status: status, Message: msg} }
