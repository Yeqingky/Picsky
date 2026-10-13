package service

import (
	"errors"
	"strings"

	"gorm.io/gorm"

	"github.com/yeqingky/Picsky/internal/apperr"
	"github.com/yeqingky/Picsky/internal/model"
)

// Groups 管理用户组: 权限位, 配额, 限额与策略白名单.
type Groups struct {
	db       *gorm.DB
	settings *Settings
	audit    *Audit
}

// NewGroups 创建用户组服务.
func NewGroups(database *gorm.DB, settings *Settings, audit *Audit) *Groups {
	return &Groups{db: database, settings: settings, audit: audit}
}

// GroupInput 是用户组创建/更新入参.
type GroupInput struct {
	Name             string   `json:"name"`
	Description      string   `json:"description"`
	IsDefault        *bool    `json:"is_default"`
	Permissions      []string `json:"permissions"`
	QuotaMode        *string  `json:"quota_mode"`
	QuotaValue       *int64   `json:"quota_value"`
	MaxFileSizeBytes *int64   `json:"max_file_size_bytes"`
	AllowedFormats   string   `json:"allowed_formats"`
	UploadRateLimit  *int     `json:"upload_rate_limit"`
	Sort             int      `json:"sort"`
	PolicyIDs        []int64  `json:"policy_ids"`
}

// GroupView 是用户组视图.
type GroupView struct {
	model.Group
	PermissionNames []string `json:"permissions"`
	PermissionBits  int64    `json:"permission_bits"`
	UserCount       int64    `json:"user_count"`
	PolicyIDs       []int64  `json:"policy_ids"`
}

// List 返回全部用户组.
func (g *Groups) List() ([]GroupView, error) {
	var groups []model.Group
	if err := g.db.Order("sort ASC, id ASC").Find(&groups).Error; err != nil {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	views := make([]GroupView, 0, len(groups))
	for _, group := range groups {
		view, err := g.view(&group)
		if err != nil {
			return nil, err
		}
		views = append(views, *view)
	}
	return views, nil
}

// Get 读取用户组.
func (g *Groups) Get(id int64) (*model.Group, error) {
	var group model.Group
	if err := g.db.First(&group, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperr.ErrNotFound.WithMessage("用户组不存在")
		}
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	return &group, nil
}

// View 组装用户组视图.
func (g *Groups) View(group *model.Group) (*GroupView, error) { return g.view(group) }

// Create 创建用户组.
func (g *Groups) Create(in GroupInput, actor *Principal, ip, userAgent string) (*model.Group, error) {
	permissions, err := g.validate(&in, nil)
	if err != nil {
		return nil, err
	}
	group := model.Group{
		Name:             in.Name,
		Description:      strings.TrimSpace(in.Description),
		IsDefault:        in.IsDefault != nil && *in.IsDefault,
		Permissions:      permissions,
		QuotaMode:        normalizeQuotaMode(in.QuotaMode),
		QuotaValue:       in.QuotaValue,
		MaxFileSizeBytes: in.MaxFileSizeBytes,
		AllowedFormats:   normalizeFormatList(in.AllowedFormats),
		UploadRateLimit:  in.UploadRateLimit,
		Sort:             in.Sort,
		CreatedAt:        nowUTC(),
		UpdatedAt:        nowUTC(),
	}
	if err := g.db.Transaction(func(tx *gorm.DB) error {
		if group.IsDefault {
			if err := tx.Model(&model.Group{}).Where("is_default = ?", true).Update("is_default", false).Error; err != nil {
				return err
			}
		}
		if err := tx.Create(&group).Error; err != nil {
			return err
		}
		return g.replacePolicies(tx, group.ID, in.PolicyIDs)
	}); err != nil {
		return nil, apperr.ErrInternal.WithMessage("创建用户组失败: " + err.Error())
	}

	g.audit.Recordf(Entry{
		UserID:     actorID(actor),
		Action:     "group_create",
		TargetType: "group",
		TargetID:   &group.ID,
		Detail:     map[string]any{"name": group.Name},
		IP:         ip,
		UserAgent:  userAgent,
	})
	return &group, nil
}

// Update 更新用户组.
func (g *Groups) Update(id int64, in GroupInput, actor *Principal, ip, userAgent string) (*model.Group, error) {
	group, err := g.Get(id)
	if err != nil {
		return nil, err
	}
	if in.Name == "" {
		in.Name = group.Name
	}
	permissions, err := g.validate(&in, group)
	if err != nil {
		return nil, err
	}
	isDefault := group.IsDefault
	if in.IsDefault != nil {
		isDefault = *in.IsDefault
	}
	updates := map[string]any{
		"name":                in.Name,
		"description":         strings.TrimSpace(in.Description),
		"is_default":          isDefault,
		"permissions":         int64(permissions),
		"quota_mode":          normalizeQuotaMode(in.QuotaMode),
		"quota_value":         in.QuotaValue,
		"max_file_size_bytes": in.MaxFileSizeBytes,
		"allowed_formats":     normalizeFormatList(in.AllowedFormats),
		"upload_rate_limit":   in.UploadRateLimit,
		"sort":                in.Sort,
		"updated_at":          nowUTC(),
	}
	if err := g.db.Transaction(func(tx *gorm.DB) error {
		if isDefault {
			if err := tx.Model(&model.Group{}).Where("is_default = ? AND id <> ?", true, group.ID).Update("is_default", false).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&model.Group{}).Where("id = ?", group.ID).Updates(updates).Error; err != nil {
			return err
		}
		if in.PolicyIDs != nil {
			return g.replacePolicies(tx, group.ID, in.PolicyIDs)
		}
		return nil
	}); err != nil {
		return nil, apperr.ErrInternal.WithMessage("更新用户组失败: " + err.Error())
	}

	g.audit.Recordf(Entry{
		UserID:     actorID(actor),
		Action:     "group_update",
		TargetType: "group",
		TargetID:   &group.ID,
		Detail:     map[string]any{"name": in.Name, "permissions": permissions.Names()},
		IP:         ip,
		UserAgent:  userAgent,
	})
	return g.Get(group.ID)
}

// Delete 删除用户组; 仍有关联用户时拒绝.
func (g *Groups) Delete(id int64, actor *Principal, ip, userAgent string) error {
	group, err := g.Get(id)
	if err != nil {
		return err
	}
	var userCount int64
	if err := g.db.Model(&model.User{}).Where("group_id = ?", group.ID).Count(&userCount).Error; err != nil {
		return apperr.ErrInternal.WithMessage(err.Error())
	}
	if userCount > 0 {
		return apperr.ErrInvalidRequest.WithMessage("用户组仍有用户, 不能删除")
	}
	if group.IsDefault {
		return apperr.ErrInvalidRequest.WithMessage("默认用户组不能删除")
	}
	if err := g.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("group_id = ?", group.ID).Delete(&model.GroupPolicy{}).Error; err != nil {
			return err
		}
		return tx.Delete(&model.Group{}, group.ID).Error
	}); err != nil {
		return apperr.ErrInternal.WithMessage("删除用户组失败: " + err.Error())
	}
	g.audit.Recordf(Entry{
		UserID:     actorID(actor),
		Action:     "group_delete",
		TargetType: "group",
		TargetID:   &group.ID,
		Detail:     map[string]any{"name": group.Name},
		IP:         ip,
		UserAgent:  userAgent,
	})
	return nil
}

// SetPolicies 覆盖组的策略白名单.
func (g *Groups) SetPolicies(id int64, policyIDs []int64, actor *Principal, ip, userAgent string) error {
	group, err := g.Get(id)
	if err != nil {
		return err
	}
	if err := g.db.Transaction(func(tx *gorm.DB) error {
		return g.replacePolicies(tx, group.ID, policyIDs)
	}); err != nil {
		return apperr.ErrInternal.WithMessage("更新策略白名单失败: " + err.Error())
	}
	g.audit.Recordf(Entry{
		UserID:     actorID(actor),
		Action:     "group_policies_update",
		TargetType: "group",
		TargetID:   &group.ID,
		Detail:     map[string]any{"policy_ids": policyIDs},
		IP:         ip,
		UserAgent:  userAgent,
	})
	return nil
}

// PolicyIDsFor 返回组的策略白名单.
func (g *Groups) PolicyIDsFor(groupID int64) ([]int64, error) {
	var rows []model.GroupPolicy
	if err := g.db.Where("group_id = ?", groupID).Find(&rows).Error; err != nil {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.PolicyID)
	}
	return ids, nil
}

func (g *Groups) view(group *model.Group) (*GroupView, error) {
	policyIDs, err := g.PolicyIDsFor(group.ID)
	if err != nil {
		return nil, err
	}
	var userCount int64
	if err := g.db.Model(&model.User{}).Where("group_id = ?", group.ID).Count(&userCount).Error; err != nil {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	return &GroupView{
		Group:           *group,
		PermissionNames: group.Permissions.Names(),
		PermissionBits:  int64(group.Permissions),
		UserCount:       userCount,
		PolicyIDs:       policyIDs,
	}, nil
}

// validate 校验用户组入参.
func (g *Groups) validate(in *GroupInput, group *model.Group) (model.Permission, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return 0, apperr.ErrInvalidRequest.WithMessage("用户组名称不能为空")
	}
	var duplicate int64
	query := g.db.Model(&model.Group{}).Where("name = ?", in.Name)
	if group != nil {
		query = query.Where("id <> ?", group.ID)
	}
	if err := query.Count(&duplicate).Error; err != nil {
		return 0, apperr.ErrInternal.WithMessage(err.Error())
	}
	if duplicate > 0 {
		return 0, apperr.ErrInvalidRequest.WithMessage("用户组名称已存在")
	}

	permissions, err := model.PermissionsFromNames(in.Permissions)
	if err != nil {
		return 0, apperr.ErrInvalidRequest.WithMessage(err.Error())
	}
	if in.QuotaMode != nil {
		mode := strings.TrimSpace(*in.QuotaMode)
		switch mode {
		case "", model.QuotaModeCount, model.QuotaModeBytes:
		default:
			return 0, apperr.ErrInvalidRequest.WithMessage("配额方式只能是 count 或 bytes")
		}
	}
	if in.QuotaValue != nil && *in.QuotaValue < 0 {
		return 0, apperr.ErrInvalidRequest.WithMessage("配额数值不能为负数")
	}
	if in.MaxFileSizeBytes != nil && *in.MaxFileSizeBytes < 0 {
		return 0, apperr.ErrInvalidRequest.WithMessage("单文件上限不能为负数")
	}
	if in.UploadRateLimit != nil && *in.UploadRateLimit < 0 {
		return 0, apperr.ErrInvalidRequest.WithMessage("上传速率限制不能为负数")
	}
	if err := validateFormatSubset(in.AllowedFormats, g.settings); err != nil {
		return 0, err
	}
	return permissions, nil
}

// replacePolicies 覆盖写入组策略白名单.
func (g *Groups) replacePolicies(tx *gorm.DB, groupID int64, policyIDs []int64) error {
	if err := tx.Where("group_id = ?", groupID).Delete(&model.GroupPolicy{}).Error; err != nil {
		return err
	}
	rows := make([]model.GroupPolicy, 0, len(policyIDs))
	seen := map[int64]bool{}
	for _, policyID := range policyIDs {
		if policyID <= 0 || seen[policyID] {
			continue
		}
		seen[policyID] = true
		rows = append(rows, model.GroupPolicy{GroupID: groupID, PolicyID: policyID})
	}
	if len(rows) == 0 {
		return nil
	}
	return tx.Create(&rows).Error
}

// validateFormatSubset 校验组格式白名单是系统基线的子集且不含 SVG.
func validateFormatSubset(raw string, settings *Settings) error {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	baseline := splitFormats(settings.String("default_allowed_formats"))
	baselineSet := map[string]bool{}
	for _, item := range baseline {
		baselineSet[item] = true
	}
	for _, item := range splitFormats(raw) {
		if item == "svg" {
			return apperr.ErrInvalidRequest.WithMessage("系统级禁止 SVG, 白名单不可包含")
		}
		if !baselineSet[item] {
			return apperr.ErrInvalidRequest.WithMessage("格式 " + item + " 不在系统基线内, 用户组只能收紧")
		}
	}
	return nil
}

// normalizeFormatList 规范化格式列表.
func normalizeFormatList(raw string) string {
	return strings.Join(splitFormats(raw), ",")
}

// normalizeQuotaMode 规范化配额方式.
func normalizeQuotaMode(mode *string) *string {
	if mode == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*mode)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func actorID(actor *Principal) *int64 {
	if actor == nil || actor.User == nil {
		return nil
	}
	id := actor.User.ID
	return &id
}

// ownerID 返回主体的用户 ID, 无主体时为 0 (系统记录).
func ownerID(actor *Principal) int64 {
	if actor == nil || actor.User == nil {
		return 0
	}
	return actor.User.ID
}
