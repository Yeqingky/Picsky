import { useEffect, useState } from 'react';
import type { ReactNode } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { ExternalLink, Save, Trash2 } from 'lucide-react';
import { imagesApi } from '../../api/images';
import { ApiError } from '../../api/client';
import type { DeleteResult, ImageRecord } from '../../api/types';
import { LazyImage } from '../LazyImage';
import { UrlList } from '../UrlList';
import { CopyButton, CopyFormatMenu } from '../CopyButton';
import { useConfirm } from '../ConfirmDialog';
import { useAuth } from '../../hooks/useAuth';
import { Badge } from '../ui/badge';
import { Button } from '../ui/button';
import { Input } from '../ui/input';
import { Skeleton } from '../ui/skeleton';
import { Sheet, SheetContent, SheetHeader, SheetTitle } from '../ui/sheet';
import { Tooltip, TooltipContent, TooltipTrigger } from '../ui/tooltip';
import { cn } from '../../lib/utils';
import { formatBytes, formatDateTime } from '../../utils/format';
import { dimensions } from '../../utils/image';

function FieldRow({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex gap-3 text-sm">
      <dt className="text-muted-foreground w-24 shrink-0">{label}</dt>
      <dd className="min-w-0 flex-1 break-all">{children}</dd>
    </div>
  );
}

function HashRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center gap-2">
      <Badge variant="outline" className="w-16 shrink-0 justify-center">
        {label}
      </Badge>
      <span className="min-w-0 flex-1 font-mono text-xs break-all">{value}</span>
      <CopyButton value={value} />
    </div>
  );
}

export interface ImageDetailDrawerProps {
  imageId: number | null;
  open: boolean;
  onClose: () => void;
  onDeleted?: (result: DeleteResult) => void;
}

/** 图片详情抽屉: 预览 + 完整哈希 + 尺寸 + 来源 + 对象状态 + 配额状态 + 各域名 URL */
export function ImageDetailDrawer({ imageId, open, onClose, onDeleted }: ImageDetailDrawerProps) {
  const { t } = useTranslation();
  const confirm = useConfirm();
  const queryClient = useQueryClient();
  const { hasPermission } = useAuth();
  const canDelete = hasPermission('delete_own') || hasPermission('delete_all');
  const [filename, setFilename] = useState('');

  const { data, isPending, isError, refetch } = useQuery({
    queryKey: ['images', 'detail', imageId],
    queryFn: () => imagesApi.get(imageId as number),
    enabled: open && imageId !== null,
  });

  useEffect(() => {
    if (data) setFilename(data.original_filename);
  }, [data]);

  const invalidateGallery = async () => {
    await queryClient.invalidateQueries({ queryKey: ['images'] });
  };

  const renameMutation = useMutation({
    mutationFn: (record: ImageRecord) =>
      imagesApi.rename(record.id, { original_filename: filename }),
    onSuccess: async (record) => {
      queryClient.setQueryData(['images', 'detail', record.id], record);
      await invalidateGallery();
      toast.success(t('common.state.success'));
    },
  });

  const deleteMutation = useMutation({
    mutationFn: (id: number) => imagesApi.remove(id),
    onSuccess: async (result) => {
      await invalidateGallery();
      onDeleted?.(result);
      if (result.object_deleted) {
        toast.success(t('gallery.delete.successSingle'));
      } else {
        toast.warning(
          t('gallery.delete.objectRetained', {
            reason: t(`gallery.delete.reason.${result.reason}`),
          }),
        );
      }
      onClose();
    },
  });

  const handleDelete = async () => {
    if (!data) return;
    const ok = await confirm({
      title: t('gallery.detail.delete'),
      description: t('common.confirm.deleteImage'),
      danger: true,
    });
    if (ok) deleteMutation.mutate(data.id);
  };

  const renameError =
    renameMutation.error instanceof ApiError
      ? renameMutation.error.message
      : t('common.state.failed');
  const deleteError =
    deleteMutation.error instanceof ApiError
      ? deleteMutation.error.message
      : t('common.state.failed');

  return (
    <Sheet
      open={open}
      onOpenChange={(next) => {
        if (!next) onClose();
      }}
    >
      <SheetContent
        side="right"
        className="w-full gap-0 p-0 data-[side=right]:w-full data-[side=right]:sm:max-w-[520px]"
      >
        <SheetHeader className="border-b">
          <SheetTitle>{t('gallery.detail.title')}</SheetTitle>
        </SheetHeader>

        <div className="overflow-y-auto p-4">
          {isPending && (
            <div aria-busy="true" aria-label={t('common.a11y.loading')}>
              <Skeleton className="mb-4 h-64 w-full rounded-lg" />
              <div className="flex flex-col gap-3">
                {Array.from({ length: 6 }, (_, index) => (
                  <div key={index} className="flex items-center gap-3">
                    <Skeleton className="h-4 w-24" />
                    <Skeleton className="h-4 flex-1" />
                  </div>
                ))}
              </div>
            </div>
          )}

          {isError && (
            <div className="flex flex-col items-start gap-2">
              <p className="text-destructive text-sm">{t('gallery.error.load')}</p>
              <Button variant="outline" size="sm" onClick={() => void refetch()}>
                {t('common.action.retry')}
              </Button>
            </div>
          )}

          {data && (
            <div className="flex flex-col gap-4">
              <div className="h-64 overflow-hidden rounded-lg border">
                <LazyImage
                  src={data.url}
                  alt={data.original_filename}
                  hash={data.sha256}
                  objectFit="contain"
                  eager
                />
              </div>

              <div className="flex flex-wrap gap-2">
                <Badge>{data.format}</Badge>
                <Badge variant={data.quota_charged ? 'default' : 'outline'}>
                  {data.quota_charged ? t('gallery.detail.charged') : t('gallery.detail.free')}
                </Badge>
                <Badge
                  variant="outline"
                  className={cn(
                    'border-transparent',
                    data.object_deleted_at
                      ? 'bg-destructive/10 text-destructive'
                      : 'bg-success/10 text-success',
                  )}
                >
                  {data.object_deleted_at
                    ? t('common.object.deleted')
                    : t('common.object.retained')}
                </Badge>
                <Badge variant="outline">
                  {t(`common.source.${data.source}`, { defaultValue: data.source })}
                </Badge>
              </div>

              <section>
                <h3 className="mb-2 text-base font-medium">{t('gallery.detail.basic')}</h3>
                <dl className="flex flex-col gap-2">
                  <FieldRow label={t('gallery.detail.filename')}>
                    <div className="flex flex-col gap-1">
                      <div className="flex items-center gap-1">
                        <Input
                          value={filename}
                          onChange={(event) => setFilename(event.target.value)}
                          aria-invalid={renameMutation.isError}
                          className="flex-1"
                        />
                        <Tooltip>
                          <TooltipTrigger asChild>
                            <span>
                              <Button
                                variant="ghost"
                                size="icon-sm"
                                disabled={
                                  filename.trim() === '' ||
                                  filename === data.original_filename ||
                                  renameMutation.isPending
                                }
                                onClick={() => renameMutation.mutate(data)}
                                aria-label={t('common.action.save')}
                              >
                                <Save className="size-4" />
                              </Button>
                            </span>
                          </TooltipTrigger>
                          <TooltipContent>{t('common.action.save')}</TooltipContent>
                        </Tooltip>
                      </div>
                      {renameMutation.isError ? (
                        <p className="text-destructive text-sm">{renameError}</p>
                      ) : (
                        <p className="text-muted-foreground text-sm">
                          {t('gallery.detail.renameHint')}
                        </p>
                      )}
                    </div>
                  </FieldRow>
                  <FieldRow label={t('gallery.detail.policy')}>{data.policy_name}</FieldRow>
                  <FieldRow label={t('gallery.detail.path')}>
                    <span className="font-mono text-xs">{data.rel_path}</span>
                  </FieldRow>
                  <FieldRow label={t('gallery.detail.objectKey')}>
                    <div className="flex items-center gap-1">
                      <span className="min-w-0 font-mono text-xs break-all">{data.object_key}</span>
                      <CopyButton value={data.object_key} />
                    </div>
                  </FieldRow>
                  <FieldRow label={t('gallery.detail.size')}>{formatBytes(data.size)}</FieldRow>
                  <FieldRow label={t('gallery.detail.dimensions')}>
                    {dimensions(data.width, data.height)}
                  </FieldRow>
                  <FieldRow label={t('gallery.detail.mime')}>{data.mime}</FieldRow>
                  <FieldRow label={t('gallery.detail.source')}>
                    {t(`common.source.${data.source}`, { defaultValue: data.source })}
                  </FieldRow>
                  <FieldRow label={t('gallery.detail.clientIp')}>{data.client_ip ?? '—'}</FieldRow>
                  <FieldRow label={t('gallery.detail.createdAt')}>
                    {formatDateTime(data.created_at)}
                  </FieldRow>
                  <FieldRow label={t('gallery.detail.objectStatus')}>
                    {data.object_deleted_at
                      ? `${t('common.object.deleted')} · ${formatDateTime(data.object_deleted_at)}`
                      : t('common.object.retained')}
                  </FieldRow>
                </dl>
              </section>

              <hr className="border-border" />

              <section>
                <h3 className="mb-2 text-base font-medium">{t('gallery.detail.hashes')}</h3>
                <div className="flex flex-col gap-2">
                  <HashRow label="md5" value={data.md5} />
                  <HashRow label="sha256" value={data.sha256} />
                  <HashRow label="sha512" value={data.sha512} />
                </div>
              </section>

              <hr className="border-border" />

              <section>
                <div className="mb-2 flex items-center justify-between gap-2">
                  <h3 className="text-base font-medium">{t('gallery.detail.urls')}</h3>
                  <CopyFormatMenu url={data.url} filename={data.original_filename} />
                </div>
                <UrlList
                  urls={
                    data.urls.length > 0
                      ? data.urls
                      : [{ alias: '—', url: data.url, is_default: true }]
                  }
                />
                <Button variant="outline" size="sm" className="mt-2" asChild>
                  <a href={data.url} target="_blank" rel="noreferrer noopener">
                    <ExternalLink className="size-4" />
                    {t('gallery.detail.openOriginal')}
                  </a>
                </Button>
              </section>

              <hr className="border-border" />

              <section>
                {canDelete && (
                  <Button
                    variant="destructive"
                    onClick={() => {
                      void handleDelete();
                    }}
                    disabled={deleteMutation.isPending}
                  >
                    <Trash2 className="size-4" />
                    {t('gallery.detail.delete')}
                  </Button>
                )}
                {deleteMutation.isError && (
                  <p className="text-destructive mt-2 text-sm">{deleteError}</p>
                )}
              </section>
            </div>
          )}
        </div>
      </SheetContent>
    </Sheet>
  );
}
