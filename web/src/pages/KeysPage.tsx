import { useState } from 'react';
import { Plus } from 'lucide-react';
import { toast } from 'sonner';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import type { Dayjs } from 'dayjs';
import { ApiError, PERMISSION_NAMES, keysApi } from '../api';
import type { ApiKeyCreateRequest, ApiKeyCreated, ApiKeyRecord, PermissionName } from '../api';
import { PageHeader } from '../components/PageHeader';
import { CopyButton } from '../components/CopyButton';
import { EmptyState, ErrorState } from '../components/States';
import { useConfirm } from '../components/ConfirmDialog';
import { DataTable } from '../components/DataTable';
import type { DataTableColumn } from '../components/DataTable';
import { DateTimePicker } from '../components/DatePicker';
import { Alert, AlertDescription } from '../components/ui/alert';
import { Badge } from '../components/ui/badge';
import { Button } from '../components/ui/button';
import { Checkbox } from '../components/ui/checkbox';
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '../components/ui/dialog';
import { Input } from '../components/ui/input';
import { Label } from '../components/ui/label';
import { useAuth } from '../hooks/useAuth';
import { useDelayedLoading } from '../hooks/useDelayedLoading';
import { dayjs, formatDateTime } from '../utils/format';

const KEYS_QUERY_KEY = ['keys'] as const;
const DEFAULT_PAGE_SIZE = 20;

type KeyStatus = 'active' | 'revoked' | 'expired';

function resolveStatus(record: ApiKeyRecord): KeyStatus {
  if (record.revoked_at) return 'revoked';
  if (record.expires_at && dayjs(record.expires_at).isBefore(dayjs())) return 'expired';
  return 'active';
}

/** 密钥状态徽标: 有效 (绿) / 已过期 (黄) / 已吊销 (灰) */
function StatusBadge({ status }: { status: KeyStatus }) {
  const { t } = useTranslation();
  const label =
    status === 'active'
      ? t('account.keys.statusActive')
      : status === 'expired'
        ? t('account.keys.statusExpired')
        : t('account.keys.statusRevoked');
  if (status === 'active')
    return <Badge className="border-transparent bg-success/10 text-success">{label}</Badge>;
  if (status === 'expired')
    return <Badge className="border-transparent bg-warning/10 text-warning">{label}</Badge>;
  return <Badge variant="outline">{label}</Badge>;
}

/** API 密钥页 (/keys, 权限位 manage_keys) */
export default function KeysPage() {
  const { t } = useTranslation();
  const { permissions } = useAuth();
  const confirm = useConfirm();
  const queryClient = useQueryClient();

  const [createOpen, setCreateOpen] = useState(false);
  const [name, setName] = useState('');
  const [selectedScopes, setSelectedScopes] = useState<PermissionName[]>([]);
  const [expiresAt, setExpiresAt] = useState<Dayjs | null>(null);
  const [nameError, setNameError] = useState<string | undefined>(undefined);
  const [created, setCreated] = useState<ApiKeyCreated | null>(null);
  const [page, setPage] = useState(0);
  const [pageSize, setPageSize] = useState(DEFAULT_PAGE_SIZE);
  // 后端把 secret 作为完整凭据返回 (access_key.secret); 兼容只返回 secret 部分的实现
  const fullKeyValue = created
    ? created.secret.startsWith(`${created.access_key}.`)
      ? created.secret
      : `${created.access_key}.${created.secret}`
    : '';

  const listQuery = useQuery({ queryKey: KEYS_QUERY_KEY, queryFn: keysApi.list });
  const showRefreshSkeleton = useDelayedLoading(listQuery.isFetching && !listQuery.isPending);

  const createMutation = useMutation({
    mutationFn: (body: ApiKeyCreateRequest) => keysApi.create(body),
    onSuccess: (data) => {
      setCreated(data);
      toast.success(t('account.keys.created'));
    },
  });

  const revokeMutation = useMutation({
    mutationFn: (id: number) => keysApi.revoke(id),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: KEYS_QUERY_KEY });
      toast.success(t('account.keys.revokeSuccess'));
    },
  });

  const availableScopes = PERMISSION_NAMES.filter((scope) => permissions.includes(scope));

  const openCreateDialog = () => {
    setCreated(null);
    createMutation.reset();
    setCreateOpen(true);
  };

  const closeCreateDialog = () => {
    setCreateOpen(false);
    setName('');
    setSelectedScopes([]);
    setExpiresAt(null);
    setNameError(undefined);
    createMutation.reset();
    if (created) {
      setCreated(null);
      void queryClient.invalidateQueries({ queryKey: KEYS_QUERY_KEY });
    }
  };

  const handleCreate = () => {
    const trimmedName = name.trim();
    if (trimmedName === '') {
      setNameError(t('account.keys.nameRequired'));
      return;
    }
    setNameError(undefined);
    createMutation.mutate({
      name: trimmedName,
      scopes: selectedScopes.length > 0 ? selectedScopes : undefined,
      expires_at: expiresAt ? expiresAt.toISOString() : undefined,
    });
  };

  const handleRevoke = async (record: ApiKeyRecord) => {
    const confirmed = await confirm({
      title: t('common.confirm.deleteKey'),
      danger: true,
      confirmText: t('account.keys.revoke'),
    });
    if (!confirmed) return;
    revokeMutation.mutate(record.id);
  };

  const records = listQuery.data ?? [];
  const createError = createMutation.error;

  const columns: DataTableColumn<ApiKeyRecord>[] = [
    {
      id: 'name',
      header: t('account.keys.name'),
      className: 'max-w-[220px]',
      cell: (record) => {
        const status = resolveStatus(record);
        return (
          <div className="flex min-w-0 items-center gap-2">
            <span className="truncate font-medium">{record.name}</span>
            <StatusBadge status={status} />
          </div>
        );
      },
    },
    {
      id: 'accessKey',
      header: t('account.keys.accessKey'),
      className: 'max-w-[280px]',
      cell: (record) => (
        <div className="flex min-w-0 items-center gap-1">
          <span className="font-mono text-sm break-all">{record.access_key}</span>
          <CopyButton value={record.access_key} />
        </div>
      ),
    },
    {
      id: 'scopes',
      header: t('account.keys.scopes'),
      className: 'max-w-[280px]',
      cell: (record) =>
        record.scopes === null || record.scopes.length === 0 ? (
          <Badge variant="outline">{t('account.keys.scopesFollowUser')}</Badge>
        ) : (
          <div className="flex flex-wrap gap-1">
            {record.scopes.map((scope) => (
              <Badge key={scope} variant="secondary">
                {(PERMISSION_NAMES as readonly string[]).includes(scope)
                  ? t(`permissions.name.${scope}`)
                  : scope}
              </Badge>
            ))}
          </div>
        ),
    },
    {
      id: 'expiresAt',
      header: t('account.keys.expiresAt'),
      cell: (record) =>
        record.expires_at ? formatDateTime(record.expires_at) : t('account.keys.neverExpires'),
    },
    {
      id: 'lastUsedAt',
      header: t('account.keys.lastUsedAt'),
      cell: (record) => formatDateTime(record.last_used_at),
    },
    {
      id: 'createdAt',
      header: t('account.keys.createdAt'),
      cell: (record) => formatDateTime(record.created_at),
    },
    {
      id: 'actions',
      header: '',
      align: 'right',
      cell: (record) => (
        <Button
          variant="destructive"
          size="sm"
          disabled={resolveStatus(record) === 'revoked' || revokeMutation.isPending}
          onClick={() => void handleRevoke(record)}
        >
          {t('account.keys.revoke')}
        </Button>
      ),
    },
  ];

  const listBody = (
    <DataTable
      columns={columns}
      rows={records}
      loading={listQuery.isPending || showRefreshSkeleton}
      pagination={{
        page,
        pageSize,
        total: records.length,
        onPageChange: setPage,
        onPageSizeChange: (next) => {
          setPageSize(next);
          setPage(0);
        },
      }}
      emptyState={
        <EmptyState
          title={t('account.keys.emptyTitle')}
          description={t('account.keys.emptyDescription')}
          action={
            <Button onClick={openCreateDialog}>
              <Plus className="size-4" />
              {t('account.keys.create')}
            </Button>
          }
        />
      }
    />
  );

  return (
    <>
      <PageHeader
        title={t('nav.keys')}
        description={t('account.keys.description')}
        actions={
          <Button onClick={openCreateDialog}>
            <Plus className="size-4" />
            {t('account.keys.create')}
          </Button>
        }
      />

      {listQuery.isError ? (
        <ErrorState
          message={listQuery.error instanceof ApiError ? listQuery.error.message : undefined}
          onRetry={() => void listQuery.refetch()}
        />
      ) : (
        listBody
      )}

      <Dialog
        open={createOpen}
        onOpenChange={(next) => {
          if (!next && !createMutation.isPending) closeCreateDialog();
        }}
      >
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>
              {created ? t('account.keys.secretTitle') : t('account.keys.createTitle')}
            </DialogTitle>
          </DialogHeader>

          {created ? (
            <div className="flex flex-col gap-4">
              <Alert className="border-warning/30 bg-warning/10">
                <AlertDescription className="text-warning">
                  {t('account.keys.secretWarning')}
                </AlertDescription>
              </Alert>
              <div className="flex flex-col gap-1.5">
                <span className="text-muted-foreground text-xs">{t('account.keys.fullKey')}</span>
                <div className="flex items-center gap-2">
                  <span className="flex-1 font-mono text-sm break-all">{fullKeyValue}</span>
                  <CopyButton value={fullKeyValue} />
                </div>
              </div>
            </div>
          ) : (
            <div className="flex flex-col gap-4">
              {createError && (
                <Alert variant="destructive">
                  <AlertDescription>
                    {createError instanceof ApiError
                      ? createError.message
                      : t('common.error.unknown')}
                  </AlertDescription>
                </Alert>
              )}

              <div className="flex flex-col gap-2">
                <Label htmlFor="key-name">{t('account.keys.name')}</Label>
                <Input
                  id="key-name"
                  value={name}
                  onChange={(event) => setName(event.target.value)}
                  aria-invalid={Boolean(nameError)}
                  autoFocus
                />
                {nameError && <p className="text-destructive text-sm">{nameError}</p>}
              </div>

              <div className="flex flex-col gap-1.5">
                <span className="text-sm font-medium">{t('account.keys.scopes')}</span>
                <div className="flex flex-col gap-2">
                  {availableScopes.map((scope) => (
                    <label key={scope} className="flex items-center gap-2 text-sm">
                      <Checkbox
                        checked={selectedScopes.includes(scope)}
                        onCheckedChange={(checked) =>
                          setSelectedScopes((prev) =>
                            checked === true
                              ? [...prev, scope]
                              : prev.filter((item) => item !== scope),
                          )
                        }
                      />
                      {t(`permissions.name.${scope}`)}
                    </label>
                  ))}
                </div>
                <p className="text-muted-foreground text-sm">{t('account.keys.scopesHint')}</p>
              </div>

              <div className="flex flex-col gap-1.5">
                <DateTimePicker
                  label={t('account.keys.expiresAt')}
                  value={expiresAt}
                  onChange={(next) => setExpiresAt(next)}
                  fullWidth
                />
                <p className="text-muted-foreground text-sm">
                  {t('account.keys.neverExpiresHint')}
                </p>
              </div>
            </div>
          )}

          <DialogFooter>
            {created ? (
              <Button onClick={closeCreateDialog}>{t('account.keys.secretDone')}</Button>
            ) : (
              <>
                <Button
                  variant="outline"
                  disabled={createMutation.isPending}
                  onClick={closeCreateDialog}
                >
                  {t('common.action.cancel')}
                </Button>
                <Button disabled={createMutation.isPending} onClick={handleCreate}>
                  {createMutation.isPending
                    ? t('account.keys.creating')
                    : t('account.keys.createSubmit')}
                </Button>
              </>
            )}
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
