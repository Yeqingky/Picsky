import { api } from './client';
import type { ApiKeyCreateRequest, ApiKeyCreated, ApiKeyRecord } from './types';

export const keysApi = {
  list: () => api.get<ApiKeyRecord[]>('/keys').then((r) => r.data),

  /** secret 仅在创建时返回一次 */
  create: (body: ApiKeyCreateRequest) => api.post<ApiKeyCreated>('/keys', body).then((r) => r.data),

  revoke: (id: number) => api.delete<void>(`/keys/${id}`).then(() => undefined),
};
