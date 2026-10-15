import type { JSX, ReactNode } from 'react';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { ChevronDown, ChevronLeft, ChevronRight, ChevronsUpDown, ChevronUp } from 'lucide-react';
import { cn } from '../lib/utils';
import { EmptyState } from './States';
import { Button } from './ui/button';
import { Checkbox } from './ui/checkbox';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from './ui/select';
import { Skeleton } from './ui/skeleton';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from './ui/table';

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
}: DataTableProps<T>): JSX.Element {
  const { t } = useTranslation();

  const resolveId = (row: T): string | number => {
    if (getRowId) return getRowId(row);
    if (row !== null && typeof row === 'object' && 'id' in row) {
      const id = row.id;
      if (typeof id === 'string' || typeof id === 'number') return id;
    }
    return '';
  };

  const rawSelected = selectedIds ?? [];
  const isSelected = (row: T) => rawSelected.some((id) => String(id) === String(resolveId(row)));

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
    if (!sort || !getValue) return rows;
    return [...rows].sort((left, right) => {
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

  const columnCount = columns.length + (selectable ? 1 : 0);
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
            visibleRows.map((row) => {
              const selected = isSelected(row);
              return (
                <TableRow
                  key={String(resolveId(row))}
                  style={{ height: rowHeight }}
                  className={cn('hover:bg-muted/40', selected && 'bg-muted/60')}
                >
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
                </TableRow>
              );
            })
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
