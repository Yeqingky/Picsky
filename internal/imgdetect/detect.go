// Package imgdetect 通过魔数探测真实格式, 并解析图片尺寸; 不信任扩展名与 Content-Type.
package imgdetect

import (
	"bytes"
	"encoding/binary"
	"image"
	"io"
	"strings"
	"unicode"

	// 注册位图解码器, 仅用于读取尺寸; 不支持的格式宽高留空.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
)

// Info 是探测结果.
type Info struct {
	Format string
	Ext    string
	MIME   string
}

type formatSpec struct {
	Format    string
	Ext       string
	MIME      string
	IsImage   bool
	Dimension bool // 是否能解析宽高
}

var (
	fmtJPEG    = formatSpec{"jpeg", "jpg", "image/jpeg", true, true}
	fmtPNG     = formatSpec{"png", "png", "image/png", true, true}
	fmtGIF     = formatSpec{"gif", "gif", "image/gif", true, true}
	fmtWebP    = formatSpec{"webp", "webp", "image/webp", true, true}
	fmtBMP     = formatSpec{"bmp", "bmp", "image/bmp", true, true}
	fmtTIFF    = formatSpec{"tiff", "tif", "image/tiff", true, true}
	fmtAVIF    = formatSpec{"avif", "avif", "image/avif", true, false}
	fmtHEIC    = formatSpec{"heic", "heic", "image/heic", true, false}
	fmtICO     = formatSpec{"ico", "ico", "image/x-icon", true, false}
	fmtSVG     = formatSpec{"svg", "svg", "image/svg+xml", true, false}
	fmtPDF     = formatSpec{"pdf", "pdf", "application/pdf", false, false}
	fmtZIP     = formatSpec{"zip", "zip", "application/zip", false, false}
	fmtGzip    = formatSpec{"gzip", "gz", "application/gzip", false, false}
	fmtRar     = formatSpec{"rar", "rar", "application/vnd.rar", false, false}
	fmt7z      = formatSpec{"7z", "7z", "application/x-7z-compressed", false, false}
	fmtMP4     = formatSpec{"mp4", "mp4", "video/mp4", false, false}
	fmtMP3     = formatSpec{"mp3", "mp3", "audio/mpeg", false, false}
	fmtText    = formatSpec{"txt", "txt", "text/plain", false, false}
	fmtUnknown = formatSpec{"unknown", "", "application/octet-stream", false, false}
)

// Known 返回格式对应的规范 (导出给上层做格式映射).
func Known(format string) (Info, bool) {
	for _, spec := range knownSpecs() {
		if spec.Format == format {
			return Info{Format: spec.Format, Ext: spec.Ext, MIME: spec.MIME}, true
		}
	}
	return Info{}, false
}

func knownSpecs() []formatSpec {
	return []formatSpec{fmtJPEG, fmtPNG, fmtGIF, fmtWebP, fmtBMP, fmtTIFF, fmtAVIF, fmtHEIC, fmtICO, fmtPDF, fmtZIP, fmtGzip, fmtRar, fmt7z, fmtMP4, fmtMP3, fmtText}
}

// Detect 根据文件头判断真实格式; 无法识别时返回 unknown.
func Detect(head []byte) Info {
	spec := detectSpec(head)
	return Info{Format: spec.Format, Ext: spec.Ext, MIME: spec.MIME}
}

// IsImage 判断探测结果是否为位图图片 (SVG 不算: 系统级禁止上传).
func IsImage(info Info) bool {
	spec, ok := specOf(info.Format)
	return ok && spec.IsImage && spec.Format != fmtSVG.Format
}

// IsSVG 判断探测结果是否为 SVG.
func IsSVG(info Info) bool { return info.Format == fmtSVG.Format }

func specOf(format string) (formatSpec, bool) {
	for _, spec := range append(knownSpecs(), fmtSVG) {
		if spec.Format == format {
			return spec, true
		}
	}
	return fmtUnknown, false
}

func detectSpec(head []byte) formatSpec {
	switch {
	case len(head) >= 8 && bytes.Equal(head[:8], []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}):
		return fmtPNG
	case len(head) >= 3 && head[0] == 0xff && head[1] == 0xd8 && head[2] == 0xff:
		return fmtJPEG
	case len(head) >= 6 && (bytes.Equal(head[:6], []byte("GIF87a")) || bytes.Equal(head[:6], []byte("GIF89a"))):
		return fmtGIF
	case len(head) >= 12 && bytes.Equal(head[:4], []byte("RIFF")) && bytes.Equal(head[8:12], []byte("WEBP")):
		return fmtWebP
	case len(head) >= 2 && head[0] == 'B' && head[1] == 'M':
		return fmtBMP
	case len(head) >= 4 && (bytes.Equal(head[:4], []byte{'I', 'I', 0x2a, 0x00}) || bytes.Equal(head[:4], []byte{'M', 'M', 0x00, 0x2a})):
		return fmtTIFF
	case len(head) >= 4 && bytes.Equal(head[:4], []byte{0x00, 0x00, 0x01, 0x00}):
		return fmtICO
	case len(head) >= 12 && bytes.Equal(head[4:8], []byte("ftyp")):
		brand := string(head[8:12])
		switch brand {
		case "avif", "avis":
			return fmtAVIF
		case "heic", "heix", "hevc", "hevx", "heim", "heis", "mif1", "msf1":
			return fmtHEIC
		default:
			return fmtMP4
		}
	case len(head) >= 5 && bytes.Equal(head[:5], []byte("%PDF-")):
		return fmtPDF
	case len(head) >= 4 && bytes.Equal(head[:4], []byte{'P', 'K', 0x03, 0x04}):
		return fmtZIP
	case len(head) >= 2 && head[0] == 0x1f && head[1] == 0x8b:
		return fmtGzip
	case len(head) >= 7 && bytes.Equal(head[:7], []byte("Rar!\x1a\x07\x00")):
		return fmtRar
	case len(head) >= 6 && bytes.Equal(head[:6], []byte{'7', 'z', 0xbc, 0xaf, 0x27, 0x1c}):
		return fmt7z
	case len(head) >= 3 && bytes.Equal(head[:3], []byte("ID3")):
		return fmtMP3
	case len(head) >= 2 && head[0] == 0xff && (head[1]&0xe0) == 0xe0:
		return fmtMP3
	}

	trimmed := bytes.TrimSpace(head)
	lower := bytes.ToLower(trimmed)
	if bytes.HasPrefix(lower, []byte("<svg")) || (bytes.HasPrefix(lower, []byte("<?xml")) && bytes.Contains(lower, []byte("<svg"))) {
		return fmtSVG
	}
	if isMostlyText(trimmed) && len(trimmed) > 0 {
		return fmtText
	}
	return fmtUnknown
}

// isMostlyText 粗略判断内容是否为文本.
func isMostlyText(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	printable := 0
	for _, b := range body {
		if b == '\n' || b == '\r' || b == '\t' || (b >= 0x20 && b < 0x7f) {
			printable++
		}
	}
	return printable*100/len(body) >= 95
}

// Dimensions 解析图片宽高; 无法解析时返回 0, 0.
func Dimensions(reader io.ReadSeeker, info Info) (int, int) {
	if info.Format == fmtICO.Format {
		return icoDimensions(reader)
	}
	spec, ok := specOf(info.Format)
	if !ok || !spec.Dimension {
		return 0, 0
	}
	if _, err := reader.Seek(0, io.SeekStart); err != nil {
		return 0, 0
	}
	cfg, _, err := image.DecodeConfig(reader)
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}

// icoDimensions 从 ICO 头解析尺寸 (0 表示 256).
func icoDimensions(reader io.ReadSeeker) (int, int) {
	if _, err := reader.Seek(0, io.SeekStart); err != nil {
		return 0, 0
	}
	// ICONDIR: reserved(2) + type(2) + count(2)
	header := make([]byte, 6)
	if _, err := io.ReadFull(reader, header); err != nil {
		return 0, 0
	}
	if binary.LittleEndian.Uint16(header[0:2]) != 0 || binary.LittleEndian.Uint16(header[2:4]) != 1 {
		return 0, 0
	}
	// ICONDIRENTRY: width(1) + height(1) + ...
	entry := make([]byte, 16)
	if _, err := io.ReadFull(reader, entry); err != nil {
		return 0, 0
	}
	width, height := int(entry[0]), int(entry[1])
	if width == 0 {
		width = 256
	}
	if height == 0 {
		height = 256
	}
	return width, height
}

// SanitizeFilenameBase 清洗原始文件名主体: 去掉路径, 控制字符与系统保留字符.
func SanitizeFilenameBase(name string) string {
	name = strings.TrimSpace(name)
	if idx := strings.LastIndexAny(name, `/\`); idx >= 0 {
		name = name[idx+1:]
	}
	if idx := strings.LastIndexByte(name, '.'); idx > 0 {
		name = name[:idx]
	}
	var sb strings.Builder
	for _, r := range name {
		switch {
		case unicode.IsControl(r):
			continue
		case r == '/' || r == '\\' || r == ':' || r == '*' || r == '?' || r == '"' || r == '<' || r == '>' || r == '|':
			continue
		default:
			sb.WriteRune(r)
		}
	}
	cleaned := strings.Trim(sb.String(), " .")
	if cleaned == "" {
		cleaned = "image"
	}
	if len([]rune(cleaned)) > 128 {
		cleaned = string([]rune(cleaned)[:128])
	}
	return cleaned
}
