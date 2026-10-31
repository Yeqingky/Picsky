// Command picsky 是 Picsky 图片上传中间层的服务入口.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/yeqingky/Picsky/internal/api"
	"github.com/yeqingky/Picsky/internal/config"
	"github.com/yeqingky/Picsky/internal/db"
	"github.com/yeqingky/Picsky/internal/logx"
	"github.com/yeqingky/Picsky/internal/service"
	"github.com/yeqingky/Picsky/internal/spool"
	"github.com/yeqingky/Picsky/internal/storage"
	"github.com/yeqingky/Picsky/internal/web"
	"github.com/yeqingky/Picsky/migrations"

	// 注册存储驱动.
	_ "github.com/yeqingky/Picsky/internal/storage/celine"
	_ "github.com/yeqingky/Picsky/internal/storage/cloudreve"
	_ "github.com/yeqingky/Picsky/internal/storage/metaso"
	_ "github.com/yeqingky/Picsky/internal/storage/sobot"
	_ "github.com/yeqingky/Picsky/internal/storage/webdav"
)

func main() {
	if err := run(); err != nil {
		slog.Error("服务启动失败", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := logx.Setup(cfg.LogLevel)

	if err := ensureTempDir(cfg.TempDir); err != nil {
		return err
	}
	if removed, err := spool.CleanupStale(cfg.TempDir); err != nil {
		log.Warn("清理残留临时文件失败", "error", err)
	} else if removed > 0 {
		log.Info("已清理残留临时文件", "count", removed)
	}

	if hint := cfg.DBHint(); hint != "" {
		log.Warn("数据库连接串可能不可达", "target", cfg.DBTarget(), "hint", hint)
	}
	log.Info("连接数据库", "driver", cfg.DBDriver, "target", cfg.DBTarget())
	database, err := db.Open(cfg)
	if err != nil {
		return err
	}
	defer db.Close(database)

	if err := migrations.Run(database); err != nil {
		return err
	}

	services, err := service.New(cfg, database, log)
	if err != nil {
		return err
	}

	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	if err := router.SetTrustedProxies(nil); err != nil {
		return err
	}
	// 访问日志不记录查询串, 避免 Lsky 客户端把 token 放在查询参数时泄漏密钥.
	router.Use(gin.LoggerWithConfig(gin.LoggerConfig{SkipQueryString: true}))
	api.New(services, cfg, log).Register(router)

	types := make([]string, 0, 4)
	for _, spec := range storage.Specs() {
		types = append(types, spec.Type)
	}
	log.Info("Picsky 已启动",
		"version", config.Version,
		"listen", cfg.Listen,
		"db_driver", cfg.DBDriver,
		"api_prefix", cfg.APIPrefix,
		"lsky_compat", cfg.LskyCompatEnabled,
		"lsky_prefix", cfg.LskyCompatPrefix,
		"storage_drivers", strings.Join(types, ","),
		"web_assets", web.Available(),
	)
	if !web.Available() {
		log.Warn("未检测到前端产物 (web/build), WebUI 将不可用; 请先执行 cd web && npm install && npm run build")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	services.Mailer.Start(ctx)
	go services.Tasks.Start(ctx)

	server := &http.Server{
		Addr:              cfg.Listen,
		Handler:           router,
		ReadHeaderTimeout: 20 * time.Second,
		// 覆盖整个请求体读取 (含大文件上传) 与响应写出, 防止慢速请求长期占用连接.
		ReadTimeout:  15 * time.Minute,
		WriteTimeout: 15 * time.Minute,
		IdleTimeout:  2 * time.Minute,
	}

	errCh := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		return err
	case sig := <-stop:
		log.Info("收到退出信号, 正在关闭", "signal", sig.String())
	}

	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return err
	}
	log.Info("已停止")
	return nil
}

// ensureTempDir 确保临时目录存在.
func ensureTempDir(dir string) error {
	if strings.TrimSpace(dir) == "" {
		return nil
	}
	return os.MkdirAll(dir, 0o750)
}
