package api

import (
	"log"
	"net/http"

	"github.com/exzco/bbs/internal/auth"
	"github.com/exzco/bbs/internal/models"
	"github.com/exzco/bbs/internal/service"
	"github.com/gorilla/csrf"
	"github.com/labstack/echo/v4"
)

// me 当前会话信息：用户 + CSRF token + 站点设置。
func (s *Server) me(c echo.Context) (any, *service.Error) {
	u := s.currentUser(c)
	resp := echo.Map{
		"user":         s.svc.UserDTO(u),
		"readOnly":     s.cfg.ReadOnly,
		"csrfToken":    csrf.Token(c.Request()),
		"siteName":     s.svc.SettingValue(models.SettingKeySiteName),
		"registration": s.svc.SettingValue(models.SettingKeyRegistration),
		"welcomeText":  s.svc.SettingValue(models.SettingKeyWelcomeText),
	}
	if u != nil {
		resp["email"] = u.Email
		resp["emailBound"] = s.svc.EmailBound(u.ID)
		resp["unread"] = s.svc.UnreadCount(u.ID)
		resp["realName"], resp["blogUrl"] = s.svc.LoadUserProfileExtra(u.ID)
	}
	return resp, nil
}

// updateEmail 换绑邮箱（需当前密码确认）。
func (s *Server) updateEmail(c echo.Context) (any, *service.Error) {
	body, err := bindJSON[service.EmailInput](c)
	if err != nil {
		return nil, err
	}
	if serr := s.svc.UpdateEmail(s.currentUser(c), *body); serr != nil {
		return nil, serr
	}
	return echo.Map{"ok": true}, nil
}

func (s *Server) login(c echo.Context) (any, *service.Error) {
	body, err := bindJSON[struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}](c)
	if err != nil || body.Username == "" || body.Password == "" {
		return nil, service.Err(http.StatusBadRequest, "请输入用户名和密码。")
	}
	u, serr := s.svc.Login(body.Username, body.Password)
	if serr != nil {
		return nil, serr
	}
	return s.startSession(c, u)
}

// startSession 创建会话 token、写 Cookie 并返回会话信息。
func (s *Server) startSession(c echo.Context, u *models.User) (any, *service.Error) {
	token, err := auth.CreateSession(s.db, u.ID, c.Request().UserAgent(), s.cfg.CookieSecure)
	if err != nil {
		log.Printf("[api] CreateSession 失败 user=%s: %v", u.Username, err)
		return nil, service.Err(http.StatusInternalServerError, "登录失败，请重试。")
	}
	auth.SetSessionCookie(c.Response(), token, s.cfg.CookieSecure)
	if err := s.svc.TouchSeen(u); err != nil {
		return nil, service.Err(http.StatusInternalServerError, "登录失败，请重试。")
	}
	return echo.Map{"user": s.svc.UserDTO(u), "unread": s.svc.UnreadCount(u.ID), "csrfToken": csrf.Token(c.Request())}, nil
}

func (s *Server) register(c echo.Context) (any, *service.Error) {
	body, err := bindJSON[service.RegisterInput](c)
	if err != nil {
		return nil, err
	}
	u, serr := s.svc.Register(*body)
	if serr != nil {
		return nil, serr
	}
	return s.startSession(c, u)
}

func (s *Server) logout(c echo.Context) (any, *service.Error) {
	auth.DestroySession(s.db, token(c))
	auth.ClearSessionCookie(c.Response(), s.cfg.CookieSecure)
	return echo.Map{"ok": true}, nil
}

// ---------- 个人设置 ----------

func (s *Server) updateProfile(c echo.Context) (any, *service.Error) {
	body, err := bindJSON[service.ProfileInput](c)
	if err != nil {
		return nil, err
	}
	user, realName, blogURL, serr := s.svc.UpdateProfile(s.currentUser(c), *body)
	if serr != nil {
		return nil, serr
	}
	return echo.Map{"user": user, "realName": realName, "blogUrl": blogURL}, nil
}

func (s *Server) updatePassword(c echo.Context) (any, *service.Error) {
	body, err := bindJSON[service.PasswordInput](c)
	if err != nil {
		return nil, err
	}
	if serr := s.svc.UpdatePassword(s.currentUser(c), *body, token(c)); serr != nil {
		return nil, serr
	}
	return echo.Map{"ok": true}, nil
}

func (s *Server) updateAvatar(c echo.Context) (any, *service.Error) {
	u := s.currentUser(c)
	if c.FormValue("action") == "remove" {
		avatar, serr := s.svc.RemoveAvatar(u)
		return echo.Map{"avatar": avatar}, serr
	}
	fh, err := c.FormFile("file")
	if err != nil {
		return nil, service.Err(http.StatusBadRequest, "请选择头像文件。")
	}
	if fh.Size > 2<<20 {
		return nil, service.Err(http.StatusBadRequest, "头像文件不能超过 2MB。")
	}
	f, err := fh.Open()
	if err != nil {
		return nil, service.Err(http.StatusBadRequest, "读取文件失败。")
	}
	defer f.Close()
	avatar, serr := s.svc.SaveAvatar(u, f)
	if serr != nil {
		return nil, serr
	}
	return echo.Map{"avatar": avatar}, nil
}
