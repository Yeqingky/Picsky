package apperr

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestFromSanitizesServerErrors(t *testing.T) {
	const leaked = "dial tcp 10.24.3.7:8443: storage.internal.example"
	tests := []struct {
		name   string
		err    error
		status int
		code   string
		msg    string
	}{
		{
			name:   "backend error",
			err:    ErrBackend.WithMessage(leaked).WithDetails(map[string]any{"endpoint": leaked}),
			status: http.StatusBadGateway,
			code:   ErrBackend.Code,
			msg:    ErrBackend.Message,
		},
		{
			name:   "internal error",
			err:    ErrInternal.WithMessage(leaked).WithDetails(map[string]any{"cause": leaked}),
			status: http.StatusInternalServerError,
			code:   ErrInternal.Code,
			msg:    ErrInternal.Message,
		},
		{
			name:   "untyped error",
			err:    errors.New(leaked),
			status: http.StatusInternalServerError,
			code:   ErrInternal.Code,
			msg:    ErrInternal.Message,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := From(test.err)
			if got.Status != test.status || got.Code != test.code || got.Message != test.msg {
				t.Fatalf("unexpected sanitized error: %#v", got)
			}
			if got.Details != nil {
				t.Fatalf("server error details must not be exposed: %#v", got.Details)
			}
			if strings.Contains(got.Error(), leaked) {
				t.Fatalf("server error leaked its cause: %q", got.Error())
			}
		})
	}
}

func TestExposePreservesOriginalError(t *testing.T) {
	original := ErrBackend.WithMessage("dial tcp 10.24.3.7:8443: connection refused").
		WithDetails(map[string]any{"policy_id": 7})
	got := Expose(original)
	if got.Status != original.Status || got.Code != original.Code || got.Message != original.Message {
		t.Fatalf("Expose 应保留原始错误: got %#v, want %#v", got, original)
	}
	if got.Details == nil || got.Details["policy_id"] != 7 {
		t.Fatalf("Expose 应保留 details: %#v", got.Details)
	}
	if got == original {
		t.Fatal("Expose 应返回副本, 避免修改预定义错误")
	}

	plain := errors.New("unexpected backend failure")
	exposed := Expose(plain)
	if exposed.Status != http.StatusInternalServerError || exposed.Code != ErrInternal.Code {
		t.Fatalf("未包装错误的状态码 = %#v", exposed)
	}
	if exposed.Message != plain.Error() {
		t.Fatalf("未包装错误的消息 = %q, want %q", exposed.Message, plain.Error())
	}

	if Expose(nil) != nil {
		t.Fatal("Expose(nil) 应返回 nil")
	}
}

func TestFromPreservesClientErrors(t *testing.T) {
	original := ErrInvalidRequest.WithMessage("参数非法").WithDetails(map[string]any{"field": "name"})
	got := From(original)
	if got != original {
		t.Fatalf("client error should remain unchanged: got %#v, want %#v", got, original)
	}
}
