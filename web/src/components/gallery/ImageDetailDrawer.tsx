import { useState } from 'react';
import type { ReactNode } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { Trash2 } from 'lucide-react';
import { imagesApi } from '../../api/images';
import { ApiError } from '../../api/client';
import type { DeleteResult } from '../../api/types';
import { LazyImage } from '../LazyImage';
import { UrlList } from '../UrlList';
import { CopyButton } from '../CopyButton';
import { CopyFormatToggle } from '../CopyFormatToggle';
import { useConfirm } from '../ConfirmDialog';
import { useAuth } from '../../hooks/useAuth';
import { Badge } from '../ui/badge';
import { Button } from '../ui/button';
import { Skeleton } from '../ui/skeleton';
import { Sheet, SheetContent, SheetHeader, SheetTitle } from '../ui/sheet';
import { formatBytes, formatDateTime } from '../../utils/format';
import { dimensions } from '../../utils/image';
import type { CopyFormat } from '../../utils/clipboard';

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
      <code
        tabIndex={0}
        className="bg-muted/50 focus-visible:ring-ring block min-w-0 flex-1 overflow-x-auto rounded-md border px-2 py-1 font-mono text-xs whitespace-nowrap focus-visible:ring-2 focus-visible:outline-none"
      >
        {value}
      </code>
      <CopyButton value={value} />
    </div>
  );
}

export interface ImageDetailDrawerProps {
  imageId: number | null;
  open: boolean;
  onClose: () => void;
  onDeleted?: (result: DeleteResult) => void;
  onFilterUser?: (email: string) => void;
}

/** 图片详情抽屉: 预览 + 完整哈希 + 尺寸 + 上传用户 + 来源 + 对象状态 + 配额状态 + URL */
export function ImageDetailDrawer({
  imageId,
  open,
  onClose,
  onDeleted,
  onFilterUser,
}: ImageDetailDrawerProps) {
  const { t } = useTranslation();
  const confirm = useConfirm();
  const queryClient = useQueryClient();
  const { hasPermission } = useAuth();
  const hasDeletePermission = hasPermission('delete_own') || hasPermission('delete_all');
  const [copyFormat, setCopyFormat] = useState<CopyFormat>('direct');

  const { data, isPending, isError, refetch } = useQuery({
    queryKey: ['images', 'detail', imageId],
    queryFn: () => imagesApi.get(imageId as number),
    enabled: open && imageId !== null,
  });
  // 后端不支持删除时不提供删除入口, 避免留下无法清理的外部对象
  const canDelete = hasDeletePermission && (data?.policy_capabilities.includes('delete') ?? false);

  const invalidateGallery = async () => {
    // 详情查询对应的记录已被删除, 刷新会触发服务端 404; 只刷新列表类查询
    await queryClient.invalidateQueries({
      queryKey: ['images'],
      predicate: (query) => query.queryKey[1] !== 'detail',
    });
  };

  const deleteMutation = useMutation({
    mutationFn: (id: number) => imagesApi.remove(id),
    onSuccess: async (result) => {
      onClose();
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
    },
  });

  const handleDelete = async () => {
    if (!data) return;
    const ok = await confirm({
      title: t('gallery.detail.deleteImage'),
      description: t('common.confirm.deleteImageObject'),
      danger: true,
    });
    if (ok) deleteMutation.mutate(data.id);
  };

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
            <div
              aria-busy="true"
              aria-label={t('common.a11y.loading')}
              className="flex flex-col gap-4"
            >
              <Skeleton className="h-64 w-full rounded-lg" />
              <div className="flex flex-wrap gap-2">
                <Skeleton className="h-5 w-12 rounded-full" />
                <Skeleton className="h-5 w-16 rounded-full" />
                <Skeleton className="h-5 w-20 rounded-full" />
              </div>
              <section>
                <Skeleton className="mb-2 h-5 w-20 rounded-sm" />
                <div className="flex flex-col gap-2">
                  <div className="flex gap-3 text-sm">
                    <Skeleton className="h-4 w-24 shrink-0 rounded-sm" />
                    <Skeleton className="h-4 min-w-0 flex-1 rounded-sm" />
                  </div>
                  {Array.from({ length: 10 }, (_, index) => (
                    <div key={index} className="flex gap-3 text-sm">
                      <Skeleton className="h-4 w-24 shrink-0 rounded-sm" />
                      <div className="flex min-w-0 flex-1 items-center gap-1">
                        <Skeleton
                          className={
                            index % 2 === 0 ? 'h-4 w-3/5 rounded-sm' : 'h-4 w-4/5 rounded-sm'
                          }
                        />
                        {index === 2 && <Skeleton className="size-7 shrink-0 rounded-lg" />}
                      </div>
                    </div>
                  ))}
                </div>
              </section>
              <div className="border-t border-border" />
              <section>
                <Skeleton className="mb-2 h-5 w-16 rounded-sm" />
                <div className="flex flex-col gap-2">
                  {Array.from({ length: 3 }, (_, index) => (
                    <div key={index} className="flex items-center gap-2">
                      <Skeleton className="h-5 w-16 shrink-0 rounded-full" />
                      <Skeleton className="h-4 min-w-0 flex-1 rounded-sm" />
                      <Skeleton className="size-7 shrink-0 rounded-lg" />
                    </div>
                  ))}
                </div>
              </section>
              <div className="border-t border-border" />
              <section>
                <div className="mb-2 flex items-center justify-between gap-2">
                  <Skeleton className="h-5 w-16 rounded-sm" />
                  <Skeleton className="size-8 rounded-lg" />
                </div>
                <div className="flex flex-col gap-2">
                  {Array.from({ length: 2 }, (_, index) => (
                    <div key={index} className="flex items-center gap-2">
                      <Skeleton className="h-5 w-16 shrink-0 rounded-full" />
                      <Skeleton className="h-4 min-w-0 flex-1 rounded-sm" />
                      <Skeleton className="size-7 shrink-0 rounded-lg" />
                    </div>
                  ))}
                </div>
                <Skeleton className="mt-2 h-8 w-32 rounded-lg" />
              </section>
              {canDelete && <Skeleton className="h-9 w-32 rounded-lg" />}
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
                <Badge variant="outline">
                  {t(`common.source.${data.source}`, { defaultValue: data.source })}
                </Badge>
              </div>

              <section>
                <h3 className="mb-2 text-base font-medium">{t('gallery.detail.basic')}</h3>
                <dl className="flex flex-col gap-2">
                  <FieldRow label={t('gallery.detail.filename')}>{data.original_filename}</FieldRow>
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
                  <FieldRow label={t('gallery.detail.uploader')}>
                    {data.user_email === '' ? (
                      '-'
                    ) : onFilterUser ? (
                      <button
                        type="button"
                        className="text-primary text-left hover:underline"
                        onClick={() => onFilterUser(data.user_email)}
                      >
                        {data.user_email}
                      </button>
                    ) : (
                      data.user_email
                    )}
                  </FieldRow>
                  <FieldRow label={t('gallery.detail.source')}>
                    {t(`common.source.${data.source}`, { defaultValue: data.source })}
                  </FieldRow>
                  <FieldRow label={t('gallery.detail.clientIp')}>{data.client_ip ?? '-'}</FieldRow>
                  <FieldRow label={t('gallery.detail.createdAt')}>
                    {formatDateTime(data.created_at)}
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
                <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
                  <h3 className="text-base font-medium">{t('gallery.detail.urls')}</h3>
                  <CopyFormatToggle value={copyFormat} onChange={setCopyFormat} />
                </div>
                <UrlList
                  urls={
                    data.urls.length > 0
                      ? data.urls
                      : [{ alias: t('common.defaultAlias'), url: data.url, is_default: true }]
                  }
                  filename={data.original_filename}
                  copyFormat={copyFormat}
                />
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
                    {t('gallery.detail.deleteImage')}
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
