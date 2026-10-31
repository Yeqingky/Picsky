import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { cn } from '../../lib/utils';
import { Card, CardContent, CardHeader } from '../ui/card';
import { Skeleton } from '../ui/skeleton';
import { MasonryGrid } from '../MasonryGrid';

/**
 * 统一骨架基元 (docs/webui.md 第 3 节):
 * - 全部走 ui/skeleton 的扫光动画 (reduced-motion 兜底见 index.css);
 * - 容器统一 aria-busy="true".
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

export function PageHeaderSkeleton({ actions = 0 }: { actions?: number }) {
  const { t } = useTranslation();
  return (
    <SkeletonSurface label={t('common.skeleton.pageHeader')} className="mb-6">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div className="min-w-0">
          <Skeleton className="h-7 w-[220px] rounded-sm sm:h-8" />
          <Skeleton className="mt-2 h-4 w-[360px] max-w-full rounded-sm" />
        </div>
        {actions > 0 && (
          <div className="flex shrink-0 flex-wrap items-center gap-2">
            {Array.from({ length: actions }, (_, index) => (
              <Skeleton
                key={index}
                className={cn('h-9 rounded-lg', index === 0 ? 'w-24' : 'w-32')}
              />
            ))}
          </div>
        )}
      </div>
    </SkeletonSurface>
  );
}

/** 路由分块加载的通用骨架: 页面操作, 筛选器, 表格与分页 */
export function PageSkeleton({ blocks = 3 }: { blocks?: number }) {
  const { t } = useTranslation();
  return (
    <SkeletonSurface label={t('common.skeleton.page')}>
      <PageHeaderSkeleton actions={1} />
      <div className="mb-4 flex flex-wrap items-end gap-3">
        {Array.from({ length: 2 }, (_, index) => (
          <div key={index} className="flex flex-col gap-1.5">
            <Skeleton className="h-4 w-24 rounded-sm" />
            <Skeleton className={cn('h-9 w-48 rounded-lg', index === 1 && 'w-40')} />
          </div>
        ))}
      </div>
      <div className="overflow-hidden rounded-lg border border-border bg-card">
        <div className="bg-muted/50 flex gap-4 px-3 py-3">
          {Array.from({ length: 6 }, (_, index) => (
            <Skeleton key={index} className="h-4 flex-1 rounded-sm" />
          ))}
        </div>
        {Array.from({ length: Math.max(5, blocks * 3) }, (_, rowIndex) => (
          <div key={rowIndex} className="flex h-13 items-center gap-4 border-t border-border px-3">
            {Array.from({ length: 6 }, (_, columnIndex) => (
              <Skeleton
                key={columnIndex}
                className={cn(
                  'h-4 rounded-sm',
                  (rowIndex + columnIndex) % 2 === 0 ? 'flex-[0.7]' : 'flex-1',
                )}
              />
            ))}
          </div>
        ))}
        <div className="flex items-center justify-between gap-3 border-t border-border px-3 py-2">
          <Skeleton className="h-4 w-32 rounded-sm" />
          <div className="flex items-center gap-2">
            <Skeleton className="size-8 rounded-lg" />
            <Skeleton className="h-4 w-16 rounded-sm" />
            <Skeleton className="size-8 rounded-lg" />
          </div>
        </div>
      </div>
    </SkeletonSurface>
  );
}
function ProfileSectionSkeleton({
  titleWidth,
  children,
}: {
  titleWidth: string;
  children: ReactNode;
}) {
  return (
    <Card>
      <CardHeader>
        <Skeleton className={cn('h-6 rounded-sm', titleWidth)} />
      </CardHeader>
      <CardContent className="flex flex-col gap-4">{children}</CardContent>
    </Card>
  );
}

/** 仪表盘首屏: 配额与统计卡, 最近上传和账户信息 */
export function DashboardSkeleton() {
  const { t } = useTranslation();
  return (
    <SkeletonSurface label={t('common.skeleton.page')}>
      <PageHeaderSkeleton actions={2} />
      <div className="flex flex-col gap-4">
        <div className="grid grid-cols-1 gap-4 lg:grid-cols-[minmax(0,340px)_minmax(0,1fr)]">
          <Card>
            <CardHeader>
              <Skeleton className="h-6 w-28 rounded-sm" />
            </CardHeader>
            <CardContent className="flex flex-col items-center gap-4 sm:flex-row">
              <div className="relative size-32 shrink-0 rounded-full border-[10px] border-muted">
                <div className="absolute inset-0 flex flex-col items-center justify-center gap-1">
                  <Skeleton className="h-5 w-12 rounded-sm" />
                  <Skeleton className="h-3 w-16 rounded-sm" />
                </div>
              </div>
              <div className="min-w-0 self-stretch">
                <Skeleton className="h-8 w-36 max-w-full rounded-sm" />
                <Skeleton className="mt-2 h-4 w-48 max-w-full rounded-sm" />
              </div>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <Skeleton className="h-6 w-32 rounded-sm" />
            </CardHeader>
            <CardContent>
              <div className="grid grid-cols-1 gap-x-6 gap-y-4 sm:grid-cols-2 xl:grid-cols-3">
                {Array.from({ length: 6 }, (_, index) => (
                  <div key={index} className="flex items-start gap-3">
                    <Skeleton className="mt-0.5 size-4 shrink-0 rounded-sm" />
                    <div className="min-w-0">
                      <Skeleton className="h-3 w-20 rounded-sm" />
                      <Skeleton className="mt-1.5 h-5 w-24 max-w-full rounded-sm" />
                    </div>
                  </div>
                ))}
              </div>
            </CardContent>
          </Card>
        </div>

        <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
          <Card className="flex flex-col">
            <CardHeader className="flex flex-row items-center justify-between gap-3 [.border-b]:pb-0">
              <Skeleton className="h-6 w-28 rounded-sm" />
              <Skeleton className="h-4 w-12 rounded-sm" />
            </CardHeader>
            <CardContent className="flex flex-1 flex-col justify-center">
              <RecentUploadsSkeleton rows={5} />
            </CardContent>
          </Card>
          <Card>
            <CardHeader className="flex flex-row items-center justify-between gap-3 [.border-b]:pb-0">
              <Skeleton className="h-6 w-28 rounded-sm" />
              <Skeleton className="h-8 w-24 rounded-lg" />
            </CardHeader>
            <CardContent className="flex flex-col gap-3">
              {Array.from({ length: 4 }, (_, index) => (
                <div key={index} className="flex min-w-0 items-center gap-2">
                  <Skeleton className="h-3 w-20 shrink-0 rounded-sm" />
                  <Skeleton className="h-4 w-32 max-w-full rounded-sm" />
                </div>
              ))}
            </CardContent>
          </Card>
        </div>
      </div>
    </SkeletonSurface>
  );
}

/** 个人设置首屏: 账户信息, 密码, 两步验证与偏好卡片 */
export function ProfileSkeleton() {
  const { t } = useTranslation();
  return (
    <SkeletonSurface label={t('common.skeleton.page')}>
      <PageHeaderSkeleton />
      <div className="flex flex-col gap-4">
        <ProfileSectionSkeleton titleWidth="w-24">
          <div className="flex min-w-0 items-center gap-2">
            <Skeleton className="h-4 w-20 shrink-0 rounded-sm" />
            <Skeleton className="h-9 w-full max-w-48 rounded-lg" />
            <Skeleton className="h-9 w-16 shrink-0 rounded-lg" />
          </div>
          {Array.from({ length: 4 }, (_, index) => (
            <div key={index} className="flex min-w-0 items-center gap-2">
              <Skeleton className="h-3 w-20 shrink-0 rounded-sm" />
              {index === 3 ? (
                <div className="min-w-0 flex-1">
                  <div className="mb-1 flex items-baseline justify-between gap-2">
                    <Skeleton className="h-4 w-20 rounded-sm" />
                    <Skeleton className="h-4 w-28 rounded-sm" />
                  </div>
                  <Skeleton className="h-1.5 w-full rounded-full" />
                </div>
              ) : (
                <Skeleton className="h-4 w-40 max-w-full rounded-sm" />
              )}
              {index === 1 && <Skeleton className="h-5 w-20 shrink-0 rounded-full" />}
            </div>
          ))}
        </ProfileSectionSkeleton>

        <ProfileSectionSkeleton titleWidth="w-24">
          <Skeleton className="h-4 w-72 max-w-full rounded-sm" />
          <div className="flex max-w-md flex-col gap-4">
            {Array.from({ length: 3 }, (_, index) => (
              <div key={index} className="flex flex-col gap-2">
                <Skeleton className="h-4 w-28 rounded-sm" />
                <Skeleton className="h-10 w-full rounded-lg" />
              </div>
            ))}
            <Skeleton className="h-9 w-16 rounded-lg" />
          </div>
        </ProfileSectionSkeleton>

        <ProfileSectionSkeleton titleWidth="w-24">
          <div className="flex flex-wrap items-center gap-2">
            <Skeleton className="h-4 w-64 max-w-full rounded-sm" />
            <Skeleton className="h-5 w-20 rounded-full" />
          </div>
          <Skeleton className="h-9 w-28 rounded-lg" />
        </ProfileSectionSkeleton>

        <ProfileSectionSkeleton titleWidth="w-16">
          <div className="flex flex-col gap-2">
            <Skeleton className="h-4 w-16 rounded-sm" />
            <div className="flex flex-row flex-wrap gap-4">
              {Array.from({ length: 3 }, (_, index) => (
                <div key={index} className="flex items-center gap-2">
                  <Skeleton className="size-4 rounded-full" />
                  <Skeleton className="h-4 w-12 rounded-sm" />
                </div>
              ))}
            </div>
          </div>
          <div className="flex flex-col gap-1">
            <Skeleton className="h-4 w-16 rounded-sm" />
            <Skeleton className="h-4 w-24 rounded-sm" />
            <Skeleton className="h-3 w-48 max-w-full rounded-sm" />
          </div>
        </ProfileSectionSkeleton>
      </div>
    </SkeletonSurface>
  );
}

/** 存储策略编辑首屏: 操作栏, 标签页与基础设置表单 */
export function PolicyEditorSkeleton({ tabs = 5 }: { tabs?: number }) {
  const { t } = useTranslation();
  return (
    <SkeletonSurface label={t('common.skeleton.form')}>
      <div className="flex flex-wrap gap-2 border-b border-border pb-[5px]">
        {Array.from({ length: tabs }, (_, index) => (
          <Skeleton key={index} className="h-9 w-24 rounded-sm" />
        ))}
      </div>
      <div className="mt-6 flex max-w-3xl flex-col gap-5">
        <Skeleton className="h-4 w-96 max-w-full rounded-sm" />
        <div className="flex flex-col gap-5">
          {['w-full sm:max-w-80', 'w-full sm:max-w-60', 'w-full sm:max-w-40'].map(
            (width, index) => (
              <div key={index} className={cn('flex flex-col gap-1.5', width)}>
                <Skeleton className="h-4 w-24 rounded-sm" />
                <Skeleton className="h-10 w-full rounded-lg" />
                {index === 2 && <Skeleton className="h-3 w-56 max-w-full rounded-sm" />}
              </div>
            ),
          )}
          <div className="flex flex-col gap-1.5">
            <div className="flex items-center gap-2">
              <Skeleton className="size-4 rounded-sm" />
              <Skeleton className="h-4 w-24 rounded-sm" />
            </div>
            <Skeleton className="ml-6 h-3 w-64 max-w-full rounded-sm" />
          </div>
        </div>
      </div>
    </SkeletonSurface>
  );
}

/** 管理概览数字卡: 标签与数值尺寸贴合实际卡片 */
export function NumberCardsSkeleton({ count = 4 }: { count?: number }) {
  const { t } = useTranslation();
  return (
    <SkeletonSurface label={t('common.skeleton.stats')}>
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 md:grid-cols-4">
        {Array.from({ length: count }, (_, index) => (
          <Card key={index} className="h-full">
            <CardContent>
              <Skeleton className="h-4 w-1/2 rounded-sm" />
              <Skeleton className="mt-1 h-7 w-[70%] rounded-sm" />
            </CardContent>
          </Card>
        ))}
      </div>
    </SkeletonSurface>
  );
}

export function ChartSkeleton({
  height = 260,
  variant = 'bar',
}: {
  height?: number;
  variant?: 'bar' | 'donut';
}) {
  const { t } = useTranslation();
  return (
    <SkeletonSurface label={t('common.skeleton.chart')}>
      <Skeleton className="mb-3 h-5 w-36 rounded-sm" />
      {variant === 'bar' ? (
        <div className="relative w-full" style={{ height }}>
          <div className="absolute inset-y-2 right-2 left-10 flex flex-col justify-between border-b border-l border-border py-1">
            {Array.from({ length: 4 }, (_, index) => (
              <Skeleton key={index} className="h-px w-full rounded-none" />
            ))}
          </div>
          <div className="absolute inset-x-12 top-4 bottom-10 flex items-end justify-around gap-3">
            {[42, 68, 52, 84, 60, 74].map((size, index) => (
              <Skeleton
                key={index}
                className="w-full max-w-8 rounded-t-sm rounded-b-none"
                style={{ height: `${size}%` }}
              />
            ))}
          </div>
          <div className="absolute inset-x-12 bottom-7 flex justify-around">
            {Array.from({ length: 6 }, (_, index) => (
              <Skeleton key={index} className="h-3 w-8 rounded-sm" />
            ))}
          </div>
          <div className="absolute inset-x-12 bottom-0 flex items-center justify-center gap-1.5">
            <Skeleton className="size-2 rounded-sm" />
            <Skeleton className="h-3 w-12 rounded-sm" />
          </div>
        </div>
      ) : (
        <div className="flex flex-col items-center justify-center gap-4" style={{ height }}>
          <div className="relative size-40 shrink-0 rounded-full border-[18px] border-muted">
            <Skeleton className="absolute inset-0 m-auto h-8 w-12 rounded-sm" />
          </div>
          <div className="flex flex-wrap items-center justify-center gap-x-4 gap-y-2">
            {Array.from({ length: 4 }, (_, index) => (
              <div key={index} className="flex items-center gap-1.5">
                <Skeleton className="size-2 rounded-sm" />
                <Skeleton className="h-3 w-16 rounded-sm" />
              </div>
            ))}
          </div>
        </div>
      )}
    </SkeletonSurface>
  );
}

/** 仪表盘最近上传行: 文件名, 时间/大小与格式徽标 */
export function RecentUploadsSkeleton({ rows = 5 }: { rows?: number }) {
  const { t } = useTranslation();
  return (
    <SkeletonSurface label={t('common.skeleton.list')}>
      <div className="divide-y divide-border">
        {Array.from({ length: rows }, (_, index) => (
          <div key={index} className="flex items-center justify-between gap-3 py-2">
            <div className="min-w-0 flex-1">
              <Skeleton className="h-4 w-3/5 rounded-sm" />
              <Skeleton className="mt-1.5 h-3 w-2/5 rounded-sm" />
            </div>
            <Skeleton className="h-5 w-12 shrink-0 rounded-full" />
          </div>
        ))}
      </div>
    </SkeletonSurface>
  );
}

/** 登录, 注册与找回密码表单骨架; AuthLayout 提供站点标题与外层卡片 */
export function AuthCardSkeleton({
  fields = 2,
  captcha = false,
}: {
  fields?: number;
  captcha?: boolean;
} = {}) {
  const { t } = useTranslation();
  return (
    <SkeletonSurface label={t('common.skeleton.authCard')}>
      <div className="flex w-full flex-col gap-4">
        <div className="flex flex-col gap-1">
          <Skeleton className="h-7 w-40 rounded-sm" />
          <Skeleton className="h-4 w-64 max-w-full rounded-sm" />
        </div>
        {Array.from({ length: fields }, (_, index) => (
          <div key={index} className="flex flex-col gap-2">
            <Skeleton className="h-4 w-24 rounded-sm" />
            <Skeleton className="h-10 w-full rounded-lg" />
            {fields > 2 && index === 0 && <Skeleton className="h-3 w-48 max-w-full rounded-sm" />}
          </div>
        ))}
        {captcha && <Skeleton className="h-16 w-full rounded-lg" />}
        <Skeleton className="h-11 w-full rounded-lg" />
        <Skeleton className="mx-auto h-4 w-48 max-w-full rounded-sm" />
      </div>
    </SkeletonSurface>
  );
}

/** 瀑布流骨架复用真实 CSS grid 行跨度与卡片宽高比 */
export function MasonrySkeleton({ count = 12 }: { count?: number }) {
  const { t } = useTranslation();
  const ratios = [1, 1.4, 0.75, 1.2, 1, 1.5];
  const items = Array.from({ length: count }, (_, index) => ({
    index,
    ratio: ratios[index % ratios.length] ?? 1,
  }));

  return (
    <SkeletonSurface label={t('common.skeleton.gallery')}>
      <MasonryGrid
        items={items}
        getKey={(item) => item.index}
        getRatio={(item) => ({ width: 1, height: item.ratio })}
        renderItem={(_item, { height }) => (
          <div
            className="relative h-full overflow-hidden rounded-lg border border-border"
            style={{ height }}
          >
            <Skeleton className="absolute inset-0 h-full w-full rounded-lg" />
            <div className="pointer-events-none absolute inset-x-1 top-1 flex justify-end">
              <Skeleton className="h-5 w-12 rounded-full" />
            </div>
          </div>
        )}
      />
    </SkeletonSurface>
  );
}
