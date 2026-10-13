package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"strconv"
	"strings"
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
	// bcryptCost 是密码散列强度.
	bcryptCost = 12
	// LoginFailureLimit 与 LoginFailureWindow 定义登录失败限流: 窗口内失败次数达到上限后拒绝登录.
	LoginFailureLimit  = 20
	LoginFailureWindow = 10 * time.Minute
	// 令牌用途.
	purposeSession        = "session"
	purposeEmailVerify    = "email_verify"
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

// Auth 负责注册, 登录, 会话, 密码, TOTP 与邮箱验证.
type Auth struct {
	db           *gorm.DB
	settings     *Settings
	cipher       *crypto.Cipher
	audit        *Audit
	captcha      *Captcha
	mailer       *Mailer
	secretKey    []byte
	log          *slog.Logger
	loginLimiter *RateLimiter
}

// NewAuth 创建认证服务.
func NewAuth(database *gorm.DB, settings *Settings, cipher *crypto.Cipher, audit *Audit, captcha *Captcha, mailer *Mailer, secretKey []byte, log *slog.Logger) *Auth {
	return &Auth{
		db:           database,
		settings:     settings,
		cipher:       cipher,
		audit:        audit,
		captcha:      captcha,
		mailer:       mailer,
		secretKey:    secretKey,
		log:          log,
		loginLimiter: NewRateLimiter(LoginFailureWindow),
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
	Identifier   string
	Password     string
	TOTPCode     string
	CaptchaToken string
}

// Login 校验凭据并返回用户.
func (a *Auth) Login(ctx context.Context, in LoginInput, ip, userAgent string) (*model.User, error) {
	if err := a.captcha.Verify(ctx, in.CaptchaToken, ip); err != nil {
		return nil, err
	}
	identifier := strings.TrimSpace(in.Identifier)
	if identifier == "" || in.Password == "" {
		return nil, apperr.ErrInvalidRequest.WithMessage("请输入用户名与密码")
	}

	// 登录失败限流: 按来源 IP 与 (IP, 账号) 双键计数, 防止在线爆破且不造成全局账号锁定.
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
		secret, err := a.cipher.Decrypt(user.TOTPSecret)
		if err != nil {
			return nil, apperr.ErrInternal.WithMessage("TOTP 秘钥解密失败")
		}
		code := strings.TrimSpace(in.TOTPCode)
		if code == "" {
			return nil, apperr.ErrUnauthorized.WithMessage("需要两步验证码").WithDetails(map[string]any{"totp_required": true})
		}
		if !totp.Validate(code, secret) {
			a.recordLoginFailure(ipKey, identifierKey)
			a.audit.Recordf(Entry{UserID: &user.ID, Action: "login_failed", Detail: map[string]any{"reason": "bad_totp"}, IP: ip, UserAgent: userAgent})
			return nil, apperr.ErrUnauthorized.WithMessage("两步验证码错误")
		}
	}

	if a.settings.Bool("email_verification_enabled", false) && user.EmailVerifiedAt == nil {
		return nil, apperr.ErrForbidden.WithMessage("邮箱尚未验证, 请先完成邮箱验证")
	}

	now := nowUTC()
	if err := a.db.Model(&model.User{}).Where("id = ?", user.ID).Updates(map[string]any{
		"last_login_at": now,
		"last_login_ip": truncate(ip, 45),
		"updated_at":    now,
	}).Error; err != nil {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	user.LastLoginAt = &now
	user.LastLoginIP = ip
	a.loginLimiter.Reset(identifierKey)
	a.loginLimiter.Reset(ipKey)

	a.audit.Recordf(Entry{UserID: &user.ID, Action: "login", TargetType: "user", TargetID: &user.ID, IP: ip, UserAgent: userAgent})
	return &user, nil
}

// recordLoginFailure 记录一次登录失败 (IP 与账号双键).
func (a *Auth) recordLoginFailure(ipKey, identifierKey string) {
	a.loginLimiter.Add(ipKey)
	a.loginLimiter.Add(identifierKey)
}

// Logout 记录登出审计.
func (a *Auth) Logout(userID int64, ip, userAgent string) {
	a.audit.Recordf(Entry{UserID: &userID, Action: "logout", IP: ip, UserAgent: userAgent})
}

// ChangePassword 修改密码.
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
		"password_hash": string(hashed),
		"updated_at":    nowUTC(),
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
	return &Principal{User: &user, Group: &group, Permissions: permissions, APIKeyID: apiKeyID}, nil
}

// UserByID 读取用户.
func (a *Auth) UserByID(userID int64) (*model.User, error) {
	var user model.User
	if err := a.db.First(&user, userID).Error; err != nil {
		return nil, apperr.ErrNotFound.WithMessage("用户不存在")
	}
	return &user, nil
}

// IssueSession 生成会话令牌.
func (a *Auth) IssueSession(userID int64) (string, error) {
	return a.signToken(purposeSession, userID, time.Now().Add(SessionTTL))
}

// ParseSession 校验会话令牌并返回用户 ID.
func (a *Auth) ParseSession(token string) (int64, error) {
	return a.parseToken(token, purposeSession)
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
	if id := a.settings.OptionalInt64("default_group_id"); id != nil {
		var group model.Group
		if err := a.db.First(&group, *id).Error; err == nil {
			return &group, nil
		}
	}
	var group model.Group
	err := a.db.Where("is_default = ?", true).Order("sort ASC, id ASC").First(&group).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = a.db.Order("id ASC").First(&group).Error
	}
	if err != nil {
		return nil, apperr.ErrInternal.WithMessage("默认用户组不存在: " + err.Error())
	}
	return &group, nil
}

// signToken 生成 HMAC 签名的令牌.
func (a *Auth) signToken(purpose string, userID int64, expiresAt time.Time) (string, error) {
	payload := strings.Join([]string{purpose, strconv.FormatInt(userID, 10), strconv.FormatInt(expiresAt.Unix(), 10)}, "|")
	signature := a.sign(payload)
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

// parseToken 校验签名与有效期并返回用户 ID.
func (a *Auth) parseToken(token, purpose string) (int64, error) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 2 {
		return 0, errors.New("令牌格式非法")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return 0, err
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return 0, err
	}
	if !hmac.Equal(signature, a.sign(string(payload))) {
		return 0, errors.New("令牌签名校验失败")
	}
	fields := strings.Split(string(payload), "|")
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
