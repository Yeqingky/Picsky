// Package storage 定义存储适配器接口, 能力声明与驱动注册表.
// 上层只依赖本包接口, 按能力降级, 不假设后端支持同一组操作.
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// 错误定义.
var (
	// ErrNotFound 表示对象不存在.
	ErrNotFound = errors.New("storage: 对象不存在")
	// ErrNotSupported 表示后端未声明对应能力.
	ErrNotSupported = errors.New("storage: 后端不支持该能力")
)

// Capability 是后端能力位.
type Capability uint16

// 能力集合, 与 docs/storage-policy.md 第 3.1 节一致.
const (
	CapUpload    Capability = 1 << iota // 上传对象
	CapDelete                           // 删除对象
	CapOverwrite                        // 覆盖同键对象
	CapStat                             // 查询对象是否存在与大小
	CapList                             // 递归列举目录
	CapRead                             // 读取对象内容
	CapMkdir                            // 自动创建父目录
	CapPresign                          // 生成预签名 URL
	CapMultipart                        // 分片上传
)

// capabilityNames 按位序保存能力名称.
var capabilityNames = []struct {
	bit  Capability
	name string
}{
	{CapUpload, "upload"},
	{CapDelete, "delete"},
	{CapOverwrite, "overwrite"},
	{CapStat, "stat"},
	{CapList, "list"},
	{CapRead, "read"},
	{CapMkdir, "mkdir"},
	{CapPresign, "presign"},
	{CapMultipart, "multipart"},
}

// Has 判断是否包含给定能力.
func (c Capability) Has(other Capability) bool {
	return c&other == other
}

// Names 返回能力名称列表.
func (c Capability) Names() []string {
	names := make([]string, 0, len(capabilityNames))
	for _, entry := range capabilityNames {
		if entry.bit.Has(entry.bit) && c&entry.bit != 0 {
			names = append(names, entry.name)
		}
	}
	return names
}

// CapabilityFromName 解析能力名称.
func CapabilityFromName(name string) (Capability, error) {
	for _, entry := range capabilityNames {
		if entry.name == name {
			return entry.bit, nil
		}
	}
	return 0, fmt.Errorf("未知能力: %s", name)
}

// ObjectInfo 描述一个后端对象.
type ObjectInfo struct {
	Key     string
	Size    int64
	ModTime time.Time
	IsDir   bool
	// URL 是后端为对象分配的直链; 为空时由上层按分发域名拼接.
	URL string
}

// Options 是驱动实例化参数: 敏感配置已由上层解密.
type Options struct {
	Config  map[string]string
	Secrets map[string]string
}

// FieldOption 是枚举配置字段的可选值.
type FieldOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Field 描述一个配置字段, 供管理后台渲染表单.
// Type 为空时按文本输入处理; 当前支持 text (默认) 与 radio.
type Field struct {
	Name        string        `json:"name"`
	Label       string        `json:"label"`
	Required    bool          `json:"required"`
	Secret      bool          `json:"secret"`
	Placeholder string        `json:"placeholder"`
	Type        string        `json:"type,omitempty"`
	Options     []FieldOption `json:"options,omitempty"`
}

// Spec 是驱动静态声明: 类型, 能力与配置字段.
type Spec struct {
	Type         string   `json:"type"`
	Label        string   `json:"label"`
	Capabilities []string `json:"capabilities"`
	ConfigFields []Field  `json:"config_fields"`
	SecretFields []Field  `json:"secret_fields"`
	// URLFromBackend 表示驱动在后端分配最终直链, 策略不配置分发域名,
	// 上传后以驱动返回的 URL 与路径作为记录直链与相对路径.
	URLFromBackend bool `json:"url_from_backend"`
	// DefaultPathTemplate 是驱动建议的默认魔法路径; 为空时使用系统设置.
	DefaultPathTemplate string `json:"default_path_template,omitempty"`
	// MaxFileSize 是驱动可接受的单对象最大字节数; 0 表示不额外限制.
	MaxFileSize int64 `json:"max_file_size_bytes"`
	// Formats 是驱动支持的图片格式 (imgdetect 格式名); 空表示不额外限制.
	Formats []string `json:"formats"`
	caps    Capability
}

// Has 判断驱动是否声明某能力.
func (s Spec) Has(cap Capability) bool { return s.caps.Has(cap) }

// PutResult 是写入结果.
// URL 非空表示后端分配了最终直链, 上层用它作为记录 URL;
// ObjectKey 非空时直接作为实际对象路径, 否则从 URL 路径推导.
// URL 为空时上层按策略分发域名与渲染路径拼接.
type PutResult struct {
	URL       string
	ObjectKey string
}

// Adapter 是存储后端驱动接口.
type Adapter interface {
	// Type 返回驱动类型名.
	Type() string
	// Capabilities 返回能力位.
	Capabilities() Capability
	// Put 写入对象; 需要 CapMkdir 时驱动自行创建父目录.
	Put(ctx context.Context, key string, body io.Reader, size int64, contentType string) (PutResult, error)
	// Stat 查询对象; 需要 CapStat, 对象不存在返回 ErrNotFound.
	Stat(ctx context.Context, key string) (ObjectInfo, error)
	// Delete 删除对象; 需要 CapDelete, 对象不存在视为成功.
	Delete(ctx context.Context, key string) error
	// List 递归列举前缀下的对象; 需要 CapList.
	List(ctx context.Context, prefix string) ([]ObjectInfo, error)
	// Read 读取对象内容; 需要 CapRead.
	Read(ctx context.Context, key string) (io.ReadCloser, error)
	// Test 写入一个探测对象, 用于连接测试; 声明 CapDelete 的驱动应自行清理.
	Test(ctx context.Context, prefix string) error
}

// ScanChecker 是可选接口, 用于在创建扫描任务前校验后端凭据与访问权限.
// 预检失败时上层拒绝入队, 不创建维护任务.
type ScanChecker interface {
	CheckScan(ctx context.Context) error
}

// Factory 由驱动实现, 用于实例化 Adapter.
type Factory func(opts Options) (Adapter, error)

var registry = map[string]struct {
	spec    Spec
	factory Factory
}{}

// Register 注册驱动; 类型名重复会 panic, 属编程错误.
func Register(spec Spec, factory Factory) {
	if spec.Type == "" {
		panic("storage: 驱动类型名不能为空")
	}
	if _, exists := registry[spec.Type]; exists {
		panic("storage: 驱动重复注册: " + spec.Type)
	}
	caps, err := CapabilitiesFromNames(spec.Capabilities)
	if err != nil {
		panic("storage: 驱动能力名非法: " + err.Error())
	}
	spec.caps = caps
	for i := range spec.ConfigFields {
		spec.ConfigFields[i].Secret = false
	}
	for i := range spec.SecretFields {
		spec.SecretFields[i].Secret = true
	}
	registry[spec.Type] = struct {
		spec    Spec
		factory Factory
	}{spec: spec, factory: factory}
}

// CapabilitiesFromNames 把能力名列表解析为能力位.
func CapabilitiesFromNames(names []string) (Capability, error) {
	var caps Capability
	for _, name := range names {
		bit, err := CapabilityFromName(strings.TrimSpace(name))
		if err != nil {
			return 0, err
		}
		caps |= bit
	}
	return caps, nil
}

// Specs 返回全部已注册驱动的声明, 按类型名排序.
func Specs() []Spec {
	specs := make([]Spec, 0, len(registry))
	for _, entry := range registry {
		specs = append(specs, entry.spec)
	}
	sort.Slice(specs, func(i, j int) bool { return specs[i].Type < specs[j].Type })
	return specs
}

// SpecOf 返回指定类型的声明.
func SpecOf(typeName string) (Spec, bool) {
	entry, ok := registry[typeName]
	return entry.spec, ok
}

// New 实例化驱动.
func New(typeName string, opts Options) (Adapter, error) {
	entry, ok := registry[typeName]
	if !ok {
		return nil, fmt.Errorf("未知存储后端类型: %s", typeName)
	}
	if opts.Config == nil {
		opts.Config = map[string]string{}
	}
	if opts.Secrets == nil {
		opts.Secrets = map[string]string{}
	}
	adapter, err := entry.factory(opts)
	if err != nil {
		return nil, err
	}
	return adapter, nil
}
