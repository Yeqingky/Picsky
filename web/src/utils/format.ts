import dayjs from 'dayjs';
import 'dayjs/locale/zh-cn';
import utc from 'dayjs/plugin/utc';
import timezone from 'dayjs/plugin/timezone';
import relativeTime from 'dayjs/plugin/relativeTime';

dayjs.extend(utc);
dayjs.extend(timezone);
dayjs.extend(relativeTime);
dayjs.locale('zh-cn');

const BYTE_UNITS = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB'];

const datetimeFormatter = new Intl.DateTimeFormat('zh-CN', {
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
  second: '2-digit',
  hour12: false,
});

const dateFormatter = new Intl.DateTimeFormat('zh-CN', {
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
});

/** 1024 进制字节数格式化 */
export function formatBytes(bytes: number | null | undefined, fractionDigits = 2): string {
  if (bytes === null || bytes === undefined || Number.isNaN(bytes)) return '—';
  if (bytes < 1024) return `${bytes} B`;
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < BYTE_UNITS.length - 1) {
    value /= 1024;
    unit += 1;
  }
  const digits = value >= 100 ? 0 : fractionDigits;
  return `${value.toFixed(digits)} ${BYTE_UNITS[unit]}`;
}

export function formatNumber(value: number | null | undefined): string {
  if (value === null || value === undefined || Number.isNaN(value)) return '—';
  return new Intl.NumberFormat('zh-CN').format(value);
}

/** RFC3339 (UTC) -> 本地时区展示 */
export function formatDateTime(value: string | null | undefined): string {
  if (!value) return '—';
  const date = dayjs(value);
  if (!date.isValid()) return value;
  return datetimeFormatter.format(date.toDate());
}

export function formatDate(value: string | null | undefined): string {
  if (!value) return '—';
  const date = dayjs(value);
  if (!date.isValid()) return value;
  return dateFormatter.format(date.toDate());
}

/** 相对时间, 例如 "3 天前" */
export function formatRelative(value: string | null | undefined): string {
  if (!value) return '—';
  const date = dayjs(value);
  if (!date.isValid()) return value;
  return date.fromNow();
}

/** 毫秒 -> "1.23 s" */
export function formatDuration(ms: number | null | undefined): string {
  if (ms === null || ms === undefined) return '—';
  if (ms < 1000) return `${Math.round(ms)} ms`;
  return `${(ms / 1000).toFixed(2)} s`;
}

/** dayjs -> RFC3339 (UTC), 用于 from / to 查询参数 */
export function toRfc3339(
  value: dayjs.Dayjs | null | undefined,
  endOfDay = false,
): string | undefined {
  if (!value || !value.isValid()) return undefined;
  const date = endOfDay ? value.endOf('day') : value.startOf('day');
  return date.toISOString();
}

export function formatPercentage(value: number, digits = 1): string {
  return `${(value * 100).toFixed(digits)}%`;
}

export { dayjs };
