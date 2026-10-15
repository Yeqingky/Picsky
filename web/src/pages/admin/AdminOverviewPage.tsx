import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { Bar, BarChart, CartesianGrid, Pie, PieChart, XAxis, YAxis } from 'recharts';
import { adminStatsApi } from '../../api';
import { PageHeader } from '../../components/PageHeader';
import { EmptyState, ErrorState } from '../../components/States';
import { ChartSkeleton, NumberCardsSkeleton } from '../../components/skeletons';
import { Card, CardContent } from '../../components/ui/card';
import {
  ChartContainer,
  ChartLegend,
  ChartLegendContent,
  ChartTooltip,
  ChartTooltipContent,
} from '../../components/ui/chart';
import type { ChartConfig } from '../../components/ui/chart';
import { formatBytes, formatNumber } from '../../utils/format';

const CHART_HEIGHT = 280;

/** 图表调色板: 统一使用设计令牌, 依次取用 (超出则循环) */
const CHART_COLORS = [
  'var(--chart-1)',
  'var(--chart-2)',
  'var(--chart-3)',
  'var(--chart-4)',
  'var(--chart-5)',
] as const;

export default function AdminOverviewPage() {
  const { t } = useTranslation();
  const statsQuery = useQuery({
    queryKey: ['admin', 'stats'],
    queryFn: () => adminStatsApi.get(),
  });

  const header = (
    <PageHeader title={t('nav.adminOverview')} description={t('admin.overview.description')} />
  );

  if (statsQuery.isPending) {
    return (
      <>
        {header}
        <NumberCardsSkeleton count={4} />
        <div className="mt-4 grid grid-cols-1 gap-4 md:grid-cols-2">
          <Card>
            <CardContent>
              <ChartSkeleton height={CHART_HEIGHT} />
            </CardContent>
          </Card>
          <Card>
            <CardContent>
              <ChartSkeleton height={CHART_HEIGHT} variant="donut" />
            </CardContent>
          </Card>
        </div>
      </>
    );
  }

  if (statsQuery.isError) {
    return (
      <>
        {header}
        <ErrorState
          message={statsQuery.error instanceof Error ? statsQuery.error.message : undefined}
          onRetry={() => void statsQuery.refetch()}
        />
      </>
    );
  }

  const stats = statsQuery.data;
  const cards = [
    { label: t('admin.overview.card.imageCount'), value: formatNumber(stats.image_count) },
    { label: t('admin.overview.card.usedBytes'), value: formatBytes(stats.used_bytes) },
    { label: t('admin.overview.card.userCount'), value: formatNumber(stats.user_count) },
    { label: t('admin.overview.card.todayUploads'), value: formatNumber(stats.today_uploads) },
  ];

  /** 按格式分布: 单序列柱状图, 序列名即图例与提示标签 */
  const formatConfig: ChartConfig = {
    count: {
      label: t('admin.overview.byFormat.series'),
      color: CHART_COLORS[0],
    },
  };

  const formatData = stats.by_format.map((item) => ({
    format: item.format,
    count: item.count,
  }));

  /** 按策略分布: 环形图, 每个策略一个扇区; key 用策略 ID 保证唯一 */
  const policyData = stats.by_policy.map((item, index) => ({
    key: String(item.policy_id),
    name: item.policy_name,
    value: item.count,
    fill: CHART_COLORS[index % CHART_COLORS.length],
  }));

  const policyConfig: ChartConfig = Object.fromEntries(
    stats.by_policy.map((item, index) => [
      String(item.policy_id),
      { label: item.policy_name, color: CHART_COLORS[index % CHART_COLORS.length] },
    ]),
  );

  return (
    <>
      {header}
      <div className="flex flex-col gap-4">
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 md:grid-cols-4">
          {cards.map((card) => (
            <Card key={card.label} className="h-full">
              <CardContent>
                <p className="text-muted-foreground text-sm">{card.label}</p>
                <p className="font-heading mt-1 text-2xl font-semibold">{card.value}</p>
              </CardContent>
            </Card>
          ))}
        </div>

        <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
          <Card className="h-full">
            <CardContent>
              <h2 className="font-heading mb-3 text-base font-semibold">
                {t('admin.overview.byFormat.title')}
              </h2>
              {stats.by_format.length === 0 ? (
                <EmptyState title={t('admin.overview.byFormat.empty')} />
              ) : (
                <ChartContainer
                  config={formatConfig}
                  className="aspect-auto w-full"
                  style={{ height: CHART_HEIGHT }}
                >
                  <BarChart data={formatData}>
                    <CartesianGrid vertical={false} />
                    <XAxis dataKey="format" tickLine={false} axisLine={false} tickMargin={8} />
                    <YAxis
                      tickLine={false}
                      axisLine={false}
                      tickMargin={8}
                      allowDecimals={false}
                      tickFormatter={(value: number) => formatNumber(value)}
                    />
                    <ChartTooltip content={<ChartTooltipContent />} />
                    <ChartLegend content={<ChartLegendContent />} />
                    <Bar dataKey="count" fill="var(--color-count)" />
                  </BarChart>
                </ChartContainer>
              )}
            </CardContent>
          </Card>

          <Card className="h-full">
            <CardContent>
              <h2 className="font-heading mb-3 text-base font-semibold">
                {t('admin.overview.byPolicy.title')}
              </h2>
              {stats.by_policy.length === 0 ? (
                <EmptyState title={t('admin.overview.byPolicy.empty')} />
              ) : (
                <ChartContainer
                  config={policyConfig}
                  className="aspect-auto w-full"
                  style={{ height: CHART_HEIGHT }}
                >
                  <PieChart>
                    <ChartTooltip cursor={false} content={<ChartTooltipContent nameKey="key" />} />
                    <ChartLegend content={<ChartLegendContent nameKey="key" />} />
                    <Pie
                      data={policyData}
                      dataKey="value"
                      nameKey="key"
                      innerRadius={48}
                      label={{
                        position: 'inside',
                        // 扇区颜色是中深色, 用背景色文字保证亮/暗两种主题下都可读
                        fill: 'var(--background)',
                        formatter: (value) => formatNumber(Number(value)),
                      }}
                    />
                  </PieChart>
                </ChartContainer>
              )}
            </CardContent>
          </Card>
        </div>
      </div>
    </>
  );
}
