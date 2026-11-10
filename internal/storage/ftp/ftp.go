// Package ftp 实现 FTP 存储驱动.
// FTP 只作内部存储通道: 对象写入远程目录, 对外直链由策略分发域名拼接,
// 与 WebDAV 驱动一致, 不存在由后端分配直链的情况.
package ftp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"

	"github.com/yeqingky/Picsky/internal/storage"
)

const driverType = "ftp"

// maxListObjects 是单次递归列举的对象上限, 防止异常后端拖垮进程.
const maxListObjects = 200000

// maxListDepth 是递归列举的最大目录深度.
const maxListDepth = 64

func init() {
	storage.Register(storage.Spec{
		Type:         driverType,
		Label:        "FTP",
		Capabilities: []string{"upload", "delete", "overwrite", "stat", "list", "read", "mkdir"},
		ConfigFields: []storage.Field{
			{Name: "endpoint", Label: "服务器地址", Required: true, Placeholder: "ftp.example.com:21"},
			{Name: "directory", Label: "远程目录", Placeholder: "/htdocs"},
		},
		SecretFields: []storage.Field{
			{Name: "username", Label: "用户名"},
			{Name: "password", Label: "密码"},
		},
	}, newAdapter)
}

type adapter struct {
	addr     string
	base     string
	username string
	password string
}

func newAdapter(opts storage.Options) (storage.Adapter, error) {
	addr, err := parseEndpoint(opts.Config["endpoint"])
	if err != nil {
		return nil, err
	}
	base, err := storage.NormalizePrefix(opts.Config["directory"])
	if err != nil {
		return nil, fmt.Errorf("FTP 远程目录非法: %w", err)
	}
	return &adapter{
		addr:     addr,
		base:     base,
		username: strings.TrimSpace(opts.Secrets["username"]),
		password: opts.Secrets["password"],
	}, nil
}

// parseEndpoint 校验服务器地址: 只接受主机与端口, 允许 ftp:// 前缀, 缺省端口 21.
func parseEndpoint(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", errors.New("FTP 服务器地址 (endpoint) 必填")
	}
	switch {
	case strings.HasPrefix(strings.ToLower(value), "ftp://"):
		value = value[len("ftp://"):]
	case strings.Contains(value, "://"):
		return "", errors.New("FTP 服务器地址只支持 ftp:// 或不带协议的主机名")
	}
	if strings.ContainsAny(value, "/?#") {
		return "", errors.New("FTP 服务器地址只填主机与端口, 远程目录请配置在目录字段")
	}
	if host, port, err := net.SplitHostPort(value); err == nil {
		if host == "" {
			return "", errors.New("FTP 服务器地址缺少主机名")
		}
		if port == "" {
			port = "21"
		}
		return net.JoinHostPort(host, port), nil
	}
	if strings.Contains(value, ":") {
		return "", fmt.Errorf("FTP 服务器地址非法: %s", value)
	}
	if value == "" {
		return "", errors.New("FTP 服务器地址缺少主机名")
	}
	return net.JoinHostPort(value, "21"), nil
}

func (a *adapter) Type() string { return driverType }

func (a *adapter) Capabilities() storage.Capability {
	return storage.CapUpload | storage.CapDelete | storage.CapOverwrite | storage.CapStat | storage.CapList | storage.CapRead | storage.CapMkdir
}

// connect 建立一次已登录的连接; 调用方负责关闭. 远程目录由写入时逐级创建,
// 这里不切换工作目录, 所有命令使用绝对路径.
func (a *adapter) connect(ctx context.Context) (*client, error) {
	return dialFTP(ctx, a.addr, a.username, a.password)
}

// remote 把对象键转换为远程绝对路径.
func (a *adapter) remote(key string) string {
	key = storage.NormalizeObjectKey("", key)
	switch {
	case a.base == "" && key == "":
		return "/"
	case a.base == "":
		return "/" + key
	case key == "":
		return "/" + a.base
	default:
		return "/" + a.base + "/" + key
	}
}

// Put 写入对象: 逐级创建父目录后以 STOR 覆盖写入.
func (a *adapter) Put(ctx context.Context, key string, body io.Reader, size int64, contentType string) (storage.PutResult, error) {
	if err := storage.ValidateObjectKey(key); err != nil {
		return storage.PutResult{}, err
	}
	c, err := a.connect(ctx)
	if err != nil {
		return storage.PutResult{}, err
	}
	defer c.Close()
	if err := c.mkdirAll(ctx, parentOf(a.remote(key))); err != nil {
		return storage.PutResult{}, err
	}
	if err := c.stor(ctx, a.remote(key), body); err != nil {
		return storage.PutResult{}, err
	}
	return storage.PutResult{}, nil
}

// Stat 查询对象: SIZE 取体积, MDTM 取修改时间; 不支持时退化为 MLSD 列举父目录.
func (a *adapter) Stat(ctx context.Context, key string) (storage.ObjectInfo, error) {
	if err := storage.ValidateObjectKey(key); err != nil {
		return storage.ObjectInfo{}, err
	}
	c, err := a.connect(ctx)
	if err != nil {
		return storage.ObjectInfo{}, err
	}
	defer c.Close()
	return c.stat(ctx, a.remote(key))
}

// Delete 删除对象; 对象不存在视为成功 (幂等).
func (a *adapter) Delete(ctx context.Context, key string) error {
	if err := storage.ValidateObjectKey(key); err != nil {
		return err
	}
	c, err := a.connect(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	remote := a.remote(key)
	code, msg, err := c.command(ctx, "DELE %s", remote)
	if err != nil {
		return fmt.Errorf("删除对象失败: %w", err)
	}
	if code >= 200 && code < 300 {
		return nil
	}
	if code != statusFileUnavailable {
		return fmt.Errorf("删除对象失败: %d %s", code, msg)
	}
	// 550 同时表示对象不存在与无权限, 用查询区分, 避免把拒绝访问当成已删除.
	if _, err := c.stat(ctx, remote); err == nil {
		return fmt.Errorf("删除对象失败: %d %s", code, msg)
	} else if errors.Is(err, storage.ErrNotFound) {
		return nil
	}
	return fmt.Errorf("删除对象失败: %d %s", code, msg)
}

// List 递归列举前缀下的全部对象, 键相对于策略远程目录.
func (a *adapter) List(ctx context.Context, prefix string) ([]storage.ObjectInfo, error) {
	if err := storage.ValidatePrefix(prefix); err != nil {
		return nil, err
	}
	c, err := a.connect(ctx)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	start := storage.NormalizeObjectKey("", strings.TrimSpace(prefix))
	queue := []string{start}
	objects := make([]storage.ObjectInfo, 0, 64)
	for depth := 0; len(queue) > 0 && depth < maxListDepth; depth++ {
		next := make([]string, 0, 8)
		for _, dir := range queue {
			entries, err := c.mlsd(ctx, a.remote(dir))
			if err != nil {
				return nil, err
			}
			for _, entry := range entries {
				if entry.name == "." || entry.name == ".." {
					continue
				}
				key := entry.name
				if dir != "" {
					key = dir + "/" + entry.name
				}
				if entry.isDir {
					next = append(next, key)
					continue
				}
				objects = append(objects, storage.ObjectInfo{
					Key:     key,
					Size:    entry.size,
					ModTime: entry.modTime,
				})
				if len(objects) > maxListObjects {
					return nil, fmt.Errorf("列举对象超过上限 %d, 请缩小扫描范围", maxListObjects)
				}
			}
		}
		queue = next
	}
	if len(queue) > 0 {
		return nil, fmt.Errorf("目录层级超过上限 %d, 请缩小扫描范围", maxListDepth)
	}
	return objects, nil
}

// Read 读取对象内容.
func (a *adapter) Read(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := storage.ValidateObjectKey(key); err != nil {
		return nil, err
	}
	c, err := a.connect(ctx)
	if err != nil {
		return nil, err
	}
	data, err := c.openData(ctx)
	if err != nil {
		c.Close()
		return nil, err
	}
	code, msg, err := c.command(ctx, "RETR %s", a.remote(key))
	if err != nil {
		data.Close()
		c.Close()
		return nil, fmt.Errorf("读取对象失败: %w", err)
	}
	if code != statusFileOk && code != statusFileOpen {
		data.Close()
		c.Close()
		if code == statusFileUnavailable {
			return nil, storage.ErrNotFound
		}
		return nil, fmt.Errorf("读取对象失败: %d %s", code, msg)
	}
	return &stream{data: data, client: c}, nil
}

// CheckScan 在扫描前列举远程目录, 校验凭据, 目录与 MLSD 支持.
func (a *adapter) CheckScan(ctx context.Context) error {
	c, err := a.connect(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	_, err = c.mlsd(ctx, a.remote(""))
	return err
}

// Test 写入并删除一个探测对象.
func (a *adapter) Test(ctx context.Context, prefix string) error {
	suffix, err := storage.RandomString(8)
	if err != nil {
		return err
	}
	key := storage.NormalizeObjectKey(prefix, "picsky-probe-"+suffix+".txt")
	content := []byte("picsky connectivity test")
	if _, err := a.Put(ctx, key, bytes.NewReader(content), int64(len(content)), "text/plain"); err != nil {
		return err
	}
	return a.Delete(ctx, key)
}

// stream 是 RETR 数据连接的读取器; 关闭时收尾控制连接.
type stream struct {
	data   net.Conn
	client *client
	closed bool
}

func (s *stream) Read(p []byte) (int, error) {
	if s.closed {
		return 0, io.EOF
	}
	if err := s.data.SetDeadline(deadlineNow()); err != nil {
		return 0, err
	}
	n, err := s.data.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		return n, fmt.Errorf("读取对象内容失败: %w", err)
	}
	return n, err
}

func (s *stream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	_ = s.data.Close()
	// 数据连接关闭后服务端返回 226 (或提前中断时的 426), 这里只做收尾, 不影响已读内容.
	_ = s.client.finish()
	return s.client.Close()
}

// mkdirAll 逐级创建远程目录; 已存在的目录返回 550, 忽略后由后续写入暴露真实权限问题.
func (c *client) mkdirAll(ctx context.Context, dir string) error {
	dir = strings.Trim(strings.TrimSpace(dir), "/")
	if dir == "" {
		return nil
	}
	path := ""
	for _, segment := range strings.Split(dir, "/") {
		if segment == "" {
			continue
		}
		if path == "" {
			path = "/" + segment
		} else {
			path = path + "/" + segment
		}
		if _, _, err := c.command(ctx, "MKD %s", path); err != nil {
			return fmt.Errorf("创建目录失败: %w", err)
		}
	}
	return nil
}

// parentOf 返回远程路径的父目录.
func parentOf(path string) string {
	index := strings.LastIndex(path, "/")
	if index <= 0 {
		return "/"
	}
	return path[:index]
}
