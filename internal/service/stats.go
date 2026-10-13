package service

import (
	"time"

	"gorm.io/gorm"

	"github.com/yeqingky/Picsky/internal/apperr"
	"github.com/yeqingky/Picsky/internal/model"
)

// Stats 提供后台概览数据.
type Stats struct {
	db       *gorm.DB
	settings *Settings
}

// NewStats 创建统计服务.
func NewStats(database *gorm.DB, settings *Settings) *Stats {
	return &Stats{db: database, settings: settings}
}

// FormatStat 是按格式的分布项.
type FormatStat struct {
	Format string `json:"format"`
	Count  int64  `json:"count"`
	Size   int64  `json:"size"`
}

// PolicyStat 是按策略的分布项.
type PolicyStat struct {
	PolicyID   int64  `json:"policy_id"`
	PolicyName string `json:"policy_name"`
	Count      int64  `json:"count"`
	Size       int64  `json:"size"`
}

// Overview 是概览数据.
type Overview struct {
	ImageCount   int64        `json:"image_count"`
	UsedBytes    int64        `json:"used_bytes"`
	UserCount    int64        `json:"user_count"`
	TodayUploads int64        `json:"today_uploads"`
	ByFormat     []FormatStat `json:"by_format"`
	ByPolicy     []PolicyStat `json:"by_policy"`
}

// Overview 汇总图片总数, 占用空间, 用户数, 今日上传与分布.
func (s *Stats) Overview() (*Overview, error) {
	overview := &Overview{ByFormat: []FormatStat{}, ByPolicy: []PolicyStat{}}

	var aggregate struct {
		Count int64
		Bytes int64
	}
	if err := s.db.Model(&model.Image{}).
		Select("COUNT(*) as count, COALESCE(SUM(size), 0) as bytes").
		Where("kind = ?", model.KindImage).
		Scan(&aggregate).Error; err != nil {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	overview.ImageCount = aggregate.Count
	overview.UsedBytes = aggregate.Bytes

	if err := s.db.Model(&model.User{}).Count(&overview.UserCount).Error; err != nil {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}

	startOfDay := s.startOfToday()
	if err := s.db.Model(&model.Image{}).
		Where("kind = ? AND created_at >= ?", model.KindImage, startOfDay).
		Count(&overview.TodayUploads).Error; err != nil {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}

	var formats []FormatStat
	if err := s.db.Model(&model.Image{}).
		Select("format, COUNT(*) as count, COALESCE(SUM(size), 0) as size").
		Where("kind = ?", model.KindImage).
		Group("format").
		Order("count DESC").
		Scan(&formats).Error; err != nil {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	if formats != nil {
		overview.ByFormat = formats
	}

	var policies []PolicyStat
	if err := s.db.Model(&model.Image{}).
		Select("images.policy_id as policy_id, storage_policies.name as policy_name, COUNT(*) as count, COALESCE(SUM(images.size), 0) as size").
		Joins("LEFT JOIN storage_policies ON storage_policies.id = images.policy_id").
		Where("images.kind = ?", model.KindImage).
		Group("images.policy_id, storage_policies.name").
		Order("count DESC").
		Scan(&policies).Error; err != nil {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	if policies != nil {
		overview.ByPolicy = policies
	}
	return overview, nil
}

// startOfToday 返回系统设置时区下的今日零点 (UTC).
func (s *Stats) startOfToday() time.Time {
	location, err := time.LoadLocation(s.settings.String("timezone"))
	if err != nil {
		location = time.UTC
	}
	now := time.Now().In(location)
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location).UTC()
}
