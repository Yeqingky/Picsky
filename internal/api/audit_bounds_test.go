package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/yeqingky/Picsky/internal/model"
	"github.com/yeqingky/Picsky/internal/service"
)

func TestOversizedLoginEmailIsRejectedWithoutAudit(t *testing.T) {
	s := newServer(t)
	email := strings.Repeat("a", 300) + "@example.com"
	result := s.json(http.MethodPost, "/api/v1/auth/login", map[string]any{
		"identifier": email,
		"password":   "password123",
	}, nil)
	if result.Status != http.StatusUnauthorized {
		t.Fatalf("超长邮箱应返回统一 401, 实际 %d %s", result.Status, string(result.Body))
	}
	if s.decode(result)["message"] != "邮箱或密码错误" {
		t.Errorf("超长邮箱应返回通用凭据错误: %s", string(result.Body))
	}

	var count int64
	if err := s.db.Model(&model.AuditLog{}).Where("action = ?", "login_failed").Count(&count).Error; err != nil {
		t.Fatalf("统计审计日志失败: %v", err)
	}
	if count != 0 {
		t.Errorf("超长邮箱不应写入登录失败审计, 实际 %d 条", count)
	}
}

func TestUploadAuditTruncatesFilename(t *testing.T) {
	s := newServer(t)
	cookie, _ := s.bootstrap()
	filename := strings.Repeat("a", 300) + ".png"

	uploaded := s.upload("/api/v1/upload", filename, pngContent(t, 41), map[string]string{}, map[string]string{}, []*http.Cookie{cookie})
	if uploaded.Status != http.StatusCreated {
		t.Fatalf("上传失败: %d %s", uploaded.Status, string(uploaded.Body))
	}

	var row model.AuditLog
	if err := s.db.Where("action = ?", "upload").Order("id DESC").First(&row).Error; err != nil {
		t.Fatalf("读取上传审计失败: %v", err)
	}
	var detail map[string]any
	if err := json.Unmarshal([]byte(row.Detail), &detail); err != nil {
		t.Fatalf("解析审计详情失败: %v", err)
	}
	name, _ := detail["original_filename"].(string)
	if len(name) != 255 {
		t.Errorf("审计文件名应截断到 255 字节, 实际 %d", len(name))
	}
}

func TestAuditDetailIsTruncated(t *testing.T) {
	s := newServer(t)
	s.svc.Audit.Recordf(service.Entry{
		Action: "audit_bounds_test",
		Detail: map[string]any{"padding": strings.Repeat("x", 32<<10)},
	})

	var row model.AuditLog
	if err := s.db.Where("action = ?", "audit_bounds_test").Order("id DESC").First(&row).Error; err != nil {
		t.Fatalf("读取审计记录失败: %v", err)
	}
	if len(row.Detail) > 16<<10 {
		t.Errorf("审计详情超过 16 KiB: %d 字节", len(row.Detail))
	}
	var detail map[string]any
	if err := json.Unmarshal([]byte(row.Detail), &detail); err != nil {
		t.Fatalf("截断后的审计详情必须是合法 JSON: %v (%s)", err, row.Detail)
	}
	if detail["truncated"] != true {
		t.Errorf("审计详情缺少截断标记: %s", row.Detail)
	}
}
