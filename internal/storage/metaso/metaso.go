// Package metaso 实现秘塔 (Metaso) 图片上传驱动的图床适配.
// 后端固定, 无鉴权与配置项; 只接受图片, 上传后由后端分配直链.
// 服务端会把图片转码为 WebP, 但响应头仍声明 image/png; 记录仍按原始上传内容计算.
package metaso

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
	driverType = "metaso"

	// uploadEndpoint 与 publicBase 固定为秘塔的上传接口与图片公开访问前缀.
	uploadEndpoint = "https://metaso.cn/api/uploadImage"
	publicBase     = "https://files.metaso.cn/api/book/image/imageBase64"

	// maxRequestBody 是接口允许的 multipart 请求体上限: 200 MiB.
	// 超限时后端不会提前返回 413, 而是收完整个请求后返回 errCode 500.
	maxRequestBody = 200 << 20

	// maxFileSize 从请求体上限中预留 200 字节 multipart 报文开销.
	maxFileSize = maxRequestBody - 200
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
		Type:           driverType,
		Label:          "秘塔",
		Capabilities:   []string{"upload"},
		ConfigFields:   []storage.Field{},
		SecretFields:   []storage.Field{},
		URLFromBackend: true,
		MaxFileSize:    maxFileSize,
	}, newAdapter)
}

type adapter struct {
	endpoint   string
	publicBase string
	client     *http.Client
}

func newAdapter(storage.Options) (storage.Adapter, error) {
	return newWithBase(uploadEndpoint, publicBase), nil
}

// newWithBase 供测试替换上传地址与公开访问前缀.
func newWithBase(endpoint, base string) *adapter {
	return &adapter{
		endpoint:   endpoint,
		publicBase: strings.TrimRight(base, "/"),
		client: &http.Client{
			Timeout: 5 * time.Minute,
		},
	}
}

func (a *adapter) Type() string { return driverType }

// Capabilities 只声明上传能力: 后端不支持查询, 列举, 读取与删除.
func (a *adapter) Capabilities() storage.Capability { return storage.CapUpload }

// Put 以 multipart 上传图片, 返回后端分配的公开直链.
// 后端错误使用 HTTP 200 与 errCode 500, 因此必须判断 errCode 而非状态码.
func (a *adapter) Put(ctx context.Context, key string, body io.Reader, size int64, contentType string) (storage.PutResult, error) {
	if err := storage.ValidateObjectKey(key); err != nil {
		return storage.PutResult{}, err
	}
	if size <= 0 {
		return storage.PutResult{}, errors.New("上传内容为空")
	}
	if size > maxFileSize {
		return storage.PutResult{}, fmt.Errorf("文件超过 Metaso 单文件上限 %d 字节", maxFileSize)
	}
	contentType = strings.TrimSpace(contentType)
	if !strings.HasPrefix(strings.ToLower(contentType), "image/") || strings.ContainsAny(contentType, "\r\n") {
		return storage.PutResult{}, errors.New("Metaso 驱动只支持图片")
	}

	boundary, err := storage.RandomString(16)
	if err != nil {
		return storage.PutResult{}, fmt.Errorf("生成 multipart 边界失败: %w", err)
	}
	boundary = "----Picsky" + boundary
	filename := sanitizeFilename("image" + strings.ToLower(path.Ext(key)))
	preamble := "--" + boundary + "\r\n" +
		`Content-Disposition: form-data; name="file"; filename="` + filename + `"` + "\r\n" +
		"Content-Type: " + contentType + "\r\n\r\n"
	epilogue := "\r\n--" + boundary + "--\r\n"

	payload := io.MultiReader(
		strings.NewReader(preamble),
		io.LimitReader(body, size),
		strings.NewReader(epilogue),
	)
	bodyLen := int64(len(preamble)) + size + int64(len(epilogue))
	if bodyLen > maxRequestBody {
		return storage.PutResult{}, fmt.Errorf("Metaso 请求体超过 200 MiB 上限: %d 字节", bodyLen)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint, payload)
	if err != nil {
		return storage.PutResult{}, err
	}
	req.ContentLength = bodyLen
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)

	resp, err := a.client.Do(req)
	if err != nil {
		return storage.PutResult{}, fmt.Errorf("Metaso 上传失败: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return storage.PutResult{}, fmt.Errorf("读取 Metaso 响应失败: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return storage.PutResult{}, fmt.Errorf("Metaso 上传失败: HTTP %d: %s", resp.StatusCode, snippet(data))
	}
	var result struct {
		ErrCode int    `json:"errCode"`
		ErrMsg  string `json:"errMsg"`
		Data    string `json:"data"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return storage.PutResult{}, fmt.Errorf("解析 Metaso 响应失败: %w", err)
	}
	if result.ErrCode != 0 {
		message := strings.TrimSpace(result.ErrMsg)
		if message == "" {
			message = snippet(data)
		}
		return storage.PutResult{}, fmt.Errorf("Metaso 上传失败: errCode=%d: %s", result.ErrCode, message)
	}
	relPath := strings.TrimSpace(result.Data)
	if relPath == "" {
		return storage.PutResult{}, errors.New("Metaso 返回的图片路径为空")
	}
	if strings.ContainsAny(relPath, " \t\r\n") {
		return storage.PutResult{}, fmt.Errorf("Metaso 返回的图片路径无效: %q", relPath)
	}
	link := a.publicBase + "/" + relPath
	if err := validateLink(link); err != nil {
		return storage.PutResult{}, fmt.Errorf("Metaso 返回的直链无效: %w", err)
	}
	return storage.PutResult{URL: link}, nil
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

// validateLink 校验公开直链必须是带主机名的 http(s) 地址.
func validateLink(raw string) error {
	if raw == "" {
		return errors.New("直链为空")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("直链必须以 http:// 或 https:// 开头")
	}
	if parsed.Host == "" {
		return errors.New("直链缺少主机名")
	}
	return nil
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
