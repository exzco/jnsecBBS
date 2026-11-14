// 邮件通知：outbox 行与业务事务同库落库（回复成功 ⇔ 邮件必进队列），
// 后台任务限量重试投递；SMTP 未启用时整条链路零开销（不排队、不启动协程）。
package service

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/exzco/bbs/internal/config"
	"github.com/exzco/bbs/internal/mail"
	"github.com/exzco/bbs/internal/models"
	"gorm.io/gorm"
)

const maxEmailAttempts = 10

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

// newMailSender 按配置构建 SMTP 客户端；host 或 from 留空即视为未启用邮件功能。
func newMailSender(cfg *config.Config) *mail.Sender {
	if cfg.Smtp.Host == "" || cfg.Smtp.From == "" {
		return nil
	}
	port := cfg.Smtp.Port
	if port <= 0 {
		port = 587
	}
	return &mail.Sender{
		Host: cfg.Smtp.Host, Port: port,
		Username: cfg.Smtp.Username, Password: cfg.Smtp.Password,
		From: cfg.Smtp.From,
	}
}

// enqueueEmail 在业务事务内写入待发邮件；未启用邮件功能时为空操作。
func (s *Service) enqueueEmail(tx *gorm.DB, to, subject, body string) error {
	if s.mail == nil || to == "" {
		return nil
	}
	return tx.Create(&EmailOutbox{Recipient: to, Subject: subject, Body: body, CreatedAt: time.Now()}).Error
}

// enqueueReplyEmail 给讨论楼主排一封回复通知邮件（楼主邮箱来自注册/绑定信息）。
func (s *Service) enqueueReplyEmail(tx *gorm.DB, replier *models.User, d *models.Discussion, number int) error {
	to := d.User.Email
	if to == "" {
		return nil
	}
	subject := fmt.Sprintf("[站内通知] %s 回复了你的讨论《%s》", replier.Username, d.Title)
	var body strings.Builder
	fmt.Fprintf(&body, "%s 回复了你的讨论《%s》（第 %d 楼）。\n\n", replier.Username, d.Title, number)
	if s.cfg.BaseURL != "" {
		fmt.Fprintf(&body, "查看：%s/d/%d-%s\n\n", s.cfg.BaseURL, d.ID, d.Slug())
	}
	body.WriteString("--\n本邮件由系统自动发送，请勿直接回复。")
	return s.enqueueEmail(tx, to, subject, body.String())
}

// StartEmailOutbox 启动后台投递任务（随 ctx 取消退出）；未启用邮件功能时不启动协程。
func StartEmailOutbox(ctx context.Context, cfg *config.Config, g *gorm.DB, interval time.Duration) {
	sender := newMailSender(cfg)
	if sender == nil {
		return
	}
	s := &Service{cfg: cfg, db: g, mail: sender}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.deliverPending()
			}
		}
	}()
}

// deliverPending 投递一批待发邮件：成功落 sent_at，失败记 last_error 并限量重试。
func (s *Service) deliverPending() {
	var batch []EmailOutbox
	if err := s.db.Where("sent_at IS NULL AND attempts < ?", maxEmailAttempts).
		Order("id").Limit(20).Find(&batch).Error; err != nil {
		log.Printf("[service] email outbox 轮询失败: %v", err)
		return
	}
	for i := range batch {
		out := &batch[i]
		sendErr := s.mail.Send(out.Recipient, out.Subject, out.Body)
		now := time.Now()
		updates := map[string]any{"attempts": out.Attempts + 1, "sent_at": nil, "last_error": nil}
		if sendErr == nil {
			updates["sent_at"] = &now
		} else {
			msg := sendErr.Error()
			if len(msg) > 500 {
				msg = msg[:500]
			}
			updates["last_error"] = msg
		}
		if err := s.db.Model(&EmailOutbox{}).Where("id = ?", out.ID).Updates(updates).Error; err != nil {
			// 状态更新失败会使下轮重发同一封（at-least-once），不丢事件。
			log.Printf("[service] email outbox 状态更新失败 id=%d: %v", out.ID, err)
		}
	}
}
