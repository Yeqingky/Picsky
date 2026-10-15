package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/yeqingky/Picsky/internal/apperr"
	"github.com/yeqingky/Picsky/internal/model"
	"github.com/yeqingky/Picsky/internal/service"
)

func TestReadBase64UploadPayloadDecodesSupportedVariants(t *testing.T) {
	standardBytes := []byte{0xfb, 0xff, 0xff}
	standard := base64.StdEncoding.EncodeToString(standardBytes)
	urlSafe := base64.RawURLEncoding.EncodeToString(standardBytes)
	rawShort := base64.RawURLEncoding.EncodeToString([]byte("M"))
	rawMedium := base64.RawURLEncoding.EncodeToString([]byte("Ma"))
	whitespaceData, err := json.Marshal(" \t" + standard + "\r\n ")
	if err != nil {
		t.Fatal(err)
	}
	crlfData, err := json.Marshal(standard[:4] + "\r\n" + standard[4:])
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		body       string
		want       []byte
		filename   string
		policyID   int64
		wantPolicy bool
	}{
		{
			name: "standard padded",
			body: `{"data":"` + standard + `"}`,
			want: standardBytes,
		},
		{
			name: "raw URL alphabet",
			body: `{"data":"` + urlSafe + `"}`,
			want: standardBytes,
		},
		{
			name: "raw URL without padding",
			body: `{"data":"` + rawShort + `"}`,
			want: []byte("M"),
		},
		{
			name: "raw URL with three-character tail",
			body: `{"data":"` + rawMedium + `"}`,
			want: []byte("Ma"),
		},
		{
			name: "data URL",
			body: `{"data":"data:image/png;base64,` + standard + `"}`,
			want: standardBytes,
		},
		{
			name: "JSON escaped base64 character",
			body: `{"data":"\u0059WJj"}`,
			want: []byte("abc"),
		},
		{
			name:       "metadata after data",
			body:       `{"data":"` + standard + `","filename":"after.png","policy_id":9}`,
			want:       standardBytes,
			filename:   "after.png",
			policyID:   9,
			wantPolicy: true,
		},
		{
			name: "trimmed whitespace",
			body: `{"data":` + string(whitespaceData) + `}`,
			want: standardBytes,
		},
		{
			name: "CRLF within base64",
			body: `{"data":` + string(crlfData) + `}`,
			want: standardBytes,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload := decodeBase64UploadForTest(t, test.body, 2, int64(len(test.want))+1)
			defer payload.Spooled.Spool.Close()
			got, err := readBase64Spool(payload)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, test.want) {
				t.Fatalf("解码结果不符: got %x, want %x", got, test.want)
			}
			if payload.Filename != test.filename || payload.Spooled.Filename != test.filename {
				t.Errorf("文件名不符: payload=%q spool=%q", payload.Filename, payload.Spooled.Filename)
			}
			if test.wantPolicy && (payload.PolicyID == nil || *payload.PolicyID != test.policyID) {
				t.Errorf("策略 ID 不符: got %v, want %d", payload.PolicyID, test.policyID)
			}
		})
	}
}

func TestReadBase64UploadPayloadUsesConfiguredSpoolLimit(t *testing.T) {
	content := bytes.Repeat([]byte{0xa5}, 1<<20)
	encoded := base64.StdEncoding.EncodeToString(content)
	tempDir := t.TempDir()
	payload, err := readBase64UploadPayload(strings.NewReader(`{"data":"`+encoded+`"}`), tempDir, 1024, int64(len(content)))
	if err != nil {
		t.Fatal(err)
	}
	defer payload.Spooled.Spool.Close()
	if !payload.Spooled.Spool.OnDisk() {
		t.Fatal("超过 PICSKY_UPLOAD_MEMORY_LIMIT 后仍驻留内存")
	}
	if got := payload.Spooled.Spool.Size(); got != int64(len(content)) {
		t.Fatalf("缓冲大小错误: got %d, want %d", got, len(content))
	}
	got, err := readBase64Spool(payload)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Fatal("临时文件中的解码内容不一致")
	}
}

func TestReadBase64UploadPayloadEnforcesDecodedLimitAndCleansSpool(t *testing.T) {
	content := bytes.Repeat([]byte{0x5a}, 4096)
	encoded := base64.StdEncoding.EncodeToString(content)
	tempDir := t.TempDir()
	_, err := readBase64UploadPayload(strings.NewReader(`{"data":"`+encoded+`"}`), tempDir, 8, int64(len(content)-1))
	if !errors.Is(err, apperr.ErrFileTooLarge) {
		t.Fatalf("超过单文件上限应返回 file_too_large, got %v", err)
	}
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("拒绝超限上传后遗留临时文件: %v", entries)
	}
}

func TestReadBase64UploadPayloadRejectsMalformedDataAndCleansSpool(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
	}{
		{name: "invalid alphabet", body: `{"data":"YWJj?"}`},
		{name: "incomplete padding", body: `{"data":"YQ="}`},
		{name: "excess padding", body: `{"data":"YQ==="}`},
		{name: "data after padding", body: `{"data":"YQ==YQ"}`},
		{name: "interior whitespace", body: `{"data":"YW Jj"}`},
		{name: "mixed alphabets", body: `{"data":"+___"}`},
		{name: "empty data", body: `{"data":""}`},
		{name: "non-base64 data URL", body: `{"data":"data:image/png,SGVsbG8="}`},
		{name: "duplicate data", body: `{"data":"YWJj","data":"TWFu"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			tempDir := t.TempDir()
			_, err := readBase64UploadPayload(strings.NewReader(test.body), tempDir, 1, 1<<20)
			if !errors.Is(err, apperr.ErrInvalidRequest) {
				t.Fatalf("非法 base64 请求应被拒绝, got %v", err)
			}
			entries, err := os.ReadDir(tempDir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("请求失败后遗留临时文件: %v", entries)
			}
		})
	}
}

func TestReadBase64UploadPayloadBoundsMetadata(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte("data"))
	tests := []struct {
		name string
		body string
	}{
		{
			name: "metadata before data",
			body: `{"padding":"` + strings.Repeat("x", maxBase64JSONMetadataBytes) + `","data":"` + encoded + `"}`,
		},
		{
			name: "metadata after data",
			body: `{"data":"` + encoded + `","padding":"` + strings.Repeat("x", maxBase64JSONMetadataBytes) + `"}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := readBase64UploadPayload(strings.NewReader(test.body), t.TempDir(), 8, 1024)
			if !errors.Is(err, apperr.ErrInvalidRequest) {
				t.Fatalf("超出 JSON 元数据预算应返回 invalid_request, got %v", err)
			}
		})
	}
}

func TestBase64BodyLimitUsesHardLimitForUnlimitedGroups(t *testing.T) {
	unlimited := int64(0)
	group := &model.Group{MaxFileSizeBytes: &unlimited}
	limit := service.UploadLimit(nil, group)
	if limit != service.MaxUploadSize {
		t.Fatalf("不限组应受单次上传硬上限约束: got %d, want %d", limit, service.MaxUploadSize)
	}
	want := service.MaxUploadSize/3*4 + 4 + base64JSONBodyOverhead
	if got := base64UploadRequestBodyLimit(limit); got != want {
		t.Fatalf("base64 JSON 请求上限错误: got %d, want %d", got, want)
	}
}

func decodeBase64UploadForTest(t *testing.T, body string, memLimit, limit int64) *base64UploadPayload {
	t.Helper()
	payload, err := readBase64UploadPayload(strings.NewReader(body), t.TempDir(), memLimit, limit)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func readBase64Spool(payload *base64UploadPayload) ([]byte, error) {
	reader, err := payload.Spooled.Spool.Reader()
	if err != nil {
		return nil, err
	}
	return io.ReadAll(reader)
}
