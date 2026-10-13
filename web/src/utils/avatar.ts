import { md5Hex } from './md5';

/** Cravatar (Gravatar 兼容) 头像地址; 邮箱 trim + 小写后取 MD5, 无头像时由 d=404 触发本地回退 */
export function cravatarAvatarUrl(email: string | undefined, size = 64): string | undefined {
  const normalized = email?.trim().toLowerCase();
  if (!normalized) {
    return undefined;
  }
  return `https://cravatar.cn/avatar/${md5Hex(normalized)}?s=${size}&d=404`;
}
