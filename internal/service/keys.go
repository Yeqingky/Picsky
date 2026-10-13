package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/yeqingky/Picsky/internal/apperr"
	"github.com/yeqingky/Picsky/internal/crypto"
	"github.com/yeqingky/Picsky/internal/model"
)

// accessKeyPrefix 与 secretPrefix 是密钥可读前缀.
const (
	accessKeyPrefix = "pk_"
	secretPrefix    = "sk_"
)

// Keys 管理 API 密钥.
type Keys struct {
	db        *gorm.DB
	secretKey []byte
}

// NewKeys 创建密钥服务.
func NewKeys(database *gorm.DB, secretKey []byte) *Keys {
	return &Keys{db: database, secretKey: secretKey}
}

// Create 创建密钥并返回明文 secret (仅此一次).
func (k *Keys) Create(userID int64, name string, scopes *model.Permission, expiresAt *time.Time) (*model.APIKey, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, "", apperr.ErrInvalidRequest.WithMessage("密钥名称不能为空")
	}
	if len([]rune(name)) > 64 {
		return nil, "", apperr.ErrInvalidRequest.WithMessage("密钥名称过长")
	}

	accessKey, err := randomAccessKey()
	if err != nil {
		return nil, "", apperr.ErrInternal.WithMessage(err.Error())
	}
	secretRaw, err := crypto.RandomToken(32)
	if err != nil {
		return nil, "", apperr.ErrInternal.WithMessage(err.Error())
	}
	secret := secretPrefix + secretRaw

	var scopesValue *int64
	if scopes != nil {
		value := int64(*scopes)
		scopesValue = &value
	}

	row := model.APIKey{
		UserID:     userID,
		Name:       name,
		AccessKey:  accessKey,
		SecretHash: k.hashSecret(secret),
		Scopes:     scopesValue,
		ExpiresAt:  expiresAt,
		CreatedAt:  nowUTC(),
	}
	if err := k.db.Create(&row).Error; err != nil {
		return nil, "", apperr.ErrInternal.WithMessage("创建密钥失败: " + err.Error())
	}
	return &row, accessKey + "." + secret, nil
}

// List 列出用户的密钥.
func (k *Keys) List(userID int64) ([]model.APIKey, error) {
	var rows []model.APIKey
	if err := k.db.Where("user_id = ?", userID).Order("id DESC").Find(&rows).Error; err != nil {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	return rows, nil
}

// Revoke 吊销密钥.
func (k *Keys) Revoke(userID int64, id int64) error {
	var row model.APIKey
	if err := k.db.Where("id = ? AND user_id = ?", id, userID).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return apperr.ErrNotFound.WithMessage("密钥不存在")
		}
		return apperr.ErrInternal.WithMessage(err.Error())
	}
	if row.RevokedAt != nil {
		return nil
	}
	now := nowUTC()
	if err := k.db.Model(&model.APIKey{}).Where("id = ?", row.ID).Update("revoked_at", now).Error; err != nil {
		return apperr.ErrInternal.WithMessage(err.Error())
	}
	return nil
}

// Authenticate 校验 Authorization 令牌并返回密钥记录.
func (k *Keys) Authenticate(token string) (*model.APIKey, error) {
	token = strings.TrimSpace(token)
	idx := strings.IndexByte(token, '.')
	if idx <= 0 || idx == len(token)-1 {
		return nil, apperr.ErrUnauthorized.WithMessage("API 密钥格式非法")
	}
	accessKey := token[:idx]
	secret := token[idx+1:]

	var row model.APIKey
	if err := k.db.Where("access_key = ?", accessKey).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperr.ErrUnauthorized
		}
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	if row.RevokedAt != nil {
		return nil, apperr.ErrUnauthorized.WithMessage("API 密钥已吊销")
	}
	if row.ExpiresAt != nil && row.ExpiresAt.Before(time.Now()) {
		return nil, apperr.ErrUnauthorized.WithMessage("API 密钥已过期")
	}
	if !hmac.Equal([]byte(k.hashSecret(secret)), []byte(row.SecretHash)) {
		return nil, apperr.ErrUnauthorized
	}

	now := nowUTC()
	// 同步更新最后使用时间: 避免每请求派生 goroutine 与单写连接争用.
	if err := k.db.Model(&model.APIKey{}).Where("id = ?", row.ID).Update("last_used_at", now).Error; err != nil {
		return nil, apperr.ErrInternal.WithMessage("更新密钥使用时间失败: " + err.Error())
	}
	row.LastUsedAt = &now
	return &row, nil
}

// hashSecret 用服务主密钥做 HMAC-SHA256, 便于逐请求快速校验.
func (k *Keys) hashSecret(secret string) string {
	mac := hmac.New(sha256.New, k.secretKey)
	mac.Write([]byte(secret))
	return hex.EncodeToString(mac.Sum(nil))
}

// randomAccessKey 生成公开访问键: pk_ + 28 位十六进制 (共 31 字符).
func randomAccessKey() (string, error) {
	raw := make([]byte, 14)
	if err := crypto.RandomBytes(raw); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s%s", accessKeyPrefix, hex.EncodeToString(raw)), nil
}
