package oneimg

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

func testAdapter(t *testing.T, endpoint string) *adapter {
	t.Helper()
	created, err := newAdapter(storage.Options{
		Config:  map[string]string{"endpoint": endpoint},
		Secrets: map[string]string{"token": "test-token"},
	})
	if err != nil {
		t.Fatalf("创建适配器失败: %v", err)
	}
	return created.(*adapter)
}

func TestDriverSpec(t *testing.T) {
	spec, ok := storage.SpecOf(driverType)
	if !ok {
		t.Fatal("初春图床驱动未注册")
	}
	if spec.Label != "初春图床" {
		t.Errorf("驱动名称 = %q, 期望 初春图床", spec.Label)
	}
	if !spec.URLFromBackend {
		t.Error("初春图床驱动应由后端分配直链")
	}
	if !spec.URLPrefixOverride {
		t.Error("初春图床驱动应允许分发域名覆盖后端直链前缀")
	}
	if len(spec.Capabilities) != 1 || spec.Capabilities[0] != "upload" {
		t.Errorf("初春图床驱动应只声明上传能力: %v", spec.Capabilities)
	}
	if len(spec.ConfigFields) != 2 || spec.ConfigFields[0].Name != "endpoint" || spec.ConfigFields[1].Name != "bucket_id" {
		t.Errorf("配置字段 = %+v, 期望 endpoint 与 bucket_id", spec.ConfigFields)
	}
	if len(spec.SecretFields) != 1 || spec.SecretFields[0].Name != "token" {
		t.Errorf("敏感字段 = %+v, 期望只有 token", spec.SecretFields)
	}
}

func TestNewAdapterValidatesConfig(t *testing.T) {
	cases := []struct {
		name    string
		opts    storage.Options
		wantSub string
	}{
		{"缺少地址", storage.Options{Secrets: map[string]string{"token": "x"}}, "站点地址"},
		{"非法地址", storage.Options{Config: map[string]string{"endpoint": "ftp://example.com"}, Secrets: map[string]string{"token": "x"}}, "http://"},
		{"缺少令牌", storage.Options{Config: map[string]string{"endpoint": "https://example.com"}}, "Token"},
		{"非法存储桶", storage.Options{Config: map[string]string{"endpoint": "https://example.com", "bucket_id": "x"}, Secrets: map[string]string{"token": "x"}}, "存储桶 ID"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := newAdapter(testCase.opts); err == nil || !strings.Contains(err.Error(), testCase.wantSub) {
				t.Fatalf("错误 = %v, 期望包含 %q", err, testCase.wantSub)
			}
		})
	}
}

func TestPutIncludesSelectedBucket(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("解析 multipart 失败: %v", err)
			return
		}
		if got := r.FormValue("bucket_id"); got != "2" {
			t.Errorf("bucket_id = %q, 期望 2", got)
		}
		_, _ = io.WriteString(w, `{"code":200,"data":{"files":[{"success":true,"url":"/uploads/a.webp"}]}}`)
	}))
	defer server.Close()

	created, err := newAdapter(storage.Options{
		Config:  map[string]string{"endpoint": server.URL, "bucket_id": "2"},
		Secrets: map[string]string{"token": "test-token"},
	})
	if err != nil {
		t.Fatalf("创建适配器失败: %v", err)
	}
	if _, err := created.Put(context.Background(), "foo.png", strings.NewReader("x"), 1, "image/png"); err != nil {
		t.Fatalf("Put 失败: %v", err)
	}
}

func TestListBuckets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != bucketsPath {
			t.Errorf("请求 = %s %s", r.Method, r.URL.Path)
		}
		if token := r.Header.Get("Authorization"); token != "Bearer test-token" {
			t.Errorf("Authorization = %q", token)
		}
		_, _ = io.WriteString(w, `{"code":200,"data":[{"id":1,"name":"默认存储","type":"default"},{"id":2,"name":"R2","type":"r2"}]}`)
	}))
	defer server.Close()

	adapter := testAdapter(t, server.URL)
	buckets, err := adapter.ListBuckets(context.Background())
	if err != nil {
		t.Fatalf("ListBuckets 失败: %v", err)
	}
	if len(buckets) != 2 || buckets[1].ID != 2 || buckets[1].Name != "R2" || buckets[1].Type != "r2" {
		t.Errorf("存储桶 = %+v", buckets)
	}
}

func TestListBucketsErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"code":401,"message":"Token 无效"}`)
	}))
	defer server.Close()

	adapter := testAdapter(t, server.URL)
	if _, err := adapter.ListBuckets(context.Background()); err == nil || !strings.Contains(err.Error(), "Token 无效") {
		t.Fatalf("错误 = %v, 期望包含 Token 无效", err)
	}
}

func TestPutUploadsMultipartAndResolvesRelativeURL(t *testing.T) {
	var called bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.Method != http.MethodPost || r.URL.Path != uploadPath {
			t.Errorf("请求 = %s %s, 期望 POST %s", r.Method, r.URL.Path, uploadPath)
		}
		if token := r.Header.Get("Authorization"); token != "Bearer test-token" {
			t.Errorf("Authorization = %q", token)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("解析 multipart 失败: %v", err)
			return
		}
		file, header, err := r.FormFile("images[]")
		if err != nil {
			t.Errorf("缺少 images[] 字段: %v", err)
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
		if err != nil || string(data) != "image-bytes" {
			t.Errorf("文件内容 = %q, 错误 = %v", data, err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":    200,
			"message": "上传成功",
			"data": map[string]any{
				"files": []map[string]any{
					{"success": true, "message": "上传成功", "url": "/uploads/2026/03/abc.webp"},
				},
			},
		})
	}))
	defer server.Close()

	adapter := testAdapter(t, server.URL)
	result, err := adapter.Put(context.Background(), "0f/foo.png", strings.NewReader("image-bytes"), int64(len("image-bytes")), "image/png")
	if err != nil {
		t.Fatalf("Put 失败: %v", err)
	}
	if !called {
		t.Error("未收到上传请求")
	}
	want := server.URL + "/uploads/2026/03/abc.webp"
	if result.URL != want {
		t.Errorf("直链 = %q, 期望 %q", result.URL, want)
	}
}

func TestPutKeepsAbsoluteURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"code":200,"data":{"files":[{"success":true,"url":"https://cdn.example.com/uploads/a.webp"}]}}`)
	}))
	defer server.Close()

	adapter := testAdapter(t, server.URL)
	result, err := adapter.Put(context.Background(), "foo.png", strings.NewReader("x"), 1, "image/png")
	if err != nil {
		t.Fatalf("Put 失败: %v", err)
	}
	if result.URL != "https://cdn.example.com/uploads/a.webp" {
		t.Errorf("直链 = %q", result.URL)
	}
}

func TestPutErrors(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		wantSub string
	}{
		{"HTTP 错误", http.StatusBadRequest, `{"code":400,"message":"文件解析失败"}`, "HTTP 400"},
		{"业务错误", http.StatusOK, `{"code":401,"message":"Token 无效"}`, "code=401"},
		{"非法 JSON", http.StatusOK, "not-json", "解析初春图床响应失败"},
		{"没有文件", http.StatusOK, `{"code":200,"data":{"files":[]}}`, "未返回文件信息"},
		{"文件失败", http.StatusOK, `{"code":200,"data":{"files":[{"success":false,"message":"图片过大"}]}}`, "图片过大"},
		{"路径为空", http.StatusOK, `{"code":200,"data":{"files":[{"success":true}]}}`, "图片路径为空"},
		{"协议相对路径", http.StatusOK, `{"code":200,"data":{"files":[{"success":true,"url":"//evil.example/a.png"}]}}`, "路径无效"},
		{"非法绝对地址", http.StatusOK, `{"code":200,"data":{"files":[{"success":true,"url":"ftp://evil.example/a.png"}]}}`, "直链无效"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(testCase.status)
				_, _ = io.WriteString(w, testCase.body)
			}))
			defer server.Close()

			adapter := testAdapter(t, server.URL)
			_, err := adapter.Put(context.Background(), "foo.png", strings.NewReader("x"), 1, "image/png")
			if err == nil || !strings.Contains(err.Error(), testCase.wantSub) {
				t.Fatalf("错误 = %v, 期望包含 %q", err, testCase.wantSub)
			}
		})
	}
}

func TestPutRejectsNonImage(t *testing.T) {
	adapter := testAdapter(t, "http://127.0.0.1:1")
	if _, err := adapter.Put(context.Background(), "foo.txt", strings.NewReader("text"), 4, "text/plain"); err == nil {
		t.Error("非图片内容应被拒绝")
	}
}

func TestUnsupportedOperations(t *testing.T) {
	adapter := &adapter{}
	if err := adapter.Delete(context.Background(), "foo.png"); err != storage.ErrNotSupported {
		t.Errorf("Delete 错误 = %v", err)
	}
	if _, err := adapter.Stat(context.Background(), "foo.png"); err != storage.ErrNotSupported {
		t.Errorf("Stat 错误 = %v", err)
	}
	if _, err := adapter.List(context.Background(), "foo"); err != storage.ErrNotSupported {
		t.Errorf("List 错误 = %v", err)
	}
	if _, err := adapter.Read(context.Background(), "foo.png"); err != storage.ErrNotSupported {
		t.Errorf("Read 错误 = %v", err)
	}
}
