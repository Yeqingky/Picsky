package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"github.com/yeqingky/Picsky/internal/apperr"
	"github.com/yeqingky/Picsky/internal/crypto"
	"github.com/yeqingky/Picsky/internal/model"
)

const (
	// SessionTTL 是会话 cookie 有效期.
	SessionTTL = 7 * 24 * time.Hour
	// EmailVerifyTTL 是邮箱验证链接有效期.
	EmailVerifyTTL = 24 * time.Hour
	// PasswordResetTTL 是密码找回链接有效期.
	PasswordResetTTL = 30 * time.Minute
	// bcryptCost 是密码散列强度.
	bcryptCost = 12
	// LoginFailureLimit 与 LoginFailureWindow 定义登录失败限流: 窗口内失败次数达到上限后拒绝登录.
	LoginFailureLimit          = 20
	LoginFailureWindow         = 10 * time.Minute
	passwordResetRequestLimit  = 5
	passwordResetRequestWindow = time.Hour
	loginChallengeTTL          = 5 * time.Minute
	maxPendingLoginChallenges  = 10000
	// 令牌用途.
	purposeSession        = "session"
	purposeEmailVerify    = "email_verify"
	purposePasswordReset  = "password_reset"
	minPasswordRuneLength = 8
	maxPasswordRuneLength = 128
	maxUsernameRuneLength = 64
)

// Principal 是认证后的主体.
type Principal struct {
	User        *model.User  `json:"-"`
	Group       *model.Group `json:"-"`
	Permissions model.Permission
	APIKeyID    *int64
}

// Has 判断主体是否具备权限位.
func (p *Principal) Has(bit model.Permission) bool {
	if p == nil {
		return false
	}
	return p.Permissions.Has(bit)
}

// IsAdmin 判断主体是否具备任一管理权限.
func (p *Principal) IsAdmin() bool {
	adminBits := model.PermManagePolicies | model.PermManageUsers | model.PermManageGroups | model.PermManageSettings | model.PermViewLogs | model.PermViewAll | model.PermDeleteAll
	return p.Has(adminBits)
}

// Auth 负责注册, 登录, 会话, 密码, TOTP, 邮箱验证与密码找回.
type Auth struct {
	db                   *gorm.DB
	settings             *Settings
	cipher               *crypto.Cipher
	audit                *Audit
	captcha              *Captcha
	mailer               *Mailer
	secretKey            []byte
	log                  *slog.Logger
	loginLimiter         *RateLimiter
	passwordResetLimiter *RateLimiter
	loginChallengesMu    sync.Mutex
	loginChallenges      map[string]loginChallenge
}

// NewAuth 创建认证服务.
func NewAuth(database *gorm.DB, settings *Settings, cipher *crypto.Cipher, audit *Audit, captcha *Captcha, mailer *Mailer, secretKey []byte, log *slog.Logger) *Auth {
	return &Auth{
		db:                   database,
		settings:             settings,
		cipher:               cipher,
		audit:                audit,
		captcha:              captcha,
		mailer:               mailer,
		secretKey:            secretKey,
		log:                  log,
		loginLimiter:         NewRateLimiter(LoginFailureWindow),
		passwordResetLimiter: NewRateLimiter(passwordResetRequestWindow),
		loginChallenges:      make(map[string]loginChallenge),
	}
}

// RegisterInput 是注册入参.
type RegisterInput struct {
	Username     string
	Email        string
	Password     string
	CaptchaToken string
}

// Register 注册用户; 首个注册账号进入管理员组.
func (a *Auth) Register(ctx context.Context, in RegisterInput, ip, userAgent string) (*model.User, error) {
	if !a.settings.Bool("register_enabled", true) {
		return nil, apperr.ErrRegisterDisabled
	}
	if err := a.captcha.Verify(ctx, in.CaptchaToken, ip); err != nil {
		return nil, err
	}

	username := strings.TrimSpace(in.Username)
	email := strings.TrimSpace(in.Email)
	if err := validateUsername(username); err != nil {
		return nil, err
	}
	if err := validateEmail(email); err != nil {
		return nil, err
	}
	if !emailDomainAllowed(email, a.settings.String("registration_email_allowlist")) {
		return nil, apperr.ErrEmailNotAllowed
	}
	if err := validatePassword(in.Password); err != nil {
		return nil, err
	}

	var existing int64
	if err := a.db.Model(&model.User{}).Where("username = ?", username).Count(&existing).Error; err != nil {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	if existing > 0 {
		return nil, apperr.ErrInvalidRequest.WithMessage("用户名已被占用")
	}
	if err := a.db.Model(&model.User{}).Where("email = ?", email).Count(&existing).Error; err != nil {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	if existing > 0 {
		return nil, apperr.ErrInvalidRequest.WithMessage("邮箱已被占用")
	}

	var userCount int64
	if err := a.db.Model(&model.User{}).Count(&userCount).Error; err != nil {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}

	var groupID int64
	if userCount == 0 {
		group, err := a.adminGroup()
		if err != nil {
			return nil, err
		}
		groupID = group.ID
	} else {
		group, err := a.defaultGroup()
		if err != nil {
			return nil, err
		}
		groupID = group.ID
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcryptCost)
	if err != nil {
		return nil, apperr.ErrInternal.WithMessage("密码散列失败: " + err.Error())
	}

	now := nowUTC()
	user := model.User{
		Username:     username,
		Email:        email,
		PasswordHash: string(hashed),
		GroupID:      groupID,
		Status:       model.UserStatusNormal,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	verificationRequired := a.settings.Bool("email_verification_enabled", false)
	if !verificationRequired {
		user.EmailVerifiedAt = &now
	}
	if err := a.db.Create(&user).Error; err != nil {
		return nil, apperr.ErrInvalidRequest.WithMessage("创建用户失败: " + err.Error())
	}

	if verificationRequired {
		if err := a.SendVerification(&user); err != nil {
			a.log.Warn("发送邮箱验证邮件失败", "user_id", user.ID, "error", err)
		}
	}

	a.audit.Recordf(Entry{
		UserID:     &user.ID,
		Action:     "register",
		TargetType: "user",
		TargetID:   &user.ID,
		Detail:     map[string]any{"username": user.Username, "first_user": userCount == 0},
		IP:         ip,
		UserAgent:  userAgent,
	})
	return &user, nil
}

// LoginInput 是登录入参.
type LoginInput struct {
	Identifier     string
	Password       string
	TOTPCode       string
	CaptchaToken   string
	LoginChallenge string
}

type loginChallenge struct {
	userID        int64
	ip            string
	userAgent     string
	ipKey         string
	identifierKey string
	expiresAt     time.Time
	inFlight      bool
}

// Login 校验凭据并返回用户.
func (a *Auth) Login(ctx context.Context, in LoginInput, ip, userAgent string) (*model.User, error) {
	if challenge := strings.TrimSpace(in.LoginChallenge); challenge != "" {
		return a.loginWithChallenge(challenge, in.TOTPCode, ip, userAgent)
	}
	if err := a.captcha.Verify(ctx, in.CaptchaToken, ip); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.TOTPCode) != "" {
		return nil, apperr.ErrInvalidRequest.WithMessage("两步验证码必须通过登录挑战提交")
	}
	identifier := strings.TrimSpace(in.Identifier)
	if identifier == "" || in.Password == "" {
		return nil, apperr.ErrInvalidRequest.WithMessage("请输入用户名与密码")
	}

	ipKey := "login:ip:" + ip
	identifierKey := "login:user:" + ip + "|" + strings.ToLower(identifier)
	for _, key := range []string{ipKey, identifierKey} {
		if a.loginLimiter.Count(key) >= LoginFailureLimit {
			return nil, apperr.ErrRateLimited.WithMessage("登录失败次数过多, 请稍后再试").
				WithDetails(map[string]any{"retry_after_seconds": int(LoginFailureWindow.Seconds())})
		}
	}

	var user model.User
	err := a.db.Where("username = ? OR email = ?", identifier, identifier).First(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		a.recordLoginFailure(ipKey, identifierKey)
		a.audit.Recordf(Entry{Action: "login_failed", Detail: map[string]any{"identifier": identifier, "reason": "not_found"}, IP: ip, UserAgent: userAgent})
		return nil, apperr.ErrUnauthorized.WithMessage("用户名或密码错误")
	}
	if err != nil {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	if user.Status == model.UserStatusDisabled {
		a.recordLoginFailure(ipKey, identifierKey)
		a.audit.Recordf(Entry{UserID: &user.ID, Action: "login_failed", Detail: map[string]any{"reason": "disabled"}, IP: ip, UserAgent: userAgent})
		return nil, apperr.ErrForbidden.WithMessage("账号已被禁用")
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(in.Password)) != nil {
		a.recordLoginFailure(ipKey, identifierKey)
		a.audit.Recordf(Entry{UserID: &user.ID, Action: "login_failed", Detail: map[string]any{"reason": "bad_password"}, IP: ip, UserAgent: userAgent})
		return nil, apperr.ErrUnauthorized.WithMessage("用户名或密码错误")
	}

	if user.TOTPSecret != "" {
		if _, err := a.cipher.Decrypt(user.TOTPSecret); err != nil {
			return nil, apperr.ErrInternal.WithMessage("TOTP 秘钥解密失败")
		}
		challenge, err := a.issueLoginChallenge(user.ID, ip, userAgent, ipKey, identifierKey)
		if err != nil {
			return nil, err
		}
		return nil, apperr.ErrUnauthorized.WithMessage("需要两步验证码").WithDetails(map[string]any{
			"totp_required":   true,
			"login_challenge": challenge,
		})
	}

	if err := a.finishLogin(&user, ip, userAgent, ipKey, identifierKey); err != nil {
		return nil, err
	}
	return &user, nil
}
func (a *Auth) issueLoginChallenge(userID int64, ip, userAgent, ipKey, identifierKey string) (string, error) {
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", apperr.ErrInternal.WithMessage("登录验证令牌生成失败: " + err.Error())
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	now := time.Now()

	a.loginChallengesMu.Lock()
	defer a.loginChallengesMu.Unlock()
	if a.loginChallenges == nil {
		a.loginChallenges = make(map[string]loginChallenge)
	}
	for token, challenge := range a.loginChallenges {
		if !challenge.expiresAt.After(now) {
			delete(a.loginChallenges, token)
		}
	}
	if len(a.loginChallenges) >= maxPendingLoginChallenges {
		return "", apperr.ErrRateLimited.WithMessage("待完成的登录验证过多, 请稍后重试")
	}
	a.loginChallenges[token] = loginChallenge{
		userID:        userID,
		ip:            ip,
		userAgent:     userAgent,
		ipKey:         ipKey,
		identifierKey: identifierKey,
		expiresAt:     now.Add(loginChallengeTTL),
	}
	return token, nil
}

func (a *Auth) reserveLoginChallenge(token, ip, userAgent string) (loginChallenge, error) {
	a.loginChallengesMu.Lock()
	defer a.loginChallengesMu.Unlock()
	challenge, ok := a.loginChallenges[token]
	if !ok || !challenge.expiresAt.After(time.Now()) {
		delete(a.loginChallenges, token)
		return loginChallenge{}, apperr.ErrUnauthorized.WithMessage("登录验证已过期或无效, 请重新登录")
	}
	if challenge.ip != ip || challenge.userAgent != userAgent || challenge.inFlight {
		return loginChallenge{}, apperr.ErrUnauthorized.WithMessage("登录验证已过期或无效, 请重新登录")
	}
	challenge.inFlight = true
	a.loginChallenges[token] = challenge
	return challenge, nil
}

func (a *Auth) releaseLoginChallenge(token string, consumed bool) {
	a.loginChallengesMu.Lock()
	defer a.loginChallengesMu.Unlock()
	challenge, ok := a.loginChallenges[token]
	if !ok {
		return
	}
	if consumed {
		delete(a.loginChallenges, token)
		return
	}
	challenge.inFlight = false
	a.loginChallenges[token] = challenge
}

func (a *Auth) loginWithChallenge(token, code, ip, userAgent string) (*model.User, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, apperr.ErrUnauthorized.WithMessage("需要两步验证码")
	}
	challenge, err := a.reserveLoginChallenge(token, ip, userAgent)
	if err != nil {
		return nil, err
	}
	consumed := false
	defer func() { a.releaseLoginChallenge(token, consumed) }()

	for _, key := range []string{challenge.ipKey, challenge.identifierKey} {
		if a.loginLimiter.Count(key) >= LoginFailureLimit {
			return nil, apperr.ErrRateLimited.WithMessage("登录失败次数过多, 请稍后再试").
				WithDetails(map[string]any{"retry_after_seconds": int(LoginFailureWindow.Seconds())})
		}
	}

	var user model.User
	if err := a.db.First(&user, challenge.userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperr.ErrUnauthorized.WithMessage("登录验证已过期或无效, 请重新登录")
		}
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	if user.Status == model.UserStatusDisabled {
		return nil, apperr.ErrForbidden.WithMessage("账号已被禁用")
	}
	if user.TOTPSecret == "" {
		return nil, apperr.ErrUnauthorized.WithMessage("两步验证已关闭, 请重新登录")
	}
	secret, err := a.cipher.Decrypt(user.TOTPSecret)
	if err != nil {
		return nil, apperr.ErrInternal.WithMessage("TOTP 秘钥解密失败")
	}
	if !totp.Validate(code, secret) {
		a.recordLoginFailure(challenge.ipKey, challenge.identifierKey)
		a.audit.Recordf(Entry{UserID: &user.ID, Action: "login_failed", Detail: map[string]any{"reason": "bad_totp"}, IP: ip, UserAgent: userAgent})
		return nil, apperr.ErrUnauthorized.WithMessage("两步验证码错误")
	}
	if err := a.finishLogin(&user, ip, userAgent, challenge.ipKey, challenge.identifierKey); err != nil {
		return nil, err
	}
	consumed = true
	return &user, nil
}

func (a *Auth) finishLogin(user *model.User, ip, userAgent, ipKey, identifierKey string) error {
	if a.settings.Bool("email_verification_enabled", false) && user.EmailVerifiedAt == nil {
		return apperr.ErrForbidden.WithMessage("邮箱尚未验证, 请先完成邮箱验证")
	}

	now := nowUTC()
	if err := a.db.Model(&model.User{}).Where("id = ?", user.ID).Updates(map[string]any{
		"last_login_at": now,
		"last_login_ip": truncate(ip, 45),
		"updated_at":    now,
	}).Error; err != nil {
		return apperr.ErrInternal.WithMessage(err.Error())
	}
	user.LastLoginAt = &now
	user.LastLoginIP = ip
	a.loginLimiter.Reset(identifierKey)
	a.loginLimiter.Reset(ipKey)

	a.audit.Recordf(Entry{UserID: &user.ID, Action: "login", TargetType: "user", TargetID: &user.ID, IP: ip, UserAgent: userAgent})
	return nil
}

// recordLoginFailure 记录一次登录失败 (IP 与账号双键).
func (a *Auth) recordLoginFailure(ipKey, identifierKey string) {
	a.loginLimiter.Add(ipKey)
	a.loginLimiter.Add(identifierKey)
}

// Logout 吊销用户的全部会话并记录登出审计.
func (a *Auth) Logout(userID int64, ip, userAgent string) error {
	result := a.db.Model(&model.User{}).Where("id = ?", userID).Updates(map[string]any{
		"session_version": gorm.Expr("session_version + 1"),
		"updated_at":      nowUTC(),
	})
	if result.Error != nil {
		return apperr.ErrInternal.WithMessage(result.Error.Error())
	}
	if result.RowsAffected != 1 {
		return apperr.ErrUnauthorized
	}
	a.audit.Recordf(Entry{UserID: &userID, Action: "logout", IP: ip, UserAgent: userAgent})
	return nil
}

// ChangePassword 修改密码并吊销修改前签发的全部会话.
func (a *Auth) ChangePassword(userID int64, oldPassword, newPassword, ip, userAgent string) error {
	var user model.User
	if err := a.db.First(&user, userID).Error; err != nil {
		return apperr.ErrNotFound.WithMessage("用户不存在")
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(oldPassword)) != nil {
		return apperr.ErrUnauthorized.WithMessage("原密码错误")
	}
	if err := validatePassword(newPassword); err != nil {
		return err
	}
	hashed, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcryptCost)
	if err != nil {
		return apperr.ErrInternal.WithMessage("密码散列失败: " + err.Error())
	}
	if err := a.db.Model(&model.User{}).Where("id = ?", user.ID).Updates(map[string]any{
		"password_hash":   string(hashed),
		"session_version": gorm.Expr("session_version + 1"),
		"updated_at":      nowUTC(),
	}).Error; err != nil {
		return apperr.ErrInternal.WithMessage(err.Error())
	}
	a.audit.Recordf(Entry{UserID: &user.ID, Action: "password_change", TargetType: "user", TargetID: &user.ID, IP: ip, UserAgent: userAgent})
	return nil
}

// UpdateProfile 更新个人资料 (目前仅昵称).
func (a *Auth) UpdateProfile(userID int64, nickname string) (*model.User, error) {
	nickname = strings.TrimSpace(nickname)
	if utf8.RuneCountInString(nickname) > 64 {
		return nil, apperr.ErrInvalidRequest.WithMessage("昵称过长")
	}
	var user model.User
	if err := a.db.First(&user, userID).Error; err != nil {
		return nil, apperr.ErrNotFound.WithMessage("用户不存在")
	}
	if err := a.db.Model(&model.User{}).Where("id = ?", userID).Updates(map[string]any{
		"nickname":   nickname,
		"updated_at": nowUTC(),
	}).Error; err != nil {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	user.Nickname = nickname
	return &user, nil
}

// PrincipalFor 加载用户, 用户组与有效权限; apiScopes 为 API 密钥的权限子集.
func (a *Auth) PrincipalFor(userID int64, apiKeyID *int64, apiScopes *int64) (*Principal, error) {
	var user model.User
	if err := a.db.First(&user, userID).Error; err != nil {
		return nil, apperr.ErrUnauthorized
	}
	return a.principalForUser(&user, apiKeyID, apiScopes)
}

// SessionPrincipal 校验服务端会话版本并加载会话主体.
func (a *Auth) SessionPrincipal(token string) (*Principal, error) {
	userID, sessionVersion, err := a.parseSessionToken(token)
	if err != nil {
		return nil, apperr.ErrUnauthorized
	}
	var user model.User
	if err := a.db.First(&user, userID).Error; err != nil || user.SessionVersion != sessionVersion {
		return nil, apperr.ErrUnauthorized
	}
	return a.principalForUser(&user, nil, nil)
}

func (a *Auth) principalForUser(user *model.User, apiKeyID *int64, apiScopes *int64) (*Principal, error) {
	if user.Status == model.UserStatusDisabled {
		return nil, apperr.ErrForbidden.WithMessage("账号已被禁用")
	}
	var group model.Group
	if err := a.db.First(&group, user.GroupID).Error; err != nil {
		return nil, apperr.ErrInternal.WithMessage("用户组不存在")
	}
	permissions := group.Permissions
	if apiScopes != nil {
		permissions = permissions & model.Permission(*apiScopes)
	}
	return &Principal{User: user, Group: &group, Permissions: permissions, APIKeyID: apiKeyID}, nil
}

// UserByID 读取用户.
func (a *Auth) UserByID(userID int64) (*model.User, error) {
	var user model.User
	if err := a.db.First(&user, userID).Error; err != nil {
		return nil, apperr.ErrNotFound.WithMessage("用户不存在")
	}
	return &user, nil
}

// IssueSession 生成绑定当前服务端版本的会话令牌.
func (a *Auth) IssueSession(userID int64) (string, error) {
	var user model.User
	if err := a.db.Select("session_version").First(&user, userID).Error; err != nil {
		return "", apperr.ErrInternal.WithMessage(err.Error())
	}
	return a.signSessionToken(userID, user.SessionVersion, time.Now().Add(SessionTTL)), nil
}

// ParseSession 校验会话签名、有效期与当前服务端版本并返回用户 ID.
func (a *Auth) ParseSession(token string) (int64, error) {
	userID, sessionVersion, err := a.parseSessionToken(token)
	if err != nil {
		return 0, err
	}
	var user model.User
	if err := a.db.Select("session_version").First(&user, userID).Error; err != nil {
		return 0, err
	}
	if user.SessionVersion != sessionVersion {
		return 0, errors.New("会话已吊销")
	}
	return userID, nil
}

// SendVerification 发送邮箱验证邮件.
func (a *Auth) SendVerification(user *model.User) error {
	if !a.mailer.Configured() {
		return errors.New("SMTP 未配置")
	}
	token, err := a.signToken(purposeEmailVerify, user.ID, time.Now().Add(EmailVerifyTTL))
	if err != nil {
		return err
	}
	siteURL := strings.TrimRight(strings.TrimSpace(a.settings.String("site_url")), "/")
	link := token
	if siteURL != "" {
		link = siteURL + "/verify-email?token=" + token
	}
	siteName := a.settings.String("site_name")
	body := fmt.Sprintf("你好 %s:\n\n请访问以下链接完成 %s 邮箱验证 (24 小时内有效):\n%s\n\n如果这不是你的操作, 请忽略本邮件.\n", user.Username, siteName, link)
	return a.mailer.Send(user.Email, siteName+" 邮箱验证", body)
}

// VerifyEmail 校验邮箱验证令牌.
func (a *Auth) VerifyEmail(token string) error {
	userID, err := a.parseToken(token, purposeEmailVerify)
	if err != nil {
		return apperr.ErrInvalidRequest.WithMessage("验证链接无效或已过期")
	}
	now := nowUTC()
	result := a.db.Model(&model.User{}).Where("id = ? AND email_verified_at IS NULL", userID).Updates(map[string]any{
		"email_verified_at": now,
		"updated_at":        now,
	})
	if result.Error != nil {
		return apperr.ErrInternal.WithMessage(result.Error.Error())
	}
	a.audit.Recordf(Entry{UserID: &userID, Action: "email_verified", TargetType: "user", TargetID: &userID})
	return nil
}

// RequestPasswordReset 对已注册邮箱发送找回链接; 对不存在或已禁用的账号保持相同响应.
func (a *Auth) RequestPasswordReset(ctx context.Context, email, captchaToken, ip string) error {
	if err := a.captcha.Verify(ctx, captchaToken, ip); err != nil {
		return err
	}
	email = strings.TrimSpace(email)
	if err := validateEmail(email); err != nil {
		return err
	}

	limited := false
	for _, key := range []string{
		"password-reset:ip:" + ip,
		"password-reset:email:" + strings.ToLower(email),
	} {
		allowed, _ := a.passwordResetLimiter.Allow(key, passwordResetRequestLimit)
		if !allowed {
			limited = true
		}
	}
	if limited {
		return nil
	}
	if !a.mailer.Configured() {
		return apperr.ErrInvalidRequest.WithMessage("SMTP 未配置, 无法发送密码找回邮件")
	}
	siteURL := strings.TrimRight(strings.TrimSpace(a.settings.String("site_url")), "/")
	if siteURL == "" {
		return apperr.ErrInvalidRequest.WithMessage("站点地址未配置, 无法生成密码找回链接")
	}

	var user model.User
	if err := a.db.Where("LOWER(email) = ?", strings.ToLower(email)).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return apperr.ErrInternal.WithMessage(err.Error())
	}
	if user.Status == model.UserStatusDisabled {
		return nil
	}

	token := a.signPasswordResetToken(user.ID, user.PasswordHash, time.Now().Add(PasswordResetTTL))
	siteName := a.settings.String("site_name")
	link := siteURL + "/reset-password?token=" + token
	body := fmt.Sprintf(
		"你好 %s:\n\n请访问以下链接重置 %s 密码 (30 分钟内有效):\n%s\n\n完成重置后, 两步验证 (TOTP) 将被关闭. 如果这不是你的操作, 请忽略本邮件.\n",
		user.Username, siteName, link,
	)
	if err := a.mailer.Send(user.Email, siteName+" 密码找回", body); err != nil {
		a.log.Warn("发送密码找回邮件失败", "user_id", user.ID, "error", err)
	}
	return nil
}

// ResetPassword 消费密码找回令牌并关闭两步验证.
func (a *Auth) ResetPassword(token, newPassword, ip, userAgent string) error {
	userID, credentialTag, err := a.parsePasswordResetToken(token)
	if err != nil {
		return apperr.ErrInvalidRequest.WithMessage("密码找回链接无效或已过期")
	}
	if err := validatePassword(newPassword); err != nil {
		return err
	}
	hashed, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcryptCost)
	if err != nil {
		return apperr.ErrInternal.WithMessage("密码散列失败: " + err.Error())
	}

	now := nowUTC()
	var user model.User
	err = a.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&user, userID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return apperr.ErrInvalidRequest.WithMessage("密码找回链接无效或已过期")
			}
			return apperr.ErrInternal.WithMessage(err.Error())
		}
		expectedTag := a.passwordResetCredentialTag(user.PasswordHash)
		if !hmac.Equal([]byte(credentialTag), []byte(expectedTag)) {
			return apperr.ErrInvalidRequest.WithMessage("密码找回链接无效或已过期")
		}
		result := tx.Model(&model.User{}).Where("id = ?", userID).Updates(map[string]any{
			"password_hash":   string(hashed),
			"totp_secret":     "",
			"session_version": gorm.Expr("session_version + 1"),
			"updated_at":      now,
		})
		if result.Error != nil {
			return apperr.ErrInternal.WithMessage(result.Error.Error())
		}
		if result.RowsAffected != 1 {
			return apperr.ErrInvalidRequest.WithMessage("密码找回链接无效或已过期")
		}
		return nil
	})
	if err != nil {
		return apperr.From(err)
	}
	a.audit.Recordf(Entry{UserID: &user.ID, Action: "password_reset", TargetType: "user", TargetID: &user.ID, IP: ip, UserAgent: userAgent})
	return nil
}

// TOTPSetup 生成 TOTP 秘钥 (尚未启用).
func (a *Auth) TOTPSetup(userID int64) (secret string, otpauthURL string, err error) {
	user, err := a.UserByID(userID)
	if err != nil {
		return "", "", err
	}
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      a.settings.String("site_name"),
		AccountName: user.Username,
	})
	if err != nil {
		return "", "", apperr.ErrInternal.WithMessage("生成 TOTP 秘钥失败: " + err.Error())
	}
	return key.Secret(), key.URL(), nil
}

// TOTPEnable 校验一次性验证码后启用两步验证.
func (a *Auth) TOTPEnable(userID int64, secret, code, ip, userAgent string) error {
	secret = strings.TrimSpace(secret)
	code = strings.TrimSpace(code)
	if secret == "" || code == "" {
		return apperr.ErrInvalidRequest.WithMessage("缺少 TOTP 秘钥或验证码")
	}
	if !totp.Validate(code, secret) {
		return apperr.ErrInvalidRequest.WithMessage("两步验证码错误")
	}
	encrypted, err := a.cipher.Encrypt(secret)
	if err != nil {
		return apperr.ErrInternal.WithMessage("加密 TOTP 秘钥失败: " + err.Error())
	}
	if err := a.db.Model(&model.User{}).Where("id = ?", userID).Updates(map[string]any{
		"totp_secret": encrypted,
		"updated_at":  nowUTC(),
	}).Error; err != nil {
		return apperr.ErrInternal.WithMessage(err.Error())
	}
	a.audit.Recordf(Entry{UserID: &userID, Action: "totp_enable", TargetType: "user", TargetID: &userID, IP: ip, UserAgent: userAgent})
	return nil
}

// TOTPDisable 关闭两步验证, 需要校验密码.
func (a *Auth) TOTPDisable(userID int64, password, ip, userAgent string) error {
	var user model.User
	if err := a.db.First(&user, userID).Error; err != nil {
		return apperr.ErrNotFound.WithMessage("用户不存在")
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
		return apperr.ErrUnauthorized.WithMessage("密码错误")
	}
	if err := a.db.Model(&model.User{}).Where("id = ?", userID).Updates(map[string]any{
		"totp_secret": "",
		"updated_at":  nowUTC(),
	}).Error; err != nil {
		return apperr.ErrInternal.WithMessage(err.Error())
	}
	a.audit.Recordf(Entry{UserID: &userID, Action: "totp_disable", TargetType: "user", TargetID: &userID, IP: ip, UserAgent: userAgent})
	return nil
}

// adminGroup 返回管理员组.
func (a *Auth) adminGroup() (*model.Group, error) {
	var group model.Group
	err := a.db.Where("permissions = ?", int64(model.AllPermissions())).Order("sort ASC, id ASC").First(&group).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = a.db.Order("permissions DESC, sort ASC, id ASC").First(&group).Error
	}
	if err != nil {
		return nil, apperr.ErrInternal.WithMessage("管理员组不存在: " + err.Error())
	}
	return &group, nil
}

// defaultGroup 返回新用户默认组.
func (a *Auth) defaultGroup() (*model.Group, error) {
	// 内置用户组不能作为默认组: 配置指向内置组时按未设置处理.
	if id := a.settings.OptionalInt64("default_group_id"); id != nil {
		var group model.Group
		if err := a.db.First(&group, *id).Error; err == nil && !group.IsSystem {
			return &group, nil
		}
	}
	var group model.Group
	err := a.db.Where("is_default = ?", true).Order("sort ASC, id ASC").First(&group).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// 没有默认组时退回编号最小的非内置组, 内置组 (管理员 / 只读) 永远不作为新用户默认组.
		err = a.db.Where("is_system = ?", false).Order("id ASC").First(&group).Error
	}
	if err != nil {
		return nil, apperr.ErrInternal.WithMessage("默认用户组不存在: " + err.Error())
	}
	return &group, nil
}

// signToken 生成 HMAC 签名的用途令牌.
func (a *Auth) signToken(purpose string, userID int64, expiresAt time.Time) (string, error) {
	payload := strings.Join([]string{purpose, strconv.FormatInt(userID, 10), strconv.FormatInt(expiresAt.Unix(), 10)}, "|")
	return a.encodeToken(payload), nil
}

func (a *Auth) signSessionToken(userID, sessionVersion int64, expiresAt time.Time) string {
	payload := strings.Join([]string{
		purposeSession,
		strconv.FormatInt(userID, 10),
		strconv.FormatInt(sessionVersion, 10),
		strconv.FormatInt(expiresAt.Unix(), 10),
	}, "|")
	return a.encodeToken(payload)
}

func (a *Auth) encodeToken(payload string) string {
	signature := a.sign(payload)
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func (a *Auth) verifiedTokenFields(token string) ([]string, error) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 2 {
		return nil, errors.New("令牌格式非法")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, err
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	if !hmac.Equal(signature, a.sign(string(payload))) {
		return nil, errors.New("令牌签名校验失败")
	}
	return strings.Split(string(payload), "|"), nil
}

// parseToken 校验签名与有效期并返回用户 ID.
func (a *Auth) parseToken(token, purpose string) (int64, error) {
	fields, err := a.verifiedTokenFields(token)
	if err != nil {
		return 0, err
	}
	if len(fields) != 3 || fields[0] != purpose {
		return 0, errors.New("令牌用途不匹配")
	}
	userID, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return 0, err
	}
	expiresAt, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil {
		return 0, err
	}
	if time.Now().Unix() > expiresAt {
		return 0, errors.New("令牌已过期")
	}
	return userID, nil
}

func (a *Auth) parseSessionToken(token string) (int64, int64, error) {
	fields, err := a.verifiedTokenFields(token)
	if err != nil {
		return 0, 0, err
	}
	if len(fields) != 4 || fields[0] != purposeSession {
		return 0, 0, errors.New("会话令牌格式非法")
	}
	userID, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return 0, 0, err
	}
	sessionVersion, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil {
		return 0, 0, err
	}
	expiresAt, err := strconv.ParseInt(fields[3], 10, 64)
	if err != nil {
		return 0, 0, err
	}
	if time.Now().Unix() > expiresAt {
		return 0, 0, errors.New("令牌已过期")
	}
	return userID, sessionVersion, nil
}
func (a *Auth) signPasswordResetToken(userID int64, passwordHash string, expiresAt time.Time) string {
	payload := strings.Join([]string{
		purposePasswordReset,
		strconv.FormatInt(userID, 10),
		strconv.FormatInt(expiresAt.Unix(), 10),
		a.passwordResetCredentialTag(passwordHash),
	}, "|")
	signature := a.sign(payload)
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func (a *Auth) parsePasswordResetToken(token string) (int64, string, error) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 2 {
		return 0, "", errors.New("令牌格式非法")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return 0, "", err
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return 0, "", err
	}
	if !hmac.Equal(signature, a.sign(string(payload))) {
		return 0, "", errors.New("令牌签名校验失败")
	}
	fields := strings.Split(string(payload), "|")
	if len(fields) != 4 || fields[0] != purposePasswordReset {
		return 0, "", errors.New("令牌用途不匹配")
	}
	userID, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return 0, "", err
	}
	expiresAt, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil {
		return 0, "", err
	}
	if time.Now().Unix() > expiresAt {
		return 0, "", errors.New("令牌已过期")
	}
	return userID, fields[3], nil
}

func (a *Auth) passwordResetCredentialTag(passwordHash string) string {
	return base64.RawURLEncoding.EncodeToString(a.sign("password-reset|" + passwordHash))
}

func (a *Auth) sign(payload string) []byte {
	mac := hmac.New(sha256.New, a.secretKey)
	mac.Write([]byte(payload))
	return mac.Sum(nil)
}

// validateUsername 校验用户名: 任意字符, 去首尾空格, 不含控制字符.
func validateUsername(username string) error {
	if username == "" {
		return apperr.ErrInvalidRequest.WithMessage("用户名不能为空")
	}
	if utf8.RuneCountInString(username) > maxUsernameRuneLength {
		return apperr.ErrInvalidRequest.WithMessage("用户名过长")
	}
	for _, r := range username {
		if unicode.IsControl(r) {
			return apperr.ErrInvalidRequest.WithMessage("用户名不能包含控制字符")
		}
	}
	return nil
}

// validateEmail 校验邮箱格式.
func validateEmail(email string) error {
	if email == "" {
		return apperr.ErrInvalidRequest.WithMessage("邮箱不能为空")
	}
	if len(email) > 255 {
		return apperr.ErrInvalidRequest.WithMessage("邮箱过长")
	}
	parsed, err := mail.ParseAddress(email)
	if err != nil || !strings.EqualFold(parsed.Address, email) {
		return apperr.ErrInvalidRequest.WithMessage("邮箱格式不正确")
	}
	return nil
}

func emailDomainAllowed(email, allowlist string) bool {
	if strings.TrimSpace(allowlist) == "" {
		return true
	}
	at := strings.LastIndexByte(email, '@')
	if at < 0 {
		return false
	}
	domain := email[at+1:]
	for remaining := allowlist; ; {
		allowedDomain, rest, found := strings.Cut(remaining, ",")
		if strings.EqualFold(strings.TrimSpace(allowedDomain), domain) {
			return true
		}
		if !found {
			return false
		}
		remaining = rest
	}
}

// validatePassword 校验密码强度.
func validatePassword(password string) error {
	length := utf8.RuneCountInString(password)
	if length < minPasswordRuneLength {
		return apperr.ErrInvalidRequest.WithMessage(fmt.Sprintf("密码至少 %d 位", minPasswordRuneLength))
	}
	if length > maxPasswordRuneLength {
		return apperr.ErrInvalidRequest.WithMessage("密码过长")
	}
	// bcrypt 只接受 72 字节以内的输入.
	if len(password) > 72 {
		return apperr.ErrInvalidRequest.WithMessage("密码字节长度不能超过 72")
	}
	return nil
}
