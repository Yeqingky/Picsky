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

func TestFromPreservesClientErrors(t *testing.T) {
	original := ErrInvalidRequest.WithMessage("参数非法").WithDetails(map[string]any{"field": "name"})
	got := From(original)
	if got != original {
		t.Fatalf("client error should remain unchanged: got %#v, want %#v", got, original)
	}
}
