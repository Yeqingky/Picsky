import { useEffect, useState } from 'react';
import {
  CircleAlert,
  CircleCheck,
  CircleSlash,
  Copy,
  FileImage,
  LoaderCircle,
  RotateCw,
  X,
} from 'lucide-react';
import { useTranslation } from 'react-i18next';
import type { UploadItem } from './types';
import { uploadItemUrl } from './types';
import { Button } from '../ui/button';
import { Dialog, DialogContent, DialogTitle } from '../ui/dialog';
import { Tooltip, TooltipContent, TooltipTrigger } from '../ui/tooltip';
import { formatBytes } from '../../utils/format';
import { cn } from '../../lib/utils';

/** 本地文件预览 URL, 组件卸载或文件替换时释放 */
function useObjectURL(file: File): string | null {
  const [url, setUrl] = useState<string | null>(null);

  useEffect(() => {
    const next = URL.createObjectURL(file);
    setUrl(next);
    return () => URL.revokeObjectURL(next);
  }, [file]);

  return url;
}

function StatusCell({ item }: { item: UploadItem }) {
  const { t } = useTranslation();

  switch (item.status) {
    case 'done':
    case 'duplicate':
      return (
        <span className="text-success flex items-center gap-1 text-xs whitespace-nowrap">
          <CircleCheck className="size-3.5" />
          {t(item.status === 'done' ? 'upload.queue.statusDone' : 'upload.queue.statusDuplicate')}
        </span>
      );
    case 'uploading':
      return (
        <div className="flex flex-col items-end gap-1">
          <span className="text-info text-xs tabular-nums">{Math.round(item.progress)}%</span>
          <div className="bg-muted h-1 w-20 overflow-hidden rounded-full">
            <div
              className="bg-info h-full rounded-full transition-[width]"
              style={{ width: `${item.progress}%` }}
            />
          </div>
        </div>
      );
    case 'compressing':
      return (
        <span className="text-info flex items-center gap-1 text-xs whitespace-nowrap">
          <LoaderCircle className="size-3.5 animate-spin" />
          {t('upload.queue.statusCompressing')}
        </span>
      );
    case 'hashing':
      return (
        <span className="text-info flex items-center gap-1 text-xs whitespace-nowrap">
          <LoaderCircle className="size-3.5 animate-spin" />
          {t('upload.queue.statusHashing')}
        </span>
      );
    case 'skipped':
      return (
        <span
          className="text-warning flex items-center gap-1 text-xs whitespace-nowrap"
          title={item.compressionError}
        >
          <CircleSlash className="size-3.5" />
          {t('upload.queue.statusSkipped')}
        </span>
      );
    case 'failed':
      return (
        <span className="text-destructive flex items-center gap-1 text-xs whitespace-nowrap">
          <CircleAlert className="size-3.5" />
          {t('upload.queue.statusFailed')}
        </span>
      );
    default:
      return (
        <span className="text-muted-foreground text-xs whitespace-nowrap">
          {t('upload.queue.statusPending')}
        </span>
      );
  }
}

/** 上传列表行: 文件信息 + 状态 + 复制/重试操作 (docs/webui.md 第 6 节) */
export function UploadFileRow({
  item,
  onCopy,
  onRetry,
  onRemove,
  className,
}: {
  item: UploadItem;
  onCopy: (item: UploadItem) => void;
  onRetry: (id: string) => void;
  onRemove: (id: string) => void;
  className?: string;
}) {
  const { t } = useTranslation();
  const url = uploadItemUrl(item);
  const preview = useObjectURL(item.file);
  const [previewOpen, setPreviewOpen] = useState(false);

  return (
    <div className={cn('flex items-center gap-3 px-3 py-2.5', className)}>
      <button
        type="button"
        aria-label={t('upload.preview')}
        onClick={() => setPreviewOpen(true)}
        className="border-border bg-muted focus-visible:ring-ring size-9 shrink-0 overflow-hidden rounded-lg border transition-opacity hover:opacity-80 focus-visible:ring-2 focus-visible:outline-none"
      >
        {preview === null ? (
          <span className="text-muted-foreground flex size-full items-center justify-center">
            <FileImage className="size-4" />
          </span>
        ) : (
          <img src={preview} alt={item.name} className="size-full object-cover" draggable={false} />
        )}
      </button>
      <div className="min-w-0 flex-1">
        <p className="truncate text-sm font-medium" title={item.name}>
          {item.name}
        </p>
        <p className="text-muted-foreground text-xs">
          {formatBytes(item.size)}
          {item.compressedSize !== undefined && (
            <>
              {' -> '}
              <span
                className={item.compressedSize > item.size ? 'line-through' : undefined}
                title={
                  item.compressedSize > item.size && item.uploadFile === undefined
                    ? t('upload.compression.largerKeptOriginal')
                    : undefined
                }
              >
                {formatBytes(item.compressedSize)}
              </span>
            </>
          )}
        </p>
      </div>
      <div className="flex w-24 shrink-0 justify-end">
        <StatusCell item={item} />
      </div>
      <div className="flex shrink-0 items-center gap-1">
        {url !== null && (
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                variant="ghost"
                size="icon-sm"
                aria-label={t('upload.copyOne')}
                onClick={() => onCopy(item)}
              >
                <Copy className="size-4" />
              </Button>
            </TooltipTrigger>
            <TooltipContent>{t('upload.copyOne')}</TooltipContent>
          </Tooltip>
        )}
        {item.status === 'failed' && (
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                variant="ghost"
                size="icon-sm"
                aria-label={t('upload.queue.retry')}
                onClick={() => onRetry(item.id)}
              >
                <RotateCw className="size-4" />
              </Button>
            </TooltipTrigger>
            <TooltipContent>{item.error ?? t('upload.queue.retry')}</TooltipContent>
          </Tooltip>
        )}
        <Tooltip>
          <TooltipTrigger asChild>
            <Button
              variant="ghost"
              size="icon-sm"
              className="text-muted-foreground hover:text-foreground"
              aria-label={t('upload.remove')}
              disabled={
                item.status === 'compressing' ||
                item.status === 'hashing' ||
                item.status === 'uploading'
              }
              onClick={() => onRemove(item.id)}
            >
              <X className="size-4" />
            </Button>
          </TooltipTrigger>
          <TooltipContent>{t('upload.remove')}</TooltipContent>
        </Tooltip>
      </div>

      <Dialog open={previewOpen} onOpenChange={setPreviewOpen}>
        <DialogContent className="w-fit max-w-[92vw] p-2 sm:max-w-[92vw]">
          <DialogTitle className="sr-only">{item.name}</DialogTitle>
          {preview !== null && (
            <img
              src={preview}
              alt={item.name}
              className="mx-auto max-h-[80vh] w-auto max-w-full rounded-md object-contain"
            />
          )}
        </DialogContent>
      </Dialog>
    </div>
  );
}
