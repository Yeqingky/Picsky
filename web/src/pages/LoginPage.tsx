import { useState } from 'react';
import type { FormEvent } from 'react';
import { Link as RouterLink, useNavigate, useSearchParams } from 'react-router';
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
import { useSiteInfo, useSiteName } from '../hooks/useSite';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '../components/ui/dialog';

/** 登录页: 用户名/邮箱 + 密码 + 按需两步验证 + 人机验证 (/login) */
export default function LoginPage() {
  const { t } = useTranslation();
  const siteName = useSiteName();
  const { data: siteInfo } = useSiteInfo();
  const { login, isLoading } = useAuth();
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();

  const [identifier, setIdentifier] = useState('');
  const [password, setPassword] = useState('');
  const [totp, setTotp] = useState('');
  const [loginChallenge, setLoginChallenge] = useState<string | null>(null);
  const [totpDialogOpen, setTotpDialogOpen] = useState(false);
  const [captchaToken, setCaptchaToken] = useState('');
  const [resetSignal, setResetSignal] = useState(0);
  const [error, setError] = useState<string | null>(null);
  const [totpError, setTotpError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<{ identifier?: string; password?: string }>({});
  const [submitting, setSubmitting] = useState(false);

  const captchaQuery = useQuery({
    queryKey: ['auth', 'captcha'],
    queryFn: authApi.captchaConfig,
    staleTime: Infinity,
  });
  const captchaConfig: CaptchaConfig | undefined = captchaQuery.data;
  const captchaRequired = (captchaConfig?.provider ?? 'none') !== 'none';

  if (isLoading) return <AuthCardSkeleton captcha={captchaQuery.isPending || captchaRequired} />;
  const finishLogin = () => {
    const redirect = searchParams.get('redirect');
    const target =
      redirect && redirect.startsWith('/') && !redirect.startsWith('//') ? redirect : '/';
    navigate(target, { replace: true });
  };

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();

    const nextErrors: { identifier?: string; password?: string } = {};
    if (identifier.trim() === '') nextErrors.identifier = t('auth.login.identifierRequired');
    if (password === '') nextErrors.password = t('auth.login.passwordRequired');
    setFieldErrors(nextErrors);
    if (nextErrors.identifier || nextErrors.password) return;

    if (captchaRequired && captchaToken === '') {
      setError(t('auth.login.captchaRequired'));
      return;
    }

    setError(null);
    setSubmitting(true);
    try {
      await login({
        identifier: identifier.trim(),
        password,
        captcha_token: captchaToken === '' ? undefined : captchaToken,
      });
      finishLogin();
    } catch (err) {
      if (err instanceof ApiError && err.code === 'captcha_failed') {
        setCaptchaToken('');
        setResetSignal((value) => value + 1);
      }
      if (
        err instanceof ApiError &&
        err.details?.totp_required === true &&
        typeof err.details.login_challenge === 'string'
      ) {
        setLoginChallenge(err.details.login_challenge);
        setTotp('');
        setTotpError(null);
        setTotpDialogOpen(true);
        setError(null);
        return;
      }
      setError(err instanceof ApiError ? err.message : t('common.error.unknown'));
    } finally {
      setSubmitting(false);
    }
  };
  const handleTotpChange = async (value: string) => {
    const code = value.replace(/\D/g, '').slice(0, 6);
    setTotp(code);
    setTotpError(null);
    if (code.length !== 6 || submitting || loginChallenge === null) return;

    setSubmitting(true);
    try {
      await login({ login_challenge: loginChallenge, totp: code });
      finishLogin();
    } catch (err) {
      setTotp('');
      setTotpError(err instanceof ApiError ? err.message : t('common.error.unknown'));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <>
      <form className="flex flex-col gap-4" onSubmit={handleSubmit} noValidate>
        <div className="flex flex-col gap-1">
          <h2 className="font-heading text-xl font-semibold">{t('auth.login.title')}</h2>
          <p className="text-muted-foreground text-sm">
            {t('auth.login.description', { siteName })}
          </p>
        </div>

        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}

        <div className="flex flex-col gap-2">
          <Label htmlFor="login-identifier">{t('auth.login.identifier')}</Label>
          <Input
            id="login-identifier"
            value={identifier}
            onChange={(event) => setIdentifier(event.target.value)}
            aria-invalid={Boolean(fieldErrors.identifier)}
            autoComplete="username"
            autoFocus
          />
          {fieldErrors.identifier && (
            <p className="text-destructive text-sm">{fieldErrors.identifier}</p>
          )}
        </div>

        <div className="flex flex-col gap-2">
          <Label htmlFor="login-password">{t('auth.login.password')}</Label>
          <Input
            id="login-password"
            type="password"
            value={password}
            onChange={(event) => setPassword(event.target.value)}
            aria-invalid={Boolean(fieldErrors.password)}
            autoComplete="current-password"
          />
          {fieldErrors.password && (
            <p className="text-destructive text-sm">{fieldErrors.password}</p>
          )}
        </div>
        {siteInfo?.password_reset_enabled && (
          <div className="text-right text-sm">
            <RouterLink
              to="/forgot-password"
              className="text-primary underline-offset-4 hover:underline"
            >
              {t('auth.login.forgotPassword')}
            </RouterLink>
          </div>
        )}

        <CaptchaWidget config={captchaConfig} onToken={setCaptchaToken} resetSignal={resetSignal} />

        <Button type="submit" size="lg" disabled={submitting}>
          {submitting ? t('auth.login.submitting') : t('auth.login.submit')}
        </Button>

        <div className="flex flex-col items-center gap-1">
          <p className="text-sm">
            {t('auth.login.noAccount')}{' '}
            <RouterLink to="/register" className="text-primary underline-offset-4 hover:underline">
              {t('auth.login.goRegister')}
            </RouterLink>
          </p>
        </div>
      </form>
      <Dialog
        open={totpDialogOpen}
        onOpenChange={(open) => {
          if (open) {
            setTotpDialogOpen(true);
            return;
          }
          if (submitting) return;
          setTotpDialogOpen(false);
          setLoginChallenge(null);
          setTotp('');
          setTotpError(null);
          setCaptchaToken('');
          setResetSignal((value) => value + 1);
        }}
      >
        <DialogContent className="sm:max-w-sm">
          <DialogHeader>
            <DialogTitle>{t('auth.login.totpDialogTitle')}</DialogTitle>
            <DialogDescription>{t('auth.login.totpHint')}</DialogDescription>
          </DialogHeader>
          {totpError && (
            <Alert variant="destructive">
              <AlertDescription>{totpError}</AlertDescription>
            </Alert>
          )}
          <div className="flex flex-col gap-2">
            <Label htmlFor="login-totp">{t('auth.login.totp')}</Label>
            <Input
              id="login-totp"
              value={totp}
              onChange={(event) => void handleTotpChange(event.target.value)}
              placeholder={t('auth.login.totpPlaceholder')}
              inputMode="numeric"
              maxLength={6}
              autoComplete="one-time-code"
              autoFocus
              disabled={submitting}
            />
            {submitting && (
              <p className="text-muted-foreground text-sm">{t('auth.login.submitting')}</p>
            )}
          </div>
        </DialogContent>
      </Dialog>
    </>
  );
}
