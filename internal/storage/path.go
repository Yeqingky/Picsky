package storage

import (
	"crypto/rand"
	"errors"
	"fmt"
	"net/url"
	"regexp"
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

// pathPart 是解析后的模板片段; 序号片段在每次渲染时替换, 其他变量只生成一次.
type pathPart struct {
	literal  string
	sequence bool
}

// PathRenderer 解析一次路径模板, 并在多次渲染时保持随机变量不变.
type PathRenderer struct {
	parts          []pathPart
	basePath       string
	hasSequence    bool
	sequencePrefix string
	sequenceSuffix string
}

// NewPathRenderer 校验并解析模板; 除 {seq} 外的变量只渲染一次.
func NewPathRenderer(template string, vars PathVars) (*PathRenderer, error) {
	if err := ValidateTemplate(template); err != nil {
		return nil, err
	}
	renderer := &PathRenderer{}
	var base strings.Builder
	for i := 0; i < len(template); {
		if template[i] != '{' {
			start := i
			for i < len(template) && template[i] != '{' {
				i++
			}
			literal := template[start:i]
			renderer.parts = append(renderer.parts, pathPart{literal: literal})
			base.WriteString(literal)
			continue
		}
		end := strings.IndexByte(template[i:], '}')
		if end < 0 {
			return nil, fmt.Errorf("模板存在未闭合的变量: %s", template[i:])
		}
		token := template[i+1 : i+end]
		if token == "seq" {
			renderer.parts = append(renderer.parts, pathPart{sequence: true})
			renderer.hasSequence = true
			i += end + 1
			continue
		}
		value, err := renderToken(token, vars)
		if err != nil {
			return nil, err
		}
		renderer.parts = append(renderer.parts, pathPart{literal: value})
		base.WriteString(value)
		i += end + 1
	}
	renderer.basePath = base.String()

	if renderer.hasSequence {
		afterSequence := false
		for _, part := range renderer.parts {
			if part.sequence {
				afterSequence = true
				continue
			}
			if afterSequence {
				renderer.sequenceSuffix += part.literal
			} else {
				renderer.sequencePrefix += part.literal
			}
		}
	} else {
		renderer.sequencePrefix, renderer.sequenceSuffix = sequencePathPattern(renderer.basePath)
	}
	return renderer, nil
}

// HasSequence reports whether the template contains {seq}.
func (r *PathRenderer) HasSequence() bool {
	return r != nil && r.hasSequence
}

// SequencePattern 返回序号所在路径的固定前缀与后缀.
// 未显式使用 {seq} 时, 对应重命名策略追加到文件名末尾的 -N.
func (r *PathRenderer) SequencePattern() (string, string) {
	if r == nil {
		return "", ""
	}
	return r.sequencePrefix, r.sequenceSuffix
}

// Render 渲染路径. 显式 {seq} 从 1 开始; 无 {seq} 时 0 返回原路径, 正数生成 -N 后缀.
func (r *PathRenderer) Render(sequence uint64) (string, error) {
	if r == nil {
		return "", errors.New("路径渲染器不能为空")
	}
	if r.hasSequence && sequence == 0 {
		return "", errors.New("变量 {seq} 必须大于 0")
	}
	if !r.hasSequence {
		if sequence == 0 {
			return validateRenderedPath(r.basePath)
		}
		return validateRenderedPath(appendSequenceSuffix(r.basePath, sequence))
	}
	var out strings.Builder
	for _, part := range r.parts {
		if part.sequence {
			out.WriteString(strconv.FormatUint(sequence, 10))
		} else {
			out.WriteString(part.literal)
		}
	}
	return validateRenderedPath(out.String())
}

// RenderPath 按模板渲染相对路径; 模板校验失败或变量缺失都返回错误.
func RenderPath(template string, vars PathVars) (string, error) {
	renderer, err := NewPathRenderer(template, vars)
	if err != nil {
		return "", err
	}
	sequence := uint64(0)
	if renderer.HasSequence() {
		sequence = 1
	}
	return renderer.Render(sequence)
}

// SequenceMatcher 按魔法路径模板从渲染结果中反向提取 {seq} 值.
type SequenceMatcher struct {
	pattern *regexp.Regexp
}

// NewSequenceMatcher 编译提取表达式; 模板不含 {seq} 时返回 nil, nil.
func NewSequenceMatcher(template string) (*SequenceMatcher, error) {
	if !strings.Contains(template, "{seq}") {
		return nil, nil
	}
	if err := ValidateTemplate(template); err != nil {
		return nil, err
	}
	var pattern strings.Builder
	pattern.WriteString("^")
	sequenceCount := 0
	for i := 0; i < len(template); {
		if template[i] != '{' {
			start := i
			for i < len(template) && template[i] != '{' {
				i++
			}
			pattern.WriteString(regexp.QuoteMeta(template[start:i]))
			continue
		}
		end := strings.IndexByte(template[i:], '}')
		if end < 0 {
			return nil, fmt.Errorf("模板存在未闭合的变量: %s", template[i:])
		}
		token := template[i+1 : i+end]
		if token == "seq" {
			sequenceCount++
			pattern.WriteString("([0-9]+)")
		} else {
			pattern.WriteString("[^/]+?")
		}
		i += end + 1
	}
	if sequenceCount != 1 {
		return nil, errors.New("魔法路径模板必须恰好包含一个 {seq}")
	}
	pattern.WriteString("$")
	compiled, err := regexp.Compile(pattern.String())
	if err != nil {
		return nil, fmt.Errorf("序号提取表达式非法: %w", err)
	}
	return &SequenceMatcher{pattern: compiled}, nil
}

// Extract 从渲染后的相对路径中提取序号; 不匹配或序号为 0 时返回 false.
func (m *SequenceMatcher) Extract(relPath string) (uint64, bool) {
	if m == nil || m.pattern == nil {
		return 0, false
	}
	matches := m.pattern.FindStringSubmatch(relPath)
	if len(matches) != 2 {
		return 0, false
	}
	if len(matches[1]) > 1 && matches[1][0] == '0' {
		return 0, false
	}
	value, err := strconv.ParseUint(matches[1], 10, 64)
	if err != nil || value == 0 {
		return 0, false
	}
	return value, true
}

// ExtractSequence 按模板从相对路径中提取序号, 用于删除后释放序号.
func ExtractSequence(template, relPath string) (uint64, bool) {
	matcher, err := NewSequenceMatcher(template)
	if err != nil {
		return 0, false
	}
	return matcher.Extract(relPath)
}

func validateRenderedPath(rel string) (string, error) {
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

func sequencePathPattern(rel string) (string, string) {
	dir := ""
	name := rel
	if slash := strings.LastIndexByte(rel, '/'); slash >= 0 {
		dir = rel[:slash+1]
		name = rel[slash+1:]
	}
	if dot := strings.LastIndexByte(name, '.'); dot > 0 {
		return dir + name[:dot] + "-", name[dot:]
	}
	return rel + "-", ""
}

func appendSequenceSuffix(rel string, sequence uint64) string {
	dir := ""
	name := rel
	if slash := strings.LastIndexByte(rel, '/'); slash >= 0 {
		dir = rel[:slash+1]
		name = rel[slash+1:]
	}
	if dot := strings.LastIndexByte(name, '.'); dot > 0 {
		return dir + name[:dot] + "-" + strconv.FormatUint(sequence, 10) + name[dot:]
	}
	return rel + "-" + strconv.FormatUint(sequence, 10)
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

// ValidateTemplate 校验模板: 必须含内容哈希变量或 {seq}, 不允许上跳与绝对路径.
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
	sequenceCount := 0
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
		case "seq":
			sequenceCount++
		case "ext", "uuid", "rand":
		default:
			return fmt.Errorf("未知或不允许的路径变量: {%s}", token)
		}
		i += end + 1
	}
	if sequenceCount > 1 {
		return errors.New("魔法路径模板最多包含一个 {seq}")
	}
	if !hasContentHash && sequenceCount == 0 {
		return errors.New("魔法路径模板必须包含内容哈希变量或 {seq}")
	}
	if !strings.HasSuffix(template, ".{ext}") {
		return errors.New("魔法路径模板必须以 .{ext} 结尾")
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

// NormalizePrefix 规范化目录前缀: 去首尾斜杠并校验路径段; 空值表示根目录.
func NormalizePrefix(raw string) (string, error) {
	prefix := strings.Trim(strings.TrimSpace(raw), "/")
	if err := ValidatePrefix(prefix); err != nil {
		return "", err
	}
	return prefix, nil
}

// WithPrefix 给对象键加上目录前缀; 已带相同前缀时不重复添加.
func WithPrefix(prefix, key string) string {
	prefix = strings.Trim(prefix, "/")
	key = NormalizeObjectKey("", key)
	if prefix == "" || key == "" || key == prefix || strings.HasPrefix(key, prefix+"/") {
		return key
	}
	return prefix + "/" + key
}

// JoinURL 拼接分发域名与相对路径, 规范化尾部斜杠.
func JoinURL(base, relPath string) string {
	return strings.TrimRight(strings.TrimSpace(base), "/") + "/" + strings.TrimLeft(strings.TrimSpace(relPath), "/")
}

// ObjectPathFromURL 从后端返回的绝对直链中提取对象路径 (不含首尾斜杠).
func ObjectPathFromURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("直链格式非法: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("直链必须以 http:// 或 https:// 开头")
	}
	if parsed.Host == "" {
		return "", errors.New("直链缺少主机名")
	}
	escaped := parsed.EscapedPath()
	value, err := url.PathUnescape(escaped)
	if err != nil {
		return "", fmt.Errorf("直链路径转义非法: %w", err)
	}
	objectKey := NormalizeObjectKey("", value)
	if objectKey == "" {
		return "", errors.New("直链缺少路径")
	}
	for _, segment := range strings.Split(objectKey, "/") {
		if segment == "." || segment == ".." {
			return "", errors.New("直链路径不允许包含 . 或 .. 段")
		}
	}
	if len(objectKey) > maxPathLen {
		return "", fmt.Errorf("直链路径过长: %d 字符", len(objectKey))
	}
	return objectKey, nil
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

// RandomString 生成 n 位小写字母数字随机串, 用于路径随机变量与探测对象名.
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
