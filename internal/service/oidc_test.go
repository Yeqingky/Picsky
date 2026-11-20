package service_test

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/yeqingky/Picsky/internal/apperr"
	"github.com/yeqingky/Picsky/internal/model"
	"github.com/yeqingky/Picsky/internal/service"
)

type fakeOIDCProvider struct {
	server    *httptest.Server
	key       *rsa.PrivateKey
	clientID  string
	mu        sync.Mutex
	nonce     string
	challenge string
	claims    map[string]any
}

func newFakeOIDCProvider(t *testing.T, clientID string) *fakeOIDCProvider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("生成测试 RSA 密钥失败: %v", err)
	}
	fake := &fakeOIDCProvider{key: key, clientID: clientID}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{
			"issuer":                                fake.server.URL,
			"authorization_endpoint":                fake.server.URL + "/authorize",
			"token_endpoint":                        fake.server.URL + "/token",
			"userinfo_endpoint":                     fake.server.URL + "/userinfo",
			"jwks_uri":                              fake.server.URL + "/jwks",
			"response_types_supported":              []string{"code"},
			"code_challenge_methods_supported":      []string{"S256"},
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{
			"keys": []map[string]any{{
				"kty": "RSA",
				"use": "sig",
				"alg": "RS256",
				"kid": "test-key",
				"n":   base64.RawURLEncoding.EncodeToString(fake.key.PublicKey.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(fake.key.PublicKey.E)).Bytes()),
			}},
		})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if r.Form.Get("code") != "test-code" {
			http.Error(w, "invalid code", http.StatusBadRequest)
			return
		}
		fake.mu.Lock()
		nonce, challenge, claims := fake.nonce, fake.challenge, cloneClaims(fake.claims)
		fake.mu.Unlock()
		verifier := r.Form.Get("code_verifier")
		sum := sha256.Sum256([]byte(verifier))
		if base64.RawURLEncoding.EncodeToString(sum[:]) != challenge {
			http.Error(w, "invalid code_verifier", http.StatusBadRequest)
			return
		}
		now := time.Now()
		idClaims := map[string]any{
			"iss":            fake.server.URL,
			"sub":            claims["sub"],
			"aud":            fake.clientID,
			"exp":            now.Add(5 * time.Minute).Unix(),
			"iat":            now.Unix(),
			"nonce":          nonce,
			"email":          claims["email"],
			"email_verified": claims["email_verified"],
		}
		idToken, err := fake.signIDToken(idClaims)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{
			"access_token": "test-access-token",
			"token_type":   "Bearer",
			"expires_in":   300,
			"id_token":     idToken,
		})
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, _ *http.Request) {
		fake.mu.Lock()
		claims := cloneClaims(fake.claims)
		fake.mu.Unlock()
		writeJSON(w, claims)
	})
	fake.server = httptest.NewServer(mux)
	t.Cleanup(fake.server.Close)
	return fake
}

func (f *fakeOIDCProvider) expect(nonce, challenge string, claims map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nonce = nonce
	f.challenge = challenge
	f.claims = cloneClaims(claims)
}

func (f *fakeOIDCProvider) signIDToken(claims map[string]any) (string, error) {
	header := map[string]any{"alg": "RS256", "typ": "JWT", "kid": "test-key"}
	headerRaw, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	claimsRaw, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	unsigned := base64.RawURLEncoding.EncodeToString(headerRaw) + "." + base64.RawURLEncoding.EncodeToString(claimsRaw)
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func cloneClaims(claims map[string]any) map[string]any {
	raw, _ := json.Marshal(claims)
	var cloned map[string]any
	_ = json.Unmarshal(raw, &cloned)
	return cloned
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func testOIDCClaims(subject, email string, verified bool) map[string]any {
	return map[string]any{
		"sub":            subject,
		"email":          email,
		"email_verified": verified,
		"name":           "Alice Example",
		"picture":        "https://example.com/alice.png",
		"groups":         []string{"testers"},
		"user_metadata": map[string]any{
			"email":        email,
			"display_name": "Alice Example",
		},
	}
}

func createTestOIDCProvider(t *testing.T, f *fixture, fake *fakeOIDCProvider, allowUnbound bool) *service.OIDCProviderView {
	t.Helper()
	document, _, err := f.svc.Auth.ImportOIDCDiscovery(context.Background(), fake.server.URL+"/.well-known/openid-configuration")
	if err != nil {
		t.Fatalf("导入测试发现文档失败: %v", err)
	}
	allocation, err := f.svc.Auth.AllocateOIDCProvider()
	if err != nil {
		t.Fatalf("分配测试应用 ID 失败: %v", err)
	}
	provider, err := f.svc.Auth.CreateOIDCProvider(service.OIDCProviderInput{
		ID:                allocation.ProviderID,
		AllocationToken:   allocation.Token,
		Name:              "测试 OIDC",
		DiscoveryURL:      "https://discovery.invalid",
		DiscoveryDocument: document,
		ClientID:          fake.clientID,
		ClientSecret:      "test-secret",
		Scopes:            "openid email profile groups",
		Enabled:           true,
		AllowUnbound:      allowUnbound,
		SubPath:           "sub",
		EmailPath:         "user_metadata.email",
		NamePath:          "user_metadata.display_name",
		PicturePath:       "picture",
		GroupClaim:        "groups",
		GroupMappings:     []model.OIDCGroupMapping{{Value: "testers", GroupID: f.normalGID}},
	})
	if err != nil {
		t.Fatalf("创建测试 OIDC 提供方失败: %v", err)
	}
	return provider
}

func runTestOIDCFlow(t *testing.T, f *fixture, fake *fakeOIDCProvider, providerID int64, claims map[string]any, redirect string) (*model.User, string, error) {
	t.Helper()
	callback := "https://picsky.example.com/api/v1/auth/oidc/" + strconv.FormatInt(providerID, 10) + "/callback"
	start, err := f.svc.Auth.BeginOIDCLogin(context.Background(), providerID, callback, redirect)
	if err != nil {
		return nil, "", err
	}
	parsed, err := url.Parse(start.AuthURL)
	if err != nil {
		t.Fatalf("解析授权地址失败: %v", err)
	}
	query := parsed.Query()
	if query.Get("code_challenge_method") != "S256" {
		t.Fatalf("授权地址缺少 PKCE S256: %s", start.AuthURL)
	}
	if query.Get("nonce") == "" {
		t.Fatalf("授权地址缺少 nonce: %s", start.AuthURL)
	}
	fake.expect(query.Get("nonce"), query.Get("code_challenge"), claims)
	result, err := f.svc.Auth.CompleteOIDCCallback(
		context.Background(),
		providerID,
		query.Get("state"),
		query.Get("state"),
		"test-code",
		"127.0.0.1",
		"test-agent",
	)
	if err != nil {
		return nil, "", err
	}
	return result.User, result.RedirectPath, nil
}

func runTestOIDCBindFlow(t *testing.T, f *fixture, fake *fakeOIDCProvider, providerID, userID int64, claims map[string]any) (*model.User, error) {
	t.Helper()
	callback := "https://picsky.example.com/api/v1/auth/oidc/" + strconv.FormatInt(providerID, 10) + "/callback"
	start, err := f.svc.Auth.BeginOIDCBind(context.Background(), userID, providerID, callback, "/profile")
	if err != nil {
		return nil, err
	}
	parsed, err := url.Parse(start.AuthURL)
	if err != nil {
		t.Fatalf("解析绑定授权地址失败: %v", err)
	}
	query := parsed.Query()
	if query.Get("code_challenge_method") != "S256" || query.Get("nonce") == "" {
		t.Fatalf("绑定授权地址缺少 PKCE 或 nonce: %s", start.AuthURL)
	}
	fake.expect(query.Get("nonce"), query.Get("code_challenge"), claims)
	result, err := f.svc.Auth.CompleteOIDCCallback(
		context.Background(),
		providerID,
		query.Get("state"),
		query.Get("state"),
		"test-code",
		"127.0.0.1",
		"test-agent",
	)
	if err != nil {
		return nil, err
	}
	if !result.Bound {
		t.Fatal("绑定回调结果应标记为 Bound")
	}
	return result.User, nil
}

func TestOIDCLoginCreatesUserAndMapsGroup(t *testing.T) {
	f := newFixture(t)
	if err := f.svc.Settings.Update(map[string]string{"site_url": "https://picsky.example.com"}); err != nil {
		t.Fatalf("设置站点地址失败: %v", err)
	}
	fake := newFakeOIDCProvider(t, "test-client")
	provider := createTestOIDCProvider(t, f, fake, true)

	claims := testOIDCClaims("subject-1", "alice@example.com", true)
	user, redirect, err := runTestOIDCFlow(t, f, fake, provider.ID, claims, "/upload?from=oidc")
	if err != nil {
		t.Fatalf("第三方登录失败: %v", err)
	}
	if redirect != "/upload?from=oidc" {
		t.Fatalf("回调目标应为站内路径, 实际 %q", redirect)
	}
	if user.Email != "alice@example.com" || user.Nickname != "Alice Example" {
		t.Fatalf("用户资料映射错误: email=%q nickname=%q", user.Email, user.Nickname)
	}
	if user.PasswordHash != "" {
		t.Fatalf("第三方创建的用户不应有本地密码")
	}
	if user.GroupID != f.normalGID {
		t.Fatalf("用户组映射失败: got %d want %d", user.GroupID, f.normalGID)
	}
	if user.AvatarURL != "https://example.com/alice.png" {
		t.Fatalf("头像映射失败: %q", user.AvatarURL)
	}
	var identity model.OIDCIdentity
	if err := f.database.Where("provider_id = ? AND subject = ?", provider.ID, "subject-1").First(&identity).Error; err != nil {
		t.Fatalf("未写入身份绑定: %v", err)
	}
	if identity.UserID != user.ID {
		t.Fatalf("身份绑定用户错误: got %d want %d", identity.UserID, user.ID)
	}

	secondUser, _, err := runTestOIDCFlow(t, f, fake, provider.ID, claims, "/")
	if err != nil {
		t.Fatalf("重复第三方登录失败: %v", err)
	}
	if secondUser.ID != user.ID {
		t.Fatalf("重复登录应复用同一用户: got %d want %d", secondUser.ID, user.ID)
	}

	publicProviders, err := f.svc.Auth.PublicOIDCProviders()
	if err != nil {
		t.Fatalf("读取公开提供方失败: %v", err)
	}
	if len(publicProviders) != 1 || publicProviders[0].Name != "测试 OIDC" {
		t.Fatalf("公开提供方列表错误: %+v", publicProviders)
	}
}

func TestOIDCLoginLinksVerifiedEmailAndRejectsUnverified(t *testing.T) {
	f := newFixture(t)
	if err := f.svc.Settings.Update(map[string]string{"site_url": "https://picsky.example.com"}); err != nil {
		t.Fatalf("设置站点地址失败: %v", err)
	}
	fake := newFakeOIDCProvider(t, "test-client")
	provider := createTestOIDCProvider(t, f, fake, false)

	var admin model.User
	if err := f.database.Where("username = ?", "admin").First(&admin).Error; err != nil {
		t.Fatalf("读取管理员失败: %v", err)
	}
	verifiedUser, _, err := runTestOIDCFlow(t, f, fake, provider.ID,
		testOIDCClaims("subject-admin", admin.Email, true), "/")
	if err != nil {
		t.Fatalf("已验证邮箱应绑定已有账号: %v", err)
	}
	if verifiedUser.ID != admin.ID {
		t.Fatalf("应绑定到已有管理员账号: got %d want %d", verifiedUser.ID, admin.ID)
	}

	_, _, err = runTestOIDCFlow(t, f, fake, provider.ID,
		testOIDCClaims("subject-unverified", admin.Email, false), "/")
	if err == nil {
		t.Fatal("未验证邮箱不得绑定已有账号")
	}
	var apiErr *apperr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "oidc_email_unverified" {
		t.Fatalf("错误码应为 oidc_email_unverified, 实际 %v", err)
	}

	_, _, err = runTestOIDCFlow(t, f, fake, provider.ID,
		testOIDCClaims("subject-unbound", "missing@example.com", true), "/")
	if err == nil {
		t.Fatal("未绑定且不允许自动创建时应拒绝登录")
	}
	if !errors.As(err, &apiErr) || apiErr.Code != "oidc_unbound" {
		t.Fatalf("错误码应为 oidc_unbound, 实际 %v", err)
	}
}

func TestOIDCProviderCRUDHidesSecret(t *testing.T) {
	f := newFixture(t)
	fake := newFakeOIDCProvider(t, "test-client")
	provider := createTestOIDCProvider(t, f, fake, true)
	if !provider.ClientSecretSet {
		t.Fatal("创建后应报告客户端密钥已设置")
	}

	empty := ""
	clear := true
	updated, err := f.svc.Auth.UpdateOIDCProvider(provider.ID, service.OIDCProviderPatch{
		ClientSecret:      &empty,
		ClearClientSecret: &clear,
	})
	if err != nil {
		t.Fatalf("更新提供方失败: %v", err)
	}
	if updated.ClientSecretSet {
		t.Fatal("清除后不应报告客户端密钥已设置")
	}
	if err := f.svc.Auth.DeleteOIDCProvider(provider.ID); err != nil {
		t.Fatalf("删除提供方失败: %v", err)
	}
	var count int64
	if err := f.database.Model(&model.OIDCIdentity{}).Where("provider_id = ?", provider.ID).Count(&count).Error; err != nil {
		t.Fatalf("统计身份绑定失败: %v", err)
	}
	if count != 0 {
		t.Fatalf("删除提供方后身份绑定应清空, 剩余 %d", count)
	}
}

func TestOIDCProviderAllocationNeverReusesIDs(t *testing.T) {
	f := newFixture(t)
	document := `{
  "issuer": "https://id.example.com",
  "authorization_endpoint": "https://id.example.com/authorize",
  "token_endpoint": "https://id.example.com/token",
  "jwks_uri": "https://id.example.com/jwks"
}`
	first, err := f.svc.Auth.AllocateOIDCProvider()
	if err != nil {
		t.Fatalf("首次分配失败: %v", err)
	}
	second, err := f.svc.Auth.AllocateOIDCProvider()
	if err != nil {
		t.Fatalf("第二次分配失败: %v", err)
	}
	if first.ProviderID == second.ProviderID {
		t.Fatalf("连续分配应使用不同 ID: %d", first.ProviderID)
	}
	if second.ProviderID <= first.ProviderID {
		t.Fatalf("ID 必须递增: first=%d second=%d", first.ProviderID, second.ProviderID)
	}

	input := service.OIDCProviderInput{
		ID:                first.ProviderID,
		AllocationToken:   first.Token,
		Name:              "分配测试",
		DiscoveryDocument: document,
		ClientID:          "client-id",
		Scopes:            "openid email profile",
		Enabled:           true,
		SubPath:           "sub",
		EmailPath:         "email",
		NamePath:          "name",
		PicturePath:       "picture",
	}
	provider, err := f.svc.Auth.CreateOIDCProvider(input)
	if err != nil {
		t.Fatalf("使用分配 ID 创建失败: %v", err)
	}
	if provider.ID != first.ProviderID {
		t.Fatalf("创建后 ID 应保持分配值: got %d want %d", provider.ID, first.ProviderID)
	}
	if err := f.svc.Auth.DeleteOIDCProvider(provider.ID); err != nil {
		t.Fatalf("删除提供方失败: %v", err)
	}
	if _, err := f.svc.Auth.CreateOIDCProvider(input); err == nil {
		t.Fatal("已消费的分配令牌不能再次创建")
	}

	third, err := f.svc.Auth.AllocateOIDCProvider()
	if err != nil {
		t.Fatalf("第三次分配失败: %v", err)
	}
	if third.ProviderID <= second.ProviderID {
		t.Fatalf("删除提供方后 ID 不得回收: deleted=%d third=%d", provider.ID, third.ProviderID)
	}
}

func TestOIDCBindAndUnbind(t *testing.T) {
	f := newFixture(t)
	if err := f.svc.Settings.Update(map[string]string{"site_url": "https://picsky.example.com"}); err != nil {
		t.Fatalf("设置站点地址失败: %v", err)
	}
	fake := newFakeOIDCProvider(t, "test-client")
	provider := createTestOIDCProvider(t, f, fake, true)

	adminUser := f.admin.User
	user, err := runTestOIDCBindFlow(t, f, fake, provider.ID, adminUser.ID,
		testOIDCClaims("bind-admin-subject", "bind-admin@example.com", true))
	if err != nil {
		t.Fatalf("绑定第三方账号失败: %v", err)
	}
	if user.ID != adminUser.ID {
		t.Fatalf("绑定后用户不应变化: got %d want %d", user.ID, adminUser.ID)
	}
	bindings, err := f.svc.Auth.OIDCBindings(adminUser.ID)
	if err != nil {
		t.Fatalf("读取绑定列表失败: %v", err)
	}
	if len(bindings) != 1 || bindings[0].ProviderID != provider.ID {
		t.Fatalf("绑定列表错误: %+v", bindings)
	}
	if err := f.svc.Auth.UnbindOIDCIdentity(adminUser.ID, provider.ID, "127.0.0.1", "test-agent"); err != nil {
		t.Fatalf("解绑失败: %v", err)
	}
	if bindings, err = f.svc.Auth.OIDCBindings(adminUser.ID); err != nil {
		t.Fatalf("读取绑定列表失败: %v", err)
	}
	if len(bindings) != 0 {
		t.Fatalf("解绑未清除身份: %+v", bindings)
	}

	// 已被其他用户绑定的 subject 不得再次绑定.
	claimUser, _, err := runTestOIDCFlow(t, f, fake, provider.ID,
		testOIDCClaims("existing-subject", "existing@example.com", true), "/")
	if err != nil {
		t.Fatalf("创建第三方用户失败: %v", err)
	}
	_, err = runTestOIDCBindFlow(t, f, fake, provider.ID, adminUser.ID,
		testOIDCClaims("existing-subject", "existing@example.com", true))
	if err == nil {
		t.Fatal("同一 subject 不应绑定到第二个用户")
	}
	var apiErr *apperr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "oidc_identity_conflict" {
		t.Fatalf("错误码应为 oidc_identity_conflict, 实际 %v", err)
	}

	// 第三方创建且没有本地密码的用户不能解绑最后一个登录方式.
	if err := f.svc.Auth.UnbindOIDCIdentity(claimUser.ID, provider.ID, "127.0.0.1", "test-agent"); err == nil {
		t.Fatal("无本地密码且只有一个绑定时不应允许解绑")
	} else if !errors.As(err, &apiErr) || apiErr.Code != "oidc_last_login_method" {
		t.Fatalf("错误码应为 oidc_last_login_method, 实际 %v", err)
	}
}
