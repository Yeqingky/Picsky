// Package cloudreve 实现 Cloudreve v4 存储驱动.
// 账号密码登录获取 AccessToken 后调用文件接口上传, 并由 Cloudreve 分配直链.
package cloudreve

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yeqingky/Picsky/internal/storage"
)

const (
	driverType          = "cloudreve"
	apiPrefix           = "/api/v4"
	scanPageSize        = 100
	scanRequestInterval = 200 * time.Millisecond
	maxListObjects      = 200000
)

// authClient 用于登录, 2FA 与刷新令牌等短请求.
var authClient = &http.Client{Timeout: 30 * time.Second}

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
		Label:        "Cloudreve",
		Capabilities: []string{"upload", "stat", "list", "read"},
		ConfigFields: []storage.Field{
			{Name: "endpoint", Label: "服务地址", Required: true, Placeholder: "https://cloudreve.example.com"},
			{Name: "directory", Label: "目录", Placeholder: "/picsky"},
			{Name: "auto_login", Label: "自动登录", Type: "switch"},
		},
		SecretFields: []storage.Field{
			{Name: "username", Label: "邮箱", Placeholder: "Cloudreve 账号邮箱"},
			{Name: "password", Label: "密码"},
			{Name: "refresh_token", Label: "登录令牌"},
		},
		URLFromBackend:      true,
		DefaultPathTemplate: "{md5:2}/{md5}.{ext}",
	}, newAdapter)
}

type adapter struct {
	endpoint     string
	apiBase      string
	username     string
	password     string
	refreshToken string
	autoLogin    bool
	directory    string
	client       *http.Client

	mu         sync.Mutex
	token      string
	tokenUntil time.Time

	paceMu      sync.Mutex
	lastPace    time.Time
	linksMu     sync.Mutex
	directLinks map[string]string
}

func newAdapter(opts storage.Options) (storage.Adapter, error) {
	raw := strings.TrimSpace(opts.Config["endpoint"])
	apiBase, err := apiBaseFromEndpoint(raw)
	if err != nil {
		return nil, err
	}

	directory, err := storage.NormalizePrefix(opts.Config["directory"])
	if err != nil {
		return nil, fmt.Errorf("Cloudreve 目录非法: %w", err)
	}
	if directory == "" {
		directory = "picsky"
	}
	username := strings.TrimSpace(opts.Secrets["username"])
	password := opts.Secrets["password"]
	refreshToken := strings.TrimSpace(opts.Secrets["refresh_token"])
	if refreshToken == "" {
		if username == "" {
			return nil, errors.New("Cloudreve 账号邮箱必填")
		}
		if password == "" {
			return nil, errors.New("Cloudreve 账号密码必填")
		}
	}

	return &adapter{
		endpoint:     raw,
		apiBase:      apiBase,
		username:     username,
		password:     password,
		refreshToken: refreshToken,
		autoLogin:    strings.EqualFold(strings.TrimSpace(opts.Config["auto_login"]), "true"),
		directory:    directory,
		directLinks:  map[string]string{},
		client: &http.Client{
			Timeout: 5 * time.Minute,
		},
	}, nil
}

// apiBaseFromEndpoint 规范化 Cloudreve 服务地址并追加 /api/v4.
func apiBaseFromEndpoint(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("Cloudreve 服务地址 (endpoint) 必填")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("Cloudreve 服务地址非法: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("Cloudreve 服务地址必须以 http:// 或 https:// 开头")
	}
	if parsed.Host == "" {
		return "", errors.New("Cloudreve 服务地址缺少主机名")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("Cloudreve 服务地址不能包含查询参数或片段")
	}
	basePath := strings.TrimRight(parsed.Path, "/")
	if !strings.HasSuffix(basePath, apiPrefix) {
		basePath += apiPrefix
	}
	return parsed.Scheme + "://" + parsed.Host + basePath, nil
}

func (a *adapter) Type() string { return driverType }

// ScanRoot 返回配置的目录; 扫描默认只覆盖该目录.
func (a *adapter) ScanRoot() string { return a.directory }

// Capabilities 声明上传, 查询, 列举与读取能力.
func (a *adapter) Capabilities() storage.Capability {
	return storage.CapUpload | storage.CapStat | storage.CapList | storage.CapRead
}

// Put 创建上传会话并分片上传 Cloudreve 文件, 最后创建公开直链.
func (a *adapter) Put(ctx context.Context, key string, body io.Reader, size int64, contentType string) (storage.PutResult, error) {
	if err := storage.ValidateObjectKey(key); err != nil {
		return storage.PutResult{}, err
	}
	fileURI := a.fileURI(key)
	if err := a.createParentFolder(ctx, fileURI); err != nil {
		return storage.PutResult{}, err
	}
	session, err := a.createUploadSession(ctx, fileURI, size, contentType)
	if err != nil {
		return storage.PutResult{}, err
	}
	if err := a.uploadChunks(ctx, session, body, size); err != nil {
		return storage.PutResult{}, err
	}

	link, err := a.createDirectLink(ctx, fileURI)
	if err != nil {
		return storage.PutResult{}, err
	}
	return storage.PutResult{URL: link, ObjectKey: key}, nil
}

// uploadSession 是创建上传会话后返回的凭据.
type uploadSession struct {
	SessionID     string   `json:"session_id"`
	ChunkSize     int64    `json:"chunk_size"`
	UploadURLs    []string `json:"upload_urls"`
	Credential    string   `json:"credential"`
	StoragePolicy struct {
		Type  string `json:"type"`
		Relay bool   `json:"relay"`
	} `json:"storage_policy"`
}

// createParentFolder 创建文件所在目录; Cloudreve 会一并创建缺失的祖先目录.
func (a *adapter) createParentFolder(ctx context.Context, fileURI string) error {
	index := strings.LastIndex(fileURI, "/")
	if index <= len("cloudreve://") {
		return errors.New("Cloudreve 文件 URI 缺少目录")
	}
	parent := fileURI[:index]
	payload, err := json.Marshal(map[string]any{
		"uri":             parent,
		"type":            "folder",
		"err_on_conflict": false,
	})
	if err != nil {
		return err
	}
	resp, err := a.authorizedRequest(ctx, http.MethodPost, a.apiBase+"/file/create", bytes.NewReader(payload), int64(len(payload)), "application/json")
	if err != nil {
		return fmt.Errorf("创建 Cloudreve 目录失败: %w", err)
	}
	if _, err := parseResponse(resp, "创建目录"); err != nil {
		return err
	}
	return nil
}

// createUploadSession 创建上传会话, 返回分片大小与上传地址.
func (a *adapter) createUploadSession(ctx context.Context, fileURI string, size int64, contentType string) (*uploadSession, error) {
	payload := map[string]any{
		"uri":  fileURI,
		"size": size,
	}
	if contentType != "" {
		payload["mime_type"] = contentType
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	resp, err := a.authorizedRequest(ctx, http.MethodPut, a.apiBase+"/file/upload", bytes.NewReader(body), int64(len(body)), "application/json")
	if err != nil {
		return nil, fmt.Errorf("创建 Cloudreve 上传会话失败: %w", err)
	}
	envelope, err := parseResponse(resp, "创建上传会话")
	if err != nil {
		return nil, err
	}
	var session uploadSession
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return nil, errors.New("创建 Cloudreve 上传会话失败: 响应缺少会话信息")
	}
	if err := json.Unmarshal(envelope.Data, &session); err != nil {
		return nil, fmt.Errorf("解析 Cloudreve 上传会话失败: %w", err)
	}
	if strings.TrimSpace(session.SessionID) == "" {
		return nil, errors.New("创建 Cloudreve 上传会话失败: session_id 为空")
	}
	if session.ChunkSize < 0 {
		session.ChunkSize = 0
	}
	if !a.supportsUploadPolicy(&session) {
		return nil, fmt.Errorf("Cloudreve 存储策略类型 %s 暂不支持, 请在 Cloudreve 中启用上传中转 (relay) 或使用本机存储策略", session.StoragePolicy.Type)
	}
	return &session, nil
}

// supportsUploadPolicy 判断能否使用 Cloudreve 分片接口或远端中转地址.
func (a *adapter) supportsUploadPolicy(session *uploadSession) bool {
	if session.StoragePolicy.Type == "local" || session.StoragePolicy.Relay {
		return true
	}
	return session.StoragePolicy.Type == "remote" && len(session.UploadURLs) > 0
}

// uploadChunks 顺序上传全部分片; chunk_size 为 0 时单请求上传完整文件.
func (a *adapter) uploadChunks(ctx context.Context, session *uploadSession, body io.Reader, size int64) error {
	if size <= 0 {
		return errors.New("上传文件大小必须大于 0")
	}
	chunkSize := session.ChunkSize
	if chunkSize <= 0 || chunkSize > size {
		chunkSize = size
	}
	total := int((size + chunkSize - 1) / chunkSize)
	offset := int64(0)
	for index := range total {
		length := chunkSize
		if remaining := size - offset; length > remaining {
			length = remaining
		}
		if length <= 0 {
			return errors.New("Cloudreve 分片长度无效")
		}
		reader := io.LimitReader(body, length)

		local := session.StoragePolicy.Type == "local" || session.StoragePolicy.Relay
		target := ""
		authorization := ""
		if local {
			target = fmt.Sprintf("%s/file/upload/%s/%d", a.apiBase, url.PathEscape(session.SessionID), index)
			token, err := a.ensureToken(ctx)
			if err != nil {
				return err
			}
			authorization = "Bearer " + token
		} else {
			parsed, err := url.Parse(session.UploadURLs[0])
			if err != nil {
				return fmt.Errorf("Cloudreve 远端上传地址无效: %w", err)
			}
			query := parsed.Query()
			query.Set("chunk", strconv.Itoa(index))
			parsed.RawQuery = query.Encode()
			target = parsed.String()
			authorization = session.Credential
		}
		if err := a.uploadChunk(ctx, target, reader, length, authorization, local); err != nil {
			return fmt.Errorf("上传 Cloudreve 分片 %d/%d 失败: %w", index+1, total, err)
		}
		offset += length
	}
	if offset != size {
		return fmt.Errorf("Cloudreve 上传数据不完整: %d/%d", offset, size)
	}
	return nil
}

// uploadChunk 上传单个分片. 本机/中转接口返回 Cloudreve 响应结构, 远端地址只校验 HTTP 状态.
func (a *adapter) uploadChunk(ctx context.Context, target string, body io.Reader, size int64, authorization string, local bool) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, body)
	if err != nil {
		return err
	}
	req.ContentLength = size
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Accept", "application/json")
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	if local {
		_, err := parseResponse(resp, "上传分片")
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("读取 Cloudreve 分片响应失败: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("上传分片失败: HTTP %d: %s", resp.StatusCode, snippet(data))
	}
	var envelope responseEnvelope
	if json.Unmarshal(data, &envelope) == nil && envelope.Code != 0 {
		return cloudreveError("上传分片", envelope)
	}
	return nil
}

// createDirectLink 为刚上传的文件创建公开直链.
func (a *adapter) createDirectLink(ctx context.Context, fileURI string) (string, error) {
	payload, err := json.Marshal(map[string][]string{"uris": {fileURI}})
	if err != nil {
		return "", err
	}
	resp, err := a.authorizedRequest(ctx, http.MethodPut, a.apiBase+"/file/source", bytes.NewReader(payload), int64(len(payload)), "application/json")
	if err != nil {
		return "", fmt.Errorf("创建 Cloudreve 直链失败: %w", err)
	}
	envelope, err := parseResponse(resp, "创建直链")
	if err != nil {
		return "", err
	}

	var links []struct {
		Link    string `json:"link"`
		FileURL string `json:"file_url"`
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return "", errors.New("创建 Cloudreve 直链失败: 响应缺少直链")
	}
	if err := json.Unmarshal(envelope.Data, &links); err != nil {
		return "", fmt.Errorf("解析 Cloudreve 直链响应失败: %w", err)
	}
	var link string
	for _, item := range links {
		if item.FileURL == fileURI {
			link = item.Link
			break
		}
	}
	if link == "" && len(links) == 1 {
		link = links[0].Link
	}
	if strings.TrimSpace(link) == "" {
		return "", errors.New("创建 Cloudreve 直链失败: 直链为空")
	}
	if err := validateLink(link); err != nil {
		return "", fmt.Errorf("Cloudreve 返回的直链无效: %w", err)
	}
	return link, nil
}

// cloudreveFile 是列举与文件信息接口的公共字段.
type cloudreveFile struct {
	Type      int    `json:"type"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	UpdatedAt string `json:"updated_at"`
	Path      string `json:"path"`
}

// Stat 查询 Cloudreve 文件并读取或创建直链.
func (a *adapter) Stat(ctx context.Context, key string) (storage.ObjectInfo, error) {
	if err := storage.ValidateObjectKey(key); err != nil {
		return storage.ObjectInfo{}, err
	}
	fileURI := a.fileURI(key)
	info, link, err := a.fileInfo(ctx, fileURI)
	if err != nil {
		return storage.ObjectInfo{}, err
	}
	if link == "" {
		link = a.cachedLink(key)
	} else {
		a.cacheLink(key, link)
	}
	if info.Type != 1 && link == "" {
		if err := a.pace(ctx); err != nil {
			return storage.ObjectInfo{}, err
		}
		link, err = a.createDirectLink(ctx, fileURI)
		if err != nil {
			return storage.ObjectInfo{}, err
		}
		a.cacheLink(key, link)
	}
	return storage.ObjectInfo{
		Key:     strings.Trim(key, "/"),
		Size:    info.Size,
		ModTime: parseCloudreveTime(info.UpdatedAt),
		IsDir:   info.Type == 1,
		URL:     link,
	}, nil
}

func (a *adapter) Delete(context.Context, string) error {
	return storage.ErrNotSupported
}

// List 递归列举 Cloudreve 目录, 分页请求之间保持间隔.
func (a *adapter) List(ctx context.Context, prefix string) ([]storage.ObjectInfo, error) {
	if err := storage.ValidatePrefix(prefix); err != nil {
		return nil, err
	}
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	if prefix == "" {
		prefix = a.directory
	} else {
		prefix = storage.WithPrefix(a.directory, prefix)
	}
	rootURI := a.fileURI(prefix)
	if prefix == "" {
		rootURI += "/"
	}
	folders := []string{rootURI}
	seenFolders := map[string]struct{}{folders[0]: {}}
	objects := make([]storage.ObjectInfo, 0, 64)
	for len(folders) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		folder := folders[0]
		folders = folders[1:]
		files, err := a.listFolder(ctx, folder)
		if err != nil {
			return nil, err
		}
		for _, file := range files {
			filePath, err := a.relativePath(file.Path, folder, file.Name)
			if err != nil {
				return nil, err
			}
			if file.Type == 1 {
				folderURI := file.Path
				if strings.TrimSpace(folderURI) == "" {
					folderURI = strings.TrimRight(folder, "/") + "/" + url.PathEscape(file.Name)
				}
				if _, seen := seenFolders[folderURI]; !seen {
					seenFolders[folderURI] = struct{}{}
					folders = append(folders, folderURI)
				}
				continue
			}
			objects = append(objects, storage.ObjectInfo{
				Key:     filePath,
				Size:    file.Size,
				ModTime: parseCloudreveTime(file.UpdatedAt),
			})
			if len(objects) > maxListObjects {
				return nil, fmt.Errorf("列举对象超过上限 %d, 请缩小扫描范围", maxListObjects)
			}
		}
	}
	return objects, nil
}

// Read 下载对象内容; 优先使用 Stat 缓存的直链.
func (a *adapter) Read(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := storage.ValidateObjectKey(key); err != nil {
		return nil, err
	}
	if link := a.cachedLink(key); link != "" {
		reader, err := a.download(ctx, link)
		if err == nil {
			return reader, nil
		}
		// 直链可能已失效, 继续尝试临时下载地址.
	}
	link, err := a.fileDownloadURL(ctx, key)
	if err != nil {
		return nil, err
	}
	return a.download(ctx, link)
}

// listFolder 读取一个目录的全部文件; 同时兼容游标与偏移分页.
func (a *adapter) listFolder(ctx context.Context, folderURI string) ([]cloudreveFile, error) {
	page := 0
	token := ""
	result := make([]cloudreveFile, 0, scanPageSize)
	for {
		if err := a.pace(ctx); err != nil {
			return nil, err
		}
		query := url.Values{}
		query.Set("uri", folderURI)
		query.Set("page", strconv.Itoa(page))
		query.Set("page_size", strconv.Itoa(scanPageSize))
		if token != "" {
			query.Set("next_page_token", token)
		}
		resp, err := a.authorizedRequest(ctx, http.MethodGet, a.apiBase+"/file?"+query.Encode(), nil, -1, "")
		if err != nil {
			return nil, fmt.Errorf("列举 Cloudreve 目录失败: %w", err)
		}
		envelope, err := parseResponse(resp, "列举文件")
		if err != nil {
			return nil, err
		}
		var data struct {
			Files      []cloudreveFile `json:"files"`
			Pagination struct {
				NextToken  string `json:"next_token"`
				IsCursor   bool   `json:"is_cursor"`
				TotalItems int    `json:"total_items"`
			} `json:"pagination"`
		}
		if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
			return nil, errors.New("列举 Cloudreve 目录失败: 响应缺少文件列表")
		}
		if err := json.Unmarshal(envelope.Data, &data); err != nil {
			return nil, fmt.Errorf("解析 Cloudreve 文件列表失败: %w", err)
		}
		result = append(result, data.Files...)
		if len(result) > maxListObjects {
			return nil, fmt.Errorf("列举对象超过上限 %d, 请缩小扫描范围", maxListObjects)
		}
		nextToken := strings.TrimSpace(data.Pagination.NextToken)
		if nextToken != "" {
			if nextToken == token {
				return nil, errors.New("Cloudreve 文件列表分页令牌重复")
			}
			token = nextToken
			continue
		}
		if data.Pagination.IsCursor {
			break
		}
		if data.Pagination.TotalItems > 0 && len(result) >= data.Pagination.TotalItems {
			break
		}
		if len(data.Files) < scanPageSize {
			break
		}
		page++
	}
	return result, nil
}

// fileInfo 查询 Cloudreve 文件详情与直链.
func (a *adapter) fileInfo(ctx context.Context, fileURI string) (*cloudreveFile, string, error) {
	if err := a.pace(ctx); err != nil {
		return nil, "", err
	}
	query := url.Values{}
	query.Set("uri", fileURI)
	query.Set("extended", "true")
	resp, err := a.authorizedRequest(ctx, http.MethodGet, a.apiBase+"/file/info?"+query.Encode(), nil, -1, "")
	if err != nil {
		return nil, "", fmt.Errorf("查询 Cloudreve 文件信息失败: %w", err)
	}
	envelope, err := parseResponse(resp, "查询文件信息")
	if err != nil {
		return nil, "", err
	}
	var data struct {
		cloudreveFile
		ExtendedInfo struct {
			DirectLinks []struct {
				URL string `json:"url"`
			} `json:"direct_links"`
		} `json:"extended_info"`
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return nil, "", errors.New("查询 Cloudreve 文件信息失败: 响应为空")
	}
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		return nil, "", fmt.Errorf("解析 Cloudreve 文件信息失败: %w", err)
	}
	link := ""
	if len(data.ExtendedInfo.DirectLinks) > 0 {
		link = strings.TrimSpace(data.ExtendedInfo.DirectLinks[0].URL)
		if link != "" {
			if err := validateLink(link); err != nil {
				return nil, "", fmt.Errorf("Cloudreve 直链无效: %w", err)
			}
		}
	}
	return &data.cloudreveFile, link, nil
}

// fileDownloadURL 获取文件的临时下载地址.
func (a *adapter) fileDownloadURL(ctx context.Context, key string) (string, error) {
	if err := a.pace(ctx); err != nil {
		return "", err
	}
	payload, err := json.Marshal(map[string]any{
		"uris":                 []string{a.fileURI(key)},
		"use_primary_site_url": true,
	})
	if err != nil {
		return "", err
	}
	resp, err := a.authorizedRequest(ctx, http.MethodPost, a.apiBase+"/file/url", bytes.NewReader(payload), int64(len(payload)), "application/json")
	if err != nil {
		return "", fmt.Errorf("获取 Cloudreve 下载地址失败: %w", err)
	}
	envelope, err := parseResponse(resp, "获取下载地址")
	if err != nil {
		return "", err
	}
	var data struct {
		URLs []struct {
			URL string `json:"url"`
		} `json:"urls"`
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return "", errors.New("获取 Cloudreve 下载地址失败: 响应为空")
	}
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		return "", fmt.Errorf("解析 Cloudreve 下载地址失败: %w", err)
	}
	if len(data.URLs) == 0 || strings.TrimSpace(data.URLs[0].URL) == "" {
		return "", errors.New("获取 Cloudreve 下载地址失败: 下载地址为空")
	}
	link := strings.TrimSpace(data.URLs[0].URL)
	if err := validateLink(link); err != nil {
		return "", fmt.Errorf("Cloudreve 下载地址无效: %w", err)
	}
	return link, nil
}

// download 读取直链内容.
func (a *adapter) download(ctx context.Context, target string) (io.ReadCloser, error) {
	if err := a.pace(ctx); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("下载 Cloudreve 文件失败: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
		resp.Body.Close()
		return nil, fmt.Errorf("下载 Cloudreve 文件失败: HTTP %d: %s", resp.StatusCode, snippet(data))
	}
	return resp.Body, nil
}

// relativePath 把 Cloudreve 文件 URI 转为相对对象键.
func (a *adapter) relativePath(raw, folder, name string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		raw = strings.TrimRight(folder, "/") + "/" + url.PathEscape(name)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("解析 Cloudreve 文件路径失败: %w", err)
	}
	if parsed.Scheme != "cloudreve" || parsed.Host != "my" {
		return "", fmt.Errorf("Cloudreve 文件路径格式不支持: %s", raw)
	}
	key := strings.Trim(parsed.Path, "/")
	if key == "" {
		return "", fmt.Errorf("Cloudreve 文件路径为空: %s", raw)
	}
	return key, nil
}

// cachedLink 读取已缓存的直链.
func (a *adapter) cachedLink(key string) string {
	a.linksMu.Lock()
	defer a.linksMu.Unlock()
	return a.directLinks[key]
}

// cacheLink 缓存直链, 供扫描后续读取复用.
func (a *adapter) cacheLink(key, link string) {
	if strings.TrimSpace(link) == "" {
		return
	}
	a.linksMu.Lock()
	defer a.linksMu.Unlock()
	a.directLinks[key] = link
}

// pace 在扫描相关请求之间保持最小间隔, 避免触发后端速率限制.
func (a *adapter) pace(ctx context.Context) error {
	a.paceMu.Lock()
	defer a.paceMu.Unlock()
	if !a.lastPace.IsZero() {
		wait := scanRequestInterval - time.Since(a.lastPace)
		if wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	a.lastPace = time.Now()
	return nil
}

// Test 登录并上传一张探测图片; 后端不支持删除, 探测对象会保留.
func (a *adapter) Test(ctx context.Context, prefix string) error {
	key := strings.Trim(strings.TrimSpace(prefix), "/")
	if key == "" {
		key = a.directory
	} else {
		key = storage.WithPrefix(a.directory, key)
	}
	key = strings.Trim(key, "/")
	if key != "" {
		key += "/"
	}
	key += fmt.Sprintf("_probe/%d.png", time.Now().UnixNano())
	_, err := a.Put(ctx, key, bytes.NewReader(probePNG), int64(len(probePNG)), "image/png")
	return err
}

// fileURI 把魔法路径直接转换为 Cloudreve 文件 URI.
func (a *adapter) fileURI(key string) string {
	var sb strings.Builder
	sb.WriteString("cloudreve://my")
	for _, segment := range strings.Split(strings.Trim(key, "/"), "/") {
		if segment == "" {
			continue
		}
		sb.WriteString("/")
		sb.WriteString(url.PathEscape(segment))
	}
	return sb.String()
}

// authorizedRequest 发送带 AccessToken 的请求.
func (a *adapter) authorizedRequest(ctx context.Context, method, target string, body io.Reader, size int64, contentType string) (*http.Response, error) {
	token, err := a.ensureToken(ctx)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if size >= 0 {
		req.ContentLength = size
	}
	return a.client.Do(req)
}

// ensureToken 返回缓存的 AccessToken, 缺失或临近过期时重新登录.
func (a *adapter) ensureToken(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.token != "" && (a.tokenUntil.IsZero() || time.Now().Before(a.tokenUntil.Add(-30*time.Second))) {
		return a.token, nil
	}
	if a.refreshToken != "" {
		result, err := RefreshAccessToken(ctx, a.endpoint, a.refreshToken)
		if err == nil {
			if result.RefreshToken != "" {
				a.refreshToken = result.RefreshToken
			}
			a.token = result.AccessToken
			a.tokenUntil = result.AccessExpires
			return a.token, nil
		}
		if !a.autoLogin {
			return "", fmt.Errorf("Cloudreve 登录已失效, 请在策略编辑页重新登录: %w", err)
		}
	}
	result, err := LoginPassword(ctx, a.endpoint, a.username, a.password)
	if err != nil {
		return "", err
	}
	if result.TwoFactor {
		return "", errors.New("Cloudreve 账号启用了两步验证, 请在策略编辑页完成登录")
	}
	a.token = result.AccessToken
	a.tokenUntil = result.AccessExpires
	a.refreshToken = result.RefreshToken
	return a.token, nil
}

// AuthResult 是 Cloudreve 登录或刷新令牌的结果.
type AuthResult struct {
	TwoFactor      bool
	SessionID      string
	AccessToken    string
	AccessExpires  time.Time
	RefreshToken   string
	RefreshExpires string
}

// LoginPassword 使用账号密码登录; 需要两步验证时返回 TwoFactor 与 SessionID.
func LoginPassword(ctx context.Context, endpoint, email, password string) (*AuthResult, error) {
	if strings.TrimSpace(email) == "" {
		return nil, errors.New("Cloudreve 账号邮箱必填")
	}
	if password == "" {
		return nil, errors.New("Cloudreve 账号密码必填")
	}
	resp, err := doAuthRequest(ctx, endpoint, http.MethodPost, "/session/token", map[string]string{
		"email":    strings.TrimSpace(email),
		"password": password,
	})
	if err != nil {
		return nil, fmt.Errorf("登录 Cloudreve 失败: %w", err)
	}
	envelope, err := readLoginEnvelope(resp, "登录 Cloudreve")
	if err != nil {
		return nil, err
	}
	if envelope.Code == 203 {
		sessionID, err := twoFactorSession(envelope.Data)
		if err != nil {
			return nil, err
		}
		return &AuthResult{TwoFactor: true, SessionID: sessionID}, nil
	}
	return parseAuthResult(envelope, "登录 Cloudreve")
}

// CompleteTwoFactor 使用一次性口令完成两步验证.
func CompleteTwoFactor(ctx context.Context, endpoint, sessionID, otp string) (*AuthResult, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, errors.New("Cloudreve 两步验证会话为空")
	}
	if strings.TrimSpace(otp) == "" {
		return nil, errors.New("Cloudreve 动态口令不能为空")
	}
	resp, err := doAuthRequest(ctx, endpoint, http.MethodPost, "/session/token/2fa", map[string]string{
		"session_id": strings.TrimSpace(sessionID),
		"opt":        strings.TrimSpace(otp),
	})
	if err != nil {
		return nil, fmt.Errorf("完成 Cloudreve 两步验证失败: %w", err)
	}
	envelope, err := readLoginEnvelope(resp, "完成两步验证")
	if err != nil {
		return nil, err
	}
	return parseAuthResult(envelope, "完成两步验证")
}

// RefreshAccessToken 使用 RefreshToken 换取新的 AccessToken.
func RefreshAccessToken(ctx context.Context, endpoint, refreshToken string) (*AuthResult, error) {
	if strings.TrimSpace(refreshToken) == "" {
		return nil, errors.New("Cloudreve RefreshToken 为空")
	}
	resp, err := doAuthRequest(ctx, endpoint, http.MethodPost, "/session/token/refresh", map[string]string{
		"refresh_token": refreshToken,
	})
	if err != nil {
		return nil, fmt.Errorf("刷新 Cloudreve 令牌失败: %w", err)
	}
	envelope, err := parseResponse(resp, "刷新令牌")
	if err != nil {
		return nil, err
	}
	var token struct {
		AccessToken    string `json:"access_token"`
		AccessExpires  string `json:"access_expires"`
		RefreshToken   string `json:"refresh_token"`
		RefreshExpires string `json:"refresh_expires"`
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return nil, errors.New("刷新 Cloudreve 令牌失败: 响应缺少令牌")
	}
	if err := json.Unmarshal(envelope.Data, &token); err != nil {
		return nil, fmt.Errorf("解析 Cloudreve 令牌响应失败: %w", err)
	}
	if strings.TrimSpace(token.AccessToken) == "" {
		return nil, errors.New("刷新 Cloudreve 令牌失败: AccessToken 为空")
	}
	refresh := strings.TrimSpace(token.RefreshToken)
	if refresh == "" {
		refresh = refreshToken
	}
	return &AuthResult{
		AccessToken:    token.AccessToken,
		AccessExpires:  parseCloudreveTime(token.AccessExpires),
		RefreshToken:   refresh,
		RefreshExpires: token.RefreshExpires,
	}, nil
}

// doAuthRequest 发送登录相关 JSON 请求.
func doAuthRequest(ctx context.Context, endpoint, method, path string, payload any) (*http.Response, error) {
	apiBase, err := apiBaseFromEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, apiBase+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return authClient.Do(req)
}

// parseAuthResult 解析登录响应中的用户, 令牌与直链权限.
func parseAuthResult(envelope *responseEnvelope, action string) (*AuthResult, error) {
	var data struct {
		User struct {
			Group struct {
				DirectLinkBatchSize int `json:"direct_link_batch_size"`
			} `json:"group"`
		} `json:"user"`
		Token struct {
			AccessToken    string `json:"access_token"`
			AccessExpires  string `json:"access_expires"`
			RefreshToken   string `json:"refresh_token"`
			RefreshExpires string `json:"refresh_expires"`
		} `json:"token"`
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return nil, fmt.Errorf("%s失败: 响应缺少令牌", action)
	}
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		return nil, fmt.Errorf("解析 Cloudreve %s响应失败: %w", action, err)
	}
	if strings.TrimSpace(data.Token.AccessToken) == "" {
		return nil, fmt.Errorf("%s失败: AccessToken 为空", action)
	}
	if strings.TrimSpace(data.Token.RefreshToken) == "" {
		return nil, fmt.Errorf("%s失败: RefreshToken 为空", action)
	}
	if data.User.Group.DirectLinkBatchSize <= 0 {
		return nil, errors.New("Cloudreve 账号所属用户组未开启直链权限 (direct_link_batch_size 为 0)")
	}
	return &AuthResult{
		AccessToken:    data.Token.AccessToken,
		AccessExpires:  parseCloudreveTime(data.Token.AccessExpires),
		RefreshToken:   data.Token.RefreshToken,
		RefreshExpires: data.Token.RefreshExpires,
	}, nil
}

// twoFactorSession 从 code 203 响应中读取两步验证会话 ID.
func twoFactorSession(data json.RawMessage) (string, error) {
	var sessionID string
	if err := json.Unmarshal(data, &sessionID); err == nil && strings.TrimSpace(sessionID) != "" {
		return sessionID, nil
	}
	var payload struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(data, &payload); err == nil && strings.TrimSpace(payload.SessionID) != "" {
		return payload.SessionID, nil
	}
	return "", errors.New("Cloudreve 两步验证响应缺少会话 ID")
}

// parseCloudreveTime 解析 RFC3339 时间; 失败时返回零值, 由调用方按令牌有效期继续使用.
func parseCloudreveTime(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

// responseEnvelope 是 Cloudreve 的统一响应结构.
type responseEnvelope struct {
	Code  int             `json:"code"`
	Msg   string          `json:"msg"`
	Error *string         `json:"error"`
	Data  json.RawMessage `json:"data"`
}

// readEnvelope 读取 Cloudreve 响应体并解析统一结构.
func readEnvelope(resp *http.Response) (*responseEnvelope, []byte, error) {
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, nil, err
	}
	var envelope responseEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, body, err
	}
	return &envelope, body, nil
}

// parseResponse 读取并校验 Cloudreve 响应; HTTP 200 也可能带非零 code.
func parseResponse(resp *http.Response, action string) (*responseEnvelope, error) {
	envelope, body, err := readEnvelope(resp)
	if err != nil {
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("%s失败: HTTP %d: %s", action, resp.StatusCode, snippet(body))
		}
		return nil, fmt.Errorf("解析 Cloudreve 响应失败: %w", err)
	}
	if envelope.Code != 0 {
		return nil, cloudreveError(action, *envelope)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s失败: HTTP %d: %s", action, resp.StatusCode, snippet(body))
	}
	return envelope, nil
}

// readLoginEnvelope 与 parseResponse 类似, 但允许 code 203 (需要两步验证).
func readLoginEnvelope(resp *http.Response, action string) (*responseEnvelope, error) {
	envelope, body, err := readEnvelope(resp)
	if err != nil {
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("%s失败: HTTP %d: %s", action, resp.StatusCode, snippet(body))
		}
		return nil, fmt.Errorf("解析 Cloudreve 响应失败: %w", err)
	}
	if envelope.Code != 0 && envelope.Code != 203 {
		return nil, cloudreveError(action, *envelope)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s失败: HTTP %d: %s", action, resp.StatusCode, snippet(body))
	}
	return envelope, nil
}

// cloudreveError 把非零 code 转换为可读错误.
func cloudreveError(action string, envelope responseEnvelope) error {
	if envelope.Code == 203 {
		return errors.New("Cloudreve 账号启用了两步验证, 当前驱动不支持")
	}
	detail := strings.TrimSpace(envelope.Msg)
	if envelope.Error != nil && strings.TrimSpace(*envelope.Error) != "" {
		if detail != "" {
			detail += ": "
		}
		detail += strings.TrimSpace(*envelope.Error)
	}
	if detail == "" {
		return fmt.Errorf("%s失败: Cloudreve 返回错误 %d", action, envelope.Code)
	}
	return fmt.Errorf("%s失败: Cloudreve 返回错误 %d: %s", action, envelope.Code, detail)
}

// validateLink 校验公开直链必须是带主机名的 http(s) 地址.
func validateLink(raw string) error {
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

// snippet 截断响应体, 用于错误信息.
func snippet(data []byte) string {
	value := strings.TrimSpace(string(data))
	if runes := []rune(value); len(runes) > 200 {
		value = string(runes[:200])
	}
	return value
}
