package imgdetect

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"

	"golang.org/x/image/bmp"
	"golang.org/x/image/tiff"
)

// encodePNG 生成测试用 PNG.
func encodePNG(t *testing.T, width, height int) []byte {
	t.Helper()
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	for x := range width {
		for y := range height {
			canvas.Set(x, y, color.RGBA{R: uint8(x * 7), G: uint8(y * 11), B: 128, A: 255})
		}
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, canvas); err != nil {
		t.Fatalf("编码 PNG 失败: %v", err)
	}
	return buffer.Bytes()
}

func TestDetectAndDimensions(t *testing.T) {
	pngBytes := encodePNG(t, 12, 7)
	info := Detect(pngBytes)
	if info.Format != "png" || info.Ext != "png" || info.MIME != "image/png" {
		t.Fatalf("PNG 探测结果不正确: %+v", info)
	}
	if !IsImage(info) {
		t.Error("PNG 应判定为位图")
	}
	if width, height := Dimensions(bytes.NewReader(pngBytes), info); width != 12 || height != 7 {
		t.Errorf("PNG 尺寸解析错误: %dx%d", width, height)
	}

	jpegBuffer := &bytes.Buffer{}
	canvas := image.NewRGBA(image.Rect(0, 0, 5, 9))
	if err := jpeg.Encode(jpegBuffer, canvas, nil); err != nil {
		t.Fatalf("编码 JPEG 失败: %v", err)
	}
	jpegInfo := Detect(jpegBuffer.Bytes())
	if jpegInfo.Format != "jpeg" || jpegInfo.Ext != "jpg" || jpegInfo.MIME != "image/jpeg" {
		t.Errorf("JPEG 探测结果不正确: %+v", jpegInfo)
	}
	if width, height := Dimensions(bytes.NewReader(jpegBuffer.Bytes()), jpegInfo); width != 5 || height != 9 {
		t.Errorf("JPEG 尺寸解析错误: %dx%d", width, height)
	}

	gifBuffer := &bytes.Buffer{}
	if err := gif.Encode(gifBuffer, canvas, nil); err != nil {
		t.Fatalf("编码 GIF 失败: %v", err)
	}
	gifInfo := Detect(gifBuffer.Bytes())
	if gifInfo.Format != "gif" || gifInfo.Ext != "gif" {
		t.Errorf("GIF 探测结果不正确: %+v", gifInfo)
	}

	bmpBuffer := &bytes.Buffer{}
	if err := bmp.Encode(bmpBuffer, canvas); err != nil {
		t.Fatalf("编码 BMP 失败: %v", err)
	}
	if got := Detect(bmpBuffer.Bytes()); got.Format != "bmp" || got.Ext != "bmp" {
		t.Errorf("BMP 探测结果不正确: %+v", got)
	}

	tiffBuffer := &bytes.Buffer{}
	if err := tiff.Encode(tiffBuffer, canvas, nil); err != nil {
		t.Fatalf("编码 TIFF 失败: %v", err)
	}
	tiffInfo := Detect(tiffBuffer.Bytes())
	if tiffInfo.Format != "tiff" || tiffInfo.Ext != "tif" {
		t.Errorf("TIFF 探测结果不正确 (期望 tif 后缀): %+v", tiffInfo)
	}
}

func TestDetectWebPAndAVIFAndHEIC(t *testing.T) {
	webpHeader := append([]byte("RIFF"), 0x00, 0x00, 0x00, 0x00)
	webpHeader = append(webpHeader, []byte("WEBPVP8 ")...)
	if got := Detect(webpHeader); got.Format != "webp" || got.Ext != "webp" {
		t.Errorf("WebP 探测结果不正确: %+v", got)
	}

	avif := append([]byte{0x00, 0x00, 0x00, 0x20}, []byte("ftypavif")...)
	avif = append(avif, make([]byte, 8)...)
	if got := Detect(avif); got.Format != "avif" || got.Ext != "avif" {
		t.Errorf("AVIF 探测结果不正确: %+v", got)
	}

	heic := append([]byte{0x00, 0x00, 0x00, 0x20}, []byte("ftypheic")...)
	heic = append(heic, make([]byte, 8)...)
	if got := Detect(heic); got.Format != "heic" || got.Ext != "heic" {
		t.Errorf("HEIC 探测结果不正确: %+v", got)
	}
}

func TestDetectICO(t *testing.T) {
	ico := []byte{0x00, 0x00, 0x01, 0x00, 0x01, 0x00}
	ico = append(ico, 0x20, 0x10, 0x00, 0x00, 0x01, 0x00, 0x20, 0x00)
	ico = append(ico, make([]byte, 8)...)
	info := Detect(ico)
	if info.Format != "ico" || info.Ext != "ico" {
		t.Fatalf("ICO 探测结果不正确: %+v", info)
	}
	width, height := Dimensions(bytes.NewReader(ico), info)
	if width != 32 || height != 16 {
		t.Errorf("ICO 尺寸解析错误: %dx%d", width, height)
	}
}

func TestDetectSVGIsNeverImage(t *testing.T) {
	svg := []byte("<svg xmlns=\"http://www.w3.org/2000/svg\"><rect width=\"1\" height=\"1\"/></svg>")
	info := Detect(svg)
	if !IsSVG(info) {
		t.Fatalf("SVG 未被识别: %+v", info)
	}
	if IsImage(info) {
		t.Error("SVG 不应判定为可上传位图")
	}
}

func TestDetectNonImageFiles(t *testing.T) {
	if got := Detect([]byte("%PDF-1.7\n")); got.Format != "pdf" {
		t.Errorf("PDF 探测结果不正确: %+v", got)
	}
	if got := Detect([]byte{'P', 'K', 0x03, 0x04, 0x14, 0x00}); got.Format != "zip" {
		t.Errorf("ZIP 探测结果不正确: %+v", got)
	}
	if got := Detect([]byte("hello world, plain text")); got.Format != "txt" {
		t.Errorf("文本探测结果不正确: %+v", got)
	}
	if got := Detect([]byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07}); got.Format != "unknown" {
		t.Errorf("未知二进制应返回 unknown: %+v", got)
	}
}

func TestSanitizeFilenameBase(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"photo.png", "photo"},
		{"../evil.php", "evil"},
		{"my photo (1).jpg", "my photo (1)"},
		{"中文名字.png", "中文名字"},
		{"...", "image"},
		{"", "image"},
	}
	for _, testCase := range cases {
		if got := SanitizeFilenameBase(testCase.in); got != testCase.want {
			t.Errorf("SanitizeFilenameBase(%q) = %q, 期望 %q", testCase.in, got, testCase.want)
		}
	}
	if got := SanitizeFilenameBase(strings.Repeat("a", 300)); len(got) != 128 {
		t.Errorf("超长文件名应截断到 128 字符, 实际 %d", len(got))
	}
	if got := Detect([]byte{0xff, 0xd8, 0xff, 0xe0}); got.Format != "jpeg" {
		t.Errorf("JPEG 魔数探测失败: %+v", got)
	}
}
