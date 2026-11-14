// Package cache 提供进程内 TTL 缓存，承载后端查库路径的 Cache-Aside 读写：
// 读路径先查缓存、未命中查库并回写；写路径在 DB 成功后显式失效（Invalidate）
// 对应 Key。所有 Key 遵循 `{module}:{entity}:{identifier_or_hash}` 规范。
package cache

import (
	"math/rand/v2"
	"strconv"
	"strings"
	"sync"
	"time"
)

// entry 单条缓存值及其过期时刻。
type entry struct {
	value   any
	expires time.Time
}

// Cache 并发安全的进程内 TTL 缓存。
type Cache struct {
	mu      sync.RWMutex
	entries map[string]entry
}

// Default 全局缓存实例（单进程服务，全部查库路径共享）。
var Default = New(10 * time.Minute)

// New 创建缓存并启动后台过期清扫协程（懒过期之外兜底回收内存）。
func New(sweepInterval time.Duration) *Cache {
	c := &Cache{entries: make(map[string]entry)}
	go func() {
		for range time.Tick(sweepInterval) {
			now := time.Now()
			c.mu.Lock()
			for k, e := range c.entries {
				if now.After(e.expires) {
					delete(c.entries, k)
				}
			}
			c.mu.Unlock()
		}
	}()
	return c
}

// TTL 返回叠加随机抖动（±jitter）后的存活时长，避免大量 Key 集中过期。
func TTL(base, jitter time.Duration) time.Duration {
	if jitter <= 0 {
		return base
	}
	return base + time.Duration(rand.Int64N(int64(2*jitter)+1)) - jitter
}

// Get 取缓存值；已过期视为未命中。
func (c *Cache) Get(key string) (any, bool) {
	c.mu.RLock()
	e, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok || time.Now().After(e.expires) {
		return nil, false
	}
	return e.value, true
}

// Set 写入缓存值。
func (c *Cache) Set(key string, value any, ttl time.Duration) {
	c.mu.Lock()
	c.entries[key] = entry{value: value, expires: time.Now().Add(ttl)}
	c.mu.Unlock()
}

// Delete 显式失效单个 Key（写路径成功后调用）。
func (c *Cache) Delete(key string) {
	c.mu.Lock()
	delete(c.entries, key)
	c.mu.Unlock()
}

// DeletePrefix 显式失效一批同前缀 Key（如按模块整体失效）。
func (c *Cache) DeletePrefix(prefix string) {
	c.mu.Lock()
	for k := range c.entries {
		if strings.HasPrefix(k, prefix) {
			delete(c.entries, k)
		}
	}
	c.mu.Unlock()
}

// ---------- 缓存 Key（{module}:{entity}:{identifier_or_hash}） ----------

// SessionTokenKey 会话缓存：token -> (user, token record)。
func SessionTokenKey(token string) string { return "session:token:" + token }

// SessionsPrefix 全部会话缓存前缀（改密/封禁等批量踢会话时整体失效）。
func SessionsPrefix() string { return "session:token:" }

// UserIDKey 用户资料缓存：userID -> flarum_users 行（含用户组）。
func UserIDKey(id uint) string { return "user:id:" + strconv.FormatUint(uint64(id), 10) }

// UserNameKey 用户名解析缓存（仅正向命中）：lower(username) -> userID。
// 用户名不可变更且 ID 不回收，正向映射恒真，无需失效。
func UserNameKey(lowerName string) string { return "user:name:" + lowerName }

// SettingKey 站点设置缓存：flarum_settings.key -> value。
func SettingKey(key string) string { return "setting:" + key }

// TagPermsKey 版块权限快照缓存：全量 flarum_tags + flarum_group_permission
// 组成的 auth.TagPerms。
func TagPermsKey() string { return "tag:perms:all" }

// TagCountsKey 版块讨论计数缓存：tagID -> 可见讨论数。
func TagCountsKey() string { return "tag:counts:all" }
