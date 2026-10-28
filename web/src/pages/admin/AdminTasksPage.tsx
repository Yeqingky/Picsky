import { useCallback, useMemo, useState } from 'react';
import { Eye, Play, RefreshCw, Trash2 } from 'lucide-react';
import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { adminTasksApi } from '../../api';
import type { MaintenanceTask } from '../../api';
import { PageHeader } from '../../components/PageHeader';
import { useConfirm } from '../../components/ConfirmDialog';
import { EmptyState, ErrorState } from '../../components/States';
import { DataTable } from '../../components/DataTable';
import type { DataTableColumn } from '../../components/DataTable';
import { Alert, AlertDescription } from '../../components/ui/alert';
import { Badge } from '../../components/ui/badge';
import { Button } from '../../components/ui/button';
import { Label } from '../../components/ui/label';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '../../components/ui/select';
import { Sheet, SheetContent, SheetHeader, SheetTitle } from '../../components/ui/sheet';
import { Tooltip, TooltipContent, TooltipTrigger } from '../../components/ui/tooltip';
import { useDelayedLoading } from '../../hooks/useDelayedLoading';
import { DEFAULT_TABLE_PAGE_SIZE } from '../../utils/constants';
import { formatDateTime } from '../../utils/format';
import { cn } from '../../lib/utils';

const TASK_TYPES = ['object_delete', 'log_purge', 'policy_scan', 'quota_reconcile'] as const;
const TASK_STATUSES = ['pending', 'running', 'done', 'failed'] as const;

/** Radix Select 不支持空字符串值, 用哨兵表示 "全部" */
const ALL_VALUE = '__all__';

const TASK_TYPE_LABEL_KEYS: Record<string, string> = {
  object_delete: 'adminSystem.tasks.type.object_delete',
  log_purge: 'adminSystem.tasks.type.log_purge',
  policy_scan: 'adminSystem.tasks.type.policy_scan',
  quota_reconcile: 'adminSystem.tasks.type.quota_reconcile',
};

const TASK_STATUS_LABEL_KEYS: Record<string, string> = {
  pending: 'common.status.pending',
  running: 'common.status.running',
  done: 'common.status.done',
  failed: 'common.status.failed',
};

/** 状态徽标语义色: pending 保持中性 outline, 其余用状态令牌 */
const TASK_STATUS_CLASSES: Record<string, string> = {
  running: 'border-transparent bg-info/10 text-info',
  done: 'border-transparent bg-success/10 text-success',
  failed: 'border-transparent bg-destructive/10 text-destructive',
};

function prettyJson(raw: string | null): string {
  if (!raw) return '';
  try {
    return JSON.stringify(JSON.parse(raw), null, 2);
  } catch {
    return raw;
  }
}

function DetailRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex flex-col gap-0.5 text-sm sm:flex-row sm:gap-4">
      <span className="text-muted-foreground sm:w-24 sm:shrink-0">{label}</span>
      <span className="min-w-0 flex-1 break-all">{value}</span>
    </div>
  );
}

export default function AdminTasksPage() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const confirm = useConfirm();

  const [type, setType] = useState('');
  const [status, setStatus] = useState('');
  const [paginationModel, setPaginationModel] = useState({
    page: 0,
    pageSize: DEFAULT_TABLE_PAGE_SIZE,
  });
  const [detail, setDetail] = useState<MaintenanceTask | null>(null);

  const params = useMemo(
    () => ({
      type: type || undefined,
      status: status || undefined,
      page: paginationModel.page + 1,
      page_size: paginationModel.pageSize,
    }),
    [type, status, paginationModel],
  );

  const tasksQuery = useQuery({
    queryKey: ['admin', 'tasks', params],
    queryFn: () => adminTasksApi.list(params),
    placeholderData: keepPreviousData,
    refetchInterval: (query) =>
      query.state.data?.items.some((task) => task.status === 'pending' || task.status === 'running')
        ? 3000
        : false,
  });

  const delayedLoading = useDelayedLoading(tasksQuery.isFetching);

  const runMutation = useMutation({
    mutationFn: (id: number) => adminTasksApi.run(id),
    onSuccess: async (_result, id) => {
      await queryClient.invalidateQueries({ queryKey: ['admin', 'tasks'] });
      toast.success(t('adminSystem.tasks.runSuccess', { id }));
    },
  });

  const runTask = runMutation.mutate;
  const running = runMutation.isPending;

  const deleteMutation = useMutation({
    mutationFn: (id: number) => adminTasksApi.delete(id),
    onSuccess: async (_result, id) => {
      await queryClient.invalidateQueries({ queryKey: ['admin', 'tasks'] });
      toast.success(t('adminSystem.tasks.deleteSuccess', { id }));
    },
  });

  const deleteTask = deleteMutation.mutate;
  const deleting = deleteMutation.isPending;
  const requestDelete = useCallback(
    async (task: MaintenanceTask) => {
      const ok = await confirm({
        title: t('adminSystem.tasks.deleteTitle'),
        description: t('adminSystem.tasks.deleteDescription', { id: task.id }),
        danger: true,
      });
      if (ok) deleteTask(task.id);
    },
    [confirm, deleteTask, t],
  );

  const columns = useMemo<DataTableColumn<MaintenanceTask>[]>(
    () => [
      {
        id: 'id',
        header: t('adminSystem.tasks.column.id'),
        className: 'w-[90px]',
        sortable: true,
        sortValue: (row) => row.id,
        cell: (row) => row.id,
      },
      {
        id: 'type',
        header: t('adminSystem.tasks.column.type'),
        className: 'w-[130px]',
        sortable: true,
        sortValue: (row) => row.type,
        cell: (row) => (
          <Badge variant="outline">
            {TASK_TYPE_LABEL_KEYS[row.type] ? t(TASK_TYPE_LABEL_KEYS[row.type]) : row.type}
          </Badge>
        ),
      },
      {
        id: 'status',
        header: t('adminSystem.tasks.column.status'),
        className: 'w-[110px]',
        sortable: true,
        sortValue: (row) => row.status,
        cell: (row) => (
          <Badge variant="outline" className={TASK_STATUS_CLASSES[row.status]}>
            {TASK_STATUS_LABEL_KEYS[row.status]
              ? t(TASK_STATUS_LABEL_KEYS[row.status])
              : row.status}
          </Badge>
        ),
      },
      {
        id: 'attempts',
        header: t('adminSystem.tasks.column.attempts'),
        className: 'w-[100px]',
        align: 'right',
        sortable: true,
        sortValue: (row) => row.attempts,
        cell: (row) => row.attempts,
      },
      {
        id: 'payload',
        header: t('adminSystem.tasks.column.payload'),
        className: 'min-w-[180px]',
        cell: (row) => {
          const value = row.payload ?? '';
          if (value.length === 0) {
            return (
              <span className="text-muted-foreground text-sm">
                {t('adminSystem.tasks.payloadEmpty')}
              </span>
            );
          }
          return (
            <Tooltip>
              <TooltipTrigger asChild>
                <span className="block max-w-[320px] truncate font-mono text-xs">{value}</span>
              </TooltipTrigger>
              <TooltipContent className="max-w-[480px] font-mono text-xs break-all whitespace-pre-wrap">
                {value}
              </TooltipContent>
            </Tooltip>
          );
        },
      },
      {
        id: 'last_error',
        header: t('adminSystem.tasks.column.lastError'),
        className: 'min-w-[180px]',
        cell: (row) => {
          const value = row.last_error ?? '';
          if (value.length === 0) {
            return (
              <span className="text-muted-foreground text-sm">
                {t('adminSystem.tasks.lastErrorEmpty')}
              </span>
            );
          }
          return (
            <Tooltip>
              <TooltipTrigger asChild>
                <span className="text-destructive block max-w-[320px] truncate text-sm">
                  {value}
                </span>
              </TooltipTrigger>
              <TooltipContent className="max-w-[480px] text-xs break-all whitespace-pre-wrap">
                {value}
              </TooltipContent>
            </Tooltip>
          );
        },
      },
      {
        id: 'created_at',
        header: t('common.time.createdAt'),
        className: 'w-[180px]',
        sortable: true,
        sortValue: (row) => row.created_at,
        cell: (row) => formatDateTime(row.created_at),
      },
      {
        id: 'finished_at',
        header: t('adminSystem.tasks.column.finishedAt'),
        className: 'w-[180px]',
        sortable: true,
        sortValue: (row) => row.finished_at,
        cell: (row) => (row.finished_at ? formatDateTime(row.finished_at) : '—'),
      },
      {
        id: 'actions',
        header: t('common.table.actions'),
        className: 'w-[150px]',
        cell: (row) => (
          <div className="flex items-center gap-1">
            <Tooltip>
              <TooltipTrigger asChild>
                <span>
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    aria-label={t('common.action.run')}
                    disabled={row.status === 'running' || running}
                    onClick={() => runTask(row.id)}
                  >
                    <Play className="size-4" />
                  </Button>
                </span>
              </TooltipTrigger>
              <TooltipContent>{t('common.action.run')}</TooltipContent>
            </Tooltip>
            {row.status === 'failed' && (
              <Tooltip>
                <TooltipTrigger asChild>
                  <span>
                    <Button
                      variant="ghost"
                      size="icon-sm"
                      aria-label={t('common.action.delete')}
                      disabled={deleting}
                      onClick={() => {
                        void requestDelete(row);
                      }}
                    >
                      <Trash2 className="size-4" />
                    </Button>
                  </span>
                </TooltipTrigger>
                <TooltipContent>{t('common.action.delete')}</TooltipContent>
              </Tooltip>
            )}
            <Tooltip>
              <TooltipTrigger asChild>
                <Button
                  variant="ghost"
                  size="icon-sm"
                  aria-label={t('common.action.detail')}
                  onClick={() => setDetail(row)}
                >
                  <Eye className="size-4" />
                </Button>
              </TooltipTrigger>
              <TooltipContent>{t('common.action.detail')}</TooltipContent>
            </Tooltip>
          </div>
        ),
      },
    ],
    [t, running, runTask, deleting, requestDelete],
  );

  const header = (
    <PageHeader
      title={t('adminSystem.tasks.title')}
      description={t('adminSystem.tasks.description')}
      actions={
        <>
          <Button
            variant="outline"
            onClick={() => {
              void tasksQuery.refetch();
            }}
            disabled={tasksQuery.isFetching}
          >
            <RefreshCw className="size-4" />
            {t('common.action.refresh')}
          </Button>
        </>
      }
    />
  );

  const errorMessage = tasksQuery.error instanceof Error ? tasksQuery.error.message : undefined;

  if (tasksQuery.isError && !tasksQuery.data) {
    return (
      <>
        {header}
        <ErrorState
          message={errorMessage}
          onRetry={() => {
            void tasksQuery.refetch();
          }}
        />
      </>
    );
  }

  return (
    <>
      {header}

      {tasksQuery.isError && errorMessage && (
        <Alert variant="destructive" className="mb-4">
          <AlertDescription>{errorMessage}</AlertDescription>
        </Alert>
      )}

      <div className="mb-4 flex flex-wrap items-end gap-3">
        <div className="flex w-full flex-col gap-1.5 md:w-50">
          <Label htmlFor="tasks-filter-type">{t('adminSystem.tasks.column.type')}</Label>
          <Select
            value={type === '' ? ALL_VALUE : type}
            onValueChange={(next) => {
              setType(next === ALL_VALUE ? '' : next);
              setPaginationModel((prev) => ({ ...prev, page: 0 }));
            }}
          >
            <SelectTrigger
              id="tasks-filter-type"
              className="w-full"
              aria-label={t('adminSystem.tasks.column.type')}
            >
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={ALL_VALUE}>{t('adminSystem.tasks.typeAll')}</SelectItem>
              {TASK_TYPES.map((value) => (
                <SelectItem key={value} value={value}>
                  {t(TASK_TYPE_LABEL_KEYS[value])}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>

        <div className="flex w-full flex-col gap-1.5 md:w-50">
          <Label htmlFor="tasks-filter-status">{t('adminSystem.tasks.column.status')}</Label>
          <Select
            value={status === '' ? ALL_VALUE : status}
            onValueChange={(next) => {
              setStatus(next === ALL_VALUE ? '' : next);
              setPaginationModel((prev) => ({ ...prev, page: 0 }));
            }}
          >
            <SelectTrigger
              id="tasks-filter-status"
              className="w-full"
              aria-label={t('adminSystem.tasks.column.status')}
            >
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={ALL_VALUE}>{t('adminSystem.tasks.statusAll')}</SelectItem>
              {TASK_STATUSES.map((value) => (
                <SelectItem key={value} value={value}>
                  {t(TASK_STATUS_LABEL_KEYS[value])}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      </div>

      <DataTable
        columns={columns}
        rows={tasksQuery.data?.items ?? []}
        loading={tasksQuery.isPending || delayedLoading}
        emptyState={
          <EmptyState
            title={t('adminSystem.tasks.emptyTitle')}
            description={t('adminSystem.tasks.emptyDescription')}
          />
        }
        pagination={{
          page: paginationModel.page,
          pageSize: paginationModel.pageSize,
          total: tasksQuery.data?.total ?? 0,
          serverSide: true,
          pageSizeOptions: [20, 50, 100],
          onPageChange: (page) => setPaginationModel((prev) => ({ ...prev, page })),
          onPageSizeChange: (pageSize) => setPaginationModel({ page: 0, pageSize }),
        }}
      />

      <Sheet
        open={detail !== null}
        onOpenChange={(next) => {
          if (!next) setDetail(null);
        }}
      >
        {detail && (
          <SheetContent
            side="right"
            className="w-full gap-0 p-0 data-[side=right]:w-full data-[side=right]:sm:max-w-[520px]"
          >
            <SheetHeader className="border-b">
              <SheetTitle>{t('adminSystem.tasks.detailTitle', { id: detail.id })}</SheetTitle>
            </SheetHeader>

            <div className="flex flex-col gap-4 overflow-y-auto p-4">
              <DetailRow
                label={t('adminSystem.tasks.column.type')}
                value={
                  TASK_TYPE_LABEL_KEYS[detail.type]
                    ? t(TASK_TYPE_LABEL_KEYS[detail.type])
                    : detail.type
                }
              />
              <DetailRow
                label={t('adminSystem.tasks.column.status')}
                value={
                  TASK_STATUS_LABEL_KEYS[detail.status]
                    ? t(TASK_STATUS_LABEL_KEYS[detail.status])
                    : detail.status
                }
              />
              <DetailRow
                label={t('adminSystem.tasks.column.attempts')}
                value={String(detail.attempts)}
              />
              <DetailRow
                label={t('common.time.createdAt')}
                value={formatDateTime(detail.created_at)}
              />
              <DetailRow
                label={t('adminSystem.tasks.column.finishedAt')}
                value={detail.finished_at ? formatDateTime(detail.finished_at) : '—'}
              />

              <div>
                <h3 className="mb-1.5 text-sm font-medium">
                  {t('adminSystem.tasks.column.payload')}
                </h3>
                <pre className="bg-muted m-0 max-h-[280px] overflow-auto rounded-lg p-3 font-mono text-xs break-all whitespace-pre-wrap">
                  {prettyJson(detail.payload) || t('adminSystem.tasks.payloadEmpty')}
                </pre>
              </div>

              <div>
                <h3 className="mb-1.5 text-sm font-medium">
                  {t('adminSystem.tasks.column.lastError')}
                </h3>
                <pre
                  className={cn(
                    'bg-muted m-0 max-h-[280px] overflow-auto rounded-lg p-3 font-mono text-xs break-all whitespace-pre-wrap',
                    detail.last_error ? 'text-destructive' : 'text-muted-foreground',
                  )}
                >
                  {detail.last_error || t('adminSystem.tasks.lastErrorEmpty')}
                </pre>
              </div>
            </div>
          </SheetContent>
        )}
      </Sheet>
    </>
  );
}
