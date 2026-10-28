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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '../components/ui/select';
import { useAuth } from '../hooks/useAuth';
import { useSiteInfo } from '../hooks/useSite';

const EMAIL_PATTERN = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;
const USERNAME_PATTERN = /^[A-Za-z0-9]+$/;
const MAX_USERNAME_LENGTH = 64;
const MIN_PASSWORD_LENGTH = 8;
const MAX_PASSWORD_BYTES = 72;

interface RegisterFieldErrors {
  username?: string;
  email?: string;
  password?: string;
  confirmPassword?: string;
}

/** 注册页: 用户名 / 邮箱 / 密码 + 人机验证 (/register) */
export default function RegisterPage() {
  const { t } = useTranslation();
  const { isLoading } = useAuth();
  const navigate = useNavigate();

  const [username, setUsername] = useState('');
  const [email, setEmail] = useState('');
  const [emailDomain, setEmailDomain] = useState('');
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
  const siteQuery = useSiteInfo();
  const emailDomains = siteQuery.data?.registration_email_domains ?? [];
  const domainSelectEnabled = emailDomains.length > 0;
  const selectedEmailDomain = emailDomains.includes(emailDomain)
    ? emailDomain
    : (emailDomains[0] ?? '');
  const captchaConfig: CaptchaConfig | undefined = captchaQuery.data;
  const captchaRequired = (captchaConfig?.provider ?? 'none') !== 'none';

  if (isLoading || siteQuery.isPending)
    return <AuthCardSkeleton fields={4} captcha={captchaQuery.isPending || captchaRequired} />;

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();

    const trimmedUsername = username.trim();
    const emailLocal = email.trim().toLowerCase();
    const normalizedEmail = domainSelectEnabled
      ? `${emailLocal}@${selectedEmailDomain.toLowerCase()}`
      : emailLocal;
    const nextErrors: RegisterFieldErrors = {};
    if (trimmedUsername === '') nextErrors.username = t('auth.register.usernameRequired');
    else if (
      trimmedUsername.length > MAX_USERNAME_LENGTH ||
      !USERNAME_PATTERN.test(trimmedUsername)
    )
      nextErrors.username = t('auth.register.usernameInvalid');
    if (emailLocal === '')
      nextErrors.email = t(
        domainSelectEnabled ? 'auth.register.emailLocalRequired' : 'auth.register.emailRequired',
      );
    else if (!EMAIL_PATTERN.test(normalizedEmail)) nextErrors.email = t('common.form.invalidEmail');
    if (password === '') nextErrors.password = t('common.form.required');
    else if (Array.from(password).length < MIN_PASSWORD_LENGTH)
      nextErrors.password = t('common.form.passwordTooShort');
    else if (new TextEncoder().encode(password).length > MAX_PASSWORD_BYTES)
      nextErrors.password = t('common.form.passwordTooLong');
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
        email: normalizedEmail,
        password,
        captcha_token: captchaToken === '' ? undefined : captchaToken,
      });
      toast.success(t('auth.register.success'));
      navigate('/login', { replace: true });
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
      <h2 className="font-heading text-xl font-semibold">{t('auth.register.title')}</h2>

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
        <div className="flex items-center justify-between gap-2">
          <Label htmlFor="register-username">{t('auth.register.username')}</Label>
          {fieldErrors.username && (
            <p id="register-username-error" className="text-destructive text-right text-sm">
              {fieldErrors.username}
            </p>
          )}
        </div>
        <Input
          id="register-username"
          value={username}
          onChange={(event) => setUsername(event.target.value)}
          onBlur={(event) => setUsername(event.currentTarget.value.trim())}
          onPaste={(event) => {
            const input = event.currentTarget;
            const start = input.selectionStart ?? input.value.length;
            const end = input.selectionEnd ?? input.value.length;
            const pasted = event.clipboardData.getData('text').replace(/[^A-Za-z0-9]/g, '');
            const nextValue = input.value.slice(0, start) + pasted + input.value.slice(end);
            event.preventDefault();
            setUsername(nextValue);
            const cursor = start + pasted.length;
            requestAnimationFrame(() => input.setSelectionRange(cursor, cursor));
          }}
          aria-invalid={Boolean(fieldErrors.username)}
          aria-describedby={fieldErrors.username ? 'register-username-error' : undefined}
          autoComplete="username"
          autoFocus
        />
      </div>

      <div className="flex flex-col gap-2">
        <div className="flex items-center justify-between gap-2">
          <Label htmlFor="register-email">{t('auth.register.email')}</Label>
          {fieldErrors.email && (
            <p id="register-email-error" className="text-destructive text-right text-sm">
              {fieldErrors.email}
            </p>
          )}
        </div>
        {domainSelectEnabled ? (
          <div className="flex min-w-0 gap-2">
            <Input
              id="register-email"
              className="min-w-0 flex-1"
              type="text"
              value={email}
              onChange={(event) => setEmail(event.target.value.toLowerCase())}
              placeholder={t('auth.register.emailLocalPlaceholder')}
              aria-invalid={Boolean(fieldErrors.email)}
              aria-describedby={fieldErrors.email ? 'register-email-error' : undefined}
              autoComplete="off"
            />
            <Select value={selectedEmailDomain} onValueChange={setEmailDomain}>
              <SelectTrigger
                className="w-40 shrink-0"
                aria-label={t('auth.register.emailDomain')}
                aria-invalid={Boolean(fieldErrors.email)}
              >
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {emailDomains.map((domain) => (
                  <SelectItem key={domain} value={domain}>
                    @{domain}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        ) : (
          <Input
            id="register-email"
            type="email"
            value={email}
            onChange={(event) => setEmail(event.target.value.toLowerCase())}
            aria-invalid={Boolean(fieldErrors.email)}
            aria-describedby={fieldErrors.email ? 'register-email-error' : undefined}
            autoComplete="email"
          />
        )}
      </div>

      <div className="flex flex-col gap-2">
        <div className="flex items-center justify-between gap-2">
          <Label htmlFor="register-password">{t('auth.register.password')}</Label>
          {fieldErrors.password && (
            <p id="register-password-error" className="text-destructive text-right text-sm">
              {fieldErrors.password}
            </p>
          )}
        </div>
        <Input
          id="register-password"
          type="password"
          value={password}
          onChange={(event) => setPassword(event.target.value)}
          aria-invalid={Boolean(fieldErrors.password)}
          aria-describedby={fieldErrors.password ? 'register-password-error' : undefined}
          autoComplete="new-password"
        />
      </div>

      <div className="flex flex-col gap-2">
        <div className="flex items-center justify-between gap-2">
          <Label htmlFor="register-confirm-password">{t('auth.register.confirmPassword')}</Label>
          {fieldErrors.confirmPassword && (
            <p id="register-confirm-password-error" className="text-destructive text-right text-sm">
              {fieldErrors.confirmPassword}
            </p>
          )}
        </div>
        <Input
          id="register-confirm-password"
          type="password"
          value={confirmPassword}
          onChange={(event) => setConfirmPassword(event.target.value)}
          aria-invalid={Boolean(fieldErrors.confirmPassword)}
          aria-describedby={
            fieldErrors.confirmPassword ? 'register-confirm-password-error' : undefined
          }
          autoComplete="new-password"
        />
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
