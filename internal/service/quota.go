package service

import (
	"strings"

	"github.com/yeqingky/Picsky/internal/apperr"
	"github.com/yeqingky/Picsky/internal/model"
)

// Quota 是生效配额: 用户级覆盖 -> 组级 -> 系统默认.
type Quota struct {
	Mode  string `json:"mode"`
	Limit *int64 `json:"limit"`
	Used  int64  `json:"used"`
}

// EffectiveQuota 计算用户生效配额.
func EffectiveQuota(settings *Settings, group *model.Group, user *model.User) Quota {
	mode := ""
	var limit *int64
	used := int64(0)

	switch {
	case user != nil && user.QuotaMode != nil && *user.QuotaMode != "":
		mode = *user.QuotaMode
		limit = user.QuotaValue
	case group != nil && group.QuotaMode != nil && *group.QuotaMode != "":
		mode = *group.QuotaMode
		limit = group.QuotaValue
	default:
		if mode = strings.TrimSpace(settings.String("default_quota_mode")); mode != "" {
			limit = settings.OptionalInt64("default_quota_value")
		}
	}

	if user != nil {
		switch mode {
		case model.QuotaModeCount:
			used = user.UsedImages
		case model.QuotaModeBytes:
			used = user.UsedBytes
		}
	}
	return Quota{Mode: mode, Limit: limit, Used: used}
}

// CheckQuota 校验配额; addImages 与 addBytes 是本次新增的用量.
func CheckQuota(quota Quota, addImages, addBytes int64) error {
	if quota.Mode == "" || quota.Limit == nil {
		return nil
	}
	switch quota.Mode {
	case model.QuotaModeCount:
		if quota.Used+addImages > *quota.Limit {
			return apperr.ErrQuotaExceeded.WithDetails(map[string]any{
				"mode":  quota.Mode,
				"used":  quota.Used,
				"limit": *quota.Limit,
			})
		}
	case model.QuotaModeBytes:
		if quota.Used+addBytes > *quota.Limit {
			return apperr.ErrQuotaExceeded.WithDetails(map[string]any{
				"mode":  quota.Mode,
				"used":  quota.Used,
				"limit": *quota.Limit,
			})
		}
	default:
		// 未知模式不静默放行: 由调用方保证 mode 合法 (users/groups 校验).
		return apperr.ErrInternal.WithMessage("未知配额方式: " + quota.Mode)
	}
	return nil
}

// EffectiveMaxFileSize 计算单文件体积上限; 0 表示不限.
func EffectiveMaxFileSize(settings *Settings, group *model.Group) int64 {
	if group != nil && group.MaxFileSizeBytes != nil {
		if *group.MaxFileSizeBytes == 0 {
			return 0
		}
		return *group.MaxFileSizeBytes
	}
	return settings.Int64("max_file_size_bytes", 10*1024*1024)
}

// EffectiveAllowedFormats 计算格式白名单: 用户组只能在系统基线内收紧.
func EffectiveAllowedFormats(settings *Settings, group *model.Group) []string {
	baseline := splitFormats(settings.String("default_allowed_formats"))
	if len(baseline) == 0 {
		baseline = splitFormats("png,jpg,jpeg,gif,webp,bmp,avif,tiff,ico,heic")
	}
	if group == nil || strings.TrimSpace(group.AllowedFormats) == "" {
		return baseline
	}
	allowed := make([]string, 0, len(baseline))
	groupSet := map[string]bool{}
	for _, item := range splitFormats(group.AllowedFormats) {
		groupSet[item] = true
	}
	for _, item := range baseline {
		if groupSet[item] {
			allowed = append(allowed, item)
		}
	}
	return allowed
}

// EffectiveUploadRateLimit 计算每分钟上传次数上限; 0 表示不限.
func EffectiveUploadRateLimit(settings *Settings, group *model.Group) int {
	if group != nil && group.UploadRateLimit != nil {
		return *group.UploadRateLimit
	}
	return int(settings.Int64("upload_rate_limit", 60))
}

// splitFormats 拆分并规范化格式列表 (小写, 去空项).
func splitFormats(raw string) []string {
	items := strings.Split(raw, ",")
	out := make([]string, 0, len(items))
	for _, item := range items {
		trimmed := strings.ToLower(strings.TrimSpace(item))
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// formatAllowed 判断探测出的真实格式是否在白名单内; SVG 永远禁止.
func formatAllowed(format string, allowed []string) bool {
	if format == "" || format == "svg" {
		return false
	}
	for _, item := range allowed {
		if item == format {
			return true
		}
	}
	return false
}
