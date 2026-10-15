import { api } from './client';
import type {
  CaptchaConfig,
  CurrentUser,
  LoginRequest,
  PasswordResetRequest,
  PasswordResetConfirmRequest,
  PasswordChangeRequest,
  ProfileUpdateRequest,
  RegisterRequest,
  TotpSetupResponse,
} from './types';

export const authApi = {
  login: (body: LoginRequest) =>
    api.post<CurrentUser>('/auth/login', body, { silent: true }).then((r) => r.data),

  register: (body: RegisterRequest) =>
    api.post<void>('/auth/register', body, { silent: true }).then(() => undefined),

  logout: () => api.post<void>('/auth/logout', undefined, { silent: true }).then(() => undefined),

  me: () => api.get<CurrentUser>('/auth/me', { silent: true }).then((r) => r.data),

  changePassword: (body: PasswordChangeRequest) =>
    api.patch<void>('/auth/password', body, { silent: true }).then(() => undefined),

  requestPasswordReset: (body: PasswordResetRequest) =>
    api.post<void>('/auth/password/reset/request', body, { silent: true }).then(() => undefined),

  resetPassword: (body: PasswordResetConfirmRequest) =>
    api.post<void>('/auth/password/reset', body, { silent: true }).then(() => undefined),

  updateProfile: (body: ProfileUpdateRequest) =>
    api.patch<CurrentUser>('/auth/profile', body, { silent: true }).then((r) => r.data),

  totpSetup: () =>
    api
      .post<TotpSetupResponse>('/auth/totp/setup', undefined, { silent: true })
      .then((r) => r.data),

  /** secret 来自 totpSetup (后端要求回传, 才会落库启用) */
  totpEnable: (secret: string, code: string) =>
    api.post<void>('/auth/totp/enable', { secret, code }, { silent: true }).then(() => undefined),

  totpDisable: (password: string) =>
    api.post<void>('/auth/totp/disable', { password }, { silent: true }).then(() => undefined),

  captchaConfig: () =>
    api.get<CaptchaConfig>('/auth/captcha', { silent: true }).then((r) => r.data),

  verifyEmail: (token: string) =>
    api.post<void>('/auth/verify-email', { token }).then(() => undefined),

  resendVerification: () =>
    api.post<void>('/auth/resend-verification', undefined, { silent: true }).then(() => undefined),
};
