package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/yeqingky/Picsky/internal/apperr"
	"github.com/yeqingky/Picsky/internal/middleware"
	"github.com/yeqingky/Picsky/internal/service"
)

// listImages 返回图片列表.
func (s *Server) listImages(c *gin.Context) {
	principal := middleware.Principal(c)
	page, pageSize := pagination(c, int(s.svc.Settings.Int64("gallery_page_size", 40)), 200)

	from, err := timeParam(c, "from")
	if err != nil {
		s.fail(c, err)
		return
	}
	to, err := timeParam(c, "to")
	if err != nil {
		s.fail(c, err)
		return
	}
	if to != nil {
		endOfTo := to.UTC().AddDate(0, 0, 1).Add(-1)
		to = &endOfTo
	}

	var policyID *int64
	if raw := strings.TrimSpace(c.Query("policy_id")); raw != "" {
		parsed, parseErr := strconv.ParseInt(raw, 10, 64)
		if parseErr != nil || parsed <= 0 {
			s.fail(c, apperr.ErrInvalidRequest.WithMessage("policy_id 必须是正整数"))
			return
		}
		policyID = &parsed
	}

	result, err := s.svc.Images.List(principal, service.ListFilter{
		Page:      page,
		PageSize:  pageSize,
		Keyword:   c.Query("keyword"),
		Format:    c.Query("format"),
		UserEmail: c.Query("user"),
		From:      from,
		To:        to,
		PolicyID:  policyID,
	})
	if err != nil {
		s.fail(c, err)
		return
	}
	items, err := s.toImageDTOs(result.Items)
	if err != nil {
		s.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"total":     result.Total,
		"page":      result.Page,
		"page_size": result.PageSize,
		"items":     items,
	})
}

// getImage 返回记录详情.
func (s *Server) getImage(c *gin.Context) {
	principal := middleware.Principal(c)
	id, err := idParam(c, "id")
	if err != nil {
		s.fail(c, err)
		return
	}
	record, err := s.svc.Images.Get(principal, id)
	if err != nil {
		s.fail(c, err)
		return
	}
	metas, err := s.metaFor([]int64{record.PolicyID})
	if err != nil {
		s.fail(c, err)
		return
	}
	emails, err := s.svc.Images.UserEmails([]int64{record.UserID})
	if err != nil {
		s.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, s.toImageDTO(record, metas[record.PolicyID], emails[record.UserID]))
}

// deleteImage 删除记录.
func (s *Server) deleteImage(c *gin.Context) {
	principal := middleware.Principal(c)
	id, err := idParam(c, "id")
	if err != nil {
		s.fail(c, err)
		return
	}
	result, err := s.svc.Images.Delete(principal, id, clientIP(c), userAgent(c))
	if err != nil {
		s.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// batchDeleteImages 批量删除.
func (s *Server) batchDeleteImages(c *gin.Context) {
	principal := middleware.Principal(c)
	var payload struct {
		IDs []int64 `json:"ids"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		s.fail(c, apperr.ErrInvalidRequest.WithMessage("请求体解析失败: "+err.Error()))
		return
	}
	results, err := s.svc.Images.BatchDelete(principal, payload.IDs, clientIP(c), userAgent(c))
	if err != nil {
		s.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": results})
}

// userPolicies 返回当前用户可用的存储策略.
func (s *Server) userPolicies(c *gin.Context) {
	principal := middleware.Principal(c)
	policies, err := s.svc.Policies.AllowedFor(principal)
	if err != nil {
		s.fail(c, err)
		return
	}
	items := make([]gin.H, 0, len(policies))
	defaultPolicyID := int64(0)
	if requested := principal.User.DefaultPolicyID; requested != nil {
		defaultPolicyID = *requested
	}
	found := false
	for _, policy := range policies {
		if policy.ID == defaultPolicyID {
			found = true
		}
	}
	if !found && len(policies) > 0 {
		defaultPolicyID = policies[0].ID
	}
	for _, policy := range policies {
		urls, err := s.svc.Policies.URLs(policy.ID)
		if err != nil {
			s.fail(c, err)
			return
		}
		urlItems := make([]urlDTO, 0, len(urls))
		for _, url := range urls {
			urlItems = append(urlItems, urlDTO{Alias: url.Alias, URL: url.URL, IsDefault: url.IsDefault})
		}
		item := gin.H{
			"id":         policy.ID,
			"name":       policy.Name,
			"type":       policy.Type,
			"urls":       urlItems,
			"is_default": policy.ID == defaultPolicyID,
		}
		items = append(items, item)
	}
	c.JSON(http.StatusOK, items)
}
