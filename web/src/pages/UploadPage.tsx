import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { CloudUpload, Copy, Play, Trash2 } from 'lucide-react';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { ApiError } from '../api/client';
import { policiesApi } from '../api/images';
import { uploadApi } from '../api/upload';
import type { PolicySummary } from '../api/types';
import { useAuth } from '../hooks/useAuth';
import { useUploadSettings } from '../hooks/useUploadSettings';
import type { UploadOutputFormat } from '../hooks/useUploadSettings';
import { compressImage } from '../lib/imageCompression';
import { isAnimatedImage } from '../utils/imageAnimation';
import { CopyFormatToggle } from '../components/CopyFormatToggle';
import { PageHeader } from '../components/PageHeader';
import { UploadFileRow } from '../components/upload/UploadFileRow';
import {
  createUploadItem,
  uploadItemFilename,
  uploadItemPayload,
  uploadItemUrl,
} from '../components/upload/types';
import type { UploadItem } from '../components/upload/types';
import { Alert, AlertDescription, AlertTitle } from '../components/ui/alert';
import { Button } from '../components/ui/button';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '../components/ui/select';
import { Tooltip, TooltipContent, TooltipTrigger } from '../components/ui/tooltip';
import { copyText, renderCopyText } from '../utils/clipboard';
import type { CopyFormat } from '../utils/clipboard';
import { sha256Hex } from '../utils/hash';
import { STORAGE_KEYS } from '../utils/constants';
import { cn } from '../lib/utils';

type UploadFilter = 'all' | 'done' | 'uploading' | 'queued' | 'failed';

const FILTERS: UploadFilter[] = ['all', 'done', 'uploading', 'queued', 'failed'];

const FORMAT_LABELS: Record<string, string> = {
  jpeg: 'JPEG',
  png: 'PNG',
  gif: 'GIF',
  webp: 'WebP',
  avif: 'AVIF',
  bmp: 'BMP',
  tiff: 'TIFF',
  ico: 'ICO',
  heic: 'HEIC',
};

function formatLabel(format: string): string {
  return FORMAT_LABELS[format] ?? format.toUpperCase();
}

/** 从浏览器 MIME 或文件名推断图片格式, 仅用于策略格式预检. */
function fileFormatName(file: File): string {
  const byType: Record<string, string> = {
    'image/jpeg': 'jpeg',
    'image/png': 'png',
    'image/gif': 'gif',
    'image/webp': 'webp',
    'image/avif': 'avif',
    'image/bmp': 'bmp',
    'image/tiff': 'tiff',
    'image/x-icon': 'ico',
    'image/vnd.microsoft.icon': 'ico',
    'image/heic': 'heic',
    'image/heif': 'heic',
  };
  const type = file.type.toLowerCase();
  if (byType[type]) return byType[type];
  const extension = file.name.split('.').pop()?.toLowerCase() ?? '';
  switch (extension) {
    case 'jpg':
    case 'jpeg':
      return 'jpeg';
    case 'tif':
      return 'tiff';
    default:
      return extension;
  }
}

function policyAllowsFormat(formats: string[] | undefined, format: string): boolean {
  return !formats || formats.length === 0 || formats.includes(format);
}

function matchesFilter(item: UploadItem, filter: UploadFilter): boolean {
  switch (filter) {
    case 'done':
      return item.status === 'done' || item.status === 'duplicate';
    case 'uploading':
      return (
        item.status === 'uploading' || item.status === 'hashing' || item.status === 'compressing'
      );
    case 'queued':
      return item.status === 'pending';
    case 'failed':
      return item.status === 'failed';
    default:
      return true;
  }
}

export default function UploadPage() {
  const { t } = useTranslation();
  const { user, hasPermission } = useAuth();
  const canUpload = hasPermission('upload');
  const settings = useUploadSettings();

  const [queue, setQueue] = useState<UploadItem[]>([]);
  const [dragging, setDragging] = useState(false);
  const [policyId, setPolicyId] = useState<number | null>(null);
  const [filter, setFilter] = useState<UploadFilter>('all');
  const [copyFormat, setCopyFormat] = useState<CopyFormat>('direct');
  const inputRef = useRef<HTMLInputElement | null>(null);
  const runningRef = useRef(false);
  const dragDepthRef = useRef(0);

  const { data: policies, isPending: policiesPending } = useQuery({
    queryKey: ['policies', 'mine'],
    queryFn: policiesApi.listMine,
    staleTime: 5 * 60 * 1000,
  });

  const selectedPolicy = policies?.find((policy) => policy.id === policyId);
  const policyFormats = selectedPolicy?.formats;
  const compressionFormat = useMemo<UploadOutputFormat>(() => {
    const requested = settings.format;
    const requestedName = requested === 'jpg' ? 'jpeg' : requested;
    if (policyAllowsFormat(policyFormats, requestedName)) return requested;
    if (policyAllowsFormat(policyFormats, 'jpeg')) return 'jpeg';
    return requested;
  }, [settings.format, policyFormats]);
  const formatOverride = compressionFormat !== settings.format;
  const formatNoticeRef = useRef<number | null>(null);

  /** 策略选择: 记忆上次选择, 首次用 default_policy_id, 再退化为默认/第一个策略 */
  useEffect(() => {
    if (!policies || policyId !== null) return;
    let stored: number | null = null;
    try {
      const raw = window.localStorage.getItem(STORAGE_KEYS.uploadPolicy);
      stored = raw === null ? null : Number(raw);
    } catch {
      stored = null;
    }
    const remembered = policies.find((policy) => policy.id === stored);
    const preferred =
      policies.find((policy) => policy.id === user?.default_policy_id) ??
      policies.find((policy) => policy.is_default) ??
      policies[0];
    setPolicyId(remembered?.id ?? preferred?.id ?? null);
  }, [policies, policyId, user?.default_policy_id]);

  const selectPolicy = (next: PolicySummary | undefined) => {
    setPolicyId(next?.id ?? null);
    if (!next) return;
    try {
      window.localStorage.setItem(STORAGE_KEYS.uploadPolicy, String(next.id));
    } catch {
      // 忽略存储失败
    }
  };

  const updateItem = useCallback((id: string, patch: Partial<UploadItem>) => {
    setQueue((current) => current.map((item) => (item.id === id ? { ...item, ...patch } : item)));
  }, []);

  const addFiles = useCallback(
    (files: File[]) => {
      const images = files.filter((file) => file.type.startsWith('image/'));
      if (images.length !== files.length) {
        toast.warning(t('upload.error.notImage'));
      }
      if (images.length === 0) return;
      setQueue((current) => [
        ...current,
        ...images.map((file) => createUploadItem(file, settings.autoUpload)),
      ]);
    },
    [settings.autoUpload, t],
  );

  // 拖拽到页面任意位置 + Ctrl+V 粘贴
  useEffect(() => {
    if (!canUpload) return;

    const onDragEnter = (event: DragEvent) => {
      event.preventDefault();
      dragDepthRef.current += 1;
      setDragging(true);
    };
    const onDragOver = (event: DragEvent) => {
      event.preventDefault();
    };
    const onDragLeave = (event: DragEvent) => {
      event.preventDefault();
      dragDepthRef.current = Math.max(0, dragDepthRef.current - 1);
      if (dragDepthRef.current === 0) setDragging(false);
    };
    const onDrop = (event: DragEvent) => {
      event.preventDefault();
      dragDepthRef.current = 0;
      setDragging(false);
      const files = event.dataTransfer?.files;
      if (files && files.length > 0) addFiles([...files]);
    };
    const onPaste = (event: ClipboardEvent) => {
      const files = event.clipboardData?.files;
      if (files && files.length > 0) addFiles([...files]);
    };

    window.addEventListener('dragenter', onDragEnter);
    window.addEventListener('dragover', onDragOver);
    window.addEventListener('dragleave', onDragLeave);
    window.addEventListener('drop', onDrop);
    window.addEventListener('paste', onPaste);
    return () => {
      window.removeEventListener('dragenter', onDragEnter);
      window.removeEventListener('dragover', onDragOver);
      window.removeEventListener('dragleave', onDragLeave);
      window.removeEventListener('drop', onDrop);
      window.removeEventListener('paste', onPaste);
    };
  }, [canUpload, addFiles]);

  // 串行处理队列: 计算 sha256 -> 秒传检查 -> 上传 (失败可重试)
  useEffect(() => {
    if (!canUpload || runningRef.current || policyId === null) return;
    const next = queue.find((item) => item.status === 'pending' && item.armed);
    if (!next) return;

    runningRef.current = true;
    const targetPolicy = policyId;

    void (async () => {
      let payload = uploadItemPayload(next);
      try {
        if (next.uploadFile === undefined && settings.compression) {
          let animated = false;
          try {
            animated = await isAnimatedImage(next.file);
          } catch {
            animated = false;
          }
          if (!animated || (settings.animated && policyAllowsFormat(policyFormats, 'webp'))) {
            updateItem(next.id, { status: 'compressing', progress: 0, error: undefined });
            try {
              const compressed = await compressImage(next.file, {
                quality: settings.quality,
                format: compressionFormat,
                forceWebp: animated,
              });
              const originalAllowed = policyAllowsFormat(policyFormats, fileFormatName(next.file));
              if (
                settings.keepOriginal &&
                originalAllowed &&
                compressed.file.size >= next.file.size
              ) {
                // 编码结果没有更小: 保留原图与原始文件名
                payload = next.file;
                updateItem(next.id, {
                  status: 'hashing',
                  compressionError: undefined,
                  compressedSize: compressed.file.size,
                });
              } else {
                if (formatOverride && formatNoticeRef.current !== targetPolicy) {
                  formatNoticeRef.current = targetPolicy;
                  toast.info(
                    t('upload.error.policyFormatOverride', {
                      format: formatLabel(compressionFormat),
                    }),
                  );
                }
                payload = compressed.file;
                updateItem(next.id, {
                  uploadFile: compressed.file,
                  status: 'hashing',
                  compressionError: undefined,
                  compressedSize: compressed.file.size,
                });
              }
            } catch (error) {
              const message =
                error instanceof Error ? error.message : t('upload.error.compressFailed');
              if (settings.failure === 'skip') {
                updateItem(next.id, { status: 'skipped', progress: 0, compressionError: message });
                return;
              }
              updateItem(next.id, { status: 'hashing', compressionError: message });
            }
          }
        }

        const payloadFormat = fileFormatName(payload);
        if (!policyAllowsFormat(policyFormats, payloadFormat)) {
          const message = t('upload.error.policyFormat', {
            format: formatLabel(payloadFormat),
            allowed: (policyFormats ?? []).map(formatLabel).join(', '),
          });
          toast.error(message);
          updateItem(next.id, { status: 'failed', error: message });
          return;
        }

        updateItem(next.id, { status: 'hashing', progress: 0, error: undefined });
        const sha256 = await sha256Hex(payload);
        const check = await uploadApi.check({ sha256, policy_id: targetPolicy });
        if (check.exists && check.record) {
          updateItem(next.id, { status: 'duplicate', progress: 100, dedupRecord: check.record });
          return;
        }
        updateItem(next.id, { status: 'uploading' });
        const result = await uploadApi.uploadFile(
          {
            policyId: targetPolicy,
            onProgress: (percent) => updateItem(next.id, { progress: percent }),
          },
          payload,
        );
        updateItem(next.id, { status: 'done', progress: 100, result });
      } catch (error) {
        updateItem(next.id, {
          status: 'failed',
          error:
            error instanceof ApiError
              ? error.message
              : error instanceof Error
                ? error.message
                : t('upload.error.uploadFailed'),
        });
      } finally {
        runningRef.current = false;
      }
    })();
  }, [
    queue,
    canUpload,
    policyId,
    updateItem,
    t,
    settings,
    policyFormats,
    compressionFormat,
    formatOverride,
  ]);

  const stats = useMemo(() => {
    let bytes = 0;
    let uploaded = 0;
    let done = 0;
    let uploading = 0;
    let queued = 0;
    let failed = 0;
    let skipped = 0;
    for (const item of queue) {
      const payloadSize = item.uploadFile?.size ?? item.size;
      if (item.status !== 'skipped') bytes += payloadSize;
      if (item.status === 'done' || item.status === 'duplicate') {
        done += 1;
        uploaded += payloadSize;
      } else if (item.status === 'uploading') {
        uploading += 1;
        uploaded += (payloadSize * item.progress) / 100;
      } else if (item.status === 'hashing' || item.status === 'compressing') {
        uploading += 1;
      } else if (item.status === 'failed') {
        failed += 1;
      } else if (item.status === 'skipped') {
        skipped += 1;
      } else {
        queued += 1;
      }
    }
    return {
      total: queue.length,
      done,
      uploading,
      queued,
      failed,
      skipped,
      progress: bytes === 0 ? 0 : Math.round((uploaded / bytes) * 100),
    };
  }, [queue]);

  const waiting = useMemo(
    () => queue.filter((item) => item.status === 'pending' && !item.armed).length,
    [queue],
  );

  const startUploads = useCallback(() => {
    setQueue((current) =>
      current.map((item) =>
        item.status === 'pending' && !item.armed ? { ...item, armed: true } : item,
      ),
    );
  }, []);

  // 开启自动上传时, 把此前等待中的文件一并放行
  useEffect(() => {
    if (settings.autoUpload) startUploads();
  }, [settings.autoUpload, startUploads]);

  const completed = useMemo(() => queue.filter((item) => uploadItemUrl(item) !== null), [queue]);

  const visible = useMemo(
    () => queue.filter((item) => matchesFilter(item, filter)),
    [queue, filter],
  );

  const retryItem = (id: string) =>
    updateItem(id, { status: 'pending', progress: 0, armed: true, error: undefined });

  const copyItem = useCallback(
    async (item: UploadItem) => {
      const url = uploadItemUrl(item);
      if (url === null) return;
      const ok = await copyText(
        renderCopyText(copyFormat, { url, filename: uploadItemFilename(item) }),
      );
      if (ok) toast.success(t('common.action.copied'));
      else toast.error(t('common.action.copyFailed'));
    },
    [copyFormat, t],
  );

  const clearQueue = useCallback(() => setQueue([]), []);

  const removeItem = useCallback((id: string) => {
    setQueue((current) => current.filter((item) => item.id !== id));
  }, []);

  const copyAll = useCallback(async () => {
    const text = completed
      .map((item) => {
        const url = uploadItemUrl(item);
        if (url === null) return '';
        return renderCopyText(copyFormat, { url, filename: uploadItemFilename(item) });
      })
      .filter((line) => line !== '')
      .join('\n');
    if (text === '') return;
    const ok = await copyText(text);
    if (ok) toast.success(t('common.action.copied'));
    else toast.error(t('common.action.copyFailed'));
  }, [completed, copyFormat, t]);

  if (!canUpload) {
    return (
      <div>
        <PageHeader title={t('upload.title')} description={t('upload.description')} />
        <Alert>
          <AlertTitle>{t('upload.noPermission.title')}</AlertTitle>
          <AlertDescription>{t('upload.noPermission.description')}</AlertDescription>
        </Alert>
      </div>
    );
  }

  const policyUnavailable = !policiesPending && (policies?.length ?? 0) === 0;

  return (
    <div className="mx-auto flex w-full max-w-4xl flex-1 flex-col gap-4">
      <div className="flex items-center justify-end gap-2">
        {policyUnavailable && (
          <p className="text-muted-foreground text-xs">{t('upload.policy.none')}</p>
        )}
        <Select
          value={policyId === null ? undefined : String(policyId)}
          onValueChange={(value) => {
            selectPolicy(policies?.find((policy) => policy.id === Number(value)));
          }}
          disabled={policiesPending || policyUnavailable}
        >
          <SelectTrigger aria-label={t('upload.policy.label')} className="w-[184px]">
            <SelectValue placeholder={t('upload.policy.label')} />
          </SelectTrigger>
          <SelectContent>
            {(policies ?? []).map((policy) => (
              <SelectItem key={policy.id} value={String(policy.id)}>
                {policy.name}
                {policy.is_default ? ` · ${t('upload.policy.defaultHint')}` : ''}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>

      <div
        onClick={() => inputRef.current?.click()}
        onKeyDown={(event) => {
          if (event.key === 'Enter' || event.key === ' ') inputRef.current?.click();
        }}
        role="button"
        tabIndex={0}
        className={cn(
          'flex w-full cursor-pointer flex-col items-center justify-center gap-1 rounded-xl border-2 border-dashed px-4 py-8 text-center transition-colors',
          dragging ? 'border-primary bg-accent' : 'border-border bg-card hover:border-primary/60',
        )}
      >
        <div className="flex items-center gap-2">
          <CloudUpload className="text-muted-foreground size-5" />
          <span className="text-sm font-medium">
            {dragging ? t('upload.dropzone.hintActive') : t('upload.dropzone.hint')}
          </span>
        </div>
        <p className="text-muted-foreground text-xs">{t('upload.dropzone.pasteHint')}</p>
        <input
          ref={inputRef}
          type="file"
          accept="image/*"
          multiple
          hidden
          onChange={(event) => {
            const files = event.target.files;
            if (files) addFiles([...files]);
            event.target.value = '';
          }}
        />
      </div>

      {queue.length > 0 && (
        <>
          <div className="flex flex-col gap-2">
            <div className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 gap-y-1 sm:flex sm:items-end sm:justify-between sm:gap-3">
              <span className="font-medium">
                {t('upload.summary.total', { count: stats.total })}
              </span>
              <div className="order-3 flex flex-wrap items-center gap-x-3 gap-y-1 text-sm sm:order-none">
                <span className="text-success text-xs">
                  {t('upload.summary.done', { count: stats.done })}
                </span>
                <span className="text-info text-xs">
                  {t('upload.summary.uploading', { count: stats.uploading })}
                </span>
                <span className="text-muted-foreground text-xs">
                  {t('upload.summary.queued', { count: stats.queued })}
                </span>
                <span className="text-destructive text-xs">
                  {t('upload.summary.failed', { count: stats.failed })}
                </span>
                {stats.skipped > 0 && (
                  <span className="text-warning text-xs">
                    {t('upload.summary.skipped', { count: stats.skipped })}
                  </span>
                )}
              </div>
              <div className="order-2 flex h-7 items-center justify-end gap-2 sm:order-none sm:ml-auto">
                {!settings.autoUpload && waiting > 0 && (
                  <Button type="button" size="sm" variant="outline" onClick={startUploads}>
                    <Play className="size-3.5" />
                    {t('upload.queue.start', { count: waiting })}
                  </Button>
                )}
                <span className="hidden text-sm font-medium tabular-nums sm:inline">
                  {stats.progress}%
                </span>
              </div>
              <span className="order-4 justify-self-end text-sm font-medium tabular-nums sm:hidden">
                {stats.progress}%
              </span>
            </div>
            <div className="bg-muted h-1 overflow-hidden rounded-full">
              <div
                className="bg-primary h-full rounded-full transition-[width]"
                style={{ width: `${stats.progress}%` }}
              />
            </div>
          </div>

          <div className="flex items-center justify-between gap-3">
            <Select value={filter} onValueChange={(value) => setFilter(value as UploadFilter)}>
              <SelectTrigger aria-label={t('upload.filter.label')} className="w-[132px]">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {FILTERS.map((option) => (
                  <SelectItem key={option} value={option}>
                    {t(`upload.filter.${option}`)}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>

            <div className="flex items-center gap-2">
              <CopyFormatToggle value={copyFormat} onChange={setCopyFormat} />
              <Tooltip>
                <TooltipTrigger asChild>
                  <Button
                    variant="outline"
                    size="icon"
                    className="text-destructive hover:text-destructive border-destructive/40 hover:bg-destructive/10 rounded-full"
                    aria-label={t('upload.clear')}
                    disabled={stats.uploading > 0}
                    onClick={clearQueue}
                  >
                    <Trash2 className="size-4" />
                  </Button>
                </TooltipTrigger>
                <TooltipContent>
                  {stats.uploading > 0 ? t('upload.clearBlocked') : t('upload.clear')}
                </TooltipContent>
              </Tooltip>
              <Tooltip>
                <TooltipTrigger asChild>
                  <Button
                    variant="outline"
                    size="icon"
                    className="rounded-full"
                    aria-label={t('upload.copyAll', { count: completed.length })}
                    disabled={completed.length === 0}
                    onClick={() => {
                      void copyAll();
                    }}
                  >
                    <Copy className="size-4" />
                  </Button>
                </TooltipTrigger>
                <TooltipContent>{t('upload.copyAll', { count: completed.length })}</TooltipContent>
              </Tooltip>
            </div>
          </div>

          <div className="bg-card overflow-hidden rounded-xl border border-border">
            {visible.length === 0 ? (
              <p className="text-muted-foreground px-4 py-6 text-center text-sm">
                {t('upload.filter.empty')}
              </p>
            ) : (
              visible.map((item, index) => (
                <UploadFileRow
                  key={item.id}
                  item={item}
                  onCopy={(target) => {
                    void copyItem(target);
                  }}
                  onRetry={retryItem}
                  onRemove={removeItem}
                  className={index > 0 ? 'border-t border-border' : undefined}
                />
              ))
            )}
          </div>
        </>
      )}
    </div>
  );
}
