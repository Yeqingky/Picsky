package service

import (
	"math"
	"path"
	"strconv"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/yeqingky/Picsky/internal/apperr"
	"github.com/yeqingky/Picsky/internal/model"
)

// ensurePolicySequence 在首次分配前用已有记录中的纯数字文件名初始化计数器.
// 多个上传并发首次分配时由 ON CONFLICT DO NOTHING 保证只插入一条.
func ensurePolicySequence(db *gorm.DB, policyID int64) error {
	var count int64
	if err := db.Model(&model.PolicySequence{}).Where("policy_id = ?", policyID).Count(&count).Error; err != nil {
		return apperr.ErrInternal.WithMessage(err.Error())
	}
	if count > 0 {
		return nil
	}
	next, err := nextAfterMaxSequence(db, policyID)
	if err != nil {
		return err
	}
	return db.Clauses(clause.OnConflict{DoNothing: true}).
		Create(&model.PolicySequence{PolicyID: policyID, NextValue: next}).Error
}

// allocatePolicySequence 原子分配并推进策略计数器, 返回值从 1 开始.
func allocatePolicySequence(db *gorm.DB, policyID int64) (uint64, error) {
	if err := ensurePolicySequence(db, policyID); err != nil {
		return 0, err
	}
	var next int64
	err := db.Raw(`INSERT INTO policy_sequences (policy_id, next_value) VALUES (?, 2)
ON CONFLICT (policy_id) DO UPDATE SET next_value = policy_sequences.next_value + 1
RETURNING next_value - 1`, policyID).Scan(&next).Error
	if err != nil {
		return 0, apperr.ErrInternal.WithMessage("分配递增序号失败: " + err.Error())
	}
	if next < 1 {
		return 0, apperr.ErrPathConflict.WithMessage("递增序号无效")
	}
	return uint64(next), nil
}

// reconcilePolicySequence 在扫描补录后把计数器抬到最大纯数字文件名之后.
func reconcilePolicySequence(db *gorm.DB, policyID int64) error {
	next, err := nextAfterMaxSequence(db, policyID)
	if err != nil {
		return err
	}
	return db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "policy_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"next_value": gorm.Expr(
				"CASE WHEN policy_sequences.next_value > ? THEN policy_sequences.next_value ELSE ? END",
				next, next,
			),
		}),
	}).Create(&model.PolicySequence{PolicyID: policyID, NextValue: next}).Error
}

// nextAfterMaxSequence 返回策略下最大纯数字文件名 + 1; 无匹配时返回 1.
// 目录层级和扩展名不参与序号, 因此同一策略的 jpg/webp/avif 共用一条序列.
func nextAfterMaxSequence(db *gorm.DB, policyID int64) (int64, error) {
	rows, err := db.Model(&model.Image{}).
		Select("object_key").
		Where("policy_id = ?", policyID).
		Rows()
	if err != nil {
		return 0, apperr.ErrInternal.WithMessage("读取递增序号失败: " + err.Error())
	}
	defer rows.Close()

	var highest uint64
	for rows.Next() {
		var objectKey string
		if err := rows.Scan(&objectKey); err != nil {
			return 0, apperr.ErrInternal.WithMessage("读取递增序号失败: " + err.Error())
		}
		base := path.Base(objectKey)
		stem := strings.TrimSuffix(base, path.Ext(base))
		if stem == "" {
			continue
		}
		numeric := true
		for index := range stem {
			if stem[index] < '0' || stem[index] > '9' {
				numeric = false
				break
			}
		}
		if !numeric {
			continue
		}
		value, err := strconv.ParseUint(stem, 10, 64)
		if err == nil && value > highest {
			highest = value
		}
	}
	if err := rows.Err(); err != nil {
		return 0, apperr.ErrInternal.WithMessage("读取递增序号失败: " + err.Error())
	}
	if highest >= math.MaxInt64-1 {
		return 0, apperr.ErrPathConflict.WithMessage("递增序号已达到上限")
	}
	return int64(highest + 1), nil
}
