package service_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yeqingky/Picsky/internal/service"

	_ "github.com/yeqingky/Picsky/internal/storage/oneimg"
	_ "github.com/yeqingky/Picsky/internal/storage/sobot"
)

func TestOneImgPolicyRequiresToken(t *testing.T) {
	f := newFixture(t)
	_, err := f.svc.Policies.Create(service.PolicyInput{
		Name:   "初春图床缺少令牌",
		Type:   "oneimg",
		Config: map[string]string{"endpoint": "https://img.example.com"},
	}, f.admin, "127.0.0.1", "test-agent")
	if err == nil || !strings.Contains(err.Error(), "API Token") {
		t.Fatalf("创建错误 = %v, 期望缺少 API Token", err)
	}
}

func TestOneImgConnectionTest(t *testing.T) {
	var called bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.URL.Path != "/api/upload/images" {
			t.Errorf("请求路径 = %s", r.URL.Path)
		}
		if token := r.Header.Get("Authorization"); token != "Bearer test-token" {
			t.Errorf("Authorization = %q", token)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("解析 multipart 失败: %v", err)
			return
		}
		if _, _, err := r.FormFile("images[]"); err != nil {
			t.Errorf("缺少 images[] 字段: %v", err)
			return
		}
		_, _ = w.Write([]byte(`{"code":200,"data":{"files":[{"success":true,"url":"/uploads/probe.webp"}]}}`))
	}))
	defer server.Close()

	f := newFixture(t)
	policy, err := f.svc.Policies.Create(service.PolicyInput{
		Name:   "初春图床",
		Type:   "oneimg",
		Config: map[string]string{"endpoint": server.URL},
		Secrets: map[string]string{
			"token": "test-token",
		},
	}, f.admin, "127.0.0.1", "test-agent")
	if err != nil {
		t.Fatalf("创建策略失败: %v", err)
	}
	if _, err := f.svc.Policies.Test(context.Background(), policy.ID); err != nil {
		t.Fatalf("连接测试失败: %v", err)
	}
	if !called {
		t.Error("未收到连接测试请求")
	}
}

func TestOneImgDistributionDomainOverridesBackendURL(t *testing.T) {
	var uploads int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uploads++
		if r.URL.Path != "/api/upload/images" {
			t.Errorf("请求路径 = %s", r.URL.Path)
		}
		if token := r.Header.Get("Authorization"); token != "Bearer test-token" {
			t.Errorf("Authorization = %q", token)
		}
		_, _ = w.Write([]byte(`{"code":200,"data":{"files":[{"success":true,"url":"/uploads/2026/10/30/abc.webp"}]}}`))
	}))
	defer server.Close()

	f := newFixture(t)
	policy, err := f.svc.Policies.Create(service.PolicyInput{
		Name:    "初春图床分发域名",
		Type:    "oneimg",
		Enabled: new(true),
		Config:  map[string]string{"endpoint": server.URL},
		Secrets: map[string]string{"token": "test-token"},
		URLs:    []service.URLInput{{Alias: "CDN", URL: "https://cdn.example.com", IsDefault: true}},
	}, f.admin, "127.0.0.1", "test-agent")
	if err != nil {
		t.Fatalf("创建策略失败: %v", err)
	}
	view, err := f.svc.Policies.View(policy)
	if err != nil {
		t.Fatalf("读取策略视图失败: %v", err)
	}
	if !view.URLPrefixOverride {
		t.Error("策略视图应声明分发域名可覆盖后端直链前缀")
	}
	if len(view.URLs) != 1 {
		t.Fatalf("分发域名数量 = %d, 期望 1", len(view.URLs))
	}

	user := f.register("oneimguser")
	f.authorize(policy.ID)
	result := f.mustUpload(user, &policy.ID, "pic.png", pngBytes(t, 3, 4, 4))
	if uploads != 1 {
		t.Fatalf("上传请求次数 = %d, 期望 1", uploads)
	}
	wantURL := "https://cdn.example.com/uploads/2026/10/30/abc.webp"
	if result.Record.URL != wantURL {
		t.Errorf("记录直链 = %q, 期望 %q", result.Record.URL, wantURL)
	}
	if result.Record.RelPath != "uploads/2026/10/30/abc.webp" || result.Record.ObjectKey != "uploads/2026/10/30/abc.webp" {
		t.Errorf("记录路径 = %q / %q, 期望后端返回路径", result.Record.RelPath, result.Record.ObjectKey)
	}

	if _, err := f.svc.Policies.Create(service.PolicyInput{
		Name:   "Sobot 分发域名",
		Type:   "sobot",
		Config: map[string]string{},
		URLs:   []service.URLInput{{Alias: "CDN", URL: "https://cdn.example.com", IsDefault: true}},
	}, f.admin, "127.0.0.1", "test-agent"); err == nil || !strings.Contains(err.Error(), "不支持分发域名") {
		t.Fatalf("其他后端直链驱动仍应拒绝分发域名, 错误 = %v", err)
	}
}

func TestOneImgExistingRecordUsesConfiguredDefaultDomain(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"code":200,"data":{"files":[{"success":true,"url":"/uploads/2026/10/pic.webp"}]}}`))
	}))
	defer server.Close()

	f := newFixture(t)
	policy, err := f.svc.Policies.Create(service.PolicyInput{
		Name:    "初春图床补配域名",
		Type:    "oneimg",
		Enabled: new(true),
		Config:  map[string]string{"endpoint": server.URL},
		Secrets: map[string]string{"token": "test-token"},
	}, f.admin, "127.0.0.1", "test-agent")
	if err != nil {
		t.Fatalf("创建策略失败: %v", err)
	}
	user := f.register("oneimgdedup")
	f.authorize(policy.ID)
	first := f.mustUpload(user, &policy.ID, "pic.png", pngBytes(t, 5, 4, 4))
	if first.Record.URL != server.URL+"/uploads/2026/10/pic.webp" {
		t.Fatalf("配置域名前的直链 = %q", first.Record.URL)
	}

	if _, err := f.svc.Policies.Update(policy.ID, service.PolicyInput{
		URLs: []service.URLInput{{Alias: "CDN", URL: "https://cdn.example.com", IsDefault: true}},
	}, f.admin, "127.0.0.1", "test-agent"); err != nil {
		t.Fatalf("补充分发域名失败: %v", err)
	}

	reused := f.mustUpload(user, &policy.ID, "pic.png", pngBytes(t, 5, 4, 4))
	wantURL := "https://cdn.example.com/uploads/2026/10/pic.webp"
	if !reused.Dedup || reused.Record.URL != wantURL {
		t.Errorf("复用记录直链 = %q, dedup=%v, 期望 %q", reused.Record.URL, reused.Dedup, wantURL)
	}
	checked, err := f.svc.Upload.Check(user, first.Record.SHA256, &policy.ID)
	if err != nil {
		t.Fatalf("秒传检查失败: %v", err)
	}
	if checked.Record == nil || checked.Record.URL != wantURL {
		t.Errorf("秒传检查直链 = %+v, 期望 %q", checked.Record, wantURL)
	}
}
