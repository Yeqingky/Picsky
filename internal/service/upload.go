package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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

// MaxUploadSize 是单次上传的硬上限: 后端无分片能力, 超过该体积直接拒绝.
const MaxUploadSize int64 = 2 << 30

// Upload 实现上传流程: 校验, 哈希层与路径层冲突检查, 外部写入, 记录入库与配额计费.
type Upload struct {
	db         *gorm.DB
	cfg        *config.Config
	settings   *Settings
	audit      *Audit
	limiter    *RateLimiter
	policies   *Policies
	log        *slog.Logger
	pathLockMu sync.Mutex
	pathLocks  map[int64]*sync.Mutex
}

// NewUpload 创建上传服务.
func NewUpload(database *gorm.DB, cfg *config.Config, settings *Settings, audit *Audit, policies *Policies, log *slog.Logger) *Upload {
	return &Upload{
		db:        database,
		cfg:       cfg,
		settings:  settings,
		audit:     audit,
		limiter:   NewRateLimiter(time.Minute),
		policies:  policies,
		pathLocks: make(map[int64]*sync.Mutex),
		log:       log,
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
	Permit    *UploadPermit
}

// UploadPermit 是在读取请求体前预留的单次上传许可.
type UploadPermit struct {
	upload *Upload
	userID int64
	used   atomic.Bool
}

// UploadResult 是上传结果.
type UploadResult struct {
	Record       *model.Image
	URLs         []model.PolicyURL
	Dedup        bool
	QuotaCharged bool
	Status       int
}

// Preflight 校验上传权限并预留限流额度, 应在读取 multipart 文件内容前调用.
func (u *Upload) Preflight(principal *Principal) (*UploadPermit, error) {
	if principal == nil || principal.User == nil {
		return nil, apperr.ErrUnauthorized
	}
	if !principal.Has(model.PermUpload) {
		return nil, apperr.ErrForbidden.WithMessage("当前用户组没有上传权限")
	}
	if err := u.allowRateLimit(principal); err != nil {
		return nil, err
	}
	return &UploadPermit{upload: u, userID: principal.User.ID}, nil
}

func (u *Upload) allowRateLimit(principal *Principal) error {
	rateLimit := EffectiveUploadRateLimit(principal.Group)
	if rateLimit <= 0 {
		return nil
	}
	allowed, retryAfter := u.limiter.Allow(fmt.Sprintf("upload:%d", principal.User.ID), rateLimit)
	if allowed {
		return nil
	}
	seconds := int(retryAfter.Seconds()) + 1
	return apperr.ErrRateLimited.WithDetails(map[string]any{"retry_after_seconds": seconds})
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

	if req.Permit != nil && (req.Permit.upload != u || req.Permit.userID != user.ID || !req.Permit.used.CompareAndSwap(false, true)) {
		return nil, apperr.ErrInvalidRequest.WithMessage("上传预检许可无效")
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
	if req.Permit == nil {
		if err := u.allowRateLimit(req.Principal); err != nil {
			return nil, err
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

	// 冲突检查第一层: 同策略下相同 sha256 的记录.
	var existing model.Image
	err = u.db.Where("policy_id = ? AND sha256 = ? AND kind = ?", policy.ID, sums.SHA256, model.KindImage).
		Order("id ASC").First(&existing).Error
	switch {
	case err == nil:
		return u.reuseRecord(req, policy, urls, defaultURL, &existing)
	case errors.Is(err, gorm.ErrRecordNotFound):
	default:
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}

	// 冲突检查第二层: 路径与外部对象.
	renderer, err := storage.NewPathRenderer(policy.PathTemplate, storage.PathVars{
		MD5:    sums.MD5,
		SHA256: sums.SHA256,
		SHA512: sums.SHA512,
		Ext:    detected.Ext,
	})
	if err != nil {
		return nil, apperr.ErrInternal.WithMessage("魔法路径渲染失败: " + err.Error())
	}
	if policy.ConflictPolicy == model.ConflictRename {
		unlock := u.lockPolicyPath(policy.ID)
		defer unlock()

		// 等待同策略的重命名上传完成后再次检查哈希, 避免并发同内容上传生成孤立对象.
		var concurrentExisting model.Image
		err := u.db.Where("policy_id = ? AND sha256 = ? AND kind = ?", policy.ID, sums.SHA256, model.KindImage).
			Order("id ASC").First(&concurrentExisting).Error
		switch {
		case err == nil:
			return u.reuseRecord(req, policy, urls, defaultURL, &concurrentExisting)
		case errors.Is(err, gorm.ErrRecordNotFound):
		default:
			return nil, apperr.ErrInternal.WithMessage(err.Error())
		}
	}

	sequence := uint64(0)
	if renderer.HasSequence() {
		sequence = 1
		if policy.ConflictPolicy == model.ConflictRename {
			prefix, suffix := renderer.SequencePattern()
			maxSequence, err := u.maxPathSequence(policy.ID, prefix, suffix)
			if err != nil {
				return nil, err
			}
			sequence, err = nextPathSequence(maxSequence)
			if err != nil {
				return nil, apperr.ErrPathConflict.WithMessage(err.Error())
			}
		}
	}
	relPath, err := renderer.Render(sequence)
	if err != nil {
		return nil, apperr.ErrInternal.WithMessage("魔法路径渲染失败: " + err.Error())
	}

	var objectKey string
	for {
		objectKey = storage.NormalizeObjectKey("", relPath)
		conflictRecord, err := u.recordForKey(policy.ID, objectKey)
		if err != nil {
			return nil, err
		}
		objectExists := conflictRecord != nil
		if conflictRecord == nil && caps.Has(storage.CapStat) {
			if _, statErr := adapter.Stat(ctx, objectKey); statErr == nil {
				objectExists = true
			} else if !errors.Is(statErr, storage.ErrNotFound) {
				u.log.Warn("冲突检查时查询外部对象失败", "object_key", objectKey, "error", statErr)
			}
		}
		if !objectExists {
			break
		}
		if conflictRecord != nil && conflictRecord.SHA256 == sums.SHA256 {
			return u.reuseRecord(req, policy, urls, defaultURL, conflictRecord)
		}
		if conflictRecord == nil {
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
		case model.ConflictSkip:
			return nil, apperr.ErrPathConflict.WithDetails(map[string]any{
				"object_key": objectKey,
				"policy":     policy.ConflictPolicy,
			})
		case model.ConflictRename:
			if !renderer.HasSequence() && sequence == 0 {
				prefix, suffix := renderer.SequencePattern()
				maxSequence, err := u.maxPathSequence(policy.ID, prefix, suffix)
				if err != nil {
					return nil, err
				}
				sequence, err = nextPathSequence(maxSequence)
				if err != nil {
					return nil, apperr.ErrPathConflict.WithMessage(err.Error())
				}
			} else {
				sequence, err = nextPathSequence(sequence)
				if err != nil {
					return nil, apperr.ErrPathConflict.WithMessage(err.Error())
				}
			}
			relPath, err = renderer.Render(sequence)
			if err != nil {
				return nil, apperr.ErrPathConflict.WithMessage("递增序号后路径无效: " + err.Error())
			}
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

	// 事务内重新判定计费并原子入账: 并发上传相同内容时只保留一条计费记录.
	var (
		charged bool
		reused  *model.Image
	)
	if err := u.db.Transaction(func(tx *gorm.DB) error {
		existing, err := recordForKeyTx(tx, policy.ID, objectKey)
		if err != nil {
			return err
		}

		// 并发上传相同内容: 幂等复用已有记录, 不新建.
		if existing != nil && existing.UserID == user.ID && existing.SHA256 == sums.SHA256 {
			reused = existing
			return nil
		}

		// 事务内再按内容确认一次 (串行化点): 并发相同内容上传只保留一条计费记录.
		contentRecord, err := contentRecordTx(tx, policy.ID, sums.SHA256, user.ID)
		if err != nil {
			return err
		}
		if contentRecord != nil && contentRecord.SHA256 == sums.SHA256 {
			reused = contentRecord
			return nil
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

// CheckRecord 是秒传检查返回的当前用户记录摘要.
type CheckRecord struct {
	ID   int64  `json:"id"`
	URL  string `json:"url"`
	Size int64  `json:"size"`
}

// Check 秒传检查: 仅返回当前用户在同策略下相同 sha256 的记录.
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
	err = u.db.Where("policy_id = ? AND sha256 = ? AND user_id = ? AND kind = ?", policy.ID, sum, principal.User.ID, model.KindImage).
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

// contentRecord 按内容哈希查询本策略下指定用户的记录 (秒传判定).
func (u *Upload) contentRecord(policyID int64, sha256 string, userID int64) (*model.Image, error) {
	return contentRecordTx(u.db, policyID, sha256, userID)
}

// contentRecordTx 在事务内按内容哈希查询当前用户的记录.
func contentRecordTx(tx *gorm.DB, policyID int64, sha256 string, userID int64) (*model.Image, error) {
	var record model.Image
	err := tx.Where("policy_id = ? AND sha256 = ? AND user_id = ? AND kind = ?",
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

// recordForKey 查询同一策略下指定对象键的记录; 不存在时返回 nil, nil.
func (u *Upload) recordForKey(policyID int64, objectKey string) (*model.Image, error) {
	return recordForKeyTx(u.db, policyID, objectKey)
}

// recordForKeyTx 在事务内查询同一策略下指定对象键的记录; 不存在时返回 nil, nil.
func recordForKeyTx(tx *gorm.DB, policyID int64, objectKey string) (*model.Image, error) {
	var record model.Image
	err := tx.Where("policy_id = ? AND object_key = ?", policyID, objectKey).
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

func (u *Upload) lockPolicyPath(policyID int64) func() {
	u.pathLockMu.Lock()
	if u.pathLocks == nil {
		u.pathLocks = make(map[int64]*sync.Mutex)
	}
	lock := u.pathLocks[policyID]
	if lock == nil {
		lock = &sync.Mutex{}
		u.pathLocks[policyID] = lock
	}
	u.pathLockMu.Unlock()

	lock.Lock()
	return lock.Unlock
}

func (u *Upload) maxPathSequence(policyID int64, prefix, suffix string) (uint64, error) {
	pattern := escapeLikeLiteral(prefix) + "%" + escapeLikeLiteral(suffix)
	rows, err := u.db.Model(&model.Image{}).
		Select("object_key").
		Where("policy_id = ? AND object_key LIKE ? ESCAPE '!'", policyID, pattern).
		Rows()
	if err != nil {
		return 0, apperr.ErrInternal.WithMessage("查询递增序号失败: " + err.Error())
	}
	defer rows.Close()

	var highest uint64
	for rows.Next() {
		var objectKey string
		if err := rows.Scan(&objectKey); err != nil {
			return 0, apperr.ErrInternal.WithMessage("读取递增序号失败: " + err.Error())
		}
		if len(objectKey) < len(prefix)+len(suffix) ||
			!strings.HasPrefix(objectKey, prefix) ||
			!strings.HasSuffix(objectKey, suffix) {
			continue
		}
		digits := objectKey[len(prefix) : len(objectKey)-len(suffix)]
		if digits == "" {
			continue
		}
		isDecimal := true
		for i := range digits {
			if digits[i] < '0' || digits[i] > '9' {
				isDecimal = false
				break
			}
		}
		if !isDecimal {
			continue
		}
		sequence, err := strconv.ParseUint(digits, 10, 64)
		if err == nil && sequence > highest {
			highest = sequence
		}
	}
	if err := rows.Err(); err != nil {
		return 0, apperr.ErrInternal.WithMessage("读取递增序号失败: " + err.Error())
	}
	return highest, nil
}

func escapeLikeLiteral(value string) string {
	if !strings.ContainsAny(value, "!%_") {
		return value
	}
	var escaped strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] == '!' || value[i] == '%' || value[i] == '_' {
			escaped.WriteByte('!')
		}
		escaped.WriteByte(value[i])
	}
	return escaped.String()
}

func nextPathSequence(current uint64) (uint64, error) {
	if current == ^uint64(0) {
		return 0, errors.New("递增序号已达到上限")
	}
	return current + 1, nil
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
