import { useTranslation } from 'react-i18next';
import type { CopyFormat } from '../utils/clipboard';
import { cn } from '../lib/utils';

const OPTIONS: { value: CopyFormat; labelKey: string }[] = [
  { value: 'direct', labelKey: 'common.copyFormat.short.direct' },
  { value: 'markdown', labelKey: 'common.copyFormat.short.markdown' },
  { value: 'html', labelKey: 'common.copyFormat.short.html' },
];

/** 复制格式三选项 (URL / MD / HTML), 上传页与图库详情共用 */
export function CopyFormatToggle({
  value,
  onChange,
  className,
}: {
  value: CopyFormat;
  onChange: (format: CopyFormat) => void;
  className?: string;
}) {
  const { t } = useTranslation();

  return (
    <div
      role="radiogroup"
      aria-label={t('common.copyFormat.label')}
      className={cn('bg-muted flex items-center gap-1 rounded-full p-1', className)}
    >
      {OPTIONS.map((option) => (
        <button
          key={option.value}
          type="button"
          role="radio"
          aria-checked={value === option.value}
          onClick={() => onChange(option.value)}
          className={cn(
            'h-6 rounded-full px-2.5 text-xs font-medium transition-colors',
            value === option.value
              ? 'bg-card text-foreground shadow-sm'
              : 'text-muted-foreground hover:text-foreground',
          )}
        >
          {t(option.labelKey)}
        </button>
      ))}
    </div>
  );
}
