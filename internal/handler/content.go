package handler

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/exzco/bbs/internal/service"
	"github.com/labstack/echo/v4"
)

func (s *Server) recent(c echo.Context) (any, *service.Error) {
	return s.svc.Recent(s.currentUser(c), service.RecentQuery{
		Sidebar:      c.QueryParam("sidebar") == "1",
		StickyFirst:  c.QueryParam("sticky") != "0",
		ExcludeSlugs: strings.Split(c.QueryParam("exclude"), ","),
		Page:         pageParam(c),
	}), nil
}

func (s *Server) userActivity(c echo.Context) (any, *service.Error) {
	year, _ := parseUint(c.QueryParam("year"))
	return s.svc.UserActivity(s.currentUser(c), c.Param("username"), int(year))
}

func (s *Server) preview(c echo.Context) (any, *service.Error) {
	body, err := bindJSON[struct {
		Content string `json:"content"`
	}](c)
	if err != nil {
		return nil, err
	}
	return echo.Map{"html": s.svc.Preview(body.Content)}, nil
}

func (s *Server) discussionDetail(c echo.Context) (any, *service.Error) {
	id, _ := parseUint(c.Param("id"))
	if id == 0 {
		return nil, service.Err(http.StatusNotFound, "该帖子不存在。")
	}
	beforeNumber, _ := strconv.Atoi(c.QueryParam("before_number"))
	pp, _ := strconv.Atoi(c.QueryParam("per_page"))
	return s.svc.DiscussionDetail(s.currentUser(c), id, service.DetailQuery{
		BeforeNumber: beforeNumber,
		PerPage:      pp,
		Page:         pageParam(c),
		HasPage:      c.QueryParam("page") != "",
		FromStart:    c.QueryParam("from_start") == "1" || c.QueryParam("from_start") == "true",
		LoadAll:      c.QueryParam("all") == "1" || c.QueryParam("all") == "true",
		ViewerIP:     c.RealIP(),
	})
}

func (s *Server) saveReadProgress(c echo.Context) (any, *service.Error) {
	id, _ := parseUint(c.Param("id"))
	if id == 0 {
		return nil, service.Err(http.StatusNotFound, "该帖子不存在。")
	}
	body, err := bindJSON[struct {
		PostID     uint `json:"postId"`
		PostNumber int  `json:"postNumber"`
	}](c)
	if err != nil {
		return nil, err
	}
	return s.svc.SaveReadPosition(s.currentUser(c), id, body.PostID, body.PostNumber)
}

func (s *Server) createDiscussion(c echo.Context) (any, *service.Error) {
	body, err := bindJSON[struct {
		Title   string `json:"title"`
		Content string `json:"content"`
		TagIDs  []uint `json:"tagIds"`
	}](c)
	if err != nil {
		return nil, err
	}
	return s.svc.CreateDiscussion(s.currentUser(c), body.Title, body.Content, body.TagIDs)
}

// reply 绑定回帖请求并调用业务层。
func (s *Server) reply(c echo.Context) (any, *service.Error) {
	body, err := bindJSON[struct {
		Content string `json:"content"`
	}](c)
	if err != nil {
		return nil, service.Err(http.StatusBadRequest, "回复内容不能为空。")
	}
	id, _ := parseUint(c.Param("id"))
	return s.svc.Reply(s.currentUser(c), id, body.Content)
}

func (s *Server) moderate(c echo.Context) (any, *service.Error) {
	body, err := bindJSON[struct {
		Action string `json:"action"`
		Title  string `json:"title"`
		TagIDs []uint `json:"tagIds"`
	}](c)
	if err != nil {
		return nil, err
	}
	id, _ := parseUint(c.Param("id"))
	if serr := s.svc.Moderate(s.currentUser(c), id, body.Action, body.Title, body.TagIDs); serr != nil {
		return nil, serr
	}
	return echo.Map{"ok": true}, nil
}

func (s *Server) postEditGet(c echo.Context) (any, *service.Error) {
	id, _ := parseUint(c.Param("id"))
	return s.svc.PostEdit(s.currentUser(c), id)
}

func (s *Server) editPost(c echo.Context) (any, *service.Error) {
	body, err := bindJSON[struct {
		Content string `json:"content"`
	}](c)
	if err != nil {
		return nil, service.Err(http.StatusBadRequest, "内容不能为空。")
	}
	id, _ := parseUint(c.Param("id"))
	editedAt, serr := s.svc.EditPost(s.currentUser(c), id, body.Content)
	if serr != nil {
		return nil, serr
	}
	return echo.Map{"ok": true, "editedAt": editedAt}, nil
}

func (s *Server) hidePost(c echo.Context) (any, *service.Error) {
	id, _ := parseUint(c.Param("id"))
	hidden, err := s.svc.HidePost(s.currentUser(c), id)
	if err != nil {
		return nil, err
	}
	return echo.Map{"hidden": hidden}, nil
}

func (s *Server) toggleLike(c echo.Context) (any, *service.Error) {
	id, _ := parseUint(c.Param("id"))
	return s.svc.ToggleLike(s.currentUser(c), id)
}

func (s *Server) search(c echo.Context) (any, *service.Error) {
	res := s.svc.Search(s.currentUser(c), c.QueryParam("q"))
	data := echo.Map{"q": res.Q}
	if res.Found {
		data["discussions"], data["posts"] = res.Discussions, res.Posts
	}
	return data, nil
}

// ---------- 附件上传 / 下载（multipart 与文件伺服为传输层职责） ----------

// upload 处理论坛图片与附件上传（multipart 提取在传输层，落盘与记录在 service）。
func (s *Server) upload(c echo.Context) (any, *service.Error) {
	fh, err := c.FormFile("file")
	if err != nil {
		return nil, service.Err(http.StatusBadRequest, "请选择要上传的文件。")
	}
	f, err := fh.Open()
	if err != nil {
		return nil, service.Err(http.StatusBadRequest, "读取文件失败。")
	}
	defer f.Close()
	return s.svc.SaveUpload(s.currentUser(c), fh.Filename, fh.Header.Get("Content-Type"), fh.Size, f)
}

// downloadFile 兼容 Flarum fof/upload 的 UUID 附件下载。
func (s *Server) downloadFile(c echo.Context) (any, *service.Error) {
	path, name, err := s.svc.DownloadUpload(c.Param("uuid"))
	if err != nil {
		return nil, err
	}
	// 附件响应属于 HTTP 层；提交后的传输错误无法改写为 JSON。
	if err := c.Attachment(path, name); err != nil && !c.Response().Committed {
		return nil, service.Err(http.StatusInternalServerError, "文件读取失败。")
	}
	return nil, nil
}
