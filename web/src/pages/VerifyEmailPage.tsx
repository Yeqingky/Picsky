import { Link as RouterLink, useSearchParams } from 'react-router';
import { LoaderCircle } from 'lucide-react';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { authApi } from '../api/auth';
import { ApiError } from '../api/client';
import { Alert, AlertDescription } from '../components/ui/alert';
import { Button } from '../components/ui/button';

/**
 * 邮箱验证落地页 (公开): 消费邮件里的 ?token= 并提交 POST /auth/verify-email.
 * 邮箱验证开启时, 注册与个人设置页发出的链接都指向本路由.
 */
export default function VerifyEmailPage() {
  const { t } = useTranslation();
  const [searchParams] = useSearchParams();
  const token = (searchParams.get('token') ?? '').trim();

  const { status, error } = useQuery({
    queryKey: ['auth', 'verify-email', token],
    queryFn: () => authApi.verifyEmail(token),
    enabled: token !== '',
    retry: false,
    staleTime: Infinity,
    refetchOnMount: false,
  });

  if (token === '') {
    return (
      <div className="flex flex-col gap-4">
        <h2 className="font-heading text-xl font-semibold">{t('auth.verifyEmail.title')}</h2>
        <Alert className="border-warning/30 bg-warning/10">
          <AlertDescription className="text-warning">
            {t('auth.verifyEmail.missingToken')}
          </AlertDescription>
        </Alert>
        <Button asChild>
          <RouterLink to="/login">{t('auth.verifyEmail.goLogin')}</RouterLink>
        </Button>
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-4">
      <h2 className="font-heading text-xl font-semibold">{t('auth.verifyEmail.title')}</h2>

      {status === 'pending' && (
        <div className="flex items-center gap-3">
          <LoaderCircle className="text-muted-foreground size-5 animate-spin" />
          <p className="text-muted-foreground text-sm">{t('auth.verifyEmail.verifying')}</p>
        </div>
      )}

      {status === 'success' && (
        <>
          <Alert className="border-success/30 bg-success/10">
            <AlertDescription className="text-success">
              {t('auth.verifyEmail.success')}
            </AlertDescription>
          </Alert>
          <Button asChild>
            <RouterLink to="/login">{t('auth.verifyEmail.goLogin')}</RouterLink>
          </Button>
        </>
      )}

      {status === 'error' && (
        <>
          <Alert variant="destructive">
            <AlertDescription>
              {error instanceof ApiError ? error.message : t('auth.verifyEmail.failed')}
            </AlertDescription>
          </Alert>
          <Button variant="outline" asChild>
            <RouterLink to="/login">{t('auth.verifyEmail.goLogin')}</RouterLink>
          </Button>
        </>
      )}
    </div>
  );
}
