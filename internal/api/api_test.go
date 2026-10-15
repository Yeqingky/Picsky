package api_test

import (
	"bufio"
	"bytes"
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
	for key, value := range fields {
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
	registered := s.json(http.MethodPost, "/api/v1/auth/register", map[string]any{
		"username": "admin",
		"email":    "admin@example.com",
		"password": "password123",
	}, nil)
	if registered.Status != http.StatusCreated {
		s.t.Fatalf("注册失败: %d %s", registered.Status, string(registered.Body))
	}
	cookie := s.sessionCookie(registered)

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

func TestClientIPSourceDiscoveryAndLoginRateLimit(t *testing.T) {
	s := newServer(t)

	unauthorized := s.raw(http.MethodGet, "/api/v1/admin/settings/client-ip-sources", nil, nil, nil)
	if unauthorized.Status != http.StatusUnauthorized {
		t.Fatalf("IP 来源探测接口必须认证, 实际状态码 %d", unauthorized.Status)
	}

	registered := s.json(http.MethodPost, "/api/v1/auth/register", map[string]any{
		"username": "admin",
		"email":    "admin@example.com",
		"password": "password123",
	}, nil)
	if registered.Status != http.StatusCreated {
		t.Fatalf("注册管理员失败: %d %s", registered.Status, string(registered.Body))
	}
	cookie := s.sessionCookie(registered)

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

	loginBody, err := json.Marshal(map[string]string{"identifier": "missing-user", "password": "invalid"})
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
		"identifier": "admin@example.com",
		"password":   "password123",
	}, nil)
	if login.Status != http.StatusOK {
		t.Fatalf("登录失败: %d %s", login.Status, string(login.Body))
	}
	loginCookie := s.sessionCookie(login)

	badLogin := s.json(http.MethodPost, "/api/v1/auth/login", map[string]any{
		"identifier": "admin",
		"password":   "wrong-password",
	}, nil)
	if badLogin.Status != http.StatusUnauthorized {
		t.Errorf("错误密码应返回 401, 实际 %d", badLogin.Status)
	}

	changed := s.json(http.MethodPatch, "/api/v1/auth/password", map[string]any{
		"old_password": "password123",
		"new_password": "password456",
	}, []*http.Cookie{loginCookie})
	if changed.Status != http.StatusNoContent {
		t.Errorf("修改密码失败: %d %s", changed.Status, string(changed.Body))
	}
	if relogin := s.json(http.MethodPost, "/api/v1/auth/login", map[string]any{
		"identifier": "admin",
		"password":   "password456",
	}, nil); relogin.Status != http.StatusOK {
		t.Errorf("新密码登录失败: %d %s", relogin.Status, string(relogin.Body))
	}

	logout := s.raw(http.MethodPost, "/api/v1/auth/logout", nil, nil, []*http.Cookie{loginCookie})
	if logout.Status != http.StatusNoContent {
		t.Errorf("登出失败: %d", logout.Status)
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

	registered := s.json(http.MethodPost, "/api/v1/auth/register", map[string]any{
		"username": "reset-user",
		"email":    "reset@example.com",
		"password": "old-password123",
	}, nil)
	if registered.Status != http.StatusCreated {
		t.Fatalf("注册失败: %d %s", registered.Status, string(registered.Body))
	}
	userID := int64(s.decode(registered)["id"].(float64))
	secret, _, err := s.svc.Auth.TOTPSetup(userID)
	if err != nil {
		t.Fatalf("生成 TOTP 秘钥失败: %v", err)
	}
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("生成 TOTP 验证码失败: %v", err)
	}
	if err := s.svc.Auth.TOTPEnable(userID, secret, code, "127.0.0.1", "test"); err != nil {
		t.Fatalf("启用 TOTP 失败: %v", err)
	}
	beforeResetLogin := s.json(http.MethodPost, "/api/v1/auth/login", map[string]any{
		"identifier": "reset-user",
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
		"email": "reset@example.com",
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
	const linkPrefix = "https://picsky.example/reset-password?token="
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
	if replay := s.json(http.MethodPost, "/api/v1/auth/password/reset", map[string]any{
		"token":    token,
		"password": "replayed-password789",
	}, nil); replay.Status != http.StatusBadRequest {
		t.Errorf("重置令牌重放应被拒绝, 实际 %d %s", replay.Status, string(replay.Body))
	}
	if oldPassword := s.json(http.MethodPost, "/api/v1/auth/login", map[string]any{
		"identifier": "reset-user",
		"password":   "old-password123",
	}, nil); oldPassword.Status != http.StatusUnauthorized {
		t.Errorf("重置后旧密码应失效, 实际 %d %s", oldPassword.Status, string(oldPassword.Body))
	}
	newPassword := s.json(http.MethodPost, "/api/v1/auth/login", map[string]any{
		"identifier": "reset-user",
		"password":   "new-password456",
	}, nil)
	if newPassword.Status != http.StatusOK {
		t.Errorf("重置后新密码应直接登录且无需 TOTP, 实际 %d %s", newPassword.Status, string(newPassword.Body))
	}
}
func TestLoginTwoStepTotpAPI(t *testing.T) {
	s := newServer(t)
	registered := s.json(http.MethodPost, "/api/v1/auth/register", map[string]any{
		"username": "two-step",
		"email":    "two-step@example.com",
		"password": "password123",
	}, nil)
	if registered.Status != http.StatusCreated {
		t.Fatalf("注册失败: %d %s", registered.Status, string(registered.Body))
	}
	cookie := s.sessionCookie(registered)

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
	enabled := s.json(http.MethodPost, "/api/v1/auth/totp/enable", map[string]any{
		"secret": secret,
		"code":   code,
	}, []*http.Cookie{cookie})
	if enabled.Status != http.StatusNoContent {
		t.Fatalf("启用 TOTP 失败: %d %s", enabled.Status, string(enabled.Body))
	}

	first := s.json(http.MethodPost, "/api/v1/auth/login", map[string]any{
		"identifier": "two-step",
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

	code, err = totp.GenerateCode(secret, time.Now().UTC())
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
	if item["width"].(float64) != 8 || item["height"].(float64) != 8 {
		t.Errorf("列表项尺寸不正确: %v", item)
	}

	detail := s.raw(http.MethodGet, fmt.Sprintf("/api/v1/images/%d", id), nil, nil, []*http.Cookie{cookie})
	if detail.Status != http.StatusOK {
		t.Errorf("详情失败: %d %s", detail.Status, string(detail.Body))
	}

	renamed := s.json(http.MethodPatch, fmt.Sprintf("/api/v1/images/%d", id), map[string]any{
		"original_filename": "renamed",
	}, []*http.Cookie{cookie})
	if renamed.Status != http.StatusOK || s.decode(renamed)["original_filename"] != "renamed.png" {
		t.Errorf("改名失败: %d %s", renamed.Status, string(renamed.Body))
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
	scopedRegister := s.json(http.MethodPost, "/api/v1/auth/register", map[string]any{
		"username": "scoped",
		"email":    "scoped@example.com",
		"password": "password123",
	}, nil)
	if scopedRegister.Status != http.StatusCreated {
		t.Fatalf("注册普通用户失败: %d %s", scopedRegister.Status, string(scopedRegister.Body))
	}
	scopedCookie := s.sessionCookie(scopedRegister)
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

	disabled := s.json(http.MethodPatch, fmt.Sprintf("/api/v1/admin/users/%d", userID), map[string]any{
		"status": 1,
	}, []*http.Cookie{cookie})
	if disabled.Status != http.StatusOK {
		t.Fatalf("禁用用户失败: %d %s", disabled.Status, string(disabled.Body))
	}

	aliceLogin := s.json(http.MethodPost, "/api/v1/auth/login", map[string]any{
		"identifier": "alice",
		"password":   "password123",
	}, nil)
	if aliceLogin.Status != http.StatusForbidden {
		t.Errorf("被禁用账号登录应返回 403, 实际 %d %s", aliceLogin.Status, string(aliceLogin.Body))
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
	if scanned.Status != http.StatusOK {
		t.Errorf("扫描接口失败: %d %s", scanned.Status, string(scanned.Body))
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
		"identifier": "alice",
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
		"identifier": "viewer",
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
		"identifier": "admin",
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
	if allowed.Status != http.StatusCreated {
		t.Fatalf("白名单域名应允许注册, 实际 %d %s", allowed.Status, string(allowed.Body))
	}
}
