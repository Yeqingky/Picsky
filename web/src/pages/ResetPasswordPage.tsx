import { useLayoutEffect, useState } from 'react';
import type { FormEvent } from 'react';
import { Link as RouterLink, useLocation } from 'react-router';
import { useTranslation } from 'react-i18next';
import { ApiError, authApi } from '../api';
import { Alert, AlertDescription } from '../components/ui/alert';
import { Button } from '../components/ui/button';
import { Input } from '../components/ui/input';
import { Label } from '../components/ui/label';

const MIN_PASSWORD_LENGTH = 8;

/** 通过邮件令牌设置新密码; 后端在同一事务中关闭 TOTP (/reset-password). */
export default function ResetPasswordPage() {
  const { t } = useTranslation();
  const location = useLocation();
  const [token] = useState(
    () => new URLSearchParams(location.hash.slice(1)).get('token')?.trim() ?? '',
  );
  const [password, setPassword] = useState('');
  const [confirmPassword, setConfirmPassword] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  useLayoutEffect(() => {
    if (token === '' || window.location.hash === '') return;
    window.history.replaceState(
      window.history.state,
      '',
      `${window.location.pathname}${window.location.search}`,
    );
  }, [token]);

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (password === '') {
      setError(t('auth.passwordReset.passwordRequired'));
      return;
    }
    if (password.length < MIN_PASSWORD_LENGTH) {
      setError(t('common.form.passwordTooShort'));
      return;
    }
    if (password !== confirmPassword) {
      setError(t('common.form.passwordMismatch'));
      return;
    }

    setError(null);
    setSubmitting(true);
    try {
      await authApi.resetPassword({ token, password });
      setSuccess(true);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : t('common.error.unknown'));
    } finally {
      setSubmitting(false);
    }
  };

  if (token === '') {
    return (
      <div className="flex flex-col gap-4">
        <h2 className="font-heading text-xl font-semibold">{t('auth.passwordReset.title')}</h2>
        <Alert className="border-warning/30 bg-warning/10">
          <AlertDescription className="text-warning">
            {t('auth.passwordReset.missingToken')}
          </AlertDescription>
        </Alert>
        <Button asChild>
          <RouterLink to="/login">{t('auth.passwordReset.goLogin')}</RouterLink>
        </Button>
      </div>
    );
  }

  if (success) {
    return (
      <div className="flex flex-col gap-4">
        <div className="flex flex-col gap-1">
          <h2 className="font-heading text-xl font-semibold">{t('auth.passwordReset.title')}</h2>
          <p className="text-muted-foreground text-sm">{t('auth.passwordReset.description')}</p>
        </div>
        <Alert className="border-success/30 bg-success/10">
          <AlertDescription className="text-success">
            {t('auth.passwordReset.success')}
          </AlertDescription>
        </Alert>
        <Button asChild>
          <RouterLink to="/login">{t('auth.passwordReset.goLogin')}</RouterLink>
        </Button>
      </div>
    );
  }

  return (
    <form className="flex flex-col gap-4" onSubmit={handleSubmit} noValidate>
      <div className="flex flex-col gap-1">
        <h2 className="font-heading text-xl font-semibold">{t('auth.passwordReset.title')}</h2>
        <p className="text-muted-foreground text-sm">{t('auth.passwordReset.description')}</p>
      </div>
      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      <div className="flex flex-col gap-2">
        <Label htmlFor="password-reset-new">{t('auth.passwordReset.newPassword')}</Label>
        <Input
          id="password-reset-new"
          type="password"
          value={password}
          onChange={(event) => setPassword(event.target.value)}
          autoComplete="new-password"
          autoFocus
        />
      </div>
      <div className="flex flex-col gap-2">
        <Label htmlFor="password-reset-confirm">{t('auth.passwordReset.confirmPassword')}</Label>
        <Input
          id="password-reset-confirm"
          type="password"
          value={confirmPassword}
          onChange={(event) => setConfirmPassword(event.target.value)}
          autoComplete="new-password"
        />
      </div>
      <Button type="submit" size="lg" disabled={submitting}>
        {submitting ? t('auth.passwordReset.submitting') : t('auth.passwordReset.submit')}
      </Button>
      <p className="text-center text-sm">
        <RouterLink to="/login" className="text-primary underline-offset-4 hover:underline">
          {t('auth.passwordReset.goLogin')}
        </RouterLink>
      </p>
    </form>
  );
}
