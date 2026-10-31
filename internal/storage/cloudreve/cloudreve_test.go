package cloudreve

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yeqingky/Picsky/internal/storage"
)

const (
	testToken     = "access-token"
	testSessionID = "session-1"
)

func TestDriverSpec(t *testing.T) {
	spec, ok := storage.SpecOf(driverType)
	if !ok {
		t.Fatal("Cloudreve 驱动未注册")
	}
	if spec.Label != "Cloudreve" {
		t.Errorf("驱动名称 = %q, 期望 Cloudreve", spec.Label)
	}
	if !spec.URLFromBackend {
		t.Error("Cloudreve 驱动应由后端分配直链")
	}
	if spec.DefaultPathTemplate != "{md5:2}/{md5}.{ext}" {
		t.Errorf("Cloudreve 默认路径 = %q", spec.DefaultPathTemplate)
	}
	if !spec.Has(storage.CapUpload) || !spec.Has(storage.CapList) || !spec.Has(storage.CapRead) || !spec.Has(storage.CapStat) {
		t.Errorf("Cloudreve 驱动能力声明不完整: %v", spec.Capabilities)
	}
	if spec.Has(storage.CapDelete) {
		t.Error("Cloudreve 驱动不应声明删除能力")
	}
	if len(spec.ConfigFields) != 3 {
		t.Fatalf("配置字段数量 = %d, 期望 3", len(spec.ConfigFields))
	}
	if spec.ConfigFields[1].Name != "directory" {
		t.Errorf("目录字段 = %+v", spec.ConfigFields[1])
	}
	if spec.ConfigFields[2].Name != "auto_login" || spec.ConfigFields[2].Type != "switch" {
		t.Errorf("自动登录字段 = %+v", spec.ConfigFields[2])
	}
	if len(spec.SecretFields) != 3 ||
		spec.SecretFields[0].Name != "username" ||
		spec.SecretFields[1].Name != "password" ||
		spec.SecretFields[2].Name != "refresh_token" {
		t.Errorf("凭据字段 = %+v", spec.SecretFields)
	}
}

func TestPutLogsInUploadsAndReturnsDirectLink(t *testing.T) {
	var logins atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v4/session/token":
			logins.Add(1)
			checkLogin(t, r)
			writeEnvelope(t, w, 0, loginPayload(testToken, time.Now().Add(time.Hour).Format(time.RFC3339Nano)))
		case "/api/v4/file/create":
			checkFolder(t, r)
			writeEnvelope(t, w, 0, map[string]any{})
		case "/api/v4/file/upload":
			checkSession(t, r)
			writeEnvelope(t, w, 0, uploadSessionPayload(0))
		case "/api/v4/file/upload/" + testSessionID + "/0":
			checkChunk(t, r, "image-bytes", testToken)
			writeEnvelope(t, w, 0, map[string]any{})
		case "/api/v4/file/source":
			checkSource(t, r)
			writeEnvelope(t, w, 0, []map[string]string{{
				"link":     serverURL(r) + "/f/abc/foo.png",
				"file_url": "cloudreve://my/picsky/0f/foo.png",
			}})
		default:
			t.Errorf("未预期的请求: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter := newTestAdapter(t, server.URL)
	result, err := adapter.Put(context.Background(), "picsky/0f/foo.png", strings.NewReader("image-bytes"), int64(len("image-bytes")), "image/png")
	if err != nil {
		t.Fatalf("Put 失败: %v", err)
	}
	if got := logins.Load(); got != 1 {
		t.Errorf("登录次数 = %d, 期望 1", got)
	}
	want := server.URL + "/f/abc/foo.png"
	if result.URL != want {
		t.Errorf("直链 = %q, 期望 %q", result.URL, want)
	}
}

func TestPutUploadsChunksInOrder(t *testing.T) {
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v4/session/token":
			writeEnvelope(t, w, 0, loginPayload(testToken))
		case r.URL.Path == "/api/v4/file/create":
			writeEnvelope(t, w, 0, map[string]any{})
		case r.URL.Path == "/api/v4/file/upload":
			writeEnvelope(t, w, 0, uploadSessionPayload(4))
		case strings.HasPrefix(r.URL.Path, "/api/v4/file/upload/"+testSessionID+"/"):
			data, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("读取分片失败: %v", err)
				return
			}
			bodies = append(bodies, string(data))
			writeEnvelope(t, w, 0, map[string]any{})
		case r.URL.Path == "/api/v4/file/source":
			writeEnvelope(t, w, 0, []map[string]string{{"link": serverURL(r) + "/f/abc/foo.png"}})
		default:
			t.Errorf("未预期的请求: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter := newTestAdapter(t, server.URL)
	if _, err := adapter.Put(context.Background(), "foo.png", strings.NewReader("0123456789"), 10, "image/png"); err != nil {
		t.Fatalf("Put 失败: %v", err)
	}
	want := []string{"0123", "4567", "89"}
	if len(bodies) != len(want) {
		t.Fatalf("分片数量 = %d, 期望 %d", len(bodies), len(want))
	}
	for index := range want {
		if bodies[index] != want[index] {
			t.Errorf("分片 %d = %q, 期望 %q", index, bodies[index], want[index])
		}
	}
}

func TestPutUsesEndpointAPIPrefix(t *testing.T) {
	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path)
		switch r.URL.Path {
		case "/cloud/api/v4/session/token":
			writeEnvelope(t, w, 0, loginPayload(testToken))
		case "/cloud/api/v4/file/create":
			writeEnvelope(t, w, 0, map[string]any{})
		case "/cloud/api/v4/file/upload":
			writeEnvelope(t, w, 0, uploadSessionPayload(0))
		case "/cloud/api/v4/file/upload/" + testSessionID + "/0":
			writeEnvelope(t, w, 0, map[string]any{})
		case "/cloud/api/v4/file/source":
			writeEnvelope(t, w, 0, []map[string]string{{"link": serverURL(r) + "/f/abc/foo.png"}})
		default:
			t.Errorf("未预期的请求: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter := newTestAdapter(t, server.URL+"/cloud/")
	if _, err := adapter.Put(context.Background(), "foo.png", strings.NewReader("image"), 5, "image/png"); err != nil {
		t.Fatalf("Put 失败: %v", err)
	}
	if len(seen) != 5 {
		t.Errorf("请求路径 = %v", seen)
	}
}

func TestPutUsesRemoteUploadURL(t *testing.T) {
	var chunks []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v4/session/token":
			writeEnvelope(t, w, 0, loginPayload(testToken))
		case r.URL.Path == "/api/v4/file/create":
			writeEnvelope(t, w, 0, map[string]any{})
		case r.URL.Path == "/api/v4/file/upload":
			writeEnvelope(t, w, 0, map[string]any{
				"session_id": testSessionID,
				"chunk_size": 2,
				"upload_urls": []string{
					serverURL(r) + "/slave/upload/" + testSessionID,
				},
				"credential": "Bearer Cr slave-token",
				"storage_policy": map[string]any{
					"type":  "remote",
					"relay": false,
				},
			})
		case r.URL.Path == "/slave/upload/"+testSessionID:
			if got := r.Header.Get("Authorization"); got != "Bearer Cr slave-token" {
				t.Errorf("远端 Authorization = %q", got)
			}
			chunks = append(chunks, r.URL.Query().Get("chunk"))
			_, _ = io.WriteString(w, `{"code":0}`)
		case r.URL.Path == "/api/v4/file/source":
			writeEnvelope(t, w, 0, []map[string]string{{"link": serverURL(r) + "/f/abc/foo.png"}})
		default:
			t.Errorf("未预期的请求: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter := newTestAdapter(t, server.URL)
	if _, err := adapter.Put(context.Background(), "foo.png", strings.NewReader("abcde"), 5, "image/png"); err != nil {
		t.Fatalf("Put 失败: %v", err)
	}
	if len(chunks) != 3 || chunks[0] != "0" || chunks[1] != "1" || chunks[2] != "2" {
		t.Errorf("远端分片索引 = %v", chunks)
	}
}

func TestPutErrors(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
		wantSub string
	}{
		{
			name: "登录失败",
			handler: func(w http.ResponseWriter, r *http.Request) {
				writeEnvelope(t, w, 40001, nil)
			},
			wantSub: "Cloudreve 返回错误 40001",
		},
		{
			name: "需要两步验证",
			handler: func(w http.ResponseWriter, r *http.Request) {
				writeEnvelope(t, w, 203, nil)
			},
			wantSub: "两步验证",
		},
		{
			name: "没有直链权限",
			handler: func(w http.ResponseWriter, r *http.Request) {
				payload := loginPayload(testToken)
				payload["user"] = map[string]any{
					"group": map[string]any{"direct_link_batch_size": 0},
				}
				writeEnvelope(t, w, 0, payload)
			},
			wantSub: "直链权限",
		},
		{
			name: "上传 HTTP 错误",
			handler: func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v4/session/token":
					writeEnvelope(t, w, 0, loginPayload(testToken))
				case "/api/v4/file/create":
					writeEnvelope(t, w, 0, map[string]any{})
				case "/api/v4/file/upload":
					writeEnvelope(t, w, 0, uploadSessionPayload(0))
				default:
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = io.WriteString(w, "boom")
				}
			},
			wantSub: "HTTP 500",
		},
		{
			name: "不支持的存储策略",
			handler: func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v4/session/token":
					writeEnvelope(t, w, 0, loginPayload(testToken))
				case "/api/v4/file/create":
					writeEnvelope(t, w, 0, map[string]any{})
				default:
					writeEnvelope(t, w, 0, map[string]any{
						"session_id": testSessionID,
						"chunk_size": 0,
						"storage_policy": map[string]any{
							"type":  "s3",
							"relay": false,
						},
					})
				}
			},
			wantSub: "暂不支持",
		},
		{
			name: "直链接口错误",
			handler: func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v4/session/token":
					writeEnvelope(t, w, 0, loginPayload(testToken))
				case "/api/v4/file/create":
					writeEnvelope(t, w, 0, map[string]any{})
				case "/api/v4/file/upload":
					writeEnvelope(t, w, 0, uploadSessionPayload(0))
				case "/api/v4/file/upload/" + testSessionID + "/0":
					writeEnvelope(t, w, 0, map[string]any{})
				default:
					writeEnvelope(t, w, 50001, nil)
				}
			},
			wantSub: "Cloudreve 返回错误 50001",
		},
		{
			name: "直链为空",
			handler: func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v4/session/token":
					writeEnvelope(t, w, 0, loginPayload(testToken))
				case "/api/v4/file/create":
					writeEnvelope(t, w, 0, map[string]any{})
				case "/api/v4/file/upload":
					writeEnvelope(t, w, 0, uploadSessionPayload(0))
				case "/api/v4/file/upload/" + testSessionID + "/0":
					writeEnvelope(t, w, 0, map[string]any{})
				default:
					writeEnvelope(t, w, 0, []map[string]string{{"link": ""}})
				}
			},
			wantSub: "直链为空",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(testCase.handler)
			defer server.Close()
			adapter := newTestAdapter(t, server.URL)
			_, err := adapter.Put(context.Background(), "foo.png", strings.NewReader("image"), 5, "image/png")
			if err == nil || !strings.Contains(err.Error(), testCase.wantSub) {
				t.Fatalf("错误 = %v, 期望包含 %q", err, testCase.wantSub)
			}
		})
	}
}

func TestNewAdapterValidation(t *testing.T) {
	validSecrets := map[string]string{"username": "me@example.com", "password": "secret"}
	cases := []struct {
		name    string
		config  map[string]string
		secrets map[string]string
		wantSub string
	}{
		{"缺少地址", map[string]string{}, validSecrets, "endpoint"},
		{"非法协议", map[string]string{"endpoint": "ftp://example.com"}, validSecrets, "http:// 或 https://"},
		{"缺少邮箱", map[string]string{"endpoint": "https://cloudreve.example.com"}, map[string]string{"password": "secret"}, "邮箱"},
		{"缺少密码", map[string]string{"endpoint": "https://cloudreve.example.com"}, map[string]string{"username": "me@example.com"}, "密码"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := newAdapter(storage.Options{Config: testCase.config, Secrets: testCase.secrets})
			if err == nil || !strings.Contains(err.Error(), testCase.wantSub) {
				t.Fatalf("错误 = %v, 期望包含 %q", err, testCase.wantSub)
			}
		})
	}
}

func TestLoginPasswordTwoFactor(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v4/session/token":
			writeEnvelope(t, w, 203, "2fa-session")
		case "/api/v4/session/token/2fa":
			var body struct {
				SessionID string `json:"session_id"`
				OTP       string `json:"opt"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("解析 2FA 请求失败: %v", err)
				return
			}
			if body.SessionID != "2fa-session" || body.OTP != "123456" {
				t.Errorf("2FA 请求 = %+v", body)
			}
			writeEnvelope(t, w, 0, loginPayload(testToken))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	result, err := LoginPassword(context.Background(), server.URL, "me@example.com", "secret")
	if err != nil {
		t.Fatalf("LoginPassword 失败: %v", err)
	}
	if !result.TwoFactor || result.SessionID != "2fa-session" {
		t.Fatalf("两步验证结果 = %+v", result)
	}
	auth, err := CompleteTwoFactor(context.Background(), server.URL, result.SessionID, "123456")
	if err != nil {
		t.Fatalf("CompleteTwoFactor 失败: %v", err)
	}
	if auth.RefreshToken != "refresh-token" || auth.AccessToken != testToken {
		t.Errorf("两步验证令牌 = %+v", auth)
	}
}

func TestRefreshAccessToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/session/token/refresh" {
			http.NotFound(w, r)
			return
		}
		var body struct {
			RefreshToken string `json:"refresh_token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("解析刷新请求失败: %v", err)
			return
		}
		if body.RefreshToken != "refresh-token" {
			t.Errorf("RefreshToken = %q", body.RefreshToken)
		}
		writeEnvelope(t, w, 0, map[string]any{
			"access_token":    "new-access",
			"access_expires":  time.Now().Add(time.Hour).Format(time.RFC3339Nano),
			"refresh_token":   "new-refresh",
			"refresh_expires": time.Now().Add(24 * time.Hour).Format(time.RFC3339Nano),
		})
	}))
	defer server.Close()

	result, err := RefreshAccessToken(context.Background(), server.URL, "refresh-token")
	if err != nil {
		t.Fatalf("RefreshAccessToken 失败: %v", err)
	}
	if result.AccessToken != "new-access" || result.RefreshToken != "new-refresh" {
		t.Errorf("刷新结果 = %+v", result)
	}
}

func TestAutoLoginFallsBackToPassword(t *testing.T) {
	var logins int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v4/session/token/refresh":
			writeEnvelope(t, w, 400, map[string]any{})
		case "/api/v4/session/token":
			logins++
			writeEnvelope(t, w, 0, loginPayload("auto-access"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	rawAdapter, err := newAdapter(storage.Options{
		Config: map[string]string{
			"endpoint":   server.URL,
			"auto_login": "true",
		},
		Secrets: map[string]string{
			"username":      "me@example.com",
			"password":      "secret",
			"refresh_token": "expired-refresh",
		},
	})
	if err != nil {
		t.Fatalf("初始化自动登录驱动失败: %v", err)
	}
	auto, ok := rawAdapter.(*adapter)
	if !ok {
		t.Fatal("自动登录驱动类型错误")
	}
	token, err := auto.ensureToken(context.Background())
	if err != nil {
		t.Fatalf("自动登录失败: %v", err)
	}
	if token != "auto-access" || logins != 1 {
		t.Fatalf("自动登录 token = %q, 登录次数 = %d", token, logins)
	}

	manual, err := newAdapter(storage.Options{
		Config: map[string]string{
			"endpoint":   server.URL,
			"auto_login": "false",
		},
		Secrets: map[string]string{
			"username":      "me@example.com",
			"password":      "secret",
			"refresh_token": "expired-refresh",
		},
	})
	if err != nil {
		t.Fatalf("初始化手动令牌驱动失败: %v", err)
	}
	manualAdapter, ok := manual.(*adapter)
	if !ok {
		t.Fatal("手动令牌驱动类型错误")
	}
	if _, err := manualAdapter.ensureToken(context.Background()); err == nil {
		t.Fatal("关闭自动登录时刷新失败应返回错误")
	}
	if logins != 1 {
		t.Fatalf("关闭自动登录后不应再次登录, 登录次数 = %d", logins)
	}
}

func TestPutUsesStoredRefreshToken(t *testing.T) {
	var passwordLogin, refreshLogin bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v4/session/token":
			passwordLogin = true
			writeEnvelope(t, w, 0, loginPayload(testToken))
		case "/api/v4/session/token/refresh":
			refreshLogin = true
			writeEnvelope(t, w, 0, map[string]any{
				"access_token":  testToken,
				"refresh_token": "new-refresh",
			})
		case "/api/v4/file/create":
			writeEnvelope(t, w, 0, map[string]any{})
		case "/api/v4/file/upload":
			writeEnvelope(t, w, 0, uploadSessionPayload(0))
		case "/api/v4/file/upload/" + testSessionID + "/0":
			writeEnvelope(t, w, 0, map[string]any{})
		case "/api/v4/file/source":
			writeEnvelope(t, w, 0, []map[string]string{{"link": serverURL(r) + "/f/abc/foo.png"}})
		default:
			t.Errorf("未预期的请求: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter, err := newAdapter(storage.Options{
		Config: map[string]string{"endpoint": server.URL},
		Secrets: map[string]string{
			"refresh_token": "stored-refresh",
		},
	})
	if err != nil {
		t.Fatalf("初始化驱动失败: %v", err)
	}
	if _, err := adapter.Put(context.Background(), "foo.png", strings.NewReader("image"), 5, "image/png"); err != nil {
		t.Fatalf("Put 失败: %v", err)
	}
	if passwordLogin {
		t.Error("存在 RefreshToken 时不应使用账号密码登录")
	}
	if !refreshLogin {
		t.Error("应使用 RefreshToken 刷新 AccessToken")
	}
}

func TestTestUploadsProbePNG(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v4/session/token":
			writeEnvelope(t, w, 0, loginPayload(testToken))
		case r.URL.Path == "/api/v4/file/create":
			var body struct {
				URI string `json:"uri"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("解析目录请求失败: %v", err)
				return
			}
			if body.URI != "cloudreve://my/picsky/_probe" {
				t.Errorf("探测目录 = %q", body.URI)
			}
			writeEnvelope(t, w, 0, map[string]any{})
		case r.URL.Path == "/api/v4/file/upload":
			var body struct {
				URI string `json:"uri"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("解析上传会话失败: %v", err)
				return
			}
			if !strings.HasPrefix(body.URI, "cloudreve://my/picsky/_probe/") || !strings.HasSuffix(body.URI, ".png") {
				t.Errorf("探测文件 URI = %q", body.URI)
			}
			writeEnvelope(t, w, 0, uploadSessionPayload(0))
		case strings.HasPrefix(r.URL.Path, "/api/v4/file/upload/"):
			data, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("读取探测图片失败: %v", err)
				return
			}
			if !strings.HasPrefix(string(data), "\x89PNG") {
				t.Errorf("探测内容不是 PNG: %x", data)
			}
			writeEnvelope(t, w, 0, map[string]any{})
		case r.URL.Path == "/api/v4/file/source":
			writeEnvelope(t, w, 0, []map[string]string{{"link": serverURL(r) + "/f/probe/probe.png"}})
		default:
			t.Errorf("未预期的请求: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter := newTestAdapter(t, server.URL)
	if err := adapter.Test(context.Background(), ""); err != nil {
		t.Fatalf("Test 失败: %v", err)
	}
}

func TestListRecursesAndPaginates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v4/session/token":
			writeEnvelope(t, w, 0, loginPayload(testToken))
		case "/api/v4/file":
			if r.URL.Query().Get("page_size") != "100" {
				t.Errorf("page_size = %q", r.URL.Query().Get("page_size"))
			}
			switch r.URL.Query().Get("uri") {
			case "cloudreve://my/picsky":
				if r.URL.Query().Get("next_page_token") == "next" {
					writeEnvelope(t, w, 0, map[string]any{
						"files": []map[string]any{
							{"type": 0, "name": "b.png", "size": 2, "path": "cloudreve://my/picsky/b.png"},
						},
						"pagination": map[string]any{"is_cursor": true},
					})
					return
				}
				writeEnvelope(t, w, 0, map[string]any{
					"files": []map[string]any{
						{"type": 1, "name": "sub", "path": "cloudreve://my/picsky/sub"},
						{"type": 0, "name": "a.png", "size": 1, "path": "cloudreve://my/picsky/a.png"},
					},
					"pagination": map[string]any{"is_cursor": true, "next_token": "next"},
				})
			case "cloudreve://my/picsky/sub":
				writeEnvelope(t, w, 0, map[string]any{
					"files": []map[string]any{
						{"type": 0, "name": "c.png", "size": 3, "path": "cloudreve://my/picsky/sub/c.png"},
					},
					"pagination": map[string]any{"is_cursor": true},
				})
			default:
				t.Errorf("未预期的列举 URI: %s", r.URL.Query().Get("uri"))
				http.NotFound(w, r)
			}
		default:
			t.Errorf("未预期的请求: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter := newTestAdapter(t, server.URL)
	objects, err := adapter.List(context.Background(), "picsky")
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	keys := map[string]int64{}
	for _, object := range objects {
		keys[object.Key] = object.Size
	}
	for key, size := range map[string]int64{
		"picsky/a.png":     1,
		"picsky/b.png":     2,
		"picsky/sub/c.png": 3,
	} {
		if keys[key] != size {
			t.Errorf("对象 %s 大小 = %d, 期望 %d", key, keys[key], size)
		}
	}
	if len(objects) != 3 {
		t.Errorf("列举对象数 = %d, 期望 3", len(objects))
	}
}

func TestStatAndReadUseDirectLinks(t *testing.T) {
	var downloadCalls, tempURLCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v4/session/token":
			writeEnvelope(t, w, 0, loginPayload(testToken))
		case "/api/v4/file/info":
			if r.URL.Query().Get("uri") != "cloudreve://my/picsky/foo.png" || r.URL.Query().Get("extended") != "true" {
				t.Errorf("文件信息参数 = %s", r.URL.RawQuery)
			}
			writeEnvelope(t, w, 0, map[string]any{
				"type":       0,
				"name":       "foo.png",
				"size":       5,
				"updated_at": time.Now().Format(time.RFC3339),
				"path":       "cloudreve://my/picsky/foo.png",
				"extended_info": map[string]any{
					"direct_links": []map[string]string{
						{"url": serverURL(r) + "/f/a/foo.png"},
					},
				},
			})
		case "/f/a/foo.png":
			downloadCalls++
			_, _ = io.WriteString(w, "hello")
		case "/api/v4/file/url":
			tempURLCalls++
			http.Error(w, "should not be called", http.StatusInternalServerError)
		default:
			t.Errorf("未预期的请求: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter := newTestAdapter(t, server.URL)
	info, err := adapter.Stat(context.Background(), "picsky/foo.png")
	if err != nil {
		t.Fatalf("Stat 失败: %v", err)
	}
	if info.URL != server.URL+"/f/a/foo.png" || info.Size != 5 {
		t.Errorf("对象信息 = %+v", info)
	}
	reader, err := adapter.Read(context.Background(), "picsky/foo.png")
	if err != nil {
		t.Fatalf("Read 失败: %v", err)
	}
	data, err := io.ReadAll(reader)
	reader.Close()
	if err != nil || string(data) != "hello" {
		t.Errorf("读取内容 = %q, 错误 %v", data, err)
	}
	if downloadCalls != 1 || tempURLCalls != 0 {
		t.Errorf("下载次数 = %d, 临时地址次数 = %d", downloadCalls, tempURLCalls)
	}
}

func TestReadFallsBackToTemporaryURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v4/session/token":
			writeEnvelope(t, w, 0, loginPayload(testToken))
		case "/api/v4/file/url":
			var body struct {
				URIs []string `json:"uris"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("解析下载地址请求失败: %v", err)
				return
			}
			if len(body.URIs) != 1 || body.URIs[0] != "cloudreve://my/picsky/foo.png" {
				t.Errorf("下载地址 URI = %v", body.URIs)
			}
			writeEnvelope(t, w, 0, map[string]any{
				"urls": []map[string]string{{"url": serverURL(r) + "/download/foo.png"}},
			})
		case "/download/foo.png":
			_, _ = io.WriteString(w, "content")
		default:
			t.Errorf("未预期的请求: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter := newTestAdapter(t, server.URL)
	reader, err := adapter.Read(context.Background(), "picsky/foo.png")
	if err != nil {
		t.Fatalf("Read 失败: %v", err)
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil || string(data) != "content" {
		t.Errorf("读取内容 = %q, 错误 %v", data, err)
	}
}

func TestUnsupportedOperations(t *testing.T) {
	adapter := &adapter{}
	if err := adapter.Delete(context.Background(), "foo.png"); err != storage.ErrNotSupported {
		t.Errorf("Delete 错误 = %v", err)
	}
}

func newTestAdapter(t *testing.T, endpoint string) storage.Adapter {
	t.Helper()
	adapter, err := newAdapter(storage.Options{
		Config:  map[string]string{"endpoint": endpoint},
		Secrets: map[string]string{"username": "me@example.com", "password": "secret"},
	})
	if err != nil {
		t.Fatalf("初始化驱动失败: %v", err)
	}
	return adapter
}

func uploadSessionPayload(chunkSize int64) map[string]any {
	return map[string]any{
		"session_id": testSessionID,
		"chunk_size": chunkSize,
		"storage_policy": map[string]any{
			"type":  "local",
			"relay": false,
		},
	}
}

func checkLogin(t *testing.T, r *http.Request) {
	t.Helper()
	if r.Method != http.MethodPost {
		t.Errorf("登录方法 = %s, 期望 POST", r.Method)
	}
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Errorf("解析登录请求失败: %v", err)
		return
	}
	if body.Email != "me@example.com" || body.Password != "secret" {
		t.Errorf("登录凭据 = %+v", body)
	}
}

func checkFolder(t *testing.T, r *http.Request) {
	t.Helper()
	if r.Method != http.MethodPost {
		t.Errorf("创建目录方法 = %s, 期望 POST", r.Method)
	}
	var body struct {
		URI           string `json:"uri"`
		Type          string `json:"type"`
		ErrOnConflict bool   `json:"err_on_conflict"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Errorf("解析目录请求失败: %v", err)
		return
	}
	if body.URI != "cloudreve://my/picsky/0f" || body.Type != "folder" || body.ErrOnConflict {
		t.Errorf("目录请求 = %+v", body)
	}
}

func checkSession(t *testing.T, r *http.Request) {
	t.Helper()
	if r.Method != http.MethodPut {
		t.Errorf("创建会话方法 = %s, 期望 PUT", r.Method)
	}
	var body struct {
		URI      string `json:"uri"`
		Size     int64  `json:"size"`
		MIMEType string `json:"mime_type"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Errorf("解析上传会话失败: %v", err)
		return
	}
	if body.URI != "cloudreve://my/picsky/0f/foo.png" || body.Size != int64(len("image-bytes")) || body.MIMEType != "image/png" {
		t.Errorf("上传会话 = %+v", body)
	}
}

func checkChunk(t *testing.T, r *http.Request, wantBody, token string) {
	t.Helper()
	if r.Method != http.MethodPost {
		t.Errorf("上传分片方法 = %s, 期望 POST", r.Method)
	}
	if got := r.Header.Get("Authorization"); got != "Bearer "+token {
		t.Errorf("Authorization = %q", got)
	}
	if r.ContentLength != int64(len(wantBody)) {
		t.Errorf("Content-Length = %d", r.ContentLength)
	}
	data, err := io.ReadAll(r.Body)
	if err != nil {
		t.Errorf("读取分片失败: %v", err)
		return
	}
	if string(data) != wantBody {
		t.Errorf("分片内容 = %q", data)
	}
}

func checkSource(t *testing.T, r *http.Request) {
	t.Helper()
	if r.Method != http.MethodPut {
		t.Errorf("创建直链方法 = %s, 期望 PUT", r.Method)
	}
	if got := r.Header.Get("Authorization"); got != "Bearer "+testToken {
		t.Errorf("Authorization = %q", got)
	}
	var body struct {
		URIs []string `json:"uris"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Errorf("解析直链请求失败: %v", err)
		return
	}
	if len(body.URIs) != 1 || body.URIs[0] != "cloudreve://my/picsky/0f/foo.png" {
		t.Errorf("直链请求 URI = %v", body.URIs)
	}
}

func loginPayload(accessToken string, expires ...string) map[string]any {
	token := map[string]any{
		"access_token":  accessToken,
		"refresh_token": "refresh-token",
	}
	if len(expires) > 0 {
		token["access_expires"] = expires[0]
	}
	return map[string]any{
		"user": map[string]any{
			"group": map[string]any{"direct_link_batch_size": 10},
		},
		"token": token,
	}
}

func writeEnvelope(t *testing.T, w http.ResponseWriter, code int, data any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"code": code,
		"msg":  "",
		"data": data,
	}); err != nil {
		t.Errorf("写入响应失败: %v", err)
	}
}

func serverURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
