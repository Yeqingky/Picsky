// Package config 负责从环境变量加载运行时配置.
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// Version 为构建版本, 可通过 -ldflags "-X ...config.Version=x.y.z" 覆盖.
var Version = "0.1.0"

// Config 是进程级配置, 全部来自环境变量; 站点级设置存数据库.
type Config struct {
	SecretKey         []byte
	DBDriver          string
	DBDSN             string
	Listen            string
	APIPrefix         string
	LskyCompatEnabled bool
	LskyCompatPrefix  string
	TempDir           string
	UploadMemoryLimit int64
	LogLevel          string
}

// Load 读取环境变量并校验.
func Load() (*Config, error) {
	cfg := &Config{
		DBDriver:          env("PICSKY_DB_DRIVER", "sqlite"),
		DBDSN:             env("PICSKY_DB_DSN", "data/picsky.db"),
		Listen:            env("PICSKY_LISTEN", "127.0.0.1:5214"),
		APIPrefix:         env("PICSKY_API_PREFIX", "/api/v1"),
		LskyCompatEnabled: envBool("PICSKY_LSKY_COMPAT_ENABLED", true),
		LskyCompatPrefix:  env("PICSKY_LSKY_COMPAT_PREFIX", "/lsky"),
		TempDir:           env("PICSKY_TEMP_DIR", ""),
		LogLevel:          env("PICSKY_LOG_LEVEL", "info"),
	}

	memLimit, err := envInt64("PICSKY_UPLOAD_MEMORY_LIMIT", 8*1024*1024)
	if err != nil {
		return nil, err
	}
	cfg.UploadMemoryLimit = memLimit

	raw := strings.TrimSpace(os.Getenv("PICSKY_SECRET_KEY"))
	if raw == "" {
		return nil, errors.New("缺少 PICSKY_SECRET_KEY: 需要 32 字节 base64 主密钥 (openssl rand -base64 32)")
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("PICSKY_SECRET_KEY 不是合法 base64: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("PICSKY_SECRET_KEY 解码后长度必须为 32 字节, 当前 %d", len(key))
	}
	cfg.SecretKey = key

	switch cfg.DBDriver {
	case "sqlite", "postgres":
	default:
		return nil, fmt.Errorf("不支持的 PICSKY_DB_DRIVER: %s (仅 sqlite / postgres)", cfg.DBDriver)
	}

	cfg.APIPrefix, err = normalizePrefix(cfg.APIPrefix, true)
	if err != nil {
		return nil, fmt.Errorf("PICSKY_API_PREFIX 非法: %w", err)
	}
	cfg.LskyCompatPrefix, err = normalizePrefix(cfg.LskyCompatPrefix, false)
	if err != nil {
		return nil, fmt.Errorf("PICSKY_LSKY_COMPAT_PREFIX 非法: %w", err)
	}
	if cfg.LskyCompatEnabled && cfg.LskyCompatPrefix == "" && cfg.APIPrefix == "/api/v1" {
		return nil, errors.New("兼容层根模式会让 /api/v1 与 Lsky 路由冲突: 请把 PICSKY_API_PREFIX 改为 /api/v2, 或设置 PICSKY_LSKY_COMPAT_PREFIX")
	}
	if cfg.LskyCompatEnabled && cfg.LskyCompatPrefix != "" && strings.HasPrefix(cfg.LskyCompatPrefix, "/api/") {
		return nil, errors.New("PICSKY_LSKY_COMPAT_PREFIX 不能位于 /api/ 下, 否则与原生 API 冲突")
	}
	if cfg.LskyCompatEnabled && cfg.LskyCompatPrefix != "" && strings.HasSuffix(cfg.APIPrefix, cfg.LskyCompatPrefix) {
		return nil, errors.New("PICSKY_API_PREFIX 不能以兼容层前缀结尾")
	}

	switch cfg.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return nil, fmt.Errorf("不支持的 PICSKY_LOG_LEVEL: %s", cfg.LogLevel)
	}

	return cfg, nil
}

// DBTarget 返回不含密码的数据库目标描述, 用于启动日志.
func (c *Config) DBTarget() string {
	if c.DBDriver == "sqlite" {
		if strings.TrimSpace(c.DBDSN) == "" {
			return "data/picsky.db"
		}
		return c.DBDSN
	}
	host, port, dbName := parsePostgresDSN(c.DBDSN)
	if host == "" && port == "" && dbName == "" {
		return "<连接串未配置>"
	}
	target := host
	if port != "" {
		target += ":" + port
	}
	if dbName != "" {
		target += "/" + dbName
	}
	return target
}

// DBHint 在连接串指向本机回环地址时给出容器编排提示 (回环地址在容器内指向容器自身).
func (c *Config) DBHint() string {
	if c.DBDriver != "postgres" {
		return ""
	}
	host, _, _ := parsePostgresDSN(c.DBDSN)
	switch host {
	case "127.0.0.1", "localhost", "::1":
		return "连接串主机是回环地址: docker compose 部署时数据库主机名应为服务名 (例如 host=postgres), 或把 PICSKY_DB_DSN 留空由编排拼装"
	}
	return ""
}

// parsePostgresDSN 解析 keyword=value 或 URL 形式的 PostgreSQL 连接串 (只为日志与提示提取 host/port/dbname).
func parsePostgresDSN(dsn string) (host, port, dbname string) {
	trimmed := strings.TrimSpace(dsn)
	if trimmed == "" {
		return "", "", ""
	}
	if strings.HasPrefix(trimmed, "postgres://") || strings.HasPrefix(trimmed, "postgresql://") {
		parsed, err := url.Parse(trimmed)
		if err != nil {
			return "", "", ""
		}
		return parsed.Hostname(), parsed.Port(), strings.TrimPrefix(parsed.Path, "/")
	}
	for _, field := range strings.Fields(trimmed) {
		key, value, found := strings.Cut(field, "=")
		if !found {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), "'")
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "host":
			host = value
		case "port":
			port = value
		case "dbname", "database":
			dbname = value
		}
	}
	return host, port, dbname
}

// normalizePrefix 规范化路径前缀: 去除尾部斜杠; allowEmpty 决定空串是否合法.
func normalizePrefix(v string, allowEmpty bool) (string, error) {
	v = strings.TrimSpace(v)
	v = strings.TrimSuffix(v, "/")
	if v == "" {
		if allowEmpty {
			return "", errors.New("不能为空")
		}
		return "", nil
	}
	if !strings.HasPrefix(v, "/") {
		return "", errors.New("必须以 / 开头")
	}
	if strings.Contains(v, "//") {
		return "", errors.New("不能包含连续斜杠")
	}
	if strings.ContainsAny(v, " \t?#") {
		return "", errors.New("不能包含空白或 ? # 字符")
	}
	return v, nil
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

func envBool(key string, def bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return def
	}
	parsed, err := strconv.ParseBool(strings.TrimSpace(v))
	if err != nil {
		return def
	}
	return parsed
}

func envInt64(key string, def int64) (int64, error) {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return def, nil
	}
	parsed, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s 必须是整数: %w", key, err)
	}
	if parsed < 0 {
		return 0, fmt.Errorf("%s 不能为负数", key)
	}
	return parsed, nil
}
