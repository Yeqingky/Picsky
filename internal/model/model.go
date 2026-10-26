// Package model 定义数据模型; 逻辑类型由迁移层映射到具体数据库类型.
package model

import "time"

// 用户状态.
const (
	UserStatusNormal     int16 = 0
	UserStatusUnverified int16 = 1
	UserStatusBanned     int16 = 2
)

// 配额方式.
const (
	QuotaModeCount = "count"
	QuotaModeBytes = "bytes"
)

// 图片记录类型.
const (
	KindImage = "image"
	KindFile  = "file"
)

// 图片来源.
const (
	SourceWeb  = "web"
	SourceAPI  = "api"
	SourceScan = "scan"
)

// 冲突策略.
const (
	ConflictRename = "rename"
	ConflictSkip   = "skip"
)

// 维护任务类型.
const (
	TaskObjectDelete   = "object_delete"
	TaskLogPurge       = "log_purge"
	TaskPolicyScan     = "policy_scan"
	TaskQuotaReconcile = "quota_reconcile"
)

// 维护任务状态.
const (
	TaskPending = "pending"
	TaskRunning = "running"
	TaskDone    = "done"
	TaskFailed  = "failed"
)

// Group 是用户组: 权限位, 配额与策略白名单的载体.
type Group struct {
	ID               int64      `gorm:"primaryKey;autoIncrement" json:"id"`
	Name             string     `gorm:"size:64;not null;uniqueIndex" json:"name"`
	Description      string     `gorm:"size:255" json:"description"`
	IsDefault        bool       `gorm:"not null;default:false" json:"is_default"`
	IsSystem         bool       `gorm:"not null;default:false" json:"is_system"` // 内置组: 权限位固定, 不可删除, 不可设为默认组
	Permissions      Permission `gorm:"not null;default:0" json:"-"`
	QuotaMode        *string    `gorm:"size:8" json:"quota_mode"`
	QuotaValue       *int64     `json:"quota_value"`
	MaxFileSizeBytes *int64     `json:"max_file_size_bytes"`
	UploadRateLimit  *int       `json:"upload_rate_limit"`
	Sort             int        `gorm:"not null;default:0" json:"sort"`
	CreatedAt        time.Time  `gorm:"not null" json:"created_at"`
	UpdatedAt        time.Time  `gorm:"not null" json:"updated_at"`
}

// GroupPolicy 是用户组与存储策略的绑定 (白名单).
type GroupPolicy struct {
	GroupID  int64 `gorm:"primaryKey" json:"group_id"`
	PolicyID int64 `gorm:"primaryKey" json:"policy_id"`
}

// StoragePolicy 是一条完整的存储策略: 后端, 前缀, 魔法路径, 冲突策略.
type StoragePolicy struct {
	ID             int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	Name           string    `gorm:"size:64;not null;uniqueIndex" json:"name"`
	Type           string    `gorm:"size:32;not null" json:"type"`
	Enabled        bool      `gorm:"not null" json:"enabled"`
	PathTemplate   string    `gorm:"size:255;not null" json:"path_template"`
	ConflictPolicy string    `gorm:"size:16;not null;default:'rename'" json:"conflict_policy"`
	Config         string    `gorm:"type:text;not null;default:'{}'" json:"config"`
	Secrets        string    `gorm:"type:text" json:"-"`
	Sort           int       `gorm:"not null;default:0" json:"sort"`
	CreatedAt      time.Time `gorm:"not null" json:"created_at"`
	UpdatedAt      time.Time `gorm:"not null" json:"updated_at"`
}

// PolicyURL 是策略的分发域名别名.
type PolicyURL struct {
	ID        int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	PolicyID  int64     `gorm:"not null;uniqueIndex:idx_policy_urls_policy_url,priority:1" json:"policy_id"`
	Alias     string    `gorm:"size:64;not null" json:"alias"`
	URL       string    `gorm:"size:255;not null;uniqueIndex:idx_policy_urls_policy_url,priority:2" json:"url"`
	IsDefault bool      `gorm:"not null;default:false" json:"is_default"`
	Sort      int       `gorm:"not null;default:0" json:"sort"`
	CreatedAt time.Time `gorm:"not null" json:"created_at"`
}

// User 是用户.
type User struct {
	ID              int64      `gorm:"primaryKey;autoIncrement" json:"id"`
	Username        string     `gorm:"size:64;not null;uniqueIndex" json:"username"`
	Email           string     `gorm:"size:255;not null;uniqueIndex" json:"email"`
	PasswordHash    string     `gorm:"size:255;not null" json:"-"`
	EmailVerifiedAt *time.Time `json:"email_verified_at"`
	Nickname        string     `gorm:"size:64" json:"nickname"`
	GroupID         int64      `gorm:"not null;index" json:"group_id"`
	RestoreGroupID  *int64     `gorm:"index" json:"-"`
	DefaultPolicyID *int64     `json:"default_policy_id"`
	QuotaMode       *string    `gorm:"size:8" json:"quota_mode"`
	QuotaValue      *int64     `json:"quota_value"`
	UsedImages      int64      `gorm:"not null;default:0" json:"used_images"`
	UsedBytes       int64      `gorm:"not null;default:0" json:"used_bytes"`
	Status          int16      `gorm:"not null;default:0" json:"status"`
	SessionVersion  int64      `gorm:"not null;default:1" json:"-"`
	TOTPSecret      string     `gorm:"size:255" json:"-"`
	TOTPLastCounter int64      `gorm:"not null;default:0" json:"-"`
	LastLoginAt     *time.Time `json:"last_login_at"`
	LastLoginIP     string     `gorm:"size:45" json:"last_login_ip"`
	CreatedAt       time.Time  `gorm:"not null" json:"created_at"`
	UpdatedAt       time.Time  `gorm:"not null" json:"updated_at"`
}

// Image 是图片元数据与用户记录; 外部对象按 (policy_id, object_key) 复用.
type Image struct {
	ID               int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID           int64     `gorm:"not null;index:idx_images_user_kind_created,priority:1;index:idx_images_user_sha256,priority:1" json:"user_id"`
	PolicyID         int64     `gorm:"not null;index:idx_images_policy_object,priority:1" json:"policy_id"`
	RelPath          string    `gorm:"size:512;not null" json:"rel_path"`
	ObjectKey        string    `gorm:"size:512;not null;index:idx_images_policy_object,priority:2" json:"object_key"`
	MD5              string    `gorm:"size:32;not null" json:"md5"`
	SHA1             string    `gorm:"size:40;not null" json:"sha1"`
	SHA256           string    `gorm:"size:64;not null;index:idx_images_sha256;index:idx_images_user_sha256,priority:2" json:"sha256"`
	SHA512           string    `gorm:"size:128;not null" json:"sha512"`
	Size             int64     `gorm:"not null" json:"size"`
	Kind             string    `gorm:"size:8;not null;default:'image';index:idx_images_user_kind_created,priority:2" json:"kind"`
	Format           string    `gorm:"size:16;not null" json:"format"`
	Ext              string    `gorm:"size:16;not null" json:"ext"`
	MIME             string    `gorm:"size:128;not null" json:"mime"`
	Width            *int      `json:"width"`
	Height           *int      `json:"height"`
	OriginalFilename string    `gorm:"size:255;not null" json:"original_filename"`
	URL              string    `gorm:"size:1024;not null" json:"url"`
	QuotaCharged     bool      `gorm:"not null" json:"quota_charged"`
	Source           string    `gorm:"size:16;not null;default:'web'" json:"source"`
	ClientIP         string    `gorm:"size:45" json:"client_ip"`
	CreatedAt        time.Time `gorm:"not null;index:idx_images_user_kind_created,priority:3" json:"created_at"`
}

// APIKey 是 API 密钥; secret 只存散列.
type APIKey struct {
	ID         int64      `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID     int64      `gorm:"not null;index" json:"user_id"`
	Name       string     `gorm:"size:64;not null" json:"name"`
	AccessKey  string     `gorm:"size:32;not null;uniqueIndex" json:"access_key"`
	SecretHash string     `gorm:"size:255;not null" json:"-"`
	Scopes     *int64     `json:"scopes"`
	LastUsedAt *time.Time `json:"last_used_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
	CreatedAt  time.Time  `gorm:"not null" json:"created_at"`
}

// AuditLog 是审计日志; detail 为 JSON 摘要, 不写凭据.
type AuditLog struct {
	ID         int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID     *int64    `gorm:"index:idx_audit_logs_user_created,priority:1" json:"user_id"`
	Action     string    `gorm:"size:64;not null;index:idx_audit_logs_action_created,priority:1" json:"action"`
	TargetType string    `gorm:"size:32" json:"target_type"`
	TargetID   *int64    `json:"target_id"`
	Detail     string    `gorm:"type:text" json:"detail"`
	IP         string    `gorm:"size:45" json:"ip"`
	UserAgent  string    `gorm:"size:255" json:"user_agent"`
	CreatedAt  time.Time `gorm:"not null;index:idx_audit_logs_user_created,priority:2;index:idx_audit_logs_action_created,priority:2" json:"created_at"`
}

// Setting 是系统设置键值对.
type Setting struct {
	Key       string    `gorm:"primaryKey;size:64" json:"key"`
	Value     string    `gorm:"type:text" json:"value"`
	UpdatedAt time.Time `gorm:"not null" json:"updated_at"`
}

// PolicyScanManifest 保存策略扫描在列举阶段生成的对象键清单.
type PolicyScanManifest struct {
	TaskID     int64  `gorm:"primaryKey" json:"task_id"`
	ObjectKeys string `gorm:"type:text;not null" json:"-"`
}

// MaintenanceTask 是维护任务队列项.
type MaintenanceTask struct {
	ID         int64      `gorm:"primaryKey;autoIncrement" json:"id"`
	Type       string     `gorm:"size:32;not null" json:"type"`
	Payload    string     `gorm:"type:text" json:"payload"`
	Result     string     `gorm:"type:text;not null;default:''" json:"result,omitempty"`
	Status     string     `gorm:"size:16;not null;default:'pending';index:idx_maintenance_tasks_status_created,priority:1" json:"status"`
	Attempts   int        `gorm:"not null;default:0" json:"attempts"`
	LastError  string     `gorm:"type:text" json:"last_error"`
	CreatedAt  time.Time  `gorm:"not null;index:idx_maintenance_tasks_status_created,priority:2" json:"created_at"`
	FinishedAt *time.Time `json:"finished_at"`
}

// AllModels 是迁移涉及的模型清单, 顺序即建表顺序.
func AllModels() []any {
	return []any{
		&Group{},
		&GroupPolicy{},
		&StoragePolicy{},
		&PolicyURL{},
		&User{},
		&Image{},
		&APIKey{},
		&AuditLog{},
		&Setting{},
		&MaintenanceTask{},
		&PolicyScanManifest{},
	}
}
