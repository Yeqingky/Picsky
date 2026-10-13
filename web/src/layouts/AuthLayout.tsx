import { Outlet } from 'react-router';
import { useTranslation } from 'react-i18next';
import { ThemeModeToggleButton } from '../components/ThemeModeToggleButton';
import { Card } from '../components/ui/card';

/** 登录 / 注册等公开页面的居中布局 */
export default function AuthLayout() {
  const { t } = useTranslation();

  return (
    <div className="bg-background flex min-h-screen items-center justify-center p-4">
      <div className="fixed top-3 right-3">
        <ThemeModeToggleButton />
      </div>
      <div className="flex w-full max-w-[420px] flex-col items-center gap-6">
        <div className="flex flex-col items-center">
          <h1 className="font-heading text-2xl font-bold">{t('common.appName')}</h1>
          <p className="text-muted-foreground text-sm">{t('common.appSubtitle')}</p>
        </div>
        <Card className="w-full border-border p-5 sm:p-6">
          <Outlet />
        </Card>
      </div>
    </div>
  );
}
