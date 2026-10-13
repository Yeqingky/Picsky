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

// maxFormFieldLength 限制单字段长度, 防止异常客户端占用内存.
const maxFormFieldLength = 4096

// Result 是一次 multipart 解析结果.
type Result struct {
	Spooled  *service.SpooledUpload
	Filename string
	Fields   map[string]string
}

// Read 解析 multipart 请求: file 字段写入缓冲并计算哈希, 其余字段收集为字符串.
// limit 为文件读取上限 (字节), 0 表示不限.
func Read(c *gin.Context, tempDir string, memLimit int64, limit int64) (*Result, error) {
	reader, err := c.Request.MultipartReader()
	if err != nil {
		return nil, apperr.ErrInvalidRequest.WithMessage("请求不是合法的 multipart/form-data: " + err.Error())
	}

	result := &Result{Fields: map[string]string{}}
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			closeResult(result)
			return nil, apperr.ErrInvalidRequest.WithMessage("解析上传请求失败: " + err.Error())
		}
		if err := handlePart(part, tempDir, memLimit, limit, result); err != nil {
			closeResult(result)
			return nil, err
		}
	}

	if result.Spooled == nil {
		return nil, apperr.ErrInvalidRequest.WithMessage("缺少 file 字段")
	}
	return result, nil
}

// handlePart 处理单个 part.
func handlePart(part *multipart.Part, tempDir string, memLimit, limit int64, result *Result) error {
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
	value, err := io.ReadAll(io.LimitReader(part, maxFormFieldLength))
	if err != nil {
		return apperr.ErrInvalidRequest.WithMessage("读取表单字段失败: " + err.Error())
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
