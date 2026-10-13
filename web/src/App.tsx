import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { RouterProvider } from 'react-router';
import 'dayjs/locale/zh-cn';
import { AppThemeProvider } from './theme/ThemeModeProvider';
import { ConfirmProvider } from './components/ConfirmDialog';
import { Toaster } from './components/ui/sonner';
import { TooltipProvider } from './components/ui/tooltip';
import { AuthProvider } from './hooks/useAuth';
import { router } from './router';
import { ApiError as ApiErrorClass } from './api/client';

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: (failureCount, error) => {
        // 认证 / 权限 / 参数类错误不重试
        if (error instanceof ApiErrorClass && error.status >= 400 && error.status < 500)
          return false;
        return failureCount < 2;
      },
      refetchOnWindowFocus: false,
      staleTime: 10_000,
    },
    mutations: { retry: false },
  },
});

export default function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <AppThemeProvider>
        <TooltipProvider delayDuration={200}>
          <ConfirmProvider>
            <AuthProvider>
              <RouterProvider router={router} />
            </AuthProvider>
          </ConfirmProvider>
          <Toaster
            position="bottom-right"
            visibleToasts={3}
            duration={3200}
            richColors
            closeButton
          />
        </TooltipProvider>
      </AppThemeProvider>
    </QueryClientProvider>
  );
}
