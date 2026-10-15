import { Ban, CircleAlert, CircleCheck, LoaderCircle, RotateCw, Trash2 } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import type { UploadItem } from './types';
import { Badge } from '../ui/badge';
import { Button } from '../ui/button';
import { Progress } from '../ui/progress';
import { Skeleton } from '../ui/skeleton';
import { Tooltip, TooltipContent, TooltipTrigger } from '../ui/tooltip';
import { formatBytes } from '../../utils/format';

const STATUS_LABEL_KEY: Record<UploadItem['status'], string> = {
  pending: 'upload.queue.statusPending',
  hashing: 'upload.queue.statusHashing',
  uploading: 'upload.queue.statusUploading',
  done: 'upload.queue.statusDone',
  duplicate: 'upload.queue.statusDuplicate',
  failed: 'upload.queue.statusFailed',
};

/** 状态徽标配色: 全部走语义令牌 (Chip color → Badge className) */
const STATUS_BADGE_CLASS: Record<UploadItem['status'], string> = {
  pending: '',
  hashing: 'border-transparent bg-info/10 text-info',
  uploading: 'border-transparent bg-info/10 text-info',
  done: 'border-transparent bg-success/10 text-success',
  duplicate: 'border-transparent bg-warning/10 text-warning',
  failed: 'border-transparent bg-destructive/10 text-destructive',
};

/** 未开始与校验中的队列行用骨架占位 (docs/webui.md 第 6 节) */
export function UploadQueueRow({
  item,
  onRetry,
  onRemove,
}: {
  item: UploadItem;
  onRetry: (id: string) => void;
  onRemove: (id: string) => void;
}) {
  const { t } = useTranslation();

  if (item.status === 'pending' || item.status === 'hashing') {
    return (
      <div aria-busy="true" aria-label={t(STATUS_LABEL_KEY[item.status])} className="w-full">
        <div className="flex items-center gap-4 rounded-lg border border-border p-3">
          <div className="flex w-14 shrink-0 justify-center">
            <Skeleton className="size-4 rounded-sm" />
          </div>
          <div className="flex min-w-0 flex-1 flex-col gap-1">
            <Skeleton className="h-4 w-[45%] rounded-sm" />
            <Skeleton className="h-3 w-20 rounded-sm" />
          </div>
          <Skeleton className="h-5 w-16 rounded-full" />
          <Skeleton className="size-8 rounded-lg" />
        </div>
      </div>
    );
  }

  const icon =
    item.status === 'done' ? (
      <CircleCheck className="size-4 text-success" />
    ) : item.status === 'duplicate' ? (
      <Ban className="size-4 text-warning" />
    ) : item.status === 'uploading' ? (
      <LoaderCircle className="size-4 animate-spin text-info" />
    ) : (
      <CircleAlert className="size-4 text-destructive" />
    );

  return (
    <div className="flex items-center gap-4 rounded-lg border border-border p-3">
      <div className="flex w-14 shrink-0 justify-center">{icon}</div>
      <div className="flex min-w-0 flex-1 flex-col gap-1">
        <p className="truncate text-sm" title={item.name}>
          {item.name}
        </p>
        <p className="text-muted-foreground text-xs">{formatBytes(item.size)}</p>
        {item.status === 'uploading' && <Progress value={item.progress} className="h-1.5" />}
        {item.status === 'failed' && (
          <p className="text-destructive text-xs">{item.error ?? t('upload.error.uploadFailed')}</p>
        )}
      </div>
      <Badge variant="outline" className={STATUS_BADGE_CLASS[item.status]}>
        {t(STATUS_LABEL_KEY[item.status])}
      </Badge>
      {item.status === 'failed' && (
        <Tooltip>
          <TooltipTrigger asChild>
            <Button
              variant="ghost"
              size="icon-sm"
              onClick={() => onRetry(item.id)}
              aria-label={t('upload.queue.retry')}
            >
              <RotateCw className="size-4" />
            </Button>
          </TooltipTrigger>
          <TooltipContent>{t('upload.queue.retry')}</TooltipContent>
        </Tooltip>
      )}
      <Tooltip>
        <TooltipTrigger asChild>
          <Button
            variant="ghost"
            size="icon-sm"
            onClick={() => onRemove(item.id)}
            aria-label={t('upload.queue.remove')}
          >
            <Trash2 className="size-4" />
          </Button>
        </TooltipTrigger>
        <TooltipContent>{t('upload.queue.remove')}</TooltipContent>
      </Tooltip>
    </div>
  );
}
