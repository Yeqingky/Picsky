package service_test

import (
	"strings"
	"testing"

	"github.com/yeqingky/Picsky/internal/service"

	_ "github.com/yeqingky/Picsky/internal/storage/acgfaka"
)

func TestAcgFakaAutoLoginValidation(t *testing.T) {
	f := newFixture(t)
	config := map[string]string{
		"endpoint":   "https://acgfaka.example.com",
		"auto_login": "true",
	}
	secrets := map[string]string{
		"cookie":   "USER_SESSION=session",
		"username": "user@example.com",
		"password": "secret",
	}

	if _, err := f.svc.Policies.Create(service.PolicyInput{
		Name:    "acgfaka 非法自动登录",
		Type:    "acgfaka",
		Config:  map[string]string{"endpoint": "https://acgfaka.example.com", "auto_login": "yes"},
		Secrets: secrets,
	}, f.admin, "127.0.0.1", "test-agent"); err == nil || !strings.Contains(err.Error(), "自动登录") {
		t.Fatalf("错误 = %v, 期望拒绝非法自动登录配置", err)
	}

	if _, err := f.svc.Policies.Create(service.PolicyInput{
		Name:   "acgfaka 缺少密码",
		Type:   "acgfaka",
		Config: config,
		Secrets: map[string]string{
			"cookie":   "USER_SESSION=session",
			"username": "user@example.com",
		},
	}, f.admin, "127.0.0.1", "test-agent"); err == nil || !strings.Contains(err.Error(), "密码") {
		t.Fatalf("错误 = %v, 期望自动登录缺少密码时拒绝", err)
	}

	policy, err := f.svc.Policies.Create(service.PolicyInput{
		Name:    "acgfaka 自动登录",
		Type:    "acgfaka",
		Config:  config,
		Secrets: secrets,
	}, f.admin, "127.0.0.1", "test-agent")
	if err != nil {
		t.Fatalf("创建策略失败: %v", err)
	}
	if !policy.Enabled {
		t.Error("acgfaka 策略默认应启用")
	}
	view, err := f.svc.Policies.View(policy)
	if err != nil {
		t.Fatalf("读取策略视图失败: %v", err)
	}
	if !view.URLFromBackend {
		t.Error("acgfaka 应由后端分配直链")
	}
	if len(view.Capabilities) != 1 || view.Capabilities[0] != "upload" {
		t.Errorf("能力 = %v, 期望只有 upload", view.Capabilities)
	}
	if len(view.Formats) != 3 {
		t.Errorf("格式 = %v, 期望 png/jpeg/webp", view.Formats)
	}

	// 更新时未重新提交凭据, 应沿用已有账号密码.
	if _, err := f.svc.Policies.Update(policy.ID, service.PolicyInput{
		Config:  config,
		Secrets: map[string]string{"cookie": "USER_SESSION=updated"},
	}, f.admin, "127.0.0.1", "test-agent"); err != nil {
		t.Fatalf("沿用已有账号密码更新失败: %v", err)
	}

	if _, err := f.svc.Policies.Update(policy.ID, service.PolicyInput{
		Config: config,
		Secrets: map[string]string{
			"cookie":   "USER_SESSION=updated",
			"username": "",
			"password": "",
		},
	}, f.admin, "127.0.0.1", "test-agent"); err == nil || !strings.Contains(err.Error(), "账号") {
		t.Fatalf("错误 = %v, 期望清空自动登录账号时拒绝", err)
	}
}
