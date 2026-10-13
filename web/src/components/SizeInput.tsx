import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { cn } from '../lib/utils';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from './ui/select';

/** 体积单位 (1024 进制); 组件对外统一使用字节数 */
export type SizeUnit = 'KB' | 'MB' | 'GB';

const UNIT_FACTORS: Record<SizeUnit, number> = {
  KB: 1024,
  MB: 1024 * 1024,
  GB: 1024 * 1024 * 1024,
};

/** 数字是 1024 的整数倍时优先较大的单位, 否则用默认单位 */
function pickUnit(bytes: number | null, units: readonly SizeUnit[], fallback: SizeUnit): SizeUnit {
  if (bytes === null || bytes <= 0) return fallback;
  const exact = units.filter((unit) => bytes % UNIT_FACTORS[unit] === 0);
  if (exact.length === 0) return fallback;
  return exact.reduce((a, b) => (UNIT_FACTORS[b] > UNIT_FACTORS[a] ? b : a));
}

/** 字节数换算为展示数值, 最多两位小数并去掉尾零 */
function formatAmount(bytes: number | null, unit: SizeUnit): string {
  if (bytes === null || Number.isNaN(bytes)) return '';
  return String(Number((bytes / UNIT_FACTORS[unit]).toFixed(2)));
}

/**
 * 体积输入: 数字与单位在同一个框内 (数字在左, 单位下拉在右).
 * value/onChange 使用字节数; 空输入回调 null (由调用方决定 "继承" 或报错).
 */
export function SizeInput({
  id,
  value,
  units,
  defaultUnit,
  onChange,
  disabled = false,
  invalid = false,
  className,
}: {
  id?: string;
  value: number | null;
  units: readonly SizeUnit[];
  defaultUnit?: SizeUnit;
  onChange: (bytes: number | null) => void;
  disabled?: boolean;
  invalid?: boolean;
  className?: string;
}) {
  const { t } = useTranslation();
  const fallback = defaultUnit ?? units[0];
  const [unit, setUnit] = useState<SizeUnit>(() => pickUnit(value, units, fallback));
  const [amount, setAmount] = useState(() => formatAmount(value, pickUnit(value, units, fallback)));

  // 外部值变化 (打开弹窗, 服务端返回) 时同步显示; 由输入自身驱动的变化保持原样
  useEffect(() => {
    setAmount((prev) => {
      const parsed = Number(prev);
      const derived =
        prev.trim() === '' || !Number.isFinite(parsed)
          ? null
          : Math.round(parsed * UNIT_FACTORS[unit]);
      return derived === value ? prev : formatAmount(value, unit);
    });
  }, [value, unit]);

  /** 只接受数字与小数点, 输入即换算为字节数 */
  const changeAmount = (raw: string) => {
    const sanitized = raw.replace(/[^0-9.]/g, '');
    setAmount(sanitized);
    const parsed = Number(sanitized);
    if (sanitized === '' || !Number.isFinite(parsed)) {
      onChange(null);
      return;
    }
    onChange(Math.round(parsed * UNIT_FACTORS[unit]));
  };

  /** 切换单位只改变展示数值, 字节数保持不变 */
  const changeUnit = (next: SizeUnit) => {
    setUnit(next);
    setAmount(formatAmount(value, next));
  };

  return (
    <div
      aria-invalid={invalid || undefined}
      className={cn(
        'border-input flex h-8 w-full min-w-0 items-center rounded-lg border bg-transparent transition-colors focus-within:border-ring focus-within:ring-3 focus-within:ring-ring/50 aria-invalid:border-destructive aria-invalid:ring-3 aria-invalid:ring-destructive/20 dark:bg-input/30',
        disabled && 'pointer-events-none opacity-50',
        className,
      )}
    >
      <input
        id={id}
        className="h-full min-w-0 flex-1 bg-transparent px-2.5 text-base outline-none placeholder:text-muted-foreground md:text-sm"
        inputMode="numeric"
        value={amount}
        disabled={disabled}
        onChange={(event) => changeAmount(event.target.value)}
      />
      <span className="bg-border h-4 w-px shrink-0" aria-hidden="true" />
      <Select value={unit} onValueChange={(next) => changeUnit(next as SizeUnit)}>
        <SelectTrigger
          aria-label={t('common.form.sizeUnit')}
          disabled={disabled}
          className="h-8 shrink-0 rounded-none border-0 bg-transparent pr-2.5 pl-2 focus-visible:border-0 focus-visible:ring-0 dark:bg-transparent dark:hover:bg-transparent"
        >
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {units.map((item) => (
            <SelectItem key={item} value={item}>
              {item}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </div>
  );
}
