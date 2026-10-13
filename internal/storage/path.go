package storage

import (
	"crypto/rand"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// PathVars 是魔法路径模板可用的变量值.
type PathVars struct {
	MD5    string
	SHA256 string
	SHA512 string
	Ext    string
}

// 路径长度限制.
const (
	maxPathSegmentLen = 128
	maxPathLen        = 512
)

// RenderPath 按模板渲染相对路径; 模板校验失败或变量缺失都返回错误.
func RenderPath(template string, vars PathVars) (string, error) {
	if err := ValidateTemplate(template); err != nil {
		return "", err
	}
	var out strings.Builder
	for i := 0; i < len(template); {
		if template[i] != '{' {
			out.WriteByte(template[i])
			i++
			continue
		}
		end := strings.IndexByte(template[i:], '}')
		if end < 0 {
			return "", fmt.Errorf("模板存在未闭合的变量: %s", template[i:])
		}
		token := template[i+1 : i+end]
		value, err := renderToken(token, vars)
		if err != nil {
			return "", err
		}
		out.WriteString(value)
		i += end + 1
	}

	rel := out.String()
	if strings.Contains(rel, "//") {
		return "", errors.New("模板渲染结果包含连续斜杠")
	}
	if strings.HasPrefix(rel, "/") || strings.HasSuffix(rel, "/") {
		return "", errors.New("模板渲染结果不能以斜杠开头或结尾")
	}
	for _, segment := range strings.Split(rel, "/") {
		if segment == "" {
			return "", errors.New("模板渲染结果包含空路径段")
		}
		if len(segment) > maxPathSegmentLen {
			return "", fmt.Errorf("路径段超过 %d 字符: %s", maxPathSegmentLen, segment)
		}
	}
	if len(rel) > maxPathLen {
		return "", fmt.Errorf("路径超过 %d 字符", maxPathLen)
	}
	return rel, nil
}

// renderToken 渲染单个变量.
func renderToken(token string, vars PathVars) (string, error) {
	name := token
	limit := 0
	hasLimit := false
	if idx := strings.IndexByte(token, ':'); idx >= 0 {
		name = token[:idx]
		raw := token[idx+1:]
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			return "", fmt.Errorf("变量截断长度非法: {%s}", token)
		}
		limit = parsed
		hasLimit = true
	}

	switch name {
	case "md5":
		return truncate(vars.MD5, limit, hasLimit, 32, token)
	case "sha256":
		return truncate(vars.SHA256, limit, hasLimit, 64, token)
	case "sha512":
		return truncate(vars.SHA512, limit, hasLimit, 128, token)
	case "ext":
		if hasLimit {
			return "", fmt.Errorf("变量不支持截断: {%s}", token)
		}
		if vars.Ext == "" {
			return "", errors.New("变量 {ext} 需要扩展名")
		}
		return vars.Ext, nil
	case "uuid":
		if hasLimit {
			return "", fmt.Errorf("变量不支持截断: {%s}", token)
		}
		return RandomUUID()
	case "rand":
		if hasLimit {
			if err := validateTruncation("rand", limit); err != nil {
				return "", err
			}
			return randomString(limit)
		}
		return randomString(8)
	default:
		return "", fmt.Errorf("未知路径变量: {%s}", token)
	}
}

// truncate 处理哈希变量的截断.
func truncate(value string, limit int, hasLimit bool, max int, token string) (string, error) {
	if value == "" {
		return "", fmt.Errorf("变量 {%s} 缺少哈希值", token)
	}
	if !hasLimit {
		return value, nil
	}
	if limit < 1 || limit > max {
		return "", fmt.Errorf("变量 {%s} 的截断长度必须在 1-%d", token, max)
	}
	if limit > len(value) {
		return "", fmt.Errorf("变量 {%s} 的截断长度超过哈希长度", token)
	}
	return value[:limit], nil
}

// validateTruncation 校验变量截断长度是否合法.
func validateTruncation(name string, limit int) error {
	max := 0
	switch name {
	case "md5":
		max = 32
	case "sha256":
		max = 64
	case "sha512":
		max = 128
	case "rand":
		max = 64
	default:
		return fmt.Errorf("变量不支持截断: {%s:N}", name)
	}
	if limit < 1 || limit > max {
		return fmt.Errorf("变量 {%s:N} 的截断长度必须在 1-%d", name, max)
	}
	return nil
}

// ValidateTemplate 校验模板: 必须含内容哈希变量, 不允许上跳与绝对路径.
func ValidateTemplate(template string) error {
	if strings.TrimSpace(template) == "" {
		return errors.New("魔法路径模板不能为空")
	}
	if strings.HasPrefix(template, "/") {
		return errors.New("魔法路径模板不能以 / 开头")
	}
	if strings.Contains(template, "//") {
		return errors.New("魔法路径模板不能包含连续斜杠")
	}
	if strings.Contains(template, "..") {
		return errors.New("魔法路径模板不能包含 ..")
	}
	if len(template) > 255 {
		return errors.New("魔法路径模板过长")
	}
	for _, segment := range strings.Split(template, "/") {
		if segment == "" {
			return errors.New("魔法路径模板不能包含空路径段")
		}
		if len(segment) > maxPathSegmentLen {
			return fmt.Errorf("路径段超过 %d 字符: %s", maxPathSegmentLen, segment)
		}
	}

	hasContentHash := false
	for i := 0; i < len(template); {
		if template[i] != '{' {
			i++
			continue
		}
		end := strings.IndexByte(template[i:], '}')
		if end < 0 {
			return fmt.Errorf("魔法路径模板存在未闭合变量: %s", template[i:])
		}
		token := template[i+1 : i+end]
		name := token
		if idx := strings.IndexByte(token, ':'); idx >= 0 {
			name = token[:idx]
			limit, err := strconv.Atoi(token[idx+1:])
			if err != nil {
				return fmt.Errorf("变量截断长度非法: {%s}", token)
			}
			if err := validateTruncation(name, limit); err != nil {
				return err
			}
		}
		switch name {
		case "md5", "sha256", "sha512":
			hasContentHash = true
		case "ext", "uuid", "rand":
		default:
			return fmt.Errorf("未知或不允许的路径变量: {%s}", token)
		}
		i += end + 1
	}
	if !hasContentHash {
		return errors.New("魔法路径模板必须包含内容哈希变量 ({md5} / {sha256} / {sha512})")
	}
	return nil
}

// ValidatePrefix 校验路径前缀: 不允许绝对路径, 空段, "." 与 ".." 段.
func ValidatePrefix(prefix string) error {
	trimmed := strings.TrimSpace(prefix)
	if trimmed == "" {
		return nil
	}
	if strings.HasPrefix(trimmed, "/") {
		return errors.New("路径前缀不能以 / 开头")
	}
	if strings.Contains(trimmed, "//") {
		return errors.New("路径前缀不能包含连续斜杠")
	}
	if len(trimmed) > maxPathLen {
		return fmt.Errorf("路径前缀超过 %d 字符", maxPathLen)
	}
	for _, segment := range strings.Split(trimmed, "/") {
		if segment == "" {
			return errors.New("路径前缀不能包含空路径段")
		}
		if segment == "." || segment == ".." {
			return fmt.Errorf("路径前缀不能包含 %q 段", segment)
		}
		if len(segment) > maxPathSegmentLen {
			return fmt.Errorf("前缀路径段超过 %d 字符: %s", maxPathSegmentLen, segment)
		}
	}
	return nil
}

// ValidateObjectKey 是驱动侧的防御性检查: 对象键不允许 "." 与 ".." 段.
func ValidateObjectKey(key string) error {
	if strings.TrimSpace(key) == "" {
		return errors.New("对象键不能为空")
	}
	for _, segment := range strings.Split(key, "/") {
		if segment == "." || segment == ".." {
			return fmt.Errorf("对象键不能包含 %q 段", segment)
		}
	}
	return nil
}

// NormalizeObjectKey 拼接并规范化对象键: 去重复斜杠与首尾斜杠.
func NormalizeObjectKey(prefix, relPath string) string {
	joined := strings.TrimSpace(prefix) + "/" + strings.TrimSpace(relPath)
	segments := make([]string, 0, 8)
	for _, segment := range strings.Split(joined, "/") {
		if segment == "" {
			continue
		}
		segments = append(segments, segment)
	}
	return strings.Join(segments, "/")
}

// JoinURL 拼接分发域名与相对路径, 规范化尾部斜杠.
func JoinURL(base, relPath string) string {
	return strings.TrimRight(strings.TrimSpace(base), "/") + "/" + strings.TrimLeft(strings.TrimSpace(relPath), "/")
}

// NormalizeURLBase 规范化分发域名: 必须是 http(s) 且去掉尾部斜杠.
func NormalizeURLBase(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", errors.New("分发域名不能为空")
	}
	if !strings.HasPrefix(value, "http://") && !strings.HasPrefix(value, "https://") {
		return "", errors.New("分发域名必须以 http:// 或 https:// 开头")
	}
	return strings.TrimRight(value, "/"), nil
}

// RandomString 生成 n 位小写字母数字随机串, 用于冲突重命名与探测对象名.
func RandomString(n int) (string, error) {
	return randomString(n)
}

// randomString 生成 n 位小写字母数字随机串.
func randomString(n int) (string, error) {
	if n < 1 || n > 64 {
		return "", errors.New("随机串长度必须在 1-64")
	}
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	buf := make([]byte, n)
	random := make([]byte, n)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	for i, b := range random {
		buf[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(buf), nil
}

// RandomUUID 生成 UUIDv4.
func RandomUUID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", buf[0:4], buf[4:6], buf[6:8], buf[8:10], buf[10:16]), nil
}
