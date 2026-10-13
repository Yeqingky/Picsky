import { useEffect, useMemo, useState } from 'react';
import { Loader2, RotateCw } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { useThemeMode } from '../theme/ThemeModeProvider';
import { placeholderColor } from '../utils/image';
import { cn } from '../lib/utils';
import { Button } from './ui/button';
import { Tooltip, TooltipContent, TooltipTrigger } from './ui/tooltip';

export interface LazyImageProps {
  /** 图片直链 (原图) */
  src: string;
  alt?: string;
  /** 占位色来源: sha256 前若干位映射固定色板 */
  hash?: string | null;
  objectFit?: 'cover' | 'contain';
  /** 大图预览需要立即加载 */
  eager?: boolean;
  /** 失败时显示重试按钮 */
  retryable?: boolean;
  onClick?: () => void;
  className?: string;
  /** 图片自身的圆角等样式 */
  imageClassName?: string;
}

/**
 * 统一图片组件 (docs/webui.md 4.2): 占位色 + onload 后淡入 + 失败重试。
 * 不做模糊到清晰, 不代理图片内容, 浏览器直连分发域名。
 */
export function LazyImage({
  src,
  alt = '',
  hash,
  objectFit = 'cover',
  eager = false,
  retryable = true,
  onClick,
  className,
  imageClassName,
}: LazyImageProps) {
  const { t } = useTranslation();
  const { resolvedMode } = useThemeMode();
  const [attempt, setAttempt] = useState(0);
  const [status, setStatus] = useState<'loading' | 'loaded' | 'error'>('loading');

  const resolvedSrc = useMemo(
    () => (attempt === 0 ? src : `${src}#retry-${attempt}`),
    [src, attempt],
  );

  useEffect(() => {
    setStatus('loading');
  }, [resolvedSrc]);

  const retry = () => {
    setAttempt((value) => value + 1);
  };

  return (
    <div
      className={cn('relative h-full w-full overflow-hidden', className)}
      style={{ backgroundColor: placeholderColor(hash, resolvedMode) }}
    >
      {status === 'loading' && (
        <div className="absolute inset-0 flex items-center justify-center">
          <Loader2 className="size-[22px] animate-spin opacity-35" />
        </div>
      )}

      {status === 'error' && retryable && (
        <div className="absolute inset-0 flex items-center justify-center">
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                aria-label={t('common.action.retry')}
                onClick={(event) => {
                  event.stopPropagation();
                  retry();
                }}
              >
                <RotateCw className="size-4" />
              </Button>
            </TooltipTrigger>
            <TooltipContent>{t('common.action.retry')}</TooltipContent>
          </Tooltip>
        </div>
      )}

      {status !== 'error' && (
        <img
          src={resolvedSrc}
          alt={alt}
          loading={eager ? 'eager' : 'lazy'}
          decoding="async"
          onClick={onClick}
          onLoad={() => setStatus('loaded')}
          onError={() => setStatus('error')}
          className={cn(
            'block h-full w-full transition-opacity duration-200',
            objectFit === 'contain' ? 'object-contain' : 'object-cover',
            status === 'loaded' ? 'opacity-100' : 'opacity-0',
            onClick && 'cursor-zoom-in',
            imageClassName,
          )}
        />
      )}
    </div>
  );
}
