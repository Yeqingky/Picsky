import { useEffect, useMemo, useState } from 'react';
import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Ban,
  CircleCheck,
  Gauge,
  KeyRound,
  Plus,
  ScrollText,
  Search,
  Trash2,
  Users,
} from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { adminGroupsApi, adminPoliciesApi, adminUsersApi } from '../../api';
import type {
  AdminGroup,
  AdminPolicy,
  AdminUser,
  AdminUserCreateRequest,
  AdminUserUpdateRequest,
  QuotaMode,
} from '../../api/types';
import { useConfirm } from '../../components/ConfirmDialog';
import { DataTable } from '../../components/DataTable';
import type { DataTableColumn } from '../../components/DataTable';
import { PageHeader } from '../../components/PageHeader';
import { EmptyState, ErrorState } from '../../components/States';
import { TableSkeleton } from '../../components/skeletons';
import { Badge } from '../../components/ui/badge';
import { Button } from '../../components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '../../components/ui/dialog';
import { Input } from '../../components/ui/input';
import { Label } from '../../components/ui/label';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '../../components/ui/select';
import { Tooltip, TooltipContent, TooltipTrigger } from '../../components/ui/tooltip';
import { useAuth } from '../../hooks/useAuth';
import { useDebouncedValue } from '../../hooks/useDebouncedValue';
import { useDelayedLoading } from '../../hooks/useDelayedLoading';
import { DEFAULT_TABLE_PAGE_SIZE } from '../../utils/constants';
import { formatBytes, formatDateTime, formatNumber } from '../../utils/format';

const EMAIL_PATTERN = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;
const PAGE_SIZE_OPTIONS = [20, 50, 100];
/** Radix Select 不允许空字符串值, 用哨兵表示 "继承" */
const INHERIT_VALUE = '__inherit__';

type UserDialogState =
  | { kind: 'create' }
  | { kind: 'group'; user: AdminUser }
  | { kind: 'password'; user: AdminUser }
  | { kind: 'quota'; user: AdminUser }
  | { kind: 'policy'; user: AdminUser };

export default function AdminUsersPage() {
  const { t } = useTranslation();
  const confirm = useConfirm();
  const queryClient = useQueryClient();

  const [keywordInput, setKeywordInput] = useState('');
  const keyword = useDebouncedValue(keywordInput, 400).trim();
  const [paginationModel, setPaginationModel] = useState({
    page: 0,
    pageSize: DEFAULT_TABLE_PAGE_SIZE,
  });
  const [dialog, setDialog] = useState<UserDialogState | null>(null);

  useEffect(() => {
    setPaginationModel((prev) => (prev.page === 0 ? prev : { ...prev, page: 0 }));
  }, [keyword]);

  const usersQuery = useQuery({
    queryKey: [
      'admin',
      'users',
      { page: paginationModel.page + 1, page_size: paginationModel.pageSize, keyword },
    ],
    queryFn: () =>
      adminUsersApi.list({
        page: paginationModel.page + 1,
        page_size: paginationModel.pageSize,
        keyword: keyword === '' ? undefined : keyword,
      }),
    placeholderData: keepPreviousData,
  });

  const { user: currentUser, hasPermission } = useAuth();
  const canManagePolicies = hasPermission('manage_policies');
  const canManageGroups = hasPermission('manage_groups');
  const rows = usersQuery.data?.items ?? [];
  const needsPolicyNames = rows.some((row) => row.default_policy_id !== null);

  // 辅助数据需要额外权限位: 无权限时不发起请求 (避免 403 反复提示, 并隐藏对应入口)
  const policiesQuery = useQuery({
    queryKey: ['admin', 'policies'],
    queryFn: () => adminPoliciesApi.list(),
    enabled: canManagePolicies && (needsPolicyNames || dialog?.kind === 'policy'),
  });

  const groupsQuery = useQuery({
    queryKey: ['admin', 'groups'],
    queryFn: () => adminGroupsApi.list(),
    enabled: canManageGroups && (dialog?.kind === 'create' || dialog?.kind === 'group'),
  });

  const policyNameById = useMemo(() => {
    const map = new Map<number, string>();
    for (const policy of policiesQuery.data ?? []) map.set(policy.id, policy.name);
    return map;
  }, [policiesQuery.data]);

  const invalidateUsers = () => queryClient.invalidateQueries({ queryKey: ['admin', 'users'] });

  const createMutation = useMutation({
    mutationFn: (body: AdminUserCreateRequest) => adminUsersApi.create(body),
    onSuccess: async (_data, body) => {
      await invalidateUsers();
      toast.success(t('admin.users.feedback.created', { username: body.username }));
      setDialog(null);
    },
  });

  const updateMutation = useMutation({
    mutationFn: (vars: { id: number; body: AdminUserUpdateRequest; message: string }) =>
      adminUsersApi.update(vars.id, vars.body),
    onSuccess: async (_data, vars) => {
      await invalidateUsers();
      toast.success(vars.message);
      setDialog(null);
    },
  });

  const deleteMutation = useMutation({
    mutationFn: (vars: { id: number }) => adminUsersApi.remove(vars.id),
    onSuccess: async () => {
      await invalidateUsers();
      toast.success(t('admin.users.feedback.deleted'));
    },
  });

  const refreshIndicator = useDelayedLoading(usersQuery.isFetching && !usersQuery.isPending);

  const handleToggleStatus = async (user: AdminUser) => {
    const disabling = user.status === 0;
    const ok = await confirm({
      title: disabling ? t('admin.users.action.disable') : t('admin.users.action.enable'),
      description: t(disabling ? 'admin.users.confirm.disable' : 'admin.users.confirm.enable', {
        username: user.username,
      }),
      danger: disabling,
      confirmText: disabling ? t('admin.users.action.disable') : t('admin.users.action.enable'),
    });
    if (!ok) return;
    updateMutation.mutate({
      id: user.id,
      body: { status: disabling ? 1 : 0 },
      message: t(disabling ? 'admin.users.feedback.disabled' : 'admin.users.feedback.enabled', {
        username: user.username,
      }),
    });
  };

  const handleDelete = async (user: AdminUser) => {
    const ok = await confirm({
      description: t('admin.users.confirm.delete', { username: user.username }),
      danger: true,
      confirmText: t('common.action.delete'),
    });
    if (!ok) return;
    deleteMutation.mutate({ id: user.id });
  };

  const columns: DataTableColumn<AdminUser>[] = [
    {
      id: 'id',
      header: t('admin.users.column.id'),
      className: 'w-[70px]',
      sortable: true,
      sortValue: (row) => row.id,
      cell: (row) => row.id,
    },
    {
      id: 'username',
      header: t('admin.users.column.username'),
      className: 'min-w-[140px]',
      sortable: true,
      sortValue: (row) => row.username,
      cell: (row) => row.username,
    },
    {
      id: 'nickname',
      header: t('admin.users.column.nickname'),
      className: 'min-w-[120px]',
      sortable: true,
      sortValue: (row) => row.nickname,
      cell: (row) => row.nickname || '—',
    },
    {
      id: 'email',
      header: t('admin.users.column.email'),
      className: 'min-w-[240px]',
      cell: (row) => (
        <div className="flex min-w-0 items-center gap-1.5">
          <span className="truncate">{row.email}</span>
          <Badge
            variant="outline"
            className={
              row.email_verified ? 'border-transparent bg-success/10 text-success' : undefined
            }
          >
            {row.email_verified ? t('admin.users.emailVerified') : t('admin.users.emailUnverified')}
          </Badge>
        </div>
      ),
    },
    {
      id: 'group_name',
      header: t('admin.users.column.group'),
      className: 'w-[130px]',
      sortable: true,
      sortValue: (row) => row.group_name,
      cell: (row) => row.group_name,
    },
    {
      id: 'status',
      header: t('admin.users.column.status'),
      className: 'w-[100px]',
      cell: (row) =>
        row.status === 0 ? (
          <Badge className="border-transparent bg-success/10 text-success">
            {t('common.status.normal')}
          </Badge>
        ) : (
          <Badge variant="secondary">{t('common.status.disabled')}</Badge>
        ),
    },
    {
      id: 'quota',
      header: t('admin.users.column.quota'),
      className: 'w-[170px]',
      cell: (row) => {
        if (row.quota_mode === null || row.quota_value === null) {
          return <span className="text-muted-foreground text-sm">{t('common.quota.unset')}</span>;
        }
        return (
          <span className="text-sm">
            {row.quota_mode === 'bytes'
              ? `${t('common.quota.modeBytes')}: ${formatBytes(row.quota_value)}`
              : `${t('common.quota.modeCount')}: ${formatNumber(row.quota_value)}`}
          </span>
        );
      },
    },
    {
      id: 'usage',
      header: t('admin.users.column.usage'),
      className: 'w-[200px]',
      cell: (row) =>
        `${formatNumber(row.used_images)} ${t('common.quota.modeCount')} · ${formatBytes(row.used_bytes)}`,
    },
    {
      id: 'default_policy_id',
      header: t('admin.users.column.defaultPolicy'),
      className: 'w-[170px]',
      cell: (row) =>
        row.default_policy_id === null ? (
          <span className="text-muted-foreground text-sm">
            {t('admin.users.policyInheritShort')}
          </span>
        ) : (
          <span className="block truncate">
            {policyNameById.get(row.default_policy_id) ?? `#${row.default_policy_id}`}
          </span>
        ),
    },
    {
      id: 'last_login_at',
      header: t('admin.users.column.lastLogin'),
      className: 'w-[180px]',
      sortable: true,
      sortValue: (row) => row.last_login_at,
      cell: (row) => formatDateTime(row.last_login_at),
    },
    {
      id: 'created_at',
      header: t('admin.users.column.createdAt'),
      className: 'w-[180px]',
      sortable: true,
      sortValue: (row) => row.created_at,
      cell: (row) => formatDateTime(row.created_at),
    },
    {
      id: 'actions',
      header: t('common.table.actions'),
      className: 'w-[260px]',
      cell: (row) => {
        const statusLabel =
          row.status === 0 ? t('admin.users.action.disable') : t('admin.users.action.enable');
        return (
          <div className="flex items-center gap-0.5">
            <Tooltip>
              <TooltipTrigger asChild>
                <Button
                  variant="ghost"
                  size="icon-sm"
                  className={row.status === 0 ? 'text-destructive' : 'text-success'}
                  aria-label={statusLabel}
                  onClick={() => void handleToggleStatus(row)}
                >
                  {row.status === 0 ? (
                    <Ban className="size-4" />
                  ) : (
                    <CircleCheck className="size-4" />
                  )}
                </Button>
              </TooltipTrigger>
              <TooltipContent>{statusLabel}</TooltipContent>
            </Tooltip>
            {canManageGroups && (
              <Tooltip>
                <TooltipTrigger asChild>
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    aria-label={t('admin.users.action.group')}
                    onClick={() => setDialog({ kind: 'group', user: row })}
                  >
                    <Users className="size-4" />
                  </Button>
                </TooltipTrigger>
                <TooltipContent>{t('admin.users.action.group')}</TooltipContent>
              </Tooltip>
            )}
            <Tooltip>
              <TooltipTrigger asChild>
                <Button
                  variant="ghost"
                  size="icon-sm"
                  aria-label={t('admin.users.action.password')}
                  onClick={() => setDialog({ kind: 'password', user: row })}
                >
                  <KeyRound className="size-4" />
                </Button>
              </TooltipTrigger>
              <TooltipContent>{t('admin.users.action.password')}</TooltipContent>
            </Tooltip>
            <Tooltip>
              <TooltipTrigger asChild>
                <Button
                  variant="ghost"
                  size="icon-sm"
                  aria-label={t('admin.users.action.quota')}
                  onClick={() => setDialog({ kind: 'quota', user: row })}
                >
                  <Gauge className="size-4" />
                </Button>
              </TooltipTrigger>
              <TooltipContent>{t('admin.users.action.quota')}</TooltipContent>
            </Tooltip>
            {canManagePolicies && (
              <Tooltip>
                <TooltipTrigger asChild>
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    aria-label={t('admin.users.action.policy')}
                    onClick={() => setDialog({ kind: 'policy', user: row })}
                  >
                    <ScrollText className="size-4" />
                  </Button>
                </TooltipTrigger>
                <TooltipContent>{t('admin.users.action.policy')}</TooltipContent>
              </Tooltip>
            )}
            {row.id !== currentUser?.id && (
              <Tooltip>
                <TooltipTrigger asChild>
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    className="text-destructive"
                    aria-label={t('admin.users.action.delete')}
                    onClick={() => void handleDelete(row)}
                  >
                    <Trash2 className="size-4" />
                  </Button>
                </TooltipTrigger>
                <TooltipContent>{t('admin.users.action.delete')}</TooltipContent>
              </Tooltip>
            )}
          </div>
        );
      },
    },
  ];

  const renderDialog = () => {
    const current = dialog;
    if (!current) return null;
    const groups = groupsQuery.data ?? [];
    const policies = policiesQuery.data ?? [];

    if (current.kind === 'create') {
      return (
        <CreateUserDialog
          groups={groups}
          submitting={createMutation.isPending}
          onClose={() => setDialog(null)}
          onSubmit={(body) => createMutation.mutate(body)}
        />
      );
    }

    const target = current.user;

    if (current.kind === 'group') {
      return (
        <GroupDialog
          user={target}
          groups={groups}
          submitting={updateMutation.isPending}
          onClose={() => setDialog(null)}
          onSubmit={(groupId) =>
            updateMutation.mutate({
              id: target.id,
              body: { group_id: groupId },
              message: t('admin.users.feedback.group'),
            })
          }
        />
      );
    }

    if (current.kind === 'password') {
      return (
        <PasswordDialog
          user={target}
          submitting={updateMutation.isPending}
          onClose={() => setDialog(null)}
          onSubmit={(password) =>
            updateMutation.mutate({
              id: target.id,
              body: { password },
              message: t('admin.users.feedback.password'),
            })
          }
        />
      );
    }

    if (current.kind === 'quota') {
      return (
        <QuotaDialog
          user={target}
          submitting={updateMutation.isPending}
          onClose={() => setDialog(null)}
          onSubmit={(mode, value) =>
            updateMutation.mutate({
              id: target.id,
              body: { quota_mode: mode, quota_value: value },
              message: t('admin.users.feedback.quota'),
            })
          }
        />
      );
    }

    return (
      <PolicyDialog
        user={target}
        policies={policies}
        submitting={updateMutation.isPending}
        onClose={() => setDialog(null)}
        onSubmit={(policyId) =>
          updateMutation.mutate({
            id: target.id,
            body: { default_policy_id: policyId },
            message: t('admin.users.feedback.policy'),
          })
        }
      />
    );
  };

  const header = (
    <PageHeader
      title={t('nav.adminUsers')}
      description={t('admin.users.description')}
      actions={
        // 新建用户需要选择用户组 (GET /admin/groups 需 manage_groups 权限)
        <Tooltip>
          <TooltipTrigger asChild>
            <span className="inline-flex">
              <Button disabled={!canManageGroups} onClick={() => setDialog({ kind: 'create' })}>
                <Plus className="size-4" />
                {t('admin.users.createTitle')}
              </Button>
            </span>
          </TooltipTrigger>
          {!canManageGroups && (
            <TooltipContent>{t('admin.users.createNeedsGroups')}</TooltipContent>
          )}
        </Tooltip>
      }
    />
  );

  if (usersQuery.isPending) {
    return (
      <>
        {header}
        <TableSkeleton rows={8} columns={6} />
      </>
    );
  }

  if (usersQuery.isError) {
    return (
      <>
        {header}
        <ErrorState
          message={usersQuery.error instanceof Error ? usersQuery.error.message : undefined}
          onRetry={() => void usersQuery.refetch()}
        />
      </>
    );
  }

  return (
    <>
      {header}
      <div className="mb-4 flex flex-col gap-3 sm:flex-row sm:items-center">
        <div className="flex w-full flex-col gap-1.5 sm:max-w-[320px]">
          <Label htmlFor="admin-users-keyword">{t('common.table.keyword')}</Label>
          <div className="relative">
            <Search className="text-muted-foreground pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2" />
            <Input
              id="admin-users-keyword"
              className="pl-8"
              value={keywordInput}
              placeholder={t('admin.users.searchPlaceholder')}
              onChange={(event) => setKeywordInput(event.target.value)}
            />
          </div>
        </div>
      </div>

      <DataTable
        columns={columns}
        rows={rows}
        getRowId={(row) => row.id}
        loading={refreshIndicator}
        emptyState={<EmptyState />}
        pagination={{
          page: paginationModel.page,
          pageSize: paginationModel.pageSize,
          total: usersQuery.data?.total ?? 0,
          serverSide: true,
          pageSizeOptions: PAGE_SIZE_OPTIONS,
          onPageChange: (page) => setPaginationModel((prev) => ({ ...prev, page })),
          onPageSizeChange: (pageSize) => setPaginationModel({ page: 0, pageSize }),
        }}
      />

      {renderDialog()}
    </>
  );
}

function CreateUserDialog({
  groups,
  submitting,
  onClose,
  onSubmit,
}: {
  groups: AdminGroup[];
  submitting: boolean;
  onClose: () => void;
  onSubmit: (body: AdminUserCreateRequest) => void;
}) {
  const { t } = useTranslation();
  const [username, setUsername] = useState('');
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [nickname, setNickname] = useState('');
  const [groupId, setGroupId] = useState('');
  const [errors, setErrors] = useState<Partial<Record<string, string>>>({});

  useEffect(() => {
    if (groupId !== '' || groups.length === 0) return;
    const fallback = groups.find((group) => group.is_default) ?? groups[0];
    setGroupId(String(fallback.id));
  }, [groups, groupId]);

  const submit = () => {
    const next: Record<string, string> = {};
    if (username.trim() === '') next.username = t('common.form.required');
    if (email.trim() === '') next.email = t('common.form.required');
    else if (!EMAIL_PATTERN.test(email.trim())) next.email = t('common.form.invalidEmail');
    if (password === '') next.password = t('common.form.required');
    else if (password.length < 8) next.password = t('common.form.passwordTooShort');
    if (groupId === '') next.groupId = t('common.form.required');
    setErrors(next);
    if (Object.keys(next).length > 0) return;

    onSubmit({
      username: username.trim(),
      email: email.trim(),
      password,
      nickname: nickname.trim() === '' ? undefined : nickname.trim(),
      group_id: Number(groupId),
    });
  };

  return (
    <Dialog
      open
      onOpenChange={(next) => {
        if (!next) onClose();
      }}
    >
      <DialogContent aria-describedby={undefined} className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t('admin.users.createTitle')}</DialogTitle>
        </DialogHeader>
        <div className="flex flex-col gap-4">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="admin-user-username">
              {t('admin.users.field.username')} <span className="text-destructive">*</span>
            </Label>
            <Input
              id="admin-user-username"
              value={username}
              onChange={(event) => setUsername(event.target.value)}
              aria-invalid={Boolean(errors.username)}
              required
            />
            {errors.username && <p className="text-destructive text-sm">{errors.username}</p>}
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="admin-user-email">
              {t('admin.users.field.email')} <span className="text-destructive">*</span>
            </Label>
            <Input
              id="admin-user-email"
              type="email"
              value={email}
              onChange={(event) => setEmail(event.target.value)}
              aria-invalid={Boolean(errors.email)}
              required
            />
            {errors.email && <p className="text-destructive text-sm">{errors.email}</p>}
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="admin-user-password">
              {t('admin.users.field.password')} <span className="text-destructive">*</span>
            </Label>
            <Input
              id="admin-user-password"
              type="password"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
              aria-invalid={Boolean(errors.password)}
              autoComplete="new-password"
              required
            />
            <p
              className={
                errors.password ? 'text-destructive text-sm' : 'text-muted-foreground text-sm'
              }
            >
              {errors.password ?? t('admin.users.passwordHint')}
            </p>
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="admin-user-nickname">
              {`${t('admin.users.field.nickname')} (${t('common.form.optional')})`}
            </Label>
            <Input
              id="admin-user-nickname"
              value={nickname}
              onChange={(event) => setNickname(event.target.value)}
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="admin-user-group">
              {t('admin.users.field.group')} <span className="text-destructive">*</span>
            </Label>
            <Select value={groupId} onValueChange={setGroupId}>
              <SelectTrigger
                id="admin-user-group"
                className="w-full"
                aria-invalid={Boolean(errors.groupId)}
              >
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {groups.map((group) => (
                  <SelectItem key={group.id} value={String(group.id)}>
                    {group.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <p
              className={
                errors.groupId ? 'text-destructive text-sm' : 'text-muted-foreground text-sm'
              }
            >
              {errors.groupId ?? t('admin.users.groupHint')}
            </p>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            {t('common.action.cancel')}
          </Button>
          <Button onClick={submit} disabled={submitting}>
            {submitting ? t('common.action.creating') : t('common.action.create')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function GroupDialog({
  user,
  groups,
  submitting,
  onClose,
  onSubmit,
}: {
  user: AdminUser;
  groups: AdminGroup[];
  submitting: boolean;
  onClose: () => void;
  onSubmit: (groupId: number) => void;
}) {
  const { t } = useTranslation();
  const [groupId, setGroupId] = useState(String(user.group_id));
  const [error, setError] = useState('');

  const submit = () => {
    if (groupId === '') {
      setError(t('common.form.required'));
      return;
    }
    setError('');
    onSubmit(Number(groupId));
  };

  return (
    <Dialog
      open
      onOpenChange={(next) => {
        if (!next) onClose();
      }}
    >
      <DialogContent aria-describedby={undefined} className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>
            {t('admin.users.groupTitle')} · {user.username}
          </DialogTitle>
        </DialogHeader>
        <div className="flex flex-col gap-4">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="admin-user-group-change">{t('admin.users.field.group')}</Label>
            <Select value={groupId} onValueChange={setGroupId}>
              <SelectTrigger
                id="admin-user-group-change"
                className="w-full"
                aria-invalid={Boolean(error)}
              >
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {groups.map((group) => (
                  <SelectItem key={group.id} value={String(group.id)}>
                    {group.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <p className={error ? 'text-destructive text-sm' : 'text-muted-foreground text-sm'}>
              {error || t('admin.users.groupHint')}
            </p>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            {t('common.action.cancel')}
          </Button>
          <Button onClick={submit} disabled={submitting}>
            {submitting ? t('common.action.saving') : t('common.action.save')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function PasswordDialog({
  user,
  submitting,
  onClose,
  onSubmit,
}: {
  user: AdminUser;
  submitting: boolean;
  onClose: () => void;
  onSubmit: (password: string) => void;
}) {
  const { t } = useTranslation();
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');

  const submit = () => {
    if (password === '') {
      setError(t('common.form.required'));
      return;
    }
    if (password.length < 8) {
      setError(t('common.form.passwordTooShort'));
      return;
    }
    setError('');
    onSubmit(password);
  };

  return (
    <Dialog
      open
      onOpenChange={(next) => {
        if (!next) onClose();
      }}
    >
      <DialogContent aria-describedby={undefined} className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>
            {t('admin.users.passwordTitle')} · {user.username}
          </DialogTitle>
        </DialogHeader>
        <div className="flex flex-col gap-4">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="admin-user-new-password">{t('admin.users.field.password')}</Label>
            <Input
              id="admin-user-new-password"
              type="password"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
              aria-invalid={Boolean(error)}
              autoComplete="new-password"
            />
            <p className={error ? 'text-destructive text-sm' : 'text-muted-foreground text-sm'}>
              {error || t('admin.users.passwordHint')}
            </p>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            {t('common.action.cancel')}
          </Button>
          <Button onClick={submit} disabled={submitting}>
            {submitting ? t('common.action.saving') : t('common.action.save')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function QuotaDialog({
  user,
  submitting,
  onClose,
  onSubmit,
}: {
  user: AdminUser;
  submitting: boolean;
  onClose: () => void;
  /** mode 传空串表示清除用户级覆盖 (后端把空串规范化为空值) */
  onSubmit: (mode: QuotaMode | '', value: number | null) => void;
}) {
  const { t } = useTranslation();
  const [mode, setMode] = useState<QuotaMode | ''>(user.quota_mode ?? '');
  const [value, setValue] = useState(user.quota_value === null ? '' : String(user.quota_value));
  const [error, setError] = useState('');

  const submit = () => {
    if (mode === '') {
      setError('');
      onSubmit('', null);
      return;
    }
    const parsed = Number(value);
    if (value.trim() === '' || Number.isNaN(parsed) || parsed < 0) {
      setError(t('admin.users.quotaValueRequired'));
      return;
    }
    setError('');
    onSubmit(mode, parsed);
  };

  return (
    <Dialog
      open
      onOpenChange={(next) => {
        if (!next) onClose();
      }}
    >
      <DialogContent aria-describedby={undefined} className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>
            {t('admin.users.quotaTitle')} · {user.username}
          </DialogTitle>
        </DialogHeader>
        <div className="flex flex-col gap-4">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="admin-user-quota-mode">{t('admin.users.field.quotaMode')}</Label>
            <Select
              value={mode === '' ? INHERIT_VALUE : mode}
              onValueChange={(next) => setMode(next === INHERIT_VALUE ? '' : (next as QuotaMode))}
            >
              <SelectTrigger id="admin-user-quota-mode" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={INHERIT_VALUE}>{t('admin.users.quotaModeInherit')}</SelectItem>
                <SelectItem value="count">{t('common.quota.modeCount')}</SelectItem>
                <SelectItem value="bytes">{t('common.quota.modeBytes')}</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="admin-user-quota-value">{t('admin.users.field.quotaValue')}</Label>
            <Input
              id="admin-user-quota-value"
              value={value}
              inputMode="numeric"
              disabled={mode === ''}
              onChange={(event) => setValue(event.target.value)}
              aria-invalid={Boolean(error)}
            />
            <p className={error ? 'text-destructive text-sm' : 'text-muted-foreground text-sm'}>
              {error || t('admin.users.quotaValueHint')}
            </p>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            {t('common.action.cancel')}
          </Button>
          <Button onClick={submit} disabled={submitting}>
            {submitting ? t('common.action.saving') : t('common.action.save')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function PolicyDialog({
  user,
  policies,
  submitting,
  onClose,
  onSubmit,
}: {
  user: AdminUser;
  policies: AdminPolicy[];
  submitting: boolean;
  onClose: () => void;
  /** 0 表示清除 (跟随组内策略) */
  onSubmit: (policyId: number) => void;
}) {
  const { t } = useTranslation();
  const [policyId, setPolicyId] = useState(
    user.default_policy_id === null ? '' : String(user.default_policy_id),
  );

  return (
    <Dialog
      open
      onOpenChange={(next) => {
        if (!next) onClose();
      }}
    >
      <DialogContent aria-describedby={undefined} className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>
            {t('admin.users.policyTitle')} · {user.username}
          </DialogTitle>
        </DialogHeader>
        <div className="flex flex-col gap-4">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="admin-user-policy">{t('admin.users.field.defaultPolicy')}</Label>
            <Select
              value={policyId === '' ? INHERIT_VALUE : policyId}
              onValueChange={(next) => setPolicyId(next === INHERIT_VALUE ? '' : next)}
            >
              <SelectTrigger id="admin-user-policy" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={INHERIT_VALUE}>{t('admin.users.policyInherit')}</SelectItem>
                {policies.map((policy) => (
                  <SelectItem key={policy.id} value={String(policy.id)}>
                    {policy.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <p className="text-muted-foreground text-sm">{t('admin.users.policyInherit')}</p>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            {t('common.action.cancel')}
          </Button>
          <Button
            onClick={() => onSubmit(policyId === '' ? 0 : Number(policyId))}
            disabled={submitting}
          >
            {submitting ? t('common.action.saving') : t('common.action.save')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
