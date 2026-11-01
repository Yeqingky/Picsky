package api_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestJSONBodyLimitIsPathBased(t *testing.T) {
	s := newServer(t)
	payload := []byte(`{"identifier":"body-limit@example.com","password":"` +
		strings.Repeat("x", 2<<20) + `"}`)

	send := func(contentType string) (*httptest.ResponseRecorder, *countingReadCloser) {
		counted := &countingReadCloser{reader: bytes.NewReader(payload)}
		request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", counted)
		request.ContentLength = int64(len(payload))
		request.Header.Set("Content-Type", contentType)
		recorder := httptest.NewRecorder()
		s.engine.ServeHTTP(recorder, request)
		return recorder, counted
	}

	// 伪造 multipart 请求头不能绕过 JSON 接口的请求体上限, 也不能让登录处理器解析请求体.
	spoofed, spoofedBody := send("multipart/form-data; boundary=xxx")
	if spoofed.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("非 JSON Content-Type 应返回 415, 实际 %d %s", spoofed.Code, spoofed.Body.String())
	}
	if spoofedBody.bytesRead != 0 {
		t.Errorf("Content-Type 校验失败时不应读取请求体, 实际读取 %d 字节", spoofedBody.bytesRead)
	}

	// JSON 接口的 1 MiB 上限仍然生效, 读取量不能随请求体无限增长.
	jsonResult, jsonBody := send("application/json")
	if jsonResult.Code != http.StatusBadRequest {
		t.Fatalf("超过 1 MiB 的 JSON 请求应返回 400, 实际 %d %s", jsonResult.Code, jsonResult.Body.String())
	}
	if jsonBody.bytesRead > int64(1<<20)+1 {
		t.Errorf("JSON 请求体读取超过上限: %d 字节", jsonBody.bytesRead)
	}
}

func TestLoginRejectsFormContentType(t *testing.T) {
	s := newServer(t)
	result := s.raw(http.MethodPost, "/api/v1/auth/login", strings.NewReader(
		`{"identifier":"csrf@example.com","password":"password123"}`,
	), map[string]string{"Content-Type": "text/plain"}, nil)
	if result.Status != http.StatusUnsupportedMediaType {
		t.Fatalf("登录接口应拒绝非 JSON 表单提交, 实际 %d %s", result.Status, string(result.Body))
	}
	if s.decode(result)["code"] != "unsupported_media_type" {
		t.Errorf("错误码不正确: %s", string(result.Body))
	}
}
