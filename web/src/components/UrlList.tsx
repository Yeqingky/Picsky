import { useTranslation } from 'react-i18next';
import type { UploadUrlAlias } from '../api/types';
import { CopyButton } from './CopyButton';
import { Badge } from './ui/badge';
import { Tooltip, TooltipContent, TooltipTrigger } from './ui/tooltip';

/** 别名 URL 列表 (上传结果卡 / 详情抽屉 / 策略对照表共用) */
export function UrlList({
  urls,
  filename,
  emptyText = '—',
}: {
  urls: readonly UploadUrlAlias[];
  filename?: string;
  emptyText?: string;
}) {
  const { t } = useTranslation();

  if (urls.length === 0) {
    return <p className="text-muted-foreground text-sm">{emptyText}</p>;
  }

  return (
    <div className="flex flex-col gap-1.5">
      {urls.map((item) => (
        <div key={`${item.alias}-${item.url}`} className="flex min-w-0 items-center gap-2">
          <Badge variant={item.is_default ? 'default' : 'outline'} className="shrink-0">
            {item.alias || '—'}
          </Badge>
          <Tooltip>
            <TooltipTrigger asChild>
              <span className="min-w-0 flex-1 truncate font-mono text-[12.5px]">{item.url}</span>
            </TooltipTrigger>
            <TooltipContent>{item.url}</TooltipContent>
          </Tooltip>
          <CopyButton
            value={item.url}
            tooltip={t('common.action.copyAlias', { alias: item.alias || '—' })}
          />
          {filename !== undefined && (
            <CopyButton
              value={`![${filename}](${item.url})`}
              tooltip={t('common.action.copyMarkdown')}
            />
          )}
        </div>
      ))}
    </div>
  );
}
