package service_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

func TestCloudreveScanRequiresLogin(t *testing.T) {
	f := newFixture(t)
	policy, err := f.svc.Policies.Create(service.PolicyInput{
		Name:   "Cloudreve 未登录扫描",
		Type:   "cloudreve",
		Config: map[string]string{"endpoint": "https://cloudreve.example.com"},
	}, f.admin, "127.0.0.1", "test-agent")
	if err != nil {
		t.Fatalf("创建未登录策略失败: %v", err)
	}
	if _, err := f.svc.Policies.EnqueueScan(context.Background(), policy.ID, f.admin.User.ID, nil, false); err == nil ||
		!strings.Contains(err.Error(), "登录") {
		t.Fatalf("未登录策略扫描错误 = %v, 期望拒绝", err)
	}
	var tasks int64
	if err := f.database.Model(&model.MaintenanceTask{}).
		Where("type = ?", model.TaskPolicyScan).
		Count(&tasks).Error; err != nil {
		t.Fatalf("统计扫描任务失败: %v", err)
	}
	if tasks != 0 {
		t.Fatalf("未登录策略不应创建扫描任务, 实际 %d", tasks)
	}
}

func TestCloudreveScanBackfillsDirectLink(t *testing.T) {
	image := pngBytes(t, 940, 4, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v4/session/token/refresh":
			writeCloudreveEnvelope(t, w, 0, map[string]any{
				"access_token":  "access-token",
				"refresh_token": "refresh-token",
			})
		case "/api/v4/file":
			if r.URL.Query().Get("uri") != "cloudreve://my/picsky" {
				t.Errorf("扫描目录 URI = %q", r.URL.Query().Get("uri"))
			}
			writeCloudreveEnvelope(t, w, 0, map[string]any{
				"files": []map[string]any{
					{
						"type":       0,
						"name":       "foo.png",
						"size":       len(image),
						"updated_at": time.Now().Format(time.RFC3339),
						"path":       "cloudreve://my/picsky/foo.png",
					},
				},
				"pagination": map[string]any{"is_cursor": true},
			})
		case "/api/v4/file/info":
			writeCloudreveEnvelope(t, w, 0, map[string]any{
				"type":       0,
				"name":       "foo.png",
				"size":       len(image),
				"updated_at": time.Now().Format(time.RFC3339),
				"path":       "cloudreve://my/picsky/foo.png",
				"extended_info": map[string]any{
					"direct_links": []map[string]string{
						{"url": serverURL(r) + "/f/foo.png"},
					},
				},
			})
		case "/f/foo.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(image)
		default:
			t.Errorf("未预期的请求: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	f := newFixture(t)
	enabled := false
	policy, err := f.svc.Policies.Create(service.PolicyInput{
		Name:   "Cloudreve 扫描",
		Type:   "cloudreve",
		Config: map[string]string{"endpoint": server.URL},
		Secrets: map[string]string{
			"refresh_token": "refresh-token",
		},
		Enabled: &enabled,
	}, f.admin, "127.0.0.1", "test-agent")
	if err != nil {
		t.Fatalf("创建扫描策略失败: %v", err)
	}
	legacy := model.Image{
		UserID:           f.admin.User.ID,
		PolicyID:         policy.ID,
		RelPath:          "legacy.png",
		ObjectKey:        "legacy.png",
		MD5:              "00000000000000000000000000000000",
		SHA1:             "0000000000000000000000000000000000000000",
		SHA256:           "0000000000000000000000000000000000000000000000000000000000000000",
		SHA512:           strings.Repeat("0", 128),
		Size:             123,
		Kind:             model.KindImage,
		Format:           "png",
		Ext:              "png",
		MIME:             "image/png",
		OriginalFilename: "legacy.png",
		URL:              "https://legacy.example/legacy.png",
		QuotaCharged:     true,
		Source:           model.SourceScan,
		CreatedAt:        time.Now().UTC(),
	}
	if err := f.database.Create(&legacy).Error; err != nil {
		t.Fatalf("创建目录外记录失败: %v", err)
	}
	if err := f.database.Model(&model.User{}).Where("id = ?", f.admin.User.ID).
		Updates(map[string]any{"used_images": 1, "used_bytes": 123}).Error; err != nil {
		t.Fatalf("设置用户用量失败: %v", err)
	}
	result, err := f.svc.Policies.Scan(context.Background(), policy.ID, f.admin.User.ID, nil, false)
	if err != nil {
		t.Fatalf("扫描失败: %v", err)
	}
	if result.Images != 1 || result.Removed != 1 || len(result.Failed) != 0 {
		t.Fatalf("扫描结果 = %+v", result)
	}
	var record model.Image
	if err := f.database.Where("policy_id = ? AND object_key = ?", policy.ID, "picsky/foo.png").First(&record).Error; err != nil {
		t.Fatalf("读取扫描记录失败: %v", err)
	}
	if record.URL != server.URL+"/f/foo.png" {
		t.Errorf("扫描记录 URL = %q", record.URL)
	}
	if record.RelPath != "foo.png" || record.ObjectKey != "picsky/foo.png" || record.Kind != model.KindImage {
		t.Errorf("扫描记录 = %+v", record)
	}
	var removed int64
	if err := f.database.Model(&model.Image{}).Where("id = ?", legacy.ID).Count(&removed).Error; err != nil {
		t.Fatalf("统计目录外记录失败: %v", err)
	}
	if removed != 0 {
		t.Error("目录外记录应被清理")
	}
	var user model.User
	if err := f.database.First(&user, f.admin.User.ID).Error; err != nil {
		t.Fatalf("读取用户失败: %v", err)
	}
	if user.UsedImages != 0 || user.UsedBytes != 0 {
		t.Errorf("目录外记录配额未回补: images=%d bytes=%d", user.UsedImages, user.UsedBytes)
	}
}

func writeCloudreveEnvelope(t *testing.T, w http.ResponseWriter, code int, data any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"code": code, "data": data}); err != nil {
		t.Errorf("写入 Cloudreve 响应失败: %v", err)
	}
}

func serverURL(r *http.Request) string {
	return "http://" + r.Host
}
