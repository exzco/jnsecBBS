package models

import (
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// 字段类型按生产库 information_schema 实测对齐（2026-10 采集）。

// 用户组 ID 沿用 Flarum 约定。
const (
	GroupAdmin  uint = 1
	GroupGuest  uint = 2
	GroupMember uint = 3
	GroupMod    uint = 4
)

type User struct {
	ID                  uint       `gorm:"column:id;primaryKey"`
	Username            string     `gorm:"column:username;type:varchar(100);uniqueIndex"`
	Email               string     `gorm:"column:email;type:varchar(150);uniqueIndex"`
	IsEmailConfirmed    bool       `gorm:"column:is_email_confirmed"`
	Password            string     `gorm:"column:password;type:varchar(100)"`
	Bio                 string     `gorm:"column:bio;type:text"`
	AvatarURL           string     `gorm:"column:avatar_url;type:varchar(100)"`
	Preferences         []byte     `gorm:"column:preferences;type:blob"` // PHP 序列化格式，Go 只透传不解析
	JoinedAt            *time.Time `gorm:"column:joined_at"`
	LastSeenAt          *time.Time `gorm:"column:last_seen_at"`
	MarkedAllAsReadAt   *time.Time `gorm:"column:marked_all_as_read_at"`
	ReadNotificationsAt *time.Time `gorm:"column:read_notifications_at"`
	DiscussionsCount    int        `gorm:"column:discussion_count;type:int unsigned"`
	PostsCount          int        `gorm:"column:comment_count;type:int unsigned"`
	ReadFlagsAt         *time.Time `gorm:"column:read_flags_at"`
	SuspendUntil        *time.Time `gorm:"column:suspended_until"`               // 注意：flarum/suspend 的真实列名
	InviteCode          string     `gorm:"column:invite_code;type:varchar(128)"` // fof/doorman 遗留
	Groups              []Group    `gorm:"many2many:flarum_group_user;foreignKey:ID;joinForeignKey:UserID;joinReferences:GroupID"`
}

func (User) TableName() string { return "flarum_users" }

// IsBanned：flarum/suspend 语义——suspended_until 在未来即封禁中。
func (u *User) IsBanned() bool {
	return u.SuspendUntil != nil && u.SuspendUntil.After(time.Now())
}

func (u *User) HasGroup(ids ...uint) bool {
	for _, g := range u.Groups {
		for _, want := range ids {
			if g.ID == want {
				return true
			}
		}
	}
	return false
}

func (u *User) IsAdmin() bool       { return u.HasGroup(GroupAdmin) }
func (u *User) IsModerator() bool   { return u.HasGroup(GroupAdmin, GroupMod) }
func (u *User) DisplayName() string { return u.Username }

const passwordHashCost = 12

// CheckPassword 校验密码。兼容从 Flarum 导入的 PHP bcrypt 哈希：
// PHP password_hash 产出 "$2y$" 前缀，Go 的 bcrypt 只认 "$2a$/2b$"，这里做前缀归一。
func (u *User) CheckPassword(plain string) bool {
	hash := strings.TrimSpace(u.Password)
	if strings.HasPrefix(hash, "$2y$") {
		hash = "$2a$" + hash[4:]
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

// SetPassword 使用 bcrypt 对明文密码哈希并写入 User.Password。
func (u *User) SetPassword(plain string) error {
	h, err := bcrypt.GenerateFromPassword([]byte(plain), passwordHashCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	u.Password = string(h)
	return nil
}

// JoinedAtOrZero 供 DTO 使用：可空时间零值兜底。
func (u *User) JoinedAtOrZero() time.Time {
	if u.JoinedAt != nil {
		return *u.JoinedAt
	}
	return time.Time{}
}

// GroupIDs 返回用户所属组 ID（管理后台表单勾选用）。
func (u *User) GroupIDs() []uint {
	ids := make([]uint, 0, len(u.Groups))
	for _, g := range u.Groups {
		ids = append(ids, g.ID)
	}
	return ids
}

type Group struct {
	ID           uint   `gorm:"column:id;primaryKey"`
	NameSingular string `gorm:"column:name_singular;type:varchar(100)"`
	NamePlural   string `gorm:"column:name_plural;type:varchar(100)"`
	Color        string `gorm:"column:color;type:varchar(20)"`
	Icon         string `gorm:"column:icon;type:varchar(100)"`
	IsHidden     bool   `gorm:"column:is_hidden"`
}

// Name 展示名（Flarum 后台以 name_plural 为组名）。
func (g *Group) Name() string { return g.NamePlural }

func (Group) TableName() string { return "flarum_groups" }

// LoginInput 登录请求。
type LoginInput struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// RegisterInput 注册请求。
type RegisterInput struct {
	Username        string `json:"username"`
	Email           string `json:"email"`
	Password        string `json:"password"`
	PasswordConfirm string `json:"passwordConfirm"`
	Invite          string `json:"invite"`
	CaptchaID       string `json:"captchaId"`
	CaptchaValue    string `json:"captchaValue"`
}

// ProfileInput 个人资料修改请求（nil 表示不修改该字段）。
type ProfileInput struct {
	Bio      string  `json:"bio"`
	RealName *string `json:"realName"`
	BlogURL  *string `json:"blogUrl"`
}

// EmailInput 换绑邮箱请求。
type EmailInput struct {
	Email string `json:"email"`
}

// PasswordInput 修改密码请求。
type PasswordInput struct {
	Current string `json:"current"`
	New     string `json:"new"`
	Confirm string `json:"confirm"`
}

// AdminUserInput 后台用户操作请求。
type AdminUserInput struct {
	Action string `json:"action"`
	Groups []uint `json:"groups"`
}

const TableGroupUser = "flarum_group_user"

// GroupUser 映射用户与用户组的关联表 flarum_group_user。
type GroupUser struct {
	UserID  uint `gorm:"column:user_id;primaryKey"`
	GroupID uint `gorm:"column:group_id;primaryKey"`
}

func (GroupUser) TableName() string { return TableGroupUser }
