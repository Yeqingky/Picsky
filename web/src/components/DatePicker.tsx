import { useId, useState } from 'react';
import type { JSX } from 'react';
import { useTranslation } from 'react-i18next';
import dayjs from 'dayjs';
import type { Dayjs } from 'dayjs';
import { zhCN } from 'date-fns/locale';
import type { Matcher } from 'react-day-picker';
import { CalendarIcon, X } from 'lucide-react';
import { cn } from '../lib/utils';
import { Button } from './ui/button';
import { Calendar } from './ui/calendar';
import { Input } from './ui/input';
import { Label } from './ui/label';
import { Popover, PopoverContent, PopoverTrigger } from './ui/popover';

export interface DatePickerProps {
  value: Dayjs | null;
  onChange: (value: Dayjs | null) => void;
  label?: string;
  placeholder?: string;
  minDate?: Dayjs | null;
  maxDate?: Dayjs | null;
  /** 显示清除按钮 */
  clearable?: boolean;
  disabled?: boolean;
  /** 占满容器宽度 */
  fullWidth?: boolean;
  /** dayjs 格式串 (默认按日期/日期时间) */
  format?: string;
  className?: string;
}

const DEFAULT_FORMAT = { date: 'YYYY-MM-DD', datetime: 'YYYY-MM-DD HH:mm' } as const;

/** Popover + 日历的受控日期选择器, 也可用 DateTimePicker 追加时间输入 */
function PickerBase({
  mode,
  value,
  onChange,
  label,
  placeholder,
  minDate,
  maxDate,
  clearable = false,
  disabled = false,
  fullWidth = false,
  format,
  className,
}: DatePickerProps & { mode: 'date' | 'datetime' }): JSX.Element {
  const { t } = useTranslation();
  const id = useId();
  const [open, setOpen] = useState(false);
  const displayFormat = format ?? DEFAULT_FORMAT[mode];

  const disabledDays: Matcher[] = [];
  if (minDate) disabledDays.push({ before: minDate.toDate() });
  if (maxDate) disabledDays.push({ after: maxDate.toDate() });

  const setDate = (date: Date | undefined) => {
    if (!date) {
      onChange(null);
      return;
    }
    const picked = dayjs(date);
    const next =
      mode === 'datetime' && value
        ? picked.hour(value.hour()).minute(value.minute()).second(0).millisecond(0)
        : picked.startOf('day');
    onChange(next);
    if (mode === 'date') setOpen(false);
  };

  const setTime = (part: 'hour' | 'minute', raw: string) => {
    const parsed = Number.parseInt(raw, 10);
    if (Number.isNaN(parsed)) return;
    const limit = part === 'hour' ? 23 : 59;
    const clamped = Math.min(limit, Math.max(0, parsed));
    const base = value ?? dayjs();
    onChange(
      (part === 'hour' ? base.hour(clamped) : base.minute(clamped)).second(0).millisecond(0),
    );
  };

  const showClear = clearable && value !== null && !disabled;

  return (
    <div className={cn('flex flex-col gap-1.5', fullWidth && 'w-full', className)}>
      {label !== undefined && <Label htmlFor={id}>{label}</Label>}
      <div className={cn('relative', fullWidth && 'w-full')}>
        <Popover open={open} onOpenChange={setOpen}>
          <PopoverTrigger asChild>
            <Button
              id={id}
              type="button"
              variant="outline"
              disabled={disabled}
              aria-label={label}
              className={cn(
                'justify-start gap-2 font-normal',
                fullWidth ? 'w-full' : 'w-40',
                showClear && 'pr-8',
                value === null && 'text-muted-foreground',
              )}
            >
              <CalendarIcon className="size-4 shrink-0" />
              <span className="truncate">
                {value
                  ? value.format(displayFormat)
                  : (placeholder ?? t('common.form.selectPlaceholder'))}
              </span>
            </Button>
          </PopoverTrigger>
          <PopoverContent align="start" className="w-auto gap-0 p-0">
            <Calendar
              mode="single"
              selected={value?.toDate()}
              defaultMonth={value?.toDate() ?? minDate?.toDate()}
              onSelect={setDate}
              disabled={disabledDays.length > 0 ? disabledDays : undefined}
              locale={zhCN}
            />
            {mode === 'datetime' && (
              <div className="flex items-center gap-2 border-t p-2">
                <Input
                  type="number"
                  min={0}
                  max={23}
                  className="w-16"
                  value={value ? value.hour() : ''}
                  onChange={(event) => setTime('hour', event.target.value)}
                  aria-label={t('common.time.from')}
                />
                <span className="text-muted-foreground">:</span>
                <Input
                  type="number"
                  min={0}
                  max={59}
                  className="w-16"
                  value={value ? value.minute() : ''}
                  onChange={(event) => setTime('minute', event.target.value)}
                  aria-label={t('common.time.to')}
                />
              </div>
            )}
          </PopoverContent>
        </Popover>
        {showClear && (
          <Button
            type="button"
            variant="ghost"
            size="icon-xs"
            className="absolute top-1/2 right-1 z-10 -translate-y-1/2"
            onClick={() => onChange(null)}
            aria-label={t('common.action.clear')}
          >
            <X className="size-4" />
          </Button>
        )}
      </div>
    </div>
  );
}

/** 日期选择器: value/onChange 受控, 值为 dayjs 或 null */
export function DatePicker(props: DatePickerProps): JSX.Element {
  return <PickerBase mode="date" {...props} />;
}

/** 日期时间选择器: 在 DatePicker 基础上追加时/分输入 */
export function DateTimePicker(props: DatePickerProps): JSX.Element {
  return <PickerBase mode="datetime" {...props} />;
}
