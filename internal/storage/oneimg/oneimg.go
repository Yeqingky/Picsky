// Package oneimg 实现初春图床 (OneImg) 图片上传驱动的图床适配.
// 后端地址与 API Token 由策略配置; 上传后返回站点内的相对路径, 驱动补全为绝对直链.
package oneimg

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
	"strconv"
	"strings"
	"time"

	"github.com/yeqingky/Picsky/internal/storage"
)

const (
	driverType = "oneimg"

	// uploadPath 与 bucketsPath 是单张图片上传与存储桶列表接口, 站点地址由策略配置提供.
	uploadPath  = "/api/upload/images"
	bucketsPath = "/api/buckets/list"

	bucketsTimeout = 15 * time.Second
)

// maxResponseBytes 限制响应体读取, 防止异常后端拖垮进程.
const maxResponseBytes = 1 << 20

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
		Label:        "初春图床",
		Capabilities: []string{"upload"},
		ConfigFields: []storage.Field{
			{Name: "endpoint", Label: "站点地址", Required: true, Placeholder: "https://img.example.com"},
			{Name: "bucket_id", Label: "存储桶", Placeholder: "留空使用系统默认存储"},
		},
		SecretFields: []storage.Field{
			{Name: "token", Label: "API Token", Required: true},
		},
		URLFromBackend:    true,
		URLPrefixOverride: true,
	}, newAdapter)
}

type adapter struct {
	endpoint       string
	bucketEndpoint string
	publicURL      *url.URL
	token          string
	bucketID       string
	client         *http.Client
}

func newAdapter(opts storage.Options) (storage.Adapter, error) {
	raw := strings.TrimSpace(opts.Config["endpoint"])
	if raw == "" {
		return nil, errors.New("初春图床站点地址 (endpoint) 必填")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("初春图床站点地址非法: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("初春图床站点地址必须以 http:// 或 https:// 开头")
	}
	if parsed.Host == "" {
		return nil, errors.New("初春图床站点地址缺少主机名")
	}
	token := strings.TrimSpace(opts.Secrets["token"])
	if token == "" {
		return nil, errors.New("初春图床 API Token 必填")
	}

	basePath := strings.TrimRight(parsed.Path, "/")
	uploadEndpoint := *parsed
	uploadEndpoint.Path = basePath + uploadPath
	uploadEndpoint.RawQuery = ""
	uploadEndpoint.Fragment = ""
	bucketEndpoint := *parsed
	bucketEndpoint.Path = basePath + bucketsPath
	bucketEndpoint.RawQuery = ""
	bucketEndpoint.Fragment = ""
	publicURL := &url.URL{Scheme: parsed.Scheme, Host: parsed.Host}

	bucketID := strings.TrimSpace(opts.Config["bucket_id"])
	if bucketID != "" {
		id, err := strconv.Atoi(bucketID)
		if err != nil || id <= 0 {
			return nil, errors.New("初春图床存储桶 ID 必须是正整数")
		}
	}

	return &adapter{
		endpoint:       uploadEndpoint.String(),
		bucketEndpoint: bucketEndpoint.String(),
		publicURL:      publicURL,
		token:          token,
		bucketID:       bucketID,
		client: &http.Client{
			Timeout: 5 * time.Minute,
		},
	}, nil
}

func (a *adapter) Type() string { return driverType }

// Capabilities 只声明上传能力: OneImg API 不提供删除与直链查询.
func (a *adapter) Capabilities() storage.Capability { return storage.CapUpload }

// Put 以 multipart 上传单张图片; 后端返回相对路径, 使用站点地址补全.
func (a *adapter) Put(ctx context.Context, key string, body io.Reader, size int64, contentType string) (storage.PutResult, error) {
	if err := storage.ValidateObjectKey(key); err != nil {
		return storage.PutResult{}, err
	}
	if size <= 0 {
		return storage.PutResult{}, errors.New("上传内容为空")
	}
	contentType = strings.TrimSpace(contentType)
	if !strings.HasPrefix(strings.ToLower(contentType), "image/") || strings.ContainsAny(contentType, "\r\n") {
		return storage.PutResult{}, errors.New("初春图床驱动只支持图片")
	}
	boundary, err := storage.RandomString(16)
	if err != nil {
		return storage.PutResult{}, fmt.Errorf("生成 multipart 边界失败: %w", err)
	}
	boundary = "----Picsky" + boundary
	filename := sanitizeFilename("image" + strings.ToLower(path.Ext(key)))
	preamble := ""
	if a.bucketID != "" {
		preamble += "--" + boundary + "\r\n" +
			`Content-Disposition: form-data; name="bucket_id"` + "\r\n\r\n" + a.bucketID + "\r\n"
	}
	preamble += "--" + boundary + "\r\n" +
		`Content-Disposition: form-data; name="images[]"; filename="` + filename + `"` + "\r\n" +
		"Content-Type: " + contentType + "\r\n\r\n"
	epilogue := "\r\n--" + boundary + "--\r\n"
	payload := io.MultiReader(
		strings.NewReader(preamble),
		io.LimitReader(body, size),
		strings.NewReader(epilogue),
	)
	bodyLen := int64(len(preamble)) + size + int64(len(epilogue))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint, payload)
	if err != nil {
		return storage.PutResult{}, err
	}
	req.ContentLength = bodyLen
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	req.Header.Set("Authorization", "Bearer "+a.token)

	resp, err := a.client.Do(req)
	if err != nil {
		return storage.PutResult{}, fmt.Errorf("初春图床上传失败: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return storage.PutResult{}, fmt.Errorf("读取初春图床响应失败: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return storage.PutResult{}, fmt.Errorf("初春图床上传失败: HTTP %d: %s", resp.StatusCode, snippet(data))
	}
	var result struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Files []struct {
				Success bool   `json:"success"`
				Message string `json:"message"`
				URL     string `json:"url"`
			} `json:"files"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return storage.PutResult{}, fmt.Errorf("解析初春图床响应失败: %w", err)
	}
	if result.Code != http.StatusOK {
		message := strings.TrimSpace(result.Message)
		if message == "" {
			message = snippet(data)
		}
		return storage.PutResult{}, fmt.Errorf("初春图床上传失败: code=%d: %s", result.Code, message)
	}
	if len(result.Data.Files) == 0 {
		return storage.PutResult{}, errors.New("初春图床未返回文件信息")
	}
	file := result.Data.Files[0]
	if !file.Success {
		message := strings.TrimSpace(file.Message)
		if message == "" {
			message = "上传失败"
		}
		return storage.PutResult{}, fmt.Errorf("初春图床上传失败: %s", message)
	}
	link, err := a.absoluteURL(file.URL)
	if err != nil {
		return storage.PutResult{}, err
	}
	return storage.PutResult{URL: link}, nil
}

// absoluteURL 把后端返回的相对路径补全为站点绝对地址.
func (a *adapter) absoluteURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("初春图床返回的图片路径为空")
	}
	if strings.HasPrefix(raw, "//") {
		return "", fmt.Errorf("初春图床返回的图片路径无效: %q", raw)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("初春图床返回的图片路径无效: %w", err)
	}
	if parsed.Scheme != "" || parsed.Host != "" {
		if err := validateLink(parsed); err != nil {
			return "", fmt.Errorf("初春图床返回的直链无效: %w", err)
		}
		return parsed.String(), nil
	}
	resolved := a.publicURL.ResolveReference(parsed)
	if resolved.Path == "" || resolved.Path == "/" {
		return "", fmt.Errorf("初春图床返回的图片路径无效: %q", raw)
	}
	return resolved.String(), nil
}

// ListBuckets 返回后端可选存储桶, 供策略编辑页选择.
func (a *adapter) ListBuckets(ctx context.Context) ([]storage.Bucket, error) {
	ctx, cancel := context.WithTimeout(ctx, bucketsTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.bucketEndpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("获取初春图床存储桶失败: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("读取初春图床存储桶响应失败: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("获取初春图床存储桶失败: HTTP %d: %s", resp.StatusCode, snippet(data))
	}
	var result struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("解析初春图床存储桶响应失败: %w", err)
	}
	if result.Code != http.StatusOK {
		message := strings.TrimSpace(result.Message)
		if message == "" {
			message = snippet(data)
		}
		return nil, fmt.Errorf("获取初春图床存储桶失败: code=%d: %s", result.Code, message)
	}
	buckets := make([]storage.Bucket, 0, len(result.Data))
	for _, item := range result.Data {
		if item.ID <= 0 || strings.TrimSpace(item.Name) == "" {
			continue
		}
		buckets = append(buckets, storage.Bucket{ID: item.ID, Name: item.Name, Type: item.Type})
	}
	return buckets, nil
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

// Test 上传一张探测图片; 后端不支持删除, 探测对象会保留.
func (a *adapter) Test(ctx context.Context, prefix string) error {
	suffix, err := storage.RandomString(8)
	if err != nil {
		return err
	}
	key := storage.NormalizeObjectKey(prefix, "picsky-probe-"+suffix+".png")
	_, err = a.Put(ctx, key, bytes.NewReader(probePNG), int64(len(probePNG)), "image/png")
	return err
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

func validateLink(parsed *url.URL) error {
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("直链必须以 http:// 或 https:// 开头")
	}
	if parsed.Host == "" {
		return errors.New("直链缺少主机名")
	}
	return nil
}

// snippet 截断响应体, 用于错误信息.
func snippet(data []byte) string {
	value := strings.TrimSpace(string(data))
	if runes := []rune(value); len(runes) > 200 {
		value = string(runes[:200])
	}
	return value
}
