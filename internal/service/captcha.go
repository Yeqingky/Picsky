package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/yeqingky/Picsky/internal/apperr"
)

// 人机验证提供方.
const (
	CaptchaNone      = "none"
	CaptchaCap       = "cap"
	CaptchaTurnstile = "turnstile"
)

// turnstileVerifyURL 是 Cloudflare Turnstile 的校验地址.
const turnstileVerifyURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

// Captcha 负责人机验证校验; 本项目不实现本地验证码.
type Captcha struct {
	settings *Settings
	client   *http.Client
}

// CaptchaPublic 是下发给前端的公开配置.
type CaptchaPublic struct {
	Provider    string `json:"provider"`
	SiteKey     string `json:"site_key"`
	APIEndpoint string `json:"api_endpoint,omitempty"`
}

// NewCaptcha 创建人机验证服务.
func NewCaptcha(settings *Settings) *Captcha {
	return &Captcha{
		settings: settings,
		client:   &http.Client{Timeout: 10 * time.Second},
	}
}

// Provider 返回当前提供方.
func (c *Captcha) Provider() string {
	provider := strings.TrimSpace(strings.ToLower(c.settings.String("captcha_provider")))
	switch provider {
	case CaptchaCap, CaptchaTurnstile:
		return provider
	default:
		return CaptchaNone
	}
}

// Enabled 表示是否启用人机验证.
func (c *Captcha) Enabled() bool { return c.Provider() != CaptchaNone }

// Public 返回前端渲染所需的公开信息.
func (c *Captcha) Public() CaptchaPublic {
	provider := c.Provider()
	public := CaptchaPublic{Provider: provider, SiteKey: c.settings.String("captcha_site_key")}
	if provider == CaptchaCap {
		endpoint := strings.TrimRight(strings.TrimSpace(c.settings.String("captcha_endpoint")), "/")
		if endpoint != "" && public.SiteKey != "" {
			public.APIEndpoint = endpoint + "/" + public.SiteKey + "/"
		}
	}
	return public
}

// Verify 校验客户端票据; 未启用时直接放行.
func (c *Captcha) Verify(ctx context.Context, token, remoteIP string) error {
	provider := c.Provider()
	if provider == CaptchaNone {
		return nil
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return apperr.ErrCaptchaFailed.WithMessage("缺少人机验证票据")
	}
	secret, err := c.settings.Secret("captcha_secret")
	if err != nil {
		return apperr.ErrInternal.WithMessage("人机验证秘钥解密失败: " + err.Error())
	}
	if secret == "" {
		return apperr.ErrInternal.WithMessage("人机验证已启用但未配置秘钥")
	}

	switch provider {
	case CaptchaCap:
		return c.verifyCap(ctx, secret, token)
	case CaptchaTurnstile:
		return c.verifyTurnstile(ctx, secret, token, remoteIP)
	default:
		return apperr.ErrInternal.WithMessage("未知的人机验证提供方: " + provider)
	}
}

// verifyCap 调用自建 Cap 实例的 /siteverify.
func (c *Captcha) verifyCap(ctx context.Context, secret, token string) error {
	endpoint := strings.TrimRight(strings.TrimSpace(c.settings.String("captcha_endpoint")), "/")
	siteKey := strings.TrimSpace(c.settings.String("captcha_site_key"))
	if endpoint == "" || siteKey == "" {
		return apperr.ErrInternal.WithMessage("Cap 需要配置服务地址与站点 Key")
	}
	body, err := json.Marshal(map[string]string{"secret": secret, "response": token})
	if err != nil {
		return apperr.ErrInternal.WithMessage(err.Error())
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/"+url.PathEscape(siteKey)+"/siteverify", strings.NewReader(string(body)))
	if err != nil {
		return apperr.ErrInternal.WithMessage(err.Error())
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return apperr.ErrCaptchaFailed.WithMessage("人机验证服务不可达: " + err.Error())
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return apperr.ErrCaptchaFailed.WithMessage(fmt.Sprintf("人机验证服务返回 %s", resp.Status))
	}
	var parsed struct {
		Success bool `json:"success"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return apperr.ErrCaptchaFailed.WithMessage("人机验证响应无法解析: " + err.Error())
	}
	if !parsed.Success {
		return apperr.ErrCaptchaFailed
	}
	return nil
}

// verifyTurnstile 调用 Cloudflare Turnstile 校验接口.
func (c *Captcha) verifyTurnstile(ctx context.Context, secret, token, remoteIP string) error {
	form := url.Values{}
	form.Set("secret", secret)
	form.Set("response", token)
	if remoteIP != "" {
		form.Set("remoteip", remoteIP)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, turnstileVerifyURL, strings.NewReader(form.Encode()))
	if err != nil {
		return apperr.ErrInternal.WithMessage(err.Error())
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.client.Do(req)
	if err != nil {
		return apperr.ErrCaptchaFailed.WithMessage("人机验证服务不可达: " + err.Error())
	}
	defer resp.Body.Close()
	var parsed struct {
		Success    bool     `json:"success"`
		ErrorCodes []string `json:"error-codes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return apperr.ErrCaptchaFailed.WithMessage("人机验证响应无法解析: " + err.Error())
	}
	if !parsed.Success {
		message := "人机验证未通过"
		if len(parsed.ErrorCodes) > 0 {
			message += ": " + strings.Join(parsed.ErrorCodes, ",")
		}
		return apperr.ErrCaptchaFailed.WithMessage(message)
	}
	return nil
}
