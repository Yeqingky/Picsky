package service

import (
	"strconv"
	"strings"

	"github.com/yeqingky/Picsky/internal/apperr"
	"github.com/yeqingky/Picsky/internal/model"
	"github.com/yeqingky/Picsky/internal/storage"
)

// Quota 是生效配额: 用户级覆盖 -> 组级 -> 不限.
type Quota struct {
	Mode  string `json:"mode"`
	Limit *int64 `json:"limit"`
	Used  int64  `json:"used"`
}

// EffectiveQuota 计算用户生效配额.
func EffectiveQuota(group *model.Group, user *model.User) Quota {
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

// SystemMaxFileSize 返回系统基线单文件上限; 0 表示不限.
func SystemMaxFileSize(settings *Settings) int64 {
	if settings == nil {
		return 0
	}
	return settings.Int64("max_file_size_bytes", 10*1024*1024)
}

// EffectiveMaxFileSize 计算账号级单文件体积上限 (系统基线始终生效, 用户组只能收紧); 0 表示不限.
func EffectiveMaxFileSize(settings *Settings, group *model.Group) int64 {
	return FileSizeLimits(SystemMaxFileSize(settings), groupMaxFileSize(group), 0).Effective
}

// FileSizeLimit 是一次上传生效的三级上限 (字节): 0 表示该级不限.
type FileSizeLimit struct {
	Global int64
	Group  int64
	Driver int64
	// Effective 是三级中最小且非零的上限; 0 表示三级都不限.
	Effective int64
	// Source 标明生效上限来自哪一级: global / group / driver; Effective 为 0 时为空串.
	Source string
}

// FileSizeLimits 计算一次上传的三级上限. global 为系统基线, group 为用户组覆盖 (0 表示继承),
// driver 为驱动声明 (0 表示不限); 生效值为三者中最小的非零上限.
func FileSizeLimits(global, group, driver int64) FileSizeLimit {
	limit := FileSizeLimit{Global: global, Group: group, Driver: driver}
	for _, candidate := range []struct {
		source string
		value  int64
	}{
		{"global", global},
		{"group", group},
		{"driver", driver},
	} {
		if candidate.value <= 0 {
			continue
		}
		if limit.Effective == 0 || candidate.value < limit.Effective {
			limit.Effective = candidate.value
			limit.Source = candidate.source
		}
	}
	return limit
}

// EffectiveAllowedFormats 计算允许上传的格式: 只取系统设置里的格式基线 (全局生效).
func EffectiveAllowedFormats(settings *Settings) []string {
	baseline := splitFormats(settings.String("default_allowed_formats"))
	if len(baseline) == 0 {
		baseline = splitFormats("png,jpg,jpeg,gif,webp,bmp,avif,tiff,ico,heic")
	}
	return baseline
}

// EffectiveUploadRateLimit 计算每分钟上传次数上限; 0 表示不限.
func EffectiveUploadRateLimit(group *model.Group) int {
	if group == nil || group.UploadRateLimit == nil {
		return 0
	}
	return *group.UploadRateLimit
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

// groupMaxFileSize 返回用户组的额外单文件上限; 0 或未设置表示不额外限制 (继承全局).
func groupMaxFileSize(group *model.Group) int64 {
	if group == nil || group.MaxFileSizeBytes == nil || *group.MaxFileSizeBytes <= 0 {
		return 0
	}
	return *group.MaxFileSizeBytes
}

// DriverMaxFileSize 返回驱动声明的单对象上限; 0 表示不额外限制.
func DriverMaxFileSize(typeName string) int64 {
	spec, ok := storage.SpecOf(typeName)
	if !ok {
		return 0
	}
	return spec.MaxFileSize
}

// PolicyMaxFileSize 返回策略生效的单对象上限: 策略级设置优先, 未设置时取驱动声明.
func PolicyMaxFileSize(policy *model.StoragePolicy) int64 {
	if policy == nil {
		return 0
	}
	if policy.MaxFileSizeBytes > 0 {
		return policy.MaxFileSizeBytes
	}
	return DriverMaxFileSize(policy.Type)
}

// MaxAllowedPolicyFileSize 是策略级上限可设置的最大值: 驱动声明与系统基线取较小者, 0 表示不限.
func MaxAllowedPolicyFileSize(typeName string, global int64) int64 {
	driver := DriverMaxFileSize(typeName)
	if driver <= 0 {
		return global
	}
	if global <= 0 || driver < global {
		return driver
	}
	return global
}

// FormatBytes 把字节数格式化为 1024 进制的可读文本; 不能被 1024 整除时按字节显示.
func FormatBytes(bytes int64) string {
	if bytes <= 0 {
		return "不限"
	}
	if bytes%1024 != 0 {
		return strconv.FormatInt(bytes, 10) + " B"
	}
	units := []string{"KiB", "MiB", "GiB", "TiB"}
	value := float64(bytes) / 1024
	unit := 0
	for value >= 1024 && unit < len(units)-1 {
		value /= 1024
		unit++
	}
	text := strconv.FormatFloat(value, 'f', 2, 64)
	text = strings.TrimRight(strings.TrimRight(text, "0"), ".")
	return text + " " + units[unit]
}
