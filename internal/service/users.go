package service

import (
	"errors"
	"strings"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"github.com/yeqingky/Picsky/internal/apperr"
	"github.com/yeqingky/Picsky/internal/model"
)

// Users 管理用户: 创建, 禁用, 改组, 配额覆盖, 默认策略与删除.
type Users struct {
	db       *gorm.DB
	settings *Settings
	audit    *Audit
	images   *Images
}

// NewUsers 创建用户服务.
func NewUsers(database *gorm.DB, settings *Settings, audit *Audit, images *Images) *Users {
	return &Users{db: database, settings: settings, audit: audit, images: images}
}

// UserInput 是用户创建/更新入参; 指针字段为 nil 表示不修改.
type UserInput struct {
	Username        string  `json:"username"`
	Email           string  `json:"email"`
	Password        string  `json:"password"`
	Nickname        string  `json:"nickname"`
	GroupID         *int64  `json:"group_id"`
	QuotaMode       *string `json:"quota_mode"`
	QuotaValue      *int64  `json:"quota_value"`
	DefaultPolicyID *int64  `json:"default_policy_id"`
	Status          *int16  `json:"status"`
}

// UserView 是用户管理视图.
type UserView struct {
	model.User
	GroupName     string `json:"group_name"`
	EmailVerified bool   `json:"email_verified"`
	TOTPEnabled   bool   `json:"totp_enabled"`
}

// UserListFilter 是用户列表过滤条件.
type UserListFilter struct {
	Page     int
	PageSize int
	Keyword  string
	GroupID  *int64
	Status   *int16
}

// UserListResult 是用户分页结果.
type UserListResult struct {
	Total    int64      `json:"total"`
	Page     int        `json:"page"`
	PageSize int        `json:"page_size"`
	Items    []UserView `json:"items"`
}

// List 返回用户列表.
func (u *Users) List(filter UserListFilter) (*UserListResult, error) {
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

	query := u.db.Model(&model.User{})
	if keyword := strings.TrimSpace(filter.Keyword); keyword != "" {
		like := "%" + keyword + "%"
		query = query.Where("username LIKE ? OR email LIKE ? OR nickname LIKE ?", like, like, like)
	}
	if filter.GroupID != nil {
		query = query.Where("group_id = ?", *filter.GroupID)
	}
	if filter.Status != nil {
		query = query.Where("status = ?", *filter.Status)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	var users []model.User
	if err := query.Order("id ASC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&users).Error; err != nil {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	views, err := u.views(users)
	if err != nil {
		return nil, err
	}
	return &UserListResult{Total: total, Page: page, PageSize: pageSize, Items: views}, nil
}

// View 组装单个用户视图.
func (u *Users) View(user *model.User) (*UserView, error) {
	views, err := u.views([]model.User{*user})
	if err != nil {
		return nil, err
	}
	return &views[0], nil
}

// Create 创建用户.
func (u *Users) Create(in UserInput, actor *Principal, ip, userAgent string) (*model.User, error) {
	username := strings.TrimSpace(in.Username)
	email := strings.TrimSpace(in.Email)
	if err := validateUsername(username); err != nil {
		return nil, err
	}
	if err := validateEmail(email); err != nil {
		return nil, err
	}
	if err := validatePassword(in.Password); err != nil {
		return nil, err
	}
	var duplicate int64
	if err := u.db.Model(&model.User{}).Where("username = ?", username).Count(&duplicate).Error; err != nil {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	if duplicate > 0 {
		return nil, apperr.ErrInvalidRequest.WithMessage("用户名已被占用")
	}
	if err := u.db.Model(&model.User{}).Where("email = ?", email).Count(&duplicate).Error; err != nil {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	if duplicate > 0 {
		return nil, apperr.ErrInvalidRequest.WithMessage("邮箱已被占用")
	}

	groupID := int64(0)
	if in.GroupID != nil {
		groupID = *in.GroupID
	}
	if groupID <= 0 {
		return nil, apperr.ErrInvalidRequest.WithMessage("必须指定用户组")
	}
	var group model.Group
	if err := u.db.First(&group, groupID).Error; err != nil {
		return nil, apperr.ErrInvalidRequest.WithMessage("用户组不存在")
	}

	quotaMode := normalizeQuotaMode(in.QuotaMode)
	if quotaMode != nil && *quotaMode != model.QuotaModeCount && *quotaMode != model.QuotaModeBytes {
		return nil, apperr.ErrInvalidRequest.WithMessage("配额方式只能是 count 或 bytes")
	}
	if in.QuotaValue != nil && *in.QuotaValue < 0 {
		return nil, apperr.ErrInvalidRequest.WithMessage("配额数值不能为负数")
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcryptCost)
	if err != nil {
		return nil, apperr.ErrInternal.WithMessage("密码散列失败: " + err.Error())
	}
	now := nowUTC()
	user := model.User{
		Username:        username,
		Email:           email,
		PasswordHash:    string(hashed),
		EmailVerifiedAt: &now,
		Nickname:        strings.TrimSpace(in.Nickname),
		GroupID:         groupID,
		QuotaMode:       quotaMode,
		QuotaValue:      in.QuotaValue,
		Status:          model.UserStatusNormal,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if in.DefaultPolicyID != nil && *in.DefaultPolicyID > 0 {
		user.DefaultPolicyID = in.DefaultPolicyID
	}
	if in.Status != nil {
		user.Status = *in.Status
	}
	if err := u.db.Create(&user).Error; err != nil {
		return nil, apperr.ErrInternal.WithMessage("创建用户失败: " + err.Error())
	}
	u.audit.Recordf(Entry{
		UserID:     actorID(actor),
		Action:     "user_create",
		TargetType: "user",
		TargetID:   &user.ID,
		Detail:     map[string]any{"username": user.Username, "group_id": user.GroupID},
		IP:         ip,
		UserAgent:  userAgent,
	})
	return &user, nil
}

// Update 更新用户.
func (u *Users) Update(id int64, in UserInput, actor *Principal, ip, userAgent string) (*model.User, error) {
	var user model.User
	if err := u.db.First(&user, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperr.ErrNotFound.WithMessage("用户不存在")
		}
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}

	updates := map[string]any{"updated_at": nowUTC()}
	if in.Nickname != "" {
		updates["nickname"] = strings.TrimSpace(in.Nickname)
	}
	if in.GroupID != nil {
		var group model.Group
		if err := u.db.First(&group, *in.GroupID).Error; err != nil {
			return nil, apperr.ErrInvalidRequest.WithMessage("用户组不存在")
		}
		updates["group_id"] = *in.GroupID
	}
	if in.Status != nil {
		if *in.Status != model.UserStatusNormal && *in.Status != model.UserStatusDisabled {
			return nil, apperr.ErrInvalidRequest.WithMessage("状态只能是 0 (正常) 或 1 (禁用)")
		}
		updates["status"] = *in.Status
	}
	if in.QuotaMode != nil {
		mode := normalizeQuotaMode(in.QuotaMode)
		if mode != nil && *mode != model.QuotaModeCount && *mode != model.QuotaModeBytes {
			return nil, apperr.ErrInvalidRequest.WithMessage("配额方式只能是 count 或 bytes")
		}
		if in.QuotaValue != nil && *in.QuotaValue < 0 {
			return nil, apperr.ErrInvalidRequest.WithMessage("配额数值不能为负数")
		}
		updates["quota_mode"] = mode
		if mode == nil {
			updates["quota_value"] = nil
		} else {
			updates["quota_value"] = in.QuotaValue
		}
	} else if in.QuotaValue != nil {
		if *in.QuotaValue < 0 {
			return nil, apperr.ErrInvalidRequest.WithMessage("配额数值不能为负数")
		}
		updates["quota_value"] = in.QuotaValue
	}
	if in.DefaultPolicyID != nil {
		if *in.DefaultPolicyID <= 0 {
			updates["default_policy_id"] = nil
		} else {
			var policy model.StoragePolicy
			if err := u.db.First(&policy, *in.DefaultPolicyID).Error; err != nil {
				return nil, apperr.ErrInvalidRequest.WithMessage("存储策略不存在")
			}
			updates["default_policy_id"] = *in.DefaultPolicyID
		}
	}
	if strings.TrimSpace(in.Password) != "" {
		if err := validatePassword(in.Password); err != nil {
			return nil, err
		}
		hashed, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcryptCost)
		if err != nil {
			return nil, apperr.ErrInternal.WithMessage("密码散列失败: " + err.Error())
		}
		updates["password_hash"] = string(hashed)
	}

	if err := u.db.Model(&model.User{}).Where("id = ?", user.ID).Updates(updates).Error; err != nil {
		return nil, apperr.ErrInternal.WithMessage("更新用户失败: " + err.Error())
	}
	u.audit.Recordf(Entry{
		UserID:     actorID(actor),
		Action:     "user_update",
		TargetType: "user",
		TargetID:   &user.ID,
		Detail:     map[string]any{"username": user.Username},
		IP:         ip,
		UserAgent:  userAgent,
	})
	return u.UserByID(user.ID)
}

// Delete 删除用户: 其活动记录级联软删并按删除流程处理外部对象, 审计日志保留.
func (u *Users) Delete(id int64, actor *Principal, ip, userAgent string) error {
	var user model.User
	if err := u.db.First(&user, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return apperr.ErrNotFound.WithMessage("用户不存在")
		}
		return apperr.ErrInternal.WithMessage(err.Error())
	}
	if actor != nil && actor.User != nil && actor.User.ID == user.ID {
		return apperr.ErrInvalidRequest.WithMessage("不能删除当前登录账号")
	}

	var records []model.Image
	if err := u.db.Where("user_id = ? AND deleted_at IS NULL", user.ID).Find(&records).Error; err != nil {
		return apperr.ErrInternal.WithMessage(err.Error())
	}
	owner := &Principal{User: &user, Permissions: model.AllPermissions()}
	for idx := range records {
		if _, err := u.images.deleteRecord(owner, &records[idx], ip, userAgent); err != nil {
			apiErr := apperr.From(err)
			return apiErr.WithMessage("删除用户记录失败: " + apiErr.Message)
		}
	}

	if err := u.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("user_id = ?", user.ID).Delete(&model.APIKey{}).Error; err != nil {
			return err
		}
		return tx.Delete(&model.User{}, user.ID).Error
	}); err != nil {
		return apperr.ErrInternal.WithMessage("删除用户失败: " + err.Error())
	}
	u.audit.Recordf(Entry{
		UserID:     actorID(actor),
		Action:     "user_delete",
		TargetType: "user",
		TargetID:   &user.ID,
		Detail:     map[string]any{"username": user.Username, "records": len(records)},
		IP:         ip,
		UserAgent:  userAgent,
	})
	return nil
}

// UserByID 读取用户.
func (u *Users) UserByID(id int64) (*model.User, error) {
	var user model.User
	if err := u.db.First(&user, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperr.ErrNotFound.WithMessage("用户不存在")
		}
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	return &user, nil
}

// views 批量组装用户视图.
func (u *Users) views(users []model.User) ([]UserView, error) {
	views := make([]UserView, 0, len(users))
	if len(users) == 0 {
		return views, nil
	}
	groupNames := map[int64]string{}
	ids := make([]int64, 0, len(users))
	for _, user := range users {
		ids = append(ids, user.GroupID)
	}
	var groups []model.Group
	if err := u.db.Where("id IN ?", ids).Find(&groups).Error; err != nil {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	for _, group := range groups {
		groupNames[group.ID] = group.Name
	}
	for _, user := range users {
		views = append(views, UserView{
			User:          user,
			GroupName:     groupNames[user.GroupID],
			EmailVerified: user.EmailVerifiedAt != nil,
			TOTPEnabled:   user.TOTPSecret != "",
		})
	}
	return views, nil
}
