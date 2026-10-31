package api_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pquerna/otp/totp"
	"golang.org/x/net/webdav"
	"gorm.io/gorm"

	"github.com/yeqingky/Picsky/internal/api"
	"github.com/yeqingky/Picsky/internal/config"
	"github.com/yeqingky/Picsky/internal/db"
	"github.com/yeqingky/Picsky/internal/model"
	"github.com/yeqingky/Picsky/internal/service"
	"github.com/yeqingky/Picsky/migrations"

	_ "github.com/yeqingky/Picsky/internal/storage/webdav"
)

// server 是测试用的 HTTP 服务.
type server struct {
	t      *testing.T
	engine *gin.Engine
	http   *httptest.Server
	db     *gorm.DB
	svc    *service.Services
	davDir string
}

// newServer 启动完整的 API 服务 (含兼容层).
func newServer(t *testing.T) *server {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{
		SecretKey:         bytes.Repeat([]byte{0x51}, 32),
		DBDriver:          "sqlite",
		DBDSN:             filepath.Join(dir, "api.db"),
		Listen:            "127.0.0.1:0",
		APIPrefix:         "/api/v1",
		LskyCompatEnabled: true,
		LskyCompatPrefix:  "/lsky",
		TempDir:           filepath.Join(dir, "tmp"),
		UploadMemoryLimit: 1 << 20,
		LogLevel:          "error",
	}
	if err := os.MkdirAll(cfg.TempDir, 0o750); err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}

	database, err := db.Open(cfg)
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { db.Close(database) })
	if err := migrations.Run(database); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	services, err := service.New(cfg, database, logger)
	if err != nil {
		t.Fatalf("装配服务失败: %v", err)
	}

	davDir := filepath.Join(dir, "dav")
	if err := os.MkdirAll(davDir, 0o755); err != nil {
		t.Fatalf("创建 WebDAV 目录失败: %v", err)
	}
	davServer := httptest.NewServer(&webdav.Handler{
		Prefix:     "/dav",
		FileSystem: webdav.Dir(davDir),
		LockSystem: webdav.NewMemLS(),
	})
	t.Cleanup(davServer.Close)
	cfg.DBDSN = davServer.URL + "/dav"

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	api.New(services, cfg, logger).Register(engine)

	httpServer := httptest.NewServer(engine)
	t.Cleanup(httpServer.Close)

	return &server{t: t, engine: engine, http: httpServer, db: database, svc: services, davDir: davDir}
}

// response 是请求结果.
type response struct {
	Status int
	Body   []byte
	Header http.Header
}

type countingReadCloser struct {
	reader    *bytes.Reader
	bytesRead int64
}

func (r *countingReadCloser) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.bytesRead += int64(n)
	return n, err
}

func (r *countingReadCloser) Close() error { return nil }

// raw 发送请求.
func (s *server) raw(method, path string, body io.Reader, headers map[string]string, cookies []*http.Cookie) *response {
	s.t.Helper()
	request, err := http.NewRequest(method, s.http.URL+path, body)
	if err != nil {
		s.t.Fatalf("构造请求失败: %v", err)
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	result, err := s.http.Client().Do(request)
	if err != nil {
		s.t.Fatalf("请求失败: %v", err)
	}
	defer result.Body.Close()
	payload, err := io.ReadAll(result.Body)
	if err != nil {
		s.t.Fatalf("读取响应失败: %v", err)
	}
	return &response{Status: result.StatusCode, Body: payload, Header: result.Header}
}

// json 发送 JSON 请求.
func (s *server) json(method, path string, payload any, cookies []*http.Cookie) *response {
	s.t.Helper()
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			s.t.Fatalf("序列化请求失败: %v", err)
		}
		body = bytes.NewReader(encoded)
	}
	return s.raw(method, path, body, map[string]string{"Content-Type": "application/json"}, cookies)
}

// bearer 发送带 API 密钥的请求.
func (s *server) bearer(method, path string, payload any, token string) *response {
	s.t.Helper()
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			s.t.Fatalf("序列化请求失败: %v", err)
		}
		body = bytes.NewReader(encoded)
	}
	return s.raw(method, path, body, map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer " + token,
	}, nil)
}

// decode 解析响应 JSON.
func (s *server) decode(result *response) map[string]any {
	s.t.Helper()
	var parsed map[string]any
	if err := json.Unmarshal(result.Body, &parsed); err != nil {
		s.t.Fatalf("解析响应失败: %v (%s)", err, string(result.Body))
	}
	return parsed
}

// sessionCookie 返回注册/登录响应中的会话 cookie.
func (s *server) sessionCookie(result *response) *http.Cookie {
	s.t.Helper()
	for _, cookie := range result.Header.Values("Set-Cookie") {
		if strings.HasPrefix(cookie, "picsky_session=") {
			value := strings.TrimPrefix(strings.Split(cookie, ";")[0], "picsky_session=")
			return &http.Cookie{Name: "picsky_session", Value: value}
		}
	}
	s.t.Fatalf("响应未下发会话 cookie: %v", result.Header.Values("Set-Cookie"))
	return nil
}

// smtpCaptureServer 接收一次纯文本 SMTP 邮件, 用于认证流程测试.
func smtpCaptureServer(t *testing.T) (string, <-chan string) {
	return smtpCaptureServerWithGate(t, nil, nil)
}

func smtpCaptureServerWithGate(t *testing.T, accepted chan<- struct{}, release <-chan struct{}) (string, <-chan string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("启动 SMTP 测试服务失败: %v", err)
	}
	messages := make(chan string, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		if accepted != nil {
			close(accepted)
		}
		if release != nil {
			<-release
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
		rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
		reply := func(value string) bool {
			if _, err := rw.WriteString(value); err != nil {
				return false
			}
			return rw.Flush() == nil
		}
		if !reply("220 localhost ESMTP\r\n") {
			return
		}
		for {
			line, err := rw.ReadString('\n')
			if err != nil {
				return
			}
			command := strings.ToUpper(strings.TrimSpace(line))
			switch {
			case strings.HasPrefix(command, "EHLO "), strings.HasPrefix(command, "HELO "):
				if !reply("250-localhost\r\n250 OK\r\n") {
					return
				}
			case strings.HasPrefix(command, "MAIL FROM:"), strings.HasPrefix(command, "RCPT TO:"):
				if !reply("250 OK\r\n") {
					return
				}
			case command == "DATA":
				if !reply("354 End data with <CR><LF>.<CR><LF>\r\n") {
					return
				}
				var message strings.Builder
				for {
					dataLine, err := rw.ReadString('\n')
					if err != nil {
						return
					}
					if dataLine == ".\r\n" || dataLine == ".\n" {
						break
					}
					message.WriteString(dataLine)
				}
				messages <- message.String()
				if !reply("250 queued\r\n") {
					return
				}
			case command == "QUIT":
				_ = reply("221 bye\r\n")
				return
			default:
				if !reply("250 OK\r\n") {
					return
				}
			}
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("SMTP 测试服务未及时停止")
		}
	})
	return strconv.Itoa(listener.Addr().(*net.TCPAddr).Port), messages
}

// multipartBody 构造上传请求体.
func multipartBody(t *testing.T, filename string, content []byte, fields map[string]string) (*bytes.Buffer, string) {
	t.Helper()
	buffer := &bytes.Buffer{}
	writer := multipart.NewWriter(buffer)
	if token, ok := fields["token"]; ok {
		if err := writer.WriteField("token", token); err != nil {
			t.Fatalf("写入 token 字段失败: %v", err)
		}
	}
	for key, value := range fields {
		if key == "token" {
			continue
		}
		if err := writer.WriteField(key, value); err != nil {
			t.Fatalf("写入表单字段失败: %v", err)
		}
	}
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("创建文件字段失败: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("写入文件内容失败: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("关闭 multipart 失败: %v", err)
	}
	return buffer, writer.FormDataContentType()
}

// upload 上传图片.
func (s *server) upload(path string, filename string, content []byte, fields map[string]string, headers map[string]string, cookies []*http.Cookie) *response {
	s.t.Helper()
	body, contentType := multipartBody(s.t, filename, content, fields)
	headers["Content-Type"] = contentType
	return s.raw(http.MethodPost, path, body, headers, cookies)
}

func (s *server) countedUpload(path, filename string, content []byte, fields map[string]string, cookies []*http.Cookie) (*response, int64, int) {
	s.t.Helper()
	body, contentType := multipartBody(s.t, filename, content, fields)
	countedBody := &countingReadCloser{reader: bytes.NewReader(body.Bytes())}
	request := httptest.NewRequest(http.MethodPost, path, countedBody)
	request.ContentLength = int64(body.Len())
	request.Header.Set("Content-Type", contentType)
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	s.engine.ServeHTTP(recorder, request)
	return &response{
		Status: recorder.Code,
		Body:   recorder.Body.Bytes(),
		Header: recorder.Header(),
	}, countedBody.bytesRead, body.Len()
}

// pngContent 生成 PNG 内容.
func pngContent(t *testing.T, seed int) []byte {
	t.Helper()
	canvas := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for x := range 8 {
		for y := range 8 {
			canvas.Set(x, y, color.RGBA{R: uint8(x + seed), G: uint8(y + seed), B: uint8(seed), A: 255})
		}
	}
	buffer := &bytes.Buffer{}
	if err := png.Encode(buffer, canvas); err != nil {
		t.Fatalf("编码 PNG 失败: %v", err)
	}
	return buffer.Bytes()
}

// base64Std 返回标准 base64 编码.
func base64Std(t *testing.T, payload []byte) string {
	t.Helper()
	return base64.StdEncoding.EncodeToString(payload)
}

// bootstrap 注册管理员并创建策略, 返回会话 cookie 与策略 ID.
func (s *server) bootstrap() (*http.Cookie, int64) {
	s.t.Helper()
	_, cookie := s.registerAndLogin("admin", "admin@example.com", "password123")

	created := s.json(http.MethodPost, "/api/v1/admin/policies", map[string]any{
		"name":            "主存储",
		"type":            "webdav",
		"enabled":         true,
		"path_template":   "{md5:2}/{md5}.{ext}",
		"conflict_policy": "rename",
		"config":          map[string]string{"endpoint": s.svc.Cfg.DBDSN},
		"urls": []map[string]any{
			{"alias": "融合CDN", "url": "https://cdn.example.com/image/", "is_default": true},
			{"alias": "高速CDN", "url": "https://ucdn.example.com/image/"},
		},
	}, []*http.Cookie{cookie})
	if created.Status != http.StatusCreated {
		s.t.Fatalf("创建策略失败: %d %s", created.Status, string(created.Body))
	}
	policy := s.decode(created)
	return cookie, int64(policy["id"].(float64))
}

func (s *server) registerAndLogin(username, email, password string) (*response, *http.Cookie) {
	s.t.Helper()
	registered := s.json(http.MethodPost, "/api/v1/auth/register", map[string]any{
		"username": username,
		"email":    email,
		"password": password,
	}, nil)
	if registered.Status != http.StatusAccepted {
		s.t.Fatalf("注册请求失败: %d %s", registered.Status, string(registered.Body))
	}
	login := s.json(http.MethodPost, "/api/v1/auth/login", map[string]any{
		"identifier": email,
		"password":   password,
	}, nil)
	if login.Status != http.StatusOK {
		s.t.Fatalf("注册后登录失败: %d %s", login.Status, string(login.Body))
	}
	return login, s.sessionCookie(login)
}

func TestRegistrationResponsesDoNotRevealDuplicateAccounts(t *testing.T) {
	s := newServer(t)
	payloads := []map[string]any{
		{"username": "enumuser", "email": "enum@example.com", "password": "password123"},
		{"username": "enumuser", "email": "another@example.com", "password": "password123"},
		{"username": "anotheruser", "email": "ENUM@EXAMPLE.COM", "password": "password123"},
	}
	var first *response
	for index, payload := range payloads {
		result := s.json(http.MethodPost, "/api/v1/auth/register", payload, nil)
		if result.Status != http.StatusAccepted {
			t.Fatalf("注册请求 %d 应返回通用 202, 实际 %d %s", index, result.Status, string(result.Body))
		}
		if result.Header.Get("Set-Cookie") != "" {
			t.Fatalf("注册请求 %d 不应下发会话 Cookie", index)
		}
		if index == 0 {
			first = result
			continue
		}
		if string(result.Body) != string(first.Body) {
			t.Fatalf("新注册与重复注册响应不一致: 首次=%s, 重复=%s", string(first.Body), string(result.Body))
		}
	}

	login := s.json(http.MethodPost, "/api/v1/auth/login", map[string]any{
		"identifier": "ENUM@EXAMPLE.COM",
		"password":   "password123",
	}, nil)
	if login.Status != http.StatusOK {
		t.Fatalf("无注册会话时仍应可用邮箱登录: %d %s", login.Status, string(login.Body))
	}
}

func TestUnverifiedRegistrationUsesReadOnlyGroupUntilEmailVerification(t *testing.T) {
	s := newServer(t)
	_, policyID := s.bootstrap()
	smtpAccepted := make(chan struct{})
	release := make(chan struct{})
	released := false
	releaseSMTP := func() {
		if !released {
			close(release)
			released = true
		}
	}
	smtpPort, messages := smtpCaptureServerWithGate(t, smtpAccepted, release)
	t.Cleanup(releaseSMTP)
	if err := s.svc.Settings.Update(map[string]string{
		"email_verification_enabled": "true",
		"smtp_host":                  "127.0.0.1",
		"smtp_port":                  smtpPort,
		"smtp_from":                  "noreply@example.com",
		"smtp_encryption":            "none",
		"site_url":                   "https://picsky.example",
	}); err != nil {
		t.Fatalf("配置邮箱验证与 SMTP 失败: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s.svc.Mailer.Start(ctx)

	payload, err := json.Marshal(map[string]string{
		"username": "pendinguser",
		"email":    "pending@example.com",
		"password": "password123",
	})
	if err != nil {
		t.Fatalf("编码注册请求失败: %v", err)
	}
	request, err := http.NewRequest(http.MethodPost, s.http.URL+"/api/v1/auth/register", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("构造注册请求失败: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	type registrationResult struct {
		status  int
		headers http.Header
		body    []byte
		err     error
	}
	results := make(chan registrationResult, 1)
	go func() {
		response, err := s.http.Client().Do(request)
		if err != nil {
			results <- registrationResult{err: err}
			return
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		results <- registrationResult{status: response.StatusCode, headers: response.Header, body: body, err: err}
	}()
	select {
	case <-smtpAccepted:
	case <-time.After(5 * time.Second):
		t.Fatal("验证邮件没有进入 SMTP worker")
	}
	var registered registrationResult
	select {
	case registered = <-results:
	case <-time.After(2 * time.Second):
		releaseSMTP()
		select {
		case <-results:
		case <-time.After(5 * time.Second):
		}
		t.Fatal("注册请求等待 SMTP 网络发送完成")
	}
	if registered.err != nil {
		t.Fatalf("注册请求失败: %v", registered.err)
	}
	if registered.status != http.StatusAccepted || registered.headers.Get("Set-Cookie") != "" {
		t.Fatalf("注册应返回通用 202 且不下发会话: %d %s", registered.status, string(registered.body))
	}
	releaseSMTP()

	var message string
	select {
	case message = <-messages:
	case <-time.After(5 * time.Second):
		t.Fatal("异步验证邮件未发送")
	}
	const verificationLink = "https://picsky.example/verify-email?token="
	tokenStart := strings.Index(message, verificationLink)
	if tokenStart < 0 {
		t.Fatalf("验证邮件缺少链接: %s", message)
	}
	token := strings.Fields(message[tokenStart+len(verificationLink):])[0]

	var registeredUser model.User
	if err := s.db.Where("email = ?", "pending@example.com").First(&registeredUser).Error; err != nil {
		t.Fatalf("读取未验证用户失败: %v", err)
	}
	if registeredUser.Status != model.UserStatusUnverified || registeredUser.EmailVerifiedAt != nil || registeredUser.RestoreGroupID == nil {
		t.Fatalf("注册用户应保持未验证并记录恢复用户组: %+v", registeredUser)
	}
	var readOnly model.Group
	if err := s.db.Where("name = ?", "只读").First(&readOnly).Error; err != nil {
		t.Fatalf("读取只读用户组失败: %v", err)
	}
	if registeredUser.GroupID != readOnly.ID {
		t.Fatalf("未验证用户应进入只读组, 实际组 ID %d", registeredUser.GroupID)
	}

	login := s.json(http.MethodPost, "/api/v1/auth/login", map[string]any{
		"identifier": "pending@example.com",
		"password":   "password123",
	}, nil)
	if login.Status != http.StatusOK {
		t.Fatalf("未验证用户应可登录以完成验证: %d %s", login.Status, string(login.Body))
	}
	cookie := s.sessionCookie(login)
	me := s.raw(http.MethodGet, "/api/v1/auth/me", nil, nil, []*http.Cookie{cookie})
	if me.Status != http.StatusOK || s.decode(me)["group"].(map[string]any)["name"] != "只读" {
		t.Fatalf("未验证用户应使用只读组: %d %s", me.Status, string(me.Body))
	}
	upload := s.upload("/api/v1/upload", "pending.png", pngContent(t, 101), map[string]string{
		"policy_id": fmt.Sprint(policyID),
	}, map[string]string{}, []*http.Cookie{cookie})
	if upload.Status != http.StatusForbidden {
		t.Fatalf("未验证用户上传应被拒绝: %d %s", upload.Status, string(upload.Body))
	}

	verified := s.json(http.MethodPost, "/api/v1/auth/verify-email", map[string]string{"token": token}, nil)
	if verified.Status != http.StatusNoContent {
		t.Fatalf("邮箱验证失败: %d %s", verified.Status, string(verified.Body))
	}
	var activeUser model.User
	if err := s.db.First(&activeUser, registeredUser.ID).Error; err != nil {
		t.Fatalf("读取已验证用户失败: %v", err)
	}
	if activeUser.Status != model.UserStatusNormal || activeUser.EmailVerifiedAt == nil || activeUser.GroupID != *registeredUser.RestoreGroupID || activeUser.RestoreGroupID != nil {
		t.Fatalf("邮箱验证后应恢复正常状态与原用户组: %+v", activeUser)
	}
}

func TestResendVerificationRateLimitAPI(t *testing.T) {
	s := newServer(t)
	if err := s.svc.Settings.Update(map[string]string{
		"email_verification_enabled": "true",
		"smtp_host":                  "smtp.example.com",
		"smtp_from":                  "noreply@example.com",
	}); err != nil {
		t.Fatalf("配置邮箱验证与 SMTP 失败: %v", err)
	}
	registration := s.json(http.MethodPost, "/api/v1/auth/register", map[string]string{
		"username": "resendUser",
		"email":    "resend@example.com",
		"password": "password123",
	}, nil)
	if registration.Status != http.StatusAccepted {
		t.Fatalf("注册未验证账号失败: %d %s", registration.Status, string(registration.Body))
	}
	login := s.json(http.MethodPost, "/api/v1/auth/login", map[string]string{
		"identifier": "resend@example.com",
		"password":   "password123",
	}, nil)
	if login.Status != http.StatusOK {
		t.Fatalf("未验证账号登录失败: %d %s", login.Status, string(login.Body))
	}
	cookie := s.sessionCookie(login)
	for attempt := range 3 {
		resend := s.json(http.MethodPost, "/api/v1/auth/resend-verification", nil, []*http.Cookie{cookie})
		if resend.Status != http.StatusAccepted {
			t.Fatalf("第 %d 次重发失败: %d %s", attempt+1, resend.Status, string(resend.Body))
		}
	}
	limited := s.json(http.MethodPost, "/api/v1/auth/resend-verification", nil, []*http.Cookie{cookie})
	if limited.Status != http.StatusTooManyRequests {
		t.Fatalf("第 4 次重发应返回 429, 实际 %d %s", limited.Status, string(limited.Body))
	}
}

func TestClientIPSourceDiscoveryAndLoginRateLimit(t *testing.T) {
	s := newServer(t)

	unauthorized := s.raw(http.MethodGet, "/api/v1/admin/settings/client-ip-sources", nil, nil, nil)
	if unauthorized.Status != http.StatusUnauthorized {
		t.Fatalf("IP 来源探测接口必须认证, 实际状态码 %d", unauthorized.Status)
	}

	_, cookie := s.registerAndLogin("admin", "admin@example.com", "password123")

	sourcesResponse := s.raw(http.MethodGet, "/api/v1/admin/settings/client-ip-sources", nil, map[string]string{
		"X-Forwarded-For": "203.0.113.10, 10.0.0.1",
		"X-Real-IP":       "198.51.100.10",
	}, []*http.Cookie{cookie})
	if sourcesResponse.Status != http.StatusOK {
		t.Fatalf("读取 IP 来源失败: %d %s", sourcesResponse.Status, string(sourcesResponse.Body))
	}
	var sources []struct {
		Source string `json:"source"`
		IP     string `json:"ip"`
	}
	if err := json.Unmarshal(sourcesResponse.Body, &sources); err != nil {
		t.Fatalf("解析 IP 来源失败: %v", err)
	}
	found := make(map[string]string, len(sources))
	for _, source := range sources {
		found[source.Source] = source.IP
	}
	if found["REMOTE_ADDR"] != "127.0.0.1" ||
		found["HTTP_X_FORWARDED_FOR"] != "203.0.113.10" ||
		found["HTTP_X_REAL_IP"] != "198.51.100.10" {
		t.Fatalf("探测接口未返回预期来源: %#v", found)
	}

	invalidSetting := s.json(http.MethodPatch, "/api/v1/admin/settings", map[string]string{
		"client_ip_source": "HTTP_X-FORWARDED-FOR",
	}, []*http.Cookie{cookie})
	if invalidSetting.Status != http.StatusBadRequest {
		t.Fatalf("非法 IP 来源应被拒绝, 实际状态码 %d", invalidSetting.Status)
	}

	loginBody, err := json.Marshal(map[string]string{"identifier": "missing-user@example.com", "password": "invalid"})
	if err != nil {
		t.Fatalf("编码登录请求失败: %v", err)
	}
	loginHeaders := func(forwarded, realIP string) map[string]string {
		return map[string]string{
			"Content-Type":    "application/json",
			"X-Forwarded-For": forwarded,
			"X-Real-IP":       realIP,
		}
	}

	for attempt := range service.LoginFailureLimit {
		response := s.raw(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(loginBody), loginHeaders(fmt.Sprintf("203.0.113.%d", attempt+1), ""), nil)
		if response.Status != http.StatusUnauthorized {
			t.Fatalf("默认 socket 来源下第 %d 次失败登录应为 401, 实际 %d", attempt+1, response.Status)
		}
	}
	limited := s.raw(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(loginBody), loginHeaders("203.0.113.99", ""), nil)
	if limited.Status != http.StatusTooManyRequests {
		t.Fatalf("伪造不同 X-Forwarded-For 不应绕过来源限流, 实际状态码 %d", limited.Status)
	}

	var firstFailure struct {
		IP string
	}
	if err := s.db.Table("audit_logs").Select("ip").Where("action = ?", "login_failed").Order("id ASC").Take(&firstFailure).Error; err != nil {
		t.Fatalf("读取登录审计记录失败: %v", err)
	}
	if firstFailure.IP != "127.0.0.1" {
		t.Fatalf("默认审计 IP 应使用 socket 来源, 得到 %q", firstFailure.IP)
	}

	updated := s.json(http.MethodPatch, "/api/v1/admin/settings", map[string]string{
		"client_ip_source": "HTTP_X_REAL_IP",
	}, []*http.Cookie{cookie})
	if updated.Status != http.StatusOK {
		t.Fatalf("设置可信 IP 请求头失败: %d %s", updated.Status, string(updated.Body))
	}

	for attempt := range service.LoginFailureLimit {
		response := s.raw(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(loginBody), loginHeaders(fmt.Sprintf("203.0.113.%d", attempt+1), "198.51.100.20"), nil)
		if response.Status != http.StatusUnauthorized {
			t.Fatalf("选择 X-Real-IP 后第 %d 次失败登录应为 401, 实际 %d", attempt+1, response.Status)
		}
	}
	limited = s.raw(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(loginBody), loginHeaders("203.0.113.99", "198.51.100.20"), nil)
	if limited.Status != http.StatusTooManyRequests {
		t.Fatalf("未选中的 X-Forwarded-For 不应绕过限流, 实际状态码 %d", limited.Status)
	}

	var latestFailure struct {
		IP string
	}
	if err := s.db.Table("audit_logs").Select("ip").Where("action = ?", "login_failed").Order("id DESC").Take(&latestFailure).Error; err != nil {
		t.Fatalf("读取最新登录审计记录失败: %v", err)
	}
	if latestFailure.IP != "198.51.100.20" {
		t.Fatalf("审计 IP 应使用设置的 X-Real-IP, 得到 %q", latestFailure.IP)
	}
}

func TestSessionCookieSecureUsesForwardedProto(t *testing.T) {
	s := newServer(t)
	if siteURL := s.svc.Settings.String("site_url"); siteURL != "" {
		t.Fatalf("测试要求 site_url 默认为空, 得到 %q", siteURL)
	}
	register := s.json(http.MethodPost, "/api/v1/auth/register", map[string]string{
		"username": "proxycookie",
		"email":    "proxycookie@example.com",
		"password": "password123",
	}, nil)
	if register.Status != http.StatusAccepted {
		t.Fatalf("注册测试用户失败: %d %s", register.Status, string(register.Body))
	}

	loginRequest := func(headers map[string]string) *response {
		return s.raw(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"identifier":"proxycookie@example.com","password":"password123"}`), headers, nil)
	}
	setCookieHasSecure := func(result *response) bool {
		for _, value := range result.Header.Values("Set-Cookie") {
			if strings.HasPrefix(value, "picsky_session=") && strings.Contains(strings.ToLower(value), "; secure") {
				return true
			}
		}
		return false
	}

	httpProxy := loginRequest(map[string]string{
		"Content-Type":      "application/json",
		"X-Forwarded-Proto": "http",
	})
	if httpProxy.Status != http.StatusOK {
		t.Fatalf("HTTP 登录失败: %d %s", httpProxy.Status, string(httpProxy.Body))
	}
	if setCookieHasSecure(httpProxy) {
		t.Fatal("HTTP 代理请求不应设置 Secure")
	}

	httpsProxy := loginRequest(map[string]string{
		"Content-Type":      "application/json",
		"X-Forwarded-Proto": "https",
	})
	if httpsProxy.Status != http.StatusOK {
		t.Fatalf("HTTPS 代理登录失败: %d %s", httpsProxy.Status, string(httpsProxy.Body))
	}
	if !setCookieHasSecure(httpsProxy) {
		t.Fatalf("site_url 为空时 HTTPS 代理下会话 cookie 必须带 Secure: %v", httpsProxy.Header.Values("Set-Cookie"))
	}
}

func TestHealthAndAuthFlow(t *testing.T) {
	s := newServer(t)

	health := s.raw(http.MethodGet, "/api/v1/health", nil, nil, nil)
	if health.Status != http.StatusOK {
		t.Fatalf("health 状态码 %d", health.Status)
	}
	payload := s.decode(health)
	if payload["status"] != "ok" || payload["version"] == "" {
		t.Errorf("health 响应不正确: %v", payload)
	}

	captcha := s.raw(http.MethodGet, "/api/v1/auth/captcha", nil, nil, nil)
	if captcha.Status != http.StatusOK || s.decode(captcha)["provider"] != "none" {
		t.Errorf("人机验证公开配置不正确: %s", string(captcha.Body))
	}

	site := s.raw(http.MethodGet, "/api/v1/site", nil, nil, nil)
	if site.Status != http.StatusOK || s.decode(site)["password_reset_enabled"] != false {
		t.Errorf("未配置 SMTP 时找回密码应不可用: %s", string(site.Body))
	}
	if err := s.svc.Settings.Update(map[string]string{
		"smtp_host": "smtp.example.com",
		"smtp_from": "noreply@example.com",
	}); err != nil {
		t.Fatalf("配置 SMTP 失败: %v", err)
	}
	site = s.raw(http.MethodGet, "/api/v1/site", nil, nil, nil)
	if site.Status != http.StatusOK || s.decode(site)["password_reset_enabled"] != true {
		t.Errorf("配置 SMTP 后找回密码应可用: %s", string(site.Body))
	}
	if err := s.svc.Settings.Update(map[string]string{
		"registration_email_allowlist": " Example.com, corp.test ,example.com",
	}); err != nil {
		t.Fatalf("配置邮箱域名白名单失败: %v", err)
	}
	site = s.raw(http.MethodGet, "/api/v1/site", nil, nil, nil)
	domains, ok := s.decode(site)["registration_email_domains"].([]any)
	if !ok || len(domains) != 2 || domains[0] != "Example.com" || domains[1] != "corp.test" {
		t.Errorf("站点公开信息应返回去空去重的邮箱域名白名单: %s", string(site.Body))
	}
	if err := s.svc.Settings.Update(map[string]string{
		"registration_email_allowlist": "",
	}); err != nil {
		t.Fatalf("重置邮箱域名白名单失败: %v", err)
	}

	unauthorized := s.raw(http.MethodGet, "/api/v1/images", nil, nil, nil)
	if unauthorized.Status != http.StatusUnauthorized {
		t.Errorf("未认证访问图片列表应返回 401, 实际 %d", unauthorized.Status)
	}

	cookie, _ := s.bootstrap()

	me := s.raw(http.MethodGet, "/api/v1/auth/me", nil, nil, []*http.Cookie{cookie})
	if me.Status != http.StatusOK {
		t.Fatalf("读取当前用户失败: %d %s", me.Status, string(me.Body))
	}
	profile := s.decode(me)
	if profile["username"] != "admin" {
		t.Errorf("当前用户不正确: %v", profile)
	}
	permissions, ok := profile["permissions"].([]any)
	if !ok || len(permissions) != 10 {
		t.Errorf("管理员应具备全部权限位: %v", profile["permissions"])
	}
	group, ok := profile["group"].(map[string]any)
	if !ok || group["name"] != "管理员" {
		t.Errorf("首个注册账号应进入管理员组: %v", profile["group"])
	}

	login := s.json(http.MethodPost, "/api/v1/auth/login", map[string]any{
		"identifier": "ADMIN@EXAMPLE.COM",
		"password":   "password123",
	}, nil)
	if login.Status != http.StatusOK {
		t.Fatalf("登录失败: %d %s", login.Status, string(login.Body))
	}
	loginCookie := s.sessionCookie(login)

	badLogin := s.json(http.MethodPost, "/api/v1/auth/login", map[string]any{
		"identifier": "admin@example.com",
		"password":   "wrong-password",
	}, nil)
	if badLogin.Status != http.StatusUnauthorized {
		t.Errorf("错误密码应返回 401, 实际 %d", badLogin.Status)
	}
	usernameLogin := s.json(http.MethodPost, "/api/v1/auth/login", map[string]any{
		"identifier": "admin",
		"password":   "password123",
	}, nil)
	if usernameLogin.Status != http.StatusUnauthorized || s.decode(usernameLogin)["message"] != s.decode(badLogin)["message"] {
		t.Errorf("用户名登录应被拒绝, 并与错误密码使用相同响应: %d %s", usernameLogin.Status, string(usernameLogin.Body))
	}
	legacyUsernameFieldLogin := s.json(http.MethodPost, "/api/v1/auth/login", map[string]any{
		"username": "admin",
		"password": "password123",
	}, nil)
	if legacyUsernameFieldLogin.Status == http.StatusOK {
		t.Error("旧 username 请求字段不得用于登录")
	}

	changed := s.json(http.MethodPatch, "/api/v1/auth/password", map[string]any{
		"old_password": "password123",
		"new_password": "password456",
	}, []*http.Cookie{loginCookie})
	if changed.Status != http.StatusNoContent {
		t.Errorf("修改密码失败: %d %s", changed.Status, string(changed.Body))
	}
	relogin := s.json(http.MethodPost, "/api/v1/auth/login", map[string]any{
		"identifier": "admin@example.com",
		"password":   "password456",
	}, nil)
	if relogin.Status != http.StatusOK {
		t.Errorf("新密码登录失败: %d %s", relogin.Status, string(relogin.Body))
	}

	changedCookie := s.sessionCookie(changed)
	if stale := s.raw(http.MethodGet, "/api/v1/auth/me", nil, nil, []*http.Cookie{loginCookie}); stale.Status != http.StatusUnauthorized {
		t.Errorf("改密后旧会话应失效, 实际 %d %s", stale.Status, string(stale.Body))
	}
	if current := s.raw(http.MethodGet, "/api/v1/auth/me", nil, nil, []*http.Cookie{changedCookie}); current.Status != http.StatusOK {
		t.Fatalf("改密响应应续发当前会话, 实际 %d %s", current.Status, string(current.Body))
	}

	logoutCookie := s.sessionCookie(relogin)
	logout := s.raw(http.MethodPost, "/api/v1/auth/logout", nil, nil, []*http.Cookie{logoutCookie})
	if logout.Status != http.StatusNoContent {
		t.Errorf("登出失败: %d", logout.Status)
	}
	clearedCookie := false
	for _, value := range logout.Header.Values("Set-Cookie") {
		if strings.HasPrefix(value, "picsky_session=") && strings.Contains(strings.ToLower(value), "max-age=0") {
			clearedCookie = true
			break
		}
	}
	if !clearedCookie {
		t.Errorf("登出响应应清除浏览器 cookie: %v", logout.Header.Values("Set-Cookie"))
	}
	for _, cookie := range []*http.Cookie{changedCookie, logoutCookie} {
		if revoked := s.raw(http.MethodGet, "/api/v1/auth/me", nil, nil, []*http.Cookie{cookie}); revoked.Status != http.StatusUnauthorized {
			t.Errorf("登出后旧会话应失效, 实际 %d %s", revoked.Status, string(revoked.Body))
		}
	}
}

func TestPasswordResetDisablesTotp(t *testing.T) {
	s := newServer(t)
	smtpPort, messages := smtpCaptureServer(t)
	if err := s.svc.Settings.Update(map[string]string{
		"smtp_host":       "127.0.0.1",
		"smtp_port":       smtpPort,
		"smtp_from":       "noreply@example.com",
		"smtp_encryption": "none",
		"site_url":        "https://picsky.example",
	}); err != nil {
		t.Fatalf("配置 SMTP 失败: %v", err)
	}

	registered, registeredCookie := s.registerAndLogin("resetuser", "reset@example.com", "old-password123")
	userID := int64(s.decode(registered)["id"].(float64))
	secret, _, err := s.svc.Auth.TOTPSetup(userID)
	if err != nil {
		t.Fatalf("生成 TOTP 秘钥失败: %v", err)
	}
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("生成 TOTP 验证码失败: %v", err)
	}
	if err := s.svc.Auth.TOTPEnable(userID, secret, code, "old-password123", "127.0.0.1", "test"); err != nil {
		t.Fatalf("启用 TOTP 失败: %v", err)
	}
	beforeResetLogin := s.json(http.MethodPost, "/api/v1/auth/login", map[string]any{
		"identifier": "reset@example.com",
		"password":   "old-password123",
	}, nil)
	if beforeResetLogin.Status != http.StatusUnauthorized {
		t.Fatalf("重置前启用的 TOTP 应要求第二步验证, 实际 %d %s", beforeResetLogin.Status, string(beforeResetLogin.Body))
	}
	details, ok := s.decode(beforeResetLogin)["details"].(map[string]any)
	if !ok || details["totp_required"] != true {
		t.Fatalf("重置前登录应返回 TOTP 挑战: %s", string(beforeResetLogin.Body))
	}

	request := s.json(http.MethodPost, "/api/v1/auth/password/reset/request", map[string]any{
		"email": "RESET@EXAMPLE.COM",
	}, nil)
	if request.Status != http.StatusNoContent {
		t.Fatalf("请求找回邮件失败: %d %s", request.Status, string(request.Body))
	}
	unknownEmail := s.json(http.MethodPost, "/api/v1/auth/password/reset/request", map[string]any{
		"email": "missing@example.com",
	}, nil)
	if unknownEmail.Status != http.StatusNoContent {
		t.Errorf("不存在的邮箱也应返回相同成功状态, 实际 %d %s", unknownEmail.Status, string(unknownEmail.Body))
	}

	var message string
	select {
	case message = <-messages:
	case <-time.After(10 * time.Second):
		t.Fatal("SMTP 测试服务未收到找回邮件")
	}
	const linkPrefix = "https://picsky.example/reset-password#token="
	linkStart := strings.Index(message, linkPrefix)
	if linkStart < 0 {
		t.Fatalf("邮件缺少密码重置链接: %s", message)
	}
	token := strings.TrimSpace(strings.SplitN(message[linkStart+len(linkPrefix):], "\r\n", 2)[0])
	if token == "" {
		t.Fatal("邮件中的重置令牌为空")
	}

	reset := s.json(http.MethodPost, "/api/v1/auth/password/reset", map[string]any{
		"token":    token,
		"password": "new-password456",
	}, nil)
	if reset.Status != http.StatusNoContent {
		t.Fatalf("重置密码失败: %d %s", reset.Status, string(reset.Body))
	}
	var resetUser model.User
	if err := s.db.Where("email = ?", "reset@example.com").First(&resetUser).Error; err != nil {
		t.Fatalf("读取密码重置后的用户失败: %v", err)
	}
	if resetUser.TOTPSecret != "" || resetUser.TOTPLastCounter != 0 {
		t.Fatalf("密码重置应清除 TOTP 秘钥和已消费计数器: %+v", resetUser)
	}
	if stale := s.raw(http.MethodGet, "/api/v1/auth/me", nil, nil, []*http.Cookie{registeredCookie}); stale.Status != http.StatusUnauthorized {
		t.Errorf("密码重置后旧会话应失效, 实际 %d %s", stale.Status, string(stale.Body))
	}
	if replay := s.json(http.MethodPost, "/api/v1/auth/password/reset", map[string]any{
		"token":    token,
		"password": "replayed-password789",
	}, nil); replay.Status != http.StatusBadRequest {
		t.Errorf("重置令牌重放应被拒绝, 实际 %d %s", replay.Status, string(replay.Body))
	}
	if oldPassword := s.json(http.MethodPost, "/api/v1/auth/login", map[string]any{
		"identifier": "reset@example.com",
		"password":   "old-password123",
	}, nil); oldPassword.Status != http.StatusUnauthorized {
		t.Errorf("重置后旧密码应失效, 实际 %d %s", oldPassword.Status, string(oldPassword.Body))
	}
	newPassword := s.json(http.MethodPost, "/api/v1/auth/login", map[string]any{
		"identifier": "reset@example.com",
		"password":   "new-password456",
	}, nil)
	if newPassword.Status != http.StatusOK {
		t.Errorf("重置后新密码应直接登录且无需 TOTP, 实际 %d %s", newPassword.Status, string(newPassword.Body))
	}
}
func TestLoginTwoStepTotpAPI(t *testing.T) {
	s := newServer(t)
	_, cookie := s.registerAndLogin("twostep", "two-step@example.com", "password123")

	setup := s.json(http.MethodPost, "/api/v1/auth/totp/setup", nil, []*http.Cookie{cookie})
	if setup.Status != http.StatusOK {
		t.Fatalf("生成 TOTP 秘钥失败: %d %s", setup.Status, string(setup.Body))
	}
	secret, ok := s.decode(setup)["secret"].(string)
	if !ok || secret == "" {
		t.Fatalf("响应缺少 TOTP 秘钥: %s", string(setup.Body))
	}
	code, err := totp.GenerateCode(secret, time.Now().UTC())
	if err != nil {
		t.Fatalf("生成 TOTP 验证码失败: %v", err)
	}
	wrongPassword := s.json(http.MethodPost, "/api/v1/auth/totp/enable", map[string]any{
		"secret":   secret,
		"code":     code,
		"password": "wrong-password",
	}, []*http.Cookie{cookie})
	if wrongPassword.Status != http.StatusUnauthorized {
		t.Fatalf("错误的当前密码应拒绝启用 TOTP, 实际 %d %s", wrongPassword.Status, string(wrongPassword.Body))
	}
	enabled := s.json(http.MethodPost, "/api/v1/auth/totp/enable", map[string]any{
		"secret":   secret,
		"code":     code,
		"password": "password123",
	}, []*http.Cookie{cookie})
	if enabled.Status != http.StatusNoContent {
		t.Fatalf("启用 TOTP 失败: %d %s", enabled.Status, string(enabled.Body))
	}

	first := s.json(http.MethodPost, "/api/v1/auth/login", map[string]any{
		"identifier": "two-step@example.com",
		"password":   "password123",
	}, nil)
	if first.Status != http.StatusUnauthorized {
		t.Fatalf("首次登录应请求 TOTP: %d %s", first.Status, string(first.Body))
	}
	details, ok := s.decode(first)["details"].(map[string]any)
	if !ok || details["totp_required"] != true {
		t.Fatalf("首次响应缺少两步验证标记: %s", string(first.Body))
	}
	challenge, ok := details["login_challenge"].(string)
	if !ok || challenge == "" {
		t.Fatalf("首次响应缺少登录挑战: %s", string(first.Body))
	}

	code, err = totp.GenerateCode(secret, time.Now().UTC().Add(30*time.Second))
	if err != nil {
		t.Fatalf("重新生成 TOTP 验证码失败: %v", err)
	}
	second := s.json(http.MethodPost, "/api/v1/auth/login", map[string]any{
		"login_challenge": challenge,
		"totp":            code,
	}, nil)
	if second.Status != http.StatusOK {
		t.Fatalf("第二步登录失败: %d %s", second.Status, string(second.Body))
	}
	s.sessionCookie(second)

	replayLogin := s.json(http.MethodPost, "/api/v1/auth/login", map[string]any{
		"identifier": "two-step@example.com",
		"password":   "password123",
	}, nil)
	replayDetails, ok := s.decode(replayLogin)["details"].(map[string]any)
	if replayLogin.Status != http.StatusUnauthorized || !ok || replayDetails["totp_required"] != true {
		t.Fatalf("重放检查登录未创建新的 TOTP 挑战: %d %s", replayLogin.Status, string(replayLogin.Body))
	}
	replayChallenge, ok := replayDetails["login_challenge"].(string)
	if !ok || replayChallenge == "" {
		t.Fatalf("重放检查缺少登录挑战: %s", string(replayLogin.Body))
	}
	replay := s.json(http.MethodPost, "/api/v1/auth/login", map[string]any{
		"login_challenge": replayChallenge,
		"totp":            code,
	}, nil)
	if replay.Status != http.StatusUnauthorized {
		t.Fatalf("已消费的动态口令必须拒绝重放, 实际 %d %s", replay.Status, string(replay.Body))
	}
}

func TestUploadImagesAndKeys(t *testing.T) {
	s := newServer(t)
	cookie, policyID := s.bootstrap()

	content := pngContent(t, 3)
	uploaded := s.upload("/api/v1/upload", "photo.png", content, map[string]string{
		"policy_id": fmt.Sprint(policyID),
	}, map[string]string{}, []*http.Cookie{cookie})
	if uploaded.Status != http.StatusCreated {
		t.Fatalf("上传失败: %d %s", uploaded.Status, string(uploaded.Body))
	}
	record := s.decode(uploaded)
	url, _ := record["url"].(string)
	if !strings.HasPrefix(url, "https://cdn.example.com/image/") {
		t.Errorf("返回 URL 应指向分发域名: %q", url)
	}
	urls, ok := record["urls"].([]any)
	if !ok || len(urls) != 2 {
		t.Errorf("应返回全部别名 URL: %v", record["urls"])
	}
	hashes, ok := record["hashes"].(map[string]any)
	if !ok || hashes["md5"] == "" || hashes["sha256"] == "" || hashes["sha512"] == "" {
		t.Errorf("上传响应缺少哈希: %v", record["hashes"])
	}
	if record["dedup"] != false || record["quota_charged"] != true {
		t.Errorf("首次上传计费标记不正确: %v", record)
	}
	id := int64(record["id"].(float64))

	// 秒传: 同内容再次上传返回 200 且不新建记录.
	again := s.upload("/api/v1/upload", "photo2.png", content, nil, map[string]string{}, []*http.Cookie{cookie})
	if again.Status != http.StatusOK || s.decode(again)["dedup"] != true {
		t.Errorf("重复上传应命中秒传: %d %s", again.Status, string(again.Body))
	}

	checkSum := hashes["sha256"].(string)
	check := s.json(http.MethodPost, "/api/v1/upload/check", map[string]any{
		"sha256":    checkSum,
		"policy_id": policyID,
	}, []*http.Cookie{cookie})
	if check.Status != http.StatusOK || s.decode(check)["exists"] != true {
		t.Errorf("秒传检查失败: %d %s", check.Status, string(check.Body))
	}

	list := s.raw(http.MethodGet, "/api/v1/images?page=1&page_size=10", nil, nil, []*http.Cookie{cookie})
	if list.Status != http.StatusOK {
		t.Fatalf("列表失败: %d %s", list.Status, string(list.Body))
	}
	page := s.decode(list)
	if page["total"].(float64) != 1 {
		t.Errorf("图库应只有 1 条记录: %v", page["total"])
	}
	items := page["items"].([]any)
	item := items[0].(map[string]any)
	if item["original_filename"] != "photo.png" || item["policy_name"] != "主存储" {
		t.Errorf("列表项字段不正确: %v", item)
	}
	caps, ok := item["policy_capabilities"].([]any)
	if !ok {
		t.Fatalf("列表项应返回策略驱动能力: %v", item["policy_capabilities"])
	}
	hasDelete := false
	for _, capability := range caps {
		if capability == "delete" {
			hasDelete = true
		}
	}
	if !hasDelete {
		t.Errorf("WebDAV 策略应声明 delete 能力: %v", caps)
	}
	if item["user_email"] != "admin@example.com" {
		t.Errorf("列表项应返回上传用户邮箱: %v", item["user_email"])
	}

	fuzzy := s.raw(http.MethodGet, "/api/v1/images?user=ADMIN%40EXA", nil, nil, []*http.Cookie{cookie})
	if fuzzy.Status != http.StatusOK || s.decode(fuzzy)["total"].(float64) != 1 {
		t.Errorf("邮箱模糊筛选失败: %d %s", fuzzy.Status, string(fuzzy.Body))
	}
	miss := s.raw(http.MethodGet, "/api/v1/images?user=nobody", nil, nil, []*http.Cookie{cookie})
	if miss.Status != http.StatusOK || s.decode(miss)["total"].(float64) != 0 {
		t.Errorf("邮箱筛选不匹配时应返回空列表: %d %s", miss.Status, string(miss.Body))
	}
	if item["width"].(float64) != 8 || item["height"].(float64) != 8 {
		t.Errorf("列表项尺寸不正确: %v", item)
	}

	detail := s.raw(http.MethodGet, fmt.Sprintf("/api/v1/images/%d", id), nil, nil, []*http.Cookie{cookie})
	if detail.Status != http.StatusOK {
		t.Errorf("详情失败: %d %s", detail.Status, string(detail.Body))
	}

	created := s.json(http.MethodPost, "/api/v1/keys", map[string]any{
		"name":   "CI",
		"scopes": []string{"upload"},
	}, []*http.Cookie{cookie})
	if created.Status != http.StatusCreated {
		t.Fatalf("创建密钥失败: %d %s", created.Status, string(created.Body))
	}
	keyPayload := s.decode(created)
	secret, _ := keyPayload["secret"].(string)
	if !strings.Contains(secret, ".") {
		t.Fatalf("密钥应返回明文 secret 一次: %v", keyPayload)
	}
	keyID := int64(keyPayload["id"].(float64))

	viaKey := s.upload("/api/v1/upload", "key-upload.png", pngContent(t, 4), nil, map[string]string{
		"Authorization": "Bearer " + secret,
	}, nil)
	if viaKey.Status != http.StatusCreated {
		t.Fatalf("使用 API 密钥上传失败: %d %s", viaKey.Status, string(viaKey.Body))
	}
	if s.decode(viaKey)["source"] == nil {
		// 上传响应不含 source, 从记录中确认
	}
	var source string
	if err := s.db.Model(&struct {
		Source string
	}{}).Table("images").Select("source").Order("id DESC").Limit(1).Scan(&source).Error; err != nil {
		t.Fatalf("读取来源失败: %v", err)
	}
	if source != "api" {
		t.Errorf("API 密钥上传的记录来源应为 api: %q", source)
	}

	// 普通用户不能签发超出自身权限的密钥.
	_, scopedCookie := s.registerAndLogin("scoped", "scoped@example.com", "password123")
	withinScope := s.json(http.MethodPost, "/api/v1/keys", map[string]any{
		"name":   "ok",
		"scopes": []string{"upload"},
	}, []*http.Cookie{scopedCookie})
	if withinScope.Status != http.StatusCreated {
		t.Errorf("组内权限范围应可签发: %d %s", withinScope.Status, string(withinScope.Body))
	}
	overScoped := s.json(http.MethodPost, "/api/v1/keys", map[string]any{
		"name":   "bad",
		"scopes": []string{"manage_users"},
	}, []*http.Cookie{scopedCookie})
	if overScoped.Status == http.StatusCreated {
		t.Error("密钥权限超出用户权限应被拒绝")
	}

	revoked := s.raw(http.MethodDelete, fmt.Sprintf("/api/v1/keys/%d", keyID), nil, nil, []*http.Cookie{cookie})
	if revoked.Status != http.StatusNoContent {
		t.Errorf("吊销密钥失败: %d %s", revoked.Status, string(revoked.Body))
	}
	afterRevoke := s.upload("/api/v1/upload", "x.png", pngContent(t, 5), nil, map[string]string{
		"Authorization": "Bearer " + secret,
	}, nil)
	if afterRevoke.Status != http.StatusUnauthorized {
		t.Errorf("已吊销密钥应返回 401, 实际 %d", afterRevoke.Status)
	}

	batch := s.json(http.MethodPost, "/api/v1/images/batch-delete", map[string]any{
		"ids": []int64{id},
	}, []*http.Cookie{cookie})
	if batch.Status != http.StatusOK {
		t.Fatalf("批量删除失败: %d %s", batch.Status, string(batch.Body))
	}
	batchItems := s.decode(batch)["items"].([]any)
	first := batchItems[0].(map[string]any)
	if first["deleted"] != true {
		t.Errorf("批量删除结果不正确: %v", first)
	}
	if first["object_deleted"] != true || first["reason"] != "last_reference" {
		t.Errorf("引用归零时应删除外部对象: %v", first)
	}

	stats := s.raw(http.MethodGet, "/api/v1/admin/stats", nil, nil, []*http.Cookie{cookie})
	if stats.Status != http.StatusOK {
		t.Fatalf("概览失败: %d %s", stats.Status, string(stats.Body))
	}
	overview := s.decode(stats)
	if overview["image_count"].(float64) < 1 {
		t.Errorf("概览图片数不正确: %v", overview)
	}
}

func TestUploadCheckDoesNotExposeOtherUsersRecord(t *testing.T) {
	s := newServer(t)
	ownerCookie, policyID := s.bootstrap()

	uploaded := s.upload("/api/v1/upload", "private.png", pngContent(t, 19), map[string]string{
		"policy_id": fmt.Sprint(policyID),
	}, map[string]string{}, []*http.Cookie{ownerCookie})
	if uploaded.Status != http.StatusCreated {
		t.Fatalf("上传测试图片失败: %d %s", uploaded.Status, string(uploaded.Body))
	}
	record := s.decode(uploaded)
	hashes, ok := record["hashes"].(map[string]any)
	if !ok {
		t.Fatalf("上传响应缺少哈希: %v", record["hashes"])
	}
	sha256, ok := hashes["sha256"].(string)
	if !ok || sha256 == "" {
		t.Fatalf("上传响应缺少 SHA-256: %v", hashes["sha256"])
	}

	var owner model.User
	if err := s.db.Where("email = ?", "admin@example.com").First(&owner).Error; err != nil {
		t.Fatalf("读取记录所有者失败: %v", err)
	}
	userLogin, userCookie := s.registerAndLogin("other", "other@example.com", "password123")
	userID := int64(s.decode(userLogin)["id"].(float64))
	if err := s.db.Model(&model.User{}).Where("id = ?", userID).Update("group_id", owner.GroupID).Error; err != nil {
		t.Fatalf("授权测试用户使用同一策略失败: %v", err)
	}

	check := s.json(http.MethodPost, "/api/v1/upload/check", map[string]any{
		"sha256":    sha256,
		"policy_id": policyID,
	}, []*http.Cookie{userCookie})
	if check.Status != http.StatusOK {
		t.Fatalf("跨用户秒传检查失败: %d %s", check.Status, string(check.Body))
	}
	result := s.decode(check)
	if result["exists"] != false {
		t.Errorf("跨用户记录应视为当前用户不存在: %s", string(check.Body))
	}
	if _, exists := result["record"]; exists {
		t.Errorf("响应不得包含其他用户的记录 ID、URL 或大小: %s", string(check.Body))
	}
}

func TestUploadWebDAVFailureDoesNotExposeResponseBody(t *testing.T) {
	s := newServer(t)
	adminCookie, _ := s.bootstrap()
	const responseBody = "private-webdav-diagnostic"
	webdavServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(w, responseBody)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer webdavServer.Close()

	policy := s.json(http.MethodPost, "/api/v1/admin/policies", map[string]any{
		"name":            "错误后端",
		"type":            "webdav",
		"enabled":         true,
		"path_template":   "{md5}.{ext}",
		"conflict_policy": "rename",
		"config":          map[string]string{"endpoint": webdavServer.URL + "/dav"},
		"urls":            []map[string]any{{"alias": "CDN", "url": "https://cdn.example.com/", "is_default": true}},
	}, []*http.Cookie{adminCookie})
	if policy.Status != http.StatusCreated {
		t.Fatalf("创建测试策略失败: %d %s", policy.Status, string(policy.Body))
	}
	policyID := int64(s.decode(policy)["id"].(float64))
	upload := s.upload("/api/v1/upload", "private.png", pngContent(t, 31), map[string]string{
		"policy_id": fmt.Sprint(policyID),
	}, map[string]string{}, []*http.Cookie{adminCookie})
	if upload.Status != http.StatusBadGateway {
		t.Fatalf("WebDAV 上传失败应返回 502: %d %s", upload.Status, string(upload.Body))
	}
	if strings.Contains(string(upload.Body), responseBody) {
		t.Errorf("上传响应泄露了 WebDAV 错误响应体: %s", string(upload.Body))
	}
}

func TestAdminReorderPolicies(t *testing.T) {
	s := newServer(t)
	cookie, firstID := s.bootstrap()

	create := func(name string) int64 {
		t.Helper()
		created := s.json(http.MethodPost, "/api/v1/admin/policies", map[string]any{
			"name":            name,
			"type":            "webdav",
			"enabled":         true,
			"path_template":   "{sha256}/{md5}.{ext}",
			"conflict_policy": "rename",
			"config":          map[string]string{"endpoint": s.svc.Cfg.DBDSN},
			"urls": []map[string]any{
				{"alias": "CDN", "url": "https://cdn.example.com/" + name + "/", "is_default": true},
			},
		}, []*http.Cookie{cookie})
		if created.Status != http.StatusCreated {
			t.Fatalf("创建策略 %s 失败: %d %s", name, created.Status, string(created.Body))
		}
		return int64(s.decode(created)["id"].(float64))
	}
	secondID := create("第二策略")
	thirdID := create("第三策略")

	reordered := s.json(http.MethodPatch, "/api/v1/admin/policies", map[string]any{
		"ids": []int64{thirdID, firstID, secondID},
	}, []*http.Cookie{cookie})
	if reordered.Status != http.StatusNoContent {
		t.Fatalf("重排策略失败: %d %s", reordered.Status, string(reordered.Body))
	}

	listed := s.json(http.MethodGet, "/api/v1/admin/policies", nil, []*http.Cookie{cookie})
	if listed.Status != http.StatusOK {
		t.Fatalf("读取策略失败: %d %s", listed.Status, string(listed.Body))
	}
	var policies []map[string]any
	if err := json.Unmarshal(listed.Body, &policies); err != nil {
		t.Fatalf("解析策略列表失败: %v", err)
	}
	want := []int64{thirdID, firstID, secondID}
	if len(policies) != len(want) {
		t.Fatalf("策略数量 = %d, 期望 %d", len(policies), len(want))
	}
	for index, policy := range policies {
		if id := int64(policy["id"].(float64)); id != want[index] {
			t.Errorf("第 %d 项策略 = %d, 期望 %d", index, id, want[index])
		}
		if sortValue := int(policy["sort"].(float64)); sortValue != index+1 {
			t.Errorf("第 %d 项排序 = %d, 期望 %d", index, sortValue, index+1)
		}
	}

	duplicated := s.json(http.MethodPatch, "/api/v1/admin/policies", map[string]any{
		"ids": []int64{firstID, firstID},
	}, []*http.Cookie{cookie})
	if duplicated.Status != http.StatusBadRequest {
		t.Errorf("重复策略 ID 应返回 400: %d %s", duplicated.Status, string(duplicated.Body))
	}
}

func TestAdminEndpointsAndPermissions(t *testing.T) {
	s := newServer(t)
	cookie, policyID := s.bootstrap()

	groups := s.raw(http.MethodGet, "/api/v1/admin/groups", nil, nil, []*http.Cookie{cookie})
	if groups.Status != http.StatusOK {
		t.Fatalf("读取用户组失败: %d %s", groups.Status, string(groups.Body))
	}
	var groupList []map[string]any
	if err := json.Unmarshal(groups.Body, &groupList); err != nil {
		t.Fatalf("解析用户组失败: %v", err)
	}
	var normalGroupID int64
	for _, group := range groupList {
		if group["name"] == "普通用户" {
			normalGroupID = int64(group["id"].(float64))
		}
	}
	if normalGroupID == 0 {
		t.Fatal("未找到普通用户组")
	}

	authorized := s.json(http.MethodPut, fmt.Sprintf("/api/v1/admin/groups/%d/policies", normalGroupID), map[string]any{
		"policy_ids": []int64{policyID},
	}, []*http.Cookie{cookie})
	if authorized.Status != http.StatusNoContent {
		t.Fatalf("授权策略失败: %d %s", authorized.Status, string(authorized.Body))
	}

	createdUser := s.json(http.MethodPost, "/api/v1/admin/users", map[string]any{
		"username": "alice",
		"email":    "alice@example.com",
		"password": "password123",
		"group_id": normalGroupID,
	}, []*http.Cookie{cookie})
	if createdUser.Status != http.StatusCreated {
		t.Fatalf("创建用户失败: %d %s", createdUser.Status, string(createdUser.Body))
	}
	userID := int64(s.decode(createdUser)["id"].(float64))

	banned := s.json(http.MethodPatch, fmt.Sprintf("/api/v1/admin/users/%d", userID), map[string]any{
		"status": 2,
	}, []*http.Cookie{cookie})
	if banned.Status != http.StatusOK {
		t.Fatalf("封禁用户失败: %d %s", banned.Status, string(banned.Body))
	}

	aliceLogin := s.json(http.MethodPost, "/api/v1/auth/login", map[string]any{
		"identifier": "alice@example.com",
		"password":   "password123",
	}, nil)
	if aliceLogin.Status != http.StatusUnauthorized {
		t.Errorf("封禁账号登录应返回通用 401, 实际 %d %s", aliceLogin.Status, string(aliceLogin.Body))
	}
	missingLogin := s.json(http.MethodPost, "/api/v1/auth/login", map[string]any{
		"identifier": "missing@example.com",
		"password":   "password123",
	}, nil)
	if missingLogin.Status != http.StatusUnauthorized || s.decode(aliceLogin)["message"] != s.decode(missingLogin)["message"] {
		t.Errorf("封禁账号与不存在账号应使用相同登录响应: %d %s", missingLogin.Status, string(missingLogin.Body))
	}

	if err := s.svc.Settings.Update(nil); err != nil {
		t.Fatalf("空更新应成功: %v", err)
	}
	settings := s.raw(http.MethodGet, "/api/v1/admin/settings", nil, nil, []*http.Cookie{cookie})
	if settings.Status != http.StatusOK {
		t.Fatalf("读取设置失败: %d %s", settings.Status, string(settings.Body))
	}
	settingsPayload := s.decode(settings)
	if settingsPayload["site_name"] == "" || settingsPayload["definitions"] == nil {
		t.Errorf("设置响应缺少内容: %v", settingsPayload)
	}

	updated := s.json(http.MethodPatch, "/api/v1/admin/settings", map[string]any{
		"site_name":                       "Picsky 测试站",
		"register_enabled":                false,
		"captcha_secret":                  "secret-value",
		"max_file_size_bytes":             10485760,
		"delete_object_on_last_reference": true,
	}, []*http.Cookie{cookie})
	if updated.Status != http.StatusOK {
		t.Fatalf("更新设置失败: %d %s", updated.Status, string(updated.Body))
	}
	if s.decode(updated)["site_name"] != "Picsky 测试站" || s.decode(updated)["captcha_secret_set"] != true {
		t.Errorf("设置更新结果不正确: %s", string(updated.Body))
	}

	registerDisabled := s.json(http.MethodPost, "/api/v1/auth/register", map[string]any{
		"username": "bob",
		"email":    "bob@example.com",
		"password": "password123",
	}, nil)
	if registerDisabled.Status != http.StatusForbidden {
		t.Errorf("关闭注册后应返回 403, 实际 %d %s", registerDisabled.Status, string(registerDisabled.Body))
	}

	logs := s.raw(http.MethodGet, "/api/v1/admin/logs?page=1&page_size=50", nil, nil, []*http.Cookie{cookie})
	if logs.Status != http.StatusOK {
		t.Fatalf("读取日志失败: %d %s", logs.Status, string(logs.Body))
	}
	logPayload := s.decode(logs)
	if logPayload["total"].(float64) < 1 {
		t.Errorf("审计日志应有内容: %v", logPayload)
	}

	tasks := s.raw(http.MethodGet, "/api/v1/admin/tasks", nil, nil, []*http.Cookie{cookie})
	if tasks.Status != http.StatusOK {
		t.Fatalf("读取任务失败: %d %s", tasks.Status, string(tasks.Body))
	}

	task, err := s.svc.Tasks.Enqueue(model.TaskLogPurge, nil)
	if err != nil {
		t.Fatalf("创建维护任务失败: %v", err)
	}
	runTaskPath := fmt.Sprintf("/api/v1/admin/tasks/%d/run", task.ID)
	for attempt := 1; attempt <= 2; attempt++ {
		run := s.raw(http.MethodPost, runTaskPath, nil, nil, []*http.Cookie{cookie})
		if run.Status != http.StatusNoContent {
			t.Fatalf("第 %d 次运行任务失败: %d %s", attempt, run.Status, string(run.Body))
		}
	}
	var rerunTask model.MaintenanceTask
	if err := s.db.First(&rerunTask, task.ID).Error; err != nil {
		t.Fatalf("读取重跑后的任务失败: %v", err)
	}
	if rerunTask.Status != model.TaskDone || rerunTask.Attempts != 2 {
		t.Fatalf("重跑后的任务状态或尝试次数不正确: %+v", rerunTask)
	}

	preview := s.json(http.MethodPost, "/api/v1/admin/policies/preview", map[string]any{
		"path_template": "{md5:2}/{md5}.{ext}",
		"policy_id":     policyID,
	}, []*http.Cookie{cookie})
	if preview.Status != http.StatusOK {
		t.Fatalf("预览失败: %d %s", preview.Status, string(preview.Body))
	}
	previewPayload := s.decode(preview)
	if previewPayload["object_key"] != "0f/0f1e2d3c4b5a69788796a5b4c3d2e1f0.png" {
		t.Errorf("预览对象键不正确: %v", previewPayload["object_key"])
	}

	tested := s.raw(http.MethodPost, fmt.Sprintf("/api/v1/admin/policies/%d/test", policyID), nil, nil, []*http.Cookie{cookie})
	if tested.Status != http.StatusOK || s.decode(tested)["ok"] != true {
		t.Errorf("连接测试失败: %d %s", tested.Status, string(tested.Body))
	}

	scanned := s.json(http.MethodPost, fmt.Sprintf("/api/v1/admin/policies/%d/scan", policyID), map[string]any{
		"dry_run": true,
	}, []*http.Cookie{cookie})
	if scanned.Status != http.StatusAccepted {
		t.Fatalf("扫描任务未入队: %d %s", scanned.Status, string(scanned.Body))
	}
	scanTask := s.decode(scanned)
	taskID, ok := scanTask["task_id"].(float64)
	if !ok || taskID <= 0 || scanTask["status"] != model.TaskPending {
		t.Fatalf("扫描接口未返回待处理任务信息: %s", string(scanned.Body))
	}
	scanStatus := s.raw(http.MethodGet, fmt.Sprintf("/api/v1/admin/policies/%d/scan/%d", policyID, int64(taskID)), nil, nil, []*http.Cookie{cookie})
	if scanStatus.Status != http.StatusOK || s.decode(scanStatus)["status"] != model.TaskPending {
		t.Errorf("扫描任务状态查询失败: %d %s", scanStatus.Status, string(scanStatus.Body))
	}

	drivers := s.raw(http.MethodGet, "/api/v1/admin/policies/drivers", nil, nil, []*http.Cookie{cookie})
	if drivers.Status != http.StatusOK || !strings.Contains(string(drivers.Body), "webdav") {
		t.Errorf("驱动清单不正确: %d %s", drivers.Status, string(drivers.Body))
	}

	// 普通用户无权访问管理接口.
	aliceEnabled := s.json(http.MethodPatch, fmt.Sprintf("/api/v1/admin/users/%d", userID), map[string]any{
		"status": 0,
	}, []*http.Cookie{cookie})
	if aliceEnabled.Status != http.StatusOK {
		t.Fatalf("启用用户失败: %d", aliceEnabled.Status)
	}
	aliceLogin = s.json(http.MethodPost, "/api/v1/auth/login", map[string]any{
		"identifier": "alice@example.com",
		"password":   "password123",
	}, nil)
	if aliceLogin.Status != http.StatusOK {
		t.Fatalf("普通用户登录失败: %d %s", aliceLogin.Status, string(aliceLogin.Body))
	}
	aliceCookie := s.sessionCookie(aliceLogin)
	forbidden := s.raw(http.MethodGet, "/api/v1/admin/users", nil, nil, []*http.Cookie{aliceCookie})
	if forbidden.Status != http.StatusForbidden {
		t.Errorf("普通用户访问管理接口应返回 403, 实际 %d", forbidden.Status)
	}
	adminArea := s.raw(http.MethodGet, "/api/v1/admin/stats", nil, nil, []*http.Cookie{aliceCookie})
	if adminArea.Status != http.StatusForbidden {
		t.Errorf("普通用户访问后台概览应返回 403, 实际 %d", adminArea.Status)
	}

	policies := s.raw(http.MethodGet, "/api/v1/policies", nil, nil, []*http.Cookie{aliceCookie})
	if policies.Status != http.StatusOK {
		t.Fatalf("读取可用策略失败: %d %s", policies.Status, string(policies.Body))
	}
	var policyList []map[string]any
	if err := json.Unmarshal(policies.Body, &policyList); err != nil {
		t.Fatalf("解析策略失败: %v", err)
	}
	if len(policyList) != 1 {
		t.Errorf("可用策略列表不正确: %v", policyList)
	}
	if hint, ok := policyList[0]["hint"]; !ok || hint != "" {
		t.Errorf("可用策略应包含 hint 字段: %v", policyList[0])
	}

	aliceUpload := s.upload("/api/v1/upload", "alice.png", pngContent(t, 9), nil, map[string]string{}, []*http.Cookie{aliceCookie})
	if aliceUpload.Status != http.StatusCreated {
		t.Fatalf("普通用户上传失败: %d %s", aliceUpload.Status, string(aliceUpload.Body))
	}

	deletedUser := s.raw(http.MethodDelete, fmt.Sprintf("/api/v1/admin/users/%d", userID), nil, nil, []*http.Cookie{cookie})
	if deletedUser.Status != http.StatusNoContent {
		t.Errorf("删除用户失败: %d %s", deletedUser.Status, string(deletedUser.Body))
	}
}

func TestNativeUploadRejectsInvalidPayloads(t *testing.T) {
	s := newServer(t)
	cookie, policyID := s.bootstrap()

	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><rect width="1" height="1"/></svg>`)
	svgUpload := s.upload("/api/v1/upload", "vector.svg", svg, nil, map[string]string{}, []*http.Cookie{cookie})
	if svgUpload.Status != http.StatusUnsupportedMediaType {
		t.Errorf("SVG 应返回 415, 实际 %d %s", svgUpload.Status, string(svgUpload.Body))
	}

	missingFile := s.raw(http.MethodPost, "/api/v1/upload", strings.NewReader("not multipart"), map[string]string{
		"Content-Type": "multipart/form-data; boundary=xxx",
	}, []*http.Cookie{cookie})
	if missingFile.Status != http.StatusBadRequest {
		t.Errorf("非法 multipart 应返回 400, 实际 %d", missingFile.Status)
	}

	badPolicy := s.upload("/api/v1/upload", "photo.png", pngContent(t, 11), map[string]string{
		"policy_id": "9999",
	}, map[string]string{}, []*http.Cookie{cookie})
	if badPolicy.Status != http.StatusNotFound {
		t.Errorf("不存在的策略应返回 404, 实际 %d %s", badPolicy.Status, string(badPolicy.Body))
	}

	base64Upload := s.json(http.MethodPost, "/api/v1/upload/base64", map[string]any{
		"filename":  "base64.png",
		"data":      "data:image/png;base64," + base64Std(t, pngContent(t, 12)),
		"policy_id": policyID,
	}, []*http.Cookie{cookie})
	if base64Upload.Status != http.StatusCreated {
		t.Errorf("base64 上传失败: %d %s", base64Upload.Status, string(base64Upload.Body))
	}

	notFound := s.raw(http.MethodGet, "/api/v1/unknown-endpoint", nil, nil, nil)
	if notFound.Status != http.StatusNotFound {
		t.Errorf("未知 API 路径应返回 404, 实际 %d", notFound.Status)
	}
	if contentType := notFound.Header.Get("Content-Type"); !strings.Contains(contentType, "application/json") {
		t.Errorf("未知 API 路径应返回 JSON, 实际 %q", contentType)
	}
}

func TestNativeUploadRejectsOversizedMultipartField(t *testing.T) {
	s := newServer(t)
	cookie, policyID := s.bootstrap()
	result := s.upload("/api/v1/upload", "field-too-large.png", pngContent(t, 19), map[string]string{
		"policy_id": fmt.Sprint(policyID),
		"padding":   strings.Repeat("x", 4097),
	}, map[string]string{}, []*http.Cookie{cookie})
	if result.Status != http.StatusBadRequest {
		t.Fatalf("超过单字段上限应返回 400, 实际 %d %s", result.Status, string(result.Body))
	}
}

func TestNativeUploadLimitsTotalMultipartFieldBytes(t *testing.T) {
	s := newServer(t)
	cookie, policyID := s.bootstrap()
	fields := map[string]string{"policy_id": fmt.Sprint(policyID)}
	for i := range 16 {
		fields[fmt.Sprintf("field-%02d", i)] = strings.Repeat("x", 4096)
	}
	result := s.upload("/api/v1/upload", "field-budget.png", pngContent(t, 20), fields, map[string]string{}, []*http.Cookie{cookie})
	if result.Status != http.StatusBadRequest {
		t.Fatalf("超过 multipart 字段总量预算应返回 400, 实际 %d %s", result.Status, string(result.Body))
	}
}

func TestNativeUploadLimitsMultipartFieldCount(t *testing.T) {
	s := newServer(t)
	cookie, policyID := s.bootstrap()
	fields := map[string]string{"policy_id": fmt.Sprint(policyID)}
	for i := range 63 {
		fields[fmt.Sprintf("field-%02d", i)] = ""
	}
	withinLimit := s.upload("/api/v1/upload", "field-count.png", pngContent(t, 21), fields, map[string]string{}, []*http.Cookie{cookie})
	if withinLimit.Status != http.StatusCreated {
		t.Fatalf("64 个非文件字段应通过: %d %s", withinLimit.Status, string(withinLimit.Body))
	}
	fields["field-over-limit"] = ""
	overLimit := s.upload("/api/v1/upload", "field-count-over.png", pngContent(t, 22), fields, map[string]string{}, []*http.Cookie{cookie})
	if overLimit.Status != http.StatusBadRequest {
		t.Fatalf("超过 multipart 字段数量上限应返回 400, 实际 %d %s", overLimit.Status, string(overLimit.Body))
	}
}

func TestNativeUploadChecksPermissionBeforeReadingMultipartBody(t *testing.T) {
	s := newServer(t)
	adminCookie, policyID := s.bootstrap()
	group := s.json(http.MethodPost, "/api/v1/admin/groups", map[string]any{
		"name":        "无上传权限组",
		"permissions": []string{"manage_keys"},
	}, []*http.Cookie{adminCookie})
	if group.Status != http.StatusCreated {
		t.Fatalf("创建无上传权限组失败: %d %s", group.Status, string(group.Body))
	}
	groupID := int64(s.decode(group)["id"].(float64))
	created := s.json(http.MethodPost, "/api/v1/admin/users", map[string]any{
		"username": "noupload",
		"email":    "no-upload@example.com",
		"password": "password123",
		"group_id": groupID,
	}, []*http.Cookie{adminCookie})
	if created.Status != http.StatusCreated {
		t.Fatalf("创建无上传权限用户失败: %d %s", created.Status, string(created.Body))
	}
	login := s.json(http.MethodPost, "/api/v1/auth/login", map[string]any{
		"identifier": "no-upload@example.com",
		"password":   "password123",
	}, nil)
	if login.Status != http.StatusOK {
		t.Fatalf("无上传权限用户登录失败: %d %s", login.Status, string(login.Body))
	}
	fields := map[string]string{
		"policy_id": fmt.Sprint(policyID),
		"padding":   strings.Repeat("x", 2<<20),
	}
	result, bytesRead, bodySize := s.countedUpload("/api/v1/upload", "denied.png", pngContent(t, 23), fields, []*http.Cookie{s.sessionCookie(login)})
	if result.Status != http.StatusForbidden {
		t.Fatalf("无上传权限应返回 403, 实际 %d %s", result.Status, string(result.Body))
	}
	if bytesRead >= int64(bodySize)/2 {
		t.Errorf("权限拒绝前解析了大部分 multipart 请求体: %d/%d 字节", bytesRead, bodySize)
	}
}

func TestNativeUploadChecksRateLimitBeforeReadingMultipartBody(t *testing.T) {
	s := newServer(t)
	adminCookie, policyID := s.bootstrap()
	group := s.json(http.MethodPost, "/api/v1/admin/groups", map[string]any{
		"name":              "限速上传组",
		"permissions":       []string{"upload", "manage_policies"},
		"upload_rate_limit": 1,
	}, []*http.Cookie{adminCookie})
	if group.Status != http.StatusCreated {
		t.Fatalf("创建限速上传组失败: %d %s", group.Status, string(group.Body))
	}
	groupID := int64(s.decode(group)["id"].(float64))
	created := s.json(http.MethodPost, "/api/v1/admin/users", map[string]any{
		"username": "ratelimited",
		"email":    "rate-limited@example.com",
		"password": "password123",
		"group_id": groupID,
	}, []*http.Cookie{adminCookie})
	if created.Status != http.StatusCreated {
		t.Fatalf("创建限速用户失败: %d %s", created.Status, string(created.Body))
	}
	login := s.json(http.MethodPost, "/api/v1/auth/login", map[string]any{
		"identifier": "rate-limited@example.com",
		"password":   "password123",
	}, nil)
	if login.Status != http.StatusOK {
		t.Fatalf("限速用户登录失败: %d %s", login.Status, string(login.Body))
	}
	cookie := s.sessionCookie(login)
	first := s.upload("/api/v1/upload", "first.png", pngContent(t, 24), map[string]string{
		"policy_id": fmt.Sprint(policyID),
	}, map[string]string{}, []*http.Cookie{cookie})
	if first.Status != http.StatusCreated {
		t.Fatalf("限速窗口内首次上传应成功: %d %s", first.Status, string(first.Body))
	}

	fields := map[string]string{
		"policy_id": fmt.Sprint(policyID),
		"padding":   strings.Repeat("x", 2<<20),
	}
	result, bytesRead, bodySize := s.countedUpload("/api/v1/upload", "limited.png", pngContent(t, 25), fields, []*http.Cookie{cookie})
	if result.Status != http.StatusTooManyRequests {
		t.Fatalf("超出上传速率应返回 429, 实际 %d %s", result.Status, string(result.Body))
	}
	if bytesRead >= int64(bodySize)/2 {
		t.Errorf("限流拒绝前解析了大部分 multipart 请求体: %d/%d 字节", bytesRead, bodySize)
	}
}

func TestBase64UploadChecksPermissionBeforeReadingBody(t *testing.T) {
	s := newServer(t)
	adminCookie, _ := s.bootstrap()
	group := s.json(http.MethodPost, "/api/v1/admin/groups", map[string]any{
		"name":        "无上传权限组",
		"permissions": []string{"manage_keys"},
	}, []*http.Cookie{adminCookie})
	if group.Status != http.StatusCreated {
		t.Fatalf("创建无上传权限组失败: %d %s", group.Status, string(group.Body))
	}
	groupID := int64(s.decode(group)["id"].(float64))
	created := s.json(http.MethodPost, "/api/v1/admin/users", map[string]any{
		"username": "base64noupload",
		"email":    "base64-no-upload@example.com",
		"password": "password123",
		"group_id": groupID,
	}, []*http.Cookie{adminCookie})
	if created.Status != http.StatusCreated {
		t.Fatalf("创建无上传权限用户失败: %d %s", created.Status, string(created.Body))
	}
	login := s.json(http.MethodPost, "/api/v1/auth/login", map[string]any{
		"identifier": "base64-no-upload@example.com",
		"password":   "password123",
	}, nil)
	if login.Status != http.StatusOK {
		t.Fatalf("无上传权限用户登录失败: %d %s", login.Status, string(login.Body))
	}

	body := fmt.Sprintf(`{"data":"%s","padding":"%s"}`,
		base64.StdEncoding.EncodeToString(pngContent(t, 27)), strings.Repeat("x", 2<<20))
	countedBody := &countingReadCloser{reader: bytes.NewReader([]byte(body))}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/upload/base64", countedBody)
	request.ContentLength = int64(len(body))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(s.sessionCookie(login))
	recorder := httptest.NewRecorder()

	s.engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("无上传权限应返回 403, 实际 %d %s", recorder.Code, recorder.Body.String())
	}
	if countedBody.bytesRead >= int64(len(body))/2 {
		t.Errorf("权限拒绝前读取了大部分 base64 请求体: %d/%d 字节", countedBody.bytesRead, len(body))
	}
}

func TestLskyMultipartBudgetIncludesFirstTokenField(t *testing.T) {
	s := newServer(t)
	adminCookie, policyID := s.bootstrap()
	createdKey := s.json(http.MethodPost, "/api/v1/keys", map[string]any{"name": "lsky-field-budget"}, []*http.Cookie{adminCookie})
	if createdKey.Status != http.StatusCreated {
		t.Fatalf("创建 API 密钥失败: %d %s", createdKey.Status, string(createdKey.Body))
	}
	fields := map[string]string{
		"token":       s.decode(createdKey)["secret"].(string),
		"strategy_id": fmt.Sprint(policyID),
	}
	for i := range 15 {
		fields[fmt.Sprintf("field-%02d", i)] = strings.Repeat("x", 4096)
	}
	total := 0
	for name, value := range fields {
		total += len(name) + len(value)
	}
	edgeLength := (64 << 10) - total - len("edge") + 1
	if edgeLength <= 0 || edgeLength > 4096 {
		t.Fatalf("测试字段无法构造预算边界: extra=%d", edgeLength)
	}
	fields["edge"] = strings.Repeat("x", edgeLength)

	result := s.upload("/lsky/api/v1/upload", "field-budget.png", pngContent(t, 26), fields, map[string]string{}, nil)
	if result.Status != http.StatusOK {
		t.Fatalf("Lsky 业务错误应使用 HTTP 200, 实际 %d %s", result.Status, string(result.Body))
	}
	if s.decode(result)["status"] != false {
		t.Errorf("超出总量预算的 Lsky multipart 字段应被拒绝: %s", string(result.Body))
	}
}

func TestLskyUnauthenticatedUploadDoesNotReadFileBody(t *testing.T) {
	s := newServer(t)
	body, contentType := multipartBody(t, "large.png", bytes.Repeat([]byte("x"), 2<<20), nil)
	countedBody := &countingReadCloser{reader: bytes.NewReader(body.Bytes())}
	request := httptest.NewRequest(http.MethodPost, "/lsky/api/v1/upload", countedBody)
	request.ContentLength = int64(body.Len())
	request.Header.Set("Content-Type", contentType)
	recorder := httptest.NewRecorder()

	s.engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("未认证上传应返回 401, 实际 %d: %s", recorder.Code, recorder.Body.String())
	}
	if countedBody.bytesRead >= int64(body.Len())/2 {
		t.Errorf("认证失败前读取了大部分请求体: %d/%d 字节", countedBody.bytesRead, body.Len())
	}
}

func TestLskyRateLimitRejectsBeforeReadingFileBody(t *testing.T) {
	s := newServer(t)
	cookie, policyID := s.bootstrap()
	userID, err := s.svc.Auth.ParseSession(cookie.Value)
	if err != nil {
		t.Fatalf("解析会话失败: %v", err)
	}
	principal, err := s.svc.Auth.PrincipalFor(userID, nil, nil)
	if err != nil {
		t.Fatalf("读取主体失败: %v", err)
	}
	if err := s.db.Model(&model.Group{}).Where("id = ?", principal.Group.ID).Update("upload_rate_limit", 1).Error; err != nil {
		t.Fatalf("设置上传速率限制失败: %v", err)
	}
	createdKey := s.json(http.MethodPost, "/api/v1/keys", map[string]any{"name": "lsky-limit"}, []*http.Cookie{cookie})
	if createdKey.Status != http.StatusCreated {
		t.Fatalf("创建 API 密钥失败: %d %s", createdKey.Status, string(createdKey.Body))
	}
	token := s.decode(createdKey)["secret"].(string)
	first := s.upload("/lsky/api/v1/upload", "first.png", pngContent(t, 31), map[string]string{
		"token":       token,
		"strategy_id": fmt.Sprint(policyID),
	}, map[string]string{}, nil)
	if first.Status != http.StatusOK {
		t.Fatalf("首次上传应成功: %d %s", first.Status, string(first.Body))
	}

	body, contentType := multipartBody(t, "large.png", bytes.Repeat([]byte("x"), 2<<20), map[string]string{
		"token":       token,
		"strategy_id": fmt.Sprint(policyID),
	})
	countedBody := &countingReadCloser{reader: bytes.NewReader(body.Bytes())}
	request := httptest.NewRequest(http.MethodPost, "/lsky/api/v1/upload", countedBody)
	request.ContentLength = int64(body.Len())
	request.Header.Set("Content-Type", contentType)
	recorder := httptest.NewRecorder()

	s.engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("超限请求应返回 429, 实际 %d: %s", recorder.Code, recorder.Body.String())
	}
	if countedBody.bytesRead >= int64(body.Len())/2 {
		t.Errorf("限流拒绝前读取了大部分请求体: %d/%d 字节", countedBody.bytesRead, body.Len())
	}
}

func TestAdminPermissionDelegationIsBoundToActor(t *testing.T) {
	s := newServer(t)
	adminCookie, _ := s.bootstrap()

	highGroup := s.json(http.MethodPost, "/api/v1/admin/groups", map[string]any{
		"name":        "自定义全权组",
		"permissions": model.AllPermissions().Names(),
	}, []*http.Cookie{adminCookie})
	if highGroup.Status != http.StatusCreated {
		t.Fatalf("创建全权组失败: %d %s", highGroup.Status, string(highGroup.Body))
	}
	highGroupID := int64(s.decode(highGroup)["id"].(float64))

	settingsGroup := s.json(http.MethodPost, "/api/v1/admin/groups", map[string]any{
		"name":        "设置管理员组",
		"permissions": []string{"manage_policies", "manage_users", "manage_groups", "manage_settings", "view_logs", "view_all", "delete_all"},
	}, []*http.Cookie{adminCookie})
	if settingsGroup.Status != http.StatusCreated {
		t.Fatalf("创建设置管理员组失败: %d %s", settingsGroup.Status, string(settingsGroup.Body))
	}
	settingsGroupID := int64(s.decode(settingsGroup)["id"].(float64))
	createdUser := s.json(http.MethodPost, "/api/v1/admin/users", map[string]any{
		"username": "settingsmanager",
		"email":    "settings-manager@example.com",
		"password": "password123",
		"group_id": settingsGroupID,
	}, []*http.Cookie{adminCookie})
	if createdUser.Status != http.StatusCreated {
		t.Fatalf("创建设置管理员失败: %d %s", createdUser.Status, string(createdUser.Body))
	}
	login := s.json(http.MethodPost, "/api/v1/auth/login", map[string]any{
		"identifier": "settings-manager@example.com",
		"password":   "password123",
	}, nil)
	if login.Status != http.StatusOK {
		t.Fatalf("设置管理员登录失败: %d %s", login.Status, string(login.Body))
	}
	managerCookie := s.sessionCookie(login)
	managerID := int64(s.decode(createdUser)["id"].(float64))
	var adminGroup model.Group
	if err := s.db.Where("name = ?", "管理员").First(&adminGroup).Error; err != nil {
		t.Fatalf("读取管理员组失败: %v", err)
	}
	var adminUser model.User
	if err := s.db.Where("username = ?", "admin").First(&adminUser).Error; err != nil {
		t.Fatalf("读取管理员账号失败: %v", err)
	}
	for _, attempt := range []struct {
		name    string
		method  string
		path    string
		payload map[string]any
	}{
		{"修改管理员密码", http.MethodPatch, fmt.Sprintf("/api/v1/admin/users/%d", adminUser.ID), map[string]any{
			"password": "attacker-pass-123",
		}},
		{"封禁管理员", http.MethodPatch, fmt.Sprintf("/api/v1/admin/users/%d", adminUser.ID), map[string]any{
			"status": 2,
		}},
		{"删除管理员", http.MethodDelete, fmt.Sprintf("/api/v1/admin/users/%d", adminUser.ID), nil},
	} {
		result := s.json(attempt.method, attempt.path, attempt.payload, []*http.Cookie{managerCookie})
		if result.Status != http.StatusForbidden {
			t.Errorf("%s 应返回 403, 实际 %d %s", attempt.name, result.Status, string(result.Body))
		}
	}
	if adminLogin := s.json(http.MethodPost, "/api/v1/auth/login", map[string]any{
		"identifier": "admin@example.com",
		"password":   "password123",
	}, nil); adminLogin.Status != http.StatusOK {
		t.Errorf("被拒绝的密码修改不应影响管理员登录: %d %s", adminLogin.Status, string(adminLogin.Body))
	}

	secondAdmin := s.json(http.MethodPost, "/api/v1/admin/users", map[string]any{
		"username": "secondadmin",
		"email":    "second-admin@example.com",
		"password": "password123",
		"group_id": adminGroup.ID,
	}, []*http.Cookie{adminCookie})
	if secondAdmin.Status != http.StatusCreated {
		t.Fatalf("创建第二个管理员失败: %d %s", secondAdmin.Status, string(secondAdmin.Body))
	}
	secondAdminID := int64(s.decode(secondAdmin)["id"].(float64))
	if banned := s.json(http.MethodPatch, fmt.Sprintf("/api/v1/admin/users/%d", secondAdminID), map[string]any{
		"status": 2,
	}, []*http.Cookie{adminCookie}); banned.Status != http.StatusOK {
		t.Fatalf("封禁第二个管理员失败: %d %s", banned.Status, string(banned.Body))
	}
	if unban := s.json(http.MethodPatch, fmt.Sprintf("/api/v1/admin/users/%d", secondAdminID), map[string]any{
		"status": 0,
	}, []*http.Cookie{managerCookie}); unban.Status != http.StatusForbidden {
		t.Errorf("解封管理员应返回 403, 实际 %d %s", unban.Status, string(unban.Body))
	}

	for _, attempt := range []struct {
		name    string
		method  string
		path    string
		payload map[string]any
	}{
		{"修改管理员组", http.MethodPatch, fmt.Sprintf("/api/v1/admin/groups/%d", adminGroup.ID), map[string]any{
			"name": "被改名的管理员组",
		}},
		{"删除管理员组", http.MethodDelete, fmt.Sprintf("/api/v1/admin/groups/%d", adminGroup.ID), nil},
		{"覆盖管理员组策略", http.MethodPut, fmt.Sprintf("/api/v1/admin/groups/%d/policies", adminGroup.ID), map[string]any{
			"policy_ids": []int64{},
		}},
	} {
		result := s.json(attempt.method, attempt.path, attempt.payload, []*http.Cookie{managerCookie})
		if result.Status != http.StatusForbidden {
			t.Errorf("%s 应返回 403, 实际 %d %s", attempt.name, result.Status, string(result.Body))
		}
	}
	var reloadedAdminGroup model.Group
	if err := s.db.First(&reloadedAdminGroup, adminGroup.ID).Error; err != nil {
		t.Fatalf("读取管理员组失败: %v", err)
	}
	if reloadedAdminGroup.Name != "管理员" || reloadedAdminGroup.Permissions != model.AllPermissions() {
		t.Errorf("被拒绝的用户组操作不应改动管理员组: %+v", reloadedAdminGroup)
	}

	selfPromotion := s.json(http.MethodPatch, fmt.Sprintf("/api/v1/admin/users/%d", managerID), map[string]any{
		"group_id": adminGroup.ID,
	}, []*http.Cookie{managerCookie})
	if selfPromotion.Status != http.StatusBadRequest {
		t.Fatalf("将自己移入管理员组应返回 400, 实际 %d %s", selfPromotion.Status, string(selfPromotion.Body))
	}
	overprivilegedGroup := s.json(http.MethodPost, "/api/v1/admin/groups", map[string]any{
		"name":        "越权默认组",
		"is_default":  true,
		"permissions": model.AllPermissions().Names(),
	}, []*http.Cookie{managerCookie})
	if overprivilegedGroup.Status != http.StatusBadRequest {
		t.Fatalf("创建超出主体权限的默认组应返回 400, 实际 %d %s", overprivilegedGroup.Status, string(overprivilegedGroup.Body))
	}

	safeDefault := s.json(http.MethodPatch, "/api/v1/admin/settings", map[string]any{
		"default_group_id": settingsGroupID,
	}, []*http.Cookie{managerCookie})
	if safeDefault.Status != http.StatusOK {
		t.Fatalf("设置权限子集内的默认组应成功: %d %s", safeDefault.Status, string(safeDefault.Body))
	}

	overprivilegedDefault := s.json(http.MethodPatch, "/api/v1/admin/settings", map[string]any{
		"default_group_id": highGroupID,
	}, []*http.Cookie{managerCookie})
	if overprivilegedDefault.Status != http.StatusBadRequest {
		t.Fatalf("设置超出主体权限的默认组应返回 400, 实际 %d %s", overprivilegedDefault.Status, string(overprivilegedDefault.Body))
	}
	clearToMorePrivilegedFallback := s.json(http.MethodPatch, "/api/v1/admin/settings", map[string]any{
		"default_group_id": "",
	}, []*http.Cookie{managerCookie})
	if clearToMorePrivilegedFallback.Status != http.StatusBadRequest {
		t.Fatalf("回退到权限超出主体的默认组应返回 400, 实际 %d %s", clearToMorePrivilegedFallback.Status, string(clearToMorePrivilegedFallback.Body))
	}

	registered, _ := s.registerAndLogin("newmember", "new-member@example.com", "password123")
	newMember := s.decode(registered)
	group := newMember["group"].(map[string]any)
	if int64(group["id"].(float64)) != settingsGroupID {
		t.Errorf("新用户应进入权限受限的默认组: got %v, want %d", group["id"], settingsGroupID)
	}
}

func TestAdminGroupPatchPreservesOmittedFields(t *testing.T) {
	s := newServer(t)
	cookie, _ := s.bootstrap()
	created := s.json(http.MethodPost, "/api/v1/admin/groups", map[string]any{
		"name":                "保留字段组",
		"description":         "原始说明",
		"permissions":         []string{"upload"},
		"quota_mode":          "count",
		"quota_value":         10,
		"max_file_size_bytes": 2048,
		"upload_rate_limit":   3,
		"sort":                7,
	}, []*http.Cookie{cookie})
	if created.Status != http.StatusCreated {
		t.Fatalf("创建用户组失败: %d %s", created.Status, string(created.Body))
	}
	groupID := int64(s.decode(created)["id"].(float64))

	patched := s.json(http.MethodPatch, fmt.Sprintf("/api/v1/admin/groups/%d", groupID), map[string]any{
		"name": "改名",
	}, []*http.Cookie{cookie})
	if patched.Status != http.StatusOK {
		t.Fatalf("仅更新名称失败: %d %s", patched.Status, string(patched.Body))
	}
	group := s.decode(patched)
	permissions, _ := group["permissions"].([]any)
	if len(permissions) != 1 || permissions[0] != "upload" {
		t.Errorf("仅更新名称不应清空权限: %v", group["permissions"])
	}
	if group["description"] != "原始说明" || group["quota_mode"] != "count" ||
		group["quota_value"].(float64) != 10 || group["max_file_size_bytes"].(float64) != 2048 ||
		group["upload_rate_limit"].(float64) != 3 || group["sort"].(float64) != 7 {
		t.Errorf("仅更新名称不应清空其他字段: %v", group)
	}

	cleared := s.json(http.MethodPatch, fmt.Sprintf("/api/v1/admin/groups/%d", groupID), map[string]any{
		"permissions": []string{"delete_own"},
	}, []*http.Cookie{cookie})
	if cleared.Status != http.StatusOK {
		t.Fatalf("移除上传权限失败: %d %s", cleared.Status, string(cleared.Body))
	}
	clearedGroup := s.decode(cleared)
	if clearedGroup["quota_mode"] != nil || clearedGroup["quota_value"] != nil ||
		clearedGroup["max_file_size_bytes"] != nil || clearedGroup["upload_rate_limit"] != nil {
		t.Errorf("移除上传权限应清空配额与上传限额: %v", clearedGroup)
	}
	if clearedGroup["name"] != "改名" || clearedGroup["description"] != "原始说明" || clearedGroup["sort"].(float64) != 7 {
		t.Errorf("移除上传权限不应清空其他字段: %v", clearedGroup)
	}
}

func TestAdminAreaRequiresAnyAdminPermission(t *testing.T) {
	s := newServer(t)
	adminCookie, _ := s.bootstrap()

	newUserCookie := func(groupName, username string, permissions []string) *http.Cookie {
		t.Helper()
		group := s.json(http.MethodPost, "/api/v1/admin/groups", map[string]any{
			"name":        groupName,
			"permissions": permissions,
		}, []*http.Cookie{adminCookie})
		if group.Status != http.StatusCreated {
			t.Fatalf("创建用户组 %s 失败: %d %s", groupName, group.Status, string(group.Body))
		}
		groupID := int64(s.decode(group)["id"].(float64))
		created := s.json(http.MethodPost, "/api/v1/admin/users", map[string]any{
			"username": username,
			"email":    username + "@example.com",
			"password": "password123",
			"group_id": groupID,
		}, []*http.Cookie{adminCookie})
		if created.Status != http.StatusCreated {
			t.Fatalf("创建用户 %s 失败: %d %s", username, created.Status, string(created.Body))
		}
		login := s.json(http.MethodPost, "/api/v1/auth/login", map[string]any{
			"identifier": username + "@example.com",
			"password":   "password123",
		}, nil)
		if login.Status != http.StatusOK {
			t.Fatalf("用户 %s 登录失败: %d %s", username, login.Status, string(login.Body))
		}
		return s.sessionCookie(login)
	}

	checkAccess := func(cookie *http.Cookie, path string, want int) {
		t.Helper()
		result := s.raw(http.MethodGet, path, nil, nil, []*http.Cookie{cookie})
		if result.Status != want {
			t.Errorf("GET %s 应返回 %d, 实际 %d %s", path, want, result.Status, string(result.Body))
		}
	}

	userManagerCookie := newUserCookie("仅用户管理组", "usermanager", []string{"manage_users"})
	checkAccess(userManagerCookie, "/api/v1/admin/users", http.StatusOK)
	checkAccess(userManagerCookie, "/api/v1/admin/stats", http.StatusOK)
	checkAccess(userManagerCookie, "/api/v1/admin/groups", http.StatusForbidden)
	checkAccess(userManagerCookie, "/api/v1/admin/settings", http.StatusForbidden)
	checkAccess(userManagerCookie, "/api/v1/admin/logs", http.StatusForbidden)

	viewerCookie := newUserCookie("仅查看全部组", "viewall", []string{"view_all"})
	checkAccess(viewerCookie, "/api/v1/admin/stats", http.StatusOK)
	checkAccess(viewerCookie, "/api/v1/admin/users", http.StatusForbidden)

	uploaderCookie := newUserCookie("仅上传组", "uploadonly", []string{"upload"})
	checkAccess(uploaderCookie, "/api/v1/admin/stats", http.StatusForbidden)
	checkAccess(uploaderCookie, "/api/v1/admin/users", http.StatusForbidden)
}

func TestLskyCompatibilityLayer(t *testing.T) {
	s := newServer(t)
	cookie, policyID := s.bootstrap()

	// 未认证: 与 Laravel 默认 message 一致.
	unauthenticated := s.raw(http.MethodGet, "/lsky/api/v1/images", nil, nil, nil)
	if unauthenticated.Status != http.StatusUnauthorized {
		t.Fatalf("未认证应返回 401, 实际 %d", unauthenticated.Status)
	}
	if s.decode(unauthenticated)["message"] != "Unauthenticated." {
		t.Errorf("未认证 message 不正确: %s", string(unauthenticated.Body))
	}

	// 无效 Authorization: Authentication failed.
	invalid := s.raw(http.MethodGet, "/lsky/api/v1/images", nil, map[string]string{
		"Authorization": "Bearer pk_invalid.sk_invalid",
	}, nil)
	if invalid.Status != http.StatusUnauthorized || s.decode(invalid)["message"] != "Authentication failed." {
		t.Errorf("无效凭据响应不正确: %d %s", invalid.Status, string(invalid.Body))
	}

	strategies := s.raw(http.MethodGet, "/lsky/api/v1/strategies", nil, nil, []*http.Cookie{cookie})
	if strategies.Status != http.StatusOK {
		t.Fatalf("策略列表失败: %d %s", strategies.Status, string(strategies.Body))
	}
	strategiesPayload := s.decode(strategies)
	data := strategiesPayload["data"].(map[string]any)
	list := data["strategies"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["name"] != "主存储" {
		t.Errorf("兼容层策略列表不正确: %v", list)
	}

	// 相册: 虚拟默认相册.
	albums := s.raw(http.MethodGet, "/lsky/api/v1/albums", nil, nil, []*http.Cookie{cookie})
	albumData := s.decode(albums)["data"].(map[string]any)
	albumItems := albumData["data"].([]any)
	if albumData["total"].(float64) != 1 || albumItems[0].(map[string]any)["id"].(float64) != 1 {
		t.Errorf("虚拟默认相册不正确: %v", albumData)
	}
	if albumItems[0].(map[string]any)["name"] != "默认" {
		t.Errorf("默认相册名称不正确: %v", albumItems[0])
	}

	deleteDefault := s.raw(http.MethodDelete, "/lsky/api/v1/albums/1", nil, nil, []*http.Cookie{cookie})
	if s.decode(deleteDefault)["status"] != false {
		t.Error("默认相册不可删除")
	}
	deleteOther := s.raw(http.MethodDelete, "/lsky/api/v1/albums/9", nil, nil, []*http.Cookie{cookie})
	if s.decode(deleteOther)["status"] != true {
		t.Error("其他相册应幂等删除成功")
	}

	// 上传: 使用表单 token 字段鉴权.
	created := s.json(http.MethodPost, "/api/v1/keys", map[string]any{"name": "lsky"}, []*http.Cookie{cookie})
	secret := s.decode(created)["secret"].(string)

	uploaded := s.upload("/lsky/api/v1/upload", "lsky.png", pngContent(t, 21), map[string]string{
		"strategy_id": fmt.Sprint(policyID),
		"token":       secret,
		"album_id":    "1",
	}, map[string]string{}, nil)
	if uploaded.Status != http.StatusOK {
		t.Fatalf("兼容层上传失败: %d %s", uploaded.Status, string(uploaded.Body))
	}
	uploadPayload := s.decode(uploaded)
	if uploadPayload["status"] != true {
		t.Fatalf("兼容层上传状态不正确: %s", string(uploaded.Body))
	}
	imageData := uploadPayload["data"].(map[string]any)
	if imageData["key"] == nil || imageData["extension"] != "png" {
		t.Errorf("上传 data 字段不正确: %v", imageData)
	}
	sizeKB, ok := imageData["size"].(float64)
	if !ok || sizeKB <= 0 {
		t.Errorf("size 应为 KB 浮点: %v", imageData["size"])
	}
	links := imageData["links"].(map[string]any)
	if !strings.HasPrefix(links["url"].(string), "https://cdn.example.com/image/") {
		t.Errorf("links.url 不正确: %v", links["url"])
	}
	if !strings.HasPrefix(links["html"].(string), "&lt;img src=") {
		t.Errorf("links.html 应为转义形态: %v", links["html"])
	}
	if !strings.HasPrefix(links["markdown"].(string), "![") || links["delete_url"] != "" {
		t.Errorf("links 其他字段不正确: %v", links)
	}

	// 上传到不存在的相册.
	badAlbum := s.upload("/lsky/api/v1/upload", "again.png", pngContent(t, 22), map[string]string{
		"token":    secret,
		"album_id": "7",
	}, map[string]string{}, nil)
	if s.decode(badAlbum)["status"] != false || s.decode(badAlbum)["message"] != "选定的相册不存在" {
		t.Errorf("非法相册应返回业务失败: %s", string(badAlbum.Body))
	}

	// 列表.
	images := s.raw(http.MethodGet, "/lsky/api/v1/images?page=1&order=newest", nil, nil, []*http.Cookie{cookie})
	imagesPayload := s.decode(images)
	if imagesPayload["status"] != true {
		t.Fatalf("兼容层列表失败: %s", string(images.Body))
	}
	paginator := imagesPayload["data"].(map[string]any)
	if paginator["current_page"].(float64) != 1 || paginator["per_page"].(float64) < 1 {
		t.Errorf("分页字段不正确: %v", paginator)
	}
	rows := paginator["data"].([]any)
	if len(rows) != 1 {
		t.Fatalf("列表应返回 1 条: %v", rows)
	}
	row := rows[0].(map[string]any)
	if row["pathname"] == "" || row["origin_name"] != "lsky.png" {
		t.Errorf("列表项字段不正确: %v", row)
	}
	if row["human_date"] == "" || row["date"] == "" {
		t.Errorf("列表项应包含时间字段: %v", row)
	}
	if row["album"].(map[string]any)["id"].(float64) != 1 {
		t.Errorf("列表项相册不正确: %v", row["album"])
	}
	linkItems := paginator["links"].([]any)
	if len(linkItems) < 2 {
		t.Errorf("分页链接不正确: %v", linkItems)
	}

	// 资料.
	profile := s.raw(http.MethodGet, "/lsky/api/v1/profile", nil, nil, []*http.Cookie{cookie})
	profileData := s.decode(profile)["data"].(map[string]any)
	if profileData["name"] != "admin" || profileData["album_num"].(float64) != 1 {
		t.Errorf("资料字段不正确: %v", profileData)
	}
	if profileData["url"] != "" && profileData["url"] != nil {
		t.Errorf("未设置 site_url 时应为空串: %v", profileData["url"])
	}

	// 删除: 不存在时幂等成功.
	missing := s.raw(http.MethodDelete, "/lsky/api/v1/images/9999", nil, nil, []*http.Cookie{cookie})
	missingPayload := s.decode(missing)
	if missingPayload["status"] != true || missingPayload["message"] != "删除成功" {
		t.Errorf("删除不存在记录应幂等成功: %s", string(missing.Body))
	}
	deleted := s.raw(http.MethodDelete, fmt.Sprintf("/lsky/api/v1/images/%d", int64(imageData["key"].(float64))), nil, nil, []*http.Cookie{cookie})
	if s.decode(deleted)["status"] != true {
		t.Errorf("删除失败: %s", string(deleted.Body))
	}

	// 不实现的接口.
	for _, endpoint := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/lsky/api/v1/tokens"},
		{http.MethodDelete, "/lsky/api/v1/tokens"},
	} {
		result := s.raw(endpoint.method, endpoint.path, nil, nil, []*http.Cookie{cookie})
		if result.Status != http.StatusNotImplemented {
			t.Errorf("%s %s 应返回 501, 实际 %d", endpoint.method, endpoint.path, result.Status)
		}
		if !strings.Contains(s.decode(result)["message"].(string), "本服务不提供该接口") {
			t.Errorf("501 message 不正确: %s", string(result.Body))
		}
	}
}

func TestLskyUploadRequiresPermission(t *testing.T) {
	s := newServer(t)
	cookie, policyID := s.bootstrap()

	// 创建一个无上传权限的用户组并注册用户.
	readonly := s.json(http.MethodPost, "/api/v1/admin/groups", map[string]any{
		"name":        "无上传组",
		"permissions": []string{"manage_keys"},
	}, []*http.Cookie{cookie})
	if readonly.Status != http.StatusCreated {
		t.Fatalf("创建用户组失败: %d %s", readonly.Status, string(readonly.Body))
	}
	groupID := int64(s.decode(readonly)["id"].(float64))
	created := s.json(http.MethodPost, "/api/v1/admin/users", map[string]any{
		"username": "viewer",
		"email":    "viewer@example.com",
		"password": "password123",
		"group_id": groupID,
	}, []*http.Cookie{cookie})
	if created.Status != http.StatusCreated {
		t.Fatalf("创建用户失败: %d %s", created.Status, string(created.Body))
	}
	login := s.json(http.MethodPost, "/api/v1/auth/login", map[string]any{
		"identifier": "viewer@example.com",
		"password":   "password123",
	}, nil)
	viewerCookie := s.sessionCookie(login)
	viewerKey := s.json(http.MethodPost, "/api/v1/keys", map[string]any{"name": "viewer"}, []*http.Cookie{viewerCookie})
	secret := s.decode(viewerKey)["secret"].(string)

	denied := s.upload("/lsky/api/v1/upload", "denied.png", pngContent(t, 31), map[string]string{
		"token":       secret,
		"strategy_id": fmt.Sprint(policyID),
	}, map[string]string{}, nil)
	payload := s.decode(denied)
	if denied.Status != http.StatusOK || payload["status"] != false {
		t.Fatalf("无上传权限应以业务失败返回: %d %s", denied.Status, string(denied.Body))
	}
	if !strings.Contains(payload["message"].(string), "上传权限") {
		t.Errorf("失败消息不正确: %v", payload["message"])
	}
}

func TestFrontendFallback(t *testing.T) {
	s := newServer(t)
	result := s.raw(http.MethodGet, "/gallery", nil, nil, nil)
	if result.Status != http.StatusOK && result.Status != http.StatusServiceUnavailable {
		t.Errorf("SPA 回落状态码不正确: %d", result.Status)
	}
	if len(result.Body) == 0 {
		t.Error("SPA 回落应返回 HTML 内容")
	}
}

func TestReferrerPolicyHeader(t *testing.T) {
	s := newServer(t)
	for _, path := range []string{"/api/v1/health", "/api/v1/not-found", "/reset-password", "/lsky/not-found"} {
		result := s.raw(http.MethodGet, path, nil, nil, nil)
		if got := result.Header.Get("Referrer-Policy"); got != "no-referrer" {
			t.Errorf("%s Referrer-Policy = %q, want no-referrer", path, got)
		}
	}
}

func TestLskyRejectsQueryToken(t *testing.T) {
	s := newServer(t)
	cookie, _ := s.bootstrap()

	created := s.json(http.MethodPost, "/api/v1/keys", map[string]any{"name": "query-token"}, []*http.Cookie{cookie})
	secret := s.decode(created)["secret"].(string)

	// 查询参数不再是兼容层的认证方式: 避免密钥进入访问日志.
	viaQuery := s.raw(http.MethodGet, "/lsky/api/v1/images?token="+secret, nil, nil, nil)
	if viaQuery.Status != http.StatusUnauthorized {
		t.Errorf("查询参数 token 不应被接受, 实际 %d %s", viaQuery.Status, string(viaQuery.Body))
	}
	viaHeader := s.raw(http.MethodGet, "/lsky/api/v1/images", nil, map[string]string{"Authorization": "Bearer " + secret}, nil)
	if viaHeader.Status != http.StatusOK {
		t.Errorf("Authorization 头应可用: %d %s", viaHeader.Status, string(viaHeader.Body))
	}
}

func TestJSONBodySizeLimit(t *testing.T) {
	s := newServer(t)

	// 未认证接口也必须拒绝超大请求体, 不能先物化到内存.
	huge := strings.Repeat("a", 4<<20)
	result := s.json(http.MethodPost, "/api/v1/auth/login", map[string]any{
		"identifier": huge,
		"password":   huge,
	}, nil)
	if result.Status != http.StatusBadRequest {
		t.Errorf("超大 JSON 请求体应被拒绝, 实际 %d", result.Status)
	}

	s.bootstrap()
	cookie := s.json(http.MethodPost, "/api/v1/auth/login", map[string]any{
		"identifier": "admin@example.com",
		"password":   "password123",
	}, nil)
	session := s.sessionCookie(cookie)
	oversized := s.json(http.MethodPatch, "/api/v1/admin/settings", map[string]any{
		"site_name": strings.Repeat("b", 2<<20),
	}, []*http.Cookie{session})
	if oversized.Status != http.StatusBadRequest {
		t.Errorf("超大可写请求体应被拒绝, 实际 %d", oversized.Status)
	}
}

func TestRegisterEmailAllowlistAPI(t *testing.T) {
	s := newServer(t)
	if err := s.svc.Settings.Update(map[string]string{
		"registration_email_allowlist": "example.com",
	}); err != nil {
		t.Fatalf("设置邮箱白名单失败: %v", err)
	}

	blocked := s.json(http.MethodPost, "/api/v1/auth/register", map[string]any{
		"username": "blocked",
		"email":    "blocked@example.net",
		"password": "password123",
	}, nil)
	if blocked.Status != http.StatusForbidden || s.decode(blocked)["code"] != "email_not_allowed" {
		t.Fatalf("非白名单邮箱应返回 email_not_allowed, 实际 %d %s", blocked.Status, string(blocked.Body))
	}

	allowed := s.json(http.MethodPost, "/api/v1/auth/register", map[string]any{
		"username": "allowed",
		"email":    "allowed@EXAMPLE.COM",
		"password": "password123",
	}, nil)
	if allowed.Status != http.StatusAccepted {
	}
}
