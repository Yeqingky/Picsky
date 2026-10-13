package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/yeqingky/Picsky/internal/apperr"
	"github.com/yeqingky/Picsky/internal/middleware"
	"github.com/yeqingky/Picsky/internal/model"
)

// apiKeyDTO 是 API 密钥下发结构.
type apiKeyDTO struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	AccessKey  string     `json:"access_key"`
	Scopes     []string   `json:"scopes"`
	ScopesBits *int64     `json:"scopes_bits"`
	LastUsedAt *time.Time `json:"last_used_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
	CreatedAt  time.Time  `json:"created_at"`
	Secret     string     `json:"secret,omitempty"`
}

// toKeyDTO 组装密钥下发结构.
func toKeyDTO(key *model.APIKey) apiKeyDTO {
	dto := apiKeyDTO{
		ID:         key.ID,
		Name:       key.Name,
		AccessKey:  key.AccessKey,
		Scopes:     []string{},
		LastUsedAt: key.LastUsedAt,
		ExpiresAt:  key.ExpiresAt,
		RevokedAt:  key.RevokedAt,
		CreatedAt:  key.CreatedAt,
	}
	if key.Scopes != nil {
		bits := *key.Scopes
		dto.ScopesBits = &bits
		dto.Scopes = model.Permission(bits).Names()
	}
	return dto
}

// listKeys 返回当前用户的密钥列表.
func (s *Server) listKeys(c *gin.Context) {
	principal := middleware.Principal(c)
	keys, err := s.svc.Keys.List(principal.User.ID)
	if err != nil {
		s.fail(c, err)
		return
	}
	items := make([]apiKeyDTO, 0, len(keys))
	for idx := range keys {
		items = append(items, toKeyDTO(&keys[idx]))
	}
	c.JSON(http.StatusOK, items)
}

// createKey 创建密钥, secret 仅此一次返回.
func (s *Server) createKey(c *gin.Context) {
	principal := middleware.Principal(c)
	var payload struct {
		Name      string   `json:"name"`
		Scopes    []string `json:"scopes"`
		ExpiresAt *string  `json:"expires_at"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		s.fail(c, apperr.ErrInvalidRequest.WithMessage("请求体解析失败: "+err.Error()))
		return
	}

	var scopes *model.Permission
	if len(payload.Scopes) > 0 {
		bits, err := model.PermissionsFromNames(payload.Scopes)
		if err != nil {
			s.fail(c, apperr.ErrInvalidRequest.WithMessage(err.Error()))
			return
		}
		if bits & ^principal.Permissions != 0 {
			s.fail(c, apperr.ErrInvalidRequest.WithMessage("密钥权限不能超出当前用户权限"))
			return
		}
		scopes = &bits
	}

	var expiresAt *time.Time
	if payload.ExpiresAt != nil && strings.TrimSpace(*payload.ExpiresAt) != "" {
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(*payload.ExpiresAt))
		if err != nil {
			s.fail(c, apperr.ErrInvalidRequest.WithMessage("expires_at 需要 RFC3339 格式"))
			return
		}
		utc := parsed.UTC()
		if utc.Before(time.Now()) {
			s.fail(c, apperr.ErrInvalidRequest.WithMessage("过期时间必须晚于当前时间"))
			return
		}
		expiresAt = &utc
	}

	key, secret, err := s.svc.Keys.Create(principal.User.ID, payload.Name, scopes, expiresAt)
	if err != nil {
		s.fail(c, err)
		return
	}
	dto := toKeyDTO(key)
	dto.Secret = secret

	s.svc.Audit.Recordf(serviceEntry(principal, "key_create", "api_key", key.ID, map[string]any{"name": key.Name, "scopes": dto.Scopes}, clientIP(c), userAgent(c)))
	c.JSON(http.StatusCreated, dto)
}

// revokeKey 吊销密钥.
func (s *Server) revokeKey(c *gin.Context) {
	principal := middleware.Principal(c)
	id, err := idParam(c, "id")
	if err != nil {
		s.fail(c, err)
		return
	}
	if err := s.svc.Keys.Revoke(principal.User.ID, id); err != nil {
		s.fail(c, err)
		return
	}
	s.svc.Audit.Recordf(serviceEntry(principal, "key_revoke", "api_key", id, nil, clientIP(c), userAgent(c)))
	c.Status(http.StatusNoContent)
}
