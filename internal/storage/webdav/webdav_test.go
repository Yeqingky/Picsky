package webdav_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/webdav"

	"github.com/yeqingky/Picsky/internal/storage"

	_ "github.com/yeqingky/Picsky/internal/storage/webdav"
)

// newAdapter 启动进程内 WebDAV 服务并返回驱动实例.
func newAdapter(t *testing.T) (storage.Adapter, string) {
	t.Helper()
	root := t.TempDir()
	handler := &webdav.Handler{
		Prefix:     "/dav",
		FileSystem: webdav.Dir(root),
		LockSystem: webdav.NewMemLS(),
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	adapter, err := storage.New("webdav", storage.Options{
		Config: map[string]string{"endpoint": server.URL + "/dav"},
	})
	if err != nil {
		t.Fatalf("实例化 WebDAV 驱动失败: %v", err)
	}
	return adapter, root
}

func TestWebDAVCapabilities(t *testing.T) {
	adapter, _ := newAdapter(t)
	caps := adapter.Capabilities()
	for _, capability := range []storage.Capability{storage.CapUpload, storage.CapDelete, storage.CapOverwrite, storage.CapStat, storage.CapList, storage.CapRead, storage.CapMkdir} {
		if !caps.Has(capability) {
			t.Errorf("WebDAV 驱动缺少能力: %v", capability)
		}
	}
	if caps.Has(storage.CapPresign) {
		t.Error("WebDAV 驱动不应声明 presign 能力")
	}
}

func TestWebDAVPutStatReadListDelete(t *testing.T) {
	adapter, root := newAdapter(t)
	ctx := context.Background()
	key := "image/0f/0f1e2d3c.png"
	content := []byte("picsky-test-content")

	if _, err := adapter.Put(ctx, key, bytes.NewReader(content), int64(len(content)), "image/png"); err != nil {
		t.Fatalf("Put 失败: %v", err)
	}
	onDisk := filepath.Join(root, filepath.FromSlash(key))
	written, err := os.ReadFile(onDisk)
	if err != nil {
		t.Fatalf("外部对象未按对象键写入: %v", err)
	}
	if !bytes.Equal(written, content) {
		t.Error("写入内容与预期不一致")
	}

	info, err := adapter.Stat(ctx, key)
	if err != nil {
		t.Fatalf("Stat 失败: %v", err)
	}
	if info.Size != int64(len(content)) {
		t.Errorf("Stat 体积错误: %d", info.Size)
	}

	reader, err := adapter.Read(ctx, key)
	if err != nil {
		t.Fatalf("Read 失败: %v", err)
	}
	readBack, err := io.ReadAll(reader)
	reader.Close()
	if err != nil {
		t.Fatalf("读取内容失败: %v", err)
	}
	if !bytes.Equal(readBack, content) {
		t.Error("读取内容与写入不一致")
	}

	objects, err := adapter.List(ctx, "image")
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	found := false
	for _, object := range objects {
		if strings.Trim(object.Key, "/") == key {
			found = true
		}
	}
	if !found {
		t.Errorf("List 未返回写入的对象: %+v", objects)
	}

	if _, err := adapter.Stat(ctx, "image/missing.png"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("Stat 缺失对象应返回 ErrNotFound, 实际 %v", err)
	}
	if _, err := adapter.Read(ctx, "image/missing.png"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("Read 缺失对象应返回 ErrNotFound, 实际 %v", err)
	}

	if err := adapter.Delete(ctx, key); err != nil {
		t.Fatalf("Delete 失败: %v", err)
	}
	if _, err := os.Stat(onDisk); !os.IsNotExist(err) {
		t.Error("对象未被删除")
	}
	if err := adapter.Delete(ctx, key); err != nil {
		t.Errorf("重复删除应幂等成功, 实际 %v", err)
	}
}

func TestWebDAVPutCreatesParentDirectories(t *testing.T) {
	adapter, root := newAdapter(t)
	ctx := context.Background()
	key := "a/b/c/deep.png"
	if _, err := adapter.Put(ctx, key, strings.NewReader("x"), 1, "image/png"); err != nil {
		t.Fatalf("Put 失败: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "a", "b", "c", "deep.png")); err != nil {
		t.Fatalf("多级目录未创建: %v", err)
	}
}

func TestWebDAVTestProbe(t *testing.T) {
	adapter, root := newAdapter(t)
	start := time.Now()
	if err := adapter.Test(context.Background(), "image"); err != nil {
		t.Fatalf("连接测试失败: %v", err)
	}
	if time.Since(start) > 30*time.Second {
		t.Error("连接测试耗时异常")
	}
	entries, err := os.ReadDir(filepath.Join(root, "image"))
	if err != nil {
		t.Fatalf("读取目录失败: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "picsky-probe-") {
			t.Errorf("连接测试的探测对象未被清理: %s", entry.Name())
		}
	}
}

func TestWebDAVUnknownType(t *testing.T) {
	if _, err := storage.New("local", storage.Options{}); err == nil {
		t.Error("未注册的后端类型应当报错")
	}
}

func TestWebDAVRejectsTraversalKeys(t *testing.T) {
	adapter, _ := newAdapter(t)
	ctx := context.Background()

	if _, err := adapter.Put(ctx, "../escape.txt", strings.NewReader("x"), 1, "text/plain"); err == nil {
		t.Error("对象键包含 .. 时应拒绝")
	}
	if _, err := adapter.Put(ctx, "a/../b.txt", strings.NewReader("x"), 1, "text/plain"); err == nil {
		t.Error("对象键包含 .. 段时应拒绝")
	}
	if _, err := adapter.List(ctx, "../"); err == nil {
		t.Error("列举前缀包含 .. 时应拒绝")
	}
	if _, err := adapter.Stat(ctx, ".."); err == nil {
		t.Error("Stat 遇到 .. 段时应拒绝")
	}
	if err := adapter.Delete(ctx, "a/./b.txt"); err == nil {
		t.Error("Delete 遇到 . 段时应拒绝")
	}
}

func TestWebDAVErrorDoesNotExposeResponseBody(t *testing.T) {
	const responseBody = "private-webdav-diagnostic"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, responseBody)
	}))
	defer server.Close()

	adapter, err := storage.New("webdav", storage.Options{
		Config: map[string]string{"endpoint": server.URL + "/dav"},
	})
	if err != nil {
		t.Fatalf("实例化 WebDAV 驱动失败: %v", err)
	}
	_, err = adapter.Put(context.Background(), "image.png", strings.NewReader("image"), 5, "image/png")
	if err == nil {
		t.Fatal("后端 502 应返回错误")
	}
	if strings.Contains(err.Error(), responseBody) {
		t.Errorf("WebDAV 错误不应包含后端响应体: %v", err)
	}
	if !strings.Contains(err.Error(), "HTTP 502") {
		t.Errorf("WebDAV 错误应保留状态码: %v", err)
	}
}
