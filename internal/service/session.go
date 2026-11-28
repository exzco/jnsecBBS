package service

import (
	"crypto/rand"
	"encoding/hex"
	"log"
	"net/http"
	"time"

	"github.com/exzco/bbs/internal/models"
)

// NewToken 生成随机 token（20 字节 = 40 个 hex 字符——必须 ≤ flarum_access_tokens.token
// 的 varchar(40)，32 字节会触发 Data too long）。
func NewToken() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// CreateSession 写入 flarum_access_tokens（与 PHP 共享 token 存储）。
func (s *Service) CreateSession(userID uint) (string, error) {
	token, err := NewToken()
	if err != nil {
		return "", err
	}
	now := time.Now()
	at := models.AccessToken{
		Token: token, UserID: userID,
		CreatedAt: now, LastActivityAt: now, Type: "session",
	}
	if err := s.db.Create(&at).Error; err != nil {
		return "", err
	}
	return token, nil
}

// loadUserByID 按 ID 加载用户（含用户组）。
func (s *Service) loadUserByID(id uint) (models.User, bool) {
	var u models.User
	if err := s.db.Preload("Groups").First(&u, id).Error; err != nil {
		return models.User{}, false
	}
	return u, true
}

// UserByToken 依据会话 token 加载当前用户（含用户组）。
func (s *Service) UserByToken(token string) (*models.User, *models.AccessToken) {
	if token == "" {
		return nil, nil
	}
	var at models.AccessToken
	if err := s.db.Where("token = ?", token).First(&at).Error; err != nil {
		return nil, nil
	}
	u, ok := s.loadUserByID(at.UserID)
	if !ok {
		return nil, nil
	}
	// 活跃度更新（每小时一次，避免每请求写库）
	if time.Since(at.LastActivityAt) > time.Hour {
		now := time.Now()
		s.db.Model(&models.AccessToken{}).Where("id = ?", at.ID).
			Update("last_activity_at", now)
		at.LastActivityAt = now
	}
	return &u, &at
}

// DestroySession 删除会话 token 记录。
func (s *Service) DestroySession(token string) {
	if token != "" {
		s.db.Where("token = ?", token).Delete(&models.AccessToken{})
	}
}

// StartSession 创建会话 token、刷新活跃时间并返回会话视图数据。
func (s *Service) StartSession(u *models.User) (string, *UserDTO, int64, *Error) {
	token, err := s.CreateSession(u.ID)
	if err != nil {
		log.Printf("[service] CreateSession 失败 user=%s: %v", u.Username, err)
		return "", nil, 0, Err(http.StatusInternalServerError, "登录失败，请重试。")
	}
	if err := s.TouchSeen(u); err != nil {
		return "", nil, 0, Err(http.StatusInternalServerError, "登录失败，请重试。")
	}
	return token, s.UserDTO(u), s.UnreadCount(u.ID), nil
}
