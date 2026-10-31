package metaso

import (
	"context"
	"encoding/json"
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
		t.Fatal("Metaso 驱动未注册")
	}
	if spec.Label != "秘塔" {
		t.Errorf("驱动名称 = %q, 期望 秘塔", spec.Label)
	}
	if !spec.URLFromBackend {
		t.Error("Metaso 驱动应由后端分配直链")
	}
	if len(spec.Capabilities) != 1 || spec.Capabilities[0] != "upload" {
		t.Errorf("Metaso 驱动应只声明上传能力: %v", spec.Capabilities)
	}
	if !spec.Has(storage.CapUpload) {
		t.Error("Metaso 驱动应声明上传能力")
	}
	if len(spec.ConfigFields) != 0 || len(spec.SecretFields) != 0 {
		t.Error("Metaso 驱动不应有配置项")
	}
	if spec.MaxFileSize != maxFileSize {
		t.Errorf("Metaso 单文件上限 = %d, 期望 %d", spec.MaxFileSize, maxFileSize)
	}
	if spec.MaxFileSize >= maxRequestBody {
		t.Errorf("Metaso 单文件上限必须低于 200 MiB 请求体上限: %d", spec.MaxFileSize)
	}
	if spec.Has(storage.CapDelete) {
		t.Error("Metaso 驱动不应声明删除能力")
	}
}

func TestPutUploadsMultipartAndReturnsPublicURL(t *testing.T) {
	var called bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.Method != http.MethodPost {
			t.Errorf("请求方法 = %s, 期望 POST", r.Method)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("解析 multipart 失败: %v", err)
			return
		}
		if len(r.MultipartForm.Value) != 0 {
			t.Errorf("不应包含额外表单字段: %v", r.MultipartForm.Value)
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Errorf("缺少 file 字段: %v", err)
			return
		}
		defer file.Close()
		if header.Filename != "image.png" {
			t.Errorf("文件名 = %q, 期望 image.png", header.Filename)
		}
		if contentType := header.Header.Get("Content-Type"); contentType != "image/png" {
			t.Errorf("文件 Content-Type = %q, 期望 image/png", contentType)
		}
		data, err := io.ReadAll(file)
		if err != nil {
			t.Errorf("读取文件内容失败: %v", err)
			return
		}
		if string(data) != "image-bytes" {
			t.Errorf("文件内容 = %q", data)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"errCode": 0,
			"errMsg":  "success",
			"data":    "2116419628441718785",
		})
	}))
	defer server.Close()

	adapter := newWithBase(server.URL, server.URL+"/api/book/image/imageBase64")
	result, err := adapter.Put(context.Background(), "0f/foo.png", strings.NewReader("image-bytes"), int64(len("image-bytes")), "image/png")
	if err != nil {
		t.Fatalf("Put 失败: %v", err)
	}
	if !called {
		t.Error("未收到上传请求")
	}
	want := server.URL + "/api/book/image/imageBase64/2116419628441718785"
	if result.URL != want {
		t.Errorf("直链 = %q, 期望 %q", result.URL, want)
	}
}

func TestPutRejectsNonImage(t *testing.T) {
	adapter := newWithBase("http://127.0.0.1:1", "http://127.0.0.1:1")
	if _, err := adapter.Put(context.Background(), "foo.txt", strings.NewReader("text"), 4, "text/plain"); err == nil {
		t.Error("非图片内容应被拒绝")
	}
}

func TestPutRejectsOversizedFile(t *testing.T) {
	adapter := newWithBase("http://127.0.0.1:1", "http://127.0.0.1:1")
	_, err := adapter.Put(context.Background(), "foo.png", strings.NewReader("x"), maxFileSize+1, "image/png")
	if err == nil || !strings.Contains(err.Error(), "单文件上限") {
		t.Errorf("超过单文件上限应被拒绝, 实际 %v", err)
	}
}

func TestPutRejectsOversizedRequestBody(t *testing.T) {
	adapter := newWithBase("http://127.0.0.1:1", "http://127.0.0.1:1")
	key := "foo." + strings.Repeat("x", 200)
	_, err := adapter.Put(context.Background(), key, strings.NewReader("x"), maxFileSize, "image/png")
	if err == nil || !strings.Contains(err.Error(), "请求体超过") {
		t.Errorf("超过请求体上限应被拒绝, 实际 %v", err)
	}
}

func TestPutErrors(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		wantSub string
	}{
		{"后端错误", http.StatusInternalServerError, "boom", "HTTP 500"},
		{"业务错误", http.StatusOK, `{"errCode":500,"errMsg":"system error"}`, "errCode=500: system error"},
		{"业务错误缺少消息", http.StatusOK, `{"errCode":500}`, "errCode=500"},
		{"非法 JSON", http.StatusOK, "not-json", "解析 Metaso 响应失败"},
		{"缺少图片路径", http.StatusOK, `{"errCode":0,"data":""}`, "图片路径为空"},
		{"图片路径包含空白", http.StatusOK, `{"errCode":0,"data":"foo bar"}`, "图片路径无效"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(testCase.status)
				_, _ = io.WriteString(w, testCase.body)
			}))
			defer server.Close()

			adapter := newWithBase(server.URL, server.URL+"/api/book/image/imageBase64")
			_, err := adapter.Put(context.Background(), "foo.png", strings.NewReader("image"), 5, "image/png")
			if err == nil || !strings.Contains(err.Error(), testCase.wantSub) {
				t.Fatalf("错误 = %v, 期望包含 %q", err, testCase.wantSub)
			}
		})
	}
}

func TestUnsupportedOperations(t *testing.T) {
	adapter := newWithBase("http://127.0.0.1:1", "http://127.0.0.1:1")
	ctx := context.Background()
	if _, err := adapter.Stat(ctx, "foo.png"); err != storage.ErrNotSupported {
		t.Errorf("Stat 错误 = %v", err)
	}
	if err := adapter.Delete(ctx, "foo.png"); err != storage.ErrNotSupported {
		t.Errorf("Delete 错误 = %v", err)
	}
	if _, err := adapter.List(ctx, ""); err != storage.ErrNotSupported {
		t.Errorf("List 错误 = %v", err)
	}
	if _, err := adapter.Read(ctx, "foo.png"); err != storage.ErrNotSupported {
		t.Errorf("Read 错误 = %v", err)
	}
}

func TestProbeUploadsPNG(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		data, err := io.ReadAll(file)
		if err != nil {
			t.Errorf("读取探测图片失败: %v", err)
			return
		}
		if header.Filename != "image.png" {
			t.Errorf("探测文件名 = %q, 期望 image.png", header.Filename)
		}
		if contentType := header.Header.Get("Content-Type"); contentType != "image/png" {
			t.Errorf("探测文件 Content-Type = %q", contentType)
		}
		if !strings.HasPrefix(string(data), "\x89PNG") {
			t.Errorf("探测内容不是 PNG: %x", data)
		}
		_, _ = io.WriteString(w, `{"errCode":0,"errMsg":"success","data":"probe"}`)
	}))
	defer server.Close()

	adapter := newWithBase(server.URL, server.URL+"/api/book/image/imageBase64")
	if err := adapter.Test(context.Background(), ""); err != nil {
		t.Fatalf("Test 失败: %v", err)
	}
}
