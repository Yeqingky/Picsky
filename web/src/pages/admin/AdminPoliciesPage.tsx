import { useCallback, useMemo, useState } from 'react';
import { useNavigate } from 'react-router';
import { Pencil, Plus, ScanSearch, Trash2 } from 'lucide-react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import { useTranslation } from 'react-i18next';
import { adminPoliciesApi } from '../../api';
import type { AdminPolicy } from '../../api/types';
import { useConfirm } from '../../components/ConfirmDialog';
import { DataTable } from '../../components/DataTable';
import type { DataTableColumn } from '../../components/DataTable';
import { PageHeader } from '../../components/PageHeader';
import { EmptyState, ErrorState } from '../../components/States';
import { Badge } from '../../components/ui/badge';
import { Button } from '../../components/ui/button';
import { Switch } from '../../components/ui/switch';
import { Tooltip, TooltipContent, TooltipTrigger } from '../../components/ui/tooltip';
import { formatDateTime } from '../../utils/format';

export default function AdminPoliciesPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const confirm = useConfirm();

  const [page, setPage] = useState(0);
  const [pageSize, setPageSize] = useState(20);

  const policiesQuery = useQuery({
    queryKey: ['admin', 'policies'],
    queryFn: () => adminPoliciesApi.list(),
  });

  const removeMutation = useMutation({
    mutationFn: (id: number) => adminPoliciesApi.remove(id),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ['admin', 'policies'] });
      toast.success(t('common.state.success'));
    },
  });

  const scanMutation = useMutation({
    mutationFn: (id: number) => adminPoliciesApi.scan(id),
    onSuccess: async () => {
      toast.success(t('adminPolicies.list.scanSubmitted'));
      await queryClient.invalidateQueries({ queryKey: ['admin', 'tasks'] });
    },
  });

  const reorderMutation = useMutation({
    mutationFn: (ids: number[]) => adminPoliciesApi.reorder(ids),
    onMutate: async (ids) => {
      await queryClient.cancelQueries({ queryKey: ['admin', 'policies'] });
      const previous = queryClient.getQueryData<AdminPolicy[]>(['admin', 'policies']);
      if (previous !== undefined) {
        const byId = new Map(previous.map((policy) => [policy.id, policy]));
        const next = ids
          .map((id) => byId.get(id))
          .filter((policy): policy is AdminPolicy => policy !== undefined);
        if (next.length === previous.length) {
          queryClient.setQueryData(['admin', 'policies'], next);
        }
      }
      return { previous };
    },
    onError: (_error, _ids, context) => {
      if (context?.previous !== undefined) {
        queryClient.setQueryData(['admin', 'policies'], context.previous);
      }
    },
    onSettled: async () => {
      await queryClient.invalidateQueries({ queryKey: ['admin', 'policies'] });
    },
  });

  const { mutate: removePolicy } = removeMutation;
  const { mutate: scanPolicy } = scanMutation;
  const { mutate: reorderPolicies } = reorderMutation;
  const toggleMutation = useMutation({
    mutationFn: ({ id, enabled }: { id: number; enabled: boolean }) =>
      adminPoliciesApi.update(id, { enabled }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ['admin', 'policies'] });
    },
  });
  const { mutate: togglePolicy } = toggleMutation;

  const handleReorder = useCallback(
    (ids: (string | number)[]) => {
      reorderPolicies(ids.map((id) => Number(id)));
    },
    [reorderPolicies],
  );

  const handleDelete = useCallback(
    async (policy: AdminPolicy) => {
      const confirmed = await confirm({
        danger: true,
        title: t('adminPolicies.list.deleteTitle', { name: policy.name }),
        description: t('common.confirm.deletePolicy'),
      });
      if (!confirmed) return;
      removePolicy(policy.id);
    },
    [confirm, removePolicy, t],
  );

  const columns = useMemo<DataTableColumn<AdminPolicy>[]>(
    () => [
      {
        id: 'name',
        header: t('adminPolicies.list.col.name'),
        className: 'min-w-[160px] max-w-[280px]',
        sortable: true,
        sortValue: (row) => row.name,
        cell: (row) => <p className="truncate text-sm font-semibold">{row.name}</p>,
      },
      {
        id: 'type',
        header: t('adminPolicies.list.col.type'),
        className: 'w-[130px]',
        sortable: true,
        sortValue: (row) => row.type,
        cell: (row) => (
          <Badge variant="outline">
            {t(`adminPolicies.type.${row.type}`, { defaultValue: row.type })}
          </Badge>
        ),
      },
      {
        id: 'conflict_policy',
        header: t('adminPolicies.list.col.conflict'),
        className: 'w-[150px]',
        sortable: true,
        sortValue: (row) => row.conflict_policy,
        cell: (row) =>
          row.url_from_backend ? (
            <span className="text-muted-foreground text-sm">
              {t('adminPolicies.list.notApplicable')}
            </span>
          ) : (
            <p className="text-sm">{t(`adminPolicies.conflict.${row.conflict_policy}`)}</p>
          ),
      },
      {
        id: 'urls',
        header: t('adminPolicies.list.col.urls'),
        className: 'w-[200px]',
        cell: (row) => {
          if (row.url_from_backend) {
            return (
              <span className="text-muted-foreground text-sm">
                {t('adminPolicies.list.notApplicable')}
              </span>
            );
          }
          const urls = row.urls;
          if (urls.length === 0) {
            return <span className="text-muted-foreground text-sm">-</span>;
          }
          const defaultUrl = urls.find((item) => item.is_default) ?? urls[0];
          return (
            <Tooltip>
              <TooltipTrigger asChild>
                <span className="flex min-w-0 items-center gap-1">
                  <span className="text-sm">
                    {t('adminPolicies.list.urlsCount', { count: urls.length })}
                  </span>
                  <Badge>{defaultUrl.alias || t('common.defaultAlias')}</Badge>
                </span>
              </TooltipTrigger>
              <TooltipContent>{defaultUrl.url}</TooltipContent>
            </Tooltip>
          );
        },
      },
      {
        id: 'updated_at',
        header: t('adminPolicies.list.col.updatedAt'),
        className: 'w-[180px]',
        sortable: true,
        sortValue: (row) => row.updated_at,
        cell: (row) => <span className="text-sm">{formatDateTime(row.updated_at)}</span>,
      },
      {
        id: 'actions',
        header: t('adminPolicies.list.col.actions'),
        className: 'w-[150px]',
        cell: (row) => {
          const scannable = row.capabilities.includes('list') && row.capabilities.includes('read');
          const scanning = scanMutation.isPending && scanMutation.variables === row.id;
          const toggling = toggleMutation.isPending && toggleMutation.variables?.id === row.id;
          return (
            <div className="flex items-center gap-1">
              <Tooltip>
                <TooltipTrigger asChild>
                  <span className="inline-flex">
                    <Switch
                      checked={row.enabled}
                      disabled={toggling}
                      onCheckedChange={(checked) => togglePolicy({ id: row.id, enabled: checked })}
                      aria-label={
                        row.enabled
                          ? t('adminPolicies.list.disable')
                          : t('adminPolicies.list.enable')
                      }
                    />
                  </span>
                </TooltipTrigger>
                <TooltipContent>
                  {row.enabled ? t('adminPolicies.list.disable') : t('adminPolicies.list.enable')}
                </TooltipContent>
              </Tooltip>
              <Tooltip>
                <TooltipTrigger asChild>
                  <span className="inline-flex">
                    <Button
                      variant="ghost"
                      size="icon-sm"
                      aria-label={t('adminPolicies.list.scan')}
                      disabled={!scannable || scanning}
                      onClick={() => scanPolicy(row.id)}
                    >
                      <ScanSearch className="size-4" />
                    </Button>
                  </span>
                </TooltipTrigger>
                <TooltipContent>
                  {scannable
                    ? t('adminPolicies.list.scan')
                    : t('adminPolicies.list.scanUnsupported')}
                </TooltipContent>
              </Tooltip>
              <Tooltip>
                <TooltipTrigger asChild>
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    aria-label={t('common.action.edit')}
                    onClick={() => navigate(`/admin/policies/${row.id}`)}
                  >
                    <Pencil className="size-4" />
                  </Button>
                </TooltipTrigger>
                <TooltipContent>{t('common.action.edit')}</TooltipContent>
              </Tooltip>
              <Tooltip>
                <TooltipTrigger asChild>
                  <span className="inline-flex">
                    <Button
                      variant="ghost"
                      size="icon-sm"
                      className="text-destructive"
                      aria-label={t('common.action.delete')}
                      disabled={removeMutation.isPending && removeMutation.variables === row.id}
                      onClick={() => {
                        void handleDelete(row);
                      }}
                    >
                      <Trash2 className="size-4" />
                    </Button>
                  </span>
                </TooltipTrigger>
                <TooltipContent>{t('common.action.delete')}</TooltipContent>
              </Tooltip>
            </div>
          );
        },
      },
    ],
    [
      t,
      navigate,
      handleDelete,
      removeMutation.isPending,
      removeMutation.variables,
      scanMutation.isPending,
      scanMutation.variables,
      scanPolicy,
      toggleMutation.isPending,
      toggleMutation.variables,
      togglePolicy,
    ],
  );

  const createButton = (
    <Button onClick={() => navigate('/admin/policies/new')}>
      <Plus className="size-4" />
      {t('adminPolicies.list.create')}
    </Button>
  );

  return (
    <div>
      <PageHeader
        title={t('adminPolicies.list.title')}
        description={t('adminPolicies.list.description')}
        actions={createButton}
      />

      {policiesQuery.isError ? (
        <ErrorState
          message={
            policiesQuery.error instanceof Error
              ? policiesQuery.error.message
              : t('adminPolicies.list.loadFailed')
          }
          onRetry={() => {
            void policiesQuery.refetch();
          }}
        />
      ) : (
        <DataTable<AdminPolicy>
          columns={columns}
          rows={policiesQuery.data ?? []}
          loading={policiesQuery.isPending}
          getRowId={(row) => row.id}
          onReorder={handleReorder}
          reorderDisabled={reorderMutation.isPending}
          pagination={{
            page,
            pageSize,
            total: policiesQuery.data?.length ?? 0,
            onPageChange: setPage,
            onPageSizeChange: (nextPageSize) => {
              setPageSize(nextPageSize);
              setPage(0);
            },
          }}
          emptyState={
            <EmptyState
              title={t('adminPolicies.list.empty')}
              description={t('adminPolicies.list.emptyHint')}
              action={createButton}
            />
          }
        />
      )}
    </div>
  );
}
