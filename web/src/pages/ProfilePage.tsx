import { useEffect, useState } from 'react';
import type { ReactNode } from 'react';
import { toast } from 'sonner';
import { useMutation } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { ApiError, authApi } from '../api';
import type { PasswordChangeRequest, TotpSetupResponse } from '../api';
import { PageHeader } from '../components/PageHeader';
import { CopyButton } from '../components/CopyButton';
import { QuotaBar } from '../components/QuotaBar';
import { ErrorState } from '../components/States';
import { FormSkeleton } from '../components/skeletons';
import { Alert, AlertDescription } from '../components/ui/alert';
import { Badge } from '../components/ui/badge';
import { Button } from '../components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '../components/ui/card';
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '../components/ui/dialog';
import { Input } from '../components/ui/input';
import { Label } from '../components/ui/label';
import { RadioGroup, RadioGroupItem } from '../components/ui/radio-group';
import { useAuth } from '../hooks/useAuth';
import { useThemeMode } from '../theme/ThemeModeProvider';
import type { ThemeMode } from '../theme';

const MIN_PASSWORD_LENGTH = 8;
const THEME_MODES: ThemeMode[] = ['light', 'dark', 'system'];

interface PasswordFieldErrors {
  oldPassword?: string;
  newPassword?: string;
  confirmPassword?: string;
}

function SectionCard({ title, children }: { title: string; children: ReactNode }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">{children}</CardContent>
    </Card>
  );
}

function ReadonlyField({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex min-w-0 items-center gap-2">
      <span className="text-muted-foreground w-20 shrink-0 text-xs">{label}</span>
      {children}
    </div>
  );
}

function errorMessage(error: unknown, fallback: string): string {
  return error instanceof ApiError ? error.message : fallback;
}

/** 个人设置页 (/profile): 账号信息, 配额, 密码, 两步验证, 偏好 */
export default function ProfilePage() {
  const { t } = useTranslation();
  const { user, isLoading, updateUser, refresh } = useAuth();
  const { mode, setMode } = useThemeMode();

  const [nickname, setNickname] = useState('');
  const [oldPassword, setOldPassword] = useState('');
  const [newPassword, setNewPassword] = useState('');
  const [confirmPassword, setConfirmPassword] = useState('');
  const [passwordErrors, setPasswordErrors] = useState<PasswordFieldErrors>({});

  const [totpSetupData, setTotpSetupData] = useState<TotpSetupResponse | null>(null);
  const [totpCode, setTotpCode] = useState('');
  const [totpError, setTotpError] = useState<string | null>(null);
  const [disableOpen, setDisableOpen] = useState(false);
  const [disablePassword, setDisablePassword] = useState('');
  const [disableError, setDisableError] = useState<string | null>(null);

  const storedNickname = user?.nickname ?? '';
  useEffect(() => {
    setNickname(storedNickname);
  }, [storedNickname]);

  const resendMutation = useMutation({
    mutationFn: () => authApi.resendVerification(),
    onSuccess: () => toast.success(t('account.profile.resendSent')),
  });

  const nicknameMutation = useMutation({
    mutationFn: (value: string) => authApi.updateProfile({ nickname: value }),
    onSuccess: (updated) => {
      updateUser(updated);
      toast.success(t('account.profile.nicknameSaved'));
    },
  });

  const passwordMutation = useMutation({
    mutationFn: (body: PasswordChangeRequest) => authApi.changePassword(body),
    onSuccess: () => {
      setOldPassword('');
      setNewPassword('');
      setConfirmPassword('');
      setPasswordErrors({});
      toast.success(t('account.profile.passwordChanged'));
    },
  });

  const setupMutation = useMutation({
    mutationFn: () => authApi.totpSetup(),
    onSuccess: (data) => {
      setTotpSetupData(data);
      setTotpCode('');
      setTotpError(null);
    },
    onError: (error) => setTotpError(errorMessage(error, t('common.error.unknown'))),
  });

  const enableMutation = useMutation({
    mutationFn: (payload: { secret: string; code: string }) =>
      authApi.totpEnable(payload.secret, payload.code),
    onSuccess: async () => {
      setTotpSetupData(null);
      setTotpCode('');
      setTotpError(null);
      await refresh();
      toast.success(t('account.profile.totpEnabledSuccess'));
    },
    onError: (error) => setTotpError(errorMessage(error, t('common.error.unknown'))),
  });

  const disableMutation = useMutation({
    mutationFn: (password: string) => authApi.totpDisable(password),
    onSuccess: async () => {
      setDisableOpen(false);
      setDisablePassword('');
      setDisableError(null);
      await refresh();
      toast.success(t('account.profile.totpDisabledSuccess'));
    },
    onError: (error) => setDisableError(errorMessage(error, t('common.error.unknown'))),
  });

  if (isLoading) return <FormSkeleton fields={4} grouped />;
  if (!user) return <ErrorState onRetry={() => void refresh()} />;

  const handlePasswordSubmit = () => {
    const nextErrors: PasswordFieldErrors = {};
    if (oldPassword === '') nextErrors.oldPassword = t('account.profile.oldPasswordRequired');
    if (newPassword === '') nextErrors.newPassword = t('common.form.required');
    else if (newPassword.length < MIN_PASSWORD_LENGTH)
      nextErrors.newPassword = t('common.form.passwordTooShort');
    if (confirmPassword !== newPassword)
      nextErrors.confirmPassword = t('common.form.passwordMismatch');
    setPasswordErrors(nextErrors);
    if (Object.keys(nextErrors).length > 0) return;
    passwordMutation.mutate({ old_password: oldPassword, new_password: newPassword });
  };

  const handleTotpEnable = () => {
    if (totpCode.trim().length !== 6) {
      setTotpError(t('account.profile.totpCodeRequired'));
      return;
    }
    setTotpError(null);
    if (totpSetupData)
      enableMutation.mutate({ secret: totpSetupData.secret, code: totpCode.trim() });
  };

  return (
    <>
      <PageHeader title={t('nav.profile')} description={t('account.profile.description')} />

      <div className="flex flex-col gap-4">
        <SectionCard title={t('account.profile.accountSection')}>
          <ReadonlyField label={t('account.profile.username')}>
            <span className="text-sm">{user.username}</span>
          </ReadonlyField>
          <ReadonlyField label={t('account.profile.email')}>
            <span className="text-sm break-all">{user.email}</span>
            {user.email_verified ? (
              <Badge className="border-transparent bg-success/10 text-success">
                {t('account.profile.emailVerified')}
              </Badge>
            ) : (
              <Badge className="border-transparent bg-warning/10 text-warning">
                {t('account.profile.emailUnverified')}
              </Badge>
            )}
          </ReadonlyField>
          <ReadonlyField label={t('account.profile.group')}>
            <span className="text-sm">{user.group.name}</span>
          </ReadonlyField>

          {!user.email_verified && (
            <div className="flex flex-col items-start gap-2">
              <Button
                variant="outline"
                size="sm"
                disabled={resendMutation.isPending}
                onClick={() => resendMutation.mutate()}
              >
                {t('account.profile.resendVerification')}
              </Button>
              {resendMutation.isError && (
                <Alert variant="destructive">
                  <AlertDescription>
                    {errorMessage(resendMutation.error, t('common.error.unknown'))}
                  </AlertDescription>
                </Alert>
              )}
            </div>
          )}

          <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
            <div className="flex min-w-0 flex-1 flex-col gap-2 sm:max-w-[260px]">
              <Label htmlFor="profile-nickname">{t('account.profile.nickname')}</Label>
              <Input
                id="profile-nickname"
                value={nickname}
                onChange={(event) => setNickname(event.target.value)}
                placeholder={t('account.profile.nicknamePlaceholder')}
              />
            </div>
            <Button
              className="self-start sm:self-end"
              disabled={nicknameMutation.isPending || nickname.trim() === ''}
              onClick={() => nicknameMutation.mutate(nickname.trim())}
            >
              {t('common.action.save')}
            </Button>
          </div>
        </SectionCard>

        <SectionCard title={t('account.profile.quotaSection')}>
          <QuotaBar quota={user.quota} />
        </SectionCard>

        <SectionCard title={t('account.profile.passwordSection')}>
          <p className="text-muted-foreground text-sm">
            {t('account.profile.passwordDescription')}
          </p>
          {passwordMutation.isError && (
            <Alert variant="destructive">
              <AlertDescription>
                {errorMessage(passwordMutation.error, t('common.error.unknown'))}
              </AlertDescription>
            </Alert>
          )}
          <div className="flex flex-col gap-2">
            <Label htmlFor="profile-old-password">{t('account.profile.oldPassword')}</Label>
            <Input
              id="profile-old-password"
              type="password"
              value={oldPassword}
              onChange={(event) => setOldPassword(event.target.value)}
              aria-invalid={Boolean(passwordErrors.oldPassword)}
              autoComplete="current-password"
            />
            {passwordErrors.oldPassword && (
              <p className="text-destructive text-sm">{passwordErrors.oldPassword}</p>
            )}
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="profile-new-password">{t('account.profile.newPassword')}</Label>
            <Input
              id="profile-new-password"
              type="password"
              value={newPassword}
              onChange={(event) => setNewPassword(event.target.value)}
              aria-invalid={Boolean(passwordErrors.newPassword)}
              autoComplete="new-password"
            />
            {passwordErrors.newPassword && (
              <p className="text-destructive text-sm">{passwordErrors.newPassword}</p>
            )}
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="profile-confirm-password">{t('account.profile.confirmPassword')}</Label>
            <Input
              id="profile-confirm-password"
              type="password"
              value={confirmPassword}
              onChange={(event) => setConfirmPassword(event.target.value)}
              aria-invalid={Boolean(passwordErrors.confirmPassword)}
              autoComplete="new-password"
            />
            {passwordErrors.confirmPassword && (
              <p className="text-destructive text-sm">{passwordErrors.confirmPassword}</p>
            )}
          </div>
          <div>
            <Button disabled={passwordMutation.isPending} onClick={handlePasswordSubmit}>
              {t('common.action.save')}
            </Button>
          </div>
        </SectionCard>

        <SectionCard title={t('account.profile.totpSection')}>
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-muted-foreground text-sm">
              {t('account.profile.totpDescription')}
            </span>
            {user.totp_enabled ? (
              <Badge className="border-transparent bg-success/10 text-success">
                {t('account.profile.totpEnabled')}
              </Badge>
            ) : (
              <Badge variant="outline">{t('account.profile.totpDisabled')}</Badge>
            )}
          </div>

          {totpError && !totpSetupData && (
            <Alert variant="destructive">
              <AlertDescription>{totpError}</AlertDescription>
            </Alert>
          )}

          {!user.totp_enabled && totpSetupData === null && (
            <div>
              <Button
                variant="outline"
                disabled={setupMutation.isPending}
                onClick={() => setupMutation.mutate()}
              >
                {t('account.profile.totpEnable')}
              </Button>
            </div>
          )}

          {totpSetupData && (
            <div className="flex flex-col gap-3">
              <p className="text-muted-foreground text-sm">{t('account.profile.totpSetupHint')}</p>
              <ReadonlyField label={t('account.profile.totpSecret')}>
                <span className="font-mono text-sm break-all">{totpSetupData.secret}</span>
                <CopyButton value={totpSetupData.secret} />
              </ReadonlyField>
              <ReadonlyField label={t('account.profile.totpOtpAuthUrl')}>
                <span className="font-mono text-sm break-all">{totpSetupData.otpauth_url}</span>
                <CopyButton value={totpSetupData.otpauth_url} />
              </ReadonlyField>
              {totpError && (
                <Alert variant="destructive">
                  <AlertDescription>{totpError}</AlertDescription>
                </Alert>
              )}
              <div className="flex flex-col gap-2 sm:flex-row sm:items-end">
                <div className="flex flex-col gap-2 sm:w-[220px]">
                  <Label htmlFor="profile-totp-code">{t('account.profile.totpCode')}</Label>
                  <Input
                    id="profile-totp-code"
                    value={totpCode}
                    onChange={(event) => setTotpCode(event.target.value)}
                    placeholder={t('account.profile.totpCodePlaceholder')}
                    inputMode="numeric"
                    maxLength={6}
                  />
                </div>
                <Button disabled={enableMutation.isPending} onClick={handleTotpEnable}>
                  {t('account.profile.totpEnableSubmit')}
                </Button>
                <Button
                  variant="outline"
                  disabled={enableMutation.isPending}
                  onClick={() => {
                    setTotpSetupData(null);
                    setTotpCode('');
                    setTotpError(null);
                  }}
                >
                  {t('account.profile.totpCancel')}
                </Button>
              </div>
            </div>
          )}

          {user.totp_enabled && (
            <div>
              <Button
                variant="destructive"
                onClick={() => {
                  setDisableError(null);
                  setDisablePassword('');
                  setDisableOpen(true);
                }}
              >
                {t('account.profile.totpDisable')}
              </Button>
            </div>
          )}
        </SectionCard>

        <SectionCard title={t('account.profile.preferencesSection')}>
          <div className="flex flex-col gap-2">
            <span className="text-sm font-medium">{t('account.profile.theme')}</span>
            <RadioGroup
              value={mode}
              onValueChange={(value) => setMode(value as ThemeMode)}
              className="flex flex-row flex-wrap gap-4"
            >
              {THEME_MODES.map((value) => (
                <label key={value} className="flex items-center gap-2 text-sm">
                  <RadioGroupItem value={value} />
                  {t(`common.theme.${value}`)}
                </label>
              ))}
            </RadioGroup>
          </div>
          <div className="flex flex-col gap-1">
            <span className="text-sm font-medium">{t('account.profile.language')}</span>
            <span className="text-sm">{t('account.profile.languageValue')}</span>
            <span className="text-muted-foreground text-xs">
              {t('account.profile.languageHint')}
            </span>
          </div>
        </SectionCard>
      </div>

      <Dialog
        open={disableOpen}
        onOpenChange={(next) => {
          if (!next && !disableMutation.isPending) setDisableOpen(false);
        }}
      >
        <DialogContent className="sm:max-w-sm">
          <DialogHeader>
            <DialogTitle>{t('account.profile.totpDisableTitle')}</DialogTitle>
          </DialogHeader>
          <div className="flex flex-col gap-4">
            <p className="text-muted-foreground text-sm">{t('account.profile.totpDisableHint')}</p>
            {disableError && (
              <Alert variant="destructive">
                <AlertDescription>{disableError}</AlertDescription>
              </Alert>
            )}
            <div className="flex flex-col gap-2">
              <Label htmlFor="profile-disable-password">{t('account.profile.oldPassword')}</Label>
              <Input
                id="profile-disable-password"
                type="password"
                value={disablePassword}
                onChange={(event) => setDisablePassword(event.target.value)}
                autoComplete="current-password"
                autoFocus
              />
            </div>
          </div>
          <DialogFooter>
            <Button
              variant="outline"
              disabled={disableMutation.isPending}
              onClick={() => setDisableOpen(false)}
            >
              {t('common.action.cancel')}
            </Button>
            <Button
              variant="destructive"
              disabled={disableMutation.isPending || disablePassword === ''}
              onClick={() => disableMutation.mutate(disablePassword)}
            >
              {t('account.profile.totpDisableSubmit')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
