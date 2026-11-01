/** Referrer 白名单: 命中域名后缀时恢复浏览器默认 Referer, 否则保持 no-referrer. */

export type ReferrerPolicyValue = 'no-referrer' | 'strict-origin-when-cross-origin';

/** 规范化白名单条目: 去空白, 小写, 去掉前导 *. 或 . 与结尾的点. */
export function normalizeReferrerDomain(value: string): string {
  return value.trim().toLowerCase().replace(/^\*\./, '').replace(/^\./, '').replace(/\.$/, '');
}

/** 域名后缀匹配: a.com 命中 a.com 与 x.y.a.com, 不命中 nota.com. */
export function isReferrerAllowed(rawURL: string | null | undefined, allowlist: readonly string[]) {
  if (!rawURL || allowlist.length === 0) return false;
  let host: string;
  try {
    host = new URL(rawURL, window.location.origin).hostname.toLowerCase().replace(/\.$/, '');
  } catch {
    return false;
  }
  if (host === '') return false;
  return allowlist.some((entry) => {
    const domain = normalizeReferrerDomain(entry);
    return domain !== '' && (host === domain || host.endsWith('.' + domain));
  });
}

export function referrerPolicyFor(
  rawURL: string | null | undefined,
  allowlist: readonly string[],
): ReferrerPolicyValue {
  return isReferrerAllowed(rawURL, allowlist) ? 'strict-origin-when-cross-origin' : 'no-referrer';
}
