import { PERMISSION_NAMES } from '../api/types';
import type { PermissionName } from '../api/types';

/**
 * 权限位名称 (docs/permissions.md 第 2 节) 与 i18n key 的映射。
 * i18n key 统一落在 `permissions.<name>`。
 */
export const PERMISSION_BITS: Record<PermissionName, number> = {
  upload: 0,
  delete_own: 1,
  manage_keys: 2,
  view_all: 3,
  delete_all: 4,
  manage_policies: 5,
  manage_users: 6,
  manage_groups: 7,
  manage_settings: 8,
  view_logs: 9,
};

export { PERMISSION_NAMES };

export function hasPermission(
  permissions: readonly string[] | undefined,
  name: PermissionName,
): boolean {
  return permissions?.includes(name) ?? false;
}

export function hasAnyPermission(
  permissions: readonly string[] | undefined,
  names: readonly PermissionName[],
): boolean {
  return names.some((name) => hasPermission(permissions, name));
}

/** 后台是否可见 (任一管理权限位; 与后端 Principal.IsAdmin 的位集合一致) */
export const ADMIN_PERMISSIONS: PermissionName[] = [
  'manage_policies',
  'manage_users',
  'manage_groups',
  'manage_settings',
  'view_logs',
  'view_all',
  'delete_all',
];

export function isAdminVisible(permissions: readonly string[] | undefined): boolean {
  return hasAnyPermission(permissions, ADMIN_PERMISSIONS);
}

/** 位图 -> 名称数组 (用于用户组编辑权限位展示) */
export function bitsToNames(bits: number | undefined | null): PermissionName[] {
  if (!bits) return [];
  return PERMISSION_NAMES.filter((name) => (bits & (1 << PERMISSION_BITS[name])) !== 0);
}

export function namesToBits(names: readonly PermissionName[]): number {
  return names.reduce((acc, name) => acc | (1 << PERMISSION_BITS[name]), 0);
}
