package api_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/yeqingky/Picsky/internal/model"
)

func TestAdminPasswordResetRevokesExistingSessions(t *testing.T) {
	s := newServer(t)
	adminCookie, _ := s.bootstrap()
	var group model.Group
	if err := s.db.Where("is_default = ?", true).First(&group).Error; err != nil {
		t.Fatalf("读取默认用户组失败: %v", err)
	}

	created := s.json(http.MethodPost, "/api/v1/admin/users", map[string]any{
		"username": "resetuser",
		"email":    "reset-user@example.com",
		"password": "password123",
		"group_id": group.ID,
	}, []*http.Cookie{adminCookie})
	if created.Status != http.StatusCreated {
		t.Fatalf("创建用户失败: %d %s", created.Status, string(created.Body))
	}
	userID := int64(s.decode(created)["id"].(float64))

	login := s.json(http.MethodPost, "/api/v1/auth/login", map[string]any{
		"identifier": "reset-user@example.com",
		"password":   "password123",
	}, nil)
	if login.Status != http.StatusOK {
		t.Fatalf("用户登录失败: %d %s", login.Status, string(login.Body))
	}
	oldCookie := s.sessionCookie(login)

	updated := s.json(http.MethodPatch, fmt.Sprintf("/api/v1/admin/users/%d", userID), map[string]any{
		"password": "newpassword123",
	}, []*http.Cookie{adminCookie})
	if updated.Status != http.StatusOK {
		t.Fatalf("管理员重置密码失败: %d %s", updated.Status, string(updated.Body))
	}

	oldSession := s.raw(http.MethodGet, "/api/v1/auth/me", nil, nil, []*http.Cookie{oldCookie})
	if oldSession.Status != http.StatusUnauthorized {
		t.Errorf("重置密码后旧会话应失效, 实际 %d %s", oldSession.Status, string(oldSession.Body))
	}

	newLogin := s.json(http.MethodPost, "/api/v1/auth/login", map[string]any{
		"identifier": "reset-user@example.com",
		"password":   "newpassword123",
	}, nil)
	if newLogin.Status != http.StatusOK {
		t.Errorf("新密码应可登录: %d %s", newLogin.Status, string(newLogin.Body))
	}
}
