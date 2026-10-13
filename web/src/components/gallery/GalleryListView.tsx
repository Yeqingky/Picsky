import { useTranslation } from 'react-i18next';
import { Eye } from 'lucide-react';
import type { ImageRecord } from '../../api/types';
import { DataTable } from '../DataTable';
import type { DataTableColumn } from '../DataTable';
import { LazyImage } from '../LazyImage';
import { Button } from '../ui/button';
import { Tooltip, TooltipContent, TooltipTrigger } from '../ui/tooltip';
import { formatBytes, formatDateTime } from '../../utils/format';
import { dimensions } from '../../utils/image';

/** 图库列表视图: DataTable 服务端分页 */
export function GalleryListView({
  rows,
  rowCount,
  loading,
  paginationModel,
  onPaginationModelChange,
  selectionMode,
  selection,
  onSelectionChange,
  onOpen,
}: {
  rows: ImageRecord[];
  rowCount: number;
  loading: boolean;
  paginationModel: { page: number; pageSize: number };
  onPaginationModelChange: (model: { page: number; pageSize: number }) => void;
  selectionMode: boolean;
  selection: number[];
  onSelectionChange: (ids: number[]) => void;
  onOpen: (record: ImageRecord) => void;
}) {
  const { t } = useTranslation();

  const columns: DataTableColumn<ImageRecord>[] = [
    {
      id: 'thumb',
      header: t('gallery.list.thumbnail'),
      className: 'w-20',
      cell: (row) => (
        <div className="h-12 w-12 overflow-hidden rounded">
          <LazyImage src={row.url} alt={row.original_filename} hash={row.sha256} />
        </div>
      ),
    },
    {
      id: 'original_filename',
      header: t('gallery.list.filename'),
      className: 'min-w-[200px]',
      cell: (row) => (
        <div
          className="max-w-[320px] cursor-pointer truncate"
          onClick={() => onOpen(row)}
          title={row.original_filename}
        >
          {row.original_filename}
        </div>
      ),
    },
    {
      id: 'format',
      header: t('gallery.list.format'),
      className: 'w-24',
      cell: (row) => row.format,
    },
    {
      id: 'dimensions',
      header: t('gallery.list.dimensions'),
      className: 'w-32',
      cell: (row) => dimensions(row.width, row.height),
    },
    {
      id: 'size',
      header: t('gallery.list.size'),
      className: 'w-28',
      cell: (row) => formatBytes(row.size),
    },
    {
      id: 'policy_name',
      header: t('gallery.list.policy'),
      className: 'w-36',
      cell: (row) => row.policy_name,
    },
    {
      id: 'created_at',
      header: t('gallery.list.createdAt'),
      className: 'w-44',
      cell: (row) => formatDateTime(row.created_at),
    },
    {
      id: 'actions',
      header: t('common.table.actions'),
      className: 'w-16',
      cell: (row) => (
        <Tooltip>
          <TooltipTrigger asChild>
            <Button
              variant="ghost"
              size="icon-sm"
              aria-label={t('common.action.detail')}
              onClick={() => onOpen(row)}
            >
              <Eye className="size-4" />
            </Button>
          </TooltipTrigger>
          <TooltipContent>{t('common.action.detail')}</TooltipContent>
        </Tooltip>
      ),
    },
  ];

  return (
    <DataTable
      columns={columns}
      rows={rows}
      loading={loading}
      rowHeight={64}
      getRowId={(row) => row.id}
      selectable={selectionMode}
      selectedIds={selection}
      onSelectionChange={(ids) => onSelectionChange(ids.map((id) => Number(id)))}
      pagination={{
        page: paginationModel.page,
        pageSize: paginationModel.pageSize,
        total: rowCount,
        serverSide: true,
        pageSizeOptions: [20, 40, 100],
        onPageChange: (page) => onPaginationModelChange({ ...paginationModel, page }),
        onPageSizeChange: (pageSize) => onPaginationModelChange({ page: 0, pageSize }),
      }}
    />
  );
}
