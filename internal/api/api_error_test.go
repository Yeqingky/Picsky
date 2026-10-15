package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/yeqingky/Picsky/internal/apperr"
)

func TestFailSanitizesBackendError(t *testing.T) {
	const leaked = "dial tcp 10.24.3.7:8443: storage.internal.example"
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/upload", nil)
	server := &Server{log: slog.New(slog.NewTextHandler(io.Discard, nil))}

	server.fail(ctx, apperr.ErrBackend.WithMessage(leaked).WithDetails(map[string]any{"endpoint": leaked}))

	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("wrong HTTP status: got %d, want %d", recorder.Code, http.StatusBadGateway)
	}
	var response apperr.Error
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Code != apperr.ErrBackend.Code || response.Message != apperr.ErrBackend.Message || response.Details != nil {
		t.Fatalf("wrong sanitized API error: %#v", response)
	}
	if strings.Contains(recorder.Body.String(), leaked) {
		t.Fatalf("response leaked backend details: %s", recorder.Body.String())
	}
}
