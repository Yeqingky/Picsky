import { Outlet } from 'react-router';
import { ThemeModeToggleButton } from '../components/ThemeModeToggleButton';
import { useSiteName } from '../hooks/useSite';
import { Card } from '../components/ui/card';

/** 登录 / 注册等公开页面的居中布局 */
export default function AuthLayout() {
  const siteName = useSiteName();

  return (
    <div className="bg-background flex min-h-screen items-center justify-center p-4">
      <div className="fixed top-3 right-3">
        <ThemeModeToggleButton />
      </div>
      <div className="flex w-full max-w-[420px] flex-col items-center gap-6">
        <div className="flex flex-col items-center">
          <h1 className="font-heading text-2xl font-bold">{siteName}</h1>
        </div>
        <Card className="w-full border-border p-5 sm:p-6">
          <Outlet />
        </Card>
      </div>
    </div>
  );
}
