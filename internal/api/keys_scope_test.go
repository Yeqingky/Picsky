package api_test

import (
	"net/http"
	"testing"
)

func TestAPIKeyCannotCreateUnscopedKey(t *testing.T) {
	s := newServer(t)
	adminCookie, _ := s.bootstrap()

	parent := s.json(http.MethodPost, "/api/v1/keys", map[string]any{
		"name":   "parent",
		"scopes": []string{"manage_keys"},
	}, []*http.Cookie{adminCookie})
	if parent.Status != http.StatusCreated {
		t.Fatalf("创建父密钥失败: %d %s", parent.Status, string(parent.Body))
	}
	parentSecret := s.decode(parent)["secret"].(string)

	child := s.bearer(http.MethodPost, "/api/v1/keys", map[string]any{"name": "child"}, parentSecret)
	if child.Status != http.StatusCreated {
		t.Fatalf("使用父密钥创建子密钥失败: %d %s", child.Status, string(child.Body))
	}
	childPayload := s.decode(child)
	childScopes, ok := childPayload["scopes"].([]any)
	if !ok || len(childScopes) != 1 || childScopes[0] != "manage_keys" {
		t.Fatalf("未指定 scopes 的子密钥应继承父密钥权限, 实际 %v", childPayload["scopes"])
	}
	childSecret := childPayload["secret"].(string)

	// 子密钥不能借省略 scopes 恢复为未裁剪的全权限.
	denied := s.bearer(http.MethodGet, "/api/v1/admin/users", nil, childSecret)
	if denied.Status != http.StatusForbidden {
		t.Errorf("子密钥不应获得用户管理权限, 实际 %d %s", denied.Status, string(denied.Body))
	}

	overScoped := s.bearer(http.MethodPost, "/api/v1/keys", map[string]any{
		"name":   "wide",
		"scopes": []string{"upload"},
	}, parentSecret)
	if overScoped.Status != http.StatusBadRequest {
		t.Errorf("父密钥不应签发出超出自身权限的密钥, 实际 %d %s", overScoped.Status, string(overScoped.Body))
	}
}
