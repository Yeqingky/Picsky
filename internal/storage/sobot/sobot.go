// Package sobot 实现 Sobot (智齿客服) 附件上传驱动的图床适配.
// 后端固定为同一租户, 不提供配置项; 只接受图片, 上传后由后端分配直链.
package sobot

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
	driverType = "sobot"

	// uploadEndpoint 是固定的上传地址; 租户与来源由下面的常量写死.
	uploadEndpoint = "https://stemcellcn.sobot.com/ws-service/fileUploadForPostMsgBypc/v2"
	tenantBNO      = "9597f43540154294a7286a448c5e7531"
	uploaderUID    = "00000000000000000000000000000000"
	uploadType     = "ticket"
	requestOrigin  = "https://stemcellcn.sobot.com"
	requestReferer = requestOrigin + "/ticketclient-v7/index.html?sysNum=" + tenantBNO + "&uid=" + uploaderUID
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
		Label:          "Sobot",
		Capabilities:   []string{"upload"},
		ConfigFields:   []storage.Field{},
		SecretFields:   []storage.Field{},
		URLFromBackend: true,
	}, newAdapter)
}

type adapter struct {
	endpoint string
	client   *http.Client
}

func newAdapter(storage.Options) (storage.Adapter, error) {
	return newWithEndpoint(uploadEndpoint), nil
}

// newWithEndpoint 供测试替换上传地址.
func newWithEndpoint(endpoint string) *adapter {
	return &adapter{
		endpoint: endpoint,
		client: &http.Client{
			Timeout: 5 * time.Minute,
		},
	}
}

func (a *adapter) Type() string { return driverType }

// Capabilities 只声明上传能力: 后端不支持查询, 列举, 读取与删除.
func (a *adapter) Capabilities() storage.Capability { return storage.CapUpload }

// Put 以 multipart 上传图片, 返回后端分配的直链.
func (a *adapter) Put(ctx context.Context, key string, body io.Reader, size int64, contentType string) (storage.PutResult, error) {
	if err := storage.ValidateObjectKey(key); err != nil {
		return storage.PutResult{}, err
	}
	if size <= 0 {
		return storage.PutResult{}, errors.New("上传内容为空")
	}
	contentType = strings.TrimSpace(contentType)
	if !strings.HasPrefix(strings.ToLower(contentType), "image/") || strings.ContainsAny(contentType, "\r\n") {
		return storage.PutResult{}, errors.New("Sobot 驱动只支持图片")
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
		`Content-Disposition: form-data; name="pid"` + "\r\n\r\n" + tenantBNO + "\r\n" +
		"--" + boundary + "\r\n" +
		`Content-Disposition: form-data; name="uid"` + "\r\n\r\n" + uploaderUID + "\r\n" +
		"--" + boundary + "\r\n" +
		`Content-Disposition: form-data; name="type"` + "\r\n\r\n" + uploadType + "\r\n" +
		"--" + boundary + "--\r\n"

	payload := io.MultiReader(
		strings.NewReader(preamble),
		io.LimitReader(body, size),
		strings.NewReader(epilogue),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint, payload)
	if err != nil {
		return storage.PutResult{}, err
	}
	req.ContentLength = int64(len(preamble)) + size + int64(len(epilogue))
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	req.Header.Set("bno", tenantBNO)
	req.Header.Set("language", "zh")
	req.Header.Set("Origin", requestOrigin)
	req.Header.Set("Referer", requestReferer)

	resp, err := a.client.Do(req)
	if err != nil {
		return storage.PutResult{}, fmt.Errorf("Sobot 上传失败: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return storage.PutResult{}, fmt.Errorf("读取 Sobot 响应失败: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return storage.PutResult{}, fmt.Errorf("Sobot 上传失败: HTTP %d: %s", resp.StatusCode, snippet(data))
	}
	var result struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return storage.PutResult{}, fmt.Errorf("解析 Sobot 响应失败: %w", err)
	}
	link := strings.TrimSpace(result.URL)
	if err := validateLink(link); err != nil {
		return storage.PutResult{}, fmt.Errorf("Sobot 返回的直链无效: %w", err)
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

// validateLink 校验后端直链必须是带主机名的 http(s) 地址.
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
