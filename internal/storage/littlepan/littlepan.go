// Package littlepan 实现 LittlePan_v2 的图床适配.
// 公开上传接口无需登录; 上传返回后端直链, 删除按文件 MD5 处理.
package littlepan

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/yeqingky/Picsky/internal/storage"
)

const (
	driverType = "littlepan"

	// maxResponseBytes 限制响应体读取, 防止异常后端拖垮进程.
	maxResponseBytes = 1 << 20
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

func init() {
	storage.Register(storage.Spec{
		Type:         driverType,
		Label:        "LittlePan",
		Capabilities: []string{"upload"},
		ConfigFields: []storage.Field{
			{Name: "endpoint", Label: "站点地址", Required: true, Placeholder: "https://pan.example.com"},
		},
		SecretFields:   []storage.Field{},
		URLFromBackend: true,
	}, newAdapter)
}

type adapter struct {
	endpoint *url.URL
	client   *http.Client
}

func newAdapter(opts storage.Options) (storage.Adapter, error) {
	raw := strings.TrimSpace(opts.Config["endpoint"])
	if raw == "" {
		return nil, errors.New("LittlePan 站点地址 (endpoint) 必填")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("LittlePan 站点地址非法: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("LittlePan 站点地址必须以 http:// 或 https:// 开头")
	}
	if parsed.Host == "" {
		return nil, errors.New("LittlePan 站点地址缺少主机名")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return &adapter{
		endpoint: parsed,
		client: &http.Client{
			Timeout: 5 * time.Minute,
		},
	}, nil
}

func (a *adapter) Type() string { return driverType }

// Capabilities 只声明上传能力; 公开 API 不支持可用的删除与查询.
func (a *adapter) Capabilities() storage.Capability {
	return storage.CapUpload
}

// Put 以 multipart 上传文件, 隐藏首页展示并返回后端直链.
func (a *adapter) Put(ctx context.Context, key string, body io.Reader, size int64, contentType string) (storage.PutResult, error) {
	if err := storage.ValidateObjectKey(key); err != nil {
		return storage.PutResult{}, err
	}
	if size <= 0 {
		return storage.PutResult{}, errors.New("上传内容为空")
	}
	contentType = strings.TrimSpace(contentType)
	if contentType == "" || strings.ContainsAny(contentType, "\r\n") {
		contentType = "application/octet-stream"
	}

	boundary, err := storage.RandomString(24)
	if err != nil {
		return storage.PutResult{}, fmt.Errorf("生成 multipart 边界失败: %w", err)
	}
	boundary = "----Picsky" + boundary
	filename := sanitizeFilename(path.Base(key))
	preamble := "--" + boundary + "\r\n" +
		`Content-Disposition: form-data; name="file"; filename="` + filename + `"` + "\r\n" +
		"Content-Type: " + contentType + "\r\n\r\n"
	epilogue := "\r\n--" + boundary + "\r\n" +
		`Content-Disposition: form-data; name="format"` + "\r\n\r\njson\r\n" +
		"--" + boundary + "\r\n" +
		`Content-Disposition: form-data; name="show"` + "\r\n\r\n0\r\n" +
		"--" + boundary + "--\r\n"

	payload := io.MultiReader(
		strings.NewReader(preamble),
		io.LimitReader(body, size),
		strings.NewReader(epilogue),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.apiURL(), payload)
	if err != nil {
		return storage.PutResult{}, err
	}
	req.ContentLength = int64(len(preamble)) + size + int64(len(epilogue))
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)

	var result uploadResponse
	if err := a.do(req, &result); err != nil {
		return storage.PutResult{}, fmt.Errorf("LittlePan 上传失败: %w", err)
	}
	if result.Code != 0 {
		return storage.PutResult{}, fmt.Errorf("LittlePan 上传失败: %s", errorMessage(result.Message))
	}
	link := strings.TrimSpace(result.ViewURL)
	if link == "" {
		link = strings.TrimSpace(result.DownURL)
	}
	if link == "" {
		return storage.PutResult{}, errors.New("LittlePan 未返回直链")
	}
	resolved, err := a.resolveURL(link)
	if err != nil {
		return storage.PutResult{}, fmt.Errorf("LittlePan 返回的直链无效: %w", err)
	}
	return storage.PutResult{URL: resolved}, nil
}

// Delete LittlePan 的公开 API 没有可用的删除授权模型, 前端删除只允许同一浏览器会话上传的文件.
func (a *adapter) Delete(context.Context, string) error {
	return storage.ErrNotSupported
}

func (a *adapter) Stat(context.Context, string) (storage.ObjectInfo, error) {
	return storage.ObjectInfo{}, storage.ErrNotSupported
}

func (a *adapter) List(context.Context, string) ([]storage.ObjectInfo, error) {
	return nil, storage.ErrNotSupported
}

func (a *adapter) Read(context.Context, string) (io.ReadCloser, error) {
	return nil, storage.ErrNotSupported
}

// Test 上传一张探测图片; 公开 API 不提供可用的删除接口, 探测对象会保留.
func (a *adapter) Test(ctx context.Context, prefix string) error {
	suffix, err := storage.RandomString(8)
	if err != nil {
		return err
	}
	key := storage.NormalizeObjectKey(prefix, "picsky-probe-"+suffix+".png")
	_, err = a.Put(ctx, key, bytes.NewReader(probePNG), int64(len(probePNG)), "image/png")
	return err
}

type uploadResponse struct {
	Code    int    `json:"code"`
	Message string `json:"msg"`
	ViewURL string `json:"viewurl"`
	DownURL string `json:"downurl"`
}

// do 发送请求并解析 JSON 响应; 非 2xx 视为错误.
func (a *adapter) do(req *http.Request, target any) error {
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("读取响应失败: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, snippet(data))
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("解析响应失败: %w", err)
	}
	return nil
}

// apiURL 返回站点根地址下的 api.php.
func (a *adapter) apiURL() string {
	target := *a.endpoint
	target.Path = strings.TrimRight(target.Path, "/") + "/api.php"
	return target.String()
}

// resolveURL 校验后端直链, 并把站点根相对路径补全为绝对地址.
func (a *adapter) resolveURL(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if !parsed.IsAbs() {
		parsed = a.endpoint.ResolveReference(parsed)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("直链必须以 http:// 或 https:// 开头")
	}
	if parsed.Host == "" {
		return "", errors.New("直链缺少主机名")
	}
	return parsed.String(), nil
}

func errorMessage(message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return "未知错误"
	}
	return message
}

// sanitizeFilename 去掉会破坏 multipart 头的字符.
func sanitizeFilename(name string) string {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == "/" {
		return "image"
	}
	replacer := strings.NewReplacer("\\", "_", "\"", "_", "\r", "_", "\n", "_")
	return replacer.Replace(name)
}

// snippet 截断响应体, 用于错误信息.
func snippet(data []byte) string {
	value := strings.TrimSpace(string(data))
	if runes := []rune(value); len(runes) > 200 {
		value = string(runes[:200])
	}
	return value
}
