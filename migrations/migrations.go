// Package migrations 是数据库迁移层: 按版本顺序建表, 建索引并写入种子数据.
// 逻辑类型 (BOOLEAN / DATETIME / BIGINT / VARCHAR / TEXT) 由 GORM 方言映射到
// SQLite 与 PostgreSQL 的具体类型, 因此两种数据库共用同一套定义.
package migrations

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/yeqingky/Picsky/internal/model"
)

// schemaVersion 是当前迁移版本, 每次结构变更递增并追加一个迁移函数.
const schemaVersion = 13

// SchemaMigration 记录已应用的迁移版本.
type SchemaMigration struct {
	Version   int       `gorm:"primaryKey"`
	AppliedAt time.Time `gorm:"not null"`
}

// Run 应用全部未执行的迁移.
func Run(db *gorm.DB) error {
	if err := db.AutoMigrate(&SchemaMigration{}); err != nil {
		return fmt.Errorf("创建迁移记录表失败: %w", err)
	}

	var current SchemaMigration
	err := db.Order("version DESC").First(&current).Error
	switch {
	case err == gorm.ErrRecordNotFound:
		current.Version = 0
	case err != nil:
		return fmt.Errorf("读取迁移版本失败: %w", err)
	}

	for version := current.Version + 1; version <= schemaVersion; version++ {
		step, ok := steps[version]
		if !ok {
			return fmt.Errorf("缺少版本 %d 的迁移实现", version)
		}
		if err := db.Transaction(func(tx *gorm.DB) error {
			if err := step(tx); err != nil {
				return err
			}
			return tx.Create(&SchemaMigration{Version: version, AppliedAt: time.Now().UTC()}).Error
		}); err != nil {
			return fmt.Errorf("迁移到版本 %d 失败: %w", version, err)
		}
	}
	return nil
}

// steps 保存版本号到迁移实现的映射.
var steps = map[int]func(*gorm.DB) error{
	1: func(db *gorm.DB) error {
		if err := db.AutoMigrate(model.AllModels()...); err != nil {
			return err
		}
		return seed(db)
	},
	2: func(db *gorm.DB) error {
		// 下线缩略图规则: 早期版本为存储策略保留过可选列, 现已不再使用.
		// 不用 GORM 的 DropColumn: SQLite 驱动重建表时对末列会静默不生效 (返回 nil 但列仍在).
		if !db.Migrator().HasColumn(&model.StoragePolicy{}, "thumbnail_rule") {
			return nil
		}
		return db.Exec("ALTER TABLE storage_policies DROP COLUMN thumbnail_rule").Error
	},
	3: func(db *gorm.DB) error {
		// 内置用户组: 权限位固定, 不可删除, 不可设为默认组. 按名称标记初始化阶段写入的管理员与只读组.
		if err := db.AutoMigrate(&model.Group{}); err != nil {
			return err
		}
		if err := db.Model(&model.Group{}).Where("name IN ?", []string{"管理员", "只读"}).Update("is_system", true).Error; err != nil {
			return err
		}
		if err := db.Model(&model.Group{}).Where("is_system = ? AND is_default = ?", true, true).Update("is_default", false).Error; err != nil {
			return err
		}
		// 清除内置组的默认标记后可能没有任何默认组: 此时把编号最小的非内置组设为默认,
		// 避免注册流程退回 "编号最小的组" (通常是管理员组).
		var defaults int64
		if err := db.Model(&model.Group{}).Where("is_default = ?", true).Count(&defaults).Error; err != nil {
			return err
		}
		if defaults > 0 {
			return nil
		}
		var fallback model.Group
		switch err := db.Where("is_system = ?", false).Order("id ASC").First(&fallback).Error; {
		case errors.Is(err, gorm.ErrRecordNotFound):
			return nil
		case err != nil:
			return err
		}
		return db.Model(&model.Group{}).Where("id = ?", fallback.ID).Update("is_default", true).Error
	},
	4: func(db *gorm.DB) error {
		// 移除软删除与 CDN 刷新预留: 删除只作用于记录与存储后端, 历史软删记录物理清除.
		if db.Migrator().HasColumn(&model.Image{}, "deleted_at") {
			if err := db.Where("deleted_at IS NOT NULL").Delete(&model.Image{}).Error; err != nil {
				return err
			}
			if err := db.Exec("DROP INDEX IF EXISTS idx_images_deleted_at").Error; err != nil {
				return err
			}
		}
		// 不用 GORM 的 DropColumn: SQLite 驱动重建表时对末列会静默不生效 (返回 nil 但列仍在).
		for _, column := range []string{"object_deleted_at", "deleted_at"} {
			if !db.Migrator().HasColumn(&model.Image{}, column) {
				continue
			}
			if err := db.Exec("ALTER TABLE images DROP COLUMN " + column).Error; err != nil {
				return err
			}
		}
		if err := db.Where("key = ?", "record_retention_days").Delete(&model.Setting{}).Error; err != nil {
			return err
		}
		// 清理已下线的任务类型: 软删记录清理与 CDN 刷新.
		return db.Where("type IN ?", []string{"record_purge", "cdn_purge"}).Delete(&model.MaintenanceTask{}).Error
	},
	5: func(db *gorm.DB) error {
		// 旧冲突策略不再支持; 跳过冲突可避免旧覆盖策略继续改写外部对象.
		if err := db.Model(&model.StoragePolicy{}).
			Where("conflict_policy IN ?", []string{"overwrite", "fail"}).
			Update("conflict_policy", model.ConflictSkip).Error; err != nil {
			return err
		}
		return db.Model(&model.Setting{}).
			Where("key = ? AND value IN ?", "default_conflict_policy", []string{"overwrite", "fail"}).
			Update("value", model.ConflictSkip).Error
	},
	6: func(db *gorm.DB) error {
		if db.Migrator().HasColumn(&model.User{}, "SessionVersion") {
			return nil
		}
		return db.Migrator().AddColumn(&model.User{}, "SessionVersion")
	},
	7: normalizeUserEmails,
	8: migrateAccountStates,
	9: func(db *gorm.DB) error {
		return db.AutoMigrate(&model.User{})
	},
	10: func(db *gorm.DB) error {
		return db.AutoMigrate(&model.MaintenanceTask{}, &model.PolicyScanManifest{})
	},
	11: func(db *gorm.DB) error {
		// 策略序号分配器: 每个含 {seq} 的策略一条原子递增计数器.
		// 旧库可能已有两列计数器, 保留原表由版本 12 转换, 避免向非空表追加 NOT NULL 列.
		if !db.Migrator().HasTable(&model.PolicySequence{}) {
			if err := db.AutoMigrate(&model.PolicySequence{}); err != nil {
				return err
			}
		}
		// {seq} 只支持 rename; 旧数据中的 skip 组合统一改为 rename.
		if !db.Migrator().HasTable(&model.StoragePolicy{}) {
			return nil
		}
		return db.Model(&model.StoragePolicy{}).
			Where("path_template LIKE ? AND conflict_policy <> ?", "%{seq}%", model.ConflictRename).
			Update("conflict_policy", model.ConflictRename).Error
	},
	12: func(db *gorm.DB) error {
		// 旧计数器只有 policy_id 与 next_value; 转为模板快照 + 已分配最大序号 + 释放队列.
		if db.Migrator().HasTable(&model.PolicySequence{}) && db.Migrator().HasColumn(&model.PolicySequence{}, "next_value") {
			type legacySequence struct {
				PolicyID  int64
				NextValue int64
			}
			var rows []legacySequence
			if err := db.Table("policy_sequences").Select("policy_id, next_value").Scan(&rows).Error; err != nil {
				return err
			}
			if err := db.Exec("DROP TABLE policy_sequences").Error; err != nil {
				return err
			}
			if err := db.AutoMigrate(&model.PolicySequence{}); err != nil {
				return err
			}
			for _, row := range rows {
				var policy model.StoragePolicy
				if err := db.First(&policy, row.PolicyID).Error; err != nil {
					if errors.Is(err, gorm.ErrRecordNotFound) {
						continue
					}
					return err
				}
				if !strings.Contains(policy.PathTemplate, "{seq}") {
					continue
				}
				allocated := row.NextValue - 1
				if allocated < 0 {
					allocated = 0
				}
				sequence := model.PolicySequence{
					PolicyID:     policy.ID,
					PathTemplate: policy.PathTemplate,
					Allocated:    allocated,
					Released:     "[]",
				}
				if err := db.Create(&sequence).Error; err != nil {
					return err
				}
			}
		} else if err := db.AutoMigrate(&model.PolicySequence{}); err != nil {
			return err
		}
		return db.AutoMigrate(&model.StoragePolicy{})
	},
	13: func(db *gorm.DB) error {
		// 存储策略提示文字: 显示在上传页策略选择器旁.
		return db.AutoMigrate(&model.StoragePolicy{})
	},
}

func normalizeUserEmails(db *gorm.DB) error {
	type userEmail struct {
		ID    int64
		Email string
	}
	var users []userEmail
	if err := db.Model(&model.User{}).Select("id", "email").Order("id ASC").Find(&users).Error; err != nil {
		return fmt.Errorf("读取用户邮箱失败: %w", err)
	}

	owners := make(map[string]userEmail, len(users))
	for _, user := range users {
		normalized := strings.ToLower(strings.TrimSpace(user.Email))
		if previous, exists := owners[normalized]; exists {
			return fmt.Errorf("邮箱小写规范化冲突: users.id=%d 与 users.id=%d", previous.ID, user.ID)
		}
		owners[normalized] = user
	}

	for _, user := range users {
		normalized := strings.ToLower(strings.TrimSpace(user.Email))
		if normalized == user.Email {
			continue
		}
		if err := db.Model(&model.User{}).Where("id = ?", user.ID).UpdateColumn("email", normalized).Error; err != nil {
			return fmt.Errorf("规范化用户邮箱失败 (users.id=%d): %w", user.ID, err)
		}
	}
	return nil
}

func migrateAccountStates(db *gorm.DB) error {
	if err := db.AutoMigrate(&model.User{}); err != nil {
		return fmt.Errorf("更新用户状态字段失败: %w", err)
	}

	verificationEnabled := false
	if db.Migrator().HasTable(&model.Setting{}) {
		var setting model.Setting
		err := db.Select("value").Where("key = ?", "email_verification_enabled").First(&setting).Error
		if err != nil && err != gorm.ErrRecordNotFound {
			return fmt.Errorf("读取邮箱验证设置失败: %w", err)
		}
		verificationEnabled = err == nil && strings.EqualFold(strings.TrimSpace(setting.Value), "true")
	}

	query := db.Model(&model.User{}).Where("status = ?", int16(1))
	if verificationEnabled {
		query = query.Or("status = ? AND email_verified_at IS NULL", model.UserStatusNormal)
	}
	var users []model.User
	if err := query.Order("id ASC").Find(&users).Error; err != nil {
		return fmt.Errorf("读取待迁移用户状态失败: %w", err)
	}
	if len(users) == 0 {
		return nil
	}

	var readOnly model.Group
	if err := db.Where("is_system = ? AND name = ?", true, "只读").First(&readOnly).Error; err != nil {
		return fmt.Errorf("读取只读用户组失败: %w", err)
	}
	var defaultGroup model.Group
	defaultGroupLoaded := false
	for _, user := range users {
		status := model.UserStatusBanned
		if user.Status == model.UserStatusNormal {
			status = model.UserStatusUnverified
		}

		restoreGroupID := user.GroupID
		if user.RestoreGroupID != nil {
			restoreGroupID = *user.RestoreGroupID
		}
		if restoreGroupID == readOnly.ID {
			if !defaultGroupLoaded {
				if err := db.Where("is_default = ? AND is_system = ?", true, false).First(&defaultGroup).Error; err != nil {
					return fmt.Errorf("读取默认用户组失败: %w", err)
				}
				defaultGroupLoaded = true
			}
			restoreGroupID = defaultGroup.ID
		}

		updates := map[string]any{
			"status":           status,
			"group_id":         readOnly.ID,
			"restore_group_id": restoreGroupID,
		}
		if status == model.UserStatusBanned {
			updates["session_version"] = gorm.Expr("session_version + 1")
		}
		if err := db.Model(&model.User{}).Where("id = ?", user.ID).Updates(updates).Error; err != nil {
			return fmt.Errorf("迁移用户状态失败 (users.id=%d): %w", user.ID, err)
		}
	}
	return nil
}

// defaultUploadRateLimit 是初始化阶段写入默认用户组的上传速率 (次/分钟):
// 该限额原先由系统设置承载, 现在随用户组参数一起落库.
const defaultUploadRateLimit = 60

// seed 写入默认用户组与系统设置默认值.
func seed(db *gorm.DB) error {
	admin := model.Group{
		Name:            "管理员",
		Description:     "全部权限",
		IsSystem:        true,
		Permissions:     model.AllPermissions(),
		UploadRateLimit: new(defaultUploadRateLimit),
		Sort:            0,
	}
	normal := model.Group{
		Name:            "普通用户",
		Description:     "常规用户",
		IsDefault:       true,
		Permissions:     model.PermUpload | model.PermDeleteOwn | model.PermManageKeys,
		UploadRateLimit: new(defaultUploadRateLimit),
		Sort:            1,
	}
	readonly := model.Group{
		Name:        "只读",
		Description: "只读用户",
		IsSystem:    true,
		Permissions: 0,
		Sort:        2,
	}
	for _, group := range []*model.Group{&admin, &normal, &readonly} {
		var count int64
		if err := db.Model(&model.Group{}).Where("name = ?", group.Name).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			if err := db.Create(group).Error; err != nil {
				return err
			}
		}
	}

	now := time.Now().UTC()
	for key, value := range DefaultSettings {
		var existing model.Setting
		err := db.Where("key = ?", key).First(&existing).Error
		if err == nil {
			continue
		}
		if err != gorm.ErrRecordNotFound {
			return err
		}
		setting := model.Setting{Key: key, Value: value, UpdatedAt: now}
		if key == "default_group_id" {
			setting.Value = fmt.Sprintf("%d", normal.ID)
		}
		if err := db.Create(&setting).Error; err != nil {
			return err
		}
	}
	return nil
}
