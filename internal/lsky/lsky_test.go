package lsky

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/yeqingky/Picsky/internal/apperr"
)

func TestWriteErrorUsesServerStatusWithoutLeakingDetails(t *testing.T) {
	const leaked = "dial tcp 10.24.3.7:8443: storage.internal.example"
	gin.SetMode(gin.TestMode)
	handler := New(nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	tests := []struct {
		name   string
		err    error
		status int
		msg    string
	}{
		{
			name:   "backend failure",
			err:    apperr.ErrBackend.WithMessage(leaked).WithDetails(map[string]any{"endpoint": leaked}),
			status: http.StatusBadGateway,
			msg:    apperr.ErrBackend.Message,
		},
		{
			name:   "internal failure",
			err:    apperr.ErrInternal.WithMessage(leaked),
			status: http.StatusInternalServerError,
			msg:    apperr.ErrInternal.Message,
		},
		{
			name:   "untyped failure",
			err:    errors.New(leaked),
			status: http.StatusInternalServerError,
			msg:    apperr.ErrInternal.Message,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router := gin.New()
			router.GET("/failure", func(c *gin.Context) { handler.writeError(c, test.err) })
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/failure", nil))

			if recorder.Code != test.status {
				t.Fatalf("wrong HTTP status: got %d, want %d", recorder.Code, test.status)
			}
			var response envelope
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if response.Status || response.Message != test.msg {
				t.Fatalf("wrong error envelope: %#v", response)
			}
			if strings.Contains(recorder.Body.String(), leaked) {
				t.Fatalf("response leaked server error details: %s", recorder.Body.String())
			}
		})
	}
}
