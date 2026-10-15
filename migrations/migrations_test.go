package migrations

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/yeqingky/Picsky/internal/model"
)

func TestConflictPolicyMigrationMapsLegacyValuesToSkip(t *testing.T) {
	database, err := gorm.Open(sqlite.Open("file:conflict-policy-migration?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatalf("读取测试数据库连接失败: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	if err := database.AutoMigrate(&SchemaMigration{}, &model.StoragePolicy{}, &model.Setting{}); err != nil {
		t.Fatalf("准备旧版表结构失败: %v", err)
	}
	now := time.Now().UTC()
	policies := []model.StoragePolicy{
		{Name: "old-overwrite", Type: "webdav", Enabled: true, PathTemplate: "{seq}.jpg", ConflictPolicy: "overwrite", Config: "{}", CreatedAt: now, UpdatedAt: now},
		{Name: "old-fail", Type: "webdav", Enabled: true, PathTemplate: "{seq}.jpg", ConflictPolicy: "fail", Config: "{}", CreatedAt: now, UpdatedAt: now},
		{Name: "rename", Type: "webdav", Enabled: true, PathTemplate: "{seq}.jpg", ConflictPolicy: model.ConflictRename, Config: "{}", CreatedAt: now, UpdatedAt: now},
	}
	if err := database.Create(&policies).Error; err != nil {
		t.Fatalf("写入旧策略失败: %v", err)
	}
	settings := []model.Setting{
		{Key: "default_conflict_policy", Value: "fail", UpdatedAt: now},
		{Key: "unrelated", Value: "overwrite", UpdatedAt: now},
	}
	if err := database.Create(&settings).Error; err != nil {
		t.Fatalf("写入旧设置失败: %v", err)
	}
	if err := database.Create(&SchemaMigration{Version: 4, AppliedAt: now}).Error; err != nil {
		t.Fatalf("写入旧迁移版本失败: %v", err)
	}

	if err := Run(database); err != nil {
		t.Fatalf("升级迁移失败: %v", err)
	}

	for _, name := range []string{"old-overwrite", "old-fail"} {
		var policy model.StoragePolicy
		if err := database.Where("name = ?", name).First(&policy).Error; err != nil {
			t.Fatalf("读取策略 %q 失败: %v", name, err)
		}
		if policy.ConflictPolicy != model.ConflictSkip {
			t.Errorf("旧策略 %q 应迁移为 skip, 实际 %q", name, policy.ConflictPolicy)
		}
	}
	var unchanged model.StoragePolicy
	if err := database.Where("name = ?", "rename").First(&unchanged).Error; err != nil {
		t.Fatalf("读取 rename 策略失败: %v", err)
	}
	if unchanged.ConflictPolicy != model.ConflictRename {
		t.Errorf("rename 策略不应改变, 实际 %q", unchanged.ConflictPolicy)
	}

	var defaultPolicy, unrelated string
	if err := database.Model(&model.Setting{}).Where("key = ?", "default_conflict_policy").Pluck("value", &defaultPolicy).Error; err != nil {
		t.Fatalf("读取默认冲突策略失败: %v", err)
	}
	if defaultPolicy != model.ConflictSkip {
		t.Errorf("旧默认冲突策略应迁移为 skip, 实际 %q", defaultPolicy)
	}
	if err := database.Model(&model.Setting{}).Where("key = ?", "unrelated").Pluck("value", &unrelated).Error; err != nil {
		t.Fatalf("读取无关设置失败: %v", err)
	}
	if unrelated != "overwrite" {
		t.Errorf("无关设置不应改变, 实际 %q", unrelated)
	}

	var latest SchemaMigration
	if err := database.Order("version DESC").First(&latest).Error; err != nil {
		t.Fatalf("读取迁移版本失败: %v", err)
	}
	if latest.Version != 5 {
		t.Errorf("迁移版本应为 5, 实际 %d", latest.Version)
	}
}
