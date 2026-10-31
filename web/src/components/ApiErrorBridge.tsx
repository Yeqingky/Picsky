import { useEffect, useRef } from 'react';
import { useNavigate } from 'react-router';
import { useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import { useTranslation } from 'react-i18next';
import { setErrorNotifier, setUnauthorizedHandler } from '../api/client';
import { AUTH_QUERY_KEY } from '../hooks/useAuth';

/**
 * 把 axios 拦截器的全局错误与 401 会话失效接到 sonner 与路由:
 * 未认证时清空会话缓存并跳转登录页 (公开页除外).
 */
export function ApiErrorBridge() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  const navigateRef = useRef(navigate);
  navigateRef.current = navigate;

  useEffect(() => {
    setErrorNotifier((message) => {
      toast.error(message);
    });
    return () => setErrorNotifier(null);
  }, []);

  useEffect(() => {
    setUnauthorizedHandler(() => {
      queryClient.setQueryData(AUTH_QUERY_KEY, null);
      toast.warning(t('common.error.unauthorized'));
      const { pathname, search } = window.location;
      if (pathname !== '/login' && pathname !== '/register') {
        const redirect = `${pathname}${search}`;
        navigateRef.current(`/login?redirect=${encodeURIComponent(redirect)}`, { replace: true });
      }
    });
    return () => setUnauthorizedHandler(null);
  }, [queryClient, t]);

  return null;
}
