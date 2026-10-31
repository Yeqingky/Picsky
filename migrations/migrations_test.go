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

	if err := database.AutoMigrate(&SchemaMigration{}, &model.StoragePolicy{}, &model.Setting{}, &model.User{}); err != nil {
		t.Fatalf("准备旧版表结构失败: %v", err)
	}
	now := time.Now().UTC()
	policies := []model.StoragePolicy{
		{Name: "old-overwrite", Type: "webdav", Enabled: true, PathTemplate: "{md5}.jpg", ConflictPolicy: "overwrite", Config: "{}", CreatedAt: now, UpdatedAt: now},
		{Name: "old-fail", Type: "webdav", Enabled: true, PathTemplate: "{md5}.jpg", ConflictPolicy: "fail", Config: "{}", CreatedAt: now, UpdatedAt: now},
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
	if latest.Version != schemaVersion {
		t.Errorf("迁移版本应为 %d, 实际 %d", schemaVersion, latest.Version)
	}
	if !database.Migrator().HasTable(&model.PolicyScanManifest{}) {
		t.Fatal("迁移未创建 policy_scan_manifests 表")
	}
	if !database.Migrator().HasColumn(&model.MaintenanceTask{}, "result") {
		t.Fatal("迁移未创建 maintenance_tasks.result 列")
	}
}

func TestAccountStateMigrationRestrictsLegacyAndPendingUsers(t *testing.T) {
	database := newEmailNormalizationTestDB(t, "account-state-migration")
	if err := database.AutoMigrate(&SchemaMigration{}, &model.Group{}, &model.Setting{}, &userBeforeRestoreGroup{}); err != nil {
		t.Fatalf("准备旧版表结构失败: %v", err)
	}
	now := time.Now().UTC()
	groups := []model.Group{
		{Name: "普通用户", IsDefault: true, Permissions: model.PermUpload, CreatedAt: now, UpdatedAt: now},
		{Name: "只读", IsSystem: true, Permissions: 0, CreatedAt: now, UpdatedAt: now},
	}
	if err := database.Create(&groups).Error; err != nil {
		t.Fatalf("创建测试用户组失败: %v", err)
	}
	verifiedAt := now
	users := []userBeforeRestoreGroup{
		{Username: "legacy-banned", Email: "legacy-banned@example.com", PasswordHash: "hash", EmailVerifiedAt: &verifiedAt, GroupID: groups[0].ID, Status: 1, SessionVersion: 4, CreatedAt: now, UpdatedAt: now},
		{Username: "pending", Email: "pending@example.com", PasswordHash: "hash", GroupID: groups[0].ID, Status: 0, SessionVersion: 4, CreatedAt: now, UpdatedAt: now},
		{Username: "verified", Email: "verified@example.com", PasswordHash: "hash", EmailVerifiedAt: &verifiedAt, GroupID: groups[0].ID, Status: 0, SessionVersion: 4, CreatedAt: now, UpdatedAt: now},
	}
	if err := database.Create(&users).Error; err != nil {
		t.Fatalf("写入旧版用户失败: %v", err)
	}
	if err := database.Create(&model.Setting{Key: "email_verification_enabled", Value: "true", UpdatedAt: now}).Error; err != nil {
		t.Fatalf("开启邮箱验证设置失败: %v", err)
	}
	if err := database.Create(&SchemaMigration{Version: 7, AppliedAt: now}).Error; err != nil {
		t.Fatalf("写入旧迁移版本失败: %v", err)
	}

	if err := Run(database); err != nil {
		t.Fatalf("升级账号状态迁移失败: %v", err)
	}

	var legacy, pending, verified model.User
	for _, item := range []struct {
		id  int64
		out *model.User
	}{{users[0].ID, &legacy}, {users[1].ID, &pending}, {users[2].ID, &verified}} {
		if err := database.First(item.out, item.id).Error; err != nil {
			t.Fatalf("读取迁移用户失败: %v", err)
		}
	}
	if legacy.Status != model.UserStatusBanned || legacy.GroupID != groups[1].ID || legacy.RestoreGroupID == nil || *legacy.RestoreGroupID != groups[0].ID || legacy.SessionVersion != 5 {
		t.Errorf("旧禁用账号应封禁, 移入只读组并吊销旧会话: %+v", legacy)
	}
	if pending.Status != model.UserStatusUnverified || pending.GroupID != groups[1].ID || pending.RestoreGroupID == nil || *pending.RestoreGroupID != groups[0].ID || pending.SessionVersion != 4 {
		t.Errorf("待验证账号应移入只读组并保留原组: %+v", pending)
	}
	if verified.Status != model.UserStatusNormal || verified.GroupID != groups[0].ID || verified.RestoreGroupID != nil {
		t.Errorf("已验证账号状态不应改变: %+v", verified)
	}
}

func TestSessionVersionMigrationInitializesExistingUsers(t *testing.T) {
	database, err := gorm.Open(sqlite.Open("file:session-version-migration?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatalf("读取测试数据库连接失败: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	if err := database.AutoMigrate(&SchemaMigration{}, &userBeforeSessionVersion{}); err != nil {
		t.Fatalf("准备旧版用户表失败: %v", err)
	}
	now := time.Now().UTC()
	legacy := userBeforeSessionVersion{
		Username:     "legacy",
		Email:        "legacy@example.com",
		PasswordHash: "hash",
		GroupID:      1,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := database.Create(&legacy).Error; err != nil {
		t.Fatalf("写入旧版用户失败: %v", err)
	}
	if err := database.Create(&SchemaMigration{Version: 5, AppliedAt: now}).Error; err != nil {
		t.Fatalf("写入旧迁移版本失败: %v", err)
	}

	if err := Run(database); err != nil {
		t.Fatalf("升级迁移失败: %v", err)
	}
	var upgraded model.User
	if err := database.First(&upgraded, legacy.ID).Error; err != nil {
		t.Fatalf("读取升级后的用户失败: %v", err)
	}
	if upgraded.SessionVersion != 1 {
		t.Errorf("旧用户的会话版本应初始化为 1, 实际 %d", upgraded.SessionVersion)
	}
}

func TestEmailNormalizationMigrationLowercasesLegacyAddresses(t *testing.T) {
	database := newEmailNormalizationTestDB(t, "email-normalization-lowercase")
	if err := database.AutoMigrate(&SchemaMigration{}, &model.User{}); err != nil {
		t.Fatalf("准备迁移表失败: %v", err)
	}
	now := time.Now().UTC()
	user := model.User{
		Username:     "legacy",
		Email:        "  Legacy@Example.COM ",
		PasswordHash: "hash",
		GroupID:      1,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := database.Create(&user).Error; err != nil {
		t.Fatalf("写入旧用户失败: %v", err)
	}
	if err := database.Create(&SchemaMigration{Version: 6, AppliedAt: now}).Error; err != nil {
		t.Fatalf("写入迁移版本失败: %v", err)
	}

	if err := Run(database); err != nil {
		t.Fatalf("规范化旧邮箱失败: %v", err)
	}
	var upgraded model.User
	if err := database.First(&upgraded, user.ID).Error; err != nil {
		t.Fatalf("读取升级用户失败: %v", err)
	}
	if upgraded.Email != "legacy@example.com" {
		t.Fatalf("迁移后邮箱应为小写且去除首尾空格, 实际 %q", upgraded.Email)
	}
}

func TestEmailNormalizationMigrationRejectsCaseCollisions(t *testing.T) {
	database := newEmailNormalizationTestDB(t, "email-normalization-collision")
	if err := database.AutoMigrate(&SchemaMigration{}, &model.User{}); err != nil {
		t.Fatalf("准备迁移表失败: %v", err)
	}
	now := time.Now().UTC()
	users := []model.User{
		{Username: "first", Email: "User@Example.com", PasswordHash: "hash", GroupID: 1, CreatedAt: now, UpdatedAt: now},
		{Username: "second", Email: "user@example.COM", PasswordHash: "hash", GroupID: 1, CreatedAt: now, UpdatedAt: now},
	}
	if err := database.Create(&users).Error; err != nil {
		t.Fatalf("写入旧用户失败: %v", err)
	}
	if err := database.Create(&SchemaMigration{Version: 6, AppliedAt: now}).Error; err != nil {
		t.Fatalf("写入迁移版本失败: %v", err)
	}

	if err := Run(database); err == nil {
		t.Fatal("大小写邮箱冲突应阻止迁移")
	}
	var stored []model.User
	if err := database.Order("id ASC").Find(&stored).Error; err != nil {
		t.Fatalf("读取冲突用户失败: %v", err)
	}
	if stored[0].Email != "User@Example.com" || stored[1].Email != "user@example.COM" {
		t.Fatalf("冲突迁移不应部分改写邮箱: %q, %q", stored[0].Email, stored[1].Email)
	}
	var latest SchemaMigration
	if err := database.Order("version DESC").First(&latest).Error; err != nil {
		t.Fatalf("读取最新迁移版本失败: %v", err)
	}
	if latest.Version != 6 {
		t.Fatalf("冲突迁移不应记录版本, 实际版本 %d", latest.Version)
	}
}

func newEmailNormalizationTestDB(t *testing.T, name string) *gorm.DB {
	t.Helper()
	database, err := gorm.Open(sqlite.Open("file:"+name+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatalf("读取测试数据库连接失败: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return database
}

type userBeforeSessionVersion struct {
	ID           int64     `gorm:"primaryKey;autoIncrement"`
	Username     string    `gorm:"size:64;not null;uniqueIndex"`
	Email        string    `gorm:"size:255;not null;uniqueIndex"`
	PasswordHash string    `gorm:"size:255;not null"`
	GroupID      int64     `gorm:"not null;index"`
	CreatedAt    time.Time `gorm:"not null"`
	UpdatedAt    time.Time `gorm:"not null"`
}

func (userBeforeSessionVersion) TableName() string { return "users" }

type userBeforeRestoreGroup struct {
	ID              int64  `gorm:"primaryKey;autoIncrement"`
	Username        string `gorm:"size:64;not null;uniqueIndex"`
	Email           string `gorm:"size:255;not null;uniqueIndex"`
	PasswordHash    string `gorm:"size:255;not null"`
	EmailVerifiedAt *time.Time
	Nickname        string `gorm:"size:64"`
	GroupID         int64  `gorm:"not null;index"`
	DefaultPolicyID *int64
	QuotaMode       *string `gorm:"size:8"`
	QuotaValue      *int64
	UsedImages      int64  `gorm:"not null;default:0"`
	UsedBytes       int64  `gorm:"not null;default:0"`
	Status          int16  `gorm:"not null;default:0"`
	SessionVersion  int64  `gorm:"not null;default:1"`
	TOTPSecret      string `gorm:"size:255"`
	LastLoginAt     *time.Time
	LastLoginIP     string    `gorm:"size:45"`
	CreatedAt       time.Time `gorm:"not null"`
	UpdatedAt       time.Time `gorm:"not null"`
}

func (userBeforeRestoreGroup) TableName() string { return "users" }

func TestSequenceCounterMigrationConvertsLegacyRows(t *testing.T) {
	database, err := gorm.Open(sqlite.Open("file:sequence-counter-migration?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatalf("读取测试数据库连接失败: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	if err := database.AutoMigrate(&SchemaMigration{}, &legacyStoragePolicy{}); err != nil {
		t.Fatalf("准备旧版表结构失败: %v", err)
	}
	now := time.Now().UTC()
	policies := []legacyStoragePolicy{
		{Name: "seq", Type: "webdav", Enabled: true, PathTemplate: "img/{seq}.{ext}", ConflictPolicy: model.ConflictRename, Config: "{}", CreatedAt: now, UpdatedAt: now},
		{Name: "hash", Type: "webdav", Enabled: true, PathTemplate: "{md5}.{ext}", ConflictPolicy: model.ConflictRename, Config: "{}", CreatedAt: now, UpdatedAt: now},
	}
	if err := database.Create(&policies).Error; err != nil {
		t.Fatalf("写入旧策略失败: %v", err)
	}
	if err := database.Exec("CREATE TABLE policy_sequences (policy_id integer PRIMARY KEY, next_value integer NOT NULL)").Error; err != nil {
		t.Fatalf("创建旧计数器表失败: %v", err)
	}
	legacyRows := []struct {
		PolicyID  int64
		NextValue int64
	}{
		{PolicyID: policies[0].ID, NextValue: 42},
		{PolicyID: policies[1].ID, NextValue: 9},
		{PolicyID: 999, NextValue: 7},
	}
	for _, row := range legacyRows {
		if err := database.Exec("INSERT INTO policy_sequences (policy_id, next_value) VALUES (?, ?)", row.PolicyID, row.NextValue).Error; err != nil {
			t.Fatalf("写入旧计数器失败: %v", err)
		}
	}
	if err := database.Create(&SchemaMigration{Version: 11, AppliedAt: now}).Error; err != nil {
		t.Fatalf("写入旧迁移版本失败: %v", err)
	}

	if err := Run(database); err != nil {
		t.Fatalf("升级迁移失败: %v", err)
	}
	for _, column := range []string{"path_template", "allocated", "released"} {
		if !database.Migrator().HasColumn(&model.PolicySequence{}, column) {
			t.Errorf("新计数器表缺少列 %q", column)
		}
	}
	if database.Migrator().HasColumn(&model.PolicySequence{}, "next_value") {
		t.Error("旧 next_value 列应被移除")
	}

	var sequence model.PolicySequence
	if err := database.Where("policy_id = ?", policies[0].ID).First(&sequence).Error; err != nil {
		t.Fatalf("读取转换后的计数器失败: %v", err)
	}
	if sequence.PathTemplate != "img/{seq}.{ext}" || sequence.Allocated != 41 || sequence.Released != "[]" {
		t.Errorf("计数器转换不正确: %+v", sequence)
	}

	for _, id := range []int64{policies[1].ID, 999} {
		var count int64
		if err := database.Model(&model.PolicySequence{}).Where("policy_id = ?", id).Count(&count).Error; err != nil {
			t.Fatalf("统计计数器失败: %v", err)
		}
		if count != 0 {
			t.Errorf("策略 %d 的旧计数器应被丢弃", id)
		}
	}
	if !database.Migrator().HasColumn(&model.StoragePolicy{}, "reuse_released_seq") {
		t.Error("存储策略缺少 reuse_released_seq 列")
	}
}

type legacyStoragePolicy struct {
	ID             int64     `gorm:"primaryKey;autoIncrement"`
	Name           string    `gorm:"size:64;not null;uniqueIndex"`
	Type           string    `gorm:"size:32;not null"`
	Enabled        bool      `gorm:"not null"`
	PathTemplate   string    `gorm:"size:255;not null"`
	ConflictPolicy string    `gorm:"size:16;not null;default:'rename'"`
	Config         string    `gorm:"type:text;not null;default:'{}'"`
	Secrets        string    `gorm:"type:text"`
	Sort           int       `gorm:"not null;default:0"`
	CreatedAt      time.Time `gorm:"not null"`
	UpdatedAt      time.Time `gorm:"not null"`
}

func (legacyStoragePolicy) TableName() string { return "storage_policies" }

func TestSequencePolicyMigrationCreatesCounterAndForcesRename(t *testing.T) {
	database, err := gorm.Open(sqlite.Open("file:sequence-policy-migration?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatalf("读取测试数据库连接失败: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	if err := database.AutoMigrate(&SchemaMigration{}, &model.StoragePolicy{}); err != nil {
		t.Fatalf("准备旧版表结构失败: %v", err)
	}
	now := time.Now().UTC()
	policies := []model.StoragePolicy{
		{Name: "seq-skip", Type: "webdav", Enabled: true, PathTemplate: "{seq}.{ext}", ConflictPolicy: model.ConflictSkip, Config: "{}", CreatedAt: now, UpdatedAt: now},
		{Name: "hash-skip", Type: "webdav", Enabled: true, PathTemplate: "{md5}.{ext}", ConflictPolicy: model.ConflictSkip, Config: "{}", CreatedAt: now, UpdatedAt: now},
	}
	if err := database.Create(&policies).Error; err != nil {
		t.Fatalf("写入旧策略失败: %v", err)
	}
	if err := database.Create(&SchemaMigration{Version: 10, AppliedAt: now}).Error; err != nil {
		t.Fatalf("写入旧迁移版本失败: %v", err)
	}

	if err := Run(database); err != nil {
		t.Fatalf("升级迁移失败: %v", err)
	}
	if !database.Migrator().HasTable(&model.PolicySequence{}) {
		t.Fatal("应创建 policy_sequences 表")
	}
	var seqPolicy, hashPolicy model.StoragePolicy
	if err := database.Where("name = ?", "seq-skip").First(&seqPolicy).Error; err != nil {
		t.Fatalf("读取 {seq} 策略失败: %v", err)
	}
	if seqPolicy.ConflictPolicy != model.ConflictRename {
		t.Errorf("{seq} 策略应迁移为 rename, 实际 %q", seqPolicy.ConflictPolicy)
	}
	if err := database.Where("name = ?", "hash-skip").First(&hashPolicy).Error; err != nil {
		t.Fatalf("读取哈希策略失败: %v", err)
	}
	if hashPolicy.ConflictPolicy != model.ConflictSkip {
		t.Errorf("哈希策略不应改变, 实际 %q", hashPolicy.ConflictPolicy)
	}
}
