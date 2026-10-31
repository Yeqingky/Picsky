package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/gorm"

	"github.com/yeqingky/Picsky/internal/apperr"
	"github.com/yeqingky/Picsky/internal/model"
	"github.com/yeqingky/Picsky/internal/storage"
)

// 任务执行参数.
const (
	taskWorkerInterval = 15 * time.Second
	taskMaxAttempts    = 3
	taskPurgeAfter     = 30 * 24 * time.Hour
	taskScanTimeout    = 30 * time.Minute
)

// Tasks 是维护任务队列: 入队, 调度, 手动执行与幂等重试.
type Tasks struct {
	db       *gorm.DB
	settings *Settings
	audit    *Audit
	policies *Policies
	images   *Images
	log      *slog.Logger

	daily map[string]time.Time
	wake  chan struct{}
}

// NewTasks 创建任务服务.
func NewTasks(database *gorm.DB, settings *Settings, audit *Audit, policies *Policies, images *Images, log *slog.Logger) *Tasks {
	tasks := &Tasks{
		db:       database,
		settings: settings,
		audit:    audit,
		policies: policies,
		images:   images,
		log:      log,
		daily:    make(map[string]time.Time),
		wake:     make(chan struct{}, 1),
	}
	// 启动时回收上次进程中断留在 running 的任务.
	recovered := tasks.db.Model(&model.MaintenanceTask{}).
		Where("status = ?", model.TaskRunning).
		Updates(map[string]any{"status": model.TaskPending, "last_error": "进程重启, 任务回到待处理"})
	if recovered.Error != nil {
		log.Warn("回收未完成任务失败", "error", recovered.Error)
	} else if recovered.RowsAffected > 0 {
		log.Info("已回收未完成的任务", "count", recovered.RowsAffected)
	}
	return tasks
}

// Enqueue 入队一个维护任务.
func (t *Tasks) Enqueue(taskType string, payload any) (*model.MaintenanceTask, error) {
	encoded := ""
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, apperr.ErrInternal.WithMessage("任务参数序列化失败: " + err.Error())
		}
		encoded = string(raw)
	}
	task := model.MaintenanceTask{
		Type:      taskType,
		Payload:   encoded,
		Status:    model.TaskPending,
		CreatedAt: nowUTC(),
	}
	if err := t.db.Create(&task).Error; err != nil {
		return nil, apperr.ErrInternal.WithMessage("入队维护任务失败: " + err.Error())
	}
	t.notifyWorker()
	return &task, nil
}

// EnqueuePolicyScan 立即创建策略扫描任务; 对象清单由后台任务列举并持久化.
func (t *Tasks) EnqueuePolicyScan(policyID, userID int64, path *string, dryRun bool) (*model.MaintenanceTask, error) {
	return t.Enqueue(model.TaskPolicyScan, struct {
		PolicyID int64   `json:"policy_id"`
		UserID   int64   `json:"user_id"`
		Path     *string `json:"path,omitempty"`
		DryRun   bool    `json:"dry_run,omitempty"`
	}{PolicyID: policyID, UserID: userID, Path: path, DryRun: dryRun})
}

func (t *Tasks) notifyWorker() {
	select {
	case t.wake <- struct{}{}:
	default:
	}
}

// TaskFilter 是任务列表过滤条件.
type TaskFilter struct {
	Page     int
	PageSize int
	Type     string
	Status   string
}

// TaskListResult 是任务分页结果.
type TaskListResult struct {
	Total    int64                   `json:"total"`
	Page     int                     `json:"page"`
	PageSize int                     `json:"page_size"`
	Items    []model.MaintenanceTask `json:"items"`
}

// List 返回任务列表.
func (t *Tasks) List(filter TaskFilter) (*TaskListResult, error) {
	page := filter.Page
	if page < 1 {
		page = 1
	}
	pageSize := filter.PageSize
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 200 {
		pageSize = 200
	}
	query := t.db.Model(&model.MaintenanceTask{})
	if filter.Type != "" {
		query = query.Where("type = ?", filter.Type)
	}
	if filter.Status != "" {
		query = query.Where("status = ?", filter.Status)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	var items []model.MaintenanceTask
	if err := query.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&items).Error; err != nil {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	if items == nil {
		items = []model.MaintenanceTask{}
	}
	for idx := range items {
		items[idx].Result = ""
	}
	return &TaskListResult{Total: total, Page: page, PageSize: pageSize, Items: items}, nil
}

// Get 返回单个维护任务, 含任务结果.
func (t *Tasks) Get(id int64) (*model.MaintenanceTask, error) {
	var task model.MaintenanceTask
	if err := t.db.First(&task, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperr.ErrNotFound.WithMessage("任务不存在")
		}
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	return &task, nil
}

// RunNow 手动执行指定任务.
func (t *Tasks) RunNow(id int64, actor *Principal) error {
	var task model.MaintenanceTask
	if err := t.db.First(&task, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return apperr.ErrNotFound.WithMessage("任务不存在")
		}
		return apperr.ErrInternal.WithMessage(err.Error())
	}
	if task.Type == model.TaskPolicyScan {
		requeue := t.db.Model(&model.MaintenanceTask{}).
			Where("id = ? AND status IN ?", task.ID, []string{model.TaskPending, model.TaskFailed, model.TaskDone}).
			Updates(map[string]any{"status": model.TaskPending, "finished_at": nil, "last_error": "", "result": ""})
		if requeue.Error != nil {
			return apperr.ErrInternal.WithMessage(requeue.Error.Error())
		}
		if requeue.RowsAffected == 0 {
			var status string
			if err := t.db.Model(&model.MaintenanceTask{}).Select("status").Where("id = ?", task.ID).Scan(&status).Error; err != nil {
				return apperr.ErrInternal.WithMessage(err.Error())
			}
			if status != model.TaskPending {
				return apperr.ErrInvalidRequest.WithMessage("任务正在执行中")
			}
		}
		t.audit.Recordf(Entry{
			UserID:     actorID(actor),
			Action:     "task_run",
			TargetType: "task",
			TargetID:   &task.ID,
			Detail:     map[string]any{"type": task.Type},
		})
		t.notifyWorker()
		return nil
	}
	ctx, cancel := contextWithTimeout(30 * time.Minute)
	defer cancel()
	if err := t.execute(ctx, &task, true); err != nil {
		if errors.Is(err, errTaskClaimed) {
			return apperr.ErrInvalidRequest.WithMessage("任务正在执行中")
		}
		t.markFailed(&task, err)
		return apperr.ErrBackend.WithMessage(err.Error())
	}
	t.markDone(&task)
	t.audit.Recordf(Entry{
		UserID:     actorID(actor),
		Action:     "task_run",
		TargetType: "task",
		TargetID:   &task.ID,
		Detail:     map[string]any{"type": task.Type},
	})
	return nil
}

// Delete 删除已完成或失败的任务; 运行中或待处理任务不可删除.
func (t *Tasks) Delete(id int64, actor *Principal) error {
	var task model.MaintenanceTask
	if err := t.db.First(&task, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return apperr.ErrNotFound.WithMessage("任务不存在")
		}
		return apperr.ErrInternal.WithMessage(err.Error())
	}
	err := t.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("task_id = ?", id).Delete(&model.PolicyScanManifest{}).Error; err != nil {
			return err
		}
		result := tx.Where("id = ? AND status IN ?", id, []string{model.TaskDone, model.TaskFailed}).
			Delete(&model.MaintenanceTask{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return errTaskNotDeletable
		}
		return nil
	})
	switch {
	case errors.Is(err, errTaskNotDeletable):
		return apperr.ErrInvalidRequest.WithMessage("仅可删除已完成或失败的任务")
	case err != nil:
		return apperr.ErrInternal.WithMessage("删除任务失败: " + err.Error())
	}
	t.audit.Recordf(Entry{
		UserID:     actorID(actor),
		Action:     "task_delete",
		TargetType: "task",
		TargetID:   &task.ID,
		Detail:     map[string]any{"type": task.Type},
	})
	return nil
}

var errTaskNotDeletable = errors.New("task not deletable")

// Start 启动后台调度循环, 直到 ctx 结束.
func (t *Tasks) Start(ctx context.Context) {
	ticker := time.NewTicker(taskWorkerInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := t.scheduleDaily(); err != nil {
				t.log.Warn("安排每日维护任务失败", "error", err)
			}
		case <-t.wake:
		}
		if err := t.runPending(ctx); err != nil {
			t.log.Warn("执行维护任务失败", "error", err)
		}
	}
}

// scheduleDaily 按 24 小时周期安排每日任务.
func (t *Tasks) scheduleDaily() error {
	now := time.Now()
	for _, taskType := range []string{model.TaskLogPurge, model.TaskQuotaReconcile} {
		last, ok := t.daily[taskType]
		if ok && now.Sub(last) < 24*time.Hour {
			continue
		}
		if _, err := t.Enqueue(taskType, nil); err != nil {
			return err
		}
		t.daily[taskType] = now
	}
	return nil
}

// runPending 取出并执行待处理任务.
func (t *Tasks) runPending(ctx context.Context) error {
	var tasks []model.MaintenanceTask
	if err := t.db.Where("status = ?", model.TaskPending).Order("id ASC").Limit(5).Find(&tasks).Error; err != nil {
		return err
	}
	for idx := range tasks {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		task := tasks[idx]
		if err := t.execute(ctx, &task, false); err != nil {
			t.markFailed(&task, err)
			continue
		}
		t.markDone(&task)
	}
	return nil
}

// errTaskClaimed 表示任务已被其他执行者取走, 调用方不应标记失败.
var errTaskClaimed = errors.New("task already claimed")

// execute 执行单个任务; 任务必须幂等可重试.
func (t *Tasks) execute(ctx context.Context, task *model.MaintenanceTask, allowCompleted bool) error {
	// 已完成任务仅允许手动重跑, 调度路径不能认领 done, 避免从过期任务快照重复执行.
	claimableStatuses := []string{model.TaskPending, model.TaskFailed}
	if allowCompleted {
		claimableStatuses = append(claimableStatuses, model.TaskDone)
	}
	claim := t.db.Model(&model.MaintenanceTask{}).
		Where("id = ? AND status IN ?", task.ID, claimableStatuses).
		Updates(map[string]any{
			"status":      model.TaskRunning,
			"attempts":    gorm.Expr("attempts + 1"),
			"finished_at": nil,
			"last_error":  "",
			"result":      "",
		})
	if claim.Error != nil {
		return claim.Error
	}
	if claim.RowsAffected == 0 {
		return errTaskClaimed
	}

	switch task.Type {
	case model.TaskPolicyScan:
		return t.executePolicyScan(ctx, task)
	case model.TaskLogPurge:
		return t.purgeExpiredLogs()

	case model.TaskQuotaReconcile:
		fixed, err := t.images.ReconcileUsed()
		if err != nil {
			return err
		}
		t.log.Info("配额对账完成", "fixed_users", fixed)
		return nil

	case model.TaskObjectDelete:
		var payload struct {
			PolicyID  int64  `json:"policy_id"`
			ObjectKey string `json:"object_key"`
		}
		if err := json.Unmarshal([]byte(task.Payload), &payload); err != nil {
			return fmt.Errorf("任务参数非法: %w", err)
		}
		policy, err := t.policies.Get(payload.PolicyID)
		if err != nil {
			return err
		}
		return t.performObjectDelete(ctx, policy, payload.ObjectKey)

	default:
		return fmt.Errorf("未知任务类型: %s", task.Type)
	}
}

// performObjectDelete 调用后端删除对象, 校验响应, 并在对象确认删除后释放序号.
// 删除前路径仍被记录引用时跳过, 避免重跑旧任务删除已被重新占用的对象.
func (t *Tasks) performObjectDelete(ctx context.Context, policy *model.StoragePolicy, objectKey string) error {
	adapter, err := t.policies.Adapter(policy)
	if err != nil {
		return err
	}
	if !adapter.Capabilities().Has(storage.CapDelete) {
		return errors.New("后端未声明 delete 能力")
	}
	var references int64
	if err := t.db.Model(&model.Image{}).
		Where("policy_id = ? AND object_key = ?", policy.ID, objectKey).
		Count(&references).Error; err != nil {
		return err
	}
	if references > 0 {
		return nil
	}
	if err := adapter.Delete(ctx, objectKey); err != nil && !errors.Is(err, storage.ErrNotFound) {
		return err
	}
	// 删除期间路径可能被新记录重新占用, 此时不释放序号.
	if err := t.db.Model(&model.Image{}).
		Where("policy_id = ? AND object_key = ?", policy.ID, objectKey).
		Count(&references).Error; err != nil {
		return err
	}
	if references > 0 {
		return nil
	}
	if value, ok := storage.ExtractSequence(policy.PathTemplate, recordPathForPolicy(policy, objectKey)); ok {
		return releasePolicySequence(t.db, policy, value)
	}
	return nil
}

// DeleteObject 立即尝试删除对象并写入维护队列.
// 失败时任务保持 failed, 等待人工重试或删除; queued 为 true 表示后台执行器已接手.
func (t *Tasks) DeleteObject(ctx context.Context, policyID int64, objectKey string) (bool, error) {
	payload, err := json.Marshal(map[string]any{"policy_id": policyID, "object_key": objectKey})
	if err != nil {
		return false, apperr.ErrInternal.WithMessage("任务参数序列化失败: " + err.Error())
	}
	task := model.MaintenanceTask{
		Type:      model.TaskObjectDelete,
		Payload:   string(payload),
		Status:    model.TaskPending,
		CreatedAt: nowUTC(),
	}
	if err := t.db.Create(&task).Error; err != nil {
		return false, apperr.ErrInternal.WithMessage("入队对象清理任务失败: " + err.Error())
	}
	claim := t.db.Model(&model.MaintenanceTask{}).
		Where("id = ? AND status = ?", task.ID, model.TaskPending).
		Updates(map[string]any{
			"status":     model.TaskRunning,
			"attempts":   gorm.Expr("attempts + 1"),
			"last_error": "",
			"result":     "",
		})
	if claim.Error != nil {
		return false, apperr.ErrInternal.WithMessage(claim.Error.Error())
	}
	if claim.RowsAffected == 0 {
		return true, nil
	}
	policy, err := t.policies.Get(policyID)
	if err != nil {
		t.markObjectDeleteFailed(&task, err)
		return false, err
	}
	if err := t.performObjectDelete(ctx, policy, objectKey); err != nil {
		t.markObjectDeleteFailed(&task, err)
		return false, err
	}
	t.markDone(&task)
	return false, nil
}

// markObjectDeleteFailed 把对象删除任务标记为失败并保留在队列中, 等待人工处理.
func (t *Tasks) markObjectDeleteFailed(task *model.MaintenanceTask, cause error) {
	now := nowUTC()
	if err := t.db.Model(&model.MaintenanceTask{}).Where("id = ?", task.ID).Updates(map[string]any{
		"status":      model.TaskFailed,
		"last_error":  cause.Error(),
		"finished_at": now,
	}).Error; err != nil {
		t.log.Warn("更新任务状态失败", "task_id", task.ID, "error", err)
	}
	t.log.Warn("对象删除失败, 等待人工重试", "task_id", task.ID, "error", cause)
}

func (t *Tasks) executePolicyScan(ctx context.Context, task *model.MaintenanceTask) error {
	ctx, cancel := context.WithTimeout(ctx, taskScanTimeout)
	defer cancel()
	var payload struct {
		PolicyID int64   `json:"policy_id"`
		UserID   int64   `json:"user_id"`
		Path     *string `json:"path"`
		DryRun   bool    `json:"dry_run"`
	}
	if err := json.Unmarshal([]byte(task.Payload), &payload); err != nil {
		return fmt.Errorf("任务参数非法: %w", err)
	}

	var result *ScanResult
	var manifest model.PolicyScanManifest
	err := t.db.Where("task_id = ?", task.ID).First(&manifest).Error
	switch {
	case err == nil:
		var keys []string
		if err := json.Unmarshal([]byte(manifest.ObjectKeys), &keys); err != nil {
			return fmt.Errorf("扫描对象清单非法: %w", err)
		}
		result, err = t.policies.ScanListed(ctx, payload.PolicyID, payload.UserID, keys, payload.DryRun)
	case errors.Is(err, gorm.ErrRecordNotFound):
		keys, listErr := t.policies.ListScanObjects(ctx, payload.PolicyID, payload.Path)
		if listErr != nil {
			return listErr
		}
		encodedKeys, marshalErr := json.Marshal(keys)
		if marshalErr != nil {
			return fmt.Errorf("扫描对象清单序列化失败: %w", marshalErr)
		}
		if createErr := t.db.Create(&model.PolicyScanManifest{TaskID: task.ID, ObjectKeys: string(encodedKeys)}).Error; createErr != nil {
			return fmt.Errorf("保存扫描对象清单失败: %w", createErr)
		}
		result, err = t.policies.ScanListed(ctx, payload.PolicyID, payload.UserID, keys, payload.DryRun)
	}
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("扫描结果序列化失败: %w", err)
	}
	if err := t.db.Model(&model.MaintenanceTask{}).Where("id = ?", task.ID).Update("result", string(encoded)).Error; err != nil {
		return fmt.Errorf("保存扫描结果失败: %w", err)
	}
	t.log.Info("策略扫描完成", "policy_id", payload.PolicyID, "images", result.Images, "files", result.Files, "skipped", result.Skipped)
	return nil
}

// purgeExpiredLogs 清除过期审计日志与已结束的维护任务.
func (t *Tasks) purgeExpiredLogs() error {
	logRetention := t.settings.Int64("log_retention_days", 90)
	if logRetention > 0 {
		logCutoff := nowUTC().Add(-time.Duration(logRetention) * 24 * time.Hour)
		if err := t.db.Where("created_at < ?", logCutoff).Delete(&model.AuditLog{}).Error; err != nil {
			return err
		}
	}

	taskCutoff := nowUTC().Add(-taskPurgeAfter)
	expiredTasks := t.db.Model(&model.MaintenanceTask{}).
		Select("id").
		Where("status = ? AND created_at < ?", model.TaskDone, taskCutoff)
	if err := t.db.Where("task_id IN (?)", expiredTasks).Delete(&model.PolicyScanManifest{}).Error; err != nil {
		return err
	}
	if err := t.db.Where("id IN (?)", expiredTasks).Delete(&model.MaintenanceTask{}).Error; err != nil {
		return err
	}

	t.log.Info("清理过期日志与已完成任务完成")
	return nil
}

// markDone 标记任务完成.
func (t *Tasks) markDone(task *model.MaintenanceTask) {
	now := nowUTC()
	if err := t.db.Model(&model.MaintenanceTask{}).Where("id = ?", task.ID).Updates(map[string]any{
		"status":      model.TaskDone,
		"finished_at": now,
		"last_error":  "",
	}).Error; err != nil {
		t.log.Warn("更新任务状态失败", "task_id", task.ID, "error", err)
	}
}

// markFailed 标记任务失败: 对象删除失败保留 failed 等待人工处理, 其他任务未超过重试上限时回到待处理.
func (t *Tasks) markFailed(task *model.MaintenanceTask, cause error) {
	if errors.Is(cause, errTaskClaimed) {
		return
	}
	attempts := task.Attempts + 1
	status := model.TaskFailed
	var finishedAt *time.Time
	manual := task.Type == model.TaskObjectDelete
	if !manual && attempts < taskMaxAttempts {
		status = model.TaskPending
	} else {
		now := nowUTC()
		finishedAt = &now
	}
	updates := map[string]any{
		"status":     status,
		"last_error": cause.Error(),
	}
	if finishedAt != nil {
		updates["finished_at"] = *finishedAt
	}
	if err := t.db.Model(&model.MaintenanceTask{}).Where("id = ?", task.ID).Updates(updates).Error; err != nil {
		t.log.Warn("更新任务状态失败", "task_id", task.ID, "error", err)
	}
	t.log.Warn("维护任务执行失败", "task_id", task.ID, "type", task.Type, "attempts", attempts, "error", cause)
}
