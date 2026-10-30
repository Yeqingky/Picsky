// Package celine 实现思琳中国 (Celine) 公开附件上传驱动的图床适配.
// 后端固定, 无鉴权与配置项; 只接受图片, 上传后由后端分配直链.
// 服务端会重编码图片并清理 EXIF / 色彩配置等元数据, 不保证原样保存.
package celine

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
	driverType = "celine"

	// uploadEndpoint 与 publicBase 固定为思琳中国的上传接口与公开访问前缀.
	uploadEndpoint = "https://ocs.celine.cn/livechat/chat/file/upload/celine/web/0/0"
	publicBase     = "https://ocs.celine.cn/livechat/uploadfile"

	// maxFileSize 是 20 MiB 请求体上限扣除 multipart 开销后的安全文件上限,
	// 与实测值 20,971,326 字节一致; 固定的 image.<ext> 文件名保证开销不超过 194 字节.
	maxFileSize = 20<<20 - 194
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
		Label:          "思琳中国",
		Capabilities:   []string{"upload"},
		ConfigFields:   []storage.Field{},
		SecretFields:   []storage.Field{},
		URLFromBackend: true,
		MaxFileSize:    maxFileSize,
		Formats:        []string{"png", "jpeg", "gif"},
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
func (a *adapter) Put(ctx context.Context, key string, body io.Reader, size int64, contentType string) (storage.PutResult, error) {
	if err := storage.ValidateObjectKey(key); err != nil {
		return storage.PutResult{}, err
	}
	if size <= 0 {
		return storage.PutResult{}, errors.New("上传内容为空")
	}
	if size > maxFileSize {
		return storage.PutResult{}, fmt.Errorf("文件超过 Celine 单文件上限 %d 字节", maxFileSize)
	}
	contentType = strings.TrimSpace(contentType)
	if !strings.HasPrefix(strings.ToLower(contentType), "image/") || strings.ContainsAny(contentType, "\r\n") {
		return storage.PutResult{}, errors.New("Celine 驱动只支持图片")
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
	if bodyLen > 20<<20 {
		return storage.PutResult{}, fmt.Errorf("Celine 请求体超过 20 MiB 上限: %d 字节", bodyLen)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint, payload)
	if err != nil {
		return storage.PutResult{}, err
	}
	req.ContentLength = bodyLen
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)

	resp, err := a.client.Do(req)
	if err != nil {
		return storage.PutResult{}, fmt.Errorf("Celine 上传失败: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return storage.PutResult{}, fmt.Errorf("读取 Celine 响应失败: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return storage.PutResult{}, fmt.Errorf("Celine 上传失败: HTTP %d: %s", resp.StatusCode, snippet(data))
	}
	var result struct {
		Attachment struct {
			URL      string `json:"url"`
			Filename string `json:"filename"`
			FileType string `json:"filetype"`
			FileSize int64  `json:"filesize"`
		} `json:"attachment"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return storage.PutResult{}, fmt.Errorf("解析 Celine 响应失败: %w", err)
	}
	relPath := strings.TrimSpace(result.Attachment.URL)
	if relPath == "" {
		return storage.PutResult{}, errors.New("Celine 返回的附件路径为空")
	}
	if !strings.HasPrefix(relPath, "/") || strings.HasPrefix(relPath, "//") || strings.ContainsAny(relPath, "\r\n") {
		return storage.PutResult{}, fmt.Errorf("Celine 返回的附件路径无效: %q", relPath)
	}
	link := a.publicBase + relPath
	if err := validateLink(link); err != nil {
		return storage.PutResult{}, fmt.Errorf("Celine 返回的直链无效: %w", err)
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
