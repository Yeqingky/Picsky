package config

import (
	"strings"
	"testing"
)

func TestDBTargetAndHint(t *testing.T) {
	cases := []struct {
		name     string
		driver   string
		dsn      string
		target   string
		wantHint bool
	}{
		{
			name:     "postgres 关键词连接串",
			driver:   "postgres",
			dsn:      "host=postgres port=5432 user=picsky password=secret dbname=picsky sslmode=disable",
			target:   "postgres:5432/picsky",
			wantHint: false,
		},
		{
			name:     "postgres 回环地址给出提示",
			driver:   "postgres",
			dsn:      "host=127.0.0.1 port=5432 user=picsky password=secret dbname=picsky sslmode=disable",
			target:   "127.0.0.1:5432/picsky",
			wantHint: true,
		},
		{
			name:     "localhost 同样给出提示",
			driver:   "postgres",
			dsn:      "host=localhost dbname=picsky",
			target:   "localhost/picsky",
			wantHint: true,
		},
		{
			name:     "postgres URL 形式",
			driver:   "postgres",
			dsn:      "postgres://picsky:secret@db.internal:5433/picsky?sslmode=disable",
			target:   "db.internal:5433/picsky",
			wantHint: false,
		},
		{
			name:     "未配置连接串",
			driver:   "postgres",
			dsn:      "",
			target:   "<连接串未配置>",
			wantHint: false,
		},
		{
			name:     "sqlite 文件路径",
			driver:   "sqlite",
			dsn:      "/data/picsky.db",
			target:   "/data/picsky.db",
			wantHint: false,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := &Config{DBDriver: testCase.driver, DBDSN: testCase.dsn}
			if got := cfg.DBTarget(); got != testCase.target {
				t.Errorf("DBTarget() = %q, 期望 %q", got, testCase.target)
			}
			hint := cfg.DBHint()
			if testCase.wantHint && hint == "" {
				t.Error("应给出回环地址提示")
			}
			if !testCase.wantHint && hint != "" {
				t.Errorf("不应给出提示, 实际 %q", hint)
			}
			if strings.Contains(hint, "secret") {
				t.Error("提示信息不能包含密码")
			}
		})
	}
}

func TestDBTargetNeverLeaksPassword(t *testing.T) {
	cfg := &Config{
		DBDriver: "postgres",
		DBDSN:    "host=postgres port=5432 user=picsky password=top-secret dbname=picsky sslmode=disable",
	}
	if strings.Contains(cfg.DBTarget(), "top-secret") {
		t.Errorf("DBTarget 泄漏了密码: %q", cfg.DBTarget())
	}
}
