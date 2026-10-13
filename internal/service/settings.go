// Package service 实现业务逻辑: 认证, 上传, 图库, 策略, 用户组, 设置与维护任务.
package service

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/yeqingky/Picsky/internal/apperr"
	"github.com/yeqingky/Picsky/internal/crypto"
	"github.com/yeqingky/Picsky/internal/model"
	"github.com/yeqingky/Picsky/migrations"
)

// Settings 提供系统设置的读写与缓存; 秘密字段以密文入库.
type Settings struct {
	db     *gorm.DB
	cipher *crypto.Cipher

	mu     sync.RWMutex
	values map[string]string
}

// NewSettings 加载全部设置.
func NewSettings(database *gorm.DB, cipher *crypto.Cipher) (*Settings, error) {
	settings := &Settings{db: database, cipher: cipher, values: map[string]string{}}
	if err := settings.Reload(); err != nil {
		return nil, err
	}
	return settings, nil
}

// Reload 从数据库重新加载设置.
func (s *Settings) Reload() error {
	var rows []model.Setting
	if err := s.db.Find(&rows).Error; err != nil {
		return fmt.Errorf("读取系统设置失败: %w", err)
	}
	values := make(map[string]string, len(migrations.DefaultSettings))
	for key, value := range migrations.DefaultSettings {
		values[key] = value
	}
	for _, row := range rows {
		values[row.Key] = row.Value
	}
	s.mu.Lock()
	s.values = values
	s.mu.Unlock()
	return nil
}

// String 返回字符串设置值.
func (s *Settings) String(key string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.values[key]
}

// Bool 返回布尔设置值.
func (s *Settings) Bool(key string, def bool) bool {
	raw := strings.TrimSpace(s.String(key))
	if raw == "" {
		if value, ok := migrations.DefaultSettings[key]; ok {
			raw = strings.TrimSpace(value)
		}
	}
	if raw == "" {
		return def
	}
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return def
	}
	return parsed
}

// Int64 返回整数设置值.
func (s *Settings) Int64(key string, def int64) int64 {
	raw := strings.TrimSpace(s.String(key))
	if raw == "" {
		return def
	}
	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return def
	}
	return parsed
}

// OptionalInt64 返回可空整数设置值: 空串返回 nil.
func (s *Settings) OptionalInt64(key string) *int64 {
	raw := strings.TrimSpace(s.String(key))
	if raw == "" {
		return nil
	}
	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return nil
	}
	return &parsed
}

// Secret 解密并返回秘密设置值.
func (s *Settings) Secret(key string) (string, error) {
	return s.cipher.Decrypt(s.String(key))
}

// HasSecret 判断秘密设置是否已配置.
func (s *Settings) HasSecret(key string) bool {
	return strings.TrimSpace(s.String(key)) != ""
}

// VisibleValues 返回可下发的设置值: 秘密字段以空串呈现.
func (s *Settings) VisibleValues() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]string, len(s.values))
	for key, value := range s.values {
		if migrations.IsSecretSetting(key) {
			out[key] = ""
			continue
		}
		out[key] = value
	}
	return out
}

// Definitions 返回设置项的展示元数据.
func (s *Settings) Definitions() []migrations.SettingDefinition {
	return migrations.SettingDefinitions
}

// Update 批量写入设置: 秘密字段自动加密, 空串表示清空.
func (s *Settings) Update(values map[string]string) error {
	if len(values) == 0 {
		return nil
	}
	prepared := make(map[string]string, len(values))
	for key, value := range values {
		if _, known := migrations.DefaultSettings[key]; !known {
			return apperr.ErrInvalidRequest.WithMessage("未知设置项: " + key)
		}
		if migrations.IsSecretSetting(key) {
			encrypted, err := s.cipher.Encrypt(strings.TrimSpace(value))
			if err != nil {
				return fmt.Errorf("加密设置项 %s 失败: %w", key, err)
			}
			prepared[key] = encrypted
			continue
		}
		prepared[key] = value
	}
	now := nowUTC()
	rows := make([]model.Setting, 0, len(prepared))
	for key, value := range prepared {
		rows = append(rows, model.Setting{Key: key, Value: value, UpdatedAt: now})
	}
	if err := s.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"}),
	}).Create(&rows).Error; err != nil {
		return fmt.Errorf("写入系统设置失败: %w", err)
	}
	return s.Reload()
}

// Set 写入单个非秘密设置项.
func (s *Settings) Set(key, value string) error {
	return s.Update(map[string]string{key: value})
}
