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
	"unicode/utf8"

	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

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
	LoginFailureLimit           = 20
	LoginFailureWindow          = 10 * time.Minute
	passwordResetRequestLimit   = 5
	passwordResetRequestWindow  = time.Hour
	verificationResendUserLimit = 3
	verificationResendIPLimit   = 10
	verificationResendWindow    = time.Hour
	loginChallengeTTL           = 5 * time.Minute
	maxPendingLoginChallenges   = 10000
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
	if p == nil {
		return false
	}
	adminBits := model.PermManagePolicies | model.PermManageUsers | model.PermManageGroups | model.PermManageSettings | model.PermViewLogs | model.PermViewAll | model.PermDeleteAll
	return p.Permissions&adminBits != 0
}

// Auth 负责注册, 登录, 会话, 密码, TOTP, 邮箱验证与密码找回.
type Auth struct {
	db                        *gorm.DB
	settings                  *Settings
	cipher                    *crypto.Cipher
	audit                     *Audit
	captcha                   *Captcha
	mailer                    *Mailer
	secretKey                 []byte
	log                       *slog.Logger
	loginLimiter              *RateLimiter
	passwordResetLimiter      *RateLimiter
	verificationResendLimiter *RateLimiter
	dummyPasswordHash         []byte
	loginChallengesMu         sync.Mutex
	loginChallenges           map[string]loginChallenge
}

// NewAuth 创建认证服务.
func NewAuth(database *gorm.DB, settings *Settings, cipher *crypto.Cipher, audit *Audit, captcha *Captcha, mailer *Mailer, secretKey []byte, log *slog.Logger) *Auth {
	return &Auth{
		db:                        database,
		settings:                  settings,
		cipher:                    cipher,
		audit:                     audit,
		captcha:                   captcha,
		mailer:                    mailer,
		secretKey:                 secretKey,
		log:                       log,
		loginLimiter:              NewRateLimiter(LoginFailureWindow),
		dummyPasswordHash:         loginDummyPasswordHash(),
		passwordResetLimiter:      NewRateLimiter(passwordResetRequestWindow),
		verificationResendLimiter: NewRateLimiter(verificationResendWindow),
		loginChallenges:           make(map[string]loginChallenge),
	}
}

var cachedLoginDummyHash struct {
	sync.Once
	hash []byte
	err  error
}

func loginDummyPasswordHash() []byte {
	cachedLoginDummyHash.Do(func() {
		cachedLoginDummyHash.hash, cachedLoginDummyHash.err = bcrypt.GenerateFromPassword([]byte("picsky-login-timing-dummy"), bcryptCost)
	})
	if cachedLoginDummyHash.err != nil {
		panic(fmt.Sprintf("generate dummy bcrypt hash: %v", cachedLoginDummyHash.err))
	}
	return cachedLoginDummyHash.hash
}

// RegisterInput 是注册入参.
type RegisterInput struct {
	Username     string
	Email        string
	Password     string
	CaptchaToken string
}

// ErrDuplicateRegistration 表示用户名或邮箱已被占用, 由公开 API 转为通用响应.
var ErrDuplicateRegistration = errors.New("duplicate registration")

// Register 注册用户; 首个注册账号进入管理员组.
func (a *Auth) Register(ctx context.Context, in RegisterInput, ip, userAgent string) (*model.User, error) {
	if !a.settings.Bool("register_enabled", true) {
		return nil, apperr.ErrRegisterDisabled
	}
	if err := a.captcha.Verify(ctx, in.CaptchaToken, ip); err != nil {
		return nil, err
	}

	username := strings.TrimSpace(in.Username)
	email := normalizeEmail(in.Email)
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

	hashed, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcryptCost)
	if err != nil {
		return nil, apperr.ErrInternal.WithMessage("密码散列失败: " + err.Error())
	}

	var existing int64
	if err := a.db.Model(&model.User{}).Where("username = ?", username).Count(&existing).Error; err != nil {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	if existing > 0 {
		return nil, ErrDuplicateRegistration
	}
	if err := a.db.Model(&model.User{}).Where("email = ?", email).Count(&existing).Error; err != nil {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	if existing > 0 {
		return nil, ErrDuplicateRegistration
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

	now := nowUTC()
	verificationRequired := a.settings.Bool("email_verification_enabled", false)
	user := model.User{
		Username:     username,
		Email:        email,
		PasswordHash: string(hashed),
		GroupID:      groupID,
		Status:       model.UserStatusNormal,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if verificationRequired {
		readOnly, err := a.readOnlyGroup()
		if err != nil {
			return nil, err
		}
		user.RestoreGroupID = &groupID
		user.GroupID = readOnly.ID
		user.Status = model.UserStatusUnverified
	} else {
		user.EmailVerifiedAt = &now
	}
	result := a.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&user)
	if result.Error != nil {
		return nil, apperr.ErrInternal.WithMessage("创建用户失败: " + result.Error.Error())
	}
	if result.RowsAffected == 0 {
		return nil, ErrDuplicateRegistration
	}

	if verificationRequired {
		if err := a.QueueVerification(&user); err != nil {
			a.log.Warn("排队邮箱验证邮件失败", "user_id", user.ID, "error", err)
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
	Email          string
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
	email := normalizeEmail(in.Email)
	if email == "" || in.Password == "" {
		return nil, apperr.ErrInvalidRequest.WithMessage("请输入邮箱与密码")
	}

	ipKey := "login:ip:" + ip
	identifierKey := "login:email:" + ip + "|" + email
	for _, key := range []string{ipKey, identifierKey} {
		if a.loginLimiter.Count(key) >= LoginFailureLimit {
			return nil, apperr.ErrRateLimited.WithMessage("登录失败次数过多, 请稍后再试").
				WithDetails(map[string]any{"retry_after_seconds": int(LoginFailureWindow.Seconds())})
		}
	}

	var user model.User
	err := a.db.Where("email = ?", email).First(&user).Error
	found := err == nil
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}

	passwordHash := a.dummyPasswordHash
	reason := "not_found"
	if found {
		reason = "bad_password"
		if user.Status == model.UserStatusBanned {
			reason = "banned"
		} else {
			passwordHash = []byte(user.PasswordHash)
		}
	}
	passwordErr := bcrypt.CompareHashAndPassword(passwordHash, []byte(in.Password))
	if !found || user.Status == model.UserStatusBanned || passwordErr != nil {
		a.recordLoginFailure(ipKey, identifierKey)
		entry := Entry{
			Action:    "login_failed",
			Detail:    map[string]any{"email": email, "reason": reason},
			IP:        ip,
			UserAgent: userAgent,
		}
		if found {
			entry.UserID = &user.ID
		}
		a.audit.Recordf(entry)
		return nil, apperr.ErrUnauthorized.WithMessage("邮箱或密码错误")
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
	if user.Status == model.UserStatusBanned {
		return nil, apperr.ErrForbidden.WithMessage("账号已封禁")
	}
	if user.TOTPSecret == "" {
		return nil, apperr.ErrUnauthorized.WithMessage("两步验证已关闭, 请重新登录")
	}
	secret, err := a.cipher.Decrypt(user.TOTPSecret)
	if err != nil {
		return nil, apperr.ErrInternal.WithMessage("TOTP 秘钥解密失败")
	}
	counter, valid := totpCodeCounter(code, secret, time.Now())
	if !valid {
		a.recordLoginFailure(challenge.ipKey, challenge.identifierKey)
		a.audit.Recordf(Entry{UserID: &user.ID, Action: "login_failed", Detail: map[string]any{"reason": "bad_totp"}, IP: ip, UserAgent: userAgent})
		return nil, apperr.ErrUnauthorized.WithMessage("两步验证码错误")
	}
	consumedCounter := a.db.Model(&model.User{}).
		Where("id = ? AND totp_secret = ? AND totp_last_counter < ?", user.ID, user.TOTPSecret, counter).
		Updates(map[string]any{"totp_last_counter": counter, "updated_at": nowUTC()})
	if consumedCounter.Error != nil {
		return nil, apperr.ErrInternal.WithMessage(consumedCounter.Error.Error())
	}
	if consumedCounter.RowsAffected != 1 {
		a.recordLoginFailure(challenge.ipKey, challenge.identifierKey)
		a.audit.Recordf(Entry{UserID: &user.ID, Action: "login_failed", Detail: map[string]any{"reason": "totp_replay"}, IP: ip, UserAgent: userAgent})
		return nil, apperr.ErrUnauthorized.WithMessage("两步验证码错误或已使用")
	}
	if err := a.finishLogin(&user, ip, userAgent, challenge.ipKey, challenge.identifierKey); err != nil {
		return nil, err
	}
	consumed = true
	return &user, nil
}

func (a *Auth) finishLogin(user *model.User, ip, userAgent, ipKey, identifierKey string) error {

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

func totpCodeCounter(code, secret string, now time.Time) (int64, bool) {
	const periodSeconds = int64(30)
	currentCounter := now.Unix() / periodSeconds
	for offset := int64(1); offset >= -1; offset-- {
		counter := currentCounter + offset
		if counter < 0 {
			continue
		}
		candidate, err := totp.GenerateCode(secret, time.Unix(counter*periodSeconds, 0))
		if err == nil && hmac.Equal([]byte(code), []byte(candidate)) {
			return counter, true
		}
	}
	return 0, false
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
	if user.Status == model.UserStatusBanned {
		return nil, apperr.ErrForbidden.WithMessage("账号已封禁")
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

// ResendVerification queues a verification email under per-user and per-IP limits.
func (a *Auth) ResendVerification(user *model.User, ip string) error {
	if user == nil {
		return apperr.ErrUnauthorized
	}
	if user.EmailVerifiedAt != nil {
		return nil
	}
	userAllowed, userRetry := a.verificationResendLimiter.Allow(fmt.Sprintf("user:%d", user.ID), verificationResendUserLimit)
	ipAllowed, ipRetry := a.verificationResendLimiter.Allow("ip:"+ip, verificationResendIPLimit)
	if !userAllowed || !ipAllowed {
		retryAfter := userRetry
		if ipRetry > retryAfter {
			retryAfter = ipRetry
		}
		retrySeconds := int(retryAfter / time.Second)
		if retrySeconds < 1 {
			retrySeconds = 1
		}
		return apperr.ErrRateLimited.WithMessage("验证邮件请求过于频繁, 请稍后再试").
			WithDetails(map[string]any{"retry_after_seconds": retrySeconds})
	}
	return a.QueueVerification(user)
}

// QueueVerification 将邮箱验证邮件放入异步发送队列.
func (a *Auth) QueueVerification(user *model.User) error {
	if user.EmailVerifiedAt != nil {
		return nil
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
	return a.mailer.Enqueue(user.Email, siteName+" 邮箱验证", body)
}

// VerifyEmail 校验邮箱验证令牌.
func (a *Auth) VerifyEmail(token string) error {
	userID, err := a.parseToken(token, purposeEmailVerify)
	if err != nil {
		return apperr.ErrInvalidRequest.WithMessage("验证链接无效或已过期")
	}
	now := nowUTC()
	if err := a.db.Transaction(func(tx *gorm.DB) error {
		var user model.User
		if err := tx.First(&user, userID).Error; err != nil {
			return err
		}
		if user.EmailVerifiedAt != nil {
			return nil
		}
		updates := map[string]any{
			"email_verified_at": now,
			"updated_at":        now,
		}
		if user.Status == model.UserStatusUnverified {
			groupID := user.GroupID
			if user.RestoreGroupID != nil {
				groupID = *user.RestoreGroupID
			} else {
				var group model.Group
				if err := tx.Where("is_default = ? AND is_system = ?", true, false).Order("sort ASC, id ASC").First(&group).Error; err != nil {
					return err
				}
				groupID = group.ID
			}
			updates["status"] = model.UserStatusNormal
			updates["group_id"] = groupID
			updates["restore_group_id"] = nil
		}
		return tx.Model(&model.User{}).Where("id = ? AND email_verified_at IS NULL AND status = ?", userID, user.Status).Updates(updates).Error
	}); err != nil {
		return apperr.ErrInternal.WithMessage(err.Error())
	}
	a.audit.Recordf(Entry{UserID: &userID, Action: "email_verified", TargetType: "user", TargetID: &userID})
	return nil
}

// RequestPasswordReset 对已注册邮箱发送找回链接; 对不存在或已封禁的账号保持相同响应.
func (a *Auth) RequestPasswordReset(ctx context.Context, email, captchaToken, ip string) error {
	if err := a.captcha.Verify(ctx, captchaToken, ip); err != nil {
		return err
	}
	email = normalizeEmail(email)
	if err := validateEmail(email); err != nil {
		return err
	}

	limited := false
	for _, key := range []string{
		"password-reset:ip:" + ip,
		"password-reset:email:" + email,
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
	if err := a.db.Where("email = ?", email).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return apperr.ErrInternal.WithMessage(err.Error())
	}
	if user.Status == model.UserStatusBanned {
		return nil
	}

	token := a.signPasswordResetToken(user.ID, user.PasswordHash, time.Now().Add(PasswordResetTTL))
	siteName := a.settings.String("site_name")
	link := siteURL + "/reset-password#token=" + token
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
			"password_hash":     string(hashed),
			"totp_secret":       "",
			"totp_last_counter": int64(0),
			"session_version":   gorm.Expr("session_version + 1"),
			"updated_at":        now,
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

// TOTPEnable 验证当前密码与未使用的验证码后启用两步验证.
func (a *Auth) TOTPEnable(userID int64, secret, code, password, ip, userAgent string) error {
	secret = strings.TrimSpace(secret)
	code = strings.TrimSpace(code)
	if secret == "" || code == "" {
		return apperr.ErrInvalidRequest.WithMessage("缺少 TOTP 秘钥或验证码")
	}
	var user model.User
	if err := a.db.First(&user, userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return apperr.ErrNotFound.WithMessage("用户不存在")
		}
		return apperr.ErrInternal.WithMessage(err.Error())
	}
	if password == "" || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
		return apperr.ErrUnauthorized.WithMessage("当前密码错误")
	}
	counter, valid := totpCodeCounter(code, secret, time.Now())
	if !valid {
		return apperr.ErrInvalidRequest.WithMessage("两步验证码错误或已使用")
	}
	encrypted, err := a.cipher.Encrypt(secret)
	if err != nil {
		return apperr.ErrInternal.WithMessage("加密 TOTP 秘钥失败: " + err.Error())
	}
	query := a.db.Model(&model.User{}).Where("id = ? AND password_hash = ?", userID, user.PasswordHash)
	if user.TOTPSecret == "" {
		query = query.Where("(totp_secret = ? OR totp_secret IS NULL)", "")
	} else {
		query = query.Where("totp_secret = ?", user.TOTPSecret)
	}
	result := query.Updates(map[string]any{
		"totp_secret":       encrypted,
		"totp_last_counter": counter,
		"updated_at":        nowUTC(),
	})
	if result.Error != nil {
		return apperr.ErrInternal.WithMessage(result.Error.Error())
	}
	if result.RowsAffected != 1 {
		return apperr.ErrUnauthorized.WithMessage("账号凭据已更新, 请重新验证")
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
		"totp_secret":       "",
		"totp_last_counter": int64(0),
		"updated_at":        nowUTC(),
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

func (a *Auth) readOnlyGroup() (*model.Group, error) {
	var group model.Group
	if err := a.db.Where("is_system = ? AND name = ?", true, "只读").First(&group).Error; err != nil {
		return nil, apperr.ErrInternal.WithMessage("只读用户组不存在: " + err.Error())
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

// validateUsername 校验用户名: 去首尾空格后仅允许 ASCII 英文字母与数字.
func validateUsername(username string) error {
	if username == "" {
		return apperr.ErrInvalidRequest.WithMessage("用户名不能为空")
	}
	if utf8.RuneCountInString(username) > maxUsernameRuneLength {
		return apperr.ErrInvalidRequest.WithMessage("用户名过长")
	}
	for _, r := range username {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return apperr.ErrInvalidRequest.WithMessage("用户名只能包含大小写英文字母和数字")
		}
	}
	return nil
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
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
