import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { CloudUpload } from 'lucide-react';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { ApiError } from '../api/client';
import { policiesApi } from '../api/images';
import { uploadApi } from '../api/upload';
import type { PolicySummary } from '../api/types';
import { useAuth } from '../hooks/useAuth';
import { PageHeader } from '../components/PageHeader';
import { UploadQueueRow } from '../components/upload/UploadQueueRow';
import { UploadResultCard } from '../components/upload/UploadResultCard';
import { createUploadItem } from '../components/upload/types';
import type { UploadItem } from '../components/upload/types';
import { Alert, AlertDescription, AlertTitle } from '../components/ui/alert';
import { Badge } from '../components/ui/badge';
import { Button } from '../components/ui/button';
import { Card, CardContent } from '../components/ui/card';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '../components/ui/select';
import { Separator } from '../components/ui/separator';
import { sha256Hex } from '../utils/hash';
import { STORAGE_KEYS } from '../utils/constants';
import { cn } from '../lib/utils';

export default function UploadPage() {
  const { t } = useTranslation();
  const { user, hasPermission } = useAuth();
  const canUpload = hasPermission('upload');

  const [queue, setQueue] = useState<UploadItem[]>([]);
  const [dragging, setDragging] = useState(false);
  const [policyId, setPolicyId] = useState<number | null>(null);
  const inputRef = useRef<HTMLInputElement | null>(null);
  const runningRef = useRef(false);
  const dragDepthRef = useRef(0);

  const { data: policies, isPending: policiesPending } = useQuery({
    queryKey: ['policies', 'mine'],
    queryFn: policiesApi.listMine,
    staleTime: 5 * 60 * 1000,
  });

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
      setQueue((current) => [...current, ...images.map(createUploadItem)]);
    },
    [t],
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
    const next = queue.find((item) => item.status === 'pending');
    if (!next) return;

    runningRef.current = true;
    const targetPolicy = policyId;

    void (async () => {
      try {
        updateItem(next.id, { status: 'hashing', progress: 0, error: undefined });
        const sha256 = await sha256Hex(next.file);
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
          next.file,
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
  }, [queue, canUpload, policyId, updateItem, t]);

  const stats = useMemo(() => {
    const done = queue.filter(
      (item) => item.status === 'done' || item.status === 'duplicate',
    ).length;
    const failed = queue.filter((item) => item.status === 'failed').length;
    return { done, failed, total: queue.length };
  }, [queue]);

  const results = queue.filter((item) => item.status === 'done' || item.status === 'duplicate');

  const retryItem = (id: string) =>
    updateItem(id, { status: 'pending', progress: 0, error: undefined });
  const removeItem = (id: string) =>
    setQueue((current) => current.filter((item) => item.id !== id));

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
    <div className="flex min-h-[calc(100svh-9rem)] flex-1 flex-col">
      <div className="flex flex-1 flex-col gap-4">
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
            <SelectTrigger aria-label={t('upload.policy.label')} className="w-[115px]">
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

        <div className="flex justify-center">
          <div
            onClick={() => inputRef.current?.click()}
            onKeyDown={(event) => {
              if (event.key === 'Enter' || event.key === ' ') inputRef.current?.click();
            }}
            role="button"
            tabIndex={0}
            className={cn(
              'border-border flex w-full max-w-xl cursor-pointer flex-col items-center rounded-xl border-2 border-dashed p-5 text-center transition-colors md:p-6',
              dragging ? 'border-primary bg-accent' : 'bg-card',
            )}
          >
            <CloudUpload className="text-muted-foreground size-11" />
            <h2 className="mt-2 text-lg font-medium">
              {dragging ? t('upload.dropzone.hintActive') : t('upload.dropzone.hint')}
            </h2>
            <p className="text-muted-foreground mt-1 text-sm">{t('upload.dropzone.pasteHint')}</p>
            <Button className="mt-4">
              <CloudUpload className="size-4" />
              {t('upload.dropzone.selectFiles')}
            </Button>
            <p className="text-muted-foreground mt-3 text-xs">
              {t('upload.dropzone.objectRetainHint')}
            </p>
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
        </div>

        {queue.length > 0 && (
          <Card>
            <CardContent>
              <div className="mb-3 flex items-center justify-between gap-3">
                <div className="min-w-0">
                  <h2 className="font-heading text-base font-medium">{t('upload.queue.title')}</h2>
                  <p className="text-muted-foreground text-xs">
                    {t('upload.queue.summary', stats)}
                  </p>
                </div>
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() =>
                    setQueue((current) =>
                      current.filter(
                        (item) => item.status !== 'done' && item.status !== 'duplicate',
                      ),
                    )
                  }
                >
                  {t('upload.queue.clearFinished')}
                </Button>
              </div>
              <div className="flex flex-col gap-2">
                {queue.map((item) => (
                  <UploadQueueRow
                    key={item.id}
                    item={item}
                    onRetry={retryItem}
                    onRemove={removeItem}
                  />
                ))}
              </div>
            </CardContent>
          </Card>
        )}

        {results.length > 0 && (
          <div className="flex flex-col gap-4">
            <Separator />
            <div className="flex items-center gap-2">
              <h2 className="font-heading text-base font-medium">{t('upload.result.title')}</h2>
              <Badge variant="outline">{results.length}</Badge>
            </div>
            <div className="flex flex-col gap-4">
              {results.map((item) => (
                <UploadResultCard key={item.id} item={item} />
              ))}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
