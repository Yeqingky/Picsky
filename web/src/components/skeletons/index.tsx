import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { cn } from '../../lib/utils';
import { Card, CardContent } from '../ui/card';
import { Skeleton } from '../ui/skeleton';
import { useMasonryColumns } from '../MasonryGrid';

/**
 * 统一骨架基元 (docs/webui.md 第 3 节):
 * - 全部走 ui/skeleton 的扫光动画 (reduced-motion 兜底见 index.css);
 * - 容器统一 aria-busy="true"。
 */
export function SkeletonSurface({
  children,
  className,
  label,
  dense,
}: {
  children: ReactNode;
  className?: string;
  label?: string;
  dense?: boolean;
}) {
  return (
    <div aria-busy="true" aria-label={label} className={cn('w-full', !dense && 'p-0', className)}>
      {children}
    </div>
  );
}

export function TextLineSkeleton({
  width = '100%',
  height = 16,
}: {
  width?: number | string;
  height?: number;
}) {
  return <Skeleton className="rounded-sm" style={{ width, height }} />;
}

export function TextLinesSkeleton({
  lines = 3,
  lastWidth = '60%',
}: {
  lines?: number;
  lastWidth?: string;
}) {
  return (
    <div className="flex flex-col gap-1">
      {Array.from({ length: lines }, (_, index) => (
        <Skeleton
          key={index}
          className="h-4 rounded-sm"
          style={{ width: index === lines - 1 ? lastWidth : '100%' }}
        />
      ))}
    </div>
  );
}

export function PageHeaderSkeleton() {
  const { t } = useTranslation();
  return (
    <SkeletonSurface label={t('common.skeleton.pageHeader')} className="mb-4">
      <div className="flex flex-col gap-1">
        <Skeleton className="h-9 w-[220px] rounded-sm" />
        <Skeleton className="h-5 w-[360px] rounded-sm" />
      </div>
    </SkeletonSurface>
  );
}

/** 首屏整页骨架: 标题 + 若干卡片块 */
export function PageSkeleton({ blocks = 3 }: { blocks?: number }) {
  const { t } = useTranslation();
  return (
    <SkeletonSurface label={t('common.skeleton.page')}>
      <PageHeaderSkeleton />
      <div className="flex flex-col gap-4">
        {Array.from({ length: blocks }, (_, index) => (
          <Card key={index}>
            <CardContent>
              <div className="flex flex-col gap-2">
                <Skeleton className="h-6 w-40 rounded-sm" />
                <Skeleton className="h-[120px] rounded-lg" />
              </div>
            </CardContent>
          </Card>
        ))}
      </div>
    </SkeletonSurface>
  );
}

/** 表格骨架: 行高固定, 与真实 DataGrid 尺寸一致 */
export function TableSkeleton({ rows = 8, columns = 5 }: { rows?: number; columns?: number }) {
  const { t } = useTranslation();
  return (
    <SkeletonSurface label={t('common.skeleton.table')}>
      <div className="border-border overflow-hidden rounded-lg border">
        <div className="bg-muted flex gap-4 p-3">
          {Array.from({ length: columns }, (_, index) => (
            <Skeleton key={index} className="h-6 flex-1 rounded-sm" />
          ))}
        </div>
        {Array.from({ length: rows }, (_, rowIndex) => (
          <div key={rowIndex} className="border-border flex h-13 items-center gap-4 border-t p-3">
            {Array.from({ length: columns }, (_, columnIndex) => (
              <Skeleton
                key={columnIndex}
                className={cn('h-5 rounded-sm', columnIndex === 0 ? 'flex-[1.4]' : 'flex-1')}
              />
            ))}
          </div>
        ))}
      </div>
    </SkeletonSurface>
  );
}

/** 数字卡骨架 (管理概览) */
export function NumberCardsSkeleton({ count = 4 }: { count?: number }) {
  const { t } = useTranslation();
  return (
    <SkeletonSurface label={t('common.skeleton.stats')}>
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 md:grid-cols-4">
        {Array.from({ length: count }, (_, index) => (
          <Card key={index} className="h-[108px]">
            <CardContent>
              <div className="flex flex-col gap-2">
                <Skeleton className="h-4 w-1/2 rounded-sm" />
                <Skeleton className="h-8 w-[70%] rounded-sm" />
              </div>
            </CardContent>
          </Card>
        ))}
      </div>
    </SkeletonSurface>
  );
}

export function ChartSkeleton({ height = 260 }: { height?: number }) {
  const { t } = useTranslation();
  return (
    <SkeletonSurface label={t('common.skeleton.chart')}>
      <div className="flex flex-col gap-2">
        <Skeleton className="h-6 w-[140px] rounded-sm" />
        <Skeleton className="rounded-lg" style={{ height }} />
      </div>
    </SkeletonSurface>
  );
}

/** 表单/设置分组骨架 */
export function FormSkeleton({
  fields = 4,
  grouped = false,
}: {
  fields?: number;
  grouped?: boolean;
}) {
  const { t } = useTranslation();
  const body = (
    <div className="flex flex-col gap-4">
      {Array.from({ length: fields }, (_, index) => (
        <div key={index} className="flex flex-col gap-1">
          <Skeleton className="h-4 w-[120px] rounded-sm" />
          <Skeleton className="h-10 rounded-lg" />
        </div>
      ))}
    </div>
  );

  if (!grouped) {
    return <SkeletonSurface label={t('common.skeleton.form')}>{body}</SkeletonSurface>;
  }

  return (
    <SkeletonSurface label={t('common.skeleton.settings')}>
      <div className="flex flex-col gap-4">
        {Array.from({ length: Math.max(1, Math.ceil(fields / 2)) }, (_, index) => (
          <Card key={index}>
            <CardContent>
              <Skeleton className="h-6 w-40 rounded-sm" />
              <div className="mt-4">{body}</div>
            </CardContent>
          </Card>
        ))}
      </div>
    </SkeletonSurface>
  );
}

/** 列表行骨架 (队列/日志/密钥) */
export function ListRowsSkeleton({ rows = 5 }: { rows?: number }) {
  const { t } = useTranslation();
  return (
    <SkeletonSurface label={t('common.skeleton.list')}>
      <div className="flex flex-col gap-2">
        {Array.from({ length: rows }, (_, index) => (
          <div key={index} className="border-border flex items-center gap-4 rounded-lg border p-3">
            <Skeleton className="size-9 rounded-full" />
            <div className="flex flex-1 flex-col gap-1">
              <Skeleton className="h-4 w-2/5 rounded-sm" />
              <Skeleton className="h-4 w-[65%] rounded-sm" />
            </div>
            <Skeleton className="h-7 w-[72px] rounded-lg" />
          </div>
        ))}
      </div>
    </SkeletonSurface>
  );
}

/** 登录页首屏骨架 */
export function AuthCardSkeleton() {
  const { t } = useTranslation();
  return (
    <SkeletonSurface label={t('common.skeleton.authCard')}>
      <div className="flex w-full max-w-[420px] flex-col gap-4">
        <Skeleton className="h-9 w-[160px] rounded-sm" />
        <Skeleton className="h-14 rounded-lg" />
        <Skeleton className="h-14 rounded-lg" />
        <Skeleton className="h-10 rounded-lg" />
      </div>
    </SkeletonSurface>
  );
}

/** 瀑布流骨架: 与真实卡片同尺寸 (按列宽与固定宽高比占位), 避免加载完成后的跳动 */
export function MasonrySkeleton({ count = 12 }: { count?: number }) {
  const { t } = useTranslation();
  const columns = useMasonryColumns();
  const ratios = [1, 1.4, 0.75, 1.2, 1, 1.5];

  return (
    <SkeletonSurface label={t('common.skeleton.gallery')}>
      <div
        className="grid gap-3"
        style={{ gridTemplateColumns: `repeat(${columns}, minmax(0, 1fr))` }}
      >
        {Array.from({ length: count }, (_, index) => (
          <Skeleton
            key={index}
            className="rounded-lg"
            style={{ paddingTop: `${(ratios[index % ratios.length] ?? 1) * 100}%` }}
          />
        ))}
      </div>
    </SkeletonSurface>
  );
}
