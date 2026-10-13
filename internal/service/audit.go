package service

import (
	"encoding/json"
	"time"

	"gorm.io/gorm"

	"github.com/yeqingky/Picsky/internal/apperr"
	"github.com/yeqingky/Picsky/internal/model"
)

// Audit 写入审计日志.
type Audit struct {
	db *gorm.DB
}

// NewAudit 创建审计日志服务.
func NewAudit(database *gorm.DB) *Audit {
	return &Audit{db: database}
}

// Entry 是一条审计日志.
type Entry struct {
	UserID     *int64
	Action     string
	TargetType string
	TargetID   *int64
	Detail     map[string]any
	IP         string
	UserAgent  string
}

// Record 写入审计日志; 日志失败不影响主流程, 由调用方决定是否忽略错误.
func (a *Audit) Record(entry Entry) error {
	detail := ""
	if len(entry.Detail) > 0 {
		encoded, err := json.Marshal(entry.Detail)
		if err != nil {
			return err
		}
		detail = string(encoded)
	}
	row := model.AuditLog{
		UserID:     entry.UserID,
		Action:     entry.Action,
		TargetType: entry.TargetType,
		TargetID:   entry.TargetID,
		Detail:     detail,
		IP:         truncate(entry.IP, 45),
		UserAgent:  truncate(entry.UserAgent, 255),
		CreatedAt:  time.Now().UTC(),
	}
	return a.db.Create(&row).Error
}

// Recordf 是 Record 的便捷包装, 忽略写入错误.
func (a *Audit) Recordf(entry Entry) {
	_ = a.Record(entry)
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

// AuditFilter 是审计日志过滤条件.
type AuditFilter struct {
	Page     int
	PageSize int
	Action   string
	UserID   *int64
	From     *time.Time
	To       *time.Time
}

// AuditItem 是审计日志条目 (附用户名).
type AuditItem struct {
	model.AuditLog
	Username string `json:"username"`
}

// AuditListResult 是审计日志分页结果.
type AuditListResult struct {
	Total    int64       `json:"total"`
	Page     int         `json:"page"`
	PageSize int         `json:"page_size"`
	Items    []AuditItem `json:"items"`
}

// List 查询审计日志.
func (a *Audit) List(filter AuditFilter) (*AuditListResult, error) {
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

	query := a.db.Model(&model.AuditLog{})
	if filter.Action != "" {
		query = query.Where("action = ?", filter.Action)
	}
	if filter.UserID != nil {
		query = query.Where("user_id = ?", *filter.UserID)
	}
	if filter.From != nil {
		query = query.Where("created_at >= ?", filter.From.UTC())
	}
	if filter.To != nil {
		query = query.Where("created_at <= ?", filter.To.UTC())
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	var rows []model.AuditLog
	if err := query.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows).Error; err != nil {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}

	usernames := map[int64]string{}
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		if row.UserID != nil {
			ids = append(ids, *row.UserID)
		}
	}
	if len(ids) > 0 {
		var users []model.User
		if err := a.db.Select("id", "username").Where("id IN ?", ids).Find(&users).Error; err != nil {
			return nil, apperr.ErrInternal.WithMessage(err.Error())
		}
		for _, user := range users {
			usernames[user.ID] = user.Username
		}
	}

	items := make([]AuditItem, 0, len(rows))
	for _, row := range rows {
		username := ""
		if row.UserID != nil {
			username = usernames[*row.UserID]
		}
		items = append(items, AuditItem{AuditLog: row, Username: username})
	}
	return &AuditListResult{Total: total, Page: page, PageSize: pageSize, Items: items}, nil
}

func nowUTC() time.Time { return time.Now().UTC() }
