import { useTranslation } from 'react-i18next';
import type { QuotaInfo } from '../api/types';
import { formatBytes, formatNumber } from '../utils/format';
import { Progress } from './ui/progress';

/** 配额展示: 数量或空间 (docs/permissions.md 第 4 节) */
export function QuotaBar({ quota, compact = false }: { quota: QuotaInfo; compact?: boolean }) {
  const { t } = useTranslation();

  if (quota.mode === null) {
    return <p className="text-muted-foreground text-sm">{t('common.quota.empty')}</p>;
  }

  const isBytes = quota.mode === 'bytes';
  const formatValue = (value: number) => (isBytes ? formatBytes(value) : formatNumber(value));
  const limit = quota.limit;
  const percent = limit && limit > 0 ? Math.min(100, (quota.used / limit) * 100) : 0;

  return (
    <div className="w-full">
      <div className="mb-1 flex items-baseline justify-between gap-2">
        <p className="text-muted-foreground text-sm">
          {t(`common.quota.mode${isBytes ? 'Bytes' : 'Count'}`)}
        </p>
        <p className="text-sm">
          {formatValue(quota.used)}
          {limit === null ? (
            <span className="text-muted-foreground">
              {' / '}
              {t('common.quota.unlimited')}
            </span>
          ) : (
            ` / ${formatValue(limit)}`
          )}
        </p>
      </div>
      {limit !== null && <Progress value={percent} className={compact ? 'h-1' : 'h-1.5'} />}
    </div>
  );
}
