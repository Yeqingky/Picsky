import { PLACEHOLDER_PALETTE } from './constants';

/**
 * 占位色: 由 sha256 前 8 位十六进制映射到固定色板, 同一图片恒定 (docs/webui.md 4.2).
 * 缺失哈希时退化为色板首位, 不产生随机闪烁.
 */
export function placeholderColor(hash: string | null | undefined, mode: 'light' | 'dark'): string {
  const palette = PLACEHOLDER_PALETTE[mode];
  const head = (hash ?? '').slice(0, 8);
  const value = Number.parseInt(head, 16);
  if (!Number.isFinite(value)) return palette[0];
  return palette[Math.abs(value) % palette.length];
}

/** 依据记录宽高计算展示高度 (px); 缺失按 1:1 兜底 */
export function displayHeight(
  width: number | null | undefined,
  height: number | null | undefined,
  targetWidth: number,
): number {
  if (!width || !height || width <= 0 || height <= 0) return targetWidth;
  return Math.round((targetWidth * height) / width);
}

/** 宽高比字符串, 例如 "1920 x 1080" */
export function dimensions(
  width: number | null | undefined,
  height: number | null | undefined,
): string {
  if (!width || !height) return '未知';
  return `${width} x ${height}`;
}
