import type { JSX, ReactNode } from 'react';
import { useCallback, useEffect, useState } from 'react';
import { flushSync } from 'react-dom';
import { useTranslation } from 'react-i18next';
import {
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  ChevronsUpDown,
  ChevronUp,
  GripVertical,
} from 'lucide-react';
import {
  closestCenter,
  DndContext,
  KeyboardSensor,
  PointerSensor,
  useSensor,
  useSensors,
  type DragEndEvent,
} from '@dnd-kit/core';
import { restrictToVerticalAxis } from '@dnd-kit/modifiers';
import {
  arrayMove,
  SortableContext,
  sortableKeyboardCoordinates,
  useSortable,
  verticalListSortingStrategy,
} from '@dnd-kit/sortable';
import { CSS } from '@dnd-kit/utilities';
import { cn } from '../lib/utils';
import { EmptyState } from './States';
import { Button } from './ui/button';
import { Checkbox } from './ui/checkbox';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from './ui/select';
import { Skeleton } from './ui/skeleton';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from './ui/table';

interface DataTableRowProps {
  rowId: string | number;
  rowHeight: number;
  selected: boolean;
  children: ReactNode;
}

function StaticDataTableRow({ rowId, rowHeight, selected, children }: DataTableRowProps) {
  return (
    <TableRow
      data-row-id={String(rowId)}
      style={{ height: rowHeight }}
      className={cn('hover:bg-muted/40', selected && 'bg-muted/60')}
    >
      {children}
    </TableRow>
  );
}

interface SortableDataTableRowProps extends DataTableRowProps {
  disabled: boolean;
  reorderLabel: string;
  dragActive: boolean;
}

function SortableDataTableRow({
  rowId,
  rowHeight,
  selected,
  disabled,
  reorderLabel,
  dragActive,
  children,
}: SortableDataTableRowProps) {
  const {
    attributes,
    listeners,
    setNodeRef,
    setActivatorNodeRef,
    transform,
    transition,
    isDragging,
  } = useSortable({ id: rowId, disabled });

  return (
    <TableRow
      ref={setNodeRef}
      data-row-id={String(rowId)}
      style={{
        height: rowHeight,
        transform: CSS.Transform.toString(transform),
        // 释放后数据顺序与 transform 会同时归位, 关闭过渡避免非活动行先回弹再归位
        transition: dragActive && !isDragging ? transition : undefined,
        position: isDragging ? 'relative' : undefined,
        zIndex: isDragging ? 1 : undefined,
      }}
      className={cn('hover:bg-muted/40', selected && 'bg-muted/60', isDragging && 'opacity-50')}
    >
      <TableCell className="w-10 pr-0">
        <button
          ref={setActivatorNodeRef}
          type="button"
          {...attributes}
          {...listeners}
          className={cn(
            'text-muted-foreground hover:text-foreground inline-flex size-8 touch-none items-center justify-center rounded-md disabled:cursor-not-allowed disabled:opacity-50',
            isDragging ? 'cursor-grabbing' : 'cursor-grab',
          )}
          aria-label={reorderLabel}
          title={reorderLabel}
          disabled={disabled}
        >
          <GripVertical className="size-4" />
        </button>
      </TableCell>
      {children}
    </TableRow>
  );
}

export interface DataTableColumn<T> {
  /** React key */
  id: string;
  header: ReactNode;
  cell: (row: T) => ReactNode;
  /** 默认 left */
  align?: 'left' | 'center' | 'right';
  /** 单元格额外 class */
  className?: string;
  /** 表头点击排序 (asc -> desc -> 取消) */
  sortable?: boolean;
  /** 排序取值; sortable 时必填 */
  sortValue?: (row: T) => string | number | null | undefined;
}

export interface DataTablePagination {
  /** 0 基页码 */
  page: number;
  pageSize: number;
  /** 总条数 */
  total: number;
  onPageChange: (page: number) => void;
  onPageSizeChange: (pageSize: number) => void;
  /** 默认 [20, 50, 100] */
  pageSizeOptions?: readonly number[];
  /** true: rows 已是当前页; false/省略: rows 为全量, 组件内部切片 */
  serverSide?: boolean;
}

export interface DataTableProps<T> {
  columns: DataTableColumn<T>[];
  rows: T[];
  /** 默认 (row as {id}).id */
  getRowId?: (row: T) => string | number;
  /** 渲染 5 行骨架 */
  loading?: boolean;
  /** 默认 <EmptyState /> */
  emptyState?: ReactNode;
  /** 省略则不渲染分页条 */
  pagination?: DataTablePagination;
  selectable?: boolean;
  selectedIds?: readonly (string | number)[];
  onSelectionChange?: (ids: (string | number)[]) => void;
  /** 默认 48 */
  rowHeight?: number;
  className?: string;
  /** 长按拖拽排序; 提供时在最左侧渲染拖拽把手, 参数为完整行顺序的 ID */
  onReorder?: (ids: (string | number)[]) => void;
  /** 禁用拖拽 (例如保存中) */
  reorderDisabled?: boolean;
}

const DEFAULT_PAGE_SIZE_OPTIONS: readonly number[] = [20, 50, 100];
const SKELETON_ROWS = 5;
const SKELETON_CELL_WIDTHS = ['w-2/3', 'w-3/5', 'w-4/5', 'w-1/2', 'w-3/4'];

const ALIGN_CLASS: Record<NonNullable<DataTableColumn<unknown>['align']>, string> = {
  left: 'text-left',
  center: 'text-center',
  right: 'text-right',
};

/** 管理端统一数据表格: 服务端/客户端分页 + 可选多选 + 加载/空态 */
export function DataTable<T>({
  columns,
  rows,
  getRowId,
  loading = false,
  emptyState,
  pagination,
  selectable = false,
  selectedIds,
  onSelectionChange,
  rowHeight = 48,
  className,
  onReorder,
  reorderDisabled = false,
}: DataTableProps<T>): JSX.Element {
  const { t } = useTranslation();

  const resolveId = useCallback(
    (row: T): string | number => {
      if (getRowId) return getRowId(row);
      if (row !== null && typeof row === 'object' && 'id' in row) {
        const id = row.id;
        if (typeof id === 'string' || typeof id === 'number') return id;
      }
      return '';
    },
    [getRowId],
  );

  const rawSelected = selectedIds ?? [];
  const isSelected = (row: T) => rawSelected.some((id) => String(id) === String(resolveId(row)));

  const [optimisticOrder, setOptimisticOrder] = useState<(string | number)[] | null>(null);
  useEffect(() => {
    // 父组件数据更新后交给真实顺序接管
    setOptimisticOrder(null);
  }, [rows, resolveId]);

  const rowById = new Map(rows.map((row) => [String(resolveId(row)), row]));
  const orderedRows =
    optimisticOrder === null
      ? rows
      : optimisticOrder
          .map((id) => rowById.get(String(id)))
          .filter((row): row is T => row !== undefined);

  const [sort, setSort] = useState<{ id: string; desc: boolean } | null>(null);

  const toggleSort = (column: DataTableColumn<T>) => {
    setSort((current) => {
      if (current?.id !== column.id) return { id: column.id, desc: false };
      if (!current.desc) return { id: column.id, desc: true };
      return null;
    });
  };

  const sortedRows = (() => {
    const column = sort ? columns.find((item) => item.id === sort.id) : undefined;
    const getValue = column?.sortValue;
    if (!sort || !getValue) return orderedRows;
    return [...orderedRows].sort((left, right) => {
      const a = getValue(left);
      const b = getValue(right);
      if (a === null || a === undefined) return b === null || b === undefined ? 0 : 1;
      if (b === null || b === undefined) return -1;
      const delta =
        typeof a === 'number' && typeof b === 'number'
          ? a - b
          : String(a).localeCompare(String(b), undefined, { numeric: true });
      return sort.desc ? -delta : delta;
    });
  })();

  const visibleRows =
    pagination && !pagination.serverSide
      ? sortedRows.slice(
          pagination.page * pagination.pageSize,
          (pagination.page + 1) * pagination.pageSize,
        )
      : sortedRows;

  const reorderable = onReorder !== undefined && !pagination?.serverSide;
  const [dragActive, setDragActive] = useState(false);
  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 6 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  );

  const handleDragEnd = ({ active, over }: DragEndEvent) => {
    if (!reorderable || over === null || String(active.id) === String(over.id)) {
      setDragActive(false);
      return;
    }
    const pageStart =
      pagination && !pagination.serverSide ? pagination.page * pagination.pageSize : 0;
    const sourceIndex = visibleRows.findIndex(
      (row) => String(resolveId(row)) === String(active.id),
    );
    const targetIndex = visibleRows.findIndex((row) => String(resolveId(row)) === String(over.id));
    if (sourceIndex < 0 || targetIndex < 0) {
      setDragActive(false);
      return;
    }
    const nextPage = arrayMove(visibleRows, sourceIndex, targetIndex);
    const nextIds = rows.map((row) => resolveId(row));
    nextIds.splice(pageStart, nextPage.length, ...nextPage.map((row) => resolveId(row)));
    flushSync(() => {
      setDragActive(false);
      setOptimisticOrder(nextIds);
    });
    onReorder?.(nextIds);
  };

  const reorderLabel = t('common.table.reorder');
  const rowsContent = visibleRows.map((row) => {
    const selected = isSelected(row);
    const rowId = resolveId(row);
    const cells = (
      <>
        {selectable && (
          <TableCell>
            <Checkbox
              checked={selected}
              onCheckedChange={() => toggleRow(row)}
              aria-label={t('common.action.selectAll')}
            />
          </TableCell>
        )}
        {columns.map((column) => (
          <TableCell
            key={column.id}
            className={cn(ALIGN_CLASS[column.align ?? 'left'], column.className)}
          >
            {column.cell(row)}
          </TableCell>
        ))}
      </>
    );
    return reorderable ? (
      <SortableDataTableRow
        key={String(rowId)}
        rowId={rowId}
        rowHeight={rowHeight}
        selected={selected}
        disabled={reorderDisabled}
        reorderLabel={reorderLabel}
        dragActive={dragActive}
      >
        {cells}
      </SortableDataTableRow>
    ) : (
      <StaticDataTableRow
        key={String(rowId)}
        rowId={rowId}
        rowHeight={rowHeight}
        selected={selected}
      >
        {cells}
      </StaticDataTableRow>
    );
  });

  const dragContext = reorderable ? (
    <DndContext
      sensors={sensors}
      collisionDetection={closestCenter}
      modifiers={[restrictToVerticalAxis]}
      // 隐藏提示节点不能插入 tbody
      accessibility={{ container: document.body }}
      onDragStart={() => {
        setSort(null);
        setDragActive(true);
      }}
      onDragEnd={handleDragEnd}
      onDragCancel={() => setDragActive(false)}
    >
      <SortableContext
        items={visibleRows.map((row) => resolveId(row))}
        strategy={verticalListSortingStrategy}
      >
        {rowsContent}
      </SortableContext>
    </DndContext>
  ) : (
    rowsContent
  );

  const columnCount = columns.length + (selectable ? 1 : 0) + (reorderable ? 1 : 0);
  const selectedOnPage = visibleRows.filter(isSelected).length;
  const allSelected = visibleRows.length > 0 && selectedOnPage === visibleRows.length;
  const someSelected = selectedOnPage > 0 && !allSelected;

  const toggleAll = (next: boolean | 'indeterminate') => {
    if (!onSelectionChange) return;
    if (next === true) {
      const merged = new Map<string, string | number>();
      for (const id of rawSelected) merged.set(String(id), id);
      for (const row of visibleRows) {
        const id = resolveId(row);
        merged.set(String(id), id);
      }
      onSelectionChange([...merged.values()]);
    } else {
      const pageKeys = new Set(visibleRows.map((row) => String(resolveId(row))));
      onSelectionChange(rawSelected.filter((id) => !pageKeys.has(String(id))));
    }
  };

  const toggleRow = (row: T) => {
    if (!onSelectionChange) return;
    const id = resolveId(row);
    if (isSelected(row)) {
      onSelectionChange(rawSelected.filter((value) => String(value) !== String(id)));
    } else {
      onSelectionChange([...rawSelected, id]);
    }
  };

  const page = pagination?.page ?? 0;
  const pages = pagination ? Math.max(1, Math.ceil(pagination.total / pagination.pageSize)) : 1;
  const pageSizeOptions = pagination?.pageSizeOptions ?? DEFAULT_PAGE_SIZE_OPTIONS;
  const initialLoading = loading && rows.length === 0;

  return (
    <div className={cn('w-full rounded-lg border bg-card', className)}>
      <Table>
        <TableHeader>
          <TableRow className="hover:bg-muted/50">
            {reorderable && <TableHead className="bg-muted/50 w-10" />}
            {selectable && (
              <TableHead className="bg-muted/50 text-muted-foreground w-10">
                {initialLoading ? (
                  <Skeleton className="size-4" />
                ) : (
                  <Checkbox
                    checked={allSelected ? true : someSelected ? 'indeterminate' : false}
                    onCheckedChange={toggleAll}
                    disabled={visibleRows.length === 0}
                    aria-label={t('common.action.selectAll')}
                  />
                )}
              </TableHead>
            )}
            {columns.map((column) => {
              const active = sort?.id === column.id;
              const align = column.align ?? 'left';
              return (
                <TableHead
                  key={column.id}
                  aria-sort={active ? (sort.desc ? 'descending' : 'ascending') : undefined}
                  className={cn(
                    'bg-muted/50 text-muted-foreground text-sm font-medium',
                    ALIGN_CLASS[align],
                    column.className,
                  )}
                >
                  {column.sortable ? (
                    <button
                      type="button"
                      onClick={() => toggleSort(column)}
                      className={cn(
                        'hover:text-foreground -mx-1 inline-flex w-full items-center gap-1 rounded px-1 transition-colors',
                        align === 'right' && 'justify-end',
                        align === 'center' && 'justify-center',
                        active && 'text-foreground',
                      )}
                    >
                      {column.header}
                      {active ? (
                        sort.desc ? (
                          <ChevronDown className="size-3.5" />
                        ) : (
                          <ChevronUp className="size-3.5" />
                        )
                      ) : (
                        <ChevronsUpDown className="size-3.5 opacity-60" />
                      )}
                    </button>
                  ) : (
                    column.header
                  )}
                </TableHead>
              );
            })}
          </TableRow>
        </TableHeader>

        <TableBody>
          {loading ? (
            Array.from({ length: SKELETON_ROWS }, (_, rowIndex) => (
              <TableRow key={`skeleton-${rowIndex}`} style={{ height: rowHeight }}>
                {reorderable && (
                  <TableCell>
                    <Skeleton className="size-4" />
                  </TableCell>
                )}
                {selectable && (
                  <TableCell>
                    <Skeleton className="size-4" />
                  </TableCell>
                )}
                {columns.map((column, columnIndex) => {
                  return (
                    <TableCell
                      key={column.id}
                      className={cn(ALIGN_CLASS[column.align ?? 'left'], column.className)}
                    >
                      {column.id === 'thumb' ? (
                        <Skeleton className="size-12 rounded" />
                      ) : column.id === 'actions' ? (
                        <div className="flex justify-end gap-2">
                          <Skeleton className="size-8 rounded-lg" />
                          <Skeleton className="size-8 rounded-lg" />
                        </div>
                      ) : (
                        <Skeleton
                          className={cn(
                            'h-4 rounded-sm',
                            SKELETON_CELL_WIDTHS[
                              (rowIndex * 2 + columnIndex) % SKELETON_CELL_WIDTHS.length
                            ],
                          )}
                        />
                      )}
                    </TableCell>
                  );
                })}
              </TableRow>
            ))
          ) : visibleRows.length === 0 ? (
            <TableRow className="hover:bg-transparent">
              <TableCell colSpan={columnCount} className="whitespace-normal">
                {emptyState ?? <EmptyState />}
              </TableCell>
            </TableRow>
          ) : (
            dragContext
          )}
        </TableBody>
      </Table>

      {pagination && (
        <div className="flex flex-col gap-2 border-t px-3 py-2 sm:flex-row sm:items-center sm:justify-between">
          {initialLoading ? (
            <>
              <div className="flex flex-wrap items-center gap-2">
                <Skeleton className="h-4 w-16 rounded-sm" />
                <Skeleton className="h-8 w-[130px] rounded-lg" />
              </div>
              <div className="flex items-center gap-2">
                <Skeleton className="size-8 rounded-lg" />
                <Skeleton className="h-4 w-16 rounded-sm" />
                <Skeleton className="size-8 rounded-lg" />
              </div>
            </>
          ) : (
            <>
              <div className="flex flex-wrap items-center gap-2">
                <span className="text-muted-foreground text-sm">
                  {t('common.table.total', { count: pagination.total })}
                </span>
                <Select
                  value={String(pagination.pageSize)}
                  onValueChange={(value) => pagination.onPageSizeChange(Number.parseInt(value, 10))}
                >
                  <SelectTrigger
                    size="sm"
                    className="w-[130px]"
                    aria-label={t('common.table.pageSize', { count: pagination.pageSize })}
                  >
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {pageSizeOptions.map((option) => (
                      <SelectItem key={option} value={String(option)}>
                        {t('common.table.pageSize', { count: option })}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <div className="flex items-center gap-2">
                <Button
                  variant="outline"
                  size="icon-sm"
                  aria-label={t('common.table.prev')}
                  disabled={page <= 0}
                  onClick={() => pagination.onPageChange(page - 1)}
                >
                  <ChevronLeft className="size-4" />
                </Button>
                <span className="text-muted-foreground text-sm">
                  {t('common.table.pageOf', { page: page + 1, pages })}
                </span>
                <Button
                  variant="outline"
                  size="icon-sm"
                  aria-label={t('common.table.next')}
                  disabled={page >= pages - 1}
                  onClick={() => pagination.onPageChange(page + 1)}
                >
                  <ChevronRight className="size-4" />
                </Button>
              </div>
            </>
          )}
        </div>
      )}
    </div>
  );
}
