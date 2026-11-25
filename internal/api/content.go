package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/exzco/bbs/internal/service"
	"github.com/labstack/echo/v4"
)

// ---------- 内容类薄 handler：绑定 -> 调 Service -> 输出 ----------

// parseExcludeSlugs 解析 exclude=slug1,slug2（最多 10 个、单个长度 ≤100）。
func parseExcludeSlugs(raw string) []string {
	var slugs []string
	for _, slug := range strings.Split(raw, ",") {
		if slug = strings.TrimSpace(slug); slug != "" && len(slug) <= 100 && len(slugs) < 10 {
			slugs = append(slugs, slug)
		}
	}
	return slugs
}

func (s *Server) recent(c echo.Context) (any, *service.Error) {
	return s.svc.Recent(s.currentUser(c), service.RecentQuery{
		Sidebar:      c.QueryParam("sidebar") == "1",
		StickyFirst:  c.QueryParam("sidebar") != "1" && c.QueryParam("sticky") != "0",
		ExcludeSlugs: parseExcludeSlugs(c.QueryParam("exclude")),
		Page:         pageParam(c),
	}), nil
}

func (s *Server) userActivity(c echo.Context) (any, *service.Error) {
	year := time.Now().Year()
	if v := c.QueryParam("year"); v != "" {
		if n, err := parseUint(v); err == nil && n >= 2000 && int(n) <= year {
			year = int(n)
		}
	}
	return s.svc.UserActivity(s.currentUser(c), c.Param("username"), year)
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

// reply 回帖（独立命名方法：并发回归测试直接挂载本 handler）。
func (s *Server) reply(c echo.Context) (any, *service.Error) {
	body, err := bindJSON[struct {
		Content string `json:"content"`
	}](c)
	if err != nil || strings.TrimSpace(body.Content) == "" {
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
	if err != nil || strings.TrimSpace(body.Content) == "" {
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
	res := s.svc.Search(s.currentUser(c), strings.TrimSpace(c.QueryParam("q")))
	data := echo.Map{"q": res.Q}
	if res.Found {
		data["discussions"], data["posts"] = res.Discussions, res.Posts
	}
	return data, nil
}

// ---------- 附件上传 / 下载（multipart 与文件伺服为传输层职责） ----------

// upload 处理论坛图片与附件上传（multipart 提取在传输层，落盘与记录在 service）。
func (s *Server) upload(c echo.Context) (any, *service.Error) {
	if s.cfg.ReadOnly {
		return nil, service.Err(http.StatusForbidden, "只读模式，暂不能上传文件。")
	}
	fh, err := c.FormFile("file")
	if err != nil {
		return nil, service.Err(http.StatusBadRequest, "请选择要上传的文件。")
	}
	if fh.Size > 25<<20 {
		return nil, service.Err(http.StatusBadRequest, "文件大小不能超过 25MB。")
	}
	f, err := fh.Open()
	if err != nil {
		return nil, service.Err(http.StatusBadRequest, "读取文件失败。")
	}
	defer f.Close()
	res, serr := s.svc.SaveUpload(s.currentUser(c), fh.Filename, fh.Header.Get("Content-Type"), fh.Size, f)
	if serr != nil {
		return nil, serr
	}
	return echo.Map{
		"url": res.URL, "uuid": res.UUID, "name": res.Name,
		"size": res.Size, "isImage": res.IsImage, "markdown": res.Markdown,
	}, nil
}

// downloadFile 兼容 Flarum fof/upload 的 UUID 附件下载。
func (s *Server) downloadFile(c echo.Context) (any, *service.Error) {
	rec, err := s.svc.FindUpload(c.Param("uuid"))
	if err != nil {
		return nil, err
	}
	cleanRel := filepath.Clean("/" + rec.Path)
	// 优先从 DataDir/assets/files/ 读取，回退至 AssetsDir/files/
	paths := []string{filepath.Join(s.cfg.DataDir, "assets", "files", cleanRel)}
	if s.cfg.AssetsDir != "" {
		paths = append(paths, filepath.Join(s.cfg.AssetsDir, "files", cleanRel))
	}
	for _, path := range paths {
		if st, serr := os.Stat(path); serr == nil && !st.IsDir() {
			// 附件直接写出响应；提交前失败（如文件打不开）显式 500，已提交后的
			// 传输层错误无法转换为响应，交由 h 的 Response.Committed 跳过 JSON。
			if err := c.Attachment(path, rec.BaseName); err != nil && !c.Response().Committed {
				return nil, service.Err(http.StatusInternalServerError, "文件读取失败。")
			}
			return nil, nil
		}
	}
	return nil, service.Err(http.StatusNotFound, "文件未找到。")
}
