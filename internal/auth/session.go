package auth

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/exzco/bbs/internal/infra/cache"
	"github.com/exzco/bbs/internal/models"
	"gorm.io/gorm"
)

const (
	SessionCookie = "jnsec_session"
	SessionMaxAge = 60 * 60 * 24 * 30
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
func CreateSession(g *gorm.DB, userID uint, ua string, secure bool) (string, error) {
	token, err := NewToken()
	if err != nil {
		return "", err
	}
	now := time.Now()
	at := models.AccessToken{
		Token: token, UserID: userID,
		CreatedAt: now, LastActivityAt: now, Type: "session",
	}
	if err := g.Create(&at).Error; err != nil {
		return "", err
	}
	return token, nil
}

func SetSessionCookie(w http.ResponseWriter, token string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookie, Value: token, Path: "/",
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
		MaxAge: SessionMaxAge,
	})
}

func ClearSessionCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookie, Value: "", Path: "/",
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
}

// sessionTokenEntry 会话缓存值：token -> 所属用户 ID 与 token 记录。
// 不缓存用户数据本身——用户资料统一走 user:id 缓存层，写路径按用户精确失效。
type sessionTokenEntry struct {
	UserID uint
	Token  models.AccessToken
}

// sessionCacheTTL 会话缓存存活时长（带随机抖动）。
func sessionCacheTTL() time.Duration {
	return cache.TTL(30*time.Second, 10*time.Second)
}

// loadUserByID 按 ID 加载用户（含用户组）。Cache-Aside：先查 user:id:{id}，
// 未命中查库并回写；updateProfile/Password/Avatar、adminUserSave 等
// 用户数据写路径显式失效该 Key。
func loadUserByID(g *gorm.DB, id uint) (models.User, bool) {
	if v, ok := cache.Default.Get(cache.UserIDKey(id)); ok {
		if u, ok := v.(models.User); ok {
			return u, true
		}
	}
	var u models.User
	if err := g.Preload("Groups").First(&u, id).Error; err != nil {
		return models.User{}, false
	}
	cache.Default.Set(cache.UserIDKey(id), u, sessionCacheTTL())
	return u, true
}

// UserFromRequest 依据 Cookie 中的 token 加载当前用户（含用户组）。
// Cache-Aside 两级：session:token:{token} -> (userID, token record)，
// user:id:{userID} -> 用户行；登出（DestroySession）与改密/封禁等写路径显式失效。
func UserFromRequest(g *gorm.DB, r *http.Request) (*models.User, *models.AccessToken) {
	c, err := r.Cookie(SessionCookie)
	if err != nil || c.Value == "" {
		return nil, nil
	}
	var at models.AccessToken
	hit := false
	if v, ok := cache.Default.Get(cache.SessionTokenKey(c.Value)); ok {
		if se, ok := v.(sessionTokenEntry); ok {
			at = se.Token
			hit = true
		}
	}
	if !hit {
		if err := g.Where("token = ?", c.Value).First(&at).Error; err != nil {
			return nil, nil
		}
	}
	u, ok := loadUserByID(g, at.UserID)
	if !ok {
		return nil, nil
	}
	// 活跃度更新（每小时一次，避免每请求写库）
	if time.Since(at.LastActivityAt) > time.Hour {
		g.Model(&models.AccessToken{}).Where("id = ?", at.ID).
			Update("last_activity_at", time.Now())
		at.LastActivityAt = time.Now()
	}
	cache.Default.Set(cache.SessionTokenKey(c.Value), sessionTokenEntry{UserID: at.UserID, Token: at}, sessionCacheTTL())
	// 返回副本：调用方对用户字段的修改不透传进缓存。
	uCopy := u
	uCopy.Groups = append([]models.Group(nil), u.Groups...)
	atCopy := at
	return &uCopy, &atCopy
}

func DestroySession(g *gorm.DB, token string) {
	if token != "" {
		g.Where("token = ?", token).Delete(&models.AccessToken{})
		cache.Default.Delete(cache.SessionTokenKey(token))
	}
}
