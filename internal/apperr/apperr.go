// Package apperr 定义 API 错误: HTTP 状态码 + 业务错误码 + 人类可读消息.
package apperr

import (
	"errors"
	"fmt"
	"net/http"
)

// Error 是带错误码的 API 错误.
type Error struct {
	Status  int            `json:"-"`
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// Error 实现 error.
func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// New 构造错误.
func New(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

// Is 让 errors.Is 能按业务错误码比较 (例如与 apperr.ErrNotFound 匹配).
func (e *Error) Is(target error) bool {
	other, ok := target.(*Error)
	if !ok {
		return false
	}
	if other.Code == "" {
		return e.Status == other.Status
	}
	return e.Code == other.Code
}

// WithDetails 返回带附加详情的副本 (不修改原对象).
func (e *Error) WithDetails(details map[string]any) *Error {
	clone := *e
	clone.Details = details
	return &clone
}

// WithMessage 返回替换消息后的副本.
func (e *Error) WithMessage(message string) *Error {
	clone := *e
	clone.Message = message
	return &clone
}

// 预定义错误, 与 docs/api.md 第 2 节的错误码表一致.
var (
	ErrInvalidRequest    = New(http.StatusBadRequest, "invalid_request", "参数缺失或非法")
	ErrCaptchaFailed     = New(http.StatusBadRequest, "captcha_failed", "人机验证未通过")
	ErrUnauthorized      = New(http.StatusUnauthorized, "unauthorized", "未认证或凭据失效")
	ErrForbidden         = New(http.StatusForbidden, "forbidden", "权限不足")
	ErrRegisterDisabled  = New(http.StatusForbidden, "register_disabled", "注册已关闭")
	ErrEmailNotAllowed   = New(http.StatusForbidden, "email_not_allowed", "邮箱域名不在注册白名单中")
	ErrPolicyNotAllowed  = New(http.StatusForbidden, "policy_not_allowed", "存储策略不在用户组的白名单内")
	ErrQuotaExceeded     = New(http.StatusForbidden, "quota_exceeded", "配额不足")
	ErrNotFound          = New(http.StatusNotFound, "not_found", "资源不存在")
	ErrPathConflict      = New(http.StatusConflict, "path_conflict", "目标路径已存在且内容不同")
	ErrFileTooLarge      = New(http.StatusRequestEntityTooLarge, "file_too_large", "超过体积上限")
	ErrUnsupportedFormat = New(http.StatusUnsupportedMediaType, "unsupported_format", "格式不在白名单")
	ErrRateLimited       = New(http.StatusTooManyRequests, "rate_limited", "超过上传速率限制")
	ErrBackend           = New(http.StatusBadGateway, "backend_error", "外部存储错误")
	ErrInternal          = New(http.StatusInternalServerError, "internal_error", "服务内部错误")
)

// From 把任意 error 归一为 API 错误.
func From(err error) *Error {
	if err == nil {
		return nil
	}
	var apiErr *Error
	if errors.As(err, &apiErr) {
		return apiErr
	}
	return ErrInternal.WithMessage(err.Error())
}
