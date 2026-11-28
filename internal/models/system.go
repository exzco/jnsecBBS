package models

// Setting 映射 flarum_settings（与 PHP 共享；forum_title 即站点名）。
type Setting struct {
	Key   string `gorm:"column:key;primaryKey;type:varchar(100)"`
	Value string `gorm:"column:value;type:text"`
}

func (Setting) TableName() string { return "flarum_settings" }

// GroupPermission 映射 flarum_group_permission（Flarum 历史权限表结构兼容）。
type GroupPermission struct {
	GroupID    uint   `gorm:"column:group_id;primaryKey"`
	Permission string `gorm:"column:permission;primaryKey;type:varchar(100)"`
}

func (GroupPermission) TableName() string { return "flarum_group_permission" }

// 设置键：站点名沿用 Flarum 的 forum_title；其余为 Go 专属键（bbs_ 前缀）。
const (
	SettingKeySiteName     = "forum_title"
	SettingKeyWelcomeText  = "bbs_welcome_text"
	SettingKeyRegistration = "bbs_registration"
)

// SettingsInput 站点设置请求与响应结构。
type SettingsInput struct {
	SiteName     string `json:"siteName"`
	Registration string `json:"registration"`
	WelcomeText  string `json:"welcomeText"`
}
