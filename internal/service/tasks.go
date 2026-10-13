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
		daily:    map[string]time.Time{},
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
	return &task, nil
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
	return &TaskListResult{Total: total, Page: page, PageSize: pageSize, Items: items}, nil
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
	ctx, cancel := contextWithTimeout(30 * time.Minute)
	defer cancel()
	if err := t.execute(ctx, &task); err != nil {
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
			if err := t.runPending(ctx); err != nil {
				t.log.Warn("执行维护任务失败", "error", err)
			}
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
		if err := t.execute(ctx, &task); err != nil {
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
func (t *Tasks) execute(ctx context.Context, task *model.MaintenanceTask) error {
	// 原子认领: 只有 pending / failed 的任务能被执行, 避免调度与手动执行重复运行同一任务.
	claim := t.db.Model(&model.MaintenanceTask{}).
		Where("id = ? AND status IN ?", task.ID, []string{model.TaskPending, model.TaskFailed}).
		Updates(map[string]any{
			"status":      model.TaskRunning,
			"attempts":    gorm.Expr("attempts + 1"),
			"finished_at": nil,
			"last_error":  "",
		})
	if claim.Error != nil {
		return claim.Error
	}
	if claim.RowsAffected == 0 {
		return errTaskClaimed
	}

	switch task.Type {
	case model.TaskPolicyScan:
		var payload struct {
			PolicyID int64 `json:"policy_id"`
			UserID   int64 `json:"user_id"`
		}
		if err := json.Unmarshal([]byte(task.Payload), &payload); err != nil {
			return fmt.Errorf("任务参数非法: %w", err)
		}
		result, err := t.policies.Scan(ctx, payload.PolicyID, payload.UserID, nil, false)
		if err != nil {
			return err
		}
		t.log.Info("策略扫描完成", "policy_id", payload.PolicyID, "images", result.Images, "files", result.Files, "skipped", result.Skipped)
		return nil

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
		adapter, err := t.policies.Adapter(policy)
		if err != nil {
			return err
		}
		if !adapter.Capabilities().Has(storage.CapDelete) {
			return errors.New("后端未声明 delete 能力")
		}
		return adapter.Delete(ctx, payload.ObjectKey)

	default:
		return fmt.Errorf("未知任务类型: %s", task.Type)
	}
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
	if err := t.db.Where("status IN ? AND created_at < ?", []string{model.TaskDone, model.TaskFailed}, taskCutoff).
		Delete(&model.MaintenanceTask{}).Error; err != nil {
		return err
	}

	t.log.Info("清理过期日志与已结束任务完成")
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

// markFailed 标记任务失败: 未超过重试上限时回到待处理.
func (t *Tasks) markFailed(task *model.MaintenanceTask, cause error) {
	if errors.Is(cause, errTaskClaimed) {
		return
	}
	attempts := task.Attempts + 1
	status := model.TaskFailed
	var finishedAt *time.Time
	if attempts < taskMaxAttempts {
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
