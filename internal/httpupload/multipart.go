// Package httpupload 解析 multipart 上传请求: 单遍读取文件字段并收集其他表单字段.
package httpupload

import (
	"errors"
	"io"
	"mime/multipart"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/yeqingky/Picsky/internal/apperr"
	"github.com/yeqingky/Picsky/internal/service"
)

// Multipart 字段按单值长度, 字段名和值总量及字段数量共同限界.
const (
	maxFormFieldLength     = 4096
	maxFormFieldTotalBytes = 64 << 10
	maxFormFieldCount      = 64
)

// Result 是一次 multipart 解析结果.
type Result struct {
	Spooled  *service.SpooledUpload
	Filename string
	Fields   map[string]string
}

// RequestReader 保留 multipart 流, 支持在读取文件前检查首个表单字段.
type RequestReader struct {
	reader      *multipart.Reader
	fields      map[string]string
	fieldBudget multipartFieldBudget
	firstRead   bool
	consumed    bool
}

type multipartFieldBudget struct {
	totalBytes int
	count      int
}

func (b *multipartFieldBudget) add(name string, value []byte) error {
	if b.count >= maxFormFieldCount {
		return apperr.ErrInvalidRequest.WithMessage("multipart 表单字段过多")
	}
	remaining := maxFormFieldTotalBytes - b.totalBytes
	if len(name) > remaining || len(value) > remaining-len(name) {
		return apperr.ErrInvalidRequest.WithMessage("multipart 表单字段总量超出限制")
	}
	b.totalBytes += len(name) + len(value)
	b.count++
	return nil
}

// NewRequestReader 创建 multipart 流读取器.
func NewRequestReader(c *gin.Context) (*RequestReader, error) {
	reader, err := c.Request.MultipartReader()
	if err != nil {
		return nil, apperr.ErrInvalidRequest.WithMessage("请求不是合法的 multipart/form-data: " + err.Error())
	}
	return &RequestReader{reader: reader, fields: map[string]string{}}, nil
}

// ReadFirstField 仅在首个 part 匹配且不是文件时读取字段值; 其他 part 不读取其内容.
func (r *RequestReader) ReadFirstField(name string) (string, error) {
	if r == nil || r.reader == nil || r.firstRead || r.consumed {
		return "", apperr.ErrInvalidRequest.WithMessage("multipart 请求读取状态无效")
	}
	r.firstRead = true
	part, err := r.reader.NextPart()
	if errors.Is(err, io.EOF) {
		return "", nil
	}
	if err != nil {
		return "", apperr.ErrInvalidRequest.WithMessage("解析上传请求失败: " + err.Error())
	}
	if part.FormName() != name || part.FileName() != "" {
		return "", nil
	}
	value, err := io.ReadAll(io.LimitReader(part, maxFormFieldLength+1))
	if err != nil {
		return "", apperr.ErrInvalidRequest.WithMessage("读取表单字段失败: " + err.Error())
	}
	if len(value) > maxFormFieldLength {
		return "", apperr.ErrInvalidRequest.WithMessage("表单字段过长")
	}
	if err := r.fieldBudget.add(name, value); err != nil {
		return "", err
	}
	if err := part.Close(); err != nil {
		return "", apperr.ErrInvalidRequest.WithMessage("读取表单字段失败: " + err.Error())
	}
	field := strings.TrimSpace(string(value))
	r.fields[name] = field
	return field, nil
}

// Read 解析剩余 multipart part; 首个字段 (如有) 会保留在 Fields 中.
func (r *RequestReader) Read(tempDir string, memLimit, limit int64) (*Result, error) {
	if r == nil || r.reader == nil || r.consumed {
		return nil, apperr.ErrInvalidRequest.WithMessage("multipart 请求读取状态无效")
	}
	r.consumed = true
	result := &Result{Fields: r.fields}
	for {
		part, err := r.reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			closeResult(result)
			return nil, apperr.ErrInvalidRequest.WithMessage("解析上传请求失败: " + err.Error())
		}
		if err := handlePart(part, tempDir, memLimit, limit, result, &r.fieldBudget); err != nil {
			closeResult(result)
			return nil, err
		}
	}
	if result.Spooled == nil {
		return nil, apperr.ErrInvalidRequest.WithMessage("缺少 file 字段")
	}
	return result, nil
}

// Read 解析 multipart 请求: file 字段写入缓冲并计算哈希, 其余字段收集为字符串.
// limit 为文件读取上限 (字节), 0 表示不限.
func Read(c *gin.Context, tempDir string, memLimit int64, limit int64) (*Result, error) {
	reader, err := NewRequestReader(c)
	if err != nil {
		return nil, err
	}
	return reader.Read(tempDir, memLimit, limit)
}

// handlePart 处理单个 part.
func handlePart(part *multipart.Part, tempDir string, memLimit, limit int64, result *Result, budget *multipartFieldBudget) error {
	defer part.Close()
	name := part.FormName()
	filename := part.FileName()
	if name == "file" || (filename != "" && name != "") {
		if result.Spooled != nil {
			return apperr.ErrInvalidRequest.WithMessage("一次请求只能上传一个文件")
		}
		spooled, err := service.SpoolReader(tempDir, memLimit, part, filename, limit)
		if err != nil {
			return err
		}
		result.Spooled = spooled
		result.Filename = filename
		return nil
	}
	value, err := io.ReadAll(io.LimitReader(part, maxFormFieldLength+1))
	if err != nil {
		return apperr.ErrInvalidRequest.WithMessage("读取表单字段失败: " + err.Error())
	}
	if len(value) > maxFormFieldLength {
		return apperr.ErrInvalidRequest.WithMessage("表单字段过长")
	}
	if err := budget.add(name, value); err != nil {
		return err
	}
	if name != "" {
		result.Fields[name] = strings.TrimSpace(string(value))
	}
	return nil
}

// PolicyID 解析字段中的存储策略 ID.
func (r *Result) PolicyID(field string) (*int64, error) {
	raw := strings.TrimSpace(r.Fields[field])
	if raw == "" {
		return nil, nil
	}
	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || parsed <= 0 {
		return nil, apperr.ErrInvalidRequest.WithMessage(field + " 必须是正整数")
	}
	return &parsed, nil
}

// closeResult 释放已缓冲的内容.
func closeResult(result *Result) {
	if result.Spooled != nil && result.Spooled.Spool != nil {
		result.Spooled.Spool.Close()
	}
}

// Close 释放资源.
func (r *Result) Close() {
	if r != nil {
		closeResult(r)
	}
}
