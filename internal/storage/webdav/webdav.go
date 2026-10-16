// Package webdav 实现通用 WebDAV 存储驱动 (OpenList / Alist / 坚果云等).
// WebDAV 只作内部存储通道, 对外直链由分发域名拼接, 与本驱动的匿名读配置无关.
package webdav

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/yeqingky/Picsky/internal/storage"
)

const driverType = "webdav"

// maxListObjects 是单次递归列举的对象上限, 防止异常后端拖垮进程.
const maxListObjects = 200000

func init() {
	storage.Register(storage.Spec{
		Type:         driverType,
		Label:        "WebDAV",
		Capabilities: []string{"delete", "overwrite", "stat", "list", "read", "mkdir"},
		ConfigFields: []storage.Field{
			{Name: "endpoint", Label: "服务地址", Required: true, Placeholder: "https://example.com/dav"},
		},
		SecretFields: []storage.Field{
			{Name: "username", Label: "用户名"},
			{Name: "password", Label: "密码"},
		},
	}, newAdapter)
}

type adapter struct {
	scheme   string
	host     string
	base     string
	username string
	password string
	client   *http.Client
}

func newAdapter(opts storage.Options) (storage.Adapter, error) {
	raw := strings.TrimSpace(opts.Config["endpoint"])
	if raw == "" {
		return nil, errors.New("WebDAV 服务地址 (endpoint) 必填")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("WebDAV 服务地址非法: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("WebDAV 服务地址必须以 http:// 或 https:// 开头")
	}
	if parsed.Host == "" {
		return nil, errors.New("WebDAV 服务地址缺少主机名")
	}
	return &adapter{
		scheme:   parsed.Scheme,
		host:     parsed.Host,
		base:     strings.TrimRight(parsed.Path, "/"),
		username: strings.TrimSpace(opts.Secrets["username"]),
		password: opts.Secrets["password"],
		client: &http.Client{
			Timeout: 5 * time.Minute,
		},
	}, nil
}

func (a *adapter) Type() string { return driverType }

func (a *adapter) Capabilities() storage.Capability {
	return storage.CapDelete | storage.CapOverwrite | storage.CapStat | storage.CapList | storage.CapRead | storage.CapMkdir
}

// url 拼出对象键对应的绝对地址, 逐段转义.
func (a *adapter) url(key string) string {
	var sb strings.Builder
	sb.WriteString(a.scheme)
	sb.WriteString("://")
	sb.WriteString(a.host)
	sb.WriteString(a.base)
	for _, segment := range strings.Split(strings.Trim(key, "/"), "/") {
		if segment == "" {
			continue
		}
		sb.WriteString("/")
		sb.WriteString(url.PathEscape(segment))
	}
	if sb.Len() == 0 {
		return a.scheme + "://" + a.host + "/"
	}
	return sb.String()
}

// do 发送带 Basic 认证的请求.
func (a *adapter) do(ctx context.Context, method, target string, body io.Reader, headers map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, err
	}
	if a.username != "" || a.password != "" {
		req.SetBasicAuth(a.username, a.password)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	return a.client.Do(req)
}

// Put 写入对象: 先创建父目录, 再上传.
func (a *adapter) Put(ctx context.Context, key string, body io.Reader, size int64, contentType string) error {
	if err := storage.ValidateObjectKey(key); err != nil {
		return err
	}
	if err := a.mkdirAll(ctx, parentOf(key)); err != nil {
		return err
	}
	headers := map[string]string{}
	if contentType != "" {
		headers["Content-Type"] = contentType
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, a.url(key), body)
	if err != nil {
		return err
	}
	if size >= 0 {
		req.ContentLength = size
	}
	if a.username != "" || a.password != "" {
		req.SetBasicAuth(a.username, a.password)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("上传对象失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return statusError("上传对象", resp)
}

// Stat 查询对象: 先试 HEAD, 不支持时退化为 PROPFIND depth 0.
func (a *adapter) Stat(ctx context.Context, key string) (storage.ObjectInfo, error) {
	if err := storage.ValidateObjectKey(key); err != nil {
		return storage.ObjectInfo{}, err
	}
	resp, err := a.do(ctx, http.MethodHead, a.url(key), nil, nil)
	if err != nil {
		return storage.ObjectInfo{}, fmt.Errorf("查询对象失败: %w", err)
	}
	resp.Body.Close()
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		info := storage.ObjectInfo{Key: strings.Trim(key, "/"), Size: resp.ContentLength}
		if resp.ContentLength < 0 {
			info.Size = 0
		}
		if modified := resp.Header.Get("Last-Modified"); modified != "" {
			if parsed, err := http.ParseTime(modified); err == nil {
				info.ModTime = parsed
			}
		}
		return info, nil
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return storage.ObjectInfo{}, storage.ErrNotFound
	case resp.StatusCode == http.StatusMethodNotAllowed || resp.StatusCode == http.StatusNotImplemented:
		return a.propfindStat(ctx, key)
	default:
		return storage.ObjectInfo{}, statusError("查询对象", resp)
	}
}

func (a *adapter) propfindStat(ctx context.Context, key string) (storage.ObjectInfo, error) {
	entries, err := a.propfind(ctx, key, "0")
	if err != nil {
		return storage.ObjectInfo{}, err
	}
	trimmed := strings.Trim(key, "/")
	for _, entry := range entries {
		if strings.Trim(entry.Key, "/") == trimmed {
			return entry, nil
		}
	}
	return storage.ObjectInfo{}, storage.ErrNotFound
}

// Delete 删除对象; 对象不存在视为成功 (幂等).
func (a *adapter) Delete(ctx context.Context, key string) error {
	if err := storage.ValidateObjectKey(key); err != nil {
		return err
	}
	resp, err := a.do(ctx, http.MethodDelete, a.url(key), nil, nil)
	if err != nil {
		return fmt.Errorf("删除对象失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		return nil
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return statusError("删除对象", resp)
}

// List 递归列举前缀下的全部对象.
func (a *adapter) List(ctx context.Context, prefix string) ([]storage.ObjectInfo, error) {
	if err := storage.ValidatePrefix(prefix); err != nil {
		return nil, err
	}
	trimmed := strings.Trim(strings.TrimSpace(prefix), "/")
	queue := []string{trimmed}
	objects := make([]storage.ObjectInfo, 0, 64)
	for depth := 0; len(queue) > 0 && depth < 64; depth++ {
		next := make([]string, 0, 8)
		for _, dir := range queue {
			entries, err := a.propfind(ctx, dir, "1")
			if err != nil {
				return nil, err
			}
			for _, entry := range entries {
				if strings.Trim(entry.Key, "/") == strings.Trim(dir, "/") {
					continue
				}
				if entry.IsDir {
					next = append(next, entry.Key)
					continue
				}
				objects = append(objects, entry)
				if len(objects) > maxListObjects {
					return nil, fmt.Errorf("列举对象超过上限 %d, 请缩小扫描范围", maxListObjects)
				}
			}
		}
		queue = next
	}
	return objects, nil
}

// Read 读取对象内容.
func (a *adapter) Read(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := storage.ValidateObjectKey(key); err != nil {
		return nil, err
	}
	resp, err := a.do(ctx, http.MethodGet, a.url(key), nil, nil)
	if err != nil {
		return nil, fmt.Errorf("读取对象失败: %w", err)
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, storage.ErrNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		return nil, statusError("读取对象", resp)
	}
	return resp.Body, nil
}

// Test 写入并删除一个探测对象.
func (a *adapter) Test(ctx context.Context, prefix string) error {
	suffix, err := randomSuffix()
	if err != nil {
		return err
	}
	key := storage.NormalizeObjectKey(prefix, "picsky-probe-"+suffix+".txt")
	content := []byte("picsky connectivity test")
	if err := a.Put(ctx, key, bytes.NewReader(content), int64(len(content)), "text/plain"); err != nil {
		return err
	}
	return a.Delete(ctx, key)
}

// mkdirAll 逐级创建目录, 已存在时忽略.
func (a *adapter) mkdirAll(ctx context.Context, dir string) error {
	dir = strings.Trim(dir, "/")
	if dir == "" {
		return nil
	}
	segments := strings.Split(dir, "/")
	path := ""
	for _, segment := range segments {
		if segment == "" {
			continue
		}
		if path == "" {
			path = segment
		} else {
			path = path + "/" + segment
		}
		resp, err := a.do(ctx, "MKCOL", a.url(path), nil, nil)
		if err != nil {
			return fmt.Errorf("创建目录失败: %w", err)
		}
		resp.Body.Close()
		switch {
		case resp.StatusCode >= 200 && resp.StatusCode < 300:
		case resp.StatusCode == http.StatusMethodNotAllowed, resp.StatusCode == http.StatusConflict:
			// 405: 目录已存在; 409: 父目录已存在 (部分实现如此返回)
		default:
			return statusError("创建目录", resp)
		}
	}
	return nil
}

// davResponse 是 PROPFIND 207 响应中的单个 response 节点.
type davResponse struct {
	Href     string `xml:"href"`
	Propstat []struct {
		Status string `xml:"status"`
		Prop   struct {
			ContentLength string `xml:"getcontentlength"`
			LastModified  string `xml:"getlastmodified"`
			ResourceType  struct {
				Collection *struct{} `xml:"collection"`
			} `xml:"resourcetype"`
		} `xml:"prop"`
	} `xml:"propstat"`
}

type davMultistatus struct {
	Responses []davResponse `xml:"response"`
}

// propfind 发送 PROPFIND 并解析结果.
func (a *adapter) propfind(ctx context.Context, key, depth string) ([]storage.ObjectInfo, error) {
	const body = `<?xml version="1.0" encoding="utf-8"?><D:propfind xmlns:D="DAV:"><D:prop><D:resourcetype/><D:getcontentlength/><D:getlastmodified/></D:prop></D:propfind>`
	resp, err := a.do(ctx, "PROPFIND", a.url(key), strings.NewReader(body), map[string]string{
		"Depth":        depth,
		"Content-Type": "application/xml; charset=utf-8",
	})
	if err != nil {
		return nil, fmt.Errorf("列举对象失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		return nil, storage.ErrNotFound
	}
	if resp.StatusCode != http.StatusMultiStatus && (resp.StatusCode < 200 || resp.StatusCode >= 300) {
		return nil, statusError("列举对象", resp)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, fmt.Errorf("读取列举结果失败: %w", err)
	}
	var parsed davMultistatus
	if err := xml.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("解析列举结果失败: %w", err)
	}

	objects := make([]storage.ObjectInfo, 0, len(parsed.Responses))
	for _, entry := range parsed.Responses {
		key, ok := a.keyFromHref(entry.Href)
		if !ok {
			continue
		}
		info := storage.ObjectInfo{Key: key}
		for _, propstat := range entry.Propstat {
			if !strings.Contains(propstat.Status, "200") {
				continue
			}
			info.IsDir = propstat.Prop.ResourceType.Collection != nil
			if size, err := strconv.ParseInt(strings.TrimSpace(propstat.Prop.ContentLength), 10, 64); err == nil && size >= 0 {
				info.Size = size
			}
			if modified := strings.TrimSpace(propstat.Prop.LastModified); modified != "" {
				if parsed, err := http.ParseTime(modified); err == nil {
					info.ModTime = parsed
				}
			}
		}
		objects = append(objects, info)
	}
	return objects, nil
}

// keyFromHref 把 DAV href 转成对象键.
func (a *adapter) keyFromHref(href string) (string, bool) {
	href = strings.TrimSpace(href)
	if href == "" {
		return "", false
	}
	parsed, err := url.Parse(href)
	if err != nil {
		return "", false
	}
	path := parsed.Path
	if a.base != "" {
		if !strings.HasPrefix(path, a.base) {
			return "", false
		}
		path = strings.TrimPrefix(path, a.base)
	}
	path = strings.Trim(path, "/")
	if path == "" {
		return "", false
	}
	return path, true
}

// parentOf 返回对象键的父目录.
func parentOf(key string) string {
	trimmed := strings.Trim(key, "/")
	idx := strings.LastIndexByte(trimmed, '/')
	if idx < 0 {
		return ""
	}
	return trimmed[:idx]
}

func statusError(action string, resp *http.Response) error {
	return fmt.Errorf("%s失败: HTTP %d", action, resp.StatusCode)
}

func randomSuffix() (string, error) {
	return storage.RandomString(8)
}
