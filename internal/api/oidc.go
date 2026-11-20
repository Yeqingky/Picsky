package api

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/yeqingky/Picsky/internal/apperr"
	"github.com/yeqingky/Picsky/internal/middleware"
	"github.com/yeqingky/Picsky/internal/service"
)

const oidcStateCookieName = "picsky_oidc_state"

const oidcModeCookieName = "picsky_oidc_mode"

const oidcStateCookieTTL = 10 * time.Minute

type adminOIDCProviderDTO struct {
	service.OIDCProviderView
	RedirectURI string `json:"redirect_uri"`
}

// oidcCallbackURI 返回提供方注册的回调地址; 依赖站点对外地址, 不使用请求 Host.
func (s *Server) oidcCallbackURI(providerID int64) string {
	siteURL := strings.TrimRight(strings.TrimSpace(s.svc.Settings.String("site_url")), "/")
	if siteURL == "" {
		return ""
	}
	return siteURL + s.cfg.APIPrefix + "/auth/oidc/" + strconv.FormatInt(providerID, 10) + "/callback"
}

// oidcStart 发起第三方登录: 302 跳转到提供方授权页.
func (s *Server) oidcStart(c *gin.Context) {
	id, err := idParam(c, "id")
	if err != nil {
		s.redirectOIDCError(c, err)
		return
	}
	callbackURI := s.oidcCallbackURI(id)
	if callbackURI == "" {
		s.redirectOIDCError(c, apperr.New(http.StatusBadRequest, "oidc_site_url", "站点地址未配置, 无法发起第三方登录"))
		return
	}
	result, err := s.svc.Auth.BeginOIDCLogin(c.Request.Context(), id, callbackURI, c.Query("redirect"))
	if err != nil {
		s.logOIDCError("发起第三方登录失败", id, err)
		s.redirectOIDCError(c, err)
		return
	}
	s.setOIDCStateCookie(c, result.State, int(oidcStateCookieTTL.Seconds()))
	s.setOIDCModeCookie(c, "", -1)
	c.Header("Cache-Control", "no-store")
	c.Redirect(http.StatusFound, result.AuthURL)
}

// oidcBindStart 发起绑定授权; 仅允许会话登录用户.
func (s *Server) oidcBindStart(c *gin.Context) {
	principal := s.sessionPrincipal(c)
	if principal == nil {
		return
	}
	id, err := idParam(c, "id")
	if err != nil {
		s.redirectOIDCBindError(c, err)
		return
	}
	callbackURI := s.oidcCallbackURI(id)
	if callbackURI == "" {
		s.redirectOIDCBindError(c, apperr.New(http.StatusBadRequest, "oidc_site_url", "站点地址未配置, 无法发起第三方登录"))
		return
	}
	result, err := s.svc.Auth.BeginOIDCBind(c.Request.Context(), principal.User.ID, id, callbackURI, "/profile")
	if err != nil {
		s.logOIDCError("发起第三方账号绑定失败", id, err)
		s.redirectOIDCBindError(c, err)
		return
	}
	s.setOIDCStateCookie(c, result.State, int(oidcStateCookieTTL.Seconds()))
	s.setOIDCModeCookie(c, "bind", int(oidcStateCookieTTL.Seconds()))
	c.Header("Cache-Control", "no-store")
	c.Redirect(http.StatusFound, result.AuthURL)
}

// sessionPrincipal 限制第三方账号管理仅使用会话登录, 不接受 API 密钥.
func (s *Server) sessionPrincipal(c *gin.Context) *service.Principal {
	principal := middleware.Principal(c)
	if principal == nil || principal.APIKeyID != nil {
		err := apperr.ErrForbidden.WithMessage("第三方账号管理仅支持会话登录")
		c.AbortWithStatusJSON(err.Status, err)
		return nil
	}
	return principal
}

// oidcCallback 处理提供方回调: 校验 state cookie, 消费授权码并签发会话.
func (s *Server) oidcCallback(c *gin.Context) {
	id, err := idParam(c, "id")
	if err != nil {
		s.redirectOIDCError(c, err)
		return
	}
	stateCookie, _ := c.Cookie(oidcStateCookieName)
	modeCookie, _ := c.Cookie(oidcModeCookieName)
	s.setOIDCStateCookie(c, "", -1)
	s.setOIDCModeCookie(c, "", -1)
	c.Header("Cache-Control", "no-store")
	isBind := modeCookie == "bind"
	if providerError := strings.TrimSpace(c.Query("error")); providerError != "" {
		description := c.Query("error_description")
		if len(description) > 255 {
			description = description[:255]
		}
		s.log.Warn("第三方授权被提供方拒绝", "provider_id", id, "bind", isBind, "error", providerError, "description", description)
		denied := apperr.New(http.StatusBadRequest, "oidc_denied", "第三方登录已取消或授权失败")
		if isBind {
			s.redirectOIDCBindError(c, denied)
		} else {
			s.redirectOIDCError(c, denied)
		}
		return
	}
	result, err := s.svc.Auth.CompleteOIDCCallback(
		c.Request.Context(),
		id,
		c.Query("state"),
		stateCookie,
		c.Query("code"),
		clientIP(c),
		userAgent(c),
	)
	if err != nil {
		s.logOIDCError("完成第三方授权失败", id, err)
		if isBind {
			s.redirectOIDCBindError(c, err)
		} else {
			s.redirectOIDCError(c, err)
		}
		return
	}
	if result.Bound {
		c.Redirect(http.StatusFound, "/profile?oidc_bind=success")
		return
	}
	token, err := s.svc.Auth.IssueSession(result.User.ID)
	if err != nil {
		s.logOIDCError("签发第三方登录会话失败", id, err)
		s.redirectOIDCError(c, err)
		return
	}
	s.setSessionCookie(c, token, int(service.SessionTTL.Seconds()))
	redirectPath := result.RedirectPath
	if redirectPath == "" {
		redirectPath = "/"
	}
	c.Redirect(http.StatusFound, redirectPath)
}

// listOIDCBindings 返回当前用户已绑定的第三方身份.
func (s *Server) listOIDCBindings(c *gin.Context) {
	principal := s.sessionPrincipal(c)
	if principal == nil {
		return
	}
	bindings, err := s.svc.Auth.OIDCBindings(principal.User.ID)
	if err != nil {
		s.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, bindings)
}

// unbindOIDCIdentity 解除当前用户的第三方绑定.
func (s *Server) unbindOIDCIdentity(c *gin.Context) {
	principal := s.sessionPrincipal(c)
	if principal == nil {
		return
	}
	providerID, err := idParam(c, "provider_id")
	if err != nil {
		s.fail(c, err)
		return
	}
	if err := s.svc.Auth.UnbindOIDCIdentity(principal.User.ID, providerID, clientIP(c), userAgent(c)); err != nil {
		s.fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) setOIDCStateCookie(c *gin.Context, value string, maxAge int) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(oidcStateCookieName, value, maxAge, "/", "", s.secureCookie(c), true)
}

func (s *Server) setOIDCModeCookie(c *gin.Context, value string, maxAge int) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(oidcModeCookieName, value, maxAge, "/", "", s.secureCookie(c), true)
}

// redirectOIDCError 只把白名单内的第三方登录错误码带回登录页; 其他错误统一显示通用失败.
func (s *Server) redirectOIDCBindError(c *gin.Context, err error) {
	code := "oidc_failed"
	var apiErr *apperr.Error
	if errors.As(err, &apiErr) && apiErr.Status < http.StatusInternalServerError &&
		strings.HasPrefix(apiErr.Code, "oidc_") {
		code = apiErr.Code
	}
	c.Redirect(http.StatusFound, "/profile?oidc_bind_error="+url.QueryEscape(code))
}

func (s *Server) redirectOIDCError(c *gin.Context, err error) {
	code := "oidc_failed"
	var apiErr *apperr.Error
	if errors.As(err, &apiErr) && apiErr.Status < http.StatusInternalServerError &&
		strings.HasPrefix(apiErr.Code, "oidc_") {
		code = apiErr.Code
	}
	c.Redirect(http.StatusFound, "/login?oidc_error="+url.QueryEscape(code))
}

func (s *Server) logOIDCError(message string, providerID int64, err error) {
	var apiErr *apperr.Error
	if errors.As(err, &apiErr) && apiErr.Status < http.StatusInternalServerError {
		s.log.Warn(message, "provider_id", providerID, "error", err)
		return
	}
	s.log.Error(message, "provider_id", providerID, "error", err)
}

// adminAllocateOIDCProvider 为新建应用预留一个永不复用的 ID 与回调地址.
func (s *Server) adminAllocateOIDCProvider(c *gin.Context) {
	principal := middleware.Principal(c)
	if strings.TrimSpace(s.svc.Settings.String("site_url")) == "" {
		s.fail(c, apperr.New(http.StatusBadRequest, "oidc_site_url", "站点地址未配置, 无法生成回调地址"))
		return
	}
	allocation, err := s.svc.Auth.AllocateOIDCProvider()
	if err != nil {
		s.fail(c, err)
		return
	}
	redirectURI := s.oidcCallbackURI(allocation.ProviderID)
	s.svc.Audit.Recordf(serviceEntry(principal, "oidc_provider_allocate", "oidc_provider", allocation.ProviderID,
		nil, clientIP(c), userAgent(c)))
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{
		"provider_id":      allocation.ProviderID,
		"allocation_token": allocation.Token,
		"redirect_uri":     redirectURI,
		"expires_at":       allocation.ExpiresAt,
	})
}

// adminImportOIDCDiscovery 从 URL 读取发现文档, 供管理端手工编辑.
func (s *Server) adminImportOIDCDiscovery(c *gin.Context) {
	var payload struct {
		URL string `json:"url"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		s.fail(c, apperr.ErrInvalidRequest.WithMessage("请求体解析失败: "+err.Error()))
		return
	}
	document, sourceURL, err := s.svc.Auth.ImportOIDCDiscovery(c.Request.Context(), payload.URL)
	if err != nil {
		s.fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{
		"discovery_document": document,
		"discovery_url":      sourceURL,
	})
}

// adminListOIDCProviders 返回全部第三方登录提供方.
func (s *Server) adminListOIDCProviders(c *gin.Context) {
	views, err := s.svc.Auth.OIDCProviders()
	if err != nil {
		s.fail(c, err)
		return
	}
	out := make([]adminOIDCProviderDTO, 0, len(views))
	for _, view := range views {
		out = append(out, adminOIDCProviderDTO{
			OIDCProviderView: view,
			RedirectURI:      s.oidcCallbackURI(view.ID),
		})
	}
	c.JSON(http.StatusOK, out)
}

// adminCreateOIDCProvider 创建第三方登录提供方.
func (s *Server) adminCreateOIDCProvider(c *gin.Context) {
	principal := middleware.Principal(c)
	var payload service.OIDCProviderInput
	if err := c.ShouldBindJSON(&payload); err != nil {
		s.fail(c, apperr.ErrInvalidRequest.WithMessage("请求体解析失败: "+err.Error()))
		return
	}
	view, err := s.svc.Auth.CreateOIDCProvider(payload)
	if err != nil {
		s.fail(c, err)
		return
	}
	s.svc.Audit.Recordf(serviceEntry(principal, "oidc_provider_create", "oidc_provider", view.ID,
		map[string]any{"name": view.Name}, clientIP(c), userAgent(c)))
	c.JSON(http.StatusCreated, adminOIDCProviderDTO{OIDCProviderView: *view, RedirectURI: s.oidcCallbackURI(view.ID)})
}

// adminUpdateOIDCProvider 局部更新第三方登录提供方.
func (s *Server) adminUpdateOIDCProvider(c *gin.Context) {
	principal := middleware.Principal(c)
	id, err := idParam(c, "id")
	if err != nil {
		s.fail(c, err)
		return
	}
	var payload service.OIDCProviderPatch
	if err := c.ShouldBindJSON(&payload); err != nil {
		s.fail(c, apperr.ErrInvalidRequest.WithMessage("请求体解析失败: "+err.Error()))
		return
	}
	view, err := s.svc.Auth.UpdateOIDCProvider(id, payload)
	if err != nil {
		s.fail(c, err)
		return
	}
	s.svc.Audit.Recordf(serviceEntry(principal, "oidc_provider_update", "oidc_provider", view.ID,
		map[string]any{"name": view.Name}, clientIP(c), userAgent(c)))
	c.JSON(http.StatusOK, adminOIDCProviderDTO{OIDCProviderView: *view, RedirectURI: s.oidcCallbackURI(view.ID)})
}

// adminDeleteOIDCProvider 删除第三方登录提供方及其身份绑定.
func (s *Server) adminDeleteOIDCProvider(c *gin.Context) {
	principal := middleware.Principal(c)
	id, err := idParam(c, "id")
	if err != nil {
		s.fail(c, err)
		return
	}
	if err := s.svc.Auth.DeleteOIDCProvider(id); err != nil {
		s.fail(c, err)
		return
	}
	s.svc.Audit.Recordf(serviceEntry(principal, "oidc_provider_delete", "oidc_provider", id, nil, clientIP(c), userAgent(c)))
	c.Status(http.StatusNoContent)
}
