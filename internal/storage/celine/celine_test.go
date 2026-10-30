package celine

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
		t.Fatal("Celine 驱动未注册")
	}
	if spec.Label != "思琳中国" {
		t.Errorf("驱动名称 = %q, 期望 思琳中国", spec.Label)
	}
	if !spec.URLFromBackend {
		t.Error("Celine 驱动应由后端分配直链")
	}
	if len(spec.Capabilities) != 1 || spec.Capabilities[0] != "upload" {
		t.Errorf("Celine 驱动应只声明上传能力: %v", spec.Capabilities)
	}
	if !spec.Has(storage.CapUpload) {
		t.Error("Celine 驱动应声明上传能力")
	}
	if len(spec.ConfigFields) != 0 || len(spec.SecretFields) != 0 {
		t.Error("Celine 驱动不应有配置项")
	}
	if spec.MaxFileSize != maxFileSize {
		t.Errorf("Celine 单文件上限 = %d, 期望 %d", spec.MaxFileSize, maxFileSize)
	}
	if len(spec.Formats) != 3 || spec.Formats[0] != "png" || spec.Formats[1] != "jpeg" || spec.Formats[2] != "gif" {
		t.Errorf("Celine 支持格式 = %v, 期望 png/jpeg/gif", spec.Formats)
	}
	if spec.MaxFileSize >= 20<<20 {
		t.Errorf("Celine 单文件上限必须低于 20 MiB 请求体上限: %d", spec.MaxFileSize)
	}
	if spec.Has(storage.CapDelete) {
		t.Error("Celine 驱动不应声明删除能力")
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
			"type":    "image",
			"brand":   "celine",
			"channel": "web",
			"attachment": map[string]any{
				"uuid":     "8DFCC16D391176C8FBB8D2AF93CBCBFE",
				"filename": "8DFCC16D391176C8FBB8D2AF93CBCBFE.png",
				"filetype": "image",
				"filesize": len("image-bytes"),
				"url":      "/celine/20261030/8DFCC16D391176C8FBB8D2AF93CBCBFE.png",
			},
		})
	}))
	defer server.Close()

	adapter := newWithBase(server.URL, server.URL+"/livechat/uploadfile")
	result, err := adapter.Put(context.Background(), "0f/foo.png", strings.NewReader("image-bytes"), int64(len("image-bytes")), "image/png")
	if err != nil {
		t.Fatalf("Put 失败: %v", err)
	}
	if !called {
		t.Error("未收到上传请求")
	}
	want := server.URL + "/livechat/uploadfile/celine/20261030/8DFCC16D391176C8FBB8D2AF93CBCBFE.png"
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

func TestPutErrors(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		wantSub string
	}{
		{"后端错误", http.StatusInternalServerError, "boom", "HTTP 500"},
		{"请求体过大", http.StatusRequestEntityTooLarge, "<html>413</html>", "HTTP 413"},
		{"非法 JSON", http.StatusOK, "not-json", "解析 Celine 响应失败"},
		{"缺少附件", http.StatusOK, `{"type":"image"}`, "附件路径为空"},
		{"相对路径缺少斜杠", http.StatusOK, `{"attachment":{"url":"celine/a.png"}}`, "附件路径无效"},
		{"协议相对路径", http.StatusOK, `{"attachment":{"url":"//evil.example/a.png"}}`, "附件路径无效"},
		{"绝对路径", http.StatusOK, `{"attachment":{"url":"https://evil.example/a.png"}}`, "附件路径无效"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(testCase.status)
				_, _ = io.WriteString(w, testCase.body)
			}))
			defer server.Close()

			adapter := newWithBase(server.URL, server.URL+"/livechat/uploadfile")
			_, err := adapter.Put(context.Background(), "0f/foo.png", strings.NewReader("image-bytes"), int64(len("image-bytes")), "image/png")
			if err == nil || !strings.Contains(err.Error(), testCase.wantSub) {
				t.Fatalf("错误 = %v, 期望包含 %q", err, testCase.wantSub)
			}
		})
	}
}
