package sobot

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
		t.Fatal("Sobot 驱动未注册")
	}
	if !spec.URLFromBackend {
		t.Error("Sobot 驱动应由后端分配直链")
	}
	if len(spec.Capabilities) != 1 || spec.Capabilities[0] != "upload" {
		t.Errorf("Sobot 驱动应只声明上传能力: %v", spec.Capabilities)
	}
	if !spec.Has(storage.CapUpload) {
		t.Error("Sobot 驱动应声明上传能力")
	}
	if len(spec.ConfigFields) != 0 || len(spec.SecretFields) != 0 {
		t.Error("Sobot 驱动不应有配置项")
	}
	if spec.MaxFileSize != 50<<20 {
		t.Errorf("Sobot 单文件上限 = %d, 期望 %d", spec.MaxFileSize, 50<<20)
	}
	if spec.Has(storage.CapDelete) {
		t.Error("Sobot 驱动不应声明删除能力")
	}
}

func TestPutUploadsMultipartAndReturnsURL(t *testing.T) {
	var called bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.Method != http.MethodPost {
			t.Errorf("请求方法 = %s, 期望 POST", r.Method)
		}
		for header, want := range map[string]string{
			"Bno":      tenantBNO,
			"Language": "zh",
			"Origin":   requestOrigin,
			"Referer":  requestReferer,
		} {
			if got := r.Header.Get(header); got != want {
				t.Errorf("请求头 %s = %q, 期望 %q", header, got, want)
			}
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("解析 multipart 失败: %v", err)
			return
		}
		for field, want := range map[string]string{"pid": tenantBNO, "uid": uploaderUID, "type": uploadType} {
			if got := r.FormValue(field); got != want {
				t.Errorf("表单字段 %s = %q, 期望 %q", field, got, want)
			}
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Errorf("缺少 file 字段: %v", err)
			return
		}
		defer file.Close()
		if header.Filename != "foo.png" {
			t.Errorf("文件名 = %q, 期望 foo.png", header.Filename)
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
		_ = json.NewEncoder(w).Encode(map[string]string{"url": "https://img.sobot.com/a/b/foo.png"})
	}))
	defer server.Close()

	adapter := newWithEndpoint(server.URL)
	result, err := adapter.Put(context.Background(), "0f/foo.png", strings.NewReader("image-bytes"), int64(len("image-bytes")), "image/png")
	if err != nil {
		t.Fatalf("Put 失败: %v", err)
	}
	if !called {
		t.Error("未收到上传请求")
	}
	if result.URL != "https://img.sobot.com/a/b/foo.png" {
		t.Errorf("直链 = %q", result.URL)
	}
}

func TestPutRejectsNonImage(t *testing.T) {
	adapter := newWithEndpoint("http://127.0.0.1:1")
	if _, err := adapter.Put(context.Background(), "foo.txt", strings.NewReader("text"), 4, "text/plain"); err == nil {
		t.Error("非图片内容应被拒绝")
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
		{"非法 JSON", http.StatusOK, "not-json", "解析 Sobot 响应失败"},
		{"缺少直链", http.StatusOK, `{"fileName":"foo.png"}`, "直链为空"},
		{"非法协议", http.StatusOK, `{"url":"ftp://img.sobot.com/a.png"}`, "http:// 或 https://"},
		{"缺少主机", http.StatusOK, `{"url":"https:///a.png"}`, "缺少主机名"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(testCase.status)
				_, _ = io.WriteString(w, testCase.body)
			}))
			defer server.Close()

			adapter := newWithEndpoint(server.URL)
			_, err := adapter.Put(context.Background(), "foo.png", strings.NewReader("image"), 5, "image/png")
			if err == nil || !strings.Contains(err.Error(), testCase.wantSub) {
				t.Fatalf("错误 = %v, 期望包含 %q", err, testCase.wantSub)
			}
		})
	}
}

func TestUnsupportedOperations(t *testing.T) {
	adapter := newWithEndpoint("http://127.0.0.1:1")
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
		if !strings.HasPrefix(header.Filename, "picsky-probe-") || !strings.HasSuffix(header.Filename, ".png") {
			t.Errorf("探测文件名 = %q", header.Filename)
		}
		if contentType := header.Header.Get("Content-Type"); contentType != "image/png" {
			t.Errorf("探测文件 Content-Type = %q", contentType)
		}
		if !strings.HasPrefix(string(data), "\x89PNG") {
			t.Errorf("探测内容不是 PNG: %x", data)
		}
		_, _ = io.WriteString(w, `{"url":"https://img.sobot.com/a/probe.png"}`)
	}))
	defer server.Close()

	adapter := newWithEndpoint(server.URL)
	if err := adapter.Test(context.Background(), ""); err != nil {
		t.Fatalf("Test 失败: %v", err)
	}
}
