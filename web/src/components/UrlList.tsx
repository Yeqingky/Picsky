import { useTranslation } from 'react-i18next';
import type { UploadUrlAlias } from '../api/types';
import type { CopyFormat } from '../utils/clipboard';
import { renderCopyText } from '../utils/clipboard';
import { CopyButton } from './CopyButton';
import { Badge } from './ui/badge';
import { Tooltip, TooltipContent, TooltipTrigger } from './ui/tooltip';

/** 别名 URL 列表 (上传结果卡 / 详情抽屉 / 策略对照表共用) */
export function UrlList({
  urls,
  filename,
  copyFormat = 'direct',
  emptyText = '-',
}: {
  urls: readonly UploadUrlAlias[];
  filename?: string;
  copyFormat?: CopyFormat;
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
            {item.alias || t('common.defaultAlias')}
          </Badge>
          <Tooltip>
            <TooltipTrigger asChild>
              <span className="min-w-0 flex-1 truncate font-mono text-[12.5px]">{item.url}</span>
            </TooltipTrigger>
            <TooltipContent>{item.url}</TooltipContent>
          </Tooltip>
          <CopyButton
            value={renderCopyText(copyFormat, { url: item.url, filename })}
            tooltip={t('common.action.copyAlias', {
              alias: item.alias || t('common.defaultAlias'),
            })}
          />
        </div>
      ))}
    </div>
  );
}
