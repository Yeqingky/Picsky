import { memo, useEffect, useState } from 'react';
import type { ReactNode } from 'react';
import { MASONRY } from '../utils/constants';
import { displayHeight } from '../utils/image';
import { useElementWidth } from '../hooks/useElementWidth';
import { cn } from '../lib/utils';

export interface MasonryGridProps<T> {
  items: readonly T[];
  getKey: (item: T) => string | number;
  /** 记录宽高 (缺失按 1:1 兜底) */
  getRatio: (item: T) => { width?: number | null; height?: number | null };
  renderItem: (item: T, layout: { columnWidth: number; height: number }) => ReactNode;
  /** 追加的额外单元格 (骨架卡片, 重试按钮等), 参与同一网格排布 */
  extraCells?: ReactNode[];
  gap?: number;
  className?: string;
}

/** Tailwind 断点 -> 列数 (与 MASONRY.columns 对齐) */
const BREAKPOINTS: readonly (readonly [string, number])[] = [
  ['(min-width: 1280px)', MASONRY.columns.xl],
  ['(min-width: 1024px)', MASONRY.columns.lg],
  ['(min-width: 768px)', MASONRY.columns.md],
  ['(min-width: 640px)', MASONRY.columns.sm],
];

/** 按 Tailwind 断点监听瀑布流列数 */
export function useMasonryColumns(): number {
  const [columns, setColumns] = useState(
    () =>
      BREAKPOINTS.find(([query]) => window.matchMedia(query).matches)?.[1] ?? MASONRY.columns.xs,
  );

  useEffect(() => {
    const lists = BREAKPOINTS.map(([query]) => window.matchMedia(query));
    const update = () => {
      const match = BREAKPOINTS.find(([query]) => window.matchMedia(query).matches);
      setColumns(match ? match[1] : MASONRY.columns.xs);
    };
    for (const list of lists) list.addEventListener('change', update);
    return () => {
      for (const list of lists) list.removeEventListener('change', update);
    };
  }, []);

  return columns;
}

/**
 * 自研瀑布流容器 (docs/webui.md 4.1): CSS grid 行跨度法.
 * grid-auto-rows: 8px, 卡片间距 12px, span = ceil((高度 + 间距) / (8 + 间距)).
 * 卡片高度由记录宽高与列宽预先计算, 图片未加载即可定位, 滚动零跳动.
 */
function MasonryGridInner<T>({
  items,
  getKey,
  getRatio,
  renderItem,
  extraCells = [],
  gap = MASONRY.gap,
  className,
}: MasonryGridProps<T>) {
  const [containerRef, containerWidth] = useElementWidth<HTMLDivElement>();
  const columns = useMasonryColumns();

  const columnWidth =
    containerWidth > 0 ? Math.max(1, (containerWidth - gap * (columns - 1)) / columns) : 0;

  const rowSpan = (height: number) =>
    Math.max(1, Math.ceil((height + gap) / (MASONRY.rowHeight + gap)));

  return (
    <div
      ref={containerRef}
      className={cn('grid', className)}
      style={{
        gridTemplateColumns: `repeat(${columns}, minmax(0, 1fr))`,
        gridAutoRows: `${MASONRY.rowHeight}px`,
        columnGap: `${gap}px`,
        rowGap: `${gap}px`,
      }}
    >
      {containerWidth > 0 &&
        items.map((item) => {
          const ratio = getRatio(item);
          const height = displayHeight(ratio.width, ratio.height, columnWidth);
          return (
            <div
              key={getKey(item)}
              className="min-w-0 [contain:layout_paint_style]"
              style={{ gridRowEnd: `span ${rowSpan(height)}` }}
            >
              {renderItem(item, { columnWidth, height })}
            </div>
          );
        })}
      {extraCells.map((cell, index) => (
        <div
          key={`extra-${index}`}
          className="min-w-0 [contain:layout_paint_style]"
          style={{ gridRowEnd: `span ${rowSpan(columnWidth)}` }}
        >
          {cell}
        </div>
      ))}
    </div>
  );
}

export const MasonryGrid = memo(MasonryGridInner) as typeof MasonryGridInner;
