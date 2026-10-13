package model

import (
	"fmt"
	"strings"
)

// Permission 是权限位位图, 与 docs/permissions.md 第 2 节一一对应.
type Permission uint64

const (
	PermUpload         Permission = 1 << 0 // 上传图片
	PermDeleteOwn      Permission = 1 << 1 // 删除自己的图片
	PermManageKeys     Permission = 1 << 2 // 管理自己的 API 密钥
	PermViewAll        Permission = 1 << 3 // 查看全部用户的图片记录
	PermDeleteAll      Permission = 1 << 4 // 删除任意图片记录
	PermManagePolicies Permission = 1 << 5 // 管理存储策略与分发域名
	PermManageUsers    Permission = 1 << 6 // 管理用户
	PermManageGroups   Permission = 1 << 7 // 管理用户组
	PermManageSettings Permission = 1 << 8 // 管理系统设置
	PermViewLogs       Permission = 1 << 9 // 查看审计日志
)

// PermissionDef 描述单个权限位.
type PermissionDef struct {
	Bit   Permission
	Name  string
	Label string
}

// PermissionDefs 按位顺序列出全部权限位.
var PermissionDefs = []PermissionDef{
	{PermUpload, "upload", "上传图片"},
	{PermDeleteOwn, "delete_own", "删除自己的图片"},
	{PermManageKeys, "manage_keys", "管理自己的 API 密钥"},
	{PermViewAll, "view_all", "查看全部图片记录"},
	{PermDeleteAll, "delete_all", "删除任意图片记录"},
	{PermManagePolicies, "manage_policies", "管理存储策略"},
	{PermManageUsers, "manage_users", "管理用户"},
	{PermManageGroups, "manage_groups", "管理用户组"},
	{PermManageSettings, "manage_settings", "管理系统设置"},
	{PermViewLogs, "view_logs", "查看审计日志"},
}

// Has 判断是否包含指定权限位.
func (p Permission) Has(bit Permission) bool {
	return p&bit == bit
}

// Names 返回位图中包含的权限位名称.
func (p Permission) Names() []string {
	names := make([]string, 0, len(PermissionDefs))
	for _, def := range PermissionDefs {
		if p.Has(def.Bit) {
			names = append(names, def.Name)
		}
	}
	return names
}

// AllPermissions 是全部权限位的并集.
func AllPermissions() Permission {
	var all Permission
	for _, def := range PermissionDefs {
		all |= def.Bit
	}
	return all
}

// PermissionsFromNames 把权限位名称解析为位图, 未知名报错.
func PermissionsFromNames(names []string) (Permission, error) {
	var bits Permission
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		found := false
		for _, def := range PermissionDefs {
			if def.Name == name {
				bits |= def.Bit
				found = true
				break
			}
		}
		if !found {
			return 0, fmt.Errorf("未知权限位: %s", name)
		}
	}
	return bits, nil
}

// PermissionNameOf 返回单个权限位名称, 未知名返回空串.
func PermissionNameOf(bit Permission) string {
	for _, def := range PermissionDefs {
		if def.Bit == bit {
			return def.Name
		}
	}
	return ""
}
