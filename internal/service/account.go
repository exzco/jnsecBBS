// 账号与会话相关业务：登录校验、注册、资料/密码/头像维护、通知。
// HTTP 绑定与 Cookie 写入由 api 层负责，本包不依赖任何 Web 框架。
package service

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"image/png"
	"io"
	"log"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/dchest/captcha"
	"github.com/disintegration/imaging"
	"github.com/exzco/bbs/internal/auth"
	"github.com/exzco/bbs/internal/cache"
	"github.com/exzco/bbs/internal/models"
	"github.com/exzco/bbs/internal/store"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var usernameRe = regexp.MustCompile(`^[A-Za-z0-9_\-]{2,20}$`)

// randHex 生成 n 字节随机数的 hex 串。
func randHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// UnreadCount 未读通知数。
func (s *Service) UnreadCount(userID uint) int64 {
	var n int64
	s.db.Model(&models.Notification{}).Where("user_id = ? AND read_at IS NULL", userID).Count(&n)
	return n
}

// Login 校验用户名/邮箱与密码（Flarum 语义），返回可登录用户。
func (s *Service) Login(username, password string) (*models.User, *Error) {
	var u models.User
	if err := s.db.Preload("Groups").
		Where("lower(username) = lower(?) OR lower(email) = lower(?)", username, username).
		First(&u).Error; err != nil ||
		!auth.CheckPassword(u.Password, password) {
		return nil, Err(http.StatusBadRequest, "用户名或密码错误。")
	}
	if u.IsBanned() {
		return nil, Err(http.StatusForbidden, "该账号已被封禁，如有疑问请联系管理员。")
	}
	return &u, nil
}

// TouchSeen 更新活跃时间并失效用户缓存（只读模式跳过）。
func (s *Service) TouchSeen(u *models.User) error {
	if s.cfg.ReadOnly {
		return nil
	}
	now := time.Now()
	if err := s.db.Model(u).Update("last_seen_at", &now).Error; err != nil {
		return err
	}
	// last_seen_at 已变更，显式失效 user:id（Cache-Aside 写路径）。
	cache.Default.Delete(cache.UserIDKey(u.ID))
	return nil
}

// RegisterInput 注册请求（已由 handler 绑定）。
type RegisterInput struct {
	Username        string `json:"username"`
	Email           string `json:"email"`
	Password        string `json:"password"`
	PasswordConfirm string `json:"passwordConfirm"`
	Invite          string `json:"invite"`
	CaptchaID       string `json:"captchaId"`
	CaptchaValue    string `json:"captchaValue"`
}

// Register 校验并创建用户（单事务），邀请码模式下消费邀请码。
func (s *Service) Register(in RegisterInput) (*models.User, *Error) {
	username := strings.TrimSpace(in.Username)
	email := strings.TrimSpace(strings.ToLower(in.Email))

	if s.SettingValue(models.SettingKeyRegistration) == "invite" {
		var inv models.InviteCode
		if err := s.db.Where("code = ?", strings.TrimSpace(in.Invite)).First(&inv).Error; err != nil {
			return nil, Err(http.StatusBadRequest, "邀请码无效。")
		}
		if inv.UsedBy != nil || (inv.ExpiresAt != nil && inv.ExpiresAt.Before(time.Now())) {
			return nil, Err(http.StatusBadRequest, "邀请码已被使用或已过期。")
		}
	} else if !captcha.VerifyString(in.CaptchaID, in.CaptchaValue) {
		return nil, Err(http.StatusBadRequest, "验证码错误。")
	}
	if !usernameRe.MatchString(username) {
		return nil, Err(http.StatusBadRequest, "用户名需为 2-20 位字母、数字、下划线或连字符。")
	}
	if _, err := mail.ParseAddress(email); err != nil {
		return nil, Err(http.StatusBadRequest, "邮箱格式不正确。")
	}
	if len(in.Password) < 8 {
		return nil, Err(http.StatusBadRequest, "密码至少 8 位。")
	}
	if in.Password != in.PasswordConfirm {
		return nil, Err(http.StatusBadRequest, "两次输入的密码不一致。")
	}
	var cnt int64
	s.db.Model(&models.User{}).Where("lower(username) = lower(?)", username).Count(&cnt)
	if cnt > 0 {
		return nil, Err(http.StatusBadRequest, "用户名已被占用。")
	}
	s.db.Model(&models.User{}).Where("lower(email) = lower(?)", email).Count(&cnt)
	if cnt > 0 {
		return nil, Err(http.StatusBadRequest, "邮箱已被注册。")
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		return nil, Err(http.StatusInternalServerError, "注册失败，请重试。")
	}
	u := models.User{
		Username: username, Email: email, Password: hash,
		IsEmailConfirmed: true, Preferences: []byte("a:0:{}"),
	}
	err = s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&u).Error; err != nil {
			return err
		}
		member := models.Group{ID: models.GroupMember}
		return tx.Model(&u).Association("Groups").Append(&member)
	})
	if err != nil {
		log.Printf("[service] register 事务失败 username=%s: %v", username, err)
		return nil, Err(http.StatusInternalServerError, "注册失败，请重试。")
	}
	if s.SettingValue(models.SettingKeyRegistration) == "invite" {
		if err := s.db.Model(&models.InviteCode{}).Where("code = ?", strings.TrimSpace(in.Invite)).
			Updates(map[string]any{"used_by": u.ID, "used_at": time.Now()}).Error; err != nil {
			log.Printf("[service] register 邀请码消费失败 username=%s: %v", username, err)
			return nil, Err(http.StatusInternalServerError, "注册失败，请重试。")
		}
	}
	return &u, nil
}

// ProfileInput 个人资料修改（nil 表示不修改该字段）。
type ProfileInput struct {
	Bio      string  `json:"bio"`
	RealName *string `json:"realName"`
	BlogURL  *string `json:"blogUrl"`
}

// UpdateProfile 校验并保存简介与扩展资料；返回（用户视图, 实名, 博客链接）。
func (s *Service) UpdateProfile(u *models.User, in ProfileInput) (*UserDTO, string, string, *Error) {
	bio := strings.TrimSpace(in.Bio)
	if len([]rune(bio)) > 500 {
		bio = string([]rune(bio)[:500])
	}
	var realName, blogURL string
	if in.RealName != nil {
		realName = strings.TrimSpace(*in.RealName)
		if len([]rune(realName)) > 30 {
			return nil, "", "", Err(http.StatusBadRequest, "实名最长 30 个字。")
		}
	}
	if in.BlogURL != nil {
		blogURL = strings.TrimSpace(*in.BlogURL)
		if blogURL != "" {
			parsed, err := url.Parse(blogURL)
			if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || len(blogURL) > 255 {
				return nil, "", "", Err(http.StatusBadRequest, "博客链接需为 http(s) 地址。")
			}
		}
	}

	var extra models.UserProfile
	err := s.db.Transaction(func(tx *gorm.DB) error {
		// Do not mutate the session user until the whole transaction commits.
		if err := tx.Model(&models.User{}).Where("id = ?", u.ID).Update("bio", bio).Error; err != nil {
			return err
		}
		if in.RealName != nil || in.BlogURL != nil {
			columns := []string{"updated_at"}
			if in.RealName != nil {
				columns = append(columns, "real_name")
			}
			if in.BlogURL != nil {
				columns = append(columns, "blog_url")
			}
			// Update only supplied fields, preserving concurrent changes to others.
			profile := models.UserProfile{UserID: u.ID, RealName: realName, BlogURL: blogURL, UpdatedAt: time.Now()}
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "user_id"}},
				DoUpdates: clause.AssignmentColumns(columns),
			}).Create(&profile).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("user_id = ?", u.ID).First(&extra).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		return nil
	})
	if err != nil {
		log.Printf("[service] updateProfile 保存失败 user=%s: %v", u.Username, err)
		return nil, "", "", Err(http.StatusInternalServerError, "保存失败，请重试。")
	}
	// bio（及扩展资料）已变更，显式失效 user:id（Cache-Aside 写路径）。
	cache.Default.Delete(cache.UserIDKey(u.ID))
	u.Bio = bio
	return s.UserDTO(u), extra.RealName, extra.BlogURL, nil
}

// EmailInput 换绑邮箱请求（需当前密码确认）。
type EmailInput struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// UpdateEmail 校验密码并更换绑定邮箱（注册邮箱即初始绑定邮箱）。
func (s *Service) UpdateEmail(u *models.User, in EmailInput) *Error {
	if !auth.CheckPassword(u.Password, in.Password) {
		return Err(http.StatusBadRequest, "密码不正确。")
	}
	email := strings.TrimSpace(strings.ToLower(in.Email))
	if _, err := mail.ParseAddress(email); err != nil {
		return Err(http.StatusBadRequest, "邮箱格式不正确。")
	}
	var cnt int64
	s.db.Model(&models.User{}).Where("lower(email) = lower(?) AND id <> ?", email, u.ID).Count(&cnt)
	if cnt > 0 {
		return Err(http.StatusBadRequest, "邮箱已被其他账号绑定。")
	}
	if err := s.db.Model(u).Update("email", email).Error; err != nil {
		return Err(http.StatusInternalServerError, "保存失败，请重试。")
	}
	// email 已变更，显式失效 user:id（Cache-Aside 写路径）。
	cache.Default.Delete(cache.UserIDKey(u.ID))
	u.Email = email
	return nil
}

// PasswordInput 修改密码请求。
type PasswordInput struct {
	Current string `json:"current"`
	New     string `json:"new"`
	Confirm string `json:"confirm"`
}

// UpdatePassword 校验并改密，踢掉其他设备的会话。
func (s *Service) UpdatePassword(u *models.User, in PasswordInput, currentToken string) *Error {
	if !auth.CheckPassword(u.Password, in.Current) {
		return Err(http.StatusBadRequest, "当前密码不正确。")
	}
	if len(in.New) < 8 {
		return Err(http.StatusBadRequest, "新密码至少 8 位。")
	}
	if in.New != in.Confirm {
		return Err(http.StatusBadRequest, "两次输入的新密码不一致。")
	}
	hash, err := auth.HashPassword(in.New)
	if err != nil {
		return Err(http.StatusInternalServerError, "修改失败，请重试。")
	}
	if err := s.db.Model(u).Update("password", hash).Error; err != nil {
		return Err(http.StatusInternalServerError, "修改失败，请重试。")
	}
	// 改密后踢掉其他设备的会话
	if err := s.db.Where("user_id = ? AND token <> ?", u.ID, currentToken).Delete(&models.AccessToken{}).Error; err != nil {
		return Err(http.StatusInternalServerError, "修改失败，请重试。")
	}
	// 密码与其他设备 token 均已变更，显式失效 user:id 与全会话缓存（Cache-Aside 写路径）。
	cache.Default.Delete(cache.UserIDKey(u.ID))
	cache.Default.DeletePrefix(cache.SessionsPrefix())
	return nil
}

// RemoveAvatar 清除头像并删除磁盘文件，返回回退字环头像 URL。
func (s *Service) RemoveAvatar(u *models.User) (string, *Error) {
	old := u.AvatarURL
	if err := s.db.Model(u).Update("avatar_url", "").Error; err != nil {
		return "", Err(http.StatusInternalServerError, "删除头像失败。")
	}
	cache.Default.Delete(cache.UserIDKey(u.ID))
	if err := s.removeAvatarFile(old); err != nil {
		log.Printf("[service] removeAvatar 旧文件清理失败 user=%d: %v", u.ID, err)
		return "", Err(http.StatusInternalServerError, "删除头像失败。")
	}
	return s.AvatarURL(u), nil
}

// SaveAvatar 解码、缩放并保存头像（PNG 128×128），更新 avatar_url 并清理旧文件。
func (s *Service) SaveAvatar(u *models.User, r io.Reader) (string, *Error) {
	head := make([]byte, 512)
	n, _ := r.Read(head)
	ct := http.DetectContentType(head[:n])
	if ct != "image/png" && ct != "image/jpeg" {
		return "", Err(http.StatusBadRequest, "仅支持 PNG / JPG 图片。")
	}
	if sk, ok := r.(io.Seeker); ok {
		if _, err := sk.Seek(0, io.SeekStart); err != nil {
			return "", Err(http.StatusBadRequest, "读取文件失败。")
		}
	}
	img, err := imaging.Decode(r)
	if err != nil {
		return "", Err(http.StatusBadRequest, "图片解析失败。")
	}
	thumb := imaging.Thumbnail(img, 128, 128, imaging.Lanczos)
	name, _ := randHex(16)
	dest := filepath.Join(s.cfg.DataDir, "avatars", name+".png")
	if err := imaging.Save(thumb, dest, imaging.PNGCompressionLevel(png.DefaultCompression)); err != nil {
		return "", Err(http.StatusInternalServerError, "保存头像失败。")
	}
	old := u.AvatarURL
	// 写入 flarum 的 avatar_url：指向新站自己的头像路由（data/avatars 由 app 伺服）
	avatarURL := "/avatars/" + name + ".png"
	if err := s.db.Model(u).Update("avatar_url", avatarURL).Error; err != nil {
		if rmErr := os.Remove(dest); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			log.Printf("[service] saveAvatar 落盘文件回滚失败 %s: %v", dest, rmErr)
		}
		return "", Err(http.StatusInternalServerError, "保存头像失败。")
	}
	// avatar_url 已变更，显式失效 user:id（Cache-Aside 写路径）。
	cache.Default.Delete(cache.UserIDKey(u.ID))
	if err := s.removeAvatarFile(old); err != nil {
		log.Printf("[service] saveAvatar 旧文件清理失败 user=%d: %v", u.ID, err)
		return "", Err(http.StatusInternalServerError, "保存头像失败。")
	}
	return avatarURL, nil
}

// removeAvatarFile 删除 data/avatars 下的头像文件（flarum /assets 路径不归 Go 管，跳过）。
// 文件不存在视为已清理（头像可能来自旧站迁移，磁盘上本就无对应文件）。
func (s *Service) removeAvatarFile(url string) error {
	if url == "" || !strings.HasPrefix(url, "/avatars/") {
		return nil
	}
	if err := os.Remove(filepath.Join(s.cfg.DataDir, "avatars", filepath.Base(url))); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// NotificationDTO 通知列表项。
type NotificationDTO struct {
	ID         uint                  `json:"id"`
	Type       string                `json:"type"`
	Sender     *UserDTO              `json:"sender"`
	Discussion *DiscussionSummaryDTO `json:"discussion,omitempty"`
	PostID     uint                  `json:"postId"`
	Read       bool                  `json:"read"`
	CreatedAt  time.Time             `json:"createdAt"`
}

// Notifications 当前用户最近 50 条通知（仅可见讨论）与未读数。
func (s *Service) Notifications(u *models.User) ([]NotificationDTO, int64) {
	var notifs []models.Notification
	s.db.Where("user_id = ? AND discussion_id IN (?)", u.ID, store.VisibleDiscussions(s.db, u).Select("flarum_discussions.id").Where("hidden_at IS NULL AND is_approved = ?", true)).
		Preload("Sender").Preload("Discussion").
		Order("created_at DESC").Limit(50).Find(&notifs)
	items := make([]NotificationDTO, 0, len(notifs))
	for _, n := range notifs {
		item := NotificationDTO{
			ID: n.ID, Type: n.Type, Sender: s.UserDTO(&n.Sender),
			PostID: n.PostID, Read: n.ReadAt != nil, CreatedAt: n.CreatedAt,
		}
		if n.Discussion != nil {
			d := s.DiscussionSummary(n.Discussion, u)
			item.Discussion = &d
		}
		items = append(items, item)
	}
	return items, s.UnreadCount(u.ID)
}

// MarkNotificationsRead 全部通知标记已读。
func (s *Service) MarkNotificationsRead(u *models.User) error {
	return s.db.Model(&models.Notification{}).
		Where("user_id = ? AND read_at IS NULL", u.ID).Update("read_at", time.Now()).Error
}
