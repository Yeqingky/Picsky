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

// adminStats 返回后台概览.
func (s *Server) adminStats(c *gin.Context) {
	overview, err := s.svc.Stats.Overview()
	if err != nil {
		s.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, overview)
}

// adminListUsers 用户列表.
func (s *Server) adminListUsers(c *gin.Context) {
	page, pageSize := pagination(c, 20, 200)
	filter := service.UserListFilter{
		Page:     page,
		PageSize: pageSize,
		Keyword:  c.Query("keyword"),
	}
	if raw := strings.TrimSpace(c.Query("group_id")); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			s.fail(c, apperr.ErrInvalidRequest.WithMessage("group_id 必须是整数"))
			return
		}
		filter.GroupID = &parsed
	}
	if raw := strings.TrimSpace(c.Query("status")); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 16)
		if err != nil {
			s.fail(c, apperr.ErrInvalidRequest.WithMessage("status 必须是整数"))
			return
		}
		value := int16(parsed)
		filter.Status = &value
	}
	result, err := s.svc.Users.List(filter)
	if err != nil {
		s.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// adminCreateUser 创建用户.
func (s *Server) adminCreateUser(c *gin.Context) {
	principal := middleware.Principal(c)
	var payload service.UserInput
	if err := c.ShouldBindJSON(&payload); err != nil {
		s.fail(c, apperr.ErrInvalidRequest.WithMessage("请求体解析失败: "+err.Error()))
		return
	}
	user, err := s.svc.Users.Create(payload, principal, clientIP(c), userAgent(c))
	if err != nil {
		s.fail(c, err)
		return
	}
	view, err := s.svc.Users.View(user)
	if err != nil {
		s.fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, view)
}

// adminUpdateUser 更新用户.
func (s *Server) adminUpdateUser(c *gin.Context) {
	principal := middleware.Principal(c)
	id, err := idParam(c, "id")
	if err != nil {
		s.fail(c, err)
		return
	}
	var payload service.UserInput
	if err := c.ShouldBindJSON(&payload); err != nil {
		s.fail(c, apperr.ErrInvalidRequest.WithMessage("请求体解析失败: "+err.Error()))
		return
	}
	user, err := s.svc.Users.Update(id, payload, principal, clientIP(c), userAgent(c))
	if err != nil {
		s.fail(c, err)
		return
	}
	view, err := s.svc.Users.View(user)
	if err != nil {
		s.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, view)
}

// adminDeleteUser 删除用户.
func (s *Server) adminDeleteUser(c *gin.Context) {
	principal := middleware.Principal(c)
	id, err := idParam(c, "id")
	if err != nil {
		s.fail(c, err)
		return
	}
	if err := s.svc.Users.Delete(id, principal, clientIP(c), userAgent(c)); err != nil {
		s.fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// adminListGroups 用户组列表.
func (s *Server) adminListGroups(c *gin.Context) {
	groups, err := s.svc.Groups.List()
	if err != nil {
		s.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, groups)
}

// adminCreateGroup 创建用户组.
func (s *Server) adminCreateGroup(c *gin.Context) {
	principal := middleware.Principal(c)
	var payload service.GroupInput
	if err := c.ShouldBindJSON(&payload); err != nil {
		s.fail(c, apperr.ErrInvalidRequest.WithMessage("请求体解析失败: "+err.Error()))
		return
	}
	group, err := s.svc.Groups.Create(payload, principal, clientIP(c), userAgent(c))
	if err != nil {
		s.fail(c, err)
		return
	}
	view, err := s.svc.Groups.View(group)
	if err != nil {
		s.fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, view)
}

// adminUpdateGroup 更新用户组.
func (s *Server) adminUpdateGroup(c *gin.Context) {
	principal := middleware.Principal(c)
	id, err := idParam(c, "id")
	if err != nil {
		s.fail(c, err)
		return
	}
	var payload service.GroupInput
	if err := c.ShouldBindJSON(&payload); err != nil {
		s.fail(c, apperr.ErrInvalidRequest.WithMessage("请求体解析失败: "+err.Error()))
		return
	}
	group, err := s.svc.Groups.Update(id, payload, principal, clientIP(c), userAgent(c))
	if err != nil {
		s.fail(c, err)
		return
	}
	view, err := s.svc.Groups.View(group)
	if err != nil {
		s.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, view)
}

// adminDeleteGroup 删除用户组.
func (s *Server) adminDeleteGroup(c *gin.Context) {
	principal := middleware.Principal(c)
	id, err := idParam(c, "id")
	if err != nil {
		s.fail(c, err)
		return
	}
	if err := s.svc.Groups.Delete(id, principal, clientIP(c), userAgent(c)); err != nil {
		s.fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// adminSetGroupPolicies 覆盖用户组策略白名单.
func (s *Server) adminSetGroupPolicies(c *gin.Context) {
	principal := middleware.Principal(c)
	id, err := idParam(c, "id")
	if err != nil {
		s.fail(c, err)
		return
	}
	var payload struct {
		PolicyIDs []int64 `json:"policy_ids"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		s.fail(c, apperr.ErrInvalidRequest.WithMessage("请求体解析失败: "+err.Error()))
		return
	}
	if err := s.svc.Groups.SetPolicies(id, payload.PolicyIDs, principal, clientIP(c), userAgent(c)); err != nil {
		s.fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
