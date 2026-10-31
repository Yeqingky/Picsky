package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/yeqingky/Picsky/internal/model"
	"github.com/yeqingky/Picsky/internal/service"

	_ "github.com/yeqingky/Picsky/internal/storage/cloudreve"
)

func TestPolicyRejectsInvalidCloudreveAutoLogin(t *testing.T) {
	f := newFixture(t)
	_, err := f.svc.Policies.Create(service.PolicyInput{
		Name: "Cloudreve 非法自动登录",
		Type: "cloudreve",
		Config: map[string]string{
			"endpoint":   "https://cloudreve.example.com",
			"auto_login": "yes",
		},
	}, f.admin, "127.0.0.1", "test-agent")
	if err == nil || !strings.Contains(err.Error(), "自动登录") {
		t.Fatalf("错误 = %v, 期望拒绝非法自动登录配置", err)
	}
}

func TestDefaultPathTemplateRequiresExtSuffix(t *testing.T) {
	f := newFixture(t)
	if err := f.svc.Settings.Update(map[string]string{"default_path_template": "{md5}.jpg"}); err == nil ||
		!strings.Contains(err.Error(), ".{ext}") {
		t.Fatalf("缺少 .{ext} 的默认路径应被拒绝, 实际 %v", err)
	}
	if err := f.svc.Settings.Update(map[string]string{"default_path_template": "picsky/{md5}.{ext}"}); err != nil {
		t.Fatalf("合法默认路径应保存成功: %v", err)
	}
}

func TestCloudrevePolicyRequiresLoginToEnable(t *testing.T) {
	f := newFixture(t)
	config := map[string]string{
		"endpoint": "https://cloudreve.example.com",
	}
	policy, err := f.svc.Policies.Create(service.PolicyInput{
		Name:   "Cloudreve 未登录",
		Type:   "cloudreve",
		Config: config,
	}, f.admin, "127.0.0.1", "test-agent")
	if err != nil {
		t.Fatalf("创建未登录策略失败: %v", err)
	}
	if policy.Enabled {
		t.Error("未登录的 Cloudreve 策略应默认禁用")
	}
	if policy.PathTemplate != "{md5:2}/{md5}.{ext}" {
		t.Errorf("Cloudreve 默认魔法路径 = %q", policy.PathTemplate)
	}
	view, err := f.svc.Policies.View(policy)
	if err != nil {
		t.Fatalf("读取策略视图失败: %v", err)
	}
	if view.LoginReady {
		t.Error("未登录策略不应标记为可登录")
	}

	enabled := true
	if _, err := f.svc.Policies.Create(service.PolicyInput{
		Name:    "Cloudreve 强制启用",
		Type:    "cloudreve",
		Config:  config,
		Enabled: &enabled,
	}, f.admin, "127.0.0.1", "test-agent"); err == nil || !strings.Contains(err.Error(), "登录") {
		t.Fatalf("未登录策略启用错误 = %v", err)
	}

	policy, err = f.svc.Policies.Create(service.PolicyInput{
		Name:   "Cloudreve 已登录",
		Type:   "cloudreve",
		Config: config,
		Secrets: map[string]string{
			"refresh_token": "refresh-token",
		},
	}, f.admin, "127.0.0.1", "test-agent")
	if err != nil {
		t.Fatalf("创建已登录策略失败: %v", err)
	}
	if !policy.Enabled {
		t.Error("已登录的 Cloudreve 策略应默认启用")
	}
	view, err = f.svc.Policies.View(policy)
	if err != nil {
		t.Fatalf("读取已登录策略视图失败: %v", err)
	}
	if !view.LoginReady {
		t.Error("已登录策略应标记为可登录")
	}

	disabled := false
	updated, err := f.svc.Policies.Update(policy.ID, service.PolicyInput{
		Secrets: map[string]string{"refresh_token": ""},
		Enabled: &disabled,
	}, f.admin, "127.0.0.1", "test-agent")
	if err != nil {
		t.Fatalf("清除登录令牌失败: %v", err)
	}
	if updated.Enabled {
		t.Error("清除登录令牌后策略应禁用")
	}
	view, err = f.svc.Policies.View(updated)
	if err != nil {
		t.Fatalf("读取清除令牌后的策略视图失败: %v", err)
	}
	if view.LoginReady {
		t.Error("清除登录令牌后不应标记为可登录")
	}
}

func TestCloudreveDoesNotSupportScan(t *testing.T) {
	f := newFixture(t)
	policy, err := f.svc.Policies.Create(service.PolicyInput{
		Name:   "Cloudreve 不支持扫描",
		Type:   "cloudreve",
		Config: map[string]string{"endpoint": "https://cloudreve.example.com"},
	}, f.admin, "127.0.0.1", "test-agent")
	if err != nil {
		t.Fatalf("创建策略失败: %v", err)
	}
	if _, err := f.svc.Policies.EnqueueScan(context.Background(), policy.ID, f.admin.User.ID, nil, false); err == nil ||
		!strings.Contains(err.Error(), "不支持扫描补录") {
		t.Fatalf("扫描错误 = %v, 期望拒绝后端下发直链的策略", err)
	}
	var tasks int64
	if err := f.database.Model(&model.MaintenanceTask{}).
		Where("type = ?", model.TaskPolicyScan).
		Count(&tasks).Error; err != nil {
		t.Fatalf("统计扫描任务失败: %v", err)
	}
	if tasks != 0 {
		t.Fatalf("不应创建扫描任务, 实际 %d", tasks)
	}
}
