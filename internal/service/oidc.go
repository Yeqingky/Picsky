package service

// OIDC 第三方登录: 授权码流程 (Authorization Code) + PKCE (S256).
// 提供方配置按管理员创建的条目动态加载, 不存在固定环境变量.

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tidwall/gjson"
	rp "github.com/zitadel/oidc/v3/pkg/client/rp"
	"github.com/zitadel/oidc/v3/pkg/oidc"
	"gorm.io/gorm"

	"github.com/yeqingky/Picsky/internal/apperr"
	"github.com/yeqingky/Picsky/internal/model"
)

const (
	oidcPendingTTL        = 10 * time.Minute
	maxPendingOIDCFlows   = 10000
	maxOIDCResponseBytes  = 1 << 20
	maxOIDCUsernameTries  = 200
	defaultOIDCScopes     = "openid email profile"
	oidcUserInfoMaxBytes  = 1 << 20
	oidcAvatarMaxURLLen   = 512
	oidcProviderNameLimit = 64
	oidcAllocationTTL     = 24 * time.Hour
)

// oidcPendingFlow 保存一次授权码流程的服务端状态; PKCE verifier 与 nonce 不下发到浏览器.
type oidcPendingFlow struct {
	providerID   int64
	state        string
	nonce        string
	verifier     string
	redirectPath string
	relyingParty rp.RelyingParty
	bindUserID   *int64
	expiresAt    time.Time
}

// OIDCPublicProvider 是登录页可见的第三方提供方.
type OIDCPublicProvider struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	IconURL string `json:"icon_url"`
}

// OIDCProviderView 是管理端的提供方视图; 客户端密钥只报告是否已设置.
type OIDCProviderView struct {
	ID                int64                    `json:"id"`
	Name              string                   `json:"name"`
	IconURL           string                   `json:"icon_url"`
	DiscoveryURL      string                   `json:"discovery_url"`
	DiscoveryDocument string                   `json:"discovery_document"`
	ClientID          string                   `json:"client_id"`
	Scopes            string                   `json:"scopes"`
	Enabled           bool                     `json:"enabled"`
	AllowUnbound      bool                     `json:"allow_unbound"`
	ClientSecretSet   bool                     `json:"client_secret_set"`
	SubPath           string                   `json:"sub_path"`
	EmailPath         string                   `json:"email_path"`
	NamePath          string                   `json:"name_path"`
	PicturePath       string                   `json:"picture_path"`
	GroupClaim        string                   `json:"group_claim"`
	GroupMappings     []model.OIDCGroupMapping `json:"group_mappings"`
	CreatedAt         time.Time                `json:"created_at"`
	UpdatedAt         time.Time                `json:"updated_at"`
}

// OIDCProviderInput 是创建提供方的入参.
type OIDCProviderInput struct {
	ID                int64                    `json:"id"`
	AllocationToken   string                   `json:"allocation_token"`
	Name              string                   `json:"name"`
	IconURL           string                   `json:"icon_url"`
	DiscoveryURL      string                   `json:"discovery_url"`
	DiscoveryDocument string                   `json:"discovery_document"`
	ClientID          string                   `json:"client_id"`
	ClientSecret      string                   `json:"client_secret"`
	Scopes            string                   `json:"scopes"`
	Enabled           bool                     `json:"enabled"`
	AllowUnbound      bool                     `json:"allow_unbound"`
	SubPath           string                   `json:"sub_path"`
	EmailPath         string                   `json:"email_path"`
	NamePath          string                   `json:"name_path"`
	PicturePath       string                   `json:"picture_path"`
	GroupClaim        string                   `json:"group_claim"`
	GroupMappings     []model.OIDCGroupMapping `json:"group_mappings"`
}

// OIDCProviderPatch 是更新提供方的入参; 指针字段为 nil 表示不修改.
type OIDCProviderPatch struct {
	Name              *string                   `json:"name"`
	IconURL           *string                   `json:"icon_url"`
	DiscoveryURL      *string                   `json:"discovery_url"`
	DiscoveryDocument *string                   `json:"discovery_document"`
	ClientID          *string                   `json:"client_id"`
	ClientSecret      *string                   `json:"client_secret"`
	ClearClientSecret *bool                     `json:"clear_client_secret"`
	Scopes            *string                   `json:"scopes"`
	Enabled           *bool                     `json:"enabled"`
	AllowUnbound      *bool                     `json:"allow_unbound"`
	SubPath           *string                   `json:"sub_path"`
	EmailPath         *string                   `json:"email_path"`
	NamePath          *string                   `json:"name_path"`
	PicturePath       *string                   `json:"picture_path"`
	GroupClaim        *string                   `json:"group_claim"`
	GroupMappings     *[]model.OIDCGroupMapping `json:"group_mappings"`
}

// OIDCStartResult 是发起授权的结果: 浏览器跳转地址与绑定浏览器 cookie 的 state.
type OIDCStartResult struct {
	AuthURL string
	State   string
}

// OIDCCallbackResult 是第三方回调结果; Bound 为 true 表示绑定账号而不是登录.
type OIDCCallbackResult struct {
	User         *model.User
	RedirectPath string
	Bound        bool
}

// OIDCBindingView 是已绑定的第三方身份.
type OIDCBindingView struct {
	ProviderID   int64  `json:"provider_id"`
	ProviderName string `json:"provider_name"`
	IconURL      string `json:"icon_url"`
	Subject      string `json:"subject"`
}

// OIDCProviderAllocationResult 是一次性应用 ID 分配结果; Token 只在分配时返回.
type OIDCProviderAllocationResult struct {
	ProviderID int64     `json:"provider_id"`
	Token      string    `json:"allocation_token"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// PublicOIDCProviders 返回已启用的提供方, 供登录页展示.
func (a *Auth) PublicOIDCProviders() ([]OIDCPublicProvider, error) {
	var providers []model.OIDCProvider
	if err := a.db.Select("id", "name", "icon_url").
		Where("enabled = ?", true).
		Order("id ASC").
		Find(&providers).Error; err != nil {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	out := make([]OIDCPublicProvider, 0, len(providers))
	for _, provider := range providers {
		out = append(out, OIDCPublicProvider{ID: provider.ID, Name: provider.Name, IconURL: provider.IconURL})
	}
	return out, nil
}

// OIDCProviders 返回管理端提供方列表.
func (a *Auth) OIDCProviders() ([]OIDCProviderView, error) {
	var providers []model.OIDCProvider
	if err := a.db.Order("id ASC").Find(&providers).Error; err != nil {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	out := make([]OIDCProviderView, 0, len(providers))
	for idx := range providers {
		view, err := oidcProviderView(&providers[idx])
		if err != nil {
			return nil, err
		}
		out = append(out, view)
	}
	return out, nil
}

// AllocateOIDCProvider 预留一个永不复用的提供方 ID; 未保存的分配不会回收.
func (a *Auth) AllocateOIDCProvider() (*OIDCProviderAllocationResult, error) {
	now := nowUTC()
	if err := a.db.Where("expires_at <= ?", now).Delete(&model.OIDCProviderAllocation{}).Error; err != nil {
		return nil, apperr.ErrInternal.WithMessage("清理过期的第三方登录分配失败: " + err.Error())
	}
	token, err := randomURLToken(32)
	if err != nil {
		return nil, apperr.ErrInternal.WithMessage("生成应用分配令牌失败: " + err.Error())
	}
	tokenHash := oidcAllocationTokenHash(token)
	expiresAt := now.Add(oidcAllocationTTL)
	var providerID int64
	if err := a.db.Transaction(func(tx *gorm.DB) error {
		// 借助 AUTOINCREMENT / 序列插入占位行后立即删除, 让被放弃的 ID 永久跳过.
		placeholder := model.OIDCProvider{
			Name:              "pending",
			DiscoveryURL:      "",
			DiscoveryDocument: "",
			ClientID:          "pending",
			Scopes:            defaultOIDCScopes,
			SubPath:           "sub",
			EmailPath:         "email",
			NamePath:          "name",
			PicturePath:       "picture",
			GroupMappings:     "[]",
			CreatedAt:         now,
			UpdatedAt:         now,
		}
		if err := tx.Create(&placeholder).Error; err != nil {
			return fmt.Errorf("预留第三方登录应用 ID 失败: %w", err)
		}
		providerID = placeholder.ID
		if err := tx.Delete(&model.OIDCProvider{}, providerID).Error; err != nil {
			return fmt.Errorf("释放第三方登录占位记录失败: %w", err)
		}
		allocation := model.OIDCProviderAllocation{
			ProviderID: providerID,
			TokenHash:  tokenHash,
			ExpiresAt:  expiresAt,
			CreatedAt:  now,
		}
		return tx.Create(&allocation).Error
	}); err != nil {
		return nil, apperr.ErrInternal.WithMessage("分配第三方登录应用 ID 失败: " + err.Error())
	}
	return &OIDCProviderAllocationResult{ProviderID: providerID, Token: token, ExpiresAt: expiresAt}, nil
}

// CreateOIDCProvider 使用一次性分配创建提供方; 分配被消费后该 ID 不可再次使用.
func (a *Auth) CreateOIDCProvider(in OIDCProviderInput) (*OIDCProviderView, error) {
	if in.ID <= 0 || strings.TrimSpace(in.AllocationToken) == "" {
		return nil, apperr.New(http.StatusBadRequest, "oidc_allocation_invalid", "缺少应用分配信息, 请重新添加")
	}
	provider := model.OIDCProvider{
		ID:                in.ID,
		Name:              in.Name,
		IconURL:           in.IconURL,
		DiscoveryURL:      in.DiscoveryURL,
		DiscoveryDocument: in.DiscoveryDocument,
		ClientID:          in.ClientID,
		Scopes:            in.Scopes,
		Enabled:           in.Enabled,
		AllowUnbound:      in.AllowUnbound,
		SubPath:           in.SubPath,
		EmailPath:         in.EmailPath,
		NamePath:          in.NamePath,
		PicturePath:       in.PicturePath,
		GroupClaim:        in.GroupClaim,
		GroupMappings:     mustJSON(in.GroupMappings),
	}
	if err := a.validateOIDCProvider(&provider); err != nil {
		return nil, err
	}
	if err := a.encryptOIDCSecret(&provider, in.ClientSecret, true); err != nil {
		return nil, err
	}
	now := nowUTC()
	provider.CreatedAt = now
	provider.UpdatedAt = now
	allocationTokenHash := oidcAllocationTokenHash(in.AllocationToken)
	if err := a.db.Transaction(func(tx *gorm.DB) error {
		var allocation model.OIDCProviderAllocation
		if err := tx.First(&allocation, in.ID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return oidcAllocationInvalidError()
			}
			return apperr.ErrInternal.WithMessage(err.Error())
		}
		if !allocation.ExpiresAt.After(now) ||
			subtle.ConstantTimeCompare([]byte(allocation.TokenHash), []byte(allocationTokenHash)) != 1 {
			return oidcAllocationInvalidError()
		}
		result := tx.Where("provider_id = ? AND token_hash = ?", in.ID, allocationTokenHash).Delete(&model.OIDCProviderAllocation{})
		if result.Error != nil {
			return apperr.ErrInternal.WithMessage(result.Error.Error())
		}
		if result.RowsAffected != 1 {
			return oidcAllocationInvalidError()
		}
		if err := tx.Create(&provider).Error; err != nil {
			return apperr.ErrInvalidRequest.WithMessage("应用 ID 已被占用, 请重新添加")
		}
		return nil
	}); err != nil {
		return nil, err
	}
	view, err := oidcProviderView(&provider)
	if err != nil {
		return nil, err
	}
	return &view, nil
}

func oidcAllocationInvalidError() error {
	return apperr.New(http.StatusBadRequest, "oidc_allocation_invalid", "应用创建窗口无效或已过期, 请重新添加")
}

func oidcAllocationTokenHash(token string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return hex.EncodeToString(sum[:])
}

// UpdateOIDCProvider 局部更新提供方.
func (a *Auth) UpdateOIDCProvider(id int64, patch OIDCProviderPatch) (*OIDCProviderView, error) {
	var provider model.OIDCProvider
	if err := a.db.First(&provider, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperr.ErrNotFound.WithMessage("第三方登录提供方不存在")
		}
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	if patch.Name != nil {
		provider.Name = *patch.Name
	}
	if patch.IconURL != nil {
		provider.IconURL = *patch.IconURL
	}
	if patch.DiscoveryURL != nil {
		provider.DiscoveryURL = *patch.DiscoveryURL
	}
	if patch.DiscoveryDocument != nil {
		provider.DiscoveryDocument = *patch.DiscoveryDocument
	}
	if patch.ClientID != nil {
		provider.ClientID = *patch.ClientID
	}
	if patch.Scopes != nil {
		provider.Scopes = *patch.Scopes
	}
	if patch.Enabled != nil {
		provider.Enabled = *patch.Enabled
	}
	if patch.AllowUnbound != nil {
		provider.AllowUnbound = *patch.AllowUnbound
	}
	if patch.SubPath != nil {
		provider.SubPath = *patch.SubPath
	}
	if patch.EmailPath != nil {
		provider.EmailPath = *patch.EmailPath
	}
	if patch.NamePath != nil {
		provider.NamePath = *patch.NamePath
	}
	if patch.PicturePath != nil {
		provider.PicturePath = *patch.PicturePath
	}
	if patch.GroupClaim != nil {
		provider.GroupClaim = *patch.GroupClaim
	}
	if patch.GroupMappings != nil {
		provider.GroupMappings = mustJSON(*patch.GroupMappings)
	}
	if err := a.validateOIDCProvider(&provider); err != nil {
		return nil, err
	}
	if patch.ClientSecret != nil && strings.TrimSpace(*patch.ClientSecret) != "" {
		if err := a.encryptOIDCSecret(&provider, *patch.ClientSecret, true); err != nil {
			return nil, err
		}
	} else if patch.ClearClientSecret != nil && *patch.ClearClientSecret {
		provider.ClientSecret = ""
	}
	provider.UpdatedAt = nowUTC()
	if err := a.db.Model(&model.OIDCProvider{}).Where("id = ?", provider.ID).Updates(map[string]any{
		"name":               provider.Name,
		"icon_url":           provider.IconURL,
		"discovery_url":      provider.DiscoveryURL,
		"discovery_document": provider.DiscoveryDocument,
		"client_id":          provider.ClientID,
		"client_secret":      provider.ClientSecret,
		"scopes":             provider.Scopes,
		"enabled":            provider.Enabled,
		"allow_unbound":      provider.AllowUnbound,
		"sub_path":           provider.SubPath,
		"email_path":         provider.EmailPath,
		"name_path":          provider.NamePath,
		"picture_path":       provider.PicturePath,
		"group_claim":        provider.GroupClaim,
		"group_mappings":     provider.GroupMappings,
		"updated_at":         provider.UpdatedAt,
	}).Error; err != nil {
		return nil, apperr.ErrInternal.WithMessage("更新第三方登录提供方失败: " + err.Error())
	}
	view, err := oidcProviderView(&provider)
	if err != nil {
		return nil, err
	}
	return &view, nil
}

// DeleteOIDCProvider 删除提供方及其身份绑定; 已注册用户保留.
func (a *Auth) DeleteOIDCProvider(id int64) error {
	var provider model.OIDCProvider
	if err := a.db.First(&provider, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return apperr.ErrNotFound.WithMessage("第三方登录提供方不存在")
		}
		return apperr.ErrInternal.WithMessage(err.Error())
	}
	if err := a.db.Where("provider_id = ?", id).Delete(&model.OIDCIdentity{}).Error; err != nil {
		return apperr.ErrInternal.WithMessage("删除第三方身份绑定失败: " + err.Error())
	}
	if err := a.db.Delete(&model.OIDCProvider{}, id).Error; err != nil {
		return apperr.ErrInternal.WithMessage("删除第三方登录提供方失败: " + err.Error())
	}
	return nil
}

// BeginOIDCLogin 生成登录授权地址并保存一次性 state / nonce / PKCE verifier.
func (a *Auth) BeginOIDCLogin(ctx context.Context, providerID int64, callbackURI, redirectPath string) (*OIDCStartResult, error) {
	return a.beginOIDCFlow(ctx, providerID, callbackURI, redirectPath, nil)
}

// BeginOIDCBind 生成绑定授权地址; 回调时将第三方身份绑定到指定用户.
func (a *Auth) BeginOIDCBind(ctx context.Context, userID, providerID int64, callbackURI, redirectPath string) (*OIDCStartResult, error) {
	if userID <= 0 {
		return nil, apperr.ErrUnauthorized
	}
	return a.beginOIDCFlow(ctx, providerID, callbackURI, redirectPath, &userID)
}

func (a *Auth) beginOIDCFlow(ctx context.Context, providerID int64, callbackURI, redirectPath string, bindUserID *int64) (*OIDCStartResult, error) {
	provider, err := a.enabledOIDCProvider(providerID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(callbackURI) == "" {
		return nil, apperr.ErrInvalidRequest.WithMessage("回调地址为空")
	}
	state, err := randomURLToken(32)
	if err != nil {
		return nil, apperr.ErrInternal.WithMessage("生成登录 state 失败: " + err.Error())
	}
	nonce, err := randomURLToken(32)
	if err != nil {
		return nil, apperr.ErrInternal.WithMessage("生成 nonce 失败: " + err.Error())
	}
	verifier, err := randomURLToken(64)
	if err != nil {
		return nil, apperr.ErrInternal.WithMessage("生成 PKCE verifier 失败: " + err.Error())
	}
	relyingParty, err := a.oidcRelyingParty(ctx, provider, callbackURI, nonce)
	if err != nil {
		return nil, err
	}
	authURL := rp.AuthURL(
		state,
		relyingParty,
		rp.WithCodeChallenge(pkceChallenge(verifier)),
		rp.AuthURLOpt(rp.WithURLParam("nonce", nonce)),
	)

	now := time.Now()
	flow := oidcPendingFlow{
		providerID:   provider.ID,
		state:        state,
		nonce:        nonce,
		verifier:     verifier,
		redirectPath: safeOIDCRedirectPath(redirectPath),
		relyingParty: relyingParty,
		bindUserID:   bindUserID,
		expiresAt:    now.Add(oidcPendingTTL),
	}
	a.oidcMu.Lock()
	defer a.oidcMu.Unlock()
	if a.oidcPending == nil {
		a.oidcPending = make(map[string]oidcPendingFlow)
	}
	for key, pending := range a.oidcPending {
		if !pending.expiresAt.After(now) {
			delete(a.oidcPending, key)
		}
	}
	if len(a.oidcPending) >= maxPendingOIDCFlows {
		return nil, apperr.ErrRateLimited.WithMessage("待完成的第三方登录过多, 请稍后重试")
	}
	a.oidcPending[state] = flow
	return &OIDCStartResult{AuthURL: authURL, State: state}, nil
}

// CompleteOIDCCallback 消费授权码; 按流程状态完成第三方登录或绑定已有账号.
func (a *Auth) CompleteOIDCCallback(ctx context.Context, providerID int64, state, stateCookie, code, ip, userAgent string) (*OIDCCallbackResult, error) {
	state = strings.TrimSpace(state)
	stateCookie = strings.TrimSpace(stateCookie)
	if state == "" || stateCookie == "" || subtle.ConstantTimeCompare([]byte(state), []byte(stateCookie)) != 1 {
		return nil, oidcStateError()
	}
	flow, ok := a.takeOIDCFlow(state)
	if !ok || flow.providerID != providerID {
		return nil, oidcStateError()
	}
	if strings.TrimSpace(code) == "" {
		return nil, apperr.New(http.StatusBadRequest, "oidc_denied", "第三方登录已取消或未返回授权码")
	}
	provider, err := a.enabledOIDCProvider(providerID)
	if err != nil {
		return nil, err
	}

	tokens, err := rp.CodeExchange[*oidc.IDTokenClaims](ctx, strings.TrimSpace(code), flow.relyingParty, rp.WithCodeVerifier(flow.verifier))
	if err != nil {
		a.recordOIDCFailure(providerID, nil, ip, userAgent, "exchange_failed")
		return nil, apperr.ErrBackend.WithMessage("第三方登录令牌交换失败: " + err.Error())
	}
	if tokens.IDTokenClaims == nil || tokens.IDTokenClaims.GetSubject() == "" {
		a.recordOIDCFailure(providerID, nil, ip, userAgent, "missing_subject")
		return nil, apperr.ErrBackend.WithMessage("第三方登录未返回有效的 ID Token")
	}

	claims, err := oidcClaimsJSON(ctx, tokens, flow.relyingParty)
	if err != nil {
		a.recordOIDCFailure(providerID, nil, ip, userAgent, "userinfo_failed")
		return nil, err
	}
	subject := oidcClaimString(claims, provider.SubPath)
	if subject == "" {
		a.recordOIDCFailure(providerID, nil, ip, userAgent, "missing_subject")
		return nil, apperr.ErrBackend.WithMessage("第三方登录未返回用户唯一标识")
	}

	if flow.bindUserID != nil {
		user, err := a.bindOIDCIdentity(provider, subject, *flow.bindUserID, ip, userAgent)
		if err != nil {
			a.recordOIDCBindFailure(provider.ID, *flow.bindUserID, ip, userAgent, oidcFailureReason(err))
			return nil, err
		}
		return &OIDCCallbackResult{User: user, RedirectPath: flow.redirectPath, Bound: true}, nil
	}

	email := normalizeEmail(oidcClaimString(claims, provider.EmailPath))
	if err := requireOIDCEmail(email); err != nil {
		a.recordOIDCFailure(providerID, nil, ip, userAgent, "missing_email")
		return nil, err
	}
	emailVerified := oidcEmailVerified(claims)
	displayName := oidcDisplayName(claims, provider.NamePath, email)
	avatar := oidcAvatarURL(claims, provider.PicturePath)
	groupValues := oidcGroupValues(claims, provider.GroupClaim)

	user, err := a.resolveOIDCUser(provider, subject, email, displayName, avatar, emailVerified, groupValues)
	if err != nil {
		a.recordOIDCFailure(providerID, nil, ip, userAgent, oidcFailureReason(err))
		return nil, err
	}
	if err := a.finishLoginWithDetail(
		user,
		ip,
		userAgent,
		"oidc:ip:"+ip,
		"oidc:"+strconv.FormatInt(provider.ID, 10)+":"+subject,
		map[string]any{"method": "oidc", "provider_id": provider.ID},
	); err != nil {
		return nil, err
	}
	return &OIDCCallbackResult{User: user, RedirectPath: flow.redirectPath}, nil
}

// bindOIDCIdentity 把第三方 subject 绑定到当前登录用户; 已绑定其他用户时拒绝.
func (a *Auth) bindOIDCIdentity(provider *model.OIDCProvider, subject string, userID int64, ip, userAgent string) (*model.User, error) {
	var user model.User
	if err := a.db.First(&user, userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperr.ErrUnauthorized
		}
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	if user.Status == model.UserStatusBanned {
		return nil, apperr.New(http.StatusForbidden, "oidc_banned", "账号已封禁")
	}
	var bound model.OIDCIdentity
	if err := a.db.Where("user_id = ? AND provider_id = ?", userID, provider.ID).First(&bound).Error; err == nil {
		if bound.Subject != subject {
			return nil, apperr.New(http.StatusConflict, "oidc_provider_already_bound", "该登录方式已绑定其他第三方账号")
		}
		return &user, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	var identity model.OIDCIdentity
	err := a.db.Where("provider_id = ? AND subject = ?", provider.ID, subject).First(&identity).Error
	if err == nil {
		if identity.UserID != userID {
			return nil, apperr.New(http.StatusConflict, "oidc_identity_conflict", "该第三方账号已绑定其他用户")
		}
		return &user, nil
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	now := nowUTC()
	identity = model.OIDCIdentity{ProviderID: provider.ID, Subject: subject, UserID: userID, CreatedAt: now, UpdatedAt: now}
	if err := a.db.Create(&identity).Error; err != nil {
		var raced model.OIDCIdentity
		if lookup := a.db.Where("provider_id = ? AND subject = ?", provider.ID, subject).First(&raced).Error; lookup == nil {
			if raced.UserID != userID {
				return nil, apperr.New(http.StatusConflict, "oidc_identity_conflict", "该第三方账号已绑定其他用户")
			}
			return &user, nil
		}
		return nil, apperr.ErrInternal.WithMessage("绑定第三方身份失败: " + err.Error())
	}
	a.audit.Recordf(Entry{
		UserID:     &userID,
		Action:     "oidc_bind",
		TargetType: "oidc_provider",
		TargetID:   &provider.ID,
		Detail:     map[string]any{"provider_id": provider.ID},
		IP:         ip,
		UserAgent:  userAgent,
	})
	return &user, nil
}

// OIDCBindings 返回当前用户已绑定的第三方身份.
func (a *Auth) OIDCBindings(userID int64) ([]OIDCBindingView, error) {
	var bindings []OIDCBindingView
	if err := a.db.Table("oidc_identities").
		Select("oidc_identities.provider_id, oidc_providers.name AS provider_name, oidc_providers.icon_url, oidc_identities.subject").
		Joins("JOIN oidc_providers ON oidc_providers.id = oidc_identities.provider_id").
		Where("oidc_identities.user_id = ?", userID).
		Order("oidc_identities.provider_id ASC").
		Scan(&bindings).Error; err != nil {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	return bindings, nil
}

// UnbindOIDCIdentity 解除当前用户的某个第三方绑定; 没有本地密码时至少保留一个登录方式.
func (a *Auth) UnbindOIDCIdentity(userID, providerID int64, ip, userAgent string) error {
	var user model.User
	if err := a.db.First(&user, userID).Error; err != nil {
		return apperr.ErrUnauthorized
	}
	if user.Status == model.UserStatusBanned {
		return apperr.New(http.StatusForbidden, "oidc_banned", "账号已封禁")
	}
	var count int64
	if err := a.db.Model(&model.OIDCIdentity{}).Where("user_id = ?", userID).Count(&count).Error; err != nil {
		return apperr.ErrInternal.WithMessage(err.Error())
	}
	if user.PasswordHash == "" && count <= 1 {
		return apperr.New(http.StatusConflict, "oidc_last_login_method", "至少保留一个登录方式")
	}
	result := a.db.Where("user_id = ? AND provider_id = ?", userID, providerID).Delete(&model.OIDCIdentity{})
	if result.Error != nil {
		return apperr.ErrInternal.WithMessage(result.Error.Error())
	}
	if result.RowsAffected == 0 {
		return apperr.ErrNotFound.WithMessage("未绑定该第三方账号")
	}
	a.audit.Recordf(Entry{UserID: &userID, Action: "oidc_unbind", TargetType: "oidc_provider", TargetID: &providerID, IP: ip, UserAgent: userAgent})
	return nil
}

func (a *Auth) recordOIDCBindFailure(providerID, userID int64, ip, userAgent, reason string) {
	a.audit.Recordf(Entry{
		UserID:    &userID,
		Action:    "oidc_bind_failed",
		Detail:    map[string]any{"provider_id": providerID, "reason": reason},
		IP:        ip,
		UserAgent: userAgent,
	})
}

// takeOIDCFlow 取出并删除待完成的流程, 保证 state 只能使用一次.
func (a *Auth) takeOIDCFlow(state string) (oidcPendingFlow, bool) {
	now := time.Now()
	a.oidcMu.Lock()
	defer a.oidcMu.Unlock()
	flow, ok := a.oidcPending[state]
	if !ok {
		return oidcPendingFlow{}, false
	}
	delete(a.oidcPending, state)
	if !flow.expiresAt.After(now) {
		return oidcPendingFlow{}, false
	}
	return flow, true
}

func (a *Auth) enabledOIDCProvider(id int64) (*model.OIDCProvider, error) {
	var provider model.OIDCProvider
	if err := a.db.First(&provider, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperr.New(http.StatusNotFound, "oidc_not_found", "第三方登录提供方不存在")
		}
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	if !provider.Enabled {
		return nil, apperr.New(http.StatusForbidden, "oidc_disabled", "第三方登录提供方未启用")
	}
	return &provider, nil
}

func oidcStateError() error {
	return apperr.New(http.StatusBadRequest, "oidc_state_invalid", "第三方登录状态已过期或无效, 请重新发起登录")
}

func oidcFailureReason(err error) string {
	var apiErr *apperr.Error
	if errors.As(err, &apiErr) {
		return apiErr.Code
	}
	return "failed"
}

func (a *Auth) recordOIDCFailure(providerID int64, userID *int64, ip, userAgent, reason string) {
	a.audit.Recordf(Entry{
		UserID:    userID,
		Action:    "login_failed",
		Detail:    map[string]any{"method": "oidc", "provider_id": providerID, "reason": reason},
		IP:        ip,
		UserAgent: userAgent,
	})
}

// oidcRelyingParty 拉取发现文档并构造 zitadel/oidc Relying Party.
func (a *Auth) oidcRelyingParty(ctx context.Context, provider *model.OIDCProvider, callbackURI, nonce string) (rp.RelyingParty, error) {
	var issuer, endpoint string
	httpClient := a.oidcHTTPClient
	if strings.TrimSpace(provider.DiscoveryDocument) != "" {
		document, err := parseOIDCDiscoveryDocument(provider.DiscoveryDocument)
		if err != nil {
			return nil, err
		}
		if err := validateOIDCDiscoveryForLogin(document); err != nil {
			return nil, err
		}
		issuer = document.Issuer
		// 用本地拦截的 synthetic discovery URL, 让保存的 JSON 成为权威配置, 不再联网覆盖.
		endpoint = "http://oidc-discovery.invalid/" + strconv.FormatInt(provider.ID, 10) + "/.well-known/openid-configuration"
		httpClient = a.withOIDCDiscoveryDocument(endpoint, []byte(provider.DiscoveryDocument))
	} else {
		discovery, liveEndpoint, err := a.oidcDiscovery(ctx, provider.DiscoveryURL)
		if err != nil {
			return nil, err
		}
		if err := validateOIDCDiscoveryForLogin(discovery); err != nil {
			return nil, err
		}
		issuer = discovery.Issuer
		endpoint = liveEndpoint
	}
	secret := ""
	var err error
	if strings.TrimSpace(provider.ClientSecret) != "" {
		secret, err = a.cipher.Decrypt(provider.ClientSecret)
		if err != nil {
			return nil, apperr.ErrInternal.WithMessage("第三方客户端密钥解密失败")
		}
	}
	scopes := strings.Fields(provider.Scopes)
	relyingParty, err := rp.NewRelyingPartyOIDC(
		ctx,
		issuer,
		provider.ClientID,
		secret,
		callbackURI,
		scopes,
		rp.WithCustomDiscoveryUrl(endpoint),
		rp.WithHTTPClient(httpClient),
		rp.WithSigningAlgsFromDiscovery(),
		rp.WithVerifierOpts(rp.WithNonce(func(context.Context) string { return nonce })),
	)
	if err != nil {
		return nil, apperr.ErrBackend.WithMessage("初始化第三方登录失败: " + err.Error())
	}
	return relyingParty, nil
}

func validateOIDCDiscoveryForLogin(discovery *oidc.DiscoveryConfiguration) error {
	if len(discovery.ResponseTypesSupported) > 0 && !slices.Contains(discovery.ResponseTypesSupported, "code") {
		return apperr.ErrBackend.WithMessage("提供方不支持授权码模式 (code)")
	}
	if len(discovery.CodeChallengeMethodsSupported) > 0 {
		supported := false
		for _, method := range discovery.CodeChallengeMethodsSupported {
			if strings.EqualFold(string(method), "S256") {
				supported = true
				break
			}
		}
		if !supported {
			return apperr.ErrBackend.WithMessage("提供方不支持 PKCE S256")
		}
	}
	return nil
}

func (a *Auth) withOIDCDiscoveryDocument(target string, document []byte) *http.Client {
	client := *a.oidcHTTPClient
	base := a.oidcHTTPClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	client.Transport = &oidcDiscoveryTransport{base: base, target: target, document: document}
	return &client
}

type oidcDiscoveryTransport struct {
	base     http.RoundTripper
	target   string
	document []byte
}

func (t *oidcDiscoveryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.String() != t.target {
		return t.base.RoundTrip(req)
	}
	return &http.Response{
		Status:        "200 OK",
		StatusCode:    http.StatusOK,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        http.Header{"Content-Type": []string{"application/json"}},
		Body:          io.NopCloser(bytes.NewReader(t.document)),
		ContentLength: int64(len(t.document)),
		Request:       req,
	}, nil
}

// ImportOIDCDiscovery 读取 URL 对应的发现文档并返回格式化 JSON 与来源地址.
func (a *Auth) ImportOIDCDiscovery(ctx context.Context, rawURL string) (string, string, error) {
	discovery, endpoint, err := a.oidcDiscovery(ctx, rawURL)
	if err != nil {
		return "", "", err
	}
	pretty, err := json.MarshalIndent(discovery, "", "  ")
	if err != nil {
		return "", "", apperr.ErrInternal.WithMessage("格式化发现文档失败: " + err.Error())
	}
	return string(pretty), endpoint, nil
}

// parseOIDCDiscoveryDocument 校验手工编辑的发现文档 JSON.
func parseOIDCDiscoveryDocument(raw string) (*oidc.DiscoveryConfiguration, error) {
	if len(raw) > maxOIDCResponseBytes {
		return nil, apperr.ErrInvalidRequest.WithMessage("发现文档过大")
	}
	var discovery oidc.DiscoveryConfiguration
	if err := json.Unmarshal([]byte(raw), &discovery); err != nil {
		return nil, apperr.ErrInvalidRequest.WithMessage("发现文档不是合法 JSON")
	}
	if discovery.Issuer == "" || discovery.AuthorizationEndpoint == "" ||
		discovery.TokenEndpoint == "" || discovery.JwksURI == "" {
		return nil, apperr.ErrInvalidRequest.WithMessage("发现文档缺少 issuer, authorization_endpoint, token_endpoint 或 jwks_uri")
	}
	return &discovery, nil
}

// oidcDiscovery 读取 well-known 文档; 输入可以是 issuer 基地址或完整发现地址.
func (a *Auth) oidcDiscovery(ctx context.Context, raw string) (*oidc.DiscoveryConfiguration, string, error) {
	endpoint, err := oidcDiscoveryURL(raw)
	if err != nil {
		return nil, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, "", apperr.ErrBackend.WithMessage("发现文档地址无效: " + err.Error())
	}
	req.Header.Set("Accept", "application/json")
	response, err := a.oidcHTTPClient.Do(req)
	if err != nil {
		return nil, "", apperr.ErrBackend.WithMessage("读取第三方发现文档失败: " + err.Error())
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, "", apperr.ErrBackend.WithMessage(fmt.Sprintf("第三方发现文档返回 HTTP %d", response.StatusCode))
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxOIDCResponseBytes))
	if err != nil {
		return nil, "", apperr.ErrBackend.WithMessage("读取第三方发现文档失败: " + err.Error())
	}
	var discovery oidc.DiscoveryConfiguration
	if err := json.Unmarshal(body, &discovery); err != nil {
		return nil, "", apperr.ErrBackend.WithMessage("第三方发现文档解析失败: " + err.Error())
	}
	if discovery.Issuer == "" || discovery.AuthorizationEndpoint == "" || discovery.TokenEndpoint == "" || discovery.JwksURI == "" {
		return nil, "", apperr.ErrBackend.WithMessage("第三方发现文档缺少 issuer, authorization_endpoint, token_endpoint 或 jwks_uri")
	}
	return &discovery, endpoint, nil
}

// oidcDiscoveryURL 规范化发现地址: 完整 .well-known 地址原样使用, issuer 基地址追加标准后缀.
func oidcDiscoveryURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", apperr.ErrInvalidRequest.WithMessage("发现文档地址不能为空")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", apperr.ErrInvalidRequest.WithMessage("发现文档地址必须是 http 或 https URL")
	}
	if parsed.User != nil {
		return "", apperr.ErrInvalidRequest.WithMessage("发现文档地址不能包含用户信息")
	}
	if strings.Contains(parsed.Path, "/.well-known/") {
		return trimmed, nil
	}
	return strings.TrimRight(trimmed, "/") + "/.well-known/openid-configuration", nil
}

// oidcClaimsJSON 合并已验证的 ID Token 声明与 UserInfo; 自定义映射按合并后的 JSON 读取.
func oidcClaimsJSON(ctx context.Context, tokens *oidc.Tokens[*oidc.IDTokenClaims], relyingParty rp.RelyingParty) ([]byte, error) {
	merged := map[string]any{}
	if tokens.IDTokenClaims != nil {
		raw, err := json.Marshal(tokens.IDTokenClaims)
		if err != nil {
			return nil, apperr.ErrInternal.WithMessage("序列化第三方声明失败: " + err.Error())
		}
		if err := json.Unmarshal(raw, &merged); err != nil {
			return nil, apperr.ErrInternal.WithMessage("解析第三方声明失败: " + err.Error())
		}
	}
	if relyingParty.UserinfoEndpoint() != "" && tokens.AccessToken != "" {
		info, err := rp.Userinfo[*oidc.UserInfo](ctx, tokens.AccessToken, tokens.Type(), tokens.IDTokenClaims.GetSubject(), relyingParty)
		if err != nil {
			return nil, apperr.ErrBackend.WithMessage("读取第三方用户信息失败: " + err.Error())
		}
		raw, err := json.Marshal(info)
		if err != nil {
			return nil, apperr.ErrInternal.WithMessage("序列化第三方用户信息失败: " + err.Error())
		}
		var infoMap map[string]any
		if err := json.Unmarshal(raw, &infoMap); err != nil {
			return nil, apperr.ErrInternal.WithMessage("解析第三方用户信息失败: " + err.Error())
		}
		for key, value := range infoMap {
			if value == nil || value == "" {
				continue
			}
			merged[key] = value
		}
	}
	raw, err := json.Marshal(merged)
	if err != nil {
		return nil, apperr.ErrInternal.WithMessage("序列化第三方声明失败: " + err.Error())
	}
	if len(raw) > oidcUserInfoMaxBytes {
		return nil, apperr.ErrBackend.WithMessage("第三方用户信息过大")
	}
	return raw, nil
}

func randomURLToken(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// safeOIDCRedirectPath 只接受站内绝对路径, 阻止开放重定向.
func safeOIDCRedirectPath(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || !strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, "//") ||
		strings.ContainsAny(trimmed, "\r\n") {
		return "/"
	}
	return trimmed
}

// oidcProviderView 转换管理视图.
func oidcProviderView(provider *model.OIDCProvider) (OIDCProviderView, error) {
	mappings := []model.OIDCGroupMapping{}
	if strings.TrimSpace(provider.GroupMappings) != "" {
		if err := json.Unmarshal([]byte(provider.GroupMappings), &mappings); err != nil {
			return OIDCProviderView{}, apperr.ErrInternal.WithMessage("第三方用户组映射解析失败: " + err.Error())
		}
	}
	if mappings == nil {
		mappings = []model.OIDCGroupMapping{}
	}
	return OIDCProviderView{
		ID:                provider.ID,
		Name:              provider.Name,
		IconURL:           provider.IconURL,
		DiscoveryURL:      provider.DiscoveryURL,
		DiscoveryDocument: provider.DiscoveryDocument,
		ClientID:          provider.ClientID,
		Scopes:            provider.Scopes,
		Enabled:           provider.Enabled,
		AllowUnbound:      provider.AllowUnbound,
		ClientSecretSet:   strings.TrimSpace(provider.ClientSecret) != "",
		SubPath:           provider.SubPath,
		EmailPath:         provider.EmailPath,
		NamePath:          provider.NamePath,
		PicturePath:       provider.PicturePath,
		GroupClaim:        provider.GroupClaim,
		GroupMappings:     mappings,
		CreatedAt:         provider.CreatedAt,
		UpdatedAt:         provider.UpdatedAt,
	}, nil
}

func (a *Auth) encryptOIDCSecret(provider *model.OIDCProvider, raw string, setEmpty bool) error {
	secret := strings.TrimSpace(raw)
	if secret == "" {
		if setEmpty {
			provider.ClientSecret = ""
		}
		return nil
	}
	if len(secret) > 4096 {
		return apperr.ErrInvalidRequest.WithMessage("客户端密钥过长")
	}
	encrypted, err := a.cipher.Encrypt(secret)
	if err != nil {
		return apperr.ErrInternal.WithMessage("加密客户端密钥失败: " + err.Error())
	}
	provider.ClientSecret = encrypted
	return nil
}

func (a *Auth) validateOIDCProvider(provider *model.OIDCProvider) error {
	provider.Name = strings.TrimSpace(provider.Name)
	provider.IconURL = strings.TrimSpace(provider.IconURL)
	provider.DiscoveryURL = strings.TrimSpace(provider.DiscoveryURL)
	provider.DiscoveryDocument = strings.TrimSpace(provider.DiscoveryDocument)
	provider.ClientID = strings.TrimSpace(provider.ClientID)
	provider.SubPath = defaultString(strings.TrimSpace(provider.SubPath), "sub")
	provider.EmailPath = defaultString(strings.TrimSpace(provider.EmailPath), "email")
	provider.NamePath = defaultString(strings.TrimSpace(provider.NamePath), "name")
	provider.PicturePath = defaultString(strings.TrimSpace(provider.PicturePath), "picture")
	provider.GroupClaim = strings.TrimSpace(provider.GroupClaim)

	if provider.Name == "" {
		return apperr.ErrInvalidRequest.WithMessage("登录方式名称不能为空")
	}
	if utf8.RuneCountInString(provider.Name) > oidcProviderNameLimit {
		return apperr.ErrInvalidRequest.WithMessage("登录方式名称过长")
	}
	if provider.IconURL != "" {
		if len(provider.IconURL) > oidcAvatarMaxURLLen {
			return apperr.ErrInvalidRequest.WithMessage("图标 URL 过长")
		}
		parsed, err := url.Parse(provider.IconURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return apperr.ErrInvalidRequest.WithMessage("图标 URL 必须是 http 或 https URL")
		}
	}
	var document *oidc.DiscoveryConfiguration
	if provider.DiscoveryDocument != "" {
		var err error
		document, err = parseOIDCDiscoveryDocument(provider.DiscoveryDocument)
		if err != nil {
			return err
		}
		if provider.DiscoveryURL == "" {
			derived, err := oidcDiscoveryURL(document.Issuer)
			if err != nil {
				return apperr.ErrInvalidRequest.WithMessage("发现文档 issuer 无效")
			}
			provider.DiscoveryURL = derived
		}
	}
	if provider.DiscoveryURL == "" {
		return apperr.ErrInvalidRequest.WithMessage("发现文档不能为空")
	}
	if _, err := oidcDiscoveryURL(provider.DiscoveryURL); err != nil {
		return err
	}
	if provider.ClientID == "" {
		return apperr.ErrInvalidRequest.WithMessage("客户端 ID 不能为空")
	}
	if len(provider.ClientID) > 255 {
		return apperr.ErrInvalidRequest.WithMessage("客户端 ID 过长")
	}
	scopes, err := normalizeOIDCScopes(provider.Scopes)
	if err != nil {
		return err
	}
	provider.Scopes = scopes
	for label, path := range map[string]string{
		"sub":   provider.SubPath,
		"邮箱":    provider.EmailPath,
		"展示名":   provider.NamePath,
		"头像":    provider.PicturePath,
		"用户组声明": provider.GroupClaim,
	} {
		if path == "" {
			continue
		}
		if len(path) > 128 {
			return apperr.ErrInvalidRequest.WithMessage(label + "字段映射过长")
		}
	}
	mappings, err := normalizeOIDCGroupMappings(provider.GroupMappings)
	if err != nil {
		return err
	}
	if len(mappings) > 100 {
		return apperr.ErrInvalidRequest.WithMessage("用户组映射最多 100 条")
	}
	if len(mappings) > 0 && provider.GroupClaim == "" {
		return apperr.ErrInvalidRequest.WithMessage("配置用户组映射时必须填写用户组字段")
	}
	for _, mapping := range mappings {
		var group model.Group
		if err := a.db.Select("id").First(&group, mapping.GroupID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return apperr.ErrInvalidRequest.WithMessage(fmt.Sprintf("用户组映射引用了不存在的用户组: %d", mapping.GroupID))
			}
			return apperr.ErrInternal.WithMessage(err.Error())
		}
	}
	provider.GroupMappings = mustJSON(mappings)
	return nil
}

func normalizeOIDCScopes(raw string) (string, error) {
	tokens := strings.FieldsFunc(raw, func(r rune) bool { return r == ' ' || r == ',' || r == '\t' || r == '\n' })
	if len(tokens) == 0 {
		tokens = strings.Fields(defaultOIDCScopes)
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(tokens))
	for _, token := range tokens {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		if _, duplicate := seen[token]; duplicate {
			continue
		}
		seen[token] = struct{}{}
		out = append(out, token)
	}
	if _, ok := seen["openid"]; !ok {
		return "", apperr.ErrInvalidRequest.WithMessage("Scope 必须包含 openid")
	}
	if len(out) > 20 {
		return "", apperr.ErrInvalidRequest.WithMessage("Scope 数量过多")
	}
	joined := strings.Join(out, " ")
	if len(joined) > 255 {
		return "", apperr.ErrInvalidRequest.WithMessage("Scope 过长")
	}
	return joined, nil
}

func normalizeOIDCGroupMappings(raw string) ([]model.OIDCGroupMapping, error) {
	mappings := []model.OIDCGroupMapping{}
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &mappings); err != nil {
			return nil, apperr.ErrInvalidRequest.WithMessage("用户组映射格式无效")
		}
	}
	normalized := make([]model.OIDCGroupMapping, 0, len(mappings))
	seen := map[string]struct{}{}
	for _, mapping := range mappings {
		mapping.Value = strings.TrimSpace(mapping.Value)
		if mapping.Value == "" || mapping.GroupID <= 0 {
			return nil, apperr.ErrInvalidRequest.WithMessage("用户组映射的声明值与用户组不能为空")
		}
		if len(mapping.Value) > 128 {
			return nil, apperr.ErrInvalidRequest.WithMessage("用户组声明值过长")
		}
		if _, duplicate := seen[mapping.Value]; duplicate {
			continue
		}
		seen[mapping.Value] = struct{}{}
		normalized = append(normalized, mapping)
	}
	return normalized, nil
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func mustJSON(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return "[]"
	}
	return string(raw)
}

// oidcClaimString 读取 GJSON 路径的字符串值.
func oidcClaimString(raw []byte, path string) string {
	if path == "" {
		return ""
	}
	return strings.TrimSpace(gjson.GetBytes(raw, path).String())
}

func oidcClaimBool(raw []byte, path string) bool {
	result := gjson.GetBytes(raw, path)
	if !result.Exists() {
		return false
	}
	if result.Type == gjson.True {
		return true
	}
	if result.Type == gjson.String {
		parsed, _ := strconv.ParseBool(strings.TrimSpace(result.String()))
		return parsed
	}
	return false
}

func oidcGroupValues(raw []byte, path string) []string {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	result := gjson.GetBytes(raw, path)
	if !result.Exists() {
		return nil
	}
	if result.IsArray() {
		values := make([]string, 0, 8)
		for _, item := range result.Array() {
			value := strings.TrimSpace(item.String())
			if value != "" {
				values = append(values, value)
			}
		}
		return values
	}
	value := strings.TrimSpace(result.String())
	if value == "" {
		return nil
	}
	return []string{value}
}

// resolveMappedGroup 按映射顺序选择首个命中且存在的用户组.
func (a *Auth) resolveMappedGroup(provider *model.OIDCProvider, values []string) (*model.Group, error) {
	if len(values) == 0 || strings.TrimSpace(provider.GroupMappings) == "" {
		return nil, nil
	}
	var mappings []model.OIDCGroupMapping
	if err := json.Unmarshal([]byte(provider.GroupMappings), &mappings); err != nil {
		return nil, apperr.ErrInternal.WithMessage("第三方用户组映射解析失败: " + err.Error())
	}
	for _, mapping := range mappings {
		for _, value := range values {
			if mapping.Value != value {
				continue
			}
			var group model.Group
			if err := a.db.First(&group, mapping.GroupID).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					continue
				}
				return nil, apperr.ErrInternal.WithMessage(err.Error())
			}
			return &group, nil
		}
	}
	return nil, nil
}

// resolveOIDCUser 依次尝试身份绑定, 已验证邮箱的本地账号, 以及允许时的自动创建.
func (a *Auth) resolveOIDCUser(provider *model.OIDCProvider, subject, email, displayName, avatar string, emailVerified bool, groupValues []string) (*model.User, error) {
	var identity model.OIDCIdentity
	err := a.db.Where("provider_id = ? AND subject = ?", provider.ID, subject).First(&identity).Error
	if err == nil {
		var user model.User
		if err := a.db.First(&user, identity.UserID).Error; err != nil {
			return nil, apperr.ErrInternal.WithMessage("第三方身份绑定的用户不存在")
		}
		if user.Status == model.UserStatusBanned {
			return nil, apperr.New(http.StatusForbidden, "oidc_banned", "账号已封禁")
		}
		verified, err := a.verifyOIDCEmail(&user, emailVerified)
		if err != nil {
			return nil, err
		}
		a.touchOIDCProfile(verified, avatar)
		return verified, nil
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}

	var existing model.User
	err = a.db.Where("email = ?", email).First(&existing).Error
	if err == nil {
		if existing.Status == model.UserStatusBanned {
			return nil, apperr.New(http.StatusForbidden, "oidc_banned", "账号已封禁")
		}
		if !emailVerified {
			return nil, apperr.New(http.StatusForbidden, "oidc_email_unverified",
				"第三方账号邮箱未验证, 无法绑定已有账号")
		}
		var bound model.OIDCIdentity
		if err := a.db.Where("user_id = ? AND provider_id = ?", existing.ID, provider.ID).First(&bound).Error; err == nil {
			return nil, apperr.New(http.StatusConflict, "oidc_provider_already_bound", "该登录方式已绑定其他第三方账号")
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperr.ErrInternal.WithMessage(err.Error())
		}
		user, err := a.linkOIDCIdentity(provider, subject, &existing, emailVerified)
		if err != nil {
			// 并发绑定可能由另一请求完成: 重新读取身份后沿用已绑定的用户.
			var raced model.OIDCIdentity
			if lookup := a.db.Where("provider_id = ? AND subject = ?", provider.ID, subject).First(&raced).Error; lookup == nil {
				var linked model.User
				if load := a.db.First(&linked, raced.UserID).Error; load == nil {
					if linked.Status == model.UserStatusBanned {
						return nil, apperr.New(http.StatusForbidden, "oidc_banned", "账号已封禁")
					}
					a.touchOIDCProfile(&linked, avatar)
					return &linked, nil
				}
			}
			return nil, err
		}
		a.touchOIDCProfile(user, avatar)
		return user, nil
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	if !provider.AllowUnbound {
		return nil, apperr.New(http.StatusForbidden, "oidc_unbound",
			"该第三方账号未绑定本站用户")
	}

	// 自动创建与公开注册共用互斥锁, 避免并发创建同名用户或重复绑定.
	a.registerMu.Lock()
	defer a.registerMu.Unlock()

	// 拿到锁后重新检查, 覆盖并发回调与并发注册.
	if err := a.db.Where("provider_id = ? AND subject = ?", provider.ID, subject).First(&identity).Error; err == nil {
		var user model.User
		if err := a.db.First(&user, identity.UserID).Error; err != nil {
			return nil, apperr.ErrInternal.WithMessage("第三方身份绑定的用户不存在")
		}
		if user.Status == model.UserStatusBanned {
			return nil, apperr.New(http.StatusForbidden, "oidc_banned", "账号已封禁")
		}
		return &user, nil
	}
	if err := a.db.Where("email = ?", email).First(&existing).Error; err == nil {
		return nil, apperr.New(http.StatusConflict, "oidc_email_conflict",
			"该邮箱已注册, 请使用现有方式登录")
	}

	group, err := a.resolveMappedGroup(provider, groupValues)
	if err != nil {
		return nil, err
	}
	if group == nil {
		group, err = a.defaultGroup()
		if err != nil {
			return nil, err
		}
	}
	return a.createOIDCUser(provider, subject, email, displayName, avatar, emailVerified, group)
}

// verifyOIDCEmail 在已绑定用户仍处于未验证状态且提供方邮箱已验证时恢复其账号.
func (a *Auth) verifyOIDCEmail(user *model.User, emailVerified bool) (*model.User, error) {
	if !emailVerified || user.EmailVerifiedAt != nil {
		return user, nil
	}
	now := nowUTC()
	updates := map[string]any{
		"email_verified_at": now,
		"updated_at":        now,
	}
	if user.Status == model.UserStatusUnverified {
		groupID := user.GroupID
		if user.RestoreGroupID != nil {
			groupID = *user.RestoreGroupID
		} else {
			group, err := a.defaultGroup()
			if err != nil {
				return nil, err
			}
			groupID = group.ID
		}
		updates["status"] = model.UserStatusNormal
		updates["group_id"] = groupID
		updates["restore_group_id"] = nil
	}
	if err := a.db.Model(&model.User{}).Where("id = ?", user.ID).Updates(updates).Error; err != nil {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	var fresh model.User
	if err := a.db.First(&fresh, user.ID).Error; err != nil {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	return &fresh, nil
}

// oidcUsername 由邮箱本地部分或展示名生成唯一 ASCII 用户名.
func (a *Auth) oidcUsername(email, displayName string) (string, error) {
	local := email
	if at := strings.IndexByte(local, '@'); at > 0 {
		local = local[:at]
	}
	base := sanitizeOIDCUsername(local)
	if base == "" {
		base = sanitizeOIDCUsername(displayName)
	}
	if base == "" {
		base = "oidcuser"
	}
	for attempt := 0; attempt < maxOIDCUsernameTries; attempt++ {
		candidate := base
		if attempt > 0 {
			suffix := strconv.Itoa(attempt)
			limit := maxUsernameRuneLength - len(suffix)
			if len(candidate) > limit {
				candidate = candidate[:limit]
			}
			candidate += suffix
		}
		var count int64
		if err := a.db.Model(&model.User{}).Where("username = ?", candidate).Count(&count).Error; err != nil {
			return "", apperr.ErrInternal.WithMessage(err.Error())
		}
		if count == 0 {
			return candidate, nil
		}
	}
	return "", apperr.ErrInternal.WithMessage("无法生成唯一的第三方登录用户名")
}

func sanitizeOIDCUsername(raw string) string {
	var builder strings.Builder
	for _, char := range raw {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') {
			builder.WriteRune(char)
			if builder.Len() >= maxUsernameRuneLength {
				break
			}
		}
	}
	return builder.String()
}

// createOIDCUser 在首次登录且允许自动创建时建立系统用户与身份绑定.
func (a *Auth) createOIDCUser(provider *model.OIDCProvider, subject, email, displayName, avatar string, emailVerified bool, group *model.Group) (*model.User, error) {
	username, err := a.oidcUsername(email, displayName)
	if err != nil {
		return nil, err
	}
	now := nowUTC()
	status := model.UserStatusNormal
	var verifiedAt *time.Time
	var restoreGroupID *int64
	if emailVerified {
		verifiedAt = &now
	} else if a.settings.Bool("email_verification_enabled", false) {
		readOnly, err := a.readOnlyGroup()
		if err != nil {
			return nil, err
		}
		restoreGroupID = &group.ID
		group = readOnly
		status = model.UserStatusUnverified
	}
	nickname := displayName
	if utf8.RuneCountInString(nickname) > 64 {
		nickname = string([]rune(nickname)[:64])
	}
	user := model.User{
		Username:        username,
		Email:           email,
		PasswordHash:    "",
		EmailVerifiedAt: verifiedAt,
		Nickname:        nickname,
		AvatarURL:       avatar,
		GroupID:         group.ID,
		RestoreGroupID:  restoreGroupID,
		Status:          status,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	result := a.db.Create(&user)
	if result.Error != nil {
		return nil, apperr.ErrInternal.WithMessage("创建第三方登录用户失败: " + result.Error.Error())
	}
	identity := model.OIDCIdentity{
		ProviderID: provider.ID,
		Subject:    subject,
		UserID:     user.ID,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := a.db.Create(&identity).Error; err != nil {
		_ = a.db.Delete(&user).Error
		return nil, apperr.ErrInternal.WithMessage("绑定第三方身份失败: " + err.Error())
	}
	if !emailVerified && status == model.UserStatusUnverified {
		if err := a.QueueVerification(&user); err != nil {
			a.log.Warn("排队第三方登录用户邮箱验证邮件失败", "user_id", user.ID, "error", err)
		}
	}
	a.audit.Recordf(Entry{
		UserID:     &user.ID,
		Action:     "register",
		TargetType: "user",
		TargetID:   &user.ID,
		Detail:     map[string]any{"method": "oidc", "provider_id": provider.ID},
	})
	return &user, nil
}

// linkOIDCIdentity 把已验证邮箱的现有账号与第三方身份绑定.
func (a *Auth) linkOIDCIdentity(provider *model.OIDCProvider, subject string, user *model.User, emailVerified bool) (*model.User, error) {
	now := nowUTC()
	err := a.db.Transaction(func(tx *gorm.DB) error {
		identity := model.OIDCIdentity{
			ProviderID: provider.ID,
			Subject:    subject,
			UserID:     user.ID,
			CreatedAt:  now,
			UpdatedAt:  now,
		}
		if err := tx.Create(&identity).Error; err != nil {
			return err
		}
		if emailVerified && user.EmailVerifiedAt == nil {
			updates := map[string]any{
				"email_verified_at": now,
				"updated_at":        now,
			}
			if user.Status == model.UserStatusUnverified {
				groupID := user.GroupID
				if user.RestoreGroupID != nil {
					groupID = *user.RestoreGroupID
				} else {
					group, err := a.defaultGroup()
					if err != nil {
						return err
					}
					groupID = group.ID
				}
				updates["status"] = model.UserStatusNormal
				updates["group_id"] = groupID
				updates["restore_group_id"] = nil
			}
			if err := tx.Model(&model.User{}).Where("id = ?", user.ID).Updates(updates).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, apperr.ErrInternal.WithMessage("绑定第三方身份失败: " + err.Error())
	}
	var fresh model.User
	if err := a.db.First(&fresh, user.ID).Error; err != nil {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	return &fresh, nil
}

// touchOIDCProfile 同步第三方头像; 昵称保留用户本地修改.
func (a *Auth) touchOIDCProfile(user *model.User, avatar string) {
	if avatar == "" || avatar == user.AvatarURL {
		return
	}
	if err := a.db.Model(&model.User{}).Where("id = ?", user.ID).Updates(map[string]any{
		"avatar_url": avatar,
		"updated_at": nowUTC(),
	}).Error; err == nil {
		user.AvatarURL = avatar
	}
}

// oidcDisplayName 取展示名, 为空时回退邮箱本地部分.
func oidcDisplayName(raw []byte, path, email string) string {
	name := oidcClaimString(raw, path)
	if name != "" {
		return name
	}
	if at := strings.IndexByte(email, '@'); at > 0 {
		return email[:at]
	}
	return email
}

func oidcAvatarURL(raw []byte, path string) string {
	avatar := oidcClaimString(raw, path)
	if avatar == "" || len(avatar) > oidcAvatarMaxURLLen {
		return ""
	}
	parsed, err := url.Parse(avatar)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return ""
	}
	return avatar
}

func oidcEmailVerified(raw []byte) bool {
	return oidcClaimBool(raw, "email_verified")
}

func requireOIDCEmail(email string) error {
	if err := validateEmail(email); err != nil {
		return apperr.ErrBackend.WithMessage("第三方登录未返回有效邮箱")
	}
	return nil
}
