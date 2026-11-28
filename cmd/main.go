package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/exzco/bbs/internal/config"
	"github.com/exzco/bbs/internal/handler"
	"github.com/exzco/bbs/internal/infra/db"
	"github.com/exzco/bbs/internal/middleware"
	"github.com/exzco/bbs/internal/service"
	"github.com/labstack/echo/v4"
	echomw "github.com/labstack/echo/v4/middleware"
)

func main() {
	log.SetFlags(log.LstdFlags)
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "serve":
			serveCmd(os.Args[2:])
			return
		default:
			os.Exit(2)
		}
	}
}

func loadCfg(args []string) *config.Config {
	fs := flag.NewFlagSet("bbs", flag.ContinueOnError)
	cfgPath := fs.String("c", "config.yaml", "配置文件路径")
	if err := fs.Parse(args); err != nil {
		log.Fatalf("[-] 参数解析失败: %v", err)
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("[-] 加载配置失败: %v", err)
	}
	return cfg
}

func serveCmd(args []string) {
	cfg := loadCfg(args)
	if cfg.SessionSecret == "" || cfg.SessionSecret == "CHANGE-ME-to-a-random-64-char-string" {
		log.Println("[-] session_secret 未配置或仍为默认值，请通过 JNSEC_SESSION_SECRET 配置会话密钥。")
	}
	if cfg.AssetsDir == "" {
		log.Println("[-] JNSEC_ASSETS_DIR 未配置：旧 Flarum 头像/附件（/assets/*）将无法访问，全部回退首字母头像。")
	}

	g, err := db.Open(cfg.Database.Driver, cfg.Database.DSN)
	if err != nil {
		log.Fatalf("[-] 连接数据库失败: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(cfg.DataDir, "avatars"), 0o755); err != nil {
		log.Fatalf("[-] 创建数据目录失败: %v", err)
	}
	if !cfg.ReadOnly {
		if err := db.CleanupJoinTableDuplicates(g); err != nil {
			log.Fatalf("[-] 清理关联表重复项失败: %v", err)
		}
	}

	mailCtx, stopMail := context.WithCancel(context.Background())
	defer stopMail()

	service.StartEmailOutbox(mailCtx, cfg, g, 30*time.Second)

	engine := echo.New()
	engine.HideBanner, engine.HidePort = true, true
	if cfg.TrustProxy {
		engine.IPExtractor = echo.ExtractIPFromXFFHeader()
	} else {
		engine.IPExtractor = echo.ExtractIPDirect()
	}

	h := handler.New(cfg, g)
	engine.Pre(middleware.CSRF(cfg))
	engine.Use(echomw.Recover())
	engine.Use(middleware.Session(h.Svc()))
	middleware.Register(engine, cfg, g, h)

	engine.Server = &http.Server{ReadHeaderTimeout: 10 * time.Second}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-quit
		log.Println("正在关闭…")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := engine.Shutdown(ctx); err != nil {
			log.Fatalf("HTTP 服务关闭超时: %v", err)
		}
	}()

	log.Printf("%s 启动于 http://%s", cfg.SiteName, cfg.Listen)
	if err := engine.Start(cfg.Listen); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("[-] HTTP 服务异常退出: %v", err)
	}
}
