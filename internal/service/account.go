// 账号与会话相关业务：登录校验、注册、资料/密码/头像维护、通知。
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
	"github.com/exzco/bbs/internal/models"
	"github.com/exzco/bbs/internal/util"
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

func (s *Service) visibleNotifications(u *models.User) *gorm.DB {
	q := s.db.Model(&models.Notification{}).Where("user_id = ?", u.ID)
	if !canViewHidden(u) {
		q = q.Where("(discussion_id = 0 OR EXISTS (SELECT 1 FROM flarum_discussions d WHERE d.id = bbs_notifications.discussion_id AND d.hidden_at IS NULL))")
	}
	return q
}

func (s *Service) unreadCount(u *models.User) int64 {
	if u == nil {
		return 0
	}
	var n int64
	s.visibleNotifications(u).Where("read_at IS NULL").Count(&n)
	return n
}

// Me 组装当前会话的站点与用户数据（供 /api/me 使用）。
func (s *Service) Me(u *models.User) map[string]any {
	resp := map[string]any{
		"user":         s.UserDTO(u),
		"readOnly":     s.cfg.ReadOnly,
		"siteName":     s.SettingValue(models.SettingKeySiteName),
		"registration": s.SettingValue(models.SettingKeyRegistration),
		"welcomeText":  s.SettingValue(models.SettingKeyWelcomeText),
	}
	if u != nil {
		resp["email"] = u.Email
		resp["emailBound"] = s.EmailBound(u.ID)
		resp["unread"] = s.unreadCount(u)
		resp["realName"], resp["blogUrl"] = s.LoadUserProfileExtra(u.ID)
	}
	return resp
}

// Login 校验用户名/邮箱与密码（Flarum 语义），返回可登录用户。
func (s *Service) Login(username, password string) (*models.User, *Error) {
	if username == "" || password == "" {
		return nil, Err(http.StatusBadRequest, "请输入用户名和密码。")
	}
	var u models.User
	if err := s.db.Preload("Groups").
		Where("lower(username) = lower(?) OR lower(email) = lower(?)", username, username).
		First(&u).Error; err != nil ||
		!u.CheckPassword(password) {
		return nil, Err(http.StatusBadRequest, "用户名或密码错误。")
	}
	if u.IsBanned() {
		return nil, Err(http.StatusForbidden, "该账号已被封禁，如有疑问请联系管理员。")
	}
	return &u, nil
}

// TouchSeen 更新活跃时间（只读模式跳过）。
func (s *Service) TouchSeen(u *models.User) error {
	if s.cfg.ReadOnly {
		return nil
	}
	now := time.Now()
	return s.db.Model(u).Update("last_seen_at", &now).Error
}

// NewCaptcha 生成新的图形验证码 ID。
func (s *Service) NewCaptcha() string { return captcha.New() }

// Register 校验并创建用户（单事务），邀请码模式下消费邀请码。
func (s *Service) Register(in models.RegisterInput) (*models.User, *Error) {
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
	u := models.User{
		Username: username, Email: email,
		IsEmailConfirmed: true, Preferences: []byte("a:0:{}"),
	}
	if err := u.SetPassword(in.Password); err != nil {
		return nil, Err(http.StatusInternalServerError, "注册失败，请重试。")
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
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

// UpdateProfile 校验并保存简介与扩展资料；返回（用户视图, 实名, 博客链接）。
func (s *Service) UpdateProfile(u *models.User, in models.ProfileInput) (*UserDTO, string, string, *Error) {
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
	u.Bio = bio
	return s.UserDTO(u), extra.RealName, extra.BlogURL, nil
}

// UpdateEmail 更换绑定邮箱并标记为已绑定（绑定后才能接收回帖邮件通知）。
func (s *Service) UpdateEmail(u *models.User, in models.EmailInput) *Error {
	email := strings.TrimSpace(strings.ToLower(in.Email))
	if _, err := mail.ParseAddress(email); err != nil {
		return Err(http.StatusBadRequest, "邮箱格式不正确。")
	}
	var cnt int64
	s.db.Model(&models.User{}).Where("lower(email) = lower(?) AND id <> ?", email, u.ID).Count(&cnt)
	if cnt > 0 {
		return Err(http.StatusBadRequest, "邮箱已被其他账号绑定。")
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(u).Update("email", email).Error; err != nil {
			return err
		}
		return tx.Where(&models.EmailBind{UserID: u.ID}).
			Assign(&models.EmailBind{UserID: u.ID, BoundAt: time.Now()}).
			FirstOrCreate(&models.EmailBind{}).Error
	})
	if err != nil {
		return Err(http.StatusInternalServerError, "保存失败，请重试。")
	}
	u.Email = email
	return nil
}

// UnbindEmail 解除邮箱绑定（停止接收回帖邮件通知；注册邮箱保留）。
func (s *Service) UnbindEmail(u *models.User) *Error {
	if err := s.db.Delete(&models.EmailBind{}, "user_id = ?", u.ID).Error; err != nil {
		return Err(http.StatusInternalServerError, "操作失败，请重试。")
	}
	return nil
}

// UpdatePassword 校验并改密，踢掉其他设备的会话。
func (s *Service) UpdatePassword(u *models.User, in models.PasswordInput, currentToken string) *Error {
	if !u.CheckPassword(in.Current) {
		return Err(http.StatusBadRequest, "当前密码不正确。")
	}
	if len(in.New) < 8 {
		return Err(http.StatusBadRequest, "新密码至少 8 位。")
	}
	if in.New != in.Confirm {
		return Err(http.StatusBadRequest, "两次输入的新密码不一致。")
	}
	oldHash := u.Password
	if err := u.SetPassword(in.New); err != nil {
		return Err(http.StatusInternalServerError, "修改失败，请重试。")
	}
	if err := s.db.Model(u).Update("password", u.Password).Error; err != nil {
		u.Password = oldHash
		return Err(http.StatusInternalServerError, "修改失败，请重试。")
	}
	// 改密后踢掉其他设备的会话
	if err := s.db.Where("user_id = ? AND token <> ?", u.ID, currentToken).Delete(&models.AccessToken{}).Error; err != nil {
		return Err(http.StatusInternalServerError, "修改失败，请重试。")
	}
	return nil
}

// RemoveAvatar 清除头像并删除磁盘文件，返回回退字环头像 URL。
func (s *Service) RemoveAvatar(u *models.User) (string, *Error) {
	old := u.AvatarURL
	if err := s.db.Model(u).Update("avatar_url", "").Error; err != nil {
		return "", Err(http.StatusInternalServerError, "删除头像失败。")
	}
	if err := s.removeAvatarFile(old); err != nil {
		log.Printf("[service] removeAvatar 旧文件清理失败 user=%d: %v", u.ID, err)
		return "", Err(http.StatusInternalServerError, "删除头像失败。")
	}
	return s.AvatarURL(u), nil
}

// SaveAvatar 解码、缩放并保存头像（PNG 128×128），更新 avatar_url 并清理旧文件。
func (s *Service) SaveAvatar(u *models.User, size int64, r io.Reader) (string, *Error) {
	if size > 2<<20 {
		return "", Err(http.StatusBadRequest, "头像文件不能超过 2MB。")
	}
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

// Notifications 当前用户最近 50 条通知与未读数。
func (s *Service) Notifications(u *models.User) ([]NotificationDTO, int64) {
	var notifs []models.Notification
	s.visibleNotifications(u).
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
	return items, s.unreadCount(u)
}

// MarkNotificationsRead 全部通知标记已读。
func (s *Service) MarkNotificationsRead(u *models.User) error {
	return s.db.Model(&models.Notification{}).
		Where("user_id = ? AND read_at IS NULL", u.ID).Update("read_at", time.Now()).Error
}

// ---------- 用户视图与通知辅助 ----------

type GroupDTO struct {
	ID    uint   `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

func groupDTOs(groups []models.Group) []GroupDTO {
	out := make([]GroupDTO, 0, len(groups))
	for i := range groups {
		out = append(out, GroupDTO{ID: groups[i].ID, Name: groups[i].Name(), Color: groups[i].Color})
	}
	return out
}

type UserDTO struct {
	ID               uint       `json:"id"`
	Username         string     `json:"username"`
	Bio              string     `json:"bio"`
	Avatar           string     `json:"avatar"`
	Groups           []GroupDTO `json:"groups"`
	IsAdmin          bool       `json:"isAdmin"`
	IsMod            bool       `json:"isMod"`
	IsBanned         bool       `json:"isBanned"`
	DiscussionsCount int        `json:"discussionsCount"`
	PostsCount       int        `json:"postsCount"`
	CreatedAt        time.Time  `json:"createdAt"`
	LastSeenAt       *time.Time `json:"lastSeenAt"`
}

// UserDTO 组装用户视图。
func (s *Service) UserDTO(u *models.User) *UserDTO {
	if u == nil {
		return nil
	}
	return &UserDTO{
		ID: u.ID, Username: u.Username, Bio: u.Bio,
		Avatar: s.AvatarURL(u), Groups: groupDTOs(u.Groups),
		IsAdmin: u.IsAdmin(), IsMod: u.IsModerator(), IsBanned: u.IsBanned(),
		DiscussionsCount: u.DiscussionsCount, PostsCount: u.PostsCount,
		CreatedAt: u.JoinedAtOrZero(), LastSeenAt: u.LastSeenAt,
	}
}

// AvatarURL：flarum avatar_url 原样（相对 /assets 路径由资产挂载伺服）；
// 为空则用字环头像。
func (s *Service) AvatarURL(u *models.User) string {
	if u == nil {
		return "/avatar/_unknown.svg"
	}
	name := "_unknown"
	if u.Username != "" {
		name = u.Username
	}
	if u.AvatarURL != "" {
		value := u.AvatarURL
		if strings.HasPrefix(value, "/assets/") || strings.HasPrefix(value, "/avatars/") {
			return value
		}
		if parsed, err := url.Parse(value); err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Host != "" {
			return value
		}
		// Flarum persists a basename, not the public avatar URL.
		if !strings.ContainsAny(value, "/\\:") && value != "." && value != ".." {
			return "/assets/avatars/" + url.PathEscape(value)
		}
	}
	return "/avatar/" + url.PathEscape(name) + ".svg"
}

// LoadUserProfileExtra 读取用户扩展资料；只读镜像可能尚未建表，缺表降级为空。
func (s *Service) LoadUserProfileExtra(userID uint) (realName, blogURL string) {
	var extra models.UserProfile
	if err := s.db.Where("user_id = ?", userID).First(&extra).Error; err == nil {
		return extra.RealName, extra.BlogURL
	}
	return "", ""
}

// usersByIDs 批量加载用户（含组）并按 ID 建索引。
func (s *Service) usersByIDs(ids []uint) map[uint]*models.User {
	m := make(map[uint]*models.User, len(ids))
	if len(ids) == 0 {
		return m
	}
	var users []models.User
	s.db.Where("id IN ?", ids).Preload("Groups").Find(&users)
	for i := range users {
		m[users[i].ID] = &users[i]
	}
	return m
}

// mentionedUserID 解析 @提及 的用户 ID。
func (s *Service) mentionedUserID(name string) (uint, bool) {
	var id uint
	s.db.Model(&models.User{}).Select("id").
		Where("lower(username) = lower(?)", name).Scan(&id)
	return id, id > 0
}

// notify 创建通知（bbs_notifications，Go 专属表；不给自己发）。
func notify(g *gorm.DB, userID, senderID uint, typ string, discussionID, postID uint) error {
	if userID == 0 || userID == senderID {
		return nil
	}
	return g.Create(&models.Notification{
		UserID: userID, SenderID: senderID, Type: typ,
		DiscussionID: discussionID, PostID: postID,
	}).Error
}

// notifyMentions 给正文 @提及 的用户发 mention 通知并返回被提及 ID 集
// （MentionedUsernames 已去重且仅含正向解析名；供调用方抑制对楼主的重复通知）。
func (s *Service) notifyMentions(g *gorm.DB, source string, senderID, discussionID, postID uint) (map[uint]bool, error) {
	mentioned := map[uint]bool{}
	for _, name := range util.MentionedUsernames(source, s.mentionedUserID) {
		mid, ok := s.mentionedUserID(name)
		if !ok || mid == senderID {
			continue
		}
		if err := notify(g, mid, senderID, "mention", discussionID, postID); err != nil {
			return nil, err
		}
		mentioned[mid] = true
	}
	return mentioned, nil
}
