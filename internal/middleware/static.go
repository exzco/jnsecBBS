package middleware

import (
	"fmt"
	"hash/fnv"
	"html"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/dchest/captcha"
	"github.com/exzco/bbs/internal/config"
	"github.com/exzco/bbs/internal/frontend"
	"github.com/labstack/echo/v4"
)

func registerStatic(e *echo.Echo, cfg *config.Config) {
	// 磁盘目录伺服：/assets/* 头像与附件，/avatars/* 上传头像
	serveDir := func(dirs []string) echo.HandlerFunc {
		return func(c echo.Context) error {
			clean := filepath.Clean("/" + c.Param("*"))
			for _, dir := range dirs {
				local := filepath.Join(dir, clean)
				if st, err := os.Stat(local); err == nil && !st.IsDir() {
					return c.File(local)
				}
			}
			return c.NoContent(http.StatusNotFound)
		}
	}
	assetsDirs := []string{filepath.Join(cfg.DataDir, "assets")}
	if cfg.AssetsDir != "" {
		assetsDirs = append(assetsDirs, cfg.AssetsDir)
	}
	e.GET("/assets/*", serveDir(assetsDirs))
	e.HEAD("/assets/*", serveDir(assetsDirs))
	e.GET("/avatars/*", serveDir([]string{filepath.Join(cfg.DataDir, "avatars")}))
	e.HEAD("/avatars/*", serveDir([]string{filepath.Join(cfg.DataDir, "avatars")}))

	// 字环头像与注册验证码图片
	e.GET("/avatar/:name", letterAvatar)
	e.GET("/captcha/:id", captchaImage)

	// SPA 兜底路由
	e.GET("/*", serveSPA)
	e.HEAD("/*", serveSPA)
}

func serveSPA(c echo.Context) error {
	path := strings.TrimPrefix(c.Request().URL.Path, "/")
	if strings.HasPrefix(path, "api/") || path == "api" {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "接口不存在。"})
	}
	if strings.HasPrefix(path, "assets/") || strings.HasPrefix(path, "avatars/") {
		return c.NoContent(http.StatusNotFound)
	}
	if path == "" {
		path = "index.html"
	}
	distPath := strings.TrimPrefix(path, "static/")
	if distPath == "index.html" || (!strings.HasPrefix(path, "static/") && !strings.Contains(distPath, ".")) {
		return serveIndex(c)
	}
	distFS, _ := fs.Sub(frontend.Dist, "dist")
	if st, err := fs.Stat(distFS, distPath); err == nil && !st.IsDir() {
		if strings.HasPrefix(path, "static/assets/") {
			c.Response().Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		http.ServeFileFS(c.Response(), c.Request(), distFS, distPath)
		return nil
	}
	if strings.HasPrefix(path, "static/") {
		return c.NoContent(http.StatusNotFound)
	}
	return serveIndex(c)
}

func serveIndex(c echo.Context) error {
	index, err := fs.ReadFile(frontend.Dist, "dist/index.html")
	if err != nil {
		return c.String(http.StatusInternalServerError, "前端未构建：请在 frontend/ 执行 bun run build 后重新编译。")
	}
	c.Response().Header().Set("Cache-Control", "no-cache")
	return c.Blob(http.StatusOK, "text/html; charset=utf-8", index)
}

var avatarPalette = []string{"#1e88e5", "#43a047", "#e53935", "#8e24aa", "#f4511e", "#00897b", "#3949ab", "#d81b60", "#6d4c41", "#7cb342"}

func letterAvatar(c echo.Context) error {
	name := strings.TrimSuffix(c.Param("name"), ".svg")
	name = strings.TrimPrefix(name, "/")
	first := "?"
	for _, r := range name {
		first = string(r)
		break
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	color := avatarPalette[int(h.Sum32())%len(avatarPalette)]
	svg := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="128" height="128" viewBox="0 0 128 128">`+
		`<rect width="128" height="128" rx="64" fill="%s"/>`+
		`<text x="64" y="64" dy="0.36em" text-anchor="middle" font-family="system-ui, sans-serif" font-size="56" fill="#fff">%s</text></svg>`,
		color, html.EscapeString(first))
	c.Response().Header().Set("Cache-Control", "public, max-age=86400")
	return c.Blob(http.StatusOK, "image/svg+xml", []byte(svg))
}

func captchaImage(c echo.Context) error {
	id := strings.TrimSuffix(strings.TrimPrefix(c.Param("id"), "/"), ".png")
	if c.QueryParam("reload") != "" {
		captcha.Reload(id)
	}
	c.Response().Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	c.Response().Header().Set("Pragma", "no-cache")
	c.Response().Header().Set("Content-Type", "image/png")
	if err := captcha.WriteImage(c.Response(), id, captcha.StdWidth*2, captcha.StdHeight*2); err != nil {
		return c.NoContent(http.StatusNotFound)
	}
	return nil
}
