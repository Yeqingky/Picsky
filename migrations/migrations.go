// Package migrations 是数据库迁移层: 按版本顺序建表, 建索引并写入种子数据.
// 逻辑类型 (BOOLEAN / DATETIME / BIGINT / VARCHAR / TEXT) 由 GORM 方言映射到
// SQLite 与 PostgreSQL 的具体类型, 因此两种数据库共用同一套定义.
package migrations

import (
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/yeqingky/Picsky/internal/model"
)

// schemaVersion 是当前迁移版本, 每次结构变更递增并追加一个迁移函数.
const schemaVersion = 6

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
		return db.AutoMigrate(&model.User{})
	},
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
