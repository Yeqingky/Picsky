package service

import (
	"errors"
	"testing"

	"github.com/yeqingky/Picsky/internal/apperr"
)

func TestCheckPolicyFormats(t *testing.T) {
	allowed := []string{"png", "jpeg", "gif"}
	if err := checkPolicyFormats(nil, "webp"); err != nil {
		t.Errorf("未声明格式限制时应通过, 实际 %v", err)
	}
	if err := checkPolicyFormats(allowed, "png"); err != nil {
		t.Errorf("允许的格式应通过, 实际 %v", err)
	}
	err := checkPolicyFormats(allowed, "webp")
	var apiErr *apperr.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("期望 API 错误, 实际 %v", err)
	}
	if apiErr.Code != "unsupported_format" {
		t.Errorf("错误码 = %q, 期望 unsupported_format", apiErr.Code)
	}
	if apiErr.Message != "此存储策略不支持 WEBP 格式, 仅支持 PNG, JPEG, GIF" {
		t.Errorf("错误消息 = %q", apiErr.Message)
	}
	if apiErr.Details["scope"] != "policy" {
		t.Errorf("scope = %v, 期望 policy", apiErr.Details["scope"])
	}
}

func TestFileSizeLimits(t *testing.T) {
	const (
		mebibyte = int64(1 << 20)
		global   = 100 * mebibyte
		driver   = 3 * mebibyte
	)
	tests := []struct {
		name        string
		global      int64
		group       int64
		driver      int64
		wantEff     int64
		wantSource  string
		description string
	}{
		{
			name:       "三级都配置时取最小",
			global:     global,
			group:      900 * mebibyte,
			driver:     driver,
			wantEff:    driver,
			wantSource: "driver",
		},
		{
			name:       "组上限比驱动宽松时仍受驱动限制",
			global:     global,
			group:      900 * mebibyte,
			driver:     driver,
			wantEff:    driver,
			wantSource: "driver",
		},
		{
			name:       "组上限更严格时由组触发",
			global:     global,
			group:      1 * mebibyte,
			driver:     driver,
			wantEff:    1 * mebibyte,
			wantSource: "group",
		},
		{
			name:       "组无覆盖时由全局触发",
			global:     global,
			group:      0,
			driver:     0,
			wantEff:    global,
			wantSource: "global",
		},
		{
			name:       "全部不限",
			global:     0,
			group:      0,
			driver:     0,
			wantEff:    0,
			wantSource: "",
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			limits := FileSizeLimits(testCase.global, testCase.group, testCase.driver)
			if limits.Effective != testCase.wantEff || limits.Source != testCase.wantSource {
				t.Fatalf("生效上限 = %d (%q), 期望 %d (%q)",
					limits.Effective, limits.Source, testCase.wantEff, testCase.wantSource)
			}
		})
	}
}

func TestFileSizeLimitError(t *testing.T) {
	const mebibyte = int64(1 << 20)
	tests := []struct {
		name    string
		limits  FileSizeLimit
		size    int64
		wantMsg string
	}{
		{
			name:    "驱动上限触发",
			limits:  FileSizeLimits(100*mebibyte, 0, 3*mebibyte),
			size:    5 * mebibyte,
			wantMsg: "此存储策略最大支持3 MiB",
		},
		{
			name:    "用户组上限触发",
			limits:  FileSizeLimits(100*mebibyte, mebibyte, 0),
			size:    2 * mebibyte,
			wantMsg: "文件超过单文件上限 1 MiB",
		},
		{
			name:    "全局上限触发",
			limits:  FileSizeLimits(100*mebibyte, 0, 0),
			size:    101 * mebibyte,
			wantMsg: "文件超过单文件上限 100 MiB",
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			err := fileSizeLimitError(testCase.limits, testCase.size)
			var apiErr *apperr.Error
			if !errors.As(err, &apiErr) {
				t.Fatalf("期望 API 错误, 实际 %v", err)
			}
			if apiErr.Code != "file_too_large" {
				t.Errorf("错误码 = %q, 期望 file_too_large", apiErr.Code)
			}
			if apiErr.Message != testCase.wantMsg {
				t.Errorf("错误消息 = %q, 期望 %q", apiErr.Message, testCase.wantMsg)
			}
			if apiErr.Details["scope"] != testCase.limits.Source {
				t.Errorf("scope = %v, 期望 %q", apiErr.Details["scope"], testCase.limits.Source)
			}
		})
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{bytes: 0, want: "不限"},
		{bytes: 10 << 20, want: "10 MiB"},
		{bytes: 10<<20 - 194, want: "10485566 B"},
		{bytes: 1024, want: "1 KiB"},
		{bytes: 10250, want: "10250 B"},
		{bytes: 1536 << 10, want: "1.5 MiB"},
	}
	for _, testCase := range tests {
		if got := FormatBytes(testCase.bytes); got != testCase.want {
			t.Errorf("FormatBytes(%d) = %q, 期望 %q", testCase.bytes, got, testCase.want)
		}
	}
}
