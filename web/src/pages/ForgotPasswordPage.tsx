import { useState } from 'react';
import type { FormEvent } from 'react';
import { Link as RouterLink } from 'react-router';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { ApiError, authApi } from '../api';
import type { CaptchaConfig } from '../api';
import { CaptchaWidget } from '../components/CaptchaWidget';
import { AuthCardSkeleton } from '../components/skeletons';
import { Alert, AlertDescription } from '../components/ui/alert';
import { Button } from '../components/ui/button';
import { Input } from '../components/ui/input';
import { Label } from '../components/ui/label';
import { useAuth } from '../hooks/useAuth';
import { useSiteInfo } from '../hooks/useSite';

const EMAIL_PATTERN = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

/** 请求通过注册邮箱发送密码找回链接 (/forgot-password). */
export default function ForgotPasswordPage() {
  const { t } = useTranslation();
  const { isLoading } = useAuth();
  const [email, setEmail] = useState('');
  const [captchaToken, setCaptchaToken] = useState('');
  const [resetSignal, setResetSignal] = useState(0);
  const [error, setError] = useState<string | null>(null);
  const [sent, setSent] = useState(false);
  const [submitting, setSubmitting] = useState(false);

  const captchaQuery = useQuery({
    queryKey: ['auth', 'captcha'],
    queryFn: authApi.captchaConfig,
    staleTime: Infinity,
  });
  const captchaConfig: CaptchaConfig | undefined = captchaQuery.data;
  const captchaRequired = (captchaConfig?.provider ?? 'none') !== 'none';
  const siteQuery = useSiteInfo();
  const siteInfo = siteQuery.data;

  if (isLoading || siteQuery.isLoading)
    return <AuthCardSkeleton fields={1} captcha={captchaQuery.isPending || captchaRequired} />;

  if (!siteInfo?.password_reset_enabled) {
    return (
      <div className="flex flex-col gap-4">
        <h2 className="font-heading text-xl font-semibold">{t('auth.passwordRecovery.title')}</h2>
        <Alert className="border-warning/30 bg-warning/10">
          <AlertDescription className="text-warning">
            {t('auth.passwordRecovery.disabled')}
          </AlertDescription>
        </Alert>
        <Button asChild>
          <RouterLink to="/login">{t('auth.passwordRecovery.goLogin')}</RouterLink>
        </Button>
      </div>
    );
  }

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const normalizedEmail = email.trim();
    if (normalizedEmail === '') {
      setError(t('auth.passwordRecovery.emailRequired'));
      return;
    }
    if (!EMAIL_PATTERN.test(normalizedEmail)) {
      setError(t('common.form.invalidEmail'));
      return;
    }
    if (captchaRequired && captchaToken === '') {
      setError(t('auth.login.captchaRequired'));
      return;
    }

    setError(null);
    setSubmitting(true);
    try {
      await authApi.requestPasswordReset({
        email: normalizedEmail,
        captcha_token: captchaToken === '' ? undefined : captchaToken,
      });
      setSent(true);
    } catch (err) {
      if (err instanceof ApiError && err.code === 'captcha_failed') {
        setCaptchaToken('');
        setResetSignal((value) => value + 1);
      }
      setError(err instanceof ApiError ? err.message : t('common.error.unknown'));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-col gap-1">
        <h2 className="font-heading text-xl font-semibold">{t('auth.passwordRecovery.title')}</h2>
        <p className="text-muted-foreground text-sm">{t('auth.passwordRecovery.description')}</p>
      </div>

      {sent ? (
        <>
          <Alert className="border-success/30 bg-success/10">
            <AlertDescription className="text-success">
              {t('auth.passwordRecovery.success')}
            </AlertDescription>
          </Alert>
          <Button asChild>
            <RouterLink to="/login">{t('auth.passwordRecovery.goLogin')}</RouterLink>
          </Button>
        </>
      ) : (
        <form className="flex flex-col gap-4" onSubmit={handleSubmit} noValidate>
          {error && (
            <Alert variant="destructive">
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          )}
          <div className="flex flex-col gap-2">
            <Label htmlFor="password-recovery-email">{t('auth.passwordRecovery.email')}</Label>
            <Input
              id="password-recovery-email"
              type="email"
              value={email}
              onChange={(event) => setEmail(event.target.value)}
              autoComplete="email"
              autoFocus
            />
          </div>
          <CaptchaWidget
            config={captchaConfig}
            onToken={setCaptchaToken}
            resetSignal={resetSignal}
          />
          <Button type="submit" size="lg" disabled={submitting}>
            {submitting ? t('auth.passwordRecovery.submitting') : t('auth.passwordRecovery.submit')}
          </Button>
          <p className="text-center text-sm">
            <RouterLink to="/login" className="text-primary underline-offset-4 hover:underline">
              {t('auth.passwordRecovery.goLogin')}
            </RouterLink>
          </p>
        </form>
      )}
    </div>
  );
}
