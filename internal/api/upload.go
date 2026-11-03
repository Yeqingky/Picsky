package api

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/yeqingky/Picsky/internal/httpupload"
	"github.com/yeqingky/Picsky/internal/middleware"
	"github.com/yeqingky/Picsky/internal/model"
	"github.com/yeqingky/Picsky/internal/service"
)

// upload 处理 multipart 上传.
func (s *Server) upload(c *gin.Context) {
	principal := middleware.Principal(c)
	permit, err := s.svc.Upload.Preflight(principal)
	if err != nil {
		s.fail(c, err)
		return
	}
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
		Permit:    permit,
	})
	if err != nil {
		s.fail(c, err)
		return
	}
	c.JSON(upload.Status, s.toUploadDTO(upload))
}

// uploadBase64 处理 JSON base64 上传.
func (s *Server) uploadBase64(c *gin.Context) {
	principal := middleware.Principal(c)
	permit, err := s.svc.Upload.Preflight(principal)
	if err != nil {
		s.fail(c, err)
		return
	}
	limit := service.UploadLimit(s.svc.Settings, principal.Group)

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, base64UploadRequestBodyLimit(limit))
	payload, err := readBase64UploadPayload(c.Request.Body, s.cfg.TempDir, s.cfg.UploadMemoryLimit, limit)
	if err != nil {
		s.fail(c, err)
		return
	}
	defer payload.Spooled.Spool.Close()

	upload, err := s.svc.Upload.Upload(c.Request.Context(), service.UploadRequest{
		Principal: principal,
		PolicyID:  payload.PolicyID,
		Spooled:   payload.Spooled,
		Source:    s.sourceOf(principal),
		ClientIP:  clientIP(c),
		UserAgent: userAgent(c),
		Permit:    permit,
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
