import type { ReactNode } from 'react';
import { Navigate, Outlet, useLocation } from 'react-router';
import { useTranslation } from 'react-i18next';
import { useAuth } from '../hooks/useAuth';
import { hasAnyPermission } from '../utils/permissions';
import type { PermissionName } from '../api/types';
import { PageSkeleton } from '../components/skeletons';
import { Button } from '../components/ui/button';

/** 会话守卫: 未认证跳转登录页, 校验期间显示骨架 */
export function RequireAuth() {
  const { isLoading, isAuthenticated } = useAuth();
  const location = useLocation();

  if (isLoading) {
    return (
      <div className="p-6">
        <PageSkeleton blocks={2} />
      </div>
    );
  }

  if (!isAuthenticated) {
    const redirect = `${location.pathname}${location.search}`;
    return <Navigate to={`/login?redirect=${encodeURIComponent(redirect)}`} replace />;
  }

  return <Outlet />;
}

export function ForbiddenPage() {
  const { t } = useTranslation();
  return (
    <div className="flex flex-col items-center justify-center gap-3 py-20">
      <h2 className="font-heading text-muted-foreground text-2xl font-semibold">
        {t('common.status403.title')}
      </h2>
      <p className="text-muted-foreground text-center text-base">
        {t('common.status403.description')}
      </p>
      <Button variant="outline" asChild>
        <a href="/">{t('common.action.back')}</a>
      </Button>
    </div>
  );
}

/** 权限位守卫: 无权限直接渲染 403 页面 (不渲染入口) */
export function RequirePermission({
  anyOf,
  children,
}: {
  anyOf: readonly PermissionName[];
  children: ReactNode;
}) {
  const { permissions, isLoading } = useAuth();

  if (isLoading) return <PageSkeleton blocks={2} />;
  if (!hasAnyPermission(permissions, anyOf)) return <ForbiddenPage />;

  return <>{children}</>;
}

/** 公开页守卫: 已登录用户访问登录/注册页时回到首页 */
export function PublicOnly() {
  const { isLoading, isAuthenticated } = useAuth();
  if (isLoading) return <Outlet />;
  if (isAuthenticated) return <Navigate to="/" replace />;
  return <Outlet />;
}
