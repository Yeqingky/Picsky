package acgfaka

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yeqingky/Picsky/internal/storage"
)

func newTestAdapter(t *testing.T, endpoint, cookie string) *adapter {
	t.Helper()
	created, err := newAdapter(storage.Options{
		Config:  map[string]string{"endpoint": endpoint},
		Secrets: map[string]string{"cookie": cookie},
	})
	if err != nil {
		t.Fatalf("创建适配器失败: %v", err)
	}
	return created.(*adapter)
}

func TestDriverSpec(t *testing.T) {
	spec, ok := storage.SpecOf(driverType)
	if !ok {
		t.Fatal("异次元店铺系统驱动未注册")
	}
	if spec.Label != "异次元店铺系统" {
		t.Errorf("驱动名称 = %q, 期望 异次元店铺系统", spec.Label)
	}
	if !spec.URLFromBackend {
		t.Error("异次元店铺系统应由后端分配直链")
	}
	if len(spec.Capabilities) != 1 || spec.Capabilities[0] != "upload" {
		t.Errorf("驱动应只声明上传能力: %v", spec.Capabilities)
	}
	if spec.Has(storage.CapDelete) || spec.Has(storage.CapList) || spec.Has(storage.CapRead) {
		t.Error("会员上传接口不应声明删除, 列举或读取能力")
	}
	if spec.MaxFileSize != 10<<20 {
		t.Errorf("单文件上限 = %d, 期望 10 MiB", spec.MaxFileSize)
	}
	if len(spec.Formats) != 3 || spec.Formats[0] != "jpeg" || spec.Formats[1] != "png" || spec.Formats[2] != "webp" {
		t.Errorf("允许格式 = %v, 期望 jpeg/png/webp", spec.Formats)
	}
	if len(spec.ConfigFields) != 2 || spec.ConfigFields[0].Name != "endpoint" || !spec.ConfigFields[0].Required || spec.ConfigFields[1].Name != "auto_login" {
		t.Errorf("配置字段 = %+v, 期望 endpoint 与 auto_login", spec.ConfigFields)
	}
	if len(spec.SecretFields) != 3 || spec.SecretFields[0].Name != "cookie" || !spec.SecretFields[0].Required ||
		spec.SecretFields[1].Name != "username" || spec.SecretFields[2].Name != "password" {
		t.Errorf("Cookie 与自动登录凭据字段 = %+v", spec.SecretFields)
	}
}

func TestNewAdapterValidatesConfig(t *testing.T) {
	cases := []struct {
		name    string
		opts    storage.Options
		wantSub string
	}{
		{"缺少地址", storage.Options{Secrets: map[string]string{"cookie": "USER_SESSION=x"}}, "站点地址"},
		{"非法协议", storage.Options{Config: map[string]string{"endpoint": "ftp://example.com"}, Secrets: map[string]string{"cookie": "USER_SESSION=x"}}, "http://"},
		{"包含查询参数", storage.Options{Config: map[string]string{"endpoint": "https://example.com?x=1"}, Secrets: map[string]string{"cookie": "USER_SESSION=x"}}, "查询参数"},
		{"缺少 Cookie", storage.Options{Config: map[string]string{"endpoint": "https://example.com"}}, "Cookie"},
		{"Cookie 缺少会话", storage.Options{Config: map[string]string{"endpoint": "https://example.com"}, Secrets: map[string]string{"cookie": "ACG-SHOP=x"}}, "Cookie"},
		{"自动登录缺少账号", storage.Options{Config: map[string]string{"endpoint": "https://example.com", "auto_login": "true"}, Secrets: map[string]string{"cookie": "USER_SESSION=x", "password": "p"}}, "账号"},
		{"自动登录缺少密码", storage.Options{Config: map[string]string{"endpoint": "https://example.com", "auto_login": "true"}, Secrets: map[string]string{"cookie": "USER_SESSION=x", "username": "u"}}, "密码"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := newAdapter(testCase.opts); err == nil || !strings.Contains(err.Error(), testCase.wantSub) {
				t.Fatalf("错误 = %v, 期望包含 %q", err, testCase.wantSub)
			}
		})
	}
}

func TestNormalizeCookie(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    string
		wantErr string
	}{
		{
			name: "只粘贴 USER_SESSION 原值",
			raw:  "eyJ0eXAiOiJKV1QifQ.eyJ1aWQiOjF9.signature",
			want: "USER_SESSION=eyJ0eXAiOiJKV1QifQ.eyJ1aWQiOjF9.signature",
		},
		{
			name: "原值包含 base64 填充",
			raw:  "Zm9vYmFy==",
			want: "USER_SESSION=Zm9vYmFy==",
		},
		{
			name: "完整 Cookie",
			raw:  "USER_SESSION=abc; ACG-SHOP=def; ACG_CSP=0123456789abcdef0123456789abcdef",
			want: "USER_SESSION=abc; ACG-SHOP=def; ACG_CSP=0123456789abcdef0123456789abcdef",
		},
		{
			name: "会话不在首位",
			raw:  "ACG-SHOP=def; USER_SESSION=abc",
			want: "ACG-SHOP=def; USER_SESSION=abc",
		},
		{
			name:    "缺少会话",
			raw:     "ACG-SHOP=def; ACG_CSP=abc",
			wantErr: "USER_SESSION",
		},
		{
			name:    "会话为空",
			raw:     "USER_SESSION=; ACG-SHOP=def",
			wantErr: "不能为空",
		},
		{
			name:    "包含换行",
			raw:     "USER_SESSION=abc\r\nX-Test: 1",
			wantErr: "换行",
		},
		{
			name:    "无效名称",
			raw:     "USER_SESSION=abc; BAD NAME=def",
			wantErr: "名称无效",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := normalizeCookie(testCase.raw)
			if testCase.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), testCase.wantErr) {
					t.Fatalf("错误 = %v, 期望包含 %q", err, testCase.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeCookie 失败: %v", err)
			}
			if got != testCase.want {
				t.Fatalf("Cookie = %q, 期望 %q", got, testCase.want)
			}
		})
	}
}

func TestPutUploadsMultipartWithCookieAndReferer(t *testing.T) {
	var (
		gotPath        string
		gotMime        string
		gotCookie      string
		gotReferer     string
		gotOrigin      string
		gotFilename    string
		gotContentType string
		gotBody        string
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMime = r.URL.Query().Get("mime")
		gotCookie = r.Header.Get("Cookie")
		gotReferer = r.Header.Get("Referer")
		gotOrigin = r.Header.Get("Origin")
		if r.Method != http.MethodPost {
			t.Errorf("请求方法 = %s, 期望 POST", r.Method)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("解析 multipart 失败: %v", err)
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Errorf("缺少 file 字段: %v", err)
			return
		}
		defer file.Close()
		gotFilename = header.Filename
		gotContentType = header.Header.Get("Content-Type")
		data, _ := io.ReadAll(file)
		gotBody = string(data)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"msg":"上传成功","data":{"url":"/assets/cache/user/1001/ticket/abc.png","upload_id":1}}`))
	}))
	defer server.Close()

	adapter := newTestAdapter(t, server.URL+"/shop", "USER_SESSION=eyJhbGciOiJIUzI1NiJ9.eyJ1aWQiOjF9.sig; ACG-SHOP=session; ACG_CSP=nonce")
	result, err := adapter.Put(context.Background(), "ab/cd/hash.png", strings.NewReader("image-bytes"), int64(len("image-bytes")), "image/png")
	if err != nil {
		t.Fatalf("Put 失败: %v", err)
	}
	if gotPath != "/shop/user/api/ticket/upload" {
		t.Errorf("请求路径 = %q, 期望 /shop/user/api/ticket/upload", gotPath)
	}
	if gotMime != "image" {
		t.Errorf("mime 参数 = %q, 期望 image", gotMime)
	}
	if gotCookie != "USER_SESSION=eyJhbGciOiJIUzI1NiJ9.eyJ1aWQiOjF9.sig; ACG-SHOP=session; ACG_CSP=nonce" {
		t.Errorf("Cookie = %q", gotCookie)
	}
	if gotReferer != server.URL+"/shop/" {
		t.Errorf("Referer = %q, 期望 %q", gotReferer, server.URL+"/shop/")
	}
	if gotOrigin != server.URL {
		t.Errorf("Origin = %q, 期望 %q", gotOrigin, server.URL)
	}
	if gotFilename != "hash.png" {
		t.Errorf("上传文件名 = %q, 期望 hash.png", gotFilename)
	}
	if gotContentType != "image/png" {
		t.Errorf("文件 Content-Type = %q", gotContentType)
	}
	if gotBody != "image-bytes" {
		t.Errorf("上传内容 = %q", gotBody)
	}
	wantURL := server.URL + "/assets/cache/user/1001/ticket/abc.png"
	if result.URL != wantURL {
		t.Errorf("直链 = %q, 期望 %q", result.URL, wantURL)
	}
	if result.ObjectKey != "" {
		t.Errorf("ObjectKey = %q, 期望为空并由直链推导", result.ObjectKey)
	}
}

func TestPutAcceptsStringAndNestedData(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "data 字符串相对路径",
			body: `{"code":200,"data":"assets/cache/user/2/ticket/a.webp"}`,
			want: "/shop/assets/cache/user/2/ticket/a.webp",
		},
		{
			name: "data.path",
			body: `{"code":200,"data":{"path":"/assets/cache/user/2/ticket/a.webp"}}`,
			want: "/assets/cache/user/2/ticket/a.webp",
		},
		{
			name: "顶层 url 兼容",
			body: `{"code":200,"url":"https://cdn.example.com/a.webp"}`,
			want: "https://cdn.example.com/a.webp",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, testCase.body)
			}))
			defer server.Close()
			adapter := newTestAdapter(t, server.URL+"/shop", "USER_SESSION=x")
			result, err := adapter.Put(context.Background(), "hash.webp", strings.NewReader("data"), 4, "image/webp")
			if err != nil {
				t.Fatalf("Put 失败: %v", err)
			}
			want := testCase.want
			if strings.HasPrefix(want, "/") {
				want = server.URL + want
			}
			if result.URL != want {
				t.Errorf("直链 = %q, 期望 %q", result.URL, want)
			}
		})
	}
}

func TestPutReturnsBackendCodeError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"code":0,"msg":"登录会话过期，请重新登录.."}`)
	}))
	defer server.Close()

	adapter := newTestAdapter(t, server.URL, "USER_SESSION=x")
	_, err := adapter.Put(context.Background(), "hash.png", strings.NewReader("data"), 4, "image/png")
	if err == nil || !strings.Contains(err.Error(), "登录会话过期") {
		t.Fatalf("错误 = %v, 期望包含后端错误信息", err)
	}
}

func TestPutRejectsInvalidResponses(t *testing.T) {
	for name, body := range map[string]string{
		"非 JSON": "<html>error</html>",
		"无地址":    `{"code":200,"data":{}}`,
		"无效直链":   `{"code":200,"data":{"url":"javascript:alert(1)"}}`,
		"协议相对直链": `{"code":200,"data":{"url":"//cdn.example.com/a.png"}}`,
		"直链缺少路径": `{"code":200,"data":{"url":"https://cdn.example.com"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, body)
			}))
			defer server.Close()
			adapter := newTestAdapter(t, server.URL, "USER_SESSION=x")
			if _, err := adapter.Put(context.Background(), "hash.png", strings.NewReader("data"), 4, "image/png"); err == nil {
				t.Fatal("无效响应应返回错误")
			}
		})
	}
}

func TestPutRejectsHTTPErrorAndInvalidInput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer server.Close()

	adapter := newTestAdapter(t, server.URL, "USER_SESSION=x")
	if _, err := adapter.Put(context.Background(), "hash.png", strings.NewReader("data"), 4, "image/png"); err == nil {
		t.Fatal("HTTP 403 应返回错误")
	}
	if _, err := adapter.Put(context.Background(), "hash.png", strings.NewReader(""), 0, "image/png"); err == nil {
		t.Fatal("空内容应被拒绝")
	}
	if _, err := adapter.Put(context.Background(), "hash.png", strings.NewReader("data"), 4, "text/plain"); err == nil {
		t.Fatal("非图片 Content-Type 应被拒绝")
	}
	if _, err := adapter.Put(context.Background(), "hash.png", strings.NewReader("data"), maxFileSize+1, "image/png"); err == nil {
		t.Fatal("超过驱动上限应被拒绝")
	}
}

func TestTestUploadsProbe(t *testing.T) {
	var gotFilename string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("解析 multipart 失败: %v", err)
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Errorf("缺少 file 字段: %v", err)
			return
		}
		defer file.Close()
		gotFilename = header.Filename
		data, _ := io.ReadAll(file)
		if string(data) != string(probePNG) {
			t.Error("探测内容不是内置 PNG")
		}
		_, _ = io.WriteString(w, `{"code":200,"data":{"url":"/assets/probe.png"}}`)
	}))
	defer server.Close()

	adapter := newTestAdapter(t, server.URL, "USER_SESSION=x")
	if err := adapter.Test(context.Background(), "picsky"); err != nil {
		t.Fatalf("Test 失败: %v", err)
	}
	if !strings.HasPrefix(gotFilename, "picsky-probe-") || !strings.HasSuffix(gotFilename, ".png") {
		t.Errorf("探测文件名 = %q", gotFilename)
	}
}

func TestUnsupportedCapabilities(t *testing.T) {
	adapter := newTestAdapter(t, "https://example.com", "USER_SESSION=x")
	if _, err := adapter.Stat(context.Background(), "hash.png"); err != storage.ErrNotSupported {
		t.Errorf("Stat 能力 = %v, 期望 ErrNotSupported", err)
	}
	if err := adapter.Delete(context.Background(), "hash.png"); err != storage.ErrNotSupported {
		t.Errorf("Delete 能力 = %v, 期望 ErrNotSupported", err)
	}
	if _, err := adapter.List(context.Background(), ""); err != storage.ErrNotSupported {
		t.Errorf("List 能力 = %v, 期望 ErrNotSupported", err)
	}
	if _, err := adapter.Read(context.Background(), "hash.png"); err != storage.ErrNotSupported {
		t.Errorf("Read 能力 = %v, 期望 ErrNotSupported", err)
	}
}

func TestLoginPasswordSetsCookieAndReadsExpiry(t *testing.T) {
	var gotPath, gotReferer, gotOrigin, gotUsername, gotPassword string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/shop/user/api/security/deviceSessions" {
			_, _ = io.WriteString(w, `{"code":200,"data":{"list":[{"current":true,"expires_time":"2026-12-01 10:00:00"}]}}`)
			return
		}
		gotPath = r.URL.Path
		gotReferer = r.Header.Get("Referer")
		gotOrigin = r.Header.Get("Origin")
		if err := r.ParseForm(); err != nil {
			t.Errorf("解析表单失败: %v", err)
		}
		gotUsername = r.Form.Get("username")
		gotPassword = r.Form.Get("password")
		http.SetCookie(w, &http.Cookie{
			Name:   "USER_SESSION",
			Value:  "session-token",
			Path:   "/",
			MaxAge: 30 * 24 * 60 * 60,
		})
		_, _ = io.WriteString(w, `{"code":200,"msg":"登录成功"}`)
	}))
	defer server.Close()

	result, err := LoginPassword(context.Background(), server.URL+"/shop", "user@example.com", "secret")
	if err != nil {
		t.Fatalf("LoginPassword 失败: %v", err)
	}
	if gotPath != "/shop/user/api/authentication/login" {
		t.Errorf("登录路径 = %q", gotPath)
	}
	if gotReferer != server.URL+"/shop/" || gotOrigin != server.URL {
		t.Errorf("Referer/Origin = %q / %q", gotReferer, gotOrigin)
	}
	if gotUsername != "user@example.com" || gotPassword != "secret" {
		t.Errorf("登录表单 = %q / %q", gotUsername, gotPassword)
	}
	if !strings.Contains(result.Cookie, "USER_SESSION=session-token") {
		t.Errorf("Cookie = %q", result.Cookie)
	}
	if result.CookieExpires == "" {
		t.Error("登录后应返回 Cookie 到期时间")
	}
}

func TestLoginPasswordRequiresTwoFactor(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "ACG-SHOP", Value: "pending-session", Path: "/"})
		_, _ = io.WriteString(w, `{"code":42001,"msg":"该账号已开启两步验证，请输入验证码"}`)
	}))
	defer server.Close()

	result, err := LoginPassword(context.Background(), server.URL, "user", "secret")
	if err != nil {
		t.Fatalf("LoginPassword 失败: %v", err)
	}
	if !result.TwoFactor {
		t.Fatal("应返回两步验证状态")
	}
	if !strings.Contains(result.SessionCookie, "ACG-SHOP=pending-session") {
		t.Errorf("两步验证会话 Cookie = %q", result.SessionCookie)
	}
}

func TestCompleteTwoFactor(t *testing.T) {
	var gotCookie string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/user/api/security/deviceSessions" {
			_, _ = io.WriteString(w, `{"code":200,"data":{"list":[{"current":true,"expires_time":"2026-12-01 10:00:00"}]}}`)
			return
		}
		gotCookie = r.Header.Get("Cookie")
		if r.URL.Path != "/user/api/authentication/totp" {
			t.Errorf("两步验证路径 = %q", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("解析表单失败: %v", err)
		}
		if r.Form.Get("code") != "123456" {
			t.Errorf("动态码 = %q", r.Form.Get("code"))
		}
		http.SetCookie(w, &http.Cookie{Name: "USER_SESSION", Value: "totp-token", Path: "/", MaxAge: 3600})
		_, _ = io.WriteString(w, `{"code":200,"msg":"登录成功"}`)
	}))
	defer server.Close()

	result, err := CompleteTwoFactor(context.Background(), server.URL, "ACG-SHOP=pending-session", "123456")
	if err != nil {
		t.Fatalf("CompleteTwoFactor 失败: %v", err)
	}
	if !strings.Contains(gotCookie, "ACG-SHOP=pending-session") {
		t.Errorf("两步验证请求 Cookie = %q", gotCookie)
	}
	if !strings.Contains(result.Cookie, "USER_SESSION=totp-token") {
		t.Errorf("登录 Cookie = %q", result.Cookie)
	}
}

func TestSessionExpiry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/shop/user/api/security/deviceSessions" {
			t.Errorf("路径 = %q", r.URL.Path)
		}
		if !strings.Contains(r.Header.Get("Cookie"), "USER_SESSION=abc") {
			t.Errorf("Cookie = %q", r.Header.Get("Cookie"))
		}
		if !strings.HasSuffix(r.Header.Get("Referer"), "/shop/user/security/device") {
			t.Errorf("Referer = %q", r.Header.Get("Referer"))
		}
		_, _ = io.WriteString(w, `{"code":200,"data":{"list":[{"current":true,"expires_time":"2026-12-01 10:00:00"}]}}`)
	}))
	defer server.Close()

	expires, err := SessionExpiry(context.Background(), server.URL+"/shop", "USER_SESSION=abc")
	if err != nil {
		t.Fatalf("SessionExpiry 失败: %v", err)
	}
	if expires != "2026-12-01 10:00:00" {
		t.Errorf("到期时间 = %q", expires)
	}
}

func TestPutRefreshesExpiredSession(t *testing.T) {
	var uploadCalls, loginCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user/api/authentication/login":
			loginCalls++
			http.SetCookie(w, &http.Cookie{Name: "USER_SESSION", Value: "fresh", Path: "/", MaxAge: 3600})
			_, _ = io.WriteString(w, `{"code":200,"msg":"登录成功"}`)
		case "/user/api/security/deviceSessions":
			_, _ = io.WriteString(w, `{"code":200,"data":{"list":[{"current":true,"expires_time":"2026-12-01 10:00:00"}]}}`)
		case "/user/api/ticket/upload":
			uploadCalls++
			if strings.Contains(r.Header.Get("Cookie"), "USER_SESSION=fresh") {
				_, _ = io.WriteString(w, `{"code":200,"data":{"url":"/assets/fresh.png"}}`)
				return
			}
			_, _ = io.WriteString(w, `{"code":0,"msg":"登录会话过期，请重新登录.."}`)
		default:
			t.Errorf("未知路径: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	created, err := newAdapter(storage.Options{
		Config: map[string]string{"endpoint": server.URL, "auto_login": "true"},
		Secrets: map[string]string{
			"cookie":   "USER_SESSION=old",
			"username": "user",
			"password": "secret",
		},
	})
	if err != nil {
		t.Fatalf("创建适配器失败: %v", err)
	}
	result, err := created.Put(context.Background(), "hash.png", strings.NewReader("data"), 4, "image/png")
	if err != nil {
		t.Fatalf("Put 失败: %v", err)
	}
	if uploadCalls != 2 || loginCalls != 1 {
		t.Errorf("上传次数 = %d, 登录次数 = %d, 期望各 2/1", uploadCalls, loginCalls)
	}
	if result.URL != server.URL+"/assets/fresh.png" {
		t.Errorf("直链 = %q", result.URL)
	}
}
