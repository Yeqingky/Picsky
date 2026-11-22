import { api } from './client';
import type {
  BatchDeleteResult,
  DeleteResult,
  ImageListParams,
  ImageRecord,
  Paginated,
  PolicyCatalog,
} from './types';

export const imagesApi = {
  list: (params: ImageListParams) =>
    api.get<Paginated<ImageRecord>>('/images', { params }).then((r) => r.data),

  get: (id: number) => api.get<ImageRecord>(`/images/${id}`).then((r) => r.data),

  remove: (id: number) => api.delete<DeleteResult>(`/images/${id}`).then((r) => r.data),

  batchDelete: (ids: number[]) =>
    api.post<BatchDeleteResult>('/images/batch-delete', { ids }).then((r) => r.data),
};

export const policiesApi = {
  /** 当前用户组白名单内的启用策略, 以及全局与用户组的单文件上限 */
  listMine: () => api.get<PolicyCatalog>('/policies').then((r) => r.data),
};
