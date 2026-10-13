package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/yeqingky/Picsky/internal/apperr"
	"github.com/yeqingky/Picsky/internal/config"
	"github.com/yeqingky/Picsky/internal/hashing"
	"github.com/yeqingky/Picsky/internal/imgdetect"
	"github.com/yeqingky/Picsky/internal/model"
	"github.com/yeqingky/Picsky/internal/spool"
	"github.com/yeqingky/Picsky/internal/storage"
)

// maxRenameAttempts 是冲突重命名的最大尝试次数.
const maxRenameAttempts = 6

// MaxUploadSize 是单次上传的硬上限: 后端无分片能力, 超过该体积直接拒绝.
const MaxUploadSize int64 = 2 << 30

// Upload 实现上传流程: 校验, 哈希层与路径层冲突检查, 外部写入, 记录入库与配额计费.
type Upload struct {
	db       *gorm.DB
	cfg      *config.Config
	settings *Settings
	audit    *Audit
	limiter  *RateLimiter
	policies *Policies
	log      *slog.Logger
}

// NewUpload 创建上传服务.
func NewUpload(database *gorm.DB, cfg *config.Config, settings *Settings, audit *Audit, policies *Policies, log *slog.Logger) *Upload {
	return &Upload{
		db:       database,
		cfg:      cfg,
		settings: settings,
		audit:    audit,
		limiter:  NewRateLimiter(time.Minute),
		policies: policies,
		log:      log,
	}
}

// SpooledUpload 是已完整缓冲并可重读的上传内容.
type SpooledUpload struct {
	Spool     *spool.Spool
	Hashes    hashing.Result
	Head      []byte
	Filename  string
	Truncated bool // limit 非 0 且输入超过 limit 时为 true, 调用方必须拒绝
}

// SpoolReader 单遍读取输入: 同时计算全部哈希并缓冲内容.
// limit 为允许的最大字节数 (0 表示不限); 超过 limit 时截断并置 Truncated, 由调用方拒绝该请求.
func SpoolReader(tempDir string, memLimit int64, reader io.Reader, filename string, limit int64) (*SpooledUpload, error) {
	buffer := spool.New(tempDir, memLimit)
	hasher := hashing.New()

	source := reader
	if limit > 0 {
		source = io.LimitReader(reader, limit+1)
	}
	written, err := io.Copy(io.MultiWriter(buffer, hasher), source)
	if err != nil {
		buffer.Close()
		return nil, apperr.ErrInvalidRequest.WithMessage("读取上传内容失败: " + err.Error())
	}

	spooled := &SpooledUpload{
		Spool:     buffer,
		Hashes:    hasher.Result(),
		Filename:  filename,
		Truncated: limit > 0 && written > limit,
	}
	if seekable, err := buffer.Reader(); err == nil {
		head := make([]byte, 512)
		read, readErr := io.ReadFull(seekable, head)
		if readErr != nil && !errors.Is(readErr, io.ErrUnexpectedEOF) && !errors.Is(readErr, io.EOF) {
			buffer.Close()
			return nil, apperr.ErrInvalidRequest.WithMessage("读取上传内容失败: " + readErr.Error())
		}
		spooled.Head = head[:read]
	}
	return spooled, nil
}

// UploadRequest 是一次上传请求.
type UploadRequest struct {
	Principal *Principal
	PolicyID  *int64
	Spooled   *SpooledUpload
	Source    string
	ClientIP  string
	UserAgent string
}

// UploadResult 是上传结果.
type UploadResult struct {
	Record       *model.Image
	URLs         []model.PolicyURL
	Dedup        bool
	QuotaCharged bool
	Status       int
}

// Upload 执行上传.
func (u *Upload) Upload(ctx context.Context, req UploadRequest) (*UploadResult, error) {
	if req.Principal == nil || req.Principal.User == nil {
		return nil, apperr.ErrUnauthorized
	}
	user := req.Principal.User
	if !req.Principal.Has(model.PermUpload) {
		return nil, apperr.ErrForbidden.WithMessage("当前用户组没有上传权限")
	}
	if req.Spooled == nil || req.Spooled.Spool == nil {
		return nil, apperr.ErrInvalidRequest.WithMessage("缺少上传内容")
	}

	detected := imgdetect.Detect(req.Spooled.Head)
	if imgdetect.IsSVG(detected) {
		return nil, apperr.ErrUnsupportedFormat.WithMessage("系统级禁止上传 SVG")
	}
	if !imgdetect.IsImage(detected) {
		return nil, apperr.ErrUnsupportedFormat.WithMessage("仅支持位图图片, 探测到 " + detected.Format)
	}
	allowedFormats := EffectiveAllowedFormats(u.settings)
	if !formatAllowed(detected.Format, allowedFormats) {
		return nil, apperr.ErrUnsupportedFormat.WithDetails(map[string]any{
			"format":  detected.Format,
			"allowed": allowedFormats,
		})
	}

	sums := req.Spooled.Hashes
	maxSize := EffectiveMaxFileSize(u.settings, req.Principal.Group)
	if req.Spooled.Truncated || (maxSize > 0 && sums.Size > maxSize) {
		return nil, apperr.ErrFileTooLarge.WithDetails(map[string]any{"size": sums.Size, "limit": maxSize})
	}
	if rateLimit := EffectiveUploadRateLimit(req.Principal.Group); rateLimit > 0 {
		if allowed, retryAfter := u.limiter.Allow(fmt.Sprintf("upload:%d", user.ID), rateLimit); !allowed {
			seconds := int(retryAfter.Seconds()) + 1
			return nil, apperr.ErrRateLimited.WithDetails(map[string]any{"retry_after_seconds": seconds})
		}
	}

	policy, err := u.policies.Resolve(req.Principal, req.PolicyID)
	if err != nil {
		return nil, err
	}
	adapter, err := u.policies.Adapter(policy)
	if err != nil {
		return nil, err
	}
	urls, err := u.policies.URLs(policy.ID)
	if err != nil {
		return nil, err
	}
	defaultURL, err := u.policies.DefaultURL(policy.ID)
	if err != nil {
		return nil, err
	}
	caps := adapter.Capabilities()

	// 冲突检查第一层: 同策略下相同 sha256 的活动记录.
	var existing model.Image
	err = u.db.Where("policy_id = ? AND sha256 = ? AND deleted_at IS NULL AND kind = ?", policy.ID, sums.SHA256, model.KindImage).
		Order("id ASC").First(&existing).Error
	switch {
	case err == nil:
		return u.reuseRecord(req, policy, urls, defaultURL, &existing)
	case errors.Is(err, gorm.ErrRecordNotFound):
	default:
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}

	// 冲突检查第二层: 路径与外部对象.
	relPath, err := storage.RenderPath(policy.PathTemplate, storage.PathVars{
		MD5:    sums.MD5,
		SHA256: sums.SHA256,
		SHA512: sums.SHA512,
		Ext:    detected.Ext,
	})
	if err != nil {
		return nil, apperr.ErrInternal.WithMessage("魔法路径渲染失败: " + err.Error())
	}

	var (
		objectKey         string
		overwriteExisting bool
	)
resolved:
	for attempt := 0; ; attempt++ {
		objectKey = storage.NormalizeObjectKey("", relPath)
		conflictRecord, err := u.recordForKey(policy.ID, objectKey)
		if err != nil {
			return nil, err
		}
		objectExists := false
		if caps.Has(storage.CapStat) {
			if _, statErr := adapter.Stat(ctx, objectKey); statErr == nil {
				objectExists = true
			} else if !errors.Is(statErr, storage.ErrNotFound) {
				u.log.Warn("冲突检查时查询外部对象失败", "object_key", objectKey, "error", statErr)
			}
		}
		if conflictRecord == nil && !objectExists {
			break
		}
		if conflictRecord != nil && conflictRecord.SHA256 == sums.SHA256 {
			return u.reuseRecord(req, policy, urls, defaultURL, conflictRecord)
		}
		if conflictRecord == nil && objectExists {
			// 对象已存在但记录尚不可见: 可能是并发上传相同内容, 先按内容再确认一次.
			contentRecord, err := u.contentRecord(policy.ID, sums.SHA256, user.ID)
			if err != nil {
				return nil, err
			}
			if contentRecord != nil {
				return u.reuseRecord(req, policy, urls, defaultURL, contentRecord)
			}
		}

		switch policy.ConflictPolicy {
		case model.ConflictSkip, model.ConflictFail:
			return nil, apperr.ErrPathConflict.WithDetails(map[string]any{
				"object_key": objectKey,
				"policy":     policy.ConflictPolicy,
			})
		case model.ConflictOverwrite:
			if !caps.Has(storage.CapOverwrite) {
				return nil, apperr.ErrPathConflict.WithMessage("后端未声明 overwrite 能力, 无法覆盖")
			}
			overwriteExisting = true
			break resolved
		case model.ConflictRename:
			if attempt >= maxRenameAttempts {
				return nil, apperr.ErrPathConflict.WithMessage("重命名后仍存在冲突, 请调整魔法路径模板")
			}
			renamed, err := renameRelPath(relPath)
			if err != nil {
				return nil, apperr.ErrInternal.WithMessage("生成冲突重命名失败: " + err.Error())
			}
			relPath = renamed
		default:
			return nil, apperr.ErrPathConflict
		}
	}

	// 提前判定计费与配额: 明显超限时不必写入外部对象; 事务内会再次原子校验.
	existingForKey, err := u.recordForKey(policy.ID, objectKey)
	if err != nil {
		return nil, err
	}
	if existingForKey == nil {
		quotaUser, err := u.freshUser(user.ID)
		if err != nil {
			return nil, err
		}
		if err := CheckQuota(EffectiveQuota(req.Principal.Group, quotaUser), 1, sums.Size); err != nil {
			return nil, err
		}
	}

	// 写入外部对象.
	body, err := req.Spooled.Spool.Reader()
	if err != nil {
		return nil, apperr.ErrInternal.WithMessage("读取缓冲内容失败: " + err.Error())
	}
	if err := adapter.Put(ctx, objectKey, body, sums.Size, detected.MIME); err != nil {
		return nil, apperr.ErrBackend.WithMessage(err.Error())
	}

	now := nowUTC()
	record := model.Image{
		UserID:           user.ID,
		PolicyID:         policy.ID,
		RelPath:          relPath,
		ObjectKey:        objectKey,
		MD5:              sums.MD5,
		SHA1:             sums.SHA1,
		SHA256:           sums.SHA256,
		SHA512:           sums.SHA512,
		Size:             sums.Size,
		Kind:             model.KindImage,
		Format:           detected.Format,
		Ext:              detected.Ext,
		MIME:             detected.MIME,
		OriginalFilename: imgdetect.SanitizeFilenameBase(req.Spooled.Filename) + "." + detected.Ext,
		URL:              storage.JoinURL(defaultURL, relPath),
		Source:           sourceOrDefault(req.Source),
		ClientIP:         truncate(req.ClientIP, 45),
		CreatedAt:        now,
	}
	if width, height := imgdetect.Dimensions(body, detected); width > 0 && height > 0 {
		record.Width = &width
		record.Height = &height
	}

	// 事务内重新判定计费并原子入账: 并发上传同一对象键时只有一条记录, 且只计费一次.
	var (
		charged  bool
		reused   *model.Image
		replaced *model.Image
	)
	if err := u.db.Transaction(func(tx *gorm.DB) error {
		existing, err := recordForKeyTx(tx, policy.ID, objectKey)
		if err != nil {
			return err
		}

		// 覆盖模式下覆盖本人已有记录: 更新元数据, 不新建记录.
		if overwriteExisting && existing != nil && existing.UserID == user.ID {
			if err := tx.Model(&model.Image{}).Where("id = ?", existing.ID).Updates(map[string]any{
				"md5":    sums.MD5,
				"sha1":   sums.SHA1,
				"sha256": sums.SHA256,
				"sha512": sums.SHA512,
				"size":   sums.Size,
				"format": detected.Format,
				"ext":    detected.Ext,
				"mime":   detected.MIME,
				"width":  record.Width,
				"height": record.Height,
			}).Error; err != nil {
				return err
			}
			// 记录仍计费时同步占用空间的差值, 避免 used_bytes 与记录不一致.
			if existing.QuotaCharged && existing.Size != sums.Size {
				delta := sums.Size - existing.Size
				if err := tx.Model(&model.User{}).Where("id = ?", existing.UserID).Updates(map[string]any{
					"used_bytes": gorm.Expr("CASE WHEN used_bytes + ? < 0 THEN 0 ELSE used_bytes + ? END", delta, delta),
					"updated_at": now,
				}).Error; err != nil {
					return err
				}
			}
			updated, err := recordForKeyTx(tx, policy.ID, objectKey)
			if err != nil {
				return err
			}
			replaced = updated
			return nil
		}

		// 并发上传相同内容: 幂等复用已有记录, 不新建.
		if existing != nil && existing.UserID == user.ID && existing.SHA256 == sums.SHA256 {
			reused = existing
			return nil
		}

		// 事务内再按内容确认一次 (串行化点): 并发相同内容上传只保留一条计费记录.
		if !overwriteExisting {
			contentRecord, err := contentRecordTx(tx, policy.ID, sums.SHA256, user.ID)
			if err != nil {
				return err
			}
			if contentRecord != nil && contentRecord.SHA256 == sums.SHA256 {
				reused = contentRecord
				return nil
			}
		}

		charge := existing == nil
		if charge {
			locked, err := u.lockUserTx(tx, user.ID)
			if err != nil {
				return err
			}
			if err := CheckQuota(EffectiveQuota(req.Principal.Group, locked), 1, sums.Size); err != nil {
				return err
			}
		}
		record.QuotaCharged = charge

		if overwriteExisting {
			var affected []model.Image
			if err := tx.Select("id", "user_id", "size", "quota_charged").
				Where("policy_id = ? AND object_key = ? AND deleted_at IS NULL", policy.ID, objectKey).
				Find(&affected).Error; err != nil {
				return err
			}
			if err := tx.Model(&model.Image{}).
				Where("policy_id = ? AND object_key = ? AND deleted_at IS NULL", policy.ID, objectKey).
				Updates(map[string]any{
					"md5":    sums.MD5,
					"sha1":   sums.SHA1,
					"sha256": sums.SHA256,
					"sha512": sums.SHA512,
					"size":   sums.Size,
					"format": detected.Format,
					"ext":    detected.Ext,
					"mime":   detected.MIME,
					"width":  record.Width,
					"height": record.Height,
				}).Error; err != nil {
				return err
			}
			// 其他用户的计费记录体积被改写, 按记录聚合重算其用量, 避免计数残留偏差.
			owners := make([]int64, 0, len(affected))
			for idx := range affected {
				if affected[idx].QuotaCharged && affected[idx].UserID != user.ID {
					owners = append(owners, affected[idx].UserID)
				}
			}
			if len(owners) > 0 {
				if err := reconcileUsersTx(tx, owners); err != nil {
					return err
				}
			}
		}
		if err := tx.Create(&record).Error; err != nil {
			return err
		}
		if charge {
			if err := tx.Model(&model.User{}).Where("id = ?", user.ID).Updates(map[string]any{
				"used_images": gorm.Expr("used_images + 1"),
				"used_bytes":  gorm.Expr("used_bytes + ?", sums.Size),
				"updated_at":  now,
			}).Error; err != nil {
				return err
			}
		}
		charged = charge
		return nil
	}); err != nil {
		if apiErr := apperr.From(err); apiErr.Status < http.StatusInternalServerError {
			return nil, apiErr
		}
		// 外部对象已写入但没有记录: 属文档中已知的孤立对象竞态, 由维护任务或人工处理.
		u.log.Warn("写入图片记录失败, 可能存在孤立对象", "object_key", objectKey, "error", err)
		return nil, apperr.ErrInternal.WithMessage("写入图片记录失败: " + err.Error())
	}

	if reused != nil {
		u.audit.Recordf(Entry{
			UserID:     &user.ID,
			Action:     "upload",
			TargetType: "image",
			TargetID:   &reused.ID,
			Detail: map[string]any{
				"policy_id":     policy.ID,
				"object_key":    reused.ObjectKey,
				"size":          reused.Size,
				"sha256":        reused.SHA256,
				"dedup":         true,
				"concurrent":    true,
				"quota_charged": reused.QuotaCharged,
			},
			IP:        req.ClientIP,
			UserAgent: req.UserAgent,
		})
		return &UploadResult{Record: reused, URLs: urls, Dedup: true, QuotaCharged: reused.QuotaCharged, Status: http.StatusOK}, nil
	}
	if replaced != nil {
		u.audit.Recordf(Entry{
			UserID:     &user.ID,
			Action:     "upload",
			TargetType: "image",
			TargetID:   &replaced.ID,
			Detail: map[string]any{
				"policy_id":     policy.ID,
				"object_key":    replaced.ObjectKey,
				"size":          replaced.Size,
				"sha256":        replaced.SHA256,
				"overwritten":   true,
				"quota_charged": replaced.QuotaCharged,
			},
			IP:        req.ClientIP,
			UserAgent: req.UserAgent,
		})
		return &UploadResult{Record: replaced, URLs: urls, Dedup: false, QuotaCharged: replaced.QuotaCharged, Status: http.StatusCreated}, nil
	}

	u.audit.Recordf(Entry{
		UserID:     &user.ID,
		Action:     "upload",
		TargetType: "image",
		TargetID:   &record.ID,
		Detail: map[string]any{
			"policy_id":         policy.ID,
			"object_key":        objectKey,
			"size":              sums.Size,
			"format":            detected.Format,
			"original_filename": req.Spooled.Filename,
			"sha256":            sums.SHA256,
			"dedup":             false,
			"quota_charged":     charged,
		},
		IP:        req.ClientIP,
		UserAgent: req.UserAgent,
	})

	return &UploadResult{Record: &record, URLs: urls, Dedup: false, QuotaCharged: charged, Status: 201}, nil
}

// reuseRecord 复用已有对象: 自己的记录幂等返回, 他人的记录新建免费记录.
func (u *Upload) reuseRecord(req UploadRequest, policy *model.StoragePolicy, urls []model.PolicyURL, defaultURL string, existing *model.Image) (*UploadResult, error) {
	user := req.Principal.User
	if existing.UserID == user.ID {
		u.audit.Recordf(Entry{
			UserID:     &user.ID,
			Action:     "upload",
			TargetType: "image",
			TargetID:   &existing.ID,
			Detail: map[string]any{
				"policy_id":         policy.ID,
				"object_key":        existing.ObjectKey,
				"size":              existing.Size,
				"original_filename": req.Spooled.Filename,
				"sha256":            existing.SHA256,
				"dedup":             true,
				"quota_charged":     existing.QuotaCharged,
			},
			IP:        req.ClientIP,
			UserAgent: req.UserAgent,
		})
		return &UploadResult{Record: existing, URLs: urls, Dedup: true, QuotaCharged: existing.QuotaCharged, Status: 200}, nil
	}

	now := nowUTC()
	record := model.Image{
		UserID:           user.ID,
		PolicyID:         existing.PolicyID,
		RelPath:          existing.RelPath,
		ObjectKey:        existing.ObjectKey,
		MD5:              existing.MD5,
		SHA1:             existing.SHA1,
		SHA256:           existing.SHA256,
		SHA512:           existing.SHA512,
		Size:             existing.Size,
		Kind:             model.KindImage,
		Format:           existing.Format,
		Ext:              existing.Ext,
		MIME:             existing.MIME,
		Width:            existing.Width,
		Height:           existing.Height,
		OriginalFilename: imgdetect.SanitizeFilenameBase(req.Spooled.Filename) + "." + existing.Ext,
		URL:              storage.JoinURL(defaultURL, existing.RelPath),
		QuotaCharged:     false,
		Source:           sourceOrDefault(req.Source),
		ClientIP:         truncate(req.ClientIP, 45),
		CreatedAt:        now,
	}
	if err := u.db.Create(&record).Error; err != nil {
		return nil, apperr.ErrInternal.WithMessage("写入复用记录失败: " + err.Error())
	}

	u.audit.Recordf(Entry{
		UserID:     &user.ID,
		Action:     "upload",
		TargetType: "image",
		TargetID:   &record.ID,
		Detail: map[string]any{
			"policy_id":         policy.ID,
			"object_key":        record.ObjectKey,
			"size":              record.Size,
			"original_filename": req.Spooled.Filename,
			"sha256":            record.SHA256,
			"dedup":             true,
			"reused_from":       existing.ID,
			"quota_charged":     false,
		},
		IP:        req.ClientIP,
		UserAgent: req.UserAgent,
	})
	return &UploadResult{Record: &record, URLs: urls, Dedup: true, QuotaCharged: false, Status: 200}, nil
}

// UploadLimit 计算读取上传输入时的字节上限: 组上限 (或系统上限) 与硬上限取较小值.
func UploadLimit(settings *Settings, group *model.Group) int64 {
	limit := EffectiveMaxFileSize(settings, group)
	if limit <= 0 || limit > MaxUploadSize {
		return MaxUploadSize
	}
	return limit
}

// CheckResult 是秒传检查结果.
type CheckResult struct {
	Exists bool         `json:"exists"`
	Record *CheckRecord `json:"record,omitempty"`
}

// CheckRecord 是秒传检查返回的既有记录摘要.
type CheckRecord struct {
	ID   int64  `json:"id"`
	URL  string `json:"url"`
	Size int64  `json:"size"`
}

// Check 秒传检查: 同策略下是否存在相同 sha256 的活动记录.
func (u *Upload) Check(principal *Principal, sha256 string, policyID *int64) (*CheckResult, error) {
	sum := strings.ToLower(strings.TrimSpace(sha256))
	if len(sum) != 64 || !isHex(sum) {
		return nil, apperr.ErrInvalidRequest.WithMessage("sha256 必须是 64 位十六进制")
	}
	if !principal.Has(model.PermUpload) {
		return nil, apperr.ErrForbidden.WithMessage("当前用户组没有上传权限")
	}
	policy, err := u.policies.Resolve(principal, policyID)
	if err != nil {
		return nil, err
	}
	var existing model.Image
	err = u.db.Where("policy_id = ? AND sha256 = ? AND deleted_at IS NULL AND kind = ?", policy.ID, sum, model.KindImage).
		Order("id ASC").First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &CheckResult{Exists: false}, nil
	}
	if err != nil {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	return &CheckResult{Exists: true, Record: &CheckRecord{ID: existing.ID, URL: existing.URL, Size: existing.Size}}, nil
}

// freshUser 读取用户当前状态, 用于配额与限额判定.
func (u *Upload) freshUser(userID int64) (*model.User, error) {
	var user model.User
	if err := u.db.First(&user, userID).Error; err != nil {
		return nil, apperr.ErrInternal.WithMessage("读取用户失败: " + err.Error())
	}
	return &user, nil
}

// contentRecord 按内容哈希查询本策略下指定用户的活动记录 (秒传判定).
func (u *Upload) contentRecord(policyID int64, sha256 string, userID int64) (*model.Image, error) {
	return contentRecordTx(u.db, policyID, sha256, userID)
}

// contentRecordTx 在事务内按内容哈希查询当前用户的活动记录.
func contentRecordTx(tx *gorm.DB, policyID int64, sha256 string, userID int64) (*model.Image, error) {
	var record model.Image
	err := tx.Where("policy_id = ? AND sha256 = ? AND user_id = ? AND deleted_at IS NULL AND kind = ?",
		policyID, sha256, userID, model.KindImage).
		Order("id ASC").First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	return &record, nil
}

// recordForKey 查询同一策略下指定对象键的活动记录; 不存在时返回 nil, nil.
func (u *Upload) recordForKey(policyID int64, objectKey string) (*model.Image, error) {
	return recordForKeyTx(u.db, policyID, objectKey)
}

// recordForKeyTx 在事务内查询同一策略下指定对象键的活动记录; 不存在时返回 nil, nil.
func recordForKeyTx(tx *gorm.DB, policyID int64, objectKey string) (*model.Image, error) {
	var record model.Image
	err := tx.Where("policy_id = ? AND object_key = ? AND deleted_at IS NULL", policyID, objectKey).
		Order("id ASC").First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	return &record, nil
}

// lockUserTx 在事务内读取用户行并加锁: PostgreSQL 使用行锁, SQLite 由单写连接串行化.
func (u *Upload) lockUserTx(tx *gorm.DB, userID int64) (*model.User, error) {
	query := tx
	if u.cfg.DBDriver == "postgres" {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var user model.User
	if err := query.First(&user, userID).Error; err != nil {
		return nil, apperr.ErrInternal.WithMessage("读取用户失败: " + err.Error())
	}
	return &user, nil
}

// renameRelPath 在最后一段文件名主体后追加随机后缀.
func renameRelPath(relPath string) (string, error) {
	suffix, err := storage.RandomString(8)
	if err != nil {
		return "", err
	}
	segments := strings.Split(relPath, "/")
	last := segments[len(segments)-1]
	if idx := strings.LastIndexByte(last, '.'); idx > 0 {
		segments[len(segments)-1] = last[:idx] + "-" + suffix + last[idx:]
	} else {
		segments[len(segments)-1] = last + "-" + suffix
	}
	return strings.Join(segments, "/"), nil
}

func sourceOrDefault(source string) string {
	switch source {
	case model.SourceWeb, model.SourceAPI, model.SourceScan:
		return source
	default:
		return model.SourceWeb
	}
}

func isHex(value string) bool {
	for _, r := range value {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f':
		default:
			return false
		}
	}
	return true
}
