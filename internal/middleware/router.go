package middleware

import (
	"github.com/exzco/bbs/internal/config"
	"github.com/exzco/bbs/internal/handler"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

// Register 注册健康检查、REST API 与静态资源 / SPA 路由。
func Register(e *echo.Echo, cfg *config.Config, _ *gorm.DB, h *handler.Server) {
	e.GET("/api/health", h.Health)

	authUser := RequireUser()
	authAdmin := RequireAdmin()

	// 公开接口：会话、注册与管理（登录/登出不受只读拦截，见 ReadOnly）。
	pub := e.Group("/api", NoStore(), ReadOnly(cfg))
	pub.GET("/me", h.Me)
	pub.GET("/auth/captcha", h.Captcha)
	pub.POST("/auth/login", h.Login, LimitWith(AuthLimiter))
	pub.POST("/auth/register", h.Register, LimitWith(AuthLimiter))
	pub.POST("/auth/logout", h.Logout)

	// 内容类接口全部要求登录（内部论坛：所有内容请登录后查看）。
	pub.POST("/me/profile", h.UpdateProfile, authUser)
	pub.POST("/me/password", h.UpdatePassword, authUser)
	pub.POST("/me/avatar", h.UpdateAvatar, authUser)
	pub.POST("/me/email", h.UpdateEmail, authUser)
	pub.POST("/me/email/unbind", h.UnbindEmail, authUser)
	pub.GET("/me/notifications", h.Notifications, authUser)
	pub.POST("/me/notifications/read", h.MarkNotificationsRead, authUser)
	pub.GET("/home", h.Home, authUser)
	pub.GET("/recent", h.Recent, authUser)
	pub.GET("/groups", h.ListGroups, authUser)
	pub.GET("/tags/:slug", h.TagDetail, authUser)
	pub.GET("/tags/:slug/discussions", h.TagDiscussions, authUser)
	pub.GET("/post-form", h.PostForm, authUser)
	pub.POST("/preview", h.Preview, authUser)
	pub.POST("/upload", h.Upload, authUser)
	pub.GET("/fof/download/:uuid", h.DownloadFile, authUser)
	pub.HEAD("/fof/download/:uuid", h.DownloadFile, authUser)
	pub.GET("/search", h.Search, authUser)
	pub.GET("/users/:username", h.Profile, authUser)
	pub.GET("/users/:username/activity", h.UserActivity, authUser)

	d := pub.Group("/discussions")
	d.GET("/:id", h.DiscussionDetail, authUser)
	d.POST("", h.CreateDiscussion, authUser, LimitWith(PostLimiter))
	d.POST("/:id/reply", h.Reply, authUser, LimitWith(PostLimiter))
	d.POST("/:id/read-progress", h.SaveReadProgress, authUser)
	d.POST("/:id/mod", h.Moderate, authUser)
	d.POST("/:id/delete", h.DeleteDiscussion, authUser)

	p := pub.Group("/posts")
	p.GET("/:id/edit", h.PostEditGet, authUser)
	p.POST("/:id/edit", h.EditPost, authUser)
	p.POST("/:id/like", h.ToggleLike, authUser)

	pub.GET("/admin/meta", h.AdminMeta, authAdmin)
	pub.GET("/admin/tags", h.AdminTags, authAdmin)
	pub.POST("/admin/tags", h.AdminSaveTag, authAdmin)
	pub.POST("/admin/tags/:id/delete", h.AdminDeleteTag, authAdmin)
	pub.GET("/admin/users", h.AdminUsers, authAdmin)
	pub.POST("/admin/users/:id", h.AdminUserSave, authAdmin)
	pub.GET("/admin/invites", h.AdminListInvites, authAdmin)
	pub.POST("/admin/invites", h.AdminCreateInvites, authAdmin)
	pub.GET("/admin/hidden", h.AdminHidden, authAdmin)
	pub.POST("/admin/discussions/:id/restore", h.AdminRestore, authAdmin)
	pub.GET("/admin/settings", h.AdminSettings, authAdmin)
	pub.POST("/admin/settings", h.AdminSaveSettings, authAdmin)

	// 静态资源 / 媒体 / SPA
	registerStatic(e, cfg)
}
