package api

import (
	"bytes"
	"encoding/base64"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/yeqingky/Picsky/internal/apperr"
	"github.com/yeqingky/Picsky/internal/httpupload"
	"github.com/yeqingky/Picsky/internal/middleware"
	"github.com/yeqingky/Picsky/internal/model"
	"github.com/yeqingky/Picsky/internal/service"
)

// upload 处理 multipart 上传.
func (s *Server) upload(c *gin.Context) {
	principal := middleware.Principal(c)
	limit := service.UploadLimit(s.svc.Settings, principal.Group)

	result, err := httpupload.Read(c, s.cfg.TempDir, s.cfg.UploadMemoryLimit, limit)
	if err != nil {
		s.fail(c, err)
		return
	}
	defer result.Close()

	policyID, err := result.PolicyID("policy_id")
	if err != nil {
		s.fail(c, err)
		return
	}
	if override := strings.TrimSpace(result.Fields["original_filename"]); override != "" {
		result.Spooled.Filename = override
	}

	upload, err := s.svc.Upload.Upload(c.Request.Context(), service.UploadRequest{
		Principal: principal,
		PolicyID:  policyID,
		Spooled:   result.Spooled,
		Source:    s.sourceOf(principal),
		ClientIP:  clientIP(c),
		UserAgent: userAgent(c),
	})
	if err != nil {
		s.fail(c, err)
		return
	}
	c.JSON(upload.Status, s.toUploadDTO(upload))
}

// uploadCheck 秒传检查.
func (s *Server) uploadCheck(c *gin.Context) {
	principal := middleware.Principal(c)
	var payload struct {
		SHA256   string `json:"sha256"`
		PolicyID *int64 `json:"policy_id"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		s.fail(c, apperr.ErrInvalidRequest.WithMessage("请求体解析失败: "+err.Error()))
		return
	}
	result, err := s.svc.Upload.Check(principal, payload.SHA256, payload.PolicyID)
	if err != nil {
		s.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// uploadBase64 处理 JSON base64 上传.
func (s *Server) uploadBase64(c *gin.Context) {
	principal := middleware.Principal(c)
	limit := service.UploadLimit(s.svc.Settings, principal.Group)

	// 先用请求体上限拦住超大 JSON: base64 膨胀约 4/3, 再留少量余量.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit/3*4+4096)

	var payload struct {
		Filename string `json:"filename"`
		Data     string `json:"data"`
		PolicyID *int64 `json:"policy_id"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		s.fail(c, apperr.ErrInvalidRequest.WithMessage("请求体解析失败: "+err.Error()))
		return
	}
	raw, err := decodeBase64Payload(payload.Data)
	if err != nil {
		s.fail(c, err)
		return
	}
	if int64(len(raw)) > limit {
		s.fail(c, apperr.ErrFileTooLarge.WithDetails(map[string]any{"size": len(raw), "limit": limit}))
		return
	}

	spooled, err := service.SpoolReader(s.cfg.TempDir, s.cfg.UploadMemoryLimit, bytes.NewReader(raw), payload.Filename, limit)
	if err != nil {
		s.fail(c, err)
		return
	}
	defer spooled.Spool.Close()

	upload, err := s.svc.Upload.Upload(c.Request.Context(), service.UploadRequest{
		Principal: principal,
		PolicyID:  payload.PolicyID,
		Spooled:   spooled,
		Source:    s.sourceOf(principal),
		ClientIP:  clientIP(c),
		UserAgent: userAgent(c),
	})
	if err != nil {
		s.fail(c, err)
		return
	}
	c.JSON(upload.Status, s.toUploadDTO(upload))
}

// sourceOf 判断请求来源: API 密钥请求记为 api, 其余记为 web.
func (s *Server) sourceOf(principal *service.Principal) string {
	if principal != nil && principal.APIKeyID != nil {
		return model.SourceAPI
	}
	return model.SourceWeb
}

// decodeBase64Payload 解析 base64 或 data URL 形式的内容.
func decodeBase64Payload(data string) ([]byte, error) {
	value := strings.TrimSpace(data)
	if value == "" {
		return nil, apperr.ErrInvalidRequest.WithMessage("缺少 data 字段")
	}
	if strings.HasPrefix(value, "data:") {
		idx := strings.Index(value, ",")
		if idx < 0 {
			return nil, apperr.ErrInvalidRequest.WithMessage("data URL 格式非法")
		}
		header := value[:idx]
		if !strings.Contains(header, ";base64") {
			return nil, apperr.ErrInvalidRequest.WithMessage("data URL 必须是 base64 编码")
		}
		value = value[idx+1:]
	}
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		decoded, err = base64.RawURLEncoding.DecodeString(value)
		if err != nil {
			return nil, apperr.ErrInvalidRequest.WithMessage("base64 解码失败: " + err.Error())
		}
	}
	return decoded, nil
}
