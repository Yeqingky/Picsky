import { X } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Badge } from './ui/badge';
import { Button } from './ui/button';
import { Input } from './ui/input';
import { Label } from './ui/label';
import { Tooltip, TooltipContent, TooltipTrigger } from './ui/tooltip';
import { cn } from '../lib/utils';

/** 不回显的敏感项: 密码输入 + 清除按钮 + 是否已设置状态 */
export function SecretField({
  id,
  label,
  hint,
  value,
  isSet,
  onChange,
  onClear,
  className,
}: {
  id: string;
  label: string;
  hint?: string;
  value: string;
  isSet: boolean;
  onChange: (next: string) => void;
  onClear: () => void;
  className?: string;
}) {
  const { t } = useTranslation();
  const clearLabel = t('adminSystem.settings.clearSecret');
  return (
    <div className={cn('flex flex-col gap-1.5', className)}>
      <Label htmlFor={id}>{label}</Label>
      <div className="flex items-center gap-1.5">
        <Input
          id={id}
          type="password"
          autoComplete="new-password"
          value={value}
          onChange={(event) => onChange(event.target.value)}
          className="min-w-0 flex-1"
        />
        <Tooltip>
          <TooltipTrigger asChild>
            <Button
              type="button"
              variant="ghost"
              size="icon-sm"
              aria-label={clearLabel}
              onClick={onClear}
              disabled={!isSet}
            >
              <X />
            </Button>
          </TooltipTrigger>
          <TooltipContent>{clearLabel}</TooltipContent>
        </Tooltip>
      </div>
      {hint !== undefined && <p className="text-muted-foreground text-xs">{hint}</p>}
      <Badge
        variant="outline"
        className={cn('w-fit', isSet && 'border-transparent bg-success/10 text-success')}
      >
        {isSet ? t('adminSystem.settings.secretSet') : t('adminSystem.settings.secretUnset')}
      </Badge>
    </div>
  );
}
