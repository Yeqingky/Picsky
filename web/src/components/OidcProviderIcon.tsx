import { useState } from 'react';
import { LogIn } from 'lucide-react';
import { useReferrerPolicy } from '../hooks/useSite';
import { cn } from '../lib/utils';

/** 第三方登录图标; 缺失或加载失败时回退到通用图标. */
export function OidcProviderIcon({ url, className }: { url: string; className?: string }) {
  const referrerPolicy = useReferrerPolicy(url);
  const [failedUrl, setFailedUrl] = useState('');

  if (url === '' || failedUrl === url) {
    return <LogIn className={cn('text-muted-foreground size-4 shrink-0', className)} />;
  }
  return (
    <img
      src={url}
      alt=""
      referrerPolicy={referrerPolicy}
      className={cn('size-4 shrink-0 rounded-sm object-contain', className)}
      onError={() => setFailedUrl(url)}
    />
  );
}
