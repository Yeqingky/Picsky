package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/yeqingky/Picsky/internal/apperr"
	"github.com/yeqingky/Picsky/internal/middleware"
	"github.com/yeqingky/Picsky/internal/service"
)

// groupRefDTO 是用户组引用.
type groupRefDTO struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// quotaDTO 是配额用量.
type quotaDTO struct {
	Mode      *string `json:"mode"`
	Used      int64   `json:"used"`
	Limit     *int64  `json:"limit"`
	UsedBytes int64   `json:"used_bytes"`
}

// meDTO 是当前用户信息.
type meDTO struct {
	ID              int64       `json:"id"`
	Username        string      `json:"username"`
	Nickname        string      `json:"nickname"`
	Email           string      `json:"email"`
	EmailVerified   bool        `json:"email_verified"`
	Group           groupRefDTO `json:"group"`
	Permissions     []string    `json:"permissions"`
	Quota           quotaDTO    `json:"quota"`
	DefaultPolicyID *int64      `json:"default_policy_id"`
	TOTPEnabled     bool        `json:"totp_enabled"`
	CreatedAt       time.Time   `json:"created_at"`
}

// meResponse 组装当前用户视图.
func (s *Server) meResponse(principal *service.Principal) meDTO {
	user := principal.User
	quota := service.EffectiveQuota(principal.Group, user)
	dto := meDTO{
		ID:              user.ID,
		Username:        user.Username,
		Nickname:        user.Nickname,
		Email:           user.Email,
		EmailVerified:   user.EmailVerifiedAt != nil,
		Group:           groupRefDTO{ID: principal.Group.ID, Name: principal.Group.Name},
		Permissions:     principal.Permissions.Names(),
		Quota:           quotaDTO{Used: quota.Used, UsedBytes: user.UsedBytes},
		DefaultPolicyID: user.DefaultPolicyID,
		TOTPEnabled:     user.TOTPSecret != "",
		CreatedAt:       user.CreatedAt,
	}
	if quota.Mode != "" {
		mode := quota.Mode
		dto.Quota.Mode = &mode
	}
	if quota.Limit != nil {
		limit := *quota.Limit
		dto.Quota.Limit = &limit
	}
	return dto
}

// login 登录并下发会话 cookie.
func (s *Server) login(c *gin.Context) {
	var payload struct {
		Identifier     string `json:"identifier"`
		Username       string `json:"username"`
		Password       string `json:"password"`
		TOTP           string `json:"totp"`
		CaptchaToken   string `json:"captcha_token"`
		LoginChallenge string `json:"login_challenge"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		s.fail(c, apperr.ErrInvalidRequest.WithMessage("请求体解析失败: "+err.Error()))
		return
	}
	identifier := strings.TrimSpace(payload.Identifier)
	if identifier == "" {
		identifier = strings.TrimSpace(payload.Username)
	}

	user, err := s.svc.Auth.Login(c.Request.Context(), service.LoginInput{
		Identifier:     identifier,
		Password:       payload.Password,
		TOTPCode:       payload.TOTP,
		CaptchaToken:   payload.CaptchaToken,
		LoginChallenge: payload.LoginChallenge,
	}, clientIP(c), userAgent(c))
	if err != nil {
		s.fail(c, err)
		return
	}

	token, err := s.svc.Auth.IssueSession(user.ID)
	if err != nil {
		s.fail(c, err)
		return
	}
	s.setSessionCookie(c, token, int(service.SessionTTL.Seconds()))

	principal, err := s.svc.Auth.PrincipalFor(user.ID, nil, nil)
	if err != nil {
		s.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, s.meResponse(principal))
}

// register 注册; 首个账号自动成为管理员.
func (s *Server) register(c *gin.Context) {
	var payload struct {
		Username     string `json:"username"`
		Email        string `json:"email"`
		Password     string `json:"password"`
		CaptchaToken string `json:"captcha_token"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		s.fail(c, apperr.ErrInvalidRequest.WithMessage("请求体解析失败: "+err.Error()))
		return
	}
	user, err := s.svc.Auth.Register(c.Request.Context(), service.RegisterInput{
		Username:     payload.Username,
		Email:        payload.Email,
		Password:     payload.Password,
		CaptchaToken: payload.CaptchaToken,
	}, clientIP(c), userAgent(c))
	if err != nil {
		s.fail(c, err)
		return
	}

	principal, err := s.svc.Auth.PrincipalFor(user.ID, nil, nil)
	if err != nil {
		s.fail(c, err)
		return
	}
	if user.EmailVerifiedAt != nil {
		token, err := s.svc.Auth.IssueSession(user.ID)
		if err != nil {
			s.fail(c, err)
			return
		}
		s.setSessionCookie(c, token, int(service.SessionTTL.Seconds()))
	}
	c.JSON(http.StatusCreated, s.meResponse(principal))
}

// logout 注销当前会话.
func (s *Server) logout(c *gin.Context) {
	principal := middleware.Principal(c)
	if principal != nil {
		s.svc.Auth.Logout(principal.User.ID, clientIP(c), userAgent(c))
	}
	s.setSessionCookie(c, "", -1)
	c.Status(http.StatusNoContent)
}

// me 返回当前用户信息.
func (s *Server) me(c *gin.Context) {
	principal := middleware.Principal(c)
	if principal == nil {
		s.fail(c, apperr.ErrUnauthorized)
		return
	}
	c.JSON(http.StatusOK, s.meResponse(principal))
}

// changePassword 修改密码.
func (s *Server) changePassword(c *gin.Context) {
	principal := middleware.Principal(c)
	var payload struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		s.fail(c, apperr.ErrInvalidRequest.WithMessage("请求体解析失败: "+err.Error()))
		return
	}
	if err := s.svc.Auth.ChangePassword(principal.User.ID, payload.OldPassword, payload.NewPassword, clientIP(c), userAgent(c)); err != nil {
		s.fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// updateProfile 更新昵称.
func (s *Server) updateProfile(c *gin.Context) {
	principal := middleware.Principal(c)
	var payload struct {
		Nickname *string `json:"nickname"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		s.fail(c, apperr.ErrInvalidRequest.WithMessage("请求体解析失败: "+err.Error()))
		return
	}
	if payload.Nickname == nil {
		s.fail(c, apperr.ErrInvalidRequest.WithMessage("缺少 nickname 字段"))
		return
	}
	user, err := s.svc.Auth.UpdateProfile(principal.User.ID, *payload.Nickname)
	if err != nil {
		s.fail(c, err)
		return
	}
	principal.User = user
	c.JSON(http.StatusOK, s.meResponse(principal))
}

// totpSetup 生成 TOTP 秘钥 (尚未启用).
func (s *Server) totpSetup(c *gin.Context) {
	principal := middleware.Principal(c)
	secret, url, err := s.svc.Auth.TOTPSetup(principal.User.ID)
	if err != nil {
		s.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"secret": secret, "otpauth_url": url})
}

// totpEnable 启用两步验证.
func (s *Server) totpEnable(c *gin.Context) {
	principal := middleware.Principal(c)
	var payload struct {
		Secret string `json:"secret"`
		Code   string `json:"code"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		s.fail(c, apperr.ErrInvalidRequest.WithMessage("请求体解析失败: "+err.Error()))
		return
	}
	if err := s.svc.Auth.TOTPEnable(principal.User.ID, payload.Secret, payload.Code, clientIP(c), userAgent(c)); err != nil {
		s.fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// totpDisable 关闭两步验证.
func (s *Server) totpDisable(c *gin.Context) {
	principal := middleware.Principal(c)
	var payload struct {
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		s.fail(c, apperr.ErrInvalidRequest.WithMessage("请求体解析失败: "+err.Error()))
		return
	}
	if err := s.svc.Auth.TOTPDisable(principal.User.ID, payload.Password, clientIP(c), userAgent(c)); err != nil {
		s.fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// captchaPublic 返回人机验证公开配置.
func (s *Server) captchaPublic(c *gin.Context) {
	c.JSON(http.StatusOK, s.svc.Captcha.Public())
}

// verifyEmail 校验邮箱验证令牌.
func (s *Server) verifyEmail(c *gin.Context) {
	var payload struct {
		Token string `json:"token"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		s.fail(c, apperr.ErrInvalidRequest.WithMessage("请求体解析失败: "+err.Error()))
		return
	}
	if err := s.svc.Auth.VerifyEmail(payload.Token); err != nil {
		s.fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// requestPasswordReset 请求发送密码找回邮件.
func (s *Server) requestPasswordReset(c *gin.Context) {
	var payload struct {
		Email        string `json:"email"`
		CaptchaToken string `json:"captcha_token"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		s.fail(c, apperr.ErrInvalidRequest.WithMessage("请求体解析失败: "+err.Error()))
		return
	}
	if err := s.svc.Auth.RequestPasswordReset(c.Request.Context(), payload.Email, payload.CaptchaToken, clientIP(c)); err != nil {
		s.fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// resetPassword 消费邮件令牌并重置密码.
func (s *Server) resetPassword(c *gin.Context) {
	var payload struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		s.fail(c, apperr.ErrInvalidRequest.WithMessage("请求体解析失败: "+err.Error()))
		return
	}
	if err := s.svc.Auth.ResetPassword(payload.Token, payload.Password, clientIP(c), userAgent(c)); err != nil {
		s.fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// resendVerification 重新发送邮箱验证邮件.
func (s *Server) resendVerification(c *gin.Context) {
	principal := middleware.Principal(c)
	if !s.svc.Mailer.Configured() {
		s.fail(c, apperr.ErrInvalidRequest.WithMessage("SMTP 未配置, 无法发送验证邮件"))
		return
	}
	if err := s.svc.Auth.SendVerification(principal.User); err != nil {
		s.fail(c, apperr.ErrBackend.WithMessage("发送验证邮件失败: "+err.Error()))
		return
	}
	c.Status(http.StatusNoContent)
}

// setSessionCookie 写入或清除会话 cookie.
func (s *Server) setSessionCookie(c *gin.Context, token string, maxAge int) {
	secure := c.Request.TLS != nil || strings.HasPrefix(strings.ToLower(s.svc.Settings.String("site_url")), "https://")
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(middleware.SessionCookieName, token, maxAge, "/", "", secure, true)
}
