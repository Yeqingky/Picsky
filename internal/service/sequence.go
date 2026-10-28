package service

import (
	"encoding/json"
	"errors"
	"math"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/yeqingky/Picsky/internal/apperr"
	"github.com/yeqingky/Picsky/internal/model"
	"github.com/yeqingky/Picsky/internal/storage"
)

// sequenceAllocation 是一次序号分配结果; FromReleased 表示该值来自释放队列.
type sequenceAllocation struct {
	Value        uint64
	FromReleased bool
}

// ensurePolicySequence 读取当前模板的计数器; 不存在时按已有记录初始化, 模板变化时重置.
func ensurePolicySequence(db *gorm.DB, policy *model.StoragePolicy) (*model.PolicySequence, error) {
	for range 4 {
		var row model.PolicySequence
		err := db.Where("policy_id = ?", policy.ID).First(&row).Error
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			max, err := maxSequenceInUse(db, policy)
			if err != nil {
				return nil, err
			}
			row = model.PolicySequence{
				PolicyID:     policy.ID,
				PathTemplate: policy.PathTemplate,
				Allocated:    max,
				Released:     "[]",
			}
			if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
				return nil, apperr.ErrInternal.WithMessage("初始化序号计数器失败: " + err.Error())
			}
			continue
		case err != nil:
			return nil, apperr.ErrInternal.WithMessage("读取序号计数器失败: " + err.Error())
		}
		if row.PathTemplate == policy.PathTemplate {
			return &row, nil
		}
		// 模板变化后旧计数与释放队列不再适用: 按新模板下已有路径重新初始化.
		max, err := maxSequenceInUse(db, policy)
		if err != nil {
			return nil, err
		}
		result := db.Model(&model.PolicySequence{}).
			Where("policy_id = ? AND path_template = ?", policy.ID, row.PathTemplate).
			Updates(map[string]any{
				"path_template": policy.PathTemplate,
				"allocated":     max,
				"released":      "[]",
			})
		if result.Error != nil {
			return nil, apperr.ErrInternal.WithMessage("重置序号计数器失败: " + result.Error.Error())
		}
	}
	return nil, apperr.ErrInternal.WithMessage("序号计数器并发更新失败")
}

// allocatePolicySequence 分配一个序号; 开启复用且释放队列非空时优先取队首.
func allocatePolicySequence(db *gorm.DB, policy *model.StoragePolicy, excluded map[uint64]struct{}) (sequenceAllocation, error) {
	if _, err := ensurePolicySequence(db, policy); err != nil {
		return sequenceAllocation{}, err
	}
	if policy.ReuseReleasedSeq {
		for range 8 {
			var row model.PolicySequence
			if err := db.Where("policy_id = ?", policy.ID).First(&row).Error; err != nil {
				return sequenceAllocation{}, apperr.ErrInternal.WithMessage("读取释放序号失败: " + err.Error())
			}
			released, err := decodeReleasedSequences(row.Released)
			if err != nil {
				return sequenceAllocation{}, err
			}
			index := -1
			for i, value := range released {
				if _, skip := excluded[value]; skip {
					continue
				}
				index = i
				break
			}
			if index < 0 {
				break
			}
			next := make([]uint64, 0, len(released)-1)
			next = append(next, released[:index]...)
			next = append(next, released[index+1:]...)
			encoded, err := encodeReleasedSequences(next)
			if err != nil {
				return sequenceAllocation{}, err
			}
			result := db.Model(&model.PolicySequence{}).
				Where("policy_id = ? AND path_template = ? AND released = ?", policy.ID, row.PathTemplate, row.Released).
				Update("released", encoded)
			if result.Error != nil {
				return sequenceAllocation{}, apperr.ErrInternal.WithMessage("取出释放序号失败: " + result.Error.Error())
			}
			if result.RowsAffected == 0 {
				continue
			}
			return sequenceAllocation{Value: released[index], FromReleased: true}, nil
		}
	}

	var allocated int64
	err := db.Raw(
		`UPDATE policy_sequences SET allocated = allocated + 1 WHERE policy_id = ? AND path_template = ? RETURNING allocated`,
		policy.ID, policy.PathTemplate,
	).Scan(&allocated).Error
	if err != nil {
		return sequenceAllocation{}, apperr.ErrInternal.WithMessage("分配递增序号失败: " + err.Error())
	}
	if allocated < 1 {
		return sequenceAllocation{}, apperr.ErrPathConflict.WithMessage("递增序号无效")
	}
	return sequenceAllocation{Value: uint64(allocated), FromReleased: false}, nil
}

// releasePolicySequence 把序号追加到释放队列尾部; 重复写入保持幂等.
func releasePolicySequence(db *gorm.DB, policy *model.StoragePolicy, value uint64) error {
	if value == 0 || value > math.MaxInt64 {
		return apperr.ErrPathConflict.WithMessage("释放序号超出范围")
	}
	for range 8 {
		var row model.PolicySequence
		err := db.Where("policy_id = ? AND path_template = ?", policy.ID, policy.PathTemplate).First(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return apperr.ErrInternal.WithMessage("读取释放序号失败: " + err.Error())
		}
		released, err := decodeReleasedSequences(row.Released)
		if err != nil {
			return err
		}
		if containsSequence(released, value) {
			return nil
		}
		next := append(released, value)
		encoded, err := encodeReleasedSequences(next)
		if err != nil {
			return err
		}
		result := db.Model(&model.PolicySequence{}).
			Where("policy_id = ? AND path_template = ? AND released = ?", policy.ID, row.PathTemplate, row.Released).
			Updates(map[string]any{
				"released": encoded,
				// 释放值必须落在已分配范围内, 避免后续新号与释放号重叠.
				"allocated": gorm.Expr("CASE WHEN allocated > ? THEN allocated ELSE ? END", int64(value), int64(value)),
			})
		if result.Error != nil {
			return apperr.ErrInternal.WithMessage("写入释放序号失败: " + result.Error.Error())
		}
		if result.RowsAffected > 0 {
			return nil
		}
	}
	return apperr.ErrInternal.WithMessage("释放序号并发更新失败")
}

// reconcilePolicySequence 在扫描补录后把计数器抬到当前模板下已有路径的最大序号.
func reconcilePolicySequence(db *gorm.DB, policy *model.StoragePolicy) error {
	if _, err := ensurePolicySequence(db, policy); err != nil {
		return err
	}
	max, err := maxSequenceInUse(db, policy)
	if err != nil {
		return err
	}
	if max == 0 {
		return nil
	}
	return db.Model(&model.PolicySequence{}).
		Where("policy_id = ? AND path_template = ? AND allocated < ?", policy.ID, policy.PathTemplate, max).
		Update("allocated", max).Error
}

// maxSequenceInUse 返回策略当前模板下已有路径中的最大序号; 无匹配时返回 0.
func maxSequenceInUse(db *gorm.DB, policy *model.StoragePolicy) (int64, error) {
	matcher, err := storage.NewSequenceMatcher(policy.PathTemplate)
	if err != nil {
		return 0, apperr.ErrInternal.WithMessage("解析序号模板失败: " + err.Error())
	}
	if matcher == nil {
		return 0, nil
	}
	rows, err := db.Model(&model.Image{}).
		Select("rel_path").
		Where("policy_id = ?", policy.ID).
		Rows()
	if err != nil {
		return 0, apperr.ErrInternal.WithMessage("读取递增序号失败: " + err.Error())
	}
	defer rows.Close()

	var highest uint64
	for rows.Next() {
		var relPath string
		if err := rows.Scan(&relPath); err != nil {
			return 0, apperr.ErrInternal.WithMessage("读取递增序号失败: " + err.Error())
		}
		value, ok := matcher.Extract(relPath)
		if !ok || value <= highest {
			continue
		}
		highest = value
	}
	if err := rows.Err(); err != nil {
		return 0, apperr.ErrInternal.WithMessage("读取递增序号失败: " + err.Error())
	}
	if highest > math.MaxInt64 {
		return 0, apperr.ErrPathConflict.WithMessage("递增序号已达到上限")
	}
	return int64(highest), nil
}

func decodeReleasedSequences(raw string) ([]uint64, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var values []uint64
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil, apperr.ErrInternal.WithMessage("释放序号数据损坏: " + err.Error())
	}
	return values, nil
}

func encodeReleasedSequences(values []uint64) (string, error) {
	if values == nil {
		values = []uint64{}
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return "", apperr.ErrInternal.WithMessage("编码释放序号失败: " + err.Error())
	}
	return string(encoded), nil
}

func containsSequence(values []uint64, target uint64) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
