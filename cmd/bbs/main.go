package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/exzco/bbs/internal/auth"
	"github.com/exzco/bbs/internal/config"
	"github.com/exzco/bbs/internal/db"
	"github.com/exzco/bbs/internal/models"
	"github.com/exzco/bbs/internal/service"
	"github.com/exzco/bbs/internal/web"
	"gorm.io/gorm"
)

func main() {
	log.SetFlags(log.LstdFlags)
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "serve":
			serveCmd(os.Args[2:])
			return
		case "audit-content":
			auditContentCmd(os.Args[2:])
			return
		case "init-db":
			cfg := loadCfg(os.Args[2:])
			g, err := db.Open(cfg.Database.Driver, cfg.Database.DSN)
			if err != nil {
				log.Fatal(err)
			}
			if err := db.InitializeEmpty(g); err != nil {
				log.Fatal(err)
			}
			log.Println("数据库初始化完成。")
			return
		case "migrate-db":
			migrateDBCmd(os.Args[2:])
			return
		case "native-import":
			nativeImportCmd(os.Args[2:])
			return
		case "seed-admin":
			seedAdminCmd(os.Args[2:])
			return
		default:
			usage()
			os.Exit(2)
		}
	}
	usage()
}

func usage() {
	fmt.Fprintf(os.Stderr, `JNSEC BBS (Go)

用法:
  bbs audit-content [-c config.yaml]      只读输出正文源文本校验和
  bbs init-db [-c config.yaml]            初始化独立空库（拒绝已有表）
  bbs migrate-db [-c config.yaml]         显式升级 Go 辅助表（不改 Flarum 表）
  bbs native-import [-initialize] -read-only-target
                                          导入独立原生影子库（DSN 从导入环境变量读取）
  bbs serve [-c config.yaml]              启动服务
  bbs seed-admin [-c config.yaml] -username x -email x@y -password xxx
                                          创建/提升管理员
`)
}

func loadCfg(args []string) *config.Config {
	fs := flag.NewFlagSet("bbs", flag.ContinueOnError)
	cfgPath := fs.String("c", "config.yaml", "配置文件路径")
	if err := fs.Parse(args); err != nil {
		log.Fatalf("参数解析失败: %v", err)
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}
	return cfg
}

func openDB(cfg *config.Config) *gorm.DB {
	g, err := db.Open(cfg.Database.Driver, cfg.Database.DSN)
	if err != nil {
		log.Fatalf("连接数据库失败: %v", err)
	}
	if cfg.ReadOnly {
		if err := db.CheckReadOnly(g); err != nil {
			log.Fatalf("只读数据库校验失败: %v", err)
		}
		return g
	}
	if err := db.CheckWritable(g); err != nil {
		log.Fatalf("数据库校验失败: %v", err)
	}
	return g
}

func serveCmd(args []string) {
	cfg := loadCfg(args)
	if cfg.SessionSecret == "" || cfg.SessionSecret == "CHANGE-ME-to-a-random-64-char-string" {
		log.Println("[警告] session_secret 未配置或仍为默认值，生产环境请在 config.yaml 中设置随机密钥。")
	}
	if cfg.AssetsDir == "" {
		log.Println("[警告] JNSEC_ASSETS_DIR 未配置：旧 Flarum 头像/附件（/assets/*）将无法访问，全部回退首字母头像。")
	}
	g := openDB(cfg)

	srv, err := web.New(cfg, g)
	if err != nil {
		log.Fatalf("初始化 Web 服务失败: %v", err)
	}
	// 邮件通知后台投递（未配置 SMTP 时不启动）；随进程退出而停止。
	mailCtx, stopMail := context.WithCancel(context.Background())
	defer stopMail()
	service.StartEmailOutbox(mailCtx, cfg, g, 30*time.Second)
	engine := srv.Engine()
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
		log.Fatalf("HTTP 服务异常退出: %v", err)
	}
}

func seedAdminCmd(args []string) {
	fs := flag.NewFlagSet("seed-admin", flag.ContinueOnError)
	cfgPath := fs.String("c", "config.yaml", "配置文件路径")
	username := fs.String("username", "", "管理员用户名")
	email := fs.String("email", "", "邮箱")
	password := fs.String("password", "", "密码（至少 8 位）")
	if err := fs.Parse(args); err != nil {
		log.Fatalf("参数解析失败: %v", err)
	}
	cfg := loadCfg([]string{"-c", *cfgPath})
	if cfg.ReadOnly {
		log.Fatal("只读模式禁止创建或修改管理员")
	}
	g := openDB(cfg)

	if *username == "" || *email == "" || len(*password) < 8 {
		log.Fatal("需要 -username -email -password（密码至少 8 位）")
	}
	hash, err := auth.HashPassword(*password)
	if err != nil {
		log.Fatalf("生成密码失败: %v", err)
	}
	var u models.User
	err = g.Where("lower(username) = lower(?)", *username).First(&u).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		u = models.User{Username: *username, Email: *email, Password: hash}
		if err := g.Create(&u).Error; err != nil {
			log.Fatalf("创建管理员失败: %v", err)
		}
		fmt.Printf("已创建管理员 %s (ID=%d)\n", u.Username, u.ID)
	} else if err != nil {
		log.Fatalf("查询用户失败: %v", err)
	} else {
		updates := map[string]any{"password": hash, "email": *email, "suspended_until": nil}
		if err := g.Model(&u).Updates(updates).Error; err != nil {
			log.Fatalf("更新管理员失败: %v", err)
		}
		fmt.Printf("已将现有用户 %s (ID=%d) 提升为管理员并重置密码\n", u.Username, u.ID)
	}
	admin := models.Group{ID: models.GroupAdmin}
	if err := g.Model(&u).Association("Groups").Append(&admin); err != nil {
		log.Fatalf("设置管理员组失败: %v", err)
	}
}
