import { api } from './client';
import type { SiteInfo } from './types';

export const siteApi = {
  /** 站点公开信息 (名称, 注册开关, 密码找回可用性, 人机验证配置) */
  get: () => api.get<SiteInfo>('/site', { silent: true }).then((r) => r.data),
};
