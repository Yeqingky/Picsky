package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/yeqingky/Picsky/internal/apperr"
	"github.com/yeqingky/Picsky/internal/middleware"
	"github.com/yeqingky/Picsky/internal/service"
	"github.com/yeqingky/Picsky/internal/storage"
)

// adminPolicyDrivers 返回已注册存储后端类型与字段定义.
func (s *Server) adminPolicyDrivers(c *gin.Context) {
	c.JSON(http.StatusOK, storage.Specs())
}

// adminListPolicies 策略列表.
func (s *Server) adminListPolicies(c *gin.Context) {
	views, err := s.svc.Policies.List()
	if err != nil {
		s.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, views)
}

// adminCreatePolicy 创建策略.
func (s *Server) adminCreatePolicy(c *gin.Context) {
	principal := middleware.Principal(c)
	var payload service.PolicyInput
	if err := c.ShouldBindJSON(&payload); err != nil {
		s.fail(c, apperr.ErrInvalidRequest.WithMessage("请求体解析失败: "+err.Error()))
		return
	}
	policy, err := s.svc.Policies.Create(payload, principal, clientIP(c), userAgent(c))
	if err != nil {
		s.fail(c, err)
		return
	}
	view, err := s.svc.Policies.View(policy)
	if err != nil {
		s.fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, view)
}

// adminUpdatePolicy 更新策略.
func (s *Server) adminUpdatePolicy(c *gin.Context) {
	principal := middleware.Principal(c)
	id, err := idParam(c, "id")
	if err != nil {
		s.fail(c, err)
		return
	}
	var payload service.PolicyInput
	if err := c.ShouldBindJSON(&payload); err != nil {
		s.fail(c, apperr.ErrInvalidRequest.WithMessage("请求体解析失败: "+err.Error()))
		return
	}
	policy, err := s.svc.Policies.Update(id, payload, principal, clientIP(c), userAgent(c))
	if err != nil {
		s.fail(c, err)
		return
	}
	view, err := s.svc.Policies.View(policy)
	if err != nil {
		s.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, view)
}

// adminDeletePolicy 删除策略.
func (s *Server) adminDeletePolicy(c *gin.Context) {
	principal := middleware.Principal(c)
	id, err := idParam(c, "id")
	if err != nil {
		s.fail(c, err)
		return
	}
	if err := s.svc.Policies.Delete(id, principal, clientIP(c), userAgent(c)); err != nil {
		s.fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// adminTestPolicy 连接测试.
func (s *Server) adminTestPolicy(c *gin.Context) {
	id, err := idParam(c, "id")
	if err != nil {
		s.fail(c, err)
		return
	}
	elapsed, testErr := s.svc.Policies.Test(c.Request.Context(), id)
	response := gin.H{
		"ok":         testErr == nil,
		"elapsed_ms": elapsed.Milliseconds(),
		"error":      nil,
	}
	if testErr != nil {
		response["error"] = apperr.From(testErr).Message
	}
	c.JSON(http.StatusOK, response)
}

// adminPreviewPolicy 路径与 URL 预览.
func (s *Server) adminPreviewPolicy(c *gin.Context) {
	var payload service.PreviewInput
	if err := c.ShouldBindJSON(&payload); err != nil {
		s.fail(c, apperr.ErrInvalidRequest.WithMessage("请求体解析失败: "+err.Error()))
		return
	}
	result, err := s.svc.Policies.Preview(payload)
	if err != nil {
		s.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// adminScanPolicy 扫描补录.
func (s *Server) adminScanPolicy(c *gin.Context) {
	principal := middleware.Principal(c)
	id, err := idParam(c, "id")
	if err != nil {
		s.fail(c, err)
		return
	}
	var payload struct {
		Path   *string `json:"path"`
		DryRun bool    `json:"dry_run"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil && err.Error() != "EOF" {
		s.fail(c, apperr.ErrInvalidRequest.WithMessage("请求体解析失败: "+err.Error()))
		return
	}
	result, err := s.svc.Policies.Scan(c.Request.Context(), id, principal.User.ID, payload.Path, payload.DryRun)
	if err != nil {
		s.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// adminGetSettings 读取系统设置.
func (s *Server) adminGetSettings(c *gin.Context) {
	response := gin.H{}
	for key, value := range s.svc.Settings.VisibleValues() {
		response[key] = value
	}
	response["captcha_secret_set"] = s.svc.Settings.HasSecret("captcha_secret")
	response["smtp_password_set"] = s.svc.Settings.HasSecret("smtp_password")
	response["definitions"] = s.svc.Settings.Definitions()
	c.JSON(http.StatusOK, response)
}

// adminUpdateSettings 更新系统设置.
func (s *Server) adminUpdateSettings(c *gin.Context) {
	principal := middleware.Principal(c)
	var payload map[string]any
	if err := c.ShouldBindJSON(&payload); err != nil {
		s.fail(c, apperr.ErrInvalidRequest.WithMessage("请求体解析失败: "+err.Error()))
		return
	}
	values := make(map[string]string, len(payload))
	for key, raw := range payload {
		switch key {
		case "captcha_secret_set", "smtp_password_set", "definitions":
			continue
		}
		value, err := settingValue(raw)
		if err != nil {
			s.fail(c, apperr.ErrInvalidRequest.WithMessage(fmt.Sprintf("设置项 %s 的值非法: %s", key, err.Error())))
			return
		}
		values[key] = value
	}
	if err := s.svc.Settings.Update(values); err != nil {
		s.fail(c, err)
		return
	}
	s.svc.Audit.Recordf(serviceEntry(principal, "settings_update", "settings", 0, map[string]any{"keys": mapKeys(values)}, clientIP(c), userAgent(c)))
	s.adminGetSettings(c)
}

// adminListTasks 维护队列列表.
func (s *Server) adminListTasks(c *gin.Context) {
	page, pageSize := pagination(c, 20, 200)
	result, err := s.svc.Tasks.List(service.TaskFilter{
		Page:     page,
		PageSize: pageSize,
		Type:     c.Query("type"),
		Status:   c.Query("status"),
	})
	if err != nil {
		s.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// adminRunTask 手动执行任务.
func (s *Server) adminRunTask(c *gin.Context) {
	principal := middleware.Principal(c)
	id, err := idParam(c, "id")
	if err != nil {
		s.fail(c, err)
		return
	}
	if err := s.svc.Tasks.RunNow(id, principal); err != nil {
		s.fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// adminPurgeTasks 立即清理到期的软删记录.
func (s *Server) adminPurgeTasks(c *gin.Context) {
	principal := middleware.Principal(c)
	created, err := s.svc.Tasks.PurgeExpired(principal)
	if err != nil {
		s.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"created": created})
}

// adminListLogs 审计日志列表.
func (s *Server) adminListLogs(c *gin.Context) {
	page, pageSize := pagination(c, 20, 200)
	filter := service.AuditFilter{
		Page:     page,
		PageSize: pageSize,
		Action:   c.Query("action"),
	}
	if raw := strings.TrimSpace(c.Query("user_id")); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			s.fail(c, apperr.ErrInvalidRequest.WithMessage("user_id 必须是整数"))
			return
		}
		filter.UserID = &parsed
	}
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
		endOfDay := to.UTC().AddDate(0, 0, 1).Add(-1)
		to = &endOfDay
	}
	filter.From = from
	filter.To = to

	result, err := s.svc.Audit.List(filter)
	if err != nil {
		s.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// settingValue 把 JSON 值转成设置字符串.
func settingValue(raw any) (string, error) {
	switch value := raw.(type) {
	case nil:
		return "", nil
	case string:
		return value, nil
	case bool:
		return strconv.FormatBool(value), nil
	case float64:
		if value == float64(int64(value)) {
			return strconv.FormatInt(int64(value), 10), nil
		}
		return "", fmt.Errorf("不支持小数")
	default:
		return "", fmt.Errorf("不支持的类型")
	}
}

// mapKeys 返回 map 的键列表 (用于审计摘要).
func mapKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}
