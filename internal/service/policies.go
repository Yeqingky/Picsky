package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/yeqingky/Picsky/internal/apperr"
	"github.com/yeqingky/Picsky/internal/config"
	"github.com/yeqingky/Picsky/internal/crypto"
	"github.com/yeqingky/Picsky/internal/hashing"
	"github.com/yeqingky/Picsky/internal/imgdetect"
	"github.com/yeqingky/Picsky/internal/model"
	"github.com/yeqingky/Picsky/internal/spool"
	"github.com/yeqingky/Picsky/internal/storage"
)

// 扫描相关限制.
const (
	scanMaxFileSize = 64 << 20 // 扫描时读取内容的体积上限
	scanHeadBytes   = 512
)

// Policies 管理存储策略, 分发域名, 路径渲染与扫描补录.
type Policies struct {
	db       *gorm.DB
	cfg      *config.Config
	settings *Settings
	cipher   *crypto.Cipher
	audit    *Audit
	log      *slog.Logger
	tasks    *Tasks
}

// NewPolicies 创建策略服务.
func NewPolicies(database *gorm.DB, cfg *config.Config, settings *Settings, cipher *crypto.Cipher, audit *Audit, log *slog.Logger) *Policies {
	return &Policies{db: database, cfg: cfg, settings: settings, cipher: cipher, audit: audit, log: log}
}

// SetTasks 注入维护任务队列, 用于创建策略后自动扫描.
func (p *Policies) SetTasks(tasks *Tasks) { p.tasks = tasks }

// URLInput 是分发域名入参.
type URLInput struct {
	Alias     string `json:"alias"`
	URL       string `json:"url"`
	IsDefault bool   `json:"is_default"`
	Sort      int    `json:"sort"`
}

// PolicyInput 是策略创建/更新入参.
type PolicyInput struct {
	Name           string            `json:"name"`
	Type           string            `json:"type"`
	Enabled        *bool             `json:"enabled"`
	PathTemplate   string            `json:"path_template"`
	ConflictPolicy string            `json:"conflict_policy"`
	Config         map[string]string `json:"config"`
	Secrets        map[string]string `json:"secrets"`
	URLs           []URLInput        `json:"urls"`
	Sort           int               `json:"sort"`
}

// PolicyView 是策略的管理视图.
type PolicyView struct {
	*model.StoragePolicy
	URLs           []model.PolicyURL `json:"urls"`
	Capabilities   []string          `json:"capabilities"`
	ConfigValues   map[string]string `json:"config"`
	Secrets        map[string]string `json:"secrets,omitempty"`
	SecretsPresent bool              `json:"secrets_present"`
	DriverLabel    string            `json:"driver_label"`
	ConfigFields   []storage.Field   `json:"config_fields"`
	SecretFields   []storage.Field   `json:"secret_fields"`
}

// ConfigMap 解码非敏感配置.
func (p *Policies) ConfigMap(policy *model.StoragePolicy) (map[string]string, error) {
	values := map[string]string{}
	if strings.TrimSpace(policy.Config) == "" {
		return values, nil
	}
	if err := json.Unmarshal([]byte(policy.Config), &values); err != nil {
		return nil, fmt.Errorf("策略配置解析失败: %w", err)
	}
	return values, nil
}

// SecretsMap 解密敏感配置.
func (p *Policies) SecretsMap(policy *model.StoragePolicy) (map[string]string, error) {
	values := map[string]string{}
	if strings.TrimSpace(policy.Secrets) == "" {
		return values, nil
	}
	plain, err := p.cipher.Decrypt(policy.Secrets)
	if err != nil {
		return nil, fmt.Errorf("策略凭据解密失败: %w", err)
	}
	if err := json.Unmarshal([]byte(plain), &values); err != nil {
		return nil, fmt.Errorf("策略凭据解析失败: %w", err)
	}
	return values, nil
}

// Adapter 实例化策略对应的存储驱动.
func (p *Policies) Adapter(policy *model.StoragePolicy) (storage.Adapter, error) {
	config, err := p.ConfigMap(policy)
	if err != nil {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	secrets, err := p.SecretsMap(policy)
	if err != nil {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	adapter, err := storage.New(policy.Type, storage.Options{Config: config, Secrets: secrets})
	if err != nil {
		return nil, apperr.ErrInternal.WithMessage("初始化存储后端失败: " + err.Error())
	}
	return adapter, nil
}

// URLs 返回策略的分发域名, 默认域名优先.
func (p *Policies) URLs(policyID int64) ([]model.PolicyURL, error) {
	var urls []model.PolicyURL
	if err := p.db.Where("policy_id = ?", policyID).Order("is_default DESC, sort ASC, id ASC").Find(&urls).Error; err != nil {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	return urls, nil
}

// DefaultURL 返回默认分发域名 URL.
func (p *Policies) DefaultURL(policyID int64) (string, error) {
	var row model.PolicyURL
	err := p.db.Where("policy_id = ? AND is_default = ?", policyID, true).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = p.db.Where("policy_id = ?", policyID).Order("sort ASC, id ASC").First(&row).Error
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", apperr.ErrInternal.WithMessage("策略未配置分发域名")
	}
	if err != nil {
		return "", apperr.ErrInternal.WithMessage(err.Error())
	}
	return row.URL, nil
}

// Get 读取策略.
func (p *Policies) Get(id int64) (*model.StoragePolicy, error) {
	var policy model.StoragePolicy
	if err := p.db.First(&policy, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperr.ErrNotFound.WithMessage("存储策略不存在")
		}
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	return &policy, nil
}

// View 组装策略管理视图.
func (p *Policies) View(policy *model.StoragePolicy) (*PolicyView, error) {
	urls, err := p.URLs(policy.ID)
	if err != nil {
		return nil, err
	}
	config, err := p.ConfigMap(policy)
	if err != nil {
		return nil, err
	}
	secrets, err := p.SecretsMap(policy)
	if err != nil {
		// 凭据损坏时不影响管理列表读取, 仅记录日志
		p.log.Warn("策略凭据解密失败", "policy_id", policy.ID, "error", err)
		secrets = map[string]string{}
	}
	spec, _ := storage.SpecOf(policy.Type)
	view := &PolicyView{
		StoragePolicy:  policy,
		URLs:           urls,
		Capabilities:   spec.Capabilities,
		ConfigValues:   config,
		Secrets:        secrets,
		SecretsPresent: strings.TrimSpace(policy.Secrets) != "",
		DriverLabel:    spec.Label,
		ConfigFields:   spec.ConfigFields,
		SecretFields:   spec.SecretFields,
	}
	if view.URLs == nil {
		view.URLs = []model.PolicyURL{}
	}
	return view, nil
}

// List 返回全部策略 (含停用).
func (p *Policies) List() ([]PolicyView, error) {
	var policies []model.StoragePolicy
	if err := p.db.Order("sort ASC, id ASC").Find(&policies).Error; err != nil {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	views := make([]PolicyView, 0, len(policies))
	for i := range policies {
		view, err := p.View(&policies[i])
		if err != nil {
			return nil, err
		}
		views = append(views, *view)
	}
	return views, nil
}

// AllowedFor 返回主体可用的启用策略: 用户组具备 manage_policies 时视为可访问全部策略.
// 判定依据是用户组的权限, 而非 API 密钥的权限子集, 避免受限密钥失去存储可见性.
func (p *Policies) AllowedFor(principal *Principal) ([]model.StoragePolicy, error) {
	query := p.db.Where("enabled = ?", true).Order("sort ASC, id ASC")
	groupHasAll := principal.Group != nil && principal.Group.Permissions.Has(model.PermManagePolicies)
	if !groupHasAll {
		query = query.Where("id IN (?)", p.db.Model(&model.GroupPolicy{}).
			Select("policy_id").
			Where("group_id = ?", principal.Group.ID))
	}
	var policies []model.StoragePolicy
	if err := query.Find(&policies).Error; err != nil {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	return policies, nil
}

// Resolve 选择上传目标策略: 请求值 -> 用户默认 -> 组内第一个可用策略.
func (p *Policies) Resolve(principal *Principal, requestedID *int64) (*model.StoragePolicy, error) {
	if requestedID != nil {
		policy, err := p.Get(*requestedID)
		if err != nil {
			return nil, err
		}
		if !policy.Enabled {
			return nil, apperr.ErrPolicyNotAllowed.WithMessage("存储策略已停用")
		}
		allowed, err := p.AllowedFor(principal)
		if err != nil {
			return nil, err
		}
		for i := range allowed {
			if allowed[i].ID == policy.ID {
				return &allowed[i], nil
			}
		}
		return nil, apperr.ErrPolicyNotAllowed
	}

	if principal.User.DefaultPolicyID != nil {
		policy, err := p.Get(*principal.User.DefaultPolicyID)
		if err == nil && policy.Enabled {
			allowed, allowedErr := p.AllowedFor(principal)
			if allowedErr != nil {
				return nil, allowedErr
			}
			for i := range allowed {
				if allowed[i].ID == policy.ID {
					return &allowed[i], nil
				}
			}
		} else if err != nil && !errors.Is(err, apperr.ErrNotFound) {
			return nil, err
		}
	}

	allowed, err := p.AllowedFor(principal)
	if err != nil {
		return nil, err
	}
	if len(allowed) == 0 {
		return nil, apperr.ErrInvalidRequest.WithMessage("当前用户组没有可用的存储策略")
	}
	return &allowed[0], nil
}

// Validate 校验策略入参.
func (p *Policies) Validate(in *PolicyInput, policy *model.StoragePolicy) error {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return apperr.ErrInvalidRequest.WithMessage("策略名称不能为空")
	}
	if len([]rune(in.Name)) > 64 {
		return apperr.ErrInvalidRequest.WithMessage("策略名称过长")
	}
	spec, ok := storage.SpecOf(in.Type)
	if !ok {
		return apperr.ErrInvalidRequest.WithMessage("未知存储后端类型: " + in.Type)
	}
	if err := storage.ValidateTemplate(in.PathTemplate); err != nil {
		return apperr.ErrInvalidRequest.WithMessage(err.Error())
	}
	switch in.ConflictPolicy {
	case model.ConflictRename, model.ConflictSkip:
	default:
		return apperr.ErrInvalidRequest.WithMessage("未知冲突策略: " + in.ConflictPolicy)
	}
	for _, field := range spec.ConfigFields {
		if field.Required && strings.TrimSpace(in.Config[field.Name]) == "" {
			return apperr.ErrInvalidRequest.WithMessage("缺少必填后端配置: " + field.Label)
		}
	}
	if len(in.URLs) == 0 {
		return apperr.ErrInvalidRequest.WithMessage("至少配置一个分发域名")
	}
	defaultCount := 0
	for i := range in.URLs {
		in.URLs[i].Alias = strings.TrimSpace(in.URLs[i].Alias)
		if in.URLs[i].Alias == "" {
			return apperr.ErrInvalidRequest.WithMessage("分发域名别名不能为空")
		}
		if len([]rune(in.URLs[i].Alias)) > 64 {
			return apperr.ErrInvalidRequest.WithMessage("分发域名别名过长")
		}
		normalized, err := storage.NormalizeURLBase(in.URLs[i].URL)
		if err != nil {
			return apperr.ErrInvalidRequest.WithMessage(err.Error())
		}
		if len(normalized) > 255 {
			return apperr.ErrInvalidRequest.WithMessage("分发域名 URL 过长")
		}
		in.URLs[i].URL = normalized
		if in.URLs[i].IsDefault {
			defaultCount++
		}
	}
	if defaultCount != 1 {
		return apperr.ErrInvalidRequest.WithMessage("必须恰好有一个默认分发域名")
	}
	_ = policy
	return nil
}

// Create 创建策略并自动入队首次扫描.
func (p *Policies) Create(in PolicyInput, actor *Principal, ip, userAgent string) (*model.StoragePolicy, error) {
	if err := p.Validate(&in, nil); err != nil {
		return nil, err
	}
	var existing int64
	if err := p.db.Model(&model.StoragePolicy{}).Where("name = ?", in.Name).Count(&existing).Error; err != nil {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	if existing > 0 {
		return nil, apperr.ErrInvalidRequest.WithMessage("策略名称已存在")
	}

	secrets, err := p.encryptSecrets(in.Secrets)
	if err != nil {
		return nil, err
	}
	configJSON, err := json.Marshal(normalizeMap(in.Config))
	if err != nil {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}

	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	policy := model.StoragePolicy{
		Name:           in.Name,
		Type:           in.Type,
		Enabled:        enabled,
		PathTemplate:   in.PathTemplate,
		ConflictPolicy: in.ConflictPolicy,
		Config:         string(configJSON),
		Secrets:        secrets,
		Sort:           in.Sort,
		CreatedAt:      nowUTC(),
		UpdatedAt:      nowUTC(),
	}
	if err := p.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&policy).Error; err != nil {
			return err
		}
		return p.replaceURLs(tx, policy.ID, in.URLs)
	}); err != nil {
		return nil, apperr.ErrInternal.WithMessage("创建策略失败: " + err.Error())
	}

	var actorID *int64
	if actor != nil {
		actorID = &actor.User.ID
	}
	p.audit.Recordf(Entry{
		UserID:     actorID,
		Action:     "policy_create",
		TargetType: "policy",
		TargetID:   &policy.ID,
		Detail:     map[string]any{"name": policy.Name, "type": policy.Type},
		IP:         ip,
		UserAgent:  userAgent,
	})

	p.enqueueInitialScan(&policy, ownerID(actor))
	return &policy, nil
}

// Update 更新策略.
func (p *Policies) Update(id int64, in PolicyInput, actor *Principal, ip, userAgent string) (*model.StoragePolicy, error) {
	policy, err := p.Get(id)
	if err != nil {
		return nil, err
	}
	if in.Type == "" {
		in.Type = policy.Type
	}
	if in.PathTemplate == "" {
		in.PathTemplate = policy.PathTemplate
	}
	if in.ConflictPolicy == "" {
		in.ConflictPolicy = policy.ConflictPolicy
	}
	if in.Config == nil {
		config, err := p.ConfigMap(policy)
		if err != nil {
			return nil, err
		}
		in.Config = config
	}
	if in.URLs == nil {
		urls, err := p.URLs(policy.ID)
		if err != nil {
			return nil, err
		}
		in.URLs = make([]URLInput, 0, len(urls))
		for _, url := range urls {
			in.URLs = append(in.URLs, URLInput{Alias: url.Alias, URL: url.URL, IsDefault: url.IsDefault, Sort: url.Sort})
		}
	}
	if in.Name == "" {
		in.Name = policy.Name
	}
	if err := p.Validate(&in, policy); err != nil {
		return nil, err
	}

	var duplicate int64
	if err := p.db.Model(&model.StoragePolicy{}).Where("name = ? AND id <> ?", in.Name, policy.ID).Count(&duplicate).Error; err != nil {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	if duplicate > 0 {
		return nil, apperr.ErrInvalidRequest.WithMessage("策略名称已存在")
	}

	secrets := policy.Secrets
	if len(in.Secrets) > 0 {
		merged, err := p.SecretsMap(policy)
		if err != nil {
			return nil, err
		}
		for key, value := range in.Secrets {
			if strings.TrimSpace(value) == "" {
				delete(merged, key)
				continue
			}
			merged[key] = value
		}
		secrets, err = p.encryptSecrets(merged)
		if err != nil {
			return nil, err
		}
	}
	configJSON, err := json.Marshal(normalizeMap(in.Config))
	if err != nil {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}

	enabled := policy.Enabled
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	updates := map[string]any{
		"name":            in.Name,
		"type":            in.Type,
		"enabled":         enabled,
		"path_template":   in.PathTemplate,
		"conflict_policy": in.ConflictPolicy,
		"config":          string(configJSON),
		"secrets":         secrets,
		"sort":            in.Sort,
		"updated_at":      nowUTC(),
	}
	if err := p.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.StoragePolicy{}).Where("id = ?", policy.ID).Updates(updates).Error; err != nil {
			return err
		}
		return p.replaceURLs(tx, policy.ID, in.URLs)
	}); err != nil {
		return nil, apperr.ErrInternal.WithMessage("更新策略失败: " + err.Error())
	}

	var actorID *int64
	if actor != nil {
		actorID = &actor.User.ID
	}
	p.audit.Recordf(Entry{
		UserID:     actorID,
		Action:     "policy_update",
		TargetType: "policy",
		TargetID:   &policy.ID,
		Detail:     map[string]any{"name": in.Name},
		IP:         ip,
		UserAgent:  userAgent,
	})
	return p.Get(policy.ID)
}

// Delete 删除策略; 仍被记录引用时拒绝.
func (p *Policies) Delete(id int64, actor *Principal, ip, userAgent string) error {
	policy, err := p.Get(id)
	if err != nil {
		return err
	}
	var recordCount int64
	if err := p.db.Model(&model.Image{}).Where("policy_id = ?", policy.ID).Count(&recordCount).Error; err != nil {
		return apperr.ErrInternal.WithMessage(err.Error())
	}
	if recordCount > 0 {
		return apperr.ErrInvalidRequest.WithMessage(fmt.Sprintf("策略仍被 %d 条记录引用, 不能删除", recordCount))
	}
	if err := p.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("policy_id = ?", policy.ID).Delete(&model.PolicyURL{}).Error; err != nil {
			return err
		}
		if err := tx.Where("policy_id = ?", policy.ID).Delete(&model.GroupPolicy{}).Error; err != nil {
			return err
		}
		return tx.Delete(&model.StoragePolicy{}, policy.ID).Error
	}); err != nil {
		return apperr.ErrInternal.WithMessage("删除策略失败: " + err.Error())
	}

	var actorID *int64
	if actor != nil {
		actorID = &actor.User.ID
	}
	p.audit.Recordf(Entry{
		UserID:     actorID,
		Action:     "policy_delete",
		TargetType: "policy",
		TargetID:   &policy.ID,
		Detail:     map[string]any{"name": policy.Name},
		IP:         ip,
		UserAgent:  userAgent,
	})
	return nil
}

// Test 执行连接测试: 写入并删除探测对象.
func (p *Policies) Test(ctx context.Context, id int64) (time.Duration, error) {
	policy, err := p.Get(id)
	if err != nil {
		return 0, err
	}
	adapter, err := p.Adapter(policy)
	if err != nil {
		return 0, err
	}
	start := time.Now()
	if err := adapter.Test(ctx, ""); err != nil {
		return time.Since(start), apperr.ErrBackend.WithMessage(err.Error())
	}
	return time.Since(start), nil
}

// PreviewInput 是路径预览入参.
type PreviewInput struct {
	PathTemplate string  `json:"path_template"`
	MD5          *string `json:"md5"`
	SHA256       *string `json:"sha256"`
	SHA512       *string `json:"sha512"`
	Ext          *string `json:"ext"`
	PolicyID     *int64  `json:"policy_id"`
}

// PreviewResult 是路径预览结果.
type PreviewResult struct {
	RelPath   string           `json:"rel_path"`
	ObjectKey string           `json:"object_key"`
	URLs      []PreviewURLItem `json:"urls"`
}

// PreviewURLItem 是预览中的单个域名 URL.
type PreviewURLItem struct {
	Alias     string `json:"alias"`
	URL       string `json:"url"`
	IsDefault bool   `json:"is_default"`
}

// Preview 渲染示例路径; 与上传共用同一渲染函数.
func (p *Policies) Preview(in PreviewInput) (*PreviewResult, error) {
	template := in.PathTemplate
	var urls []model.PolicyURL
	if in.PolicyID != nil {
		policy, err := p.Get(*in.PolicyID)
		if err != nil {
			return nil, err
		}
		if template == "" {
			template = policy.PathTemplate
		}
		urls, err = p.URLs(policy.ID)
		if err != nil {
			return nil, err
		}
	}
	md5Value := "0f1e2d3c4b5a69788796a5b4c3d2e1f0"
	sha256Value := "0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c4b5a69788796a5b4c3d2e1f0"
	sha512Value := strings.Repeat("0f1e2d3c", 16)
	extValue := "png"
	if in.MD5 != nil && strings.TrimSpace(*in.MD5) != "" {
		md5Value = strings.TrimSpace(*in.MD5)
	}
	if in.SHA256 != nil && strings.TrimSpace(*in.SHA256) != "" {
		sha256Value = strings.TrimSpace(*in.SHA256)
	}
	if in.SHA512 != nil && strings.TrimSpace(*in.SHA512) != "" {
		sha512Value = strings.TrimSpace(*in.SHA512)
	}
	if in.Ext != nil && strings.TrimSpace(*in.Ext) != "" {
		extValue = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(*in.Ext)), ".")
	}

	relPath, err := storage.RenderPath(template, storage.PathVars{
		MD5:    md5Value,
		SHA256: sha256Value,
		SHA512: sha512Value,
		Ext:    extValue,
	})
	if err != nil {
		return nil, apperr.ErrInvalidRequest.WithMessage(err.Error())
	}
	objectKey := storage.NormalizeObjectKey("", relPath)
	result := &PreviewResult{RelPath: relPath, ObjectKey: objectKey, URLs: []PreviewURLItem{}}
	for _, url := range urls {
		result.URLs = append(result.URLs, PreviewURLItem{
			Alias:     url.Alias,
			URL:       storage.JoinURL(url.URL, relPath),
			IsDefault: url.IsDefault,
		})
	}
	return result, nil
}

// ScanResult 是扫描补录结果.
type ScanResult struct {
	Images  int           `json:"images"`
	Files   int           `json:"files"`
	Skipped int           `json:"skipped"`
	Failed  []ScanFailure `json:"failed"`
}

// ScanFailure 是扫描失败明细.
type ScanFailure struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}

// Scan 扫描补录: 递归列举后端文件, 读取内容计算哈希, 以原路径入库; ownerID 为记录归属 (0 表示系统).
func (p *Policies) Scan(ctx context.Context, policyID int64, ownerID int64, subPath *string, dryRun bool) (*ScanResult, error) {
	policy, err := p.Get(policyID)
	if err != nil {
		return nil, err
	}
	adapter, err := p.Adapter(policy)
	if err != nil {
		return nil, err
	}
	caps := adapter.Capabilities()
	if !caps.Has(storage.CapList) || !caps.Has(storage.CapRead) {
		return nil, apperr.ErrInvalidRequest.WithMessage("该存储后端不支持列举与读取能力, 无法扫描补录")
	}

	prefix := ""
	if subPath != nil && strings.TrimSpace(*subPath) != "" {
		if err := storage.ValidatePrefix(*subPath); err != nil {
			return nil, apperr.ErrInvalidRequest.WithMessage(err.Error())
		}
		prefix = storage.NormalizeObjectKey("", strings.TrimSpace(*subPath))
	}
	objects, err := adapter.List(ctx, prefix)
	if err != nil {
		return nil, apperr.ErrBackend.WithMessage(err.Error())
	}

	defaultURL, err := p.DefaultURL(policy.ID)
	if err != nil {
		return nil, err
	}

	result := &ScanResult{Failed: []ScanFailure{}}
	owner := ownerID
	for _, object := range objects {
		if object.IsDir {
			continue
		}
		key := strings.Trim(object.Key, "/")

		var existing int64
		if err := p.db.Model(&model.Image{}).Where("policy_id = ? AND object_key = ?", policy.ID, key).Count(&existing).Error; err != nil {
			return nil, apperr.ErrInternal.WithMessage(err.Error())
		}
		if existing > 0 {
			result.Skipped++
			continue
		}
		if dryRun {
			result.Images++
			continue
		}

		info, err := p.scanObject(ctx, adapter, policy, key, defaultURL, owner)
		if err != nil {
			result.Failed = append(result.Failed, ScanFailure{Path: key, Error: err.Error()})
			continue
		}
		if info.Kind == model.KindImage {
			result.Images++
		} else {
			result.Files++
		}
	}

	if !dryRun {
		var actor *int64
		if owner > 0 {
			actor = &owner
		}
		p.audit.Recordf(Entry{
			UserID:     actor,
			Action:     "policy_scan",
			TargetType: "policy",
			TargetID:   &policy.ID,
			Detail:     map[string]any{"images": result.Images, "files": result.Files, "skipped": result.Skipped, "failed": len(result.Failed)},
		})
	}
	return result, nil
}

// scanObject 读取单个外部对象并入库; 返回入库记录.
func (p *Policies) scanObject(ctx context.Context, adapter storage.Adapter, policy *model.StoragePolicy, key, defaultURL string, ownerID int64) (*model.Image, error) {
	reader, err := adapter.Read(ctx, key)
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	buffer := spool.New(p.cfg.TempDir, p.cfg.UploadMemoryLimit)
	defer buffer.Close()

	hasher := hashing.New()
	written, err := io.Copy(io.MultiWriter(buffer, hasher), io.LimitReader(reader, scanMaxFileSize+1))
	if err != nil {
		return nil, err
	}
	if written > scanMaxFileSize {
		return nil, fmt.Errorf("文件超过扫描体积上限 %d 字节", scanMaxFileSize)
	}

	seekable, err := buffer.Reader()
	if err != nil {
		return nil, err
	}
	head := make([]byte, scanHeadBytes)
	headRead, err := io.ReadFull(seekable, head)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return nil, err
	}
	head = head[:headRead]

	sums := hasher.Result()
	detected := imgdetect.Detect(head)
	if detected.Format == "unknown" {
		if known, ok := imgdetect.Known(FormatFromExtension(key)); ok {
			detected = known
		}
	}

	kind := model.KindFile
	mime := detected.MIME
	if imgdetect.IsImage(detected) {
		kind = model.KindImage
	}
	relPath := key
	ext := ExtensionOf(key)
	if ext == "" {
		ext = detected.Ext
	}
	now := nowUTC()
	record := model.Image{
		UserID:           ownerID,
		PolicyID:         policy.ID,
		RelPath:          relPath,
		ObjectKey:        key,
		MD5:              sums.MD5,
		SHA1:             sums.SHA1,
		SHA256:           sums.SHA256,
		SHA512:           sums.SHA512,
		Size:             sums.Size,
		Kind:             kind,
		Format:           detected.Format,
		Ext:              ext,
		MIME:             mime,
		OriginalFilename: path.Base(key),
		URL:              storage.JoinURL(defaultURL, relPath),
		QuotaCharged:     false,
		Source:           model.SourceScan,
		CreatedAt:        now,
	}
	if kind == model.KindImage {
		if width, height := imgdetect.Dimensions(seekable, detected); width > 0 && height > 0 {
			record.Width = &width
			record.Height = &height
		}
	}
	if err := p.db.Create(&record).Error; err != nil {
		return nil, err
	}
	return &record, nil
}

// enqueueInitialScan 在创建策略后入队首次扫描.
func (p *Policies) enqueueInitialScan(policy *model.StoragePolicy, ownerID int64) {
	if p.tasks == nil {
		return
	}
	spec, ok := storage.SpecOf(policy.Type)
	if !ok || !spec.Has(storage.CapList) || !spec.Has(storage.CapRead) {
		return
	}
	payload := map[string]any{"policy_id": policy.ID, "user_id": ownerID}
	if _, err := p.tasks.Enqueue(model.TaskPolicyScan, payload); err != nil {
		p.log.Warn("入队首次扫描任务失败", "policy_id", policy.ID, "error", err)
	}
}

// replaceURLs 覆盖写入分发域名.
func (p *Policies) replaceURLs(tx *gorm.DB, policyID int64, urls []URLInput) error {
	if err := tx.Where("policy_id = ?", policyID).Delete(&model.PolicyURL{}).Error; err != nil {
		return err
	}
	now := nowUTC()
	rows := make([]model.PolicyURL, 0, len(urls))
	for i, item := range urls {
		rows = append(rows, model.PolicyURL{
			PolicyID:  policyID,
			Alias:     item.Alias,
			URL:       item.URL,
			IsDefault: item.IsDefault,
			Sort:      i,
			CreatedAt: now,
		})
	}
	if len(rows) == 0 {
		return nil
	}
	return tx.Create(&rows).Error
}

// encryptSecrets 加密敏感配置.
func (p *Policies) encryptSecrets(values map[string]string) (string, error) {
	normalized := normalizeMap(values)
	if len(normalized) == 0 {
		return "", nil
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return "", apperr.ErrInternal.WithMessage(err.Error())
	}
	encrypted, err := p.cipher.Encrypt(string(encoded))
	if err != nil {
		return "", apperr.ErrInternal.WithMessage("加密凭据失败: " + err.Error())
	}
	return encrypted, nil
}

// normalizeMap 去掉空值并裁剪空白.
func normalizeMap(values map[string]string) map[string]string {
	out := make(map[string]string, len(values))
	for key, value := range values {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			continue
		}
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue == "" {
			continue
		}
		out[trimmedKey] = trimmedValue
	}
	return out
}
