// Package middleware 提供认证, 授权与错误恢复等 HTTP 中间件.
package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/yeqingky/Picsky/internal/apperr"
	"github.com/yeqingky/Picsky/internal/model"
	"github.com/yeqingky/Picsky/internal/service"
)

// SessionCookieName 是会话 cookie 名称.
const SessionCookieName = "picsky_session"

const principalKey = "picsky.principal"

// Auth 负责从请求中解析主体.
type Auth struct {
	svc *service.Services
}

// NewAuth 创建认证中间件.
func NewAuth(svc *service.Services) *Auth {
	return &Auth{svc: svc}
}

// Resolve 解析 Bearer 令牌或会话 cookie; 未携带凭据时返回 nil, nil.
func (a *Auth) Resolve(c *gin.Context) (*service.Principal, error) {
	if header := strings.TrimSpace(c.GetHeader("Authorization")); header != "" {
		if token, ok := bearerToken(header); ok {
			key, err := a.svc.Keys.Authenticate(token)
			if err != nil {
				return nil, err
			}
			return a.svc.Auth.PrincipalFor(key.UserID, &key.ID, key.Scopes)
		}
		return nil, apperr.ErrUnauthorized.WithMessage("Authorization 头格式非法, 需要 Bearer <access_key>.<secret>")
	}

	if cookie, err := c.Cookie(SessionCookieName); err == nil && strings.TrimSpace(cookie) != "" {
		userID, err := a.svc.Auth.ParseSession(cookie)
		if err != nil {
			return nil, apperr.ErrUnauthorized
		}
		return a.svc.Auth.PrincipalFor(userID, nil, nil)
	}
	return nil, nil
}

// Required 要求认证, 并把主体写入上下文.
func (a *Auth) Required() gin.HandlerFunc {
	return func(c *gin.Context) {
		principal, err := a.Resolve(c)
		if err != nil {
			c.AbortWithStatusJSON(apperr.From(err).Status, apperr.From(err))
			return
		}
		if principal == nil {
			c.AbortWithStatusJSON(apperr.ErrUnauthorized.Status, apperr.ErrUnauthorized)
			return
		}
		SetPrincipal(c, principal)
		c.Next()
	}
}

// Require 要求指定权限位.
func Require(bit model.Permission) gin.HandlerFunc {
	return func(c *gin.Context) {
		principal := Principal(c)
		if principal == nil {
			c.AbortWithStatusJSON(apperr.ErrUnauthorized.Status, apperr.ErrUnauthorized)
			return
		}
		if !principal.Has(bit) {
			c.AbortWithStatusJSON(apperr.ErrForbidden.Status, apperr.ErrForbidden.
				WithMessage("需要权限: "+model.PermissionNameOf(bit)))
			return
		}
		c.Next()
	}
}

// SetPrincipal 把主体写入上下文.
func SetPrincipal(c *gin.Context, principal *service.Principal) {
	c.Set(principalKey, principal)
}

// Principal 读取上下文中的主体, 未认证时为 nil.
func Principal(c *gin.Context) *service.Principal {
	value, exists := c.Get(principalKey)
	if !exists {
		return nil
	}
	principal, ok := value.(*service.Principal)
	if !ok {
		return nil
	}
	return principal
}

// bearerToken 提取 Bearer 令牌.
func bearerToken(header string) (string, bool) {
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", false
	}
	token := strings.TrimSpace(parts[1])
	if token == "" {
		return "", false
	}
	return token, true
}
