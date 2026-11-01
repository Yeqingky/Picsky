// Package acgfaka 实现异次元店铺系统 (acg-faka) 的工单图片上传驱动.
// 支持手动填写 USER_SESSION Cookie, 或使用账号密码登录并自动刷新 Cookie.
package acgfaka

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/yeqingky/Picsky/internal/storage"
)

const (
	driverType = "acgfaka"

	// uploadPath 是会员上传工单图片的接口; 站点地址可包含子路径.
	uploadPath = "/user/api/ticket/upload"
	// loginPath 与 totpPath 是会员账号登录与两步验证接口.
	loginPath = "/user/api/authentication/login"
	totpPath  = "/user/api/authentication/totp"
	// deviceSessionsPath 返回会员当前设备会话, 用于读取 USER_SESSION 到期时间.
	deviceSessionsPath = "/user/api/security/deviceSessions"
	devicePath         = "/user/security/device"

	// maxFileSize 与 Ticket 服务的 MAX_IMAGE_BYTES 保持一致 (10 MiB).
	maxFileSize = 10 << 20
	// maxResponseBytes 限制响应体读取, 防止异常后端拖垮进程.
	maxResponseBytes = 1 << 20
	// maxCachedSessions 限制进程内自动登录 Cookie 缓存条目数.
	maxCachedSessions = 64
)

// probePNG 是连接测试使用的 1x1 PNG.
var probePNG = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d,
	0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4, 0x89, 0x00, 0x00, 0x00,
	0x0d, 0x49, 0x44, 0x41, 0x54, 0x78, 0xda, 0x63, 0xfc, 0xcf, 0xc0, 0x50,
	0x0f, 0x00, 0x04, 0x85, 0x01, 0x80, 0x84, 0xa9, 0x8c, 0x21, 0x00, 0x00,
	0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
}

// sessionCache 保存自动登录得到的 Cookie, 避免每个上传请求都重新登录.
var (
	sessionCacheMu sync.Mutex
	sessionCache   = map[string]string{}
)

func init() {
	storage.Register(storage.Spec{
		Type:         driverType,
		Label:        "异次元店铺系统",
		Capabilities: []string{"upload"},
		ConfigFields: []storage.Field{
			{Name: "endpoint", Label: "站点地址", Required: true, Placeholder: "https://example.com/"},
			{Name: "auto_login", Label: "自动登录", Type: "switch"},
		},
		SecretFields: []storage.Field{
			{Name: "cookie", Label: "USER_SESSION Cookie", Required: true, Placeholder: "USER_SESSION 的值或完整 Cookie"},
			{Name: "username", Label: "账号"},
			{Name: "password", Label: "密码"},
		},
		URLFromBackend: true,
		MaxFileSize:    maxFileSize,
		Formats:        []string{"jpeg", "png", "webp"},
	}, newAdapter)
}

type adapter struct {
	site      *url.URL
	uploadURL string
	referer   string
	origin    string
	cookie    string
	username  string
	password  string
	autoLogin bool
	client    *http.Client
}

func newAdapter(opts storage.Options) (storage.Adapter, error) {
	site, err := parseSite(opts.Config["endpoint"])
	if err != nil {
		return nil, err
	}
	autoLogin := strings.EqualFold(strings.TrimSpace(opts.Config["auto_login"]), "true")
	username := strings.TrimSpace(opts.Secrets["username"])
	password := opts.Secrets["password"]
	if autoLogin {
		if username == "" {
			return nil, errors.New("异次元店铺系统自动登录需要账号")
		}
		if password == "" {
			return nil, errors.New("异次元店铺系统自动登录需要密码")
		}
	}
	rawCookie := opts.Secrets["cookie"]
	if autoLogin {
		if cached, ok := cachedSession(sessionKey(site, username, password)); ok {
			rawCookie = cached
		}
	}
	cookie, err := normalizeCookie(rawCookie)
	if err != nil {
		return nil, err
	}

	upload := buildURL(site, uploadPath)
	upload.RawQuery = "mime=image"

	return &adapter{
		site:      site,
		uploadURL: upload.String(),
		referer:   site.String(),
		origin:    site.Scheme + "://" + site.Host,
		cookie:    cookie,
		username:  username,
		password:  password,
		autoLogin: autoLogin,
		client: &http.Client{
			Timeout: 5 * time.Minute,
		},
	}, nil
}

func (a *adapter) Type() string { return driverType }

// Capabilities 只声明上传能力: 会员接口不提供对象查询, 列举, 读取或删除.
func (a *adapter) Capabilities() storage.Capability { return storage.CapUpload }

// Put 以 multipart 上传单张图片; 后端返回站点内相对路径, 驱动补全为绝对直链.
// 自动登录开启且后端提示会话失效时, 使用保存的账号密码重新登录并重试一次.
func (a *adapter) Put(ctx context.Context, key string, body io.Reader, size int64, contentType string) (storage.PutResult, error) {
	if err := storage.ValidateObjectKey(key); err != nil {
		return storage.PutResult{}, err
	}
	if size <= 0 {
		return storage.PutResult{}, errors.New("上传内容为空")
	}
	if size > maxFileSize {
		return storage.PutResult{}, fmt.Errorf("异次元店铺系统单文件最大支持 %d 字节", int64(maxFileSize))
	}
	contentType = strings.TrimSpace(contentType)
	if !strings.HasPrefix(strings.ToLower(contentType), "image/") || strings.ContainsAny(contentType, "\r\n") {
		return storage.PutResult{}, errors.New("异次元店铺系统驱动只支持图片")
	}

	result, sessionExpired, err := a.doPut(ctx, key, body, size, contentType)
	if err == nil || !sessionExpired || !a.autoLogin {
		return result, err
	}

	if refreshErr := a.refreshCookie(ctx); refreshErr != nil {
		return storage.PutResult{}, fmt.Errorf("%w; 自动刷新 Cookie 失败: %v", err, refreshErr)
	}
	seeker, ok := body.(io.Seeker)
	if !ok {
		return storage.PutResult{}, fmt.Errorf("%w; 上传内容不可重读, 无法自动重试", err)
	}
	if _, seekErr := seeker.Seek(0, io.SeekStart); seekErr != nil {
		return storage.PutResult{}, fmt.Errorf("%w; 重置上传内容失败: %v", err, seekErr)
	}
	result, _, err = a.doPut(ctx, key, body, size, contentType)
	return result, err
}

// doPut 执行一次上传; sessionExpired 表示后端明确返回会话失效.
func (a *adapter) doPut(ctx context.Context, key string, body io.Reader, size int64, contentType string) (storage.PutResult, bool, error) {
	boundary, err := storage.RandomString(16)
	if err != nil {
		return storage.PutResult{}, false, fmt.Errorf("生成 multipart 边界失败: %w", err)
	}
	boundary = "----Picsky" + boundary
	filename := sanitizeFilename(path.Base(key))
	preamble := "--" + boundary + "\r\n" +
		`Content-Disposition: form-data; name="file"; filename="` + filename + `"` + "\r\n" +
		"Content-Type: " + contentType + "\r\n\r\n"
	epilogue := "\r\n--" + boundary + "--\r\n"
	payload := io.MultiReader(
		strings.NewReader(preamble),
		io.LimitReader(body, size),
		strings.NewReader(epilogue),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.uploadURL, payload)
	if err != nil {
		return storage.PutResult{}, false, err
	}
	req.ContentLength = int64(len(preamble)) + size + int64(len(epilogue))
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Cookie", a.cookie)
	// acg-faka 的 UserSession 拦截器要求 API 请求的 Referer 主机与站点一致.
	req.Header.Set("Referer", a.referer)
	req.Header.Set("Origin", a.origin)

	resp, err := a.client.Do(req)
	if err != nil {
		return storage.PutResult{}, false, fmt.Errorf("异次元店铺系统上传失败: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return storage.PutResult{}, false, fmt.Errorf("读取异次元店铺系统响应失败: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return storage.PutResult{}, false, fmt.Errorf("异次元店铺系统上传失败: HTTP %d: %s", resp.StatusCode, snippet(data))
	}

	var result struct {
		Code int             `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
		URL  string          `json:"url"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return storage.PutResult{}, false, fmt.Errorf("解析异次元店铺系统响应失败: %w", err)
	}
	if result.Code != http.StatusOK {
		message := strings.TrimSpace(result.Msg)
		if message == "" {
			message = snippet(data)
		}
		return storage.PutResult{}, sessionErrorMessage(message), fmt.Errorf("异次元店铺系统上传失败: code=%d: %s", result.Code, message)
	}
	rawURL, err := uploadPathFromData(result.Data, result.URL)
	if err != nil {
		return storage.PutResult{}, false, err
	}
	link, err := absoluteURL(a.site, rawURL)
	if err != nil {
		return storage.PutResult{}, false, err
	}
	return storage.PutResult{URL: link}, false, nil
}

// refreshCookie 使用保存的账号密码重新登录并更新进程内缓存.
func (a *adapter) refreshCookie(ctx context.Context) error {
	result, err := LoginPassword(ctx, a.site.String(), a.username, a.password)
	if err != nil {
		return err
	}
	if result.TwoFactor {
		return errors.New("账号需要两步验证, 请重新登录")
	}
	if strings.TrimSpace(result.Cookie) == "" {
		return errors.New("登录后未获取到 USER_SESSION")
	}
	a.cookie = result.Cookie
	return nil
}

// uploadPathFromData 兼容 data.url, data.path, data 字符串与顶层 url.
func uploadPathFromData(data json.RawMessage, fallback string) (string, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && string(trimmed) != "null" {
		if trimmed[0] == '"' {
			var value string
			if err := json.Unmarshal(trimmed, &value); err != nil {
				return "", fmt.Errorf("解析异次元店铺系统图片地址失败: %w", err)
			}
			if strings.TrimSpace(value) != "" {
				return value, nil
			}
		} else {
			var value struct {
				URL  string `json:"url"`
				Path string `json:"path"`
			}
			if err := json.Unmarshal(trimmed, &value); err != nil {
				return "", fmt.Errorf("解析异次元店铺系统图片地址失败: %w", err)
			}
			if strings.TrimSpace(value.URL) != "" {
				return value.URL, nil
			}
			if strings.TrimSpace(value.Path) != "" {
				return value.Path, nil
			}
		}
	}
	if strings.TrimSpace(fallback) != "" {
		return fallback, nil
	}
	return "", errors.New("异次元店铺系统未返回图片地址")
}

// absoluteURL 校验后端直链, 并把站点内相对路径补全为绝对地址.
func absoluteURL(site *url.URL, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("异次元店铺系统返回的图片地址为空")
	}
	if strings.HasPrefix(raw, "//") {
		return "", fmt.Errorf("异次元店铺系统返回的图片地址无效: %q", raw)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("异次元店铺系统返回的图片地址无效: %w", err)
	}
	if parsed.IsAbs() {
		if err := validateLink(parsed); err != nil {
			return "", fmt.Errorf("异次元店铺系统返回的直链无效: %w", err)
		}
		return parsed.String(), nil
	}
	resolved := site.ResolveReference(parsed)
	if err := validateLink(resolved); err != nil {
		return "", fmt.Errorf("异次元店铺系统返回的直链无效: %w", err)
	}
	return resolved.String(), nil
}

func validateLink(parsed *url.URL) error {
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("直链必须以 http:// 或 https:// 开头")
	}
	if parsed.Host == "" {
		return errors.New("直链缺少主机名")
	}
	if parsed.User != nil {
		return errors.New("直链不能包含用户信息")
	}
	if parsed.Path == "" || parsed.Path == "/" {
		return errors.New("直链缺少路径")
	}
	return nil
}

func (a *adapter) Stat(context.Context, string) (storage.ObjectInfo, error) {
	return storage.ObjectInfo{}, storage.ErrNotSupported
}

func (a *adapter) Delete(context.Context, string) error {
	return storage.ErrNotSupported
}

func (a *adapter) List(context.Context, string) ([]storage.ObjectInfo, error) {
	return nil, storage.ErrNotSupported
}

func (a *adapter) Read(context.Context, string) (io.ReadCloser, error) {
	return nil, storage.ErrNotSupported
}

// Test 上传一张探测图片; 会员接口不支持删除, 探测对象会保留.
func (a *adapter) Test(ctx context.Context, prefix string) error {
	suffix, err := storage.RandomString(8)
	if err != nil {
		return err
	}
	key := storage.NormalizeObjectKey(prefix, "picsky-probe-"+suffix+".png")
	_, err = a.Put(ctx, key, bytes.NewReader(probePNG), int64(len(probePNG)), "image/png")
	return err
}

// AuthResult 是会员账号登录结果.
type AuthResult struct {
	TwoFactor     bool
	SessionCookie string
	Cookie        string
	CookieExpires string
}

// LoginPassword 使用账号密码登录; 账号可为用户名, 邮箱或手机号.
// 需要两步验证时返回 SessionCookie, 供 CompleteTwoFactor 继续完成登录.
func LoginPassword(ctx context.Context, endpoint, account, password string) (*AuthResult, error) {
	site, err := parseSite(endpoint)
	if err != nil {
		return nil, err
	}
	account = strings.TrimSpace(account)
	if account == "" {
		return nil, errors.New("异次元店铺系统账号必填")
	}
	if password == "" {
		return nil, errors.New("异次元店铺系统密码必填")
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("创建 Cookie 容器失败: %w", err)
	}
	client := &http.Client{Timeout: 30 * time.Second, Jar: jar}
	form := url.Values{
		"username": {account},
		"password": {password},
		"remember": {"0"},
	}
	resp, data, err := doFormRequest(ctx, client, buildURL(site, loginPath).String(), form, site.String(), site.Scheme+"://"+site.Host)
	if err != nil {
		return nil, fmt.Errorf("登录异次元店铺系统失败: %w", err)
	}
	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("解析异次元店铺系统登录响应失败: %w", err)
	}
	if result.Code == 42001 {
		sessionCookie, err := jarCookieHeader(jar, site)
		if err != nil {
			return nil, fmt.Errorf("异次元店铺系统两步验证会话无效: %w", err)
		}
		return &AuthResult{TwoFactor: true, SessionCookie: sessionCookie}, nil
	}
	if result.Code != http.StatusOK {
		message := strings.TrimSpace(result.Msg)
		if message == "" {
			message = snippet(data)
		}
		return nil, fmt.Errorf("登录异次元店铺系统失败: code=%d: %s", result.Code, message)
	}
	cookie, err := jarCookieHeader(jar, site)
	if err != nil {
		return nil, err
	}
	if _, err := findCookieValue(cookie, "USER_SESSION"); err != nil {
		return nil, errors.New("异次元店铺系统登录后未获取到 USER_SESSION")
	}
	expires := cookieExpiryText(resp.Cookies())
	if text, expiryErr := sessionExpiry(ctx, site, cookie); expiryErr == nil {
		expires = text
	}
	cacheSession(sessionKey(site, account, password), cookie)
	return &AuthResult{Cookie: cookie, CookieExpires: expires}, nil
}

// CompleteTwoFactor 使用登录返回的临时会话完成两步验证.
func CompleteTwoFactor(ctx context.Context, endpoint, sessionCookie, code string) (*AuthResult, error) {
	site, err := parseSite(endpoint)
	if err != nil {
		return nil, err
	}
	sessionCookie = strings.TrimSpace(sessionCookie)
	if sessionCookie == "" {
		return nil, errors.New("异次元店铺系统两步验证会话为空")
	}
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, errors.New("异次元店铺系统动态口令不能为空")
	}
	jar, err := jarFromCookieHeader(site, sessionCookie)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 30 * time.Second, Jar: jar}
	form := url.Values{"code": {code}}
	resp, data, err := doFormRequest(ctx, client, buildURL(site, totpPath).String(), form, site.String(), site.Scheme+"://"+site.Host)
	if err != nil {
		return nil, fmt.Errorf("完成异次元店铺系统两步验证失败: %w", err)
	}
	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("解析异次元店铺系统两步验证响应失败: %w", err)
	}
	if result.Code != http.StatusOK {
		message := strings.TrimSpace(result.Msg)
		if message == "" {
			message = snippet(data)
		}
		return nil, fmt.Errorf("完成异次元店铺系统两步验证失败: code=%d: %s", result.Code, message)
	}
	cookie, err := jarCookieHeader(jar, site)
	if err != nil {
		return nil, err
	}
	if _, err := findCookieValue(cookie, "USER_SESSION"); err != nil {
		return nil, errors.New("异次元店铺系统两步验证后未获取到 USER_SESSION")
	}
	expires := cookieExpiryText(resp.Cookies())
	if text, expiryErr := sessionExpiry(ctx, site, cookie); expiryErr == nil {
		expires = text
	}
	return &AuthResult{Cookie: cookie, CookieExpires: expires}, nil
}

// SessionExpiry 读取当前 USER_SESSION 的设备会话到期时间.
func SessionExpiry(ctx context.Context, endpoint, cookie string) (string, error) {
	site, err := parseSite(endpoint)
	if err != nil {
		return "", err
	}
	normalized, err := normalizeCookie(cookie)
	if err != nil {
		return "", err
	}
	return sessionExpiry(ctx, site, normalized)
}

func sessionExpiry(ctx context.Context, site *url.URL, cookie string) (string, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, buildURL(site, deviceSessionsPath).String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Cookie", cookie)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", buildURL(site, devicePath).String())
	req.Header.Set("Origin", site.Scheme+"://"+site.Host)
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("读取异次元店铺系统会话失败: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return "", fmt.Errorf("读取异次元店铺系统会话响应失败: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("读取异次元店铺系统会话失败: HTTP %d: %s", resp.StatusCode, snippet(data))
	}
	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			List []struct {
				Current     bool   `json:"current"`
				ExpiresTime string `json:"expires_time"`
			} `json:"list"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("解析异次元店铺系统会话响应失败: %w", err)
	}
	if result.Code != http.StatusOK {
		message := strings.TrimSpace(result.Msg)
		if message == "" {
			message = snippet(data)
		}
		return "", fmt.Errorf("读取异次元店铺系统会话失败: code=%d: %s", result.Code, message)
	}
	for _, item := range result.Data.List {
		if item.Current {
			if strings.TrimSpace(item.ExpiresTime) == "" {
				return "", errors.New("异次元店铺系统未返回当前会话到期时间")
			}
			return item.ExpiresTime, nil
		}
	}
	return "", errors.New("异次元店铺系统未找到当前会话")
}

func doFormRequest(ctx context.Context, client *http.Client, target string, form url.Values, referer, origin string) (*http.Response, []byte, error) {
	body := form.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Referer", referer)
	req.Header.Set("Origin", origin)
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, nil, fmt.Errorf("读取响应失败: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return resp, data, fmt.Errorf("HTTP %d: %s", resp.StatusCode, snippet(data))
	}
	return resp, data, nil
}

func parseSite(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("异次元店铺系统站点地址 (endpoint) 必填")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("异次元店铺系统站点地址非法: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("异次元店铺系统站点地址必须以 http:// 或 https:// 开头")
	}
	if parsed.Host == "" {
		return nil, errors.New("异次元店铺系统站点地址缺少主机名")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("异次元店铺系统站点地址不能包含查询参数或片段")
	}
	if parsed.User != nil {
		return nil, errors.New("异次元店铺系统站点地址不能包含用户信息")
	}
	return &url.URL{
		Scheme: parsed.Scheme,
		Host:   parsed.Host,
		Path:   strings.TrimRight(parsed.Path, "/") + "/",
	}, nil
}

func buildURL(site *url.URL, targetPath string) *url.URL {
	result := *site
	result.Path = strings.TrimRight(site.Path, "/") + targetPath
	result.RawPath = ""
	result.RawQuery = ""
	result.Fragment = ""
	return &result
}

func sessionKey(site *url.URL, account, password string) string {
	value := site.String() + "\x00" + strings.TrimSpace(account) + "\x00" + password
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", sum)
}

func cachedSession(key string) (string, bool) {
	sessionCacheMu.Lock()
	defer sessionCacheMu.Unlock()
	cookie, ok := sessionCache[key]
	return cookie, ok
}

func cacheSession(key, cookie string) {
	if key == "" || cookie == "" {
		return
	}
	sessionCacheMu.Lock()
	defer sessionCacheMu.Unlock()
	if len(sessionCache) >= maxCachedSessions {
		sessionCache = map[string]string{}
	}
	sessionCache[key] = cookie
}

func sessionErrorMessage(message string) bool {
	return strings.Contains(message, "会话") ||
		strings.Contains(message, "重新登录") ||
		strings.Contains(message, "登录已失效")
}

func jarCookieHeader(jar http.CookieJar, site *url.URL) (string, error) {
	cookies := jar.Cookies(site)
	if len(cookies) == 0 {
		return "", errors.New("异次元店铺系统未返回 Cookie")
	}
	parts := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		if cookie.Name == "" {
			continue
		}
		parts = append(parts, cookie.Name+"="+cookie.Value)
	}
	if len(parts) == 0 {
		return "", errors.New("异次元店铺系统未返回有效 Cookie")
	}
	return strings.Join(parts, "; "), nil
}

func jarFromCookieHeader(site *url.URL, raw string) (*cookiejar.Jar, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("创建 Cookie 容器失败: %w", err)
	}
	pairs, err := parseCookieInput(raw)
	if err != nil {
		return nil, err
	}
	cookies := make([]*http.Cookie, 0, len(pairs))
	for _, pair := range pairs {
		cookies = append(cookies, &http.Cookie{Name: pair.name, Value: pair.value, Path: "/"})
	}
	jar.SetCookies(site, cookies)
	return jar, nil
}

func cookieExpiryText(cookies []*http.Cookie) string {
	for _, cookie := range cookies {
		if cookie.Name != "USER_SESSION" {
			continue
		}
		if cookie.MaxAge > 0 {
			return time.Now().Add(time.Duration(cookie.MaxAge) * time.Second).UTC().Format(time.RFC3339)
		}
		if !cookie.Expires.IsZero() {
			return cookie.Expires.UTC().Format(time.RFC3339)
		}
	}
	return ""
}

func findCookieValue(raw, name string) (string, error) {
	pairs, err := parseCookieInput(raw)
	if err != nil {
		return "", err
	}
	for _, pair := range pairs {
		if pair.name == name {
			if pair.value == "" {
				return "", fmt.Errorf("Cookie %s 不能为空", name)
			}
			return pair.value, nil
		}
	}
	return "", fmt.Errorf("Cookie 必须包含 %s", name)
}

type cookiePair struct {
	name  string
	value string
}

func normalizeCookie(raw string) (string, error) {
	pairs, err := parseCookieInput(raw)
	if err != nil {
		return "", err
	}
	hasSession := false
	for _, pair := range pairs {
		if pair.name == "USER_SESSION" {
			if pair.value == "" {
				return "", errors.New("异次元店铺系统 USER_SESSION Cookie 不能为空")
			}
			hasSession = true
		}
	}
	if !hasSession {
		return "", errors.New("异次元店铺系统 Cookie 必须包含 USER_SESSION")
	}
	return pairsToHeader(pairs), nil
}

func parseCookieInput(raw string) ([]cookiePair, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("异次元店铺系统 Cookie 必填")
	}
	if strings.ContainsAny(raw, "\r\n\x00") {
		return nil, errors.New("异次元店铺系统 Cookie 不能包含换行或空字符")
	}
	if !isCookieHeader(raw) {
		return []cookiePair{{name: "USER_SESSION", value: raw}}, nil
	}

	parts := strings.Split(raw, ";")
	pairs := make([]cookiePair, 0, len(parts))
	seen := map[string]bool{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, value, ok := strings.Cut(part, "=")
		if !ok {
			return nil, fmt.Errorf("异次元店铺系统 Cookie 段格式无效: %q", part)
		}
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		if !validCookieName(name) {
			return nil, fmt.Errorf("异次元店铺系统 Cookie 名称无效: %q", name)
		}
		if strings.ContainsAny(value, "\r\n\x00;") {
			return nil, fmt.Errorf("异次元店铺系统 Cookie 值无效: %q", name)
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		pairs = append(pairs, cookiePair{name: name, value: value})
	}
	return pairs, nil
}

func pairsToHeader(pairs []cookiePair) string {
	parts := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		parts = append(parts, pair.name+"="+pair.value)
	}
	return strings.Join(parts, "; ")
}

func isCookieHeader(raw string) bool {
	if strings.Contains(raw, ";") {
		return true
	}
	upper := strings.ToUpper(raw)
	return strings.HasPrefix(upper, "USER_SESSION=") ||
		strings.HasPrefix(upper, "ACG-SHOP=") ||
		strings.HasPrefix(upper, "ACG_CSP=")
}

func validCookieName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if r <= 0x20 || r >= 0x7f {
			return false
		}
		if strings.ContainsRune("()<>@,;:\\\"/[]?={}", r) {
			return false
		}
	}
	return true
}

func sanitizeFilename(name string) string {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == "/" {
		return "image.png"
	}
	replacer := strings.NewReplacer("\\", "_", "\"", "_", "\r", "_", "\n", "_")
	return replacer.Replace(name)
}

func snippet(data []byte) string {
	value := strings.TrimSpace(string(data))
	if runes := []rune(value); len(runes) > 200 {
		value = string(runes[:200])
	}
	return value
}
