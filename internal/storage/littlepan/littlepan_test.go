package littlepan

import (
	"context"
	"errors"
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
		t.Fatal("LittlePan 驱动未注册")
	}
	if spec.Label != "LittlePan" {
		t.Errorf("驱动名称 = %q", spec.Label)
	}
	if !spec.URLFromBackend {
		t.Error("LittlePan 应由后端分配直链")
	}
	if !spec.Has(storage.CapUpload) {
		t.Error("LittlePan 应声明上传能力")
	}
	if spec.Has(storage.CapDelete) {
		t.Error("LittlePan 公开 API 不应声明删除能力")
	}
	if spec.Has(storage.CapList) || spec.Has(storage.CapRead) {
		t.Error("LittlePan 不应声明列举或读取能力")
	}
	if len(spec.ConfigFields) != 1 || spec.ConfigFields[0].Name != "endpoint" || !spec.ConfigFields[0].Required {
		t.Errorf("站点地址应为唯一必填配置: %+v", spec.ConfigFields)
	}
	if len(spec.SecretFields) != 0 {
		t.Error("LittlePan 公开接口不应有密钥字段")
	}
}

func TestNewAdapterRequiresValidEndpoint(t *testing.T) {
	for _, endpoint := range []string{"", "pan.example.com", "ftp://pan.example.com", "https://"} {
		if _, err := newAdapter(storage.Options{Config: map[string]string{"endpoint": endpoint}}); err == nil {
			t.Errorf("endpoint %q 应被拒绝", endpoint)
		}
	}
}

func TestPutUploadsMultipartWithFormatAndShow(t *testing.T) {
	var (
		gotFile    string
		gotBody    string
		gotFormat  string
		gotShow    string
		gotContent string
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		gotFormat = r.FormValue("format")
		gotShow = r.FormValue("show")
		gotFile = header.Filename
		gotContent = header.Header.Get("Content-Type")
		data, _ := io.ReadAll(file)
		gotBody = string(data)
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "x"})
		_, _ = w.Write([]byte(`{"code":0,"msg":"上传成功","hash":"abc123","viewurl":"https://cdn.example.com/view.php?key=abc123","downurl":"https://cdn.example.com/down.php?key=abc123"}`))
	}))
	defer server.Close()

	adapter := newTestAdapter(t, server.URL+"/sub")
	result, err := adapter.Put(context.Background(), "ab/cd/hash.webp", strings.NewReader("image-bytes"), int64(len("image-bytes")), "image/webp")
	if err != nil {
		t.Fatalf("Put 失败: %v", err)
	}
	if gotFormat != "json" || gotShow != "0" {
		t.Errorf("format/show = %q/%q, 期望 json/0", gotFormat, gotShow)
	}
	if gotFile != "hash.webp" {
		t.Errorf("上传文件名 = %q, 期望 hash.webp", gotFile)
	}
	if gotContent != "image/webp" {
		t.Errorf("上传 Content-Type = %q", gotContent)
	}
	if gotBody != "image-bytes" {
		t.Errorf("上传内容 = %q", gotBody)
	}
	if result.URL != "https://cdn.example.com/view.php?key=abc123" {
		t.Errorf("直链 = %q, 期望优先使用 viewurl", result.URL)
	}
}

func TestPutFallsBackToDownloadURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":0,"hash":"abc123","viewurl":"","downurl":"https://cdn.example.com/down.php?key=abc123"}`))
	}))
	defer server.Close()

	adapter := newTestAdapter(t, server.URL)
	result, err := adapter.Put(context.Background(), "hash.webp", strings.NewReader("data"), 4, "image/webp")
	if err != nil {
		t.Fatalf("Put 失败: %v", err)
	}
	if result.URL != "https://cdn.example.com/down.php?key=abc123" {
		t.Errorf("直链 = %q, 期望回退 downurl", result.URL)
	}
}

func TestPutResolvesRelativeURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":0,"hash":"abc123","viewurl":"/view.php?key=abc123"}`))
	}))
	defer server.Close()

	adapter := newTestAdapter(t, server.URL+"/sub")
	result, err := adapter.Put(context.Background(), "hash.webp", strings.NewReader("data"), 4, "image/webp")
	if err != nil {
		t.Fatalf("Put 失败: %v", err)
	}
	if result.URL != server.URL+"/view.php?key=abc123" {
		t.Errorf("相对直链补全结果 = %q", result.URL)
	}
}

func TestPutReturnsBackendCodeError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":-1,"msg":"文件上传失败"}`))
	}))
	defer server.Close()

	adapter := newTestAdapter(t, server.URL)
	_, err := adapter.Put(context.Background(), "hash.webp", strings.NewReader("data"), 4, "image/webp")
	if err == nil || !strings.Contains(err.Error(), "文件上传失败") {
		t.Fatalf("错误 = %v, 期望包含接口错误信息", err)
	}
}

func TestPutRejectsInvalidResponses(t *testing.T) {
	tests := map[string]string{
		"非 JSON": "<html>error</html>",
		"空直链":    `{"code":0}`,
		"无效直链":   `{"code":0,"viewurl":"javascript:alert(1)"}`,
	}
	for name, body := range tests {
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

func TestPutRejectsHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer server.Close()
	adapter := newTestAdapter(t, server.URL)
	if _, err := adapter.Put(context.Background(), "hash.webp", strings.NewReader("data"), 4, "image/webp"); err == nil {
		t.Fatal("HTTP 403 应返回错误")
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
	if _, err := adapter.Stat(context.Background(), "abc123"); !errors.Is(err, storage.ErrNotSupported) {
		t.Errorf("Stat 能力 = %v, 期望 ErrNotSupported", err)
	}
	if _, err := adapter.List(context.Background(), ""); !errors.Is(err, storage.ErrNotSupported) {
		t.Errorf("List 能力 = %v, 期望 ErrNotSupported", err)
	}
	if _, err := adapter.Read(context.Background(), "abc123"); !errors.Is(err, storage.ErrNotSupported) {
		t.Errorf("Read 能力 = %v, 期望 ErrNotSupported", err)
	}
	if err := adapter.Delete(context.Background(), "abc123"); !errors.Is(err, storage.ErrNotSupported) {
		t.Errorf("Delete 能力 = %v, 期望 ErrNotSupported", err)
	}
}

func TestTestReportsUploadFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":-1,"msg":"上传失败"}`))
	}))
	defer server.Close()

	adapter := newTestAdapter(t, server.URL)
	if err := adapter.Test(context.Background(), "probe"); err == nil {
		t.Fatal("上传失败应导致连接测试失败")
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
