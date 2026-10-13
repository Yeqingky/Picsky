import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { siteApi } from '../api/site';

export const SITE_QUERY_KEY = ['site'] as const;

/** 站点公开信息查询. */
export function useSiteInfo() {
  return useQuery({
    queryKey: SITE_QUERY_KEY,
    queryFn: siteApi.get,
    staleTime: 5 * 60 * 1000,
    retry: false,
  });
}

/** 站点名称 (设置项 site_name); 未就绪或请求失败时回退到内置应用名 */
export function useSiteName(): string {
  const { t } = useTranslation();
  const { data } = useSiteInfo();
  return data?.site_name || t('common.appName');
}
