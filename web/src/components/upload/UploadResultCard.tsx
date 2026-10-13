import { useTranslation } from 'react-i18next';
import type { UploadItem } from './types';
import { LazyImage } from '../LazyImage';
import { UrlList } from '../UrlList';
import { CopyFormatMenu } from '../CopyButton';
import { Badge } from '../ui/badge';
import { Card, CardContent } from '../ui/card';
import { formatBytes, formatDateTime } from '../../utils/format';
import { dimensions } from '../../utils/image';

/** 上传结果卡: 预览图 + 别名 URL 列表 + 复制格式菜单 */
export function UploadResultCard({ item }: { item: UploadItem }) {
  const { t } = useTranslation();

  const result = item.result;
  const url = result?.url ?? item.dedupRecord?.url ?? '';
  const filename = result?.original_filename ?? item.name;
  const hash = result?.hashes.sha256 ?? null;
  const urls =
    result?.urls ??
    (item.dedupRecord
      ? [{ alias: t('upload.result.dedup'), url: item.dedupRecord.url, is_default: true }]
      : []);

  const rows: { label: string; value: string }[] = [
    { label: t('upload.result.filename'), value: filename },
    {
      label: t('upload.result.size'),
      value: formatBytes(result?.size ?? item.dedupRecord?.size ?? item.size),
    },
    {
      label: t('upload.result.dimensions'),
      value: result ? dimensions(result.width, result.height) : '—',
    },
    { label: t('upload.result.format'), value: result?.format ?? '—' },
    {
      label: t('upload.result.uploadedAt'),
      value: result ? formatDateTime(result.created_at) : '—',
    },
  ];

  return (
    <Card>
      <CardContent className="flex flex-col gap-4 sm:flex-row">
        <div className="h-40 w-full shrink-0 overflow-hidden rounded-lg border border-border sm:size-37">
          <LazyImage src={url} alt={filename} hash={hash} objectFit="cover" />
        </div>

        <div className="flex min-w-0 flex-1 flex-col gap-2">
          <div className="flex flex-wrap items-center gap-2">
            <p className="max-w-full truncate text-base font-medium sm:max-w-80" title={filename}>
              {filename}
            </p>
            {item.status === 'duplicate' && (
              <Badge variant="outline" className="border-transparent bg-warning/10 text-warning">
                {t('upload.result.dedup')}
              </Badge>
            )}
            {result && (
              <Badge
                variant="outline"
                className={result.quota_charged ? 'border-primary/50 text-primary' : undefined}
              >
                {result.quota_charged
                  ? t('upload.result.quotaCharged')
                  : t('upload.result.quotaFree')}
              </Badge>
            )}
          </div>

          <div className="flex flex-wrap gap-x-4 gap-y-1">
            {rows.map((row) => (
              <div key={row.label} className="flex flex-col gap-0.5">
                <p className="text-muted-foreground text-xs">{row.label}</p>
                <p className="text-sm">{row.value}</p>
              </div>
            ))}
          </div>

          <div>
            <div className="mb-0.5 flex items-center justify-between">
              <p className="text-sm font-medium">{t('upload.result.urls')}</p>
              <CopyFormatMenu
                url={url}
                filename={filename}
                label={t('upload.result.copyFormats')}
              />
            </div>
            <UrlList urls={urls} filename={filename} />
          </div>
        </div>
      </CardContent>
    </Card>
  );
}
