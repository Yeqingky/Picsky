import type { AnchorHTMLAttributes, ReactNode } from 'react';
import { cn } from '../lib/utils';
import { useReferrerPolicy } from '../hooks/useSite';

/**
 * 外部链接: 固定新窗口打开; 未命中 Referrer 白名单时禁止发送 Referer.
 * 全局 HTML meta 已设置 no-referrer, 这里再做链接级兜底.
 */
export function ExternalLink({
  className,
  children,
  ...props
}: AnchorHTMLAttributes<HTMLAnchorElement> & { children?: ReactNode }) {
  const policy = useReferrerPolicy(props.href);
  const allowed = policy !== 'no-referrer';
  return (
    <a
      {...props}
      target="_blank"
      rel={allowed ? 'noopener' : 'noreferrer'}
      referrerPolicy={policy}
      className={cn('text-primary hover:underline', className)}
    >
      {children}
    </a>
  );
}
