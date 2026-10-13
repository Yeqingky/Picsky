import { useState } from 'react';
import type { ComponentProps } from 'react';
import { Check, ChevronDown, Copy } from 'lucide-react';
import { toast } from 'sonner';
import { useTranslation } from 'react-i18next';
import { Button } from './ui/button';
import { Tooltip, TooltipContent, TooltipTrigger } from './ui/tooltip';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from './ui/dropdown-menu';
import { copyText, renderCopyText } from '../utils/clipboard';
import type { CopyFormat } from '../utils/clipboard';

type ButtonProps = ComponentProps<typeof Button>;

interface CopyButtonProps extends Omit<ButtonProps, 'onClick' | 'children' | 'title'> {
  value: string;
  tooltip?: string;
  /** 复制成功后短暂切换为对勾 */
  feedbackMs?: number;
}

/** 单值复制按钮: 即时反馈 (图标切换 + toast) */
export function CopyButton({
  value,
  tooltip,
  feedbackMs = 1500,
  variant = 'ghost',
  size = 'icon-sm',
  ...rest
}: CopyButtonProps) {
  const { t } = useTranslation();
  const [copied, setCopied] = useState(false);
  const label = tooltip ?? t('common.action.copy');

  const handleCopy = async () => {
    const ok = await copyText(value);
    if (ok) {
      setCopied(true);
      toast.success(t('common.action.copied'));
      window.setTimeout(() => setCopied(false), feedbackMs);
    } else {
      toast.error(t('common.action.copyFailed'));
    }
  };

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          type="button"
          variant={variant}
          size={size}
          aria-label={label}
          {...rest}
          onClick={() => {
            void handleCopy();
          }}
        >
          {copied ? <Check className="text-success size-4" /> : <Copy className="size-4" />}
        </Button>
      </TooltipTrigger>
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
  );
}

const COPY_FORMATS: CopyFormat[] = ['direct', 'markdown', 'html', 'bbcode', 'json'];

interface CopyFormatMenuProps extends Omit<ButtonProps, 'onClick' | 'children'> {
  url: string;
  filename?: string;
  label?: string;
}

/** 复制格式菜单: 直链 / Markdown / HTML / BBCode / JSON (docs/webui.md 第 6 节) */
export function CopyFormatMenu({
  url,
  filename,
  label,
  size = 'sm',
  variant = 'outline',
  ...rest
}: CopyFormatMenuProps) {
  const { t } = useTranslation();

  const copy = async (format: CopyFormat) => {
    const ok = await copyText(renderCopyText(format, { url, filename }));
    if (ok) toast.success(t('common.action.copied'));
    else toast.error(t('common.action.copyFailed'));
  };

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button type="button" size={size} variant={variant} {...rest}>
          <Copy className="size-4" />
          {label ?? t('common.action.copy')}
          <ChevronDown className="size-4" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        {COPY_FORMATS.map((format) => (
          <DropdownMenuItem
            key={format}
            onClick={() => {
              void copy(format);
            }}
          >
            <Copy className="size-4" />
            {t(`common.copyFormat.${format}`)}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
