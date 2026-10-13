import { Suspense, lazy } from 'react';
import type { ComponentType, ReactNode } from 'react';
import { Outlet, createBrowserRouter } from 'react-router';
import { ApiErrorBridge } from '../components/ApiErrorBridge';
import { PageSkeleton } from '../components/skeletons';
import AuthLayout from '../layouts/AuthLayout';
import { AppLayout } from '../layouts/AppLayout';
import { ForbiddenPage, PublicOnly, RequireAuth, RequirePermission } from './guards';
import { ADMIN_PERMISSIONS } from '../utils/permissions';
import type { PermissionName } from '../api/types';
import LoginPage from '../pages/LoginPage';
import RegisterPage from '../pages/RegisterPage';
import VerifyEmailPage from '../pages/VerifyEmailPage';
import DashboardPage from '../pages/DashboardPage';
import UploadPage from '../pages/UploadPage';
import GalleryPage from '../pages/GalleryPage';
import KeysPage from '../pages/KeysPage';
import ProfilePage from '../pages/ProfilePage';
import NotFoundPage from '../pages/NotFoundPage';

/** 管理区按需加载, 首屏不引入 DataGrid / x-charts */
const AdminOverviewPage = lazy(() => import('../pages/admin/AdminOverviewPage'));
const AdminUsersPage = lazy(() => import('../pages/admin/AdminUsersPage'));
const AdminGroupsPage = lazy(() => import('../pages/admin/AdminGroupsPage'));
const AdminPoliciesPage = lazy(() => import('../pages/admin/AdminPoliciesPage'));
const AdminPolicyEditPage = lazy(() => import('../pages/admin/AdminPolicyEditPage'));
const AdminSettingsPage = lazy(() => import('../pages/admin/AdminSettingsPage'));
const AdminTasksPage = lazy(() => import('../pages/admin/AdminTasksPage'));
const AdminLogsPage = lazy(() => import('../pages/admin/AdminLogsPage'));

function RootRoute() {
  return (
    <>
      <ApiErrorBridge />
      <Outlet />
    </>
  );
}

function LazyPage({ component: Component }: { component: ComponentType }) {
  return (
    <Suspense fallback={<PageSkeleton blocks={2} />}>
      <Component />
    </Suspense>
  );
}

function guarded(anyOf: readonly PermissionName[], node: ReactNode) {
  return <RequirePermission anyOf={anyOf}>{node}</RequirePermission>;
}

export const router = createBrowserRouter([
  {
    element: <RootRoute />,
    children: [
      {
        element: <PublicOnly />,
        children: [
          {
            element: <AuthLayout />,
            children: [
              { path: '/login', element: <LoginPage /> },
              { path: '/register', element: <RegisterPage /> },
              { path: '/verify-email', element: <VerifyEmailPage /> },
            ],
          },
        ],
      },
      {
        element: <RequireAuth />,
        children: [
          {
            element: <AppLayout />,
            children: [
              { index: true, element: <DashboardPage /> },
              { path: 'upload', element: <UploadPage /> },
              { path: 'gallery', element: <GalleryPage /> },
              {
                path: 'keys',
                element: guarded(['manage_keys'], <KeysPage />),
              },
              { path: 'profile', element: <ProfilePage /> },
              { path: '403', element: <ForbiddenPage /> },
              {
                path: 'admin',
                element: guarded(ADMIN_PERMISSIONS, <LazyPage component={AdminOverviewPage} />),
              },
              {
                path: 'admin/users',
                element: guarded(['manage_users'], <LazyPage component={AdminUsersPage} />),
              },
              {
                path: 'admin/groups',
                element: guarded(['manage_groups'], <LazyPage component={AdminGroupsPage} />),
              },
              {
                path: 'admin/policies',
                element: guarded(['manage_policies'], <LazyPage component={AdminPoliciesPage} />),
              },
              {
                path: 'admin/policies/:id',
                element: guarded(['manage_policies'], <LazyPage component={AdminPolicyEditPage} />),
              },
              {
                path: 'admin/settings',
                element: guarded(['manage_settings'], <LazyPage component={AdminSettingsPage} />),
              },
              {
                path: 'admin/tasks',
                element: guarded(['manage_settings'], <LazyPage component={AdminTasksPage} />),
              },
              {
                path: 'admin/logs',
                element: guarded(['view_logs'], <LazyPage component={AdminLogsPage} />),
              },
            ],
          },
        ],
      },
      { path: '*', element: <NotFoundPage /> },
    ],
  },
]);
