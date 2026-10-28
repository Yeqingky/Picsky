package service

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/yeqingky/Picsky/internal/apperr"
	"github.com/yeqingky/Picsky/internal/model"
	"github.com/yeqingky/Picsky/internal/storage"
)

// 删除响应中的原因取值.
const (
	DeleteReasonLastReference = "last_reference"
	DeleteReasonNoCapability  = "no_capability"
	DeleteReasonSettingOff    = "setting_disabled"
	DeleteReasonShared        = "shared"
	DeleteReasonQueued        = "queued"
	DeleteReasonBackendError  = "backend_error"
)

// Images 管理图片记录: 列表, 详情, 改名, 删除与配额回补.
type Images struct {
	db       *gorm.DB
	settings *Settings
	audit    *Audit
	policies *Policies
	log      *slog.Logger
	tasks    *Tasks
}

// NewImages 创建图片服务.
func NewImages(database *gorm.DB, settings *Settings, audit *Audit, policies *Policies, log *slog.Logger) *Images {
	return &Images{db: database, settings: settings, audit: audit, policies: policies, log: log}
}

// SetTasks 注入维护任务队列 (外部对象清理失败时入队重试).
func (i *Images) SetTasks(tasks *Tasks) { i.tasks = tasks }

// ListFilter 是图库过滤条件.
type ListFilter struct {
	Page      int
	PageSize  int
	Keyword   string
	Format    string
	From      *time.Time
	To        *time.Time
	UserID    *int64
	UserEmail string
	PolicyID  *int64
	SortField string
	SortDesc  bool
}

// ListResult 是分页结果.
type ListResult struct {
	Total    int64         `json:"total"`
	Page     int           `json:"page"`
	PageSize int           `json:"page_size"`
	Items    []model.Image `json:"items"`
}

// List 返回图片记录列表 (只含 kind 为 image 的记录).
func (i *Images) List(principal *Principal, filter ListFilter) (*ListResult, error) {
	page := filter.Page
	if page < 1 {
		page = 1
	}
	pageSize := filter.PageSize
	if pageSize < 1 {
		pageSize = int(i.settings.Int64("gallery_page_size", 40))
	}
	if pageSize < 1 {
		pageSize = 40
	}
	if pageSize > 200 {
		pageSize = 200
	}

	query := i.db.Model(&model.Image{}).Where("images.kind = ?", model.KindImage)
	if !principal.Has(model.PermViewAll) {
		query = query.Where("images.user_id = ?", principal.User.ID)
	} else if filter.UserID != nil {
		query = query.Where("images.user_id = ?", *filter.UserID)
	}
	if email := strings.ToLower(strings.TrimSpace(filter.UserEmail)); email != "" && principal.Has(model.PermViewAll) {
		query = query.Joins("JOIN users ON users.id = images.user_id").
			Where("LOWER(users.email) LIKE ?", "%"+email+"%")
	}
	if keyword := strings.TrimSpace(filter.Keyword); keyword != "" {
		query = query.Where("images.original_filename LIKE ?", "%"+keyword+"%")
	}
	if format := strings.ToLower(strings.TrimSpace(filter.Format)); format != "" {
		query = query.Where("images.format = ?", format)
	}
	if filter.From != nil {
		query = query.Where("images.created_at >= ?", filter.From.UTC())
	}
	if filter.To != nil {
		query = query.Where("images.created_at <= ?", filter.To.UTC())
	}
	if filter.PolicyID != nil {
		query = query.Where("images.policy_id = ?", *filter.PolicyID)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}

	sortColumn := "images.created_at"
	switch filter.SortField {
	case "size":
		sortColumn = "images.size"
	case "created_at":
		sortColumn = "images.created_at"
	case "original_filename":
		sortColumn = "images.original_filename"
	case "format":
		sortColumn = "images.format"
	}
	direction := "DESC"
	if !filter.SortDesc && filter.SortField == "" {
		direction = "DESC"
	} else if !filter.SortDesc {
		direction = "ASC"
	}
	if filter.SortField == "" {
		direction = "DESC"
	}

	var items []model.Image
	if err := query.Order(fmt.Sprintf("%s %s, images.id DESC", sortColumn, direction)).
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&items).Error; err != nil {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	if items == nil {
		items = []model.Image{}
	}
	return &ListResult{Total: total, Page: page, PageSize: pageSize, Items: items}, nil
}

// UserEmails 批量读取用户邮箱, 用于图库展示上传用户.
func (i *Images) UserEmails(userIDs []int64) (map[int64]string, error) {
	unique := make([]int64, 0, len(userIDs))
	seen := make(map[int64]struct{}, len(userIDs))
	for _, id := range userIDs {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	emails := make(map[int64]string, len(unique))
	if len(unique) == 0 {
		return emails, nil
	}
	var users []model.User
	if err := i.db.Select("id", "email").Where("id IN ?", unique).Find(&users).Error; err != nil {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	for _, user := range users {
		emails[user.ID] = user.Email
	}
	return emails, nil
}

// Get 读取单条记录.
func (i *Images) Get(principal *Principal, id int64) (*model.Image, error) {
	record, err := i.find(id)
	if err != nil {
		return nil, err
	}
	if record.UserID != principal.User.ID && !principal.Has(model.PermViewAll) {
		return nil, apperr.ErrNotFound.WithMessage("记录不存在")
	}
	return record, nil
}

// DeleteResult 是删除结果.
type DeleteResult struct {
	ID            int64  `json:"id"`
	Deleted       bool   `json:"deleted"`
	ObjectDeleted bool   `json:"object_deleted"`
	Reason        string `json:"reason,omitempty"`
}

// Delete 删除记录, 回补配额, 按引用计数与能力降级决定是否删除外部对象.
func (i *Images) Delete(principal *Principal, id int64, ip, userAgent string) (*DeleteResult, error) {
	record, err := i.find(id)
	if err != nil {
		return nil, err
	}
	if record.UserID != principal.User.ID && !principal.Has(model.PermDeleteAll) {
		return nil, apperr.ErrNotFound.WithMessage("记录不存在")
	}
	if record.UserID == principal.User.ID && !principal.Has(model.PermDeleteOwn) && !principal.Has(model.PermDeleteAll) {
		return nil, apperr.ErrForbidden.WithMessage("没有删除权限")
	}
	return i.deleteRecord(principal, record, ip, userAgent)
}

// BatchDelete 批量删除.
func (i *Images) BatchDelete(principal *Principal, ids []int64, ip, userAgent string) ([]DeleteResult, error) {
	if !principal.Has(model.PermDeleteOwn) && !principal.Has(model.PermDeleteAll) {
		return nil, apperr.ErrForbidden.WithMessage("没有删除权限")
	}
	if len(ids) == 0 {
		return nil, apperr.ErrInvalidRequest.WithMessage("ids 不能为空")
	}
	if len(ids) > 200 {
		return nil, apperr.ErrInvalidRequest.WithMessage("单次最多删除 200 条")
	}
	results := make([]DeleteResult, 0, len(ids))
	for _, id := range ids {
		record, err := i.find(id)
		if err != nil {
			results = append(results, DeleteResult{ID: id, Deleted: false})
			continue
		}
		if record.UserID != principal.User.ID && !principal.Has(model.PermDeleteAll) {
			results = append(results, DeleteResult{ID: id, Deleted: false})
			continue
		}
		result, err := i.deleteRecord(principal, record, ip, userAgent)
		if err != nil {
			apiErr := apperr.From(err)
			i.log.Warn("批量删除中的单条失败", "image_id", id, "error", apiErr.Message)
			results = append(results, DeleteResult{ID: id, Deleted: false})
			continue
		}
		results = append(results, *result)
	}
	return results, nil
}

// deleteRecord 执行单条删除流程.
func (i *Images) deleteRecord(principal *Principal, record *model.Image, ip, userAgent string) (*DeleteResult, error) {
	now := nowUTC()
	deleted := false

	// 1. 事务内条件删除记录, 回补配额并把计费移交给同对象最早的剩余记录.
	// 条件删除保证并发重复删除只有一次生效, 避免重复回补与重复移交.
	if err := i.db.Transaction(func(tx *gorm.DB) error {
		result := tx.Where("id = ?", record.ID).Delete(&model.Image{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		deleted = true

		if record.QuotaCharged {
			if err := refundQuota(tx, record.UserID, 1, record.Size, now); err != nil {
				return err
			}
			var next model.Image
			err := tx.Where("policy_id = ? AND object_key = ? AND quota_charged = ?",
				record.PolicyID, record.ObjectKey, false).
				Order("id ASC").First(&next).Error
			if err == nil {
				if err := tx.Model(&model.Image{}).Where("id = ? AND quota_charged = ?", next.ID, false).Update("quota_charged", true).Error; err != nil {
					return err
				}
				if err := chargeQuota(tx, next.UserID, 1, next.Size, now); err != nil {
					return err
				}
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		return nil
	}); err != nil {
		return nil, apperr.ErrInternal.WithMessage("删除记录失败: " + err.Error())
	}

	if !deleted {
		// 已被并发删除: 幂等返回.
		return &DeleteResult{ID: record.ID, Deleted: true}, nil
	}

	// 2. 引用计数与外部对象删除.
	var remaining int64
	if err := i.db.Model(&model.Image{}).
		Where("policy_id = ? AND object_key = ?", record.PolicyID, record.ObjectKey).
		Count(&remaining).Error; err != nil {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}

	result := &DeleteResult{ID: record.ID, Deleted: true}
	switch {
	case remaining > 0:
		result.Reason = DeleteReasonShared
	case !i.settings.Bool("delete_object_on_last_reference", true):
		result.Reason = DeleteReasonSettingOff
	default:
		policy, err := i.policies.Get(record.PolicyID)
		if err != nil {
			result.Reason = DeleteReasonBackendError
			i.log.Warn("读取策略失败, 保留外部对象", "policy_id", record.PolicyID, "error", err)
			break
		}
		adapter, err := i.policies.Adapter(policy)
		if err != nil {
			result.Reason = DeleteReasonBackendError
			i.log.Warn("初始化存储后端失败, 保留外部对象", "policy_id", record.PolicyID, "error", err)
			break
		}
		if !adapter.Capabilities().Has(storage.CapDelete) {
			result.Reason = DeleteReasonNoCapability
			break
		}
		ctx, cancel := contextWithTimeout(30 * time.Second)
		defer cancel()
		if i.tasks != nil {
			queued, err := i.tasks.DeleteObject(ctx, policy.ID, record.ObjectKey)
			if err != nil {
				result.Reason = DeleteReasonBackendError
				i.log.Warn("删除外部对象失败, 已记录到维护队列等待人工处理", "object_key", record.ObjectKey, "error", err)
				break
			}
			if queued {
				result.Reason = DeleteReasonQueued
				break
			}
			result.ObjectDeleted = true
			result.Reason = DeleteReasonLastReference
			break
		}
		if err := adapter.Delete(ctx, record.ObjectKey); err != nil && !errors.Is(err, storage.ErrNotFound) {
			result.Reason = DeleteReasonBackendError
			i.log.Warn("删除外部对象失败, 保留外部对象", "object_key", record.ObjectKey, "error", err)
			break
		}
		result.ObjectDeleted = true
		result.Reason = DeleteReasonLastReference
		i.releaseSequence(policy, record)
	}

	i.audit.Recordf(Entry{
		UserID:     &principal.User.ID,
		Action:     "delete",
		TargetType: "image",
		TargetID:   &record.ID,
		Detail: map[string]any{
			"policy_id":      record.PolicyID,
			"object_key":     record.ObjectKey,
			"object_deleted": result.ObjectDeleted,
			"reason":         result.Reason,
			"quota_charged":  record.QuotaCharged,
			"remaining_refs": remaining,
		},
		IP:        ip,
		UserAgent: userAgent,
	})
	return result, nil
}

// releaseSequence 在外部对象确认删除后, 把记录使用的 {seq} 加入策略释放队列.
func (i *Images) releaseSequence(policy *model.StoragePolicy, record *model.Image) {
	value, ok := storage.ExtractSequence(policy.PathTemplate, record.RelPath)
	if !ok {
		return
	}
	if err := releasePolicySequence(i.db, policy, value); err != nil {
		i.log.Warn("释放序号失败", "image_id", record.ID, "sequence", value, "error", err)
	}
}

// ReconcileUsed 按记录聚合重算用户的 used_images 与 used_bytes.
func (i *Images) ReconcileUsed() (int, error) {
	var users []model.User
	if err := i.db.Order("id ASC").Find(&users).Error; err != nil {
		return 0, apperr.ErrInternal.WithMessage(err.Error())
	}
	fixed := 0
	for _, user := range users {
		var aggregate struct {
			Count int64
			Bytes int64
		}
		if err := i.db.Model(&model.Image{}).
			Select("COUNT(*) as count, COALESCE(SUM(size), 0) as bytes").
			Where("user_id = ? AND quota_charged = ?", user.ID, true).
			Scan(&aggregate).Error; err != nil {
			return fixed, apperr.ErrInternal.WithMessage(err.Error())
		}
		if aggregate.Count == user.UsedImages && aggregate.Bytes == user.UsedBytes {
			continue
		}
		if err := i.db.Model(&model.User{}).Where("id = ?", user.ID).Updates(map[string]any{
			"used_images": aggregate.Count,
			"used_bytes":  aggregate.Bytes,
			"updated_at":  nowUTC(),
		}).Error; err != nil {
			return fixed, apperr.ErrInternal.WithMessage(err.Error())
		}
		fixed++
	}
	return fixed, nil
}

// find 按 ID 读取记录.
func (i *Images) find(id int64) (*model.Image, error) {
	var record model.Image
	if err := i.db.First(&record, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperr.ErrNotFound.WithMessage("记录不存在")
		}
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	return &record, nil
}

// refundQuota 原子回补用户配额计数, 结果不为负.
func refundQuota(tx *gorm.DB, userID int64, images int64, bytes int64, now time.Time) error {
	return tx.Model(&model.User{}).Where("id = ?", userID).Updates(map[string]any{
		"used_images": gorm.Expr("CASE WHEN used_images > ? THEN used_images - ? ELSE 0 END", images, images),
		"used_bytes":  gorm.Expr("CASE WHEN used_bytes > ? THEN used_bytes - ? ELSE 0 END", bytes, bytes),
		"updated_at":  now,
	}).Error
}

// chargeQuota 原子增加用户配额计数 (计费移交时使用).
func chargeQuota(tx *gorm.DB, userID int64, images int64, bytes int64, now time.Time) error {
	return tx.Model(&model.User{}).Where("id = ?", userID).Updates(map[string]any{
		"used_images": gorm.Expr("used_images + ?", images),
		"used_bytes":  gorm.Expr("used_bytes + ?", bytes),
		"updated_at":  now,
	}).Error
}
