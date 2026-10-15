import { useState } from 'react';
import type { FormEvent } from 'react';
import { Link as RouterLink, useNavigate } from 'react-router';
import { useQuery } from '@tanstack/react-query';
import { toast } from 'sonner';
import { useTranslation } from 'react-i18next';
import { ApiError, authApi } from '../api';
import type { CaptchaConfig } from '../api';
import { CaptchaWidget } from '../components/CaptchaWidget';
import { AuthCardSkeleton } from '../components/skeletons';
import { Alert, AlertDescription } from '../components/ui/alert';
import { Button } from '../components/ui/button';
import { Input } from '../components/ui/input';
import { Label } from '../components/ui/label';
import { useSiteName } from '../hooks/useSite';
import { useAuth } from '../hooks/useAuth';

const EMAIL_PATTERN = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;
const MIN_PASSWORD_LENGTH = 8;

interface RegisterFieldErrors {
  username?: string;
  email?: string;
  password?: string;
  confirmPassword?: string;
}

/** 注册页: 用户名 / 邮箱 / 密码 + 人机验证 (/register) */
export default function RegisterPage() {
  const { t } = useTranslation();
  const siteName = useSiteName();
  const { isLoading, refresh } = useAuth();
  const navigate = useNavigate();

  const [username, setUsername] = useState('');
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [confirmPassword, setConfirmPassword] = useState('');
  const [captchaToken, setCaptchaToken] = useState('');
  const [resetSignal, setResetSignal] = useState(0);
  const [error, setError] = useState<string | null>(null);
  const [registerDisabled, setRegisterDisabled] = useState(false);
  const [disableViewReason, setDisableViewReason] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<RegisterFieldErrors>({});
  const [submitting, setSubmitting] = useState(false);

  const captchaQuery = useQuery({
    queryKey: ['auth', 'captcha'],
    queryFn: authApi.captchaConfig,
    staleTime: Infinity,
  });
  const captchaConfig: CaptchaConfig | undefined = captchaQuery.data;
  const captchaRequired = (captchaConfig?.provider ?? 'none') !== 'none';

  if (isLoading)
    return <AuthCardSkeleton fields={4} captcha={captchaQuery.isPending || captchaRequired} />;

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();

    const trimmedUsername = username.trim();
    const nextErrors: RegisterFieldErrors = {};
    if (trimmedUsername === '') nextErrors.username = t('auth.register.usernameRequired');
    if (email.trim() === '') nextErrors.email = t('auth.register.emailRequired');
    else if (!EMAIL_PATTERN.test(email.trim())) nextErrors.email = t('common.form.invalidEmail');
    if (password === '') nextErrors.password = t('common.form.required');
    else if (password.length < MIN_PASSWORD_LENGTH)
      nextErrors.password = t('common.form.passwordTooShort');
    if (confirmPassword !== password)
      nextErrors.confirmPassword = t('common.form.passwordMismatch');
    setFieldErrors(nextErrors);
    if (Object.keys(nextErrors).length > 0) return;

    if (captchaRequired && captchaToken === '') {
      setError(t('auth.register.captchaRequired'));
      return;
    }

    setError(null);
    setSubmitting(true);
    try {
      await authApi.register({
        username: trimmedUsername,
        email: email.trim(),
        password,
        captcha_token: captchaToken === '' ? undefined : captchaToken,
      });
      toast.success(t('auth.register.success'));
      // 邮箱验证关闭时注册会直接下发会话; 验证开启时不发会话, 需要手动登录
      const session = await refresh();
      navigate(session ? '/' : '/login', { replace: true });
    } catch (err) {
      if (err instanceof ApiError && err.code === 'captcha_failed') {
        setCaptchaToken('');
        setResetSignal((value) => value + 1);
        setError(err.message);
      } else if (err instanceof ApiError && err.code === 'register_disabled') {
        setRegisterDisabled(true);
        setDisableViewReason(err.message);
      } else {
        setError(err instanceof ApiError ? err.message : t('common.error.unknown'));
      }
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <form className="flex flex-col gap-4" onSubmit={handleSubmit} noValidate>
      <div className="flex flex-col gap-1">
        <h2 className="font-heading text-xl font-semibold">{t('auth.register.title')}</h2>
        <p className="text-muted-foreground text-sm">
          {t('auth.register.description', { siteName })}
        </p>
      </div>

      {registerDisabled && (
        <Alert className="border-warning/30 bg-warning/10">
          <AlertDescription className="flex flex-col gap-1 text-warning">
            <p className="text-sm">{t('auth.register.disabled')}</p>
            {disableViewReason && <p className="text-xs">{disableViewReason}</p>}
          </AlertDescription>
        </Alert>
      )}
      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      <div className="flex flex-col gap-2">
        <Label htmlFor="register-username">{t('auth.register.username')}</Label>
        <Input
          id="register-username"
          value={username}
          onChange={(event) => setUsername(event.target.value)}
          aria-invalid={Boolean(fieldErrors.username)}
          autoComplete="username"
          autoFocus
        />
        {fieldErrors.username ? (
          <p className="text-destructive text-sm">{fieldErrors.username}</p>
        ) : (
          <p className="text-muted-foreground text-sm">{t('auth.register.usernameHint')}</p>
        )}
      </div>

      <div className="flex flex-col gap-2">
        <Label htmlFor="register-email">{t('auth.register.email')}</Label>
        <Input
          id="register-email"
          type="email"
          value={email}
          onChange={(event) => setEmail(event.target.value)}
          aria-invalid={Boolean(fieldErrors.email)}
          autoComplete="email"
        />
        {fieldErrors.email && <p className="text-destructive text-sm">{fieldErrors.email}</p>}
      </div>

      <div className="flex flex-col gap-2">
        <Label htmlFor="register-password">{t('auth.register.password')}</Label>
        <Input
          id="register-password"
          type="password"
          value={password}
          onChange={(event) => setPassword(event.target.value)}
          aria-invalid={Boolean(fieldErrors.password)}
          autoComplete="new-password"
        />
        {fieldErrors.password && <p className="text-destructive text-sm">{fieldErrors.password}</p>}
      </div>

      <div className="flex flex-col gap-2">
        <Label htmlFor="register-confirm-password">{t('auth.register.confirmPassword')}</Label>
        <Input
          id="register-confirm-password"
          type="password"
          value={confirmPassword}
          onChange={(event) => setConfirmPassword(event.target.value)}
          aria-invalid={Boolean(fieldErrors.confirmPassword)}
          autoComplete="new-password"
        />
        {fieldErrors.confirmPassword && (
          <p className="text-destructive text-sm">{fieldErrors.confirmPassword}</p>
        )}
      </div>

      <CaptchaWidget config={captchaConfig} onToken={setCaptchaToken} resetSignal={resetSignal} />

      <Button type="submit" size="lg" disabled={submitting || registerDisabled}>
        {submitting ? t('auth.register.submitting') : t('auth.register.submit')}
      </Button>

      <div className="flex flex-col items-center gap-1">
        <p className="text-sm">
          {t('auth.register.hasAccount')}{' '}
          <RouterLink to="/login" className="text-primary underline-offset-4 hover:underline">
            {t('auth.register.goLogin')}
          </RouterLink>
        </p>
      </div>
    </form>
  );
}
