package service_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yeqingky/Picsky/internal/service"

	_ "github.com/yeqingky/Picsky/internal/storage/oneimg"
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
