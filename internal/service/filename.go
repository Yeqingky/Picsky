package service

import (
	"strings"
)

// ExtensionOf 返回路径或文件名的小写扩展名 (不含点).
func ExtensionOf(name string) string {
	base := name
	if idx := strings.LastIndexByte(base, '/'); idx >= 0 {
		base = base[idx+1:]
	}
	idx := strings.LastIndexByte(base, '.')
	if idx < 0 || idx == len(base)-1 {
		return ""
	}
	return strings.ToLower(strings.TrimPrefix(base[idx+1:], "."))
}

// FormatFromExtension 把扩展名映射为探测格式名; 未知返回空串.
func FormatFromExtension(name string) string {
	switch ExtensionOf(name) {
	case "jpg", "jpeg":
		return "jpeg"
	case "png":
		return "png"
	case "gif":
		return "gif"
	case "webp":
		return "webp"
	case "bmp":
		return "bmp"
	case "tif", "tiff":
		return "tiff"
	case "avif":
		return "avif"
	case "heic", "heif":
		return "heic"
	case "ico":
		return "ico"
	case "pdf":
		return "pdf"
	case "zip":
		return "zip"
	case "gz":
		return "gzip"
	case "rar":
		return "rar"
	case "7z":
		return "7z"
	case "mp4":
		return "mp4"
	case "mp3":
		return "mp3"
	case "txt":
		return "txt"
	default:
		return ""
	}
}
