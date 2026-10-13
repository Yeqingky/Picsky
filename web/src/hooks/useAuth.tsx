import { createContext, useCallback, useContext, useMemo } from 'react';
import type { ReactNode } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { authApi } from '../api/auth';
import type { CurrentUser, LoginRequest } from '../api/types';
import { hasPermission as checkPermission } from '../utils/permissions';
import type { PermissionName } from '../api/types';

export const AUTH_QUERY_KEY = ['auth', 'me'] as const;

interface AuthContextValue {
  user: CurrentUser | null;
  /** 首次会话校验中 (路由守卫据此显示骨架而不是跳转) */
  isLoading: boolean;
  isAuthenticated: boolean;
  permissions: string[];
  login: (payload: LoginRequest) => Promise<CurrentUser>;
  logout: () => Promise<void>;
  refresh: () => Promise<CurrentUser | null>;
  updateUser: (user: CurrentUser | null) => void;
  hasPermission: (name: PermissionName) => boolean;
}

const AuthContext = createContext<AuthContextValue | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const queryClient = useQueryClient();

  const { data, isPending } = useQuery({
    queryKey: AUTH_QUERY_KEY,
    queryFn: authApi.me,
    retry: false,
    staleTime: 30_000,
    refetchOnWindowFocus: false,
  });

  const user = data ?? null;

  const updateUser = useCallback(
    (next: CurrentUser | null) => {
      queryClient.setQueryData(AUTH_QUERY_KEY, next);
    },
    [queryClient],
  );

  const login = useCallback(
    async (payload: LoginRequest) => {
      const loggedIn = await authApi.login(payload);
      queryClient.setQueryData(AUTH_QUERY_KEY, loggedIn);
      return loggedIn;
    },
    [queryClient],
  );

  const logout = useCallback(async () => {
    try {
      await authApi.logout();
    } finally {
      queryClient.setQueryData(AUTH_QUERY_KEY, null);
      queryClient.clear();
    }
  }, [queryClient]);

  const refresh = useCallback(async () => {
    try {
      const fresh = await authApi.me();
      queryClient.setQueryData(AUTH_QUERY_KEY, fresh);
      return fresh;
    } catch {
      queryClient.setQueryData(AUTH_QUERY_KEY, null);
      return null;
    }
  }, [queryClient]);

  const value = useMemo<AuthContextValue>(
    () => ({
      user,
      isLoading: isPending,
      isAuthenticated: user !== null,
      permissions: user?.permissions ?? [],
      login,
      logout,
      refresh,
      updateUser,
      hasPermission: (name: PermissionName) => checkPermission(user?.permissions, name),
    }),
    [user, isPending, login, logout, refresh, updateUser],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthContextValue {
  const context = useContext(AuthContext);
  if (!context) throw new Error('useAuth 必须在 AuthProvider 内使用');
  return context;
}
