import { ApiError, api } from './client';
import type {
  AdminGroup,
  AdminGroupWriteRequest,
  AdminPolicy,
  AdminPolicyWriteRequest,
  AdminStats,
  AdminUser,
  AdminUserCreateRequest,
  AdminUserListParams,
  AdminUserUpdateRequest,
  AuditLog,
  ClientIPSource,
  LogListParams,
  MaintenanceTask,
  Paginated,
  PolicyPreviewRequest,
  PolicyPreviewResult,
  PolicyScanEnqueued,
  PolicyScanRequest,
  PolicyTestResult,
  SettingsPayload,
  TaskListParams,
} from './types';

export const adminUsersApi = {
  list: (params: AdminUserListParams) =>
    api.get<Paginated<AdminUser>>('/admin/users', { params }).then((r) => r.data),

  create: (body: AdminUserCreateRequest) =>
    api.post<AdminUser>('/admin/users', body).then((r) => r.data),

  update: (id: number, body: AdminUserUpdateRequest) =>
    api.patch<AdminUser>(`/admin/users/${id}`, body).then((r) => r.data),

  remove: (id: number) => api.delete<void>(`/admin/users/${id}`).then(() => undefined),
};

export const adminGroupsApi = {
  list: () => api.get<AdminGroup[]>('/admin/groups').then((r) => r.data),

  create: (body: AdminGroupWriteRequest) =>
    api.post<AdminGroup>('/admin/groups', body).then((r) => r.data),

  update: (id: number, body: Partial<AdminGroupWriteRequest>) =>
    api.patch<AdminGroup>(`/admin/groups/${id}`, body).then((r) => r.data),

  remove: (id: number) => api.delete<void>(`/admin/groups/${id}`).then(() => undefined),

  setPolicies: (id: number, policyIds: number[]) =>
    api.put<void>(`/admin/groups/${id}/policies`, { policy_ids: policyIds }).then(() => undefined),
};

export const adminPoliciesApi = {
  list: () => api.get<AdminPolicy[]>('/admin/policies').then((r) => r.data),

  /** 原生接口没有单条读取路由, 由列表接口取单条 (docs/api.md 第 8 节) */
  get: async (id: number) => {
    const policies = await api.get<AdminPolicy[]>('/admin/policies').then((r) => r.data);
    const found = policies.find((policy) => policy.id === id);
    if (!found) {
      throw new ApiError({ status: 404, code: 'not_found', message: '资源不存在' });
    }
    return found;
  },

  create: (body: AdminPolicyWriteRequest) =>
    api.post<AdminPolicy>('/admin/policies', body).then((r) => r.data),

  update: (id: number, body: Partial<AdminPolicyWriteRequest>) =>
    api.patch<AdminPolicy>(`/admin/policies/${id}`, body).then((r) => r.data),

  /** 按给定顺序重排全部策略 (拖拽排序) */
  reorder: (ids: number[]) => api.patch<void>('/admin/policies', { ids }).then(() => undefined),

  remove: (id: number) => api.delete<void>(`/admin/policies/${id}`).then(() => undefined),

  test: (id: number) =>
    api
      .post<PolicyTestResult>(`/admin/policies/${id}/test`, undefined, { timeout: 120_000 })
      .then((r) => r.data),

  scan: (id: number, body: PolicyScanRequest = {}, options?: { silent?: boolean }) =>
    api
      .post<PolicyScanEnqueued>(`/admin/policies/${id}/scan`, body, { silent: options?.silent })
      .then((r) => r.data),

  scanStatus: (id: number, taskId: number) =>
    api.get<MaintenanceTask>(`/admin/policies/${id}/scan/${taskId}`).then((r) => r.data),

  preview: (body: PolicyPreviewRequest) =>
    api.post<PolicyPreviewResult>('/admin/policies/preview', body).then((r) => r.data),
};

export const adminSettingsApi = {
  clientIPSources: () =>
    api.get<ClientIPSource[]>('/admin/settings/client-ip-sources').then((r) => r.data),

  get: () => api.get<SettingsPayload>('/admin/settings').then((r) => r.data),

  /** 未出现的键不改动; 出现且为空串表示清空 */
  update: (patch: Record<string, string>) =>
    api.patch<SettingsPayload>('/admin/settings', patch).then((r) => r.data),
};

export const adminTasksApi = {
  list: (params: TaskListParams) =>
    api.get<Paginated<MaintenanceTask>>('/admin/tasks', { params }).then((r) => r.data),

  run: (id: number) => api.post<void>(`/admin/tasks/${id}/run`).then(() => undefined),

  delete: (id: number) => api.delete<void>(`/admin/tasks/${id}`).then(() => undefined),
};

export const adminLogsApi = {
  list: (params: LogListParams) =>
    api.get<Paginated<AuditLog>>('/admin/logs', { params }).then((r) => r.data),
};

export const adminStatsApi = {
  get: () => api.get<AdminStats>('/admin/stats').then((r) => r.data),
};
