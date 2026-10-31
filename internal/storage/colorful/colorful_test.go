package colorful

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yeqingky/Picsky/internal/storage"
)

func TestDriverSpec(t *testing.T) {
	spec, ok := storage.SpecOf(driverType)
	if !ok {
		t.Fatal("彩虹外链网盘驱动未注册")
	}
	if spec.Label != "彩虹外链网盘" {
		t.Errorf("驱动名称 = %q", spec.Label)
	}
	if !spec.URLFromBackend {
		t.Error("彩虹外链网盘应由后端分配直链")
	}
	if len(spec.Capabilities) != 1 || spec.Capabilities[0] != "upload" {
		t.Errorf("驱动应只声明上传能力: %v", spec.Capabilities)
	}
	if spec.Has(storage.CapDelete) || spec.Has(storage.CapList) || spec.Has(storage.CapRead) {
		t.Error("公开接口不应声明删除, 列举或读取能力")
	}
	if len(spec.ConfigFields) != 1 || spec.ConfigFields[0].Name != "endpoint" || !spec.ConfigFields[0].Required {
		t.Errorf("站点地址应为唯一必填配置: %+v", spec.ConfigFields)
	}
	if len(spec.SecretFields) != 0 {
		t.Error("无需登录时不应有密钥字段")
	}
}

func TestNewAdapterRequiresValidEndpoint(t *testing.T) {
	for _, endpoint := range []string{"", "pan.example.com", "ftp://pan.example.com", "https://"} {
		if _, err := newAdapter(storage.Options{Config: map[string]string{"endpoint": endpoint}}); err == nil {
			t.Errorf("endpoint %q 应被拒绝", endpoint)
		}
	}
}

func TestPutUploadsMultipartAndPrefersViewURL(t *testing.T) {
	var (
		gotPath string
		gotFile string
		gotBody string
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.Method != http.MethodPost {
			t.Errorf("请求方法 = %s, 期望 POST", r.Method)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("解析 multipart 失败: %v", err)
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Errorf("缺少 file 字段: %v", err)
			return
		}
		defer file.Close()
		gotFile = header.Filename
		data, _ := io.ReadAll(file)
		gotBody = string(data)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"msg":"ok","viewurl":"` + "https://cdn.example.com/view.php/abc.webp" + `","downurl":"https://cdn.example.com/down.php/abc.webp"}`))
	}))
	defer server.Close()

	adapter := newTestAdapter(t, server.URL+"/sub")
	result, err := adapter.Put(context.Background(), "ab/cd/hash.webp", strings.NewReader("image-bytes"), int64(len("image-bytes")), "image/webp")
	if err != nil {
		t.Fatalf("Put 失败: %v", err)
	}
	if gotPath != "/sub/api.php" {
		t.Errorf("请求路径 = %q, 期望 /sub/api.php", gotPath)
	}
	if gotFile != "hash.webp" {
		t.Errorf("上传文件名 = %q, 期望 hash.webp", gotFile)
	}
	if gotBody != "image-bytes" {
		t.Errorf("上传内容 = %q", gotBody)
	}
	if result.URL != "https://cdn.example.com/view.php/abc.webp" {
		t.Errorf("直链 = %q, 期望优先使用 viewurl", result.URL)
	}
}

func TestPutFallsBackToDownloadURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":0,"viewurl":"","downurl":"https://cdn.example.com/down.php/abc.webp"}`))
	}))
	defer server.Close()

	adapter := newTestAdapter(t, server.URL)
	result, err := adapter.Put(context.Background(), "hash.webp", strings.NewReader("data"), 4, "image/webp")
	if err != nil {
		t.Fatalf("Put 失败: %v", err)
	}
	if result.URL != "https://cdn.example.com/down.php/abc.webp" {
		t.Errorf("直链 = %q, 期望回退 downurl", result.URL)
	}
}

func TestPutResolvesRelativeURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":0,"viewurl":"/view.php/abc.webp"}`))
	}))
	defer server.Close()

	adapter := newTestAdapter(t, server.URL+"/sub")
	result, err := adapter.Put(context.Background(), "hash.webp", strings.NewReader("data"), 4, "image/webp")
	if err != nil {
		t.Fatalf("Put 失败: %v", err)
	}
	if result.URL != server.URL+"/view.php/abc.webp" {
		t.Errorf("相对直链补全结果 = %q", result.URL)
	}
}

func TestPutReturnsBackendCodeError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":-4,"msg":"当前站点未开启上传API"}`))
	}))
	defer server.Close()

	adapter := newTestAdapter(t, server.URL)
	_, err := adapter.Put(context.Background(), "hash.webp", strings.NewReader("data"), 4, "image/webp")
	if err == nil || !strings.Contains(err.Error(), "当前站点未开启上传API") {
		t.Fatalf("错误 = %v, 期望包含接口错误信息", err)
	}
}

func TestPutRejectsHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer server.Close()

	adapter := newTestAdapter(t, server.URL)
	if _, err := adapter.Put(context.Background(), "hash.webp", strings.NewReader("data"), 4, "image/webp"); err == nil {
		t.Fatal("HTTP 404 应返回错误")
	}
}

func TestPutRejectsInvalidResponse(t *testing.T) {
	for name, body := range map[string]string{
		"非 JSON": "<html>error</html>",
		"空直链":    `{"code":0}`,
		"无效直链":   `{"code":0,"viewurl":"javascript:alert(1)"}`,
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			adapter := newTestAdapter(t, server.URL)
			if _, err := adapter.Put(context.Background(), "hash.webp", strings.NewReader("data"), 4, "image/webp"); err == nil {
				t.Fatal("无效响应应返回错误")
			}
		})
	}
}

func TestPutRejectsEmptyBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()
	adapter := newTestAdapter(t, server.URL)
	if _, err := adapter.Put(context.Background(), "hash.webp", strings.NewReader(""), 0, "image/webp"); err == nil {
		t.Fatal("空内容应被拒绝")
	}
}

func TestUnsupportedCapabilities(t *testing.T) {
	adapter := newTestAdapter(t, "https://pan.example.com")
	if _, err := adapter.Stat(context.Background(), "hash.webp"); err != storage.ErrNotSupported {
		t.Errorf("Stat 能力 = %v, 期望 ErrNotSupported", err)
	}
	if err := adapter.Delete(context.Background(), "hash.webp"); err != storage.ErrNotSupported {
		t.Errorf("Delete 能力 = %v, 期望 ErrNotSupported", err)
	}
	if _, err := adapter.List(context.Background(), ""); err != storage.ErrNotSupported {
		t.Errorf("List 能力 = %v, 期望 ErrNotSupported", err)
	}
	if _, err := adapter.Read(context.Background(), "hash.webp"); err != storage.ErrNotSupported {
		t.Errorf("Read 能力 = %v, 期望 ErrNotSupported", err)
	}
}

func newTestAdapter(t *testing.T, endpoint string) storage.Adapter {
	t.Helper()
	adapter, err := newAdapter(storage.Options{Config: map[string]string{"endpoint": endpoint}})
	if err != nil {
		t.Fatalf("创建适配器失败: %v", err)
	}
	return adapter
}
