// Package db 打开并配置数据库连接.
package db

import (
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/yeqingky/Picsky/internal/config"
)

// 启动连接重试参数: PostgreSQL 可能仍在初始化, 因此有界重试而不是立即退出.
const (
	postgresConnectAttempts = 6
	postgresConnectDelay    = 5 * time.Second
)

// Open 按配置打开数据库; SQLite 启用 WAL 并限制单写连接.
func Open(cfg *config.Config) (*gorm.DB, error) {
	gormConfig := &gorm.Config{
		Logger:                                   newGormLogger(cfg.LogLevel),
		DisableForeignKeyConstraintWhenMigrating: true,
		NowFunc:                                  func() time.Time { return time.Now().UTC() },
	}

	newDialector, attempts, err := buildDialector(cfg)
	if err != nil {
		return nil, err
	}

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		handle, openErr := openOnce(newDialector, gormConfig)
		if openErr == nil {
			if err := configurePool(handle, cfg); err != nil {
				return nil, err
			}
			return handle, nil
		}
		lastErr = openErr
		if attempt < attempts {
			slog.Warn("连接数据库失败, 稍后重试",
				"attempt", attempt, "of", attempts, "delay", postgresConnectDelay.String(), "error", openErr)
			time.Sleep(postgresConnectDelay)
		}
	}

	if cfg.DBDriver == "postgres" {
		if hint := cfg.DBHint(); hint != "" {
			return nil, fmt.Errorf("连接数据库失败: %w; %s", lastErr, hint)
		}
	}
	return nil, fmt.Errorf("连接数据库失败: %w", lastErr)
}

// openOnce 建立连接并做一次 Ping, 避免惰性连接掩盖启动期错误.
func openOnce(newDialector func() gorm.Dialector, gormConfig *gorm.Config) (*gorm.DB, error) {
	handle, err := gorm.Open(newDialector(), gormConfig)
	if err != nil {
		return nil, err
	}
	sqlDB, err := handle.DB()
	if err != nil {
		return nil, err
	}
	if err := sqlDB.Ping(); err != nil {
		sqlDB.Close()
		return nil, err
	}
	return handle, nil
}

// buildDialector 返回驱动工厂与重试次数; SQLite 会先创建数据目录.
func buildDialector(cfg *config.Config) (func() gorm.Dialector, int, error) {
	switch cfg.DBDriver {
	case "sqlite":
		dsn := strings.TrimSpace(cfg.DBDSN)
		if dsn == "" {
			dsn = "data/picsky.db"
		}
		if dir := filepath.Dir(dsn); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o750); err != nil {
				return nil, 0, fmt.Errorf("创建数据库目录失败: %w", err)
			}
		}
		return func() gorm.Dialector { return sqlite.Open(dsn) }, 1, nil
	case "postgres":
		dsn := cfg.DBDSN
		return func() gorm.Dialector { return postgres.Open(dsn) }, postgresConnectAttempts, nil
	default:
		return nil, 0, fmt.Errorf("不支持的数据库驱动: %s", cfg.DBDriver)
	}
}

// configurePool 按驱动配置连接池与 SQLite 参数.
func configurePool(handle *gorm.DB, cfg *config.Config) error {
	sqlDB, err := handle.DB()
	if err != nil {
		return fmt.Errorf("获取数据库句柄失败: %w", err)
	}
	if cfg.DBDriver == "sqlite" {
		// SQLite 单写者模型: 限制为单个连接可避免 SQLITE_BUSY 与写写冲突.
		sqlDB.SetMaxOpenConns(1)
		sqlDB.SetMaxIdleConns(1)
		pragmas := []string{
			"PRAGMA journal_mode = WAL",
			"PRAGMA busy_timeout = 10000",
			"PRAGMA foreign_keys = ON",
			"PRAGMA synchronous = NORMAL",
		}
		for _, pragma := range pragmas {
			if err := handle.Exec(pragma).Error; err != nil {
				return fmt.Errorf("设置 SQLite 参数失败 (%s): %w", pragma, err)
			}
		}
		return nil
	}
	sqlDB.SetMaxOpenConns(20)
	sqlDB.SetMaxIdleConns(5)
	sqlDB.SetConnMaxLifetime(time.Hour)
	return nil
}

// Close 关闭数据库连接.
func Close(handle *gorm.DB) error {
	if handle == nil {
		return nil
	}
	sqlDB, err := handle.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// newGormLogger 构造按日志级别与配置裁剪的 GORM logger.
func newGormLogger(level string) logger.Interface {
	logLevel := logger.Warn
	switch strings.ToLower(level) {
	case "debug":
		logLevel = logger.Info
	case "error":
		logLevel = logger.Error
	case "warn", "info":
		logLevel = logger.Warn
	}
	return logger.New(log.New(os.Stdout, "", log.LstdFlags), logger.Config{
		SlowThreshold:             500 * time.Millisecond,
		LogLevel:                  logLevel,
		IgnoreRecordNotFoundError: true,
		Colorful:                  false,
	})
}
