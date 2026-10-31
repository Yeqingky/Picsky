import { useEffect, useMemo, useState } from 'react';
import { Eye, RefreshCw } from 'lucide-react';
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import type { Dayjs } from 'dayjs';
import { adminLogsApi } from '../../api';
import type { AuditLog } from '../../api';
import { PageHeader } from '../../components/PageHeader';
import { EmptyState, ErrorState } from '../../components/States';
import { DataTable } from '../../components/DataTable';
import type { DataTableColumn } from '../../components/DataTable';
import { DatePicker } from '../../components/DatePicker';
import { Alert, AlertDescription } from '../../components/ui/alert';
import { Button } from '../../components/ui/button';
import { Input } from '../../components/ui/input';
import { Label } from '../../components/ui/label';
import { Sheet, SheetContent, SheetHeader, SheetTitle } from '../../components/ui/sheet';
import { Tooltip, TooltipContent, TooltipTrigger } from '../../components/ui/tooltip';
import { useDebouncedValue } from '../../hooks/useDebouncedValue';
import { useDelayedLoading } from '../../hooks/useDelayedLoading';
import { DEFAULT_TABLE_PAGE_SIZE } from '../../utils/constants';
import { formatDateTime, toRfc3339 } from '../../utils/format';

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

export default function AdminLogsPage() {
  const { t } = useTranslation();

  const [action, setAction] = useState('');
  const [userId, setUserId] = useState('');
  const [from, setFrom] = useState<Dayjs | null>(null);
  const [to, setTo] = useState<Dayjs | null>(null);
  const [paginationModel, setPaginationModel] = useState({
    page: 0,
    pageSize: DEFAULT_TABLE_PAGE_SIZE,
  });
  const [detail, setDetail] = useState<AuditLog | null>(null);

  const debouncedAction = useDebouncedValue(action, 400);
  const trimmedUserId = userId.trim();
  const userIdInvalid = trimmedUserId.length > 0 && !/^\d+$/.test(trimmedUserId);

  useEffect(() => {
    setPaginationModel((prev) => (prev.page === 0 ? prev : { ...prev, page: 0 }));
  }, [debouncedAction, trimmedUserId, from, to]);

  const params = useMemo(
    () => ({
      action: debouncedAction.trim() || undefined,
      user_id: userIdInvalid ? undefined : Number(trimmedUserId) || undefined,
      from: toRfc3339(from),
      to: toRfc3339(to, true),
      page: paginationModel.page + 1,
      page_size: paginationModel.pageSize,
    }),
    [debouncedAction, trimmedUserId, userIdInvalid, from, to, paginationModel],
  );

  const logsQuery = useQuery({
    queryKey: ['admin', 'logs', params],
    queryFn: () => adminLogsApi.list(params),
    placeholderData: keepPreviousData,
  });

  const delayedLoading = useDelayedLoading(logsQuery.isFetching);

  const columns = useMemo<DataTableColumn<AuditLog>[]>(
    () => [
      {
        id: 'id',
        header: t('adminSystem.logs.column.id'),
        className: 'w-[90px]',
        sortable: true,
        sortValue: (row) => row.id,
        cell: (row) => row.id,
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
        id: 'username',
        header: t('adminSystem.logs.column.user'),
        className: 'w-[140px]',
        sortable: true,
        sortValue: (row) => row.username,
        cell: (row) =>
          row.user_id === null ? (
            <span className="text-muted-foreground text-sm">
              {t('adminSystem.logs.unauthenticated')}
            </span>
          ) : (
            (row.username ?? '-')
          ),
      },
      {
        id: 'action',
        header: t('adminSystem.logs.column.action'),
        className: 'w-[180px]',
        sortable: true,
        sortValue: (row) => row.action,
        cell: (row) => <span className="font-mono text-xs">{row.action}</span>,
      },
      {
        id: 'target_type',
        header: t('adminSystem.logs.column.targetType'),
        className: 'w-[130px]',
        sortable: true,
        sortValue: (row) => row.target_type,
        cell: (row) => row.target_type,
      },
      {
        id: 'target_id',
        header: t('adminSystem.logs.column.targetId'),
        className: 'w-[100px]',
        sortable: true,
        sortValue: (row) => row.target_id,
        cell: (row) => (row.target_id === null ? '-' : String(row.target_id)),
      },
      {
        id: 'ip',
        header: t('adminSystem.logs.column.ip'),
        className: 'w-[150px]',
        sortable: true,
        sortValue: (row) => row.ip,
        cell: (row) => <span className="font-mono text-xs">{row.ip ?? '-'}</span>,
      },
      {
        id: 'detail',
        header: t('adminSystem.logs.column.detail'),
        className: 'min-w-[180px]',
        cell: (row) => {
          const value = row.detail ?? '';
          if (value.length === 0) {
            return (
              <span className="text-muted-foreground text-sm">
                {t('adminSystem.logs.detailEmpty')}
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
        id: 'view',
        header: t('common.table.actions'),
        className: 'w-[80px]',
        cell: (row) => (
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
        ),
      },
    ],
    [t],
  );

  const header = (
    <PageHeader
      title={t('adminSystem.logs.title')}
      description={t('adminSystem.logs.description')}
      actions={
        <Button
          variant="outline"
          onClick={() => {
            void logsQuery.refetch();
          }}
          disabled={logsQuery.isFetching}
        >
          <RefreshCw className="size-4" />
          {t('common.action.refresh')}
        </Button>
      }
    />
  );

  const errorMessage = logsQuery.error instanceof Error ? logsQuery.error.message : undefined;

  if (logsQuery.isError && !logsQuery.data) {
    return (
      <>
        {header}
        <ErrorState
          message={errorMessage}
          onRetry={() => {
            void logsQuery.refetch();
          }}
        />
      </>
    );
  }

  const filtersActive = action.length > 0 || userId.length > 0 || from !== null || to !== null;

  return (
    <>
      {header}

      {logsQuery.isError && errorMessage && (
        <Alert variant="destructive" className="mb-4">
          <AlertDescription>{errorMessage}</AlertDescription>
        </Alert>
      )}

      <div className="mb-4 flex flex-wrap items-end gap-3">
        <div className="flex w-full flex-col gap-1.5 md:w-60">
          <Label htmlFor="logs-filter-action">{t('adminSystem.logs.column.action')}</Label>
          <Input
            id="logs-filter-action"
            value={action}
            onChange={(event) => setAction(event.target.value)}
            placeholder={t('adminSystem.logs.actionPlaceholder')}
          />
        </div>

        <div className="flex w-full flex-col gap-1.5 md:w-40">
          <Label htmlFor="logs-filter-user">{t('adminSystem.logs.column.user')}</Label>
          <Input
            id="logs-filter-user"
            value={userId}
            onChange={(event) => setUserId(event.target.value)}
            placeholder={t('adminSystem.logs.userIdPlaceholder')}
            aria-invalid={userIdInvalid}
          />
          {userIdInvalid && (
            <p className="text-destructive text-sm">{t('common.form.invalidNumber')}</p>
          )}
        </div>

        <DatePicker
          label={t('common.time.from')}
          value={from}
          onChange={(next) => setFrom(next)}
          format="YYYY-MM-DD"
          clearable
          className="w-full md:w-44"
        />
        <DatePicker
          label={t('common.time.to')}
          value={to}
          onChange={(next) => setTo(next)}
          format="YYYY-MM-DD"
          minDate={from ?? undefined}
          clearable
          className="w-full md:w-44"
        />

        <Button
          variant="ghost"
          size="sm"
          onClick={() => {
            setAction('');
            setUserId('');
            setFrom(null);
            setTo(null);
            setPaginationModel((prev) => (prev.page === 0 ? prev : { ...prev, page: 0 }));
          }}
          disabled={!filtersActive}
        >
          {t('common.action.reset')}
        </Button>
      </div>

      <DataTable
        columns={columns}
        rows={logsQuery.data?.items ?? []}
        loading={logsQuery.isPending || delayedLoading}
        emptyState={
          <EmptyState
            title={t('adminSystem.logs.emptyTitle')}
            description={t('adminSystem.logs.emptyDescription')}
          />
        }
        pagination={{
          page: paginationModel.page,
          pageSize: paginationModel.pageSize,
          total: logsQuery.data?.total ?? 0,
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
              <SheetTitle>{t('adminSystem.logs.detailTitle', { id: detail.id })}</SheetTitle>
            </SheetHeader>

            <div className="flex flex-col gap-4 overflow-y-auto p-4">
              <DetailRow
                label={t('common.time.createdAt')}
                value={formatDateTime(detail.created_at)}
              />
              <DetailRow
                label={t('adminSystem.logs.column.user')}
                value={
                  detail.user_id === null
                    ? t('adminSystem.logs.unauthenticated')
                    : detail.username
                      ? `${detail.username} (#${detail.user_id})`
                      : '-'
                }
              />
              <DetailRow label={t('adminSystem.logs.column.action')} value={detail.action} />
              <DetailRow
                label={t('adminSystem.logs.column.targetType')}
                value={detail.target_type ?? '-'}
              />
              <DetailRow
                label={t('adminSystem.logs.column.targetId')}
                value={detail.target_id === null ? '-' : String(detail.target_id)}
              />
              <DetailRow label={t('adminSystem.logs.column.ip')} value={detail.ip ?? '-'} />
              <DetailRow label={t('adminSystem.logs.userAgent')} value={detail.user_agent ?? '-'} />

              <div>
                <h3 className="mb-1.5 text-sm font-medium">
                  {t('adminSystem.logs.column.detail')}
                </h3>
                <pre className="bg-muted m-0 max-h-[320px] overflow-auto rounded-lg p-3 font-mono text-xs break-all whitespace-pre-wrap">
                  {prettyJson(detail.detail) || t('adminSystem.logs.detailEmpty')}
                </pre>
              </div>
            </div>
          </SheetContent>
        )}
      </Sheet>
    </>
  );
}
