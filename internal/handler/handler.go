package handler

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/exzco/bbs/internal/config"
	"github.com/exzco/bbs/internal/models"
	"github.com/exzco/bbs/internal/service"
	"github.com/gorilla/csrf"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

const (
	SessionCookie = "jnsec_session"
	SessionMaxAge = 60 * 60 * 24 * 30
	perPageUsers  = 20
)

// Server handler 层：请求绑定、参数提取与 JSON 输出；业务逻辑全部在 service。
type Server struct {
	cfg *config.Config
	db  *gorm.DB
	svc *service.Service
}

func New(cfg *config.Config, g *gorm.DB) *Server {
	return &Server{cfg: cfg, db: g, svc: service.New(cfg, g)}
}

func (s *Server) Svc() *service.Service { return s.svc }

func setSessionCookie(w http.ResponseWriter, tok string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookie, Value: tok, Path: "/",
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
		MaxAge: SessionMaxAge,
	})
}

func clearSessionCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookie, Value: "", Path: "/",
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
}

func parsePage(c echo.Context) int {
	page, _ := strconv.Atoi(c.QueryParam("page"))
	if page < 1 {
		return 1
	}
	return page
}

func parseID(raw string) uint {
	v, _ := strconv.ParseUint(raw, 10, 32)
	return uint(v)
}

func sessionUser(c echo.Context) *models.User {
	u, _ := c.Get("user").(*models.User)
	if u != nil && u.IsBanned() {
		return nil
	}
	return u
}

// ---------- 健康检查与认证 ----------

func (s *Server) Health(c echo.Context) error {
	c.Response().Header().Set("Cache-Control", "no-store")
	if !s.svc.Health(c.Request().Context()) {
		return c.JSON(http.StatusServiceUnavailable, echo.Map{"status": "unavailable"})
	}
	return c.JSON(http.StatusOK, echo.Map{"status": "ok"})
}

func (s *Server) Me(c echo.Context) error {
	c.Response().Header().Set("Cache-Control", "no-store")
	resp := s.svc.Me(sessionUser(c))
	resp["csrfToken"] = csrf.Token(c.Request())
	return c.JSON(http.StatusOK, resp)
}

func (s *Server) Captcha(c echo.Context) error {
	return c.JSON(http.StatusOK, echo.Map{"id": s.svc.NewCaptcha()})
}

func (s *Server) Login(c echo.Context) error {
	var body models.LoginInput
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "请求格式错误。"})
	}
	u, serr := s.svc.Login(body.Username, body.Password)
	if serr != nil {
		return c.JSON(serr.Status, echo.Map{"error": serr.Message})
	}
	return s.startSession(c, u)
}

func (s *Server) Register(c echo.Context) error {
	var body models.RegisterInput
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "请求格式错误。"})
	}
	u, serr := s.svc.Register(body)
	if serr != nil {
		return c.JSON(serr.Status, echo.Map{"error": serr.Message})
	}
	return s.startSession(c, u)
}

func (s *Server) startSession(c echo.Context, u *models.User) error {
	c.Response().Header().Set("Cache-Control", "no-store")
	tok, user, unread, serr := s.svc.StartSession(u)
	if serr != nil {
		return c.JSON(serr.Status, echo.Map{"error": serr.Message})
	}
	setSessionCookie(c.Response(), tok, s.cfg.CookieSecure)
	return c.JSON(http.StatusOK, echo.Map{"user": user, "unread": unread, "csrfToken": csrf.Token(c.Request())})
}

func (s *Server) Logout(c echo.Context) error {
	tok, _ := c.Get("token").(string)
	s.svc.DestroySession(tok)
	clearSessionCookie(c.Response(), s.cfg.CookieSecure)
	return c.JSON(http.StatusOK, echo.Map{"ok": true})
}

// ---------- 个人设置与通知 ----------

func (s *Server) UpdateProfile(c echo.Context) error {
	var body models.ProfileInput
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "请求格式错误。"})
	}
	user, realName, blogURL, serr := s.svc.UpdateProfile(sessionUser(c), body)
	if serr != nil {
		return c.JSON(serr.Status, echo.Map{"error": serr.Message})
	}
	return c.JSON(http.StatusOK, echo.Map{"user": user, "realName": realName, "blogUrl": blogURL})
}

func (s *Server) UpdatePassword(c echo.Context) error {
	var body models.PasswordInput
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "请求格式错误。"})
	}
	tok, _ := c.Get("token").(string)
	if serr := s.svc.UpdatePassword(sessionUser(c), body, tok); serr != nil {
		return c.JSON(serr.Status, echo.Map{"error": serr.Message})
	}
	return c.JSON(http.StatusOK, echo.Map{"ok": true})
}

func (s *Server) UpdateAvatar(c echo.Context) error {
	u := sessionUser(c)
	if c.FormValue("action") == "remove" {
		avatar, serr := s.svc.RemoveAvatar(u)
		if serr != nil {
			return c.JSON(serr.Status, echo.Map{"error": serr.Message})
		}
		return c.JSON(http.StatusOK, echo.Map{"avatar": avatar})
	}
	fh, err := c.FormFile("file")
	if err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "请选择头像文件。"})
	}
	f, err := fh.Open()
	if err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "读取文件失败。"})
	}
	defer f.Close()
	avatar, serr := s.svc.SaveAvatar(u, fh.Size, f)
	if serr != nil {
		return c.JSON(serr.Status, echo.Map{"error": serr.Message})
	}
	return c.JSON(http.StatusOK, echo.Map{"avatar": avatar})
}

func (s *Server) UpdateEmail(c echo.Context) error {
	var body models.EmailInput
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "请求格式错误。"})
	}
	if serr := s.svc.UpdateEmail(sessionUser(c), body); serr != nil {
		return c.JSON(serr.Status, echo.Map{"error": serr.Message})
	}
	return c.JSON(http.StatusOK, echo.Map{"ok": true})
}

func (s *Server) UnbindEmail(c echo.Context) error {
	if serr := s.svc.UnbindEmail(sessionUser(c)); serr != nil {
		return c.JSON(serr.Status, echo.Map{"error": serr.Message})
	}
	return c.JSON(http.StatusOK, echo.Map{"ok": true})
}

func (s *Server) Notifications(c echo.Context) error {
	items, unread := s.svc.Notifications(sessionUser(c))
	return c.JSON(http.StatusOK, echo.Map{"items": items, "unread": unread})
}

func (s *Server) MarkNotificationsRead(c echo.Context) error {
	if err := s.svc.MarkNotificationsRead(sessionUser(c)); err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "操作失败，请重试。"})
	}
	return c.JSON(http.StatusOK, echo.Map{"ok": true})
}

// ---------- 首页 / 版块 / 用户主页 / 讨论 ----------

func (s *Server) Home(c echo.Context) error {
	return c.JSON(http.StatusOK, echo.Map{"cards": s.svc.Home(sessionUser(c))})
}

func (s *Server) Recent(c echo.Context) error {
	return c.JSON(http.StatusOK, s.svc.Recent(sessionUser(c), service.RecentQuery{
		Sidebar:      c.QueryParam("sidebar") == "1",
		StickyFirst:  c.QueryParam("sticky") != "0",
		ExcludeSlugs: strings.Split(c.QueryParam("exclude"), ","),
		Page:         parsePage(c),
	}))
}

func (s *Server) ListGroups(c echo.Context) error {
	return c.JSON(http.StatusOK, echo.Map{"groups": s.svc.ListGroups()})
}

func (s *Server) TagDetail(c echo.Context) error {
	res, serr := s.svc.TagDetail(sessionUser(c), c.Param("slug"))
	if serr != nil {
		return c.JSON(serr.Status, echo.Map{"error": serr.Message})
	}
	return c.JSON(http.StatusOK, res)
}

func (s *Server) TagDiscussions(c echo.Context) error {
	res, serr := s.svc.TagDiscussions(sessionUser(c), c.Param("slug"), parsePage(c), c.QueryParam("all") == "1")
	if serr != nil {
		return c.JSON(serr.Status, echo.Map{"error": serr.Message})
	}
	return c.JSON(http.StatusOK, res)
}

func (s *Server) PostForm(c echo.Context) error {
	return c.JSON(http.StatusOK, echo.Map{"groups": s.svc.PostForm(sessionUser(c))})
}

func (s *Server) Profile(c echo.Context) error {
	res, serr := s.svc.Profile(sessionUser(c), c.Param("username"))
	if serr != nil {
		return c.JSON(serr.Status, echo.Map{"error": serr.Message})
	}
	return c.JSON(http.StatusOK, res)
}

func (s *Server) UserActivity(c echo.Context) error {
	year := int(parseID(c.QueryParam("year")))
	res, serr := s.svc.UserActivity(sessionUser(c), c.Param("username"), year)
	if serr != nil {
		return c.JSON(serr.Status, echo.Map{"error": serr.Message})
	}
	return c.JSON(http.StatusOK, res)
}

func (s *Server) Preview(c echo.Context) error {
	var body models.ContentInput
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "请求格式错误。"})
	}
	return c.JSON(http.StatusOK, echo.Map{"html": s.svc.Preview(body.Content)})
}

func (s *Server) DiscussionDetail(c echo.Context) error {
	id := parseID(c.Param("id"))
	if id == 0 {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "该帖子不存在。"})
	}
	beforeNumber, _ := strconv.Atoi(c.QueryParam("before_number"))
	pp, _ := strconv.Atoi(c.QueryParam("per_page"))
	res, serr := s.svc.DiscussionDetail(sessionUser(c), id, service.DetailQuery{
		BeforeNumber: beforeNumber,
		PerPage:      pp,
		Page:         parsePage(c),
		HasPage:      c.QueryParam("page") != "",
		FromStart:    c.QueryParam("from_start") == "1" || c.QueryParam("from_start") == "true",
		LoadAll:      c.QueryParam("all") == "1" || c.QueryParam("all") == "true",
		ViewerIP:     c.RealIP(),
	})
	if serr != nil {
		return c.JSON(serr.Status, echo.Map{"error": serr.Message})
	}
	return c.JSON(http.StatusOK, res)
}

func (s *Server) SaveReadProgress(c echo.Context) error {
	id := parseID(c.Param("id"))
	if id == 0 {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "该帖子不存在。"})
	}
	var body models.ReadProgressInput
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "请求格式错误。"})
	}
	res, serr := s.svc.SaveReadPosition(sessionUser(c), id, body.PostID, body.PostNumber)
	if serr != nil {
		return c.JSON(serr.Status, echo.Map{"error": serr.Message})
	}
	return c.JSON(http.StatusOK, res)
}

func (s *Server) CreateDiscussion(c echo.Context) error {
	var body models.CreateDiscussionInput
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "请求格式错误。"})
	}
	res, serr := s.svc.CreateDiscussion(sessionUser(c), body.Title, body.Content, body.TagIDs)
	if serr != nil {
		return c.JSON(serr.Status, echo.Map{"error": serr.Message})
	}
	return c.JSON(http.StatusOK, res)
}

func (s *Server) Reply(c echo.Context) error {
	var body models.ContentInput
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "回复内容不能为空。"})
	}
	res, serr := s.svc.Reply(sessionUser(c), parseID(c.Param("id")), body.Content)
	if serr != nil {
		return c.JSON(serr.Status, echo.Map{"error": serr.Message})
	}
	return c.JSON(http.StatusOK, res)
}

func (s *Server) Moderate(c echo.Context) error {
	var body models.ModerateInput
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "请求格式错误。"})
	}
	if serr := s.svc.Moderate(sessionUser(c), parseID(c.Param("id")), body.Action, body.Title, body.TagIDs); serr != nil {
		return c.JSON(serr.Status, echo.Map{"error": serr.Message})
	}
	return c.JSON(http.StatusOK, echo.Map{"ok": true})
}

func (s *Server) PostEditGet(c echo.Context) error {
	res, serr := s.svc.PostEdit(sessionUser(c), parseID(c.Param("id")))
	if serr != nil {
		return c.JSON(serr.Status, echo.Map{"error": serr.Message})
	}
	return c.JSON(http.StatusOK, res)
}

func (s *Server) EditPost(c echo.Context) error {
	var body models.ContentInput
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "内容不能为空。"})
	}
	editedAt, serr := s.svc.EditPost(sessionUser(c), parseID(c.Param("id")), body.Content)
	if serr != nil {
		return c.JSON(serr.Status, echo.Map{"error": serr.Message})
	}
	return c.JSON(http.StatusOK, echo.Map{"ok": true, "editedAt": editedAt})
}

func (s *Server) DeleteDiscussion(c echo.Context) error {
	id := parseID(c.Param("id"))
	if id == 0 {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "该帖子不存在。"})
	}
	if serr := s.svc.DeleteDiscussion(sessionUser(c), id); serr != nil {
		return c.JSON(serr.Status, echo.Map{"error": serr.Message})
	}
	return c.JSON(http.StatusOK, echo.Map{"ok": true})
}

func (s *Server) ToggleLike(c echo.Context) error {
	res, serr := s.svc.ToggleLike(sessionUser(c), parseID(c.Param("id")))
	if serr != nil {
		return c.JSON(serr.Status, echo.Map{"error": serr.Message})
	}
	return c.JSON(http.StatusOK, res)
}

func (s *Server) Search(c echo.Context) error {
	res := s.svc.Search(sessionUser(c), c.QueryParam("q"))
	data := echo.Map{"q": res.Q}
	if res.Found {
		data["discussions"], data["posts"] = res.Discussions, res.Posts
	}
	return c.JSON(http.StatusOK, data)
}

// ---------- 附件上传 / 下载（multipart 与文件伺服为传输层职责） ----------

func (s *Server) Upload(c echo.Context) error {
	fh, err := c.FormFile("file")
	if err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "请选择要上传的文件。"})
	}
	f, err := fh.Open()
	if err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "读取文件失败。"})
	}
	defer f.Close()
	res, serr := s.svc.SaveUpload(sessionUser(c), fh.Filename, fh.Header.Get("Content-Type"), fh.Size, f)
	if serr != nil {
		return c.JSON(serr.Status, echo.Map{"error": serr.Message})
	}
	return c.JSON(http.StatusOK, res)
}

func (s *Server) DownloadFile(c echo.Context) error {
	path, name, serr := s.svc.DownloadUpload(c.Param("uuid"))
	if serr != nil {
		return c.JSON(serr.Status, echo.Map{"error": serr.Message})
	}
	if err := c.Attachment(path, name); err != nil && !c.Response().Committed {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "文件读取失败。"})
	}
	return nil
}

// ---------- 管理后台 ----------

func (s *Server) AdminMeta(c echo.Context) error {
	return c.JSON(http.StatusOK, s.svc.AdminMeta())
}

func (s *Server) AdminTags(c echo.Context) error {
	return c.JSON(http.StatusOK, echo.Map{"nodes": s.svc.AdminTags()})
}

func (s *Server) AdminSaveTag(c echo.Context) error {
	var body models.TagInput
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "请求格式错误。"})
	}
	if serr := s.svc.SaveTag(body); serr != nil {
		return c.JSON(serr.Status, echo.Map{"error": serr.Message})
	}
	return c.JSON(http.StatusOK, echo.Map{"ok": true})
}

func (s *Server) AdminDeleteTag(c echo.Context) error {
	if serr := s.svc.DeleteTag(parseID(c.Param("id"))); serr != nil {
		return c.JSON(serr.Status, echo.Map{"error": serr.Message})
	}
	return c.JSON(http.StatusOK, echo.Map{"ok": true})
}

func (s *Server) AdminUsers(c echo.Context) error {
	pp := perPageUsers
	if n, err := strconv.Atoi(c.QueryParam("per_page")); err == nil && n > 0 && n <= 1000 {
		pp = n
	}
	rows, pageRes := s.svc.AdminUsers(
		strings.TrimSpace(c.QueryParam("q")), parsePage(c), pp,
		c.QueryParam("all") == "1" || c.QueryParam("all") == "true",
		c.QueryParam("sort") == "desc" || c.QueryParam("order") == "desc",
	)
	return c.JSON(http.StatusOK, echo.Map{"users": rows, "page": pageRes})
}

func (s *Server) AdminUserSave(c echo.Context) error {
	var body models.AdminUserInput
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "请求格式错误。"})
	}
	user, ids, serr := s.svc.AdminUserSave(sessionUser(c), parseID(c.Param("id")), body.Action, body.Groups)
	if serr != nil {
		return c.JSON(serr.Status, echo.Map{"error": serr.Message})
	}
	return c.JSON(http.StatusOK, echo.Map{"ok": true, "user": user, "groupIds": ids})
}

func (s *Server) AdminListInvites(c echo.Context) error {
	return c.JSON(http.StatusOK, echo.Map{"invites": s.svc.ListInvites()})
}

func (s *Server) AdminCreateInvites(c echo.Context) error {
	var body models.CreateInvitesInput
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "请求格式错误。"})
	}
	codes, serr := s.svc.CreateInvites(sessionUser(c), body.Count, body.ExpiresDays)
	if serr != nil {
		return c.JSON(serr.Status, echo.Map{"error": serr.Message})
	}
	return c.JSON(http.StatusOK, echo.Map{"codes": codes})
}

func (s *Server) AdminHidden(c echo.Context) error {
	return c.JSON(http.StatusOK, echo.Map{"items": s.svc.AdminHidden(sessionUser(c))})
}

func (s *Server) AdminRestore(c echo.Context) error {
	if err := s.svc.AdminRestore(parseID(c.Param("id"))); err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "恢复失败，请重试。"})
	}
	return c.JSON(http.StatusOK, echo.Map{"ok": true})
}

func (s *Server) AdminSettings(c echo.Context) error {
	return c.JSON(http.StatusOK, s.svc.AdminSettings())
}

func (s *Server) AdminSaveSettings(c echo.Context) error {
	var body models.SettingsInput
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "请求格式错误。"})
	}
	if serr := s.svc.SaveSettings(body); serr != nil {
		return c.JSON(serr.Status, echo.Map{"error": serr.Message})
	}
	return c.JSON(http.StatusOK, echo.Map{"ok": true})
}
