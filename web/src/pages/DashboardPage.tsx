import type { ReactNode } from 'react';
import { Link, useNavigate } from 'react-router';
import {
  CloudUpload,
  HardDrive,
  Images,
  KeyRound,
  Mail,
  Server,
  ShieldCheck,
  User,
} from 'lucide-react';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { imagesApi, policiesApi } from '../api/images';
import { keysApi } from '../api/keys';
import { useAuth } from '../hooks/useAuth';
import { useDelayedLoading } from '../hooks/useDelayedLoading';
import { PageHeader } from '../components/PageHeader';
import { EmptyState, ErrorState } from '../components/States';
import {
  FormSkeleton,
  ListRowsSkeleton,
  SkeletonSurface,
  TextLinesSkeleton,
} from '../components/skeletons';
import { Badge } from '../components/ui/badge';
import { Button } from '../components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '../components/ui/card';
import { formatBytes, formatNumber, formatRelative } from '../utils/format';

const RECENT_LIMIT = 5;
const RING_RADIUS = 52;
const RING_WIDTH = 10;

/** 配额使用环: 百分比居中, 上限不限时为占位符 */
function UsageRing({ percent, label }: { percent: number | null; label: string }) {
  const circumference = 2 * Math.PI * RING_RADIUS;
  const value = percent ?? 0;
  return (
    <div className="relative size-32 shrink-0">
      <svg viewBox="0 0 128 128" className="size-32 -rotate-90" aria-hidden="true">
        <circle
          cx="64"
          cy="64"
          r={RING_RADIUS}
          fill="none"
          strokeWidth={RING_WIDTH}
          className="stroke-secondary"
        />
        <circle
          cx="64"
          cy="64"
          r={RING_RADIUS}
          fill="none"
          strokeWidth={RING_WIDTH}
          strokeLinecap="round"
          strokeDasharray={circumference}
          strokeDashoffset={circumference * (1 - value / 100)}
          className="stroke-primary transition-[stroke-dashoffset] duration-500"
        />
      </svg>
      <div className="absolute inset-0 flex flex-col items-center justify-center">
        <span className="font-heading text-xl font-semibold">
          {percent === null ? '—' : `${Math.round(value)}%`}
        </span>
        <span className="text-muted-foreground mt-0.5 text-xs">{label}</span>
      </div>
    </div>
  );
}

/** 概览数字格: 图标 + 名称 + 数值 */
function StatTile({ icon, label, value }: { icon: ReactNode; label: string; value: string }) {
  return (
    <div className="flex items-start gap-3">
      <span className="text-primary mt-0.5 shrink-0 [&_svg]:size-4">{icon}</span>
      <div className="min-w-0">
        <p className="text-muted-foreground text-xs">{label}</p>
        <p className="font-heading mt-0.5 truncate text-lg font-semibold">{value}</p>
      </div>
    </div>
  );
}

function InfoRow({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex min-w-0 items-center gap-2">
      <span className="text-muted-foreground w-20 shrink-0 text-xs">{label}</span>
      <span className="min-w-0 text-sm break-all">{children}</span>
    </div>
  );
}

/** 仪表盘 (/): 聚合账号, 配额, 最近上传与已有路由入口 */
export default function DashboardPage() {
  const { t } = useTranslation();
  const { user, isLoading, refresh, hasPermission } = useAuth();
  const navigate = useNavigate();
  const canManageKeys = hasPermission('manage_keys');

  const imagesQuery = useQuery({
    queryKey: ['images', 'dashboard'],
    queryFn: () => imagesApi.list({ page: 1, page_size: RECENT_LIMIT }),
    enabled: user !== null,
  });
  const policiesQuery = useQuery({
    queryKey: ['policies', 'mine'],
    queryFn: policiesApi.listMine,
    enabled: user !== null,
  });
  const keysQuery = useQuery({
    queryKey: ['keys'],
    queryFn: keysApi.list,
    enabled: canManageKeys,
  });

  // 局部刷新走延迟骨架 (docs/webui.md 第 3 节)
  const showStatsSkeleton = useDelayedLoading(
    policiesQuery.isPending || (canManageKeys && keysQuery.isPending),
  );
  const showRecentSkeleton = useDelayedLoading(imagesQuery.isPending);

  if (isLoading) return <FormSkeleton fields={4} grouped />;
  if (!user) return <ErrorState onRetry={() => void refresh()} />;

  const quota = user.quota;
  const isByteQuota = quota.mode === 'bytes';
  const formatQuota = (value: number) => (isByteQuota ? formatBytes(value) : formatNumber(value));
  const limit = quota.limit;
  const percent = limit !== null && limit > 0 ? Math.min(100, (quota.used / limit) * 100) : null;
  const remaining = limit === null ? null : Math.max(0, limit - quota.used);
  const storedImages = imagesQuery.data ? formatNumber(imagesQuery.data.total) : '—';

  // 未设置存储限额时展示已存储的图片数量 (docs/webui.md 第 5 节)
  const usageValue = remaining === null ? storedImages : formatQuota(remaining);
  const usageSuffix =
    remaining === null ? t('dashboard.usage.imagesUnit') : t('dashboard.usage.available');
  const usageDetail =
    remaining === null
      ? t('dashboard.usage.noQuota')
      : t('dashboard.usage.detail', {
          used: formatQuota(quota.used),
          total: formatQuota(limit ?? 0),
        });

  const policies = policiesQuery.data ?? [];
  const activeKeys = keysQuery.data?.filter((key) => key.revoked_at === null).length;
  const defaultPolicy = policies.find((policy) => policy.id === user.default_policy_id);
  const recent = imagesQuery.data?.items ?? [];

  // 骨架格数与真实数字格一致, 避免加载完成后跳动 (docs/webui.md 第 3 节)
  const tileCount = 4 + (remaining === null ? 0 : 1) + (canManageKeys ? 1 : 0);

  const tiles = [
    {
      key: 'assets',
      icon: <Images />,
      label: t('dashboard.stats.assetsLabel'),
      value: storedImages,
    },
    ...(remaining === null
      ? []
      : [
          {
            key: 'remaining',
            icon: <HardDrive />,
            label: t('dashboard.stats.remainingLabel'),
            value: `${usageValue} ${usageSuffix}`,
          },
        ]),
    {
      key: 'policies',
      icon: <Server />,
      label: t('dashboard.stats.policiesLabel'),
      value: policiesQuery.data ? formatNumber(policies.length) : '—',
    },
    ...(canManageKeys
      ? [
          {
            key: 'keys',
            icon: <KeyRound />,
            label: t('dashboard.stats.keysLabel'),
            value: activeKeys === undefined ? '—' : formatNumber(activeKeys),
          },
        ]
      : []),
    {
      key: 'email',
      icon: <Mail />,
      label: t('dashboard.stats.emailLabel'),
      value: user.email_verified ? t('dashboard.stats.verified') : t('dashboard.stats.unverified'),
    },
    {
      key: 'totp',
      icon: <ShieldCheck />,
      label: t('dashboard.stats.totpLabel'),
      value: user.totp_enabled ? t('dashboard.stats.enabled') : t('dashboard.stats.disabled'),
    },
  ];

  return (
    <div>
      <PageHeader
        title={t('dashboard.greeting', { name: user.nickname || user.username })}
        description={t('dashboard.description')}
        actions={
          <>
            <Button onClick={() => navigate('/upload')}>
              <CloudUpload className="size-4" />
              {t('dashboard.uploadNow')}
            </Button>
            <Button variant="outline" asChild>
              <Link to="/gallery">
                <Images className="size-4" />
                {t('dashboard.galleryShortcut', {
                  total: imagesQuery.data ? formatNumber(imagesQuery.data.total) : '—',
                })}
              </Link>
            </Button>
          </>
        }
      />

      <div className="flex flex-col gap-4">
        <div className="grid grid-cols-1 gap-4 lg:grid-cols-[minmax(0,340px)_minmax(0,1fr)]">
          <Card>
            <CardHeader>
              <CardTitle>{t('dashboard.usage.title')}</CardTitle>
            </CardHeader>
            <CardContent className="flex flex-col items-center gap-4 sm:flex-row">
              {remaining !== null && (
                <UsageRing
                  percent={percent}
                  label={
                    isByteQuota ? t('dashboard.usage.ringBytes') : t('dashboard.usage.ringCount')
                  }
                />
              )}
              <div className="min-w-0 self-stretch">
                <p className="font-heading flex flex-wrap items-baseline gap-x-1.5 text-2xl font-semibold tabular-nums">
                  <span className="whitespace-nowrap">{usageValue}</span>
                  <span className="text-muted-foreground text-base font-normal">{usageSuffix}</span>
                </p>
                <p className="text-muted-foreground mt-1 text-sm">{usageDetail}</p>
              </div>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>{t('dashboard.stats.title')}</CardTitle>
            </CardHeader>
            <CardContent>
              {showStatsSkeleton ? (
                <SkeletonSurface label={t('common.skeleton.stats')}>
                  <div className="grid grid-cols-1 gap-x-6 gap-y-4 sm:grid-cols-2 xl:grid-cols-3">
                    {Array.from({ length: tileCount }, (_, index) => (
                      <TextLinesSkeleton key={index} lines={2} lastWidth="40%" />
                    ))}
                  </div>
                </SkeletonSurface>
              ) : (
                <div className="grid grid-cols-1 gap-x-6 gap-y-4 sm:grid-cols-2 xl:grid-cols-3">
                  {tiles.map((tile) => (
                    <StatTile
                      key={tile.key}
                      icon={tile.icon}
                      label={tile.label}
                      value={tile.value}
                    />
                  ))}
                </div>
              )}
            </CardContent>
          </Card>
        </div>

        <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
          <Card className="flex flex-col">
            <CardHeader className="flex flex-row items-center justify-between gap-3 [.border-b]:pb-0">
              <CardTitle>{t('dashboard.recent.title')}</CardTitle>
              <Link
                to="/gallery"
                className="text-muted-foreground hover:text-foreground text-sm transition-colors"
              >
                {t('dashboard.recent.viewAll')}
              </Link>
            </CardHeader>
            <CardContent className="flex flex-1 flex-col justify-center">
              {showRecentSkeleton ? (
                <ListRowsSkeleton rows={RECENT_LIMIT} />
              ) : imagesQuery.isError ? (
                <ErrorState onRetry={() => void imagesQuery.refetch()} />
              ) : recent.length === 0 ? (
                <EmptyState
                  title={t('dashboard.recent.emptyTitle')}
                  description={t('dashboard.recent.emptyDescription')}
                  action={
                    <Button size="sm" onClick={() => navigate('/upload')}>
                      {t('dashboard.recent.emptyAction')}
                    </Button>
                  }
                />
              ) : (
                <div className="divide-border divide-y">
                  {recent.map((item) => (
                    <div key={item.id} className="flex items-center justify-between gap-3 py-2">
                      <div className="min-w-0">
                        <p className="truncate text-sm">{item.original_filename}</p>
                        <p className="text-muted-foreground mt-0.5 text-xs">
                          {t('dashboard.recent.meta', {
                            time: formatRelative(item.created_at),
                            size: formatBytes(item.size),
                          })}
                        </p>
                      </div>
                      <Badge variant="outline" className="shrink-0">
                        {item.format}
                      </Badge>
                    </div>
                  ))}
                </div>
              )}
            </CardContent>
          </Card>

          <Card>
            <CardHeader className="flex flex-row items-center justify-between gap-3 [.border-b]:pb-0">
              <CardTitle>{t('dashboard.account.title')}</CardTitle>
              <Button variant="outline" size="sm" asChild>
                <Link to="/profile">
                  <User className="size-4" />
                  {t('dashboard.account.profile')}
                </Link>
              </Button>
            </CardHeader>
            <CardContent className="flex flex-col gap-3">
              <InfoRow label={t('dashboard.account.name')}>
                {user.nickname || user.username}
              </InfoRow>
              <InfoRow label={t('dashboard.account.email')}>
                <span className="inline-flex flex-wrap items-center gap-2">
                  {user.email}
                  <Badge
                    className={
                      user.email_verified
                        ? 'border-transparent bg-success/10 text-success'
                        : 'border-transparent bg-warning/10 text-warning'
                    }
                  >
                    {user.email_verified
                      ? t('dashboard.stats.verified')
                      : t('dashboard.stats.unverified')}
                  </Badge>
                </span>
              </InfoRow>
              <InfoRow label={t('dashboard.account.group')}>{user.group.name}</InfoRow>
              <InfoRow label={t('dashboard.account.policy')}>
                {defaultPolicy?.name ?? t('dashboard.account.policyUnset')}
              </InfoRow>
            </CardContent>
          </Card>
        </div>
      </div>
    </div>
  );
}
