import axios from 'axios';
import type { AxiosInstance, AxiosRequestConfig } from 'axios';
import type { ApiErrorBody } from './types';

declare module 'axios' {
  export interface AxiosRequestConfig {
    /** true 时不弹出全局错误提示 (由调用方自行处理) */
    silent?: boolean;
  }
}

/** 统一的后端错误 (含网络错误: status 0) */
export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly details?: Record<string, unknown> | null;

  constructor(params: {
    status: number;
    code: string;
    message: string;
    details?: Record<string, unknown> | null;
  }) {
    super(params.message);
    this.name = 'ApiError';
    this.status = params.status;
    this.code = params.code;
    this.details = params.details;
  }

  get isUnauthorized(): boolean {
    return this.status === 401;
  }

  get isForbidden(): boolean {
    return this.status === 403;
  }

  get isNetworkError(): boolean {
    return this.status === 0;
  }
}

type UnauthorizedHandler = (error: ApiError) => void;
type ErrorNotifier = (message: string, error: ApiError) => void;

let unauthorizedHandler: UnauthorizedHandler | null = null;
let errorNotifier: ErrorNotifier | null = null;

/** 由应用挂载 (会话失效时跳转登录) */
export function setUnauthorizedHandler(handler: UnauthorizedHandler | null): void {
  unauthorizedHandler = handler;
}

/** 由应用挂载 (sonner) */
export function setErrorNotifier(notifier: ErrorNotifier | null): void {
  errorNotifier = notifier;
}

/**
 * 这些端点上的 401 属于业务结果 (凭据错误 / 会话探测), 不触发全局跳转登录:
 * 登录与注册的凭据校验, 会话探测本身, 以及需要再次输入密码的敏感操作。
 */
const SILENT_401_PATHS = [
  '/auth/login',
  '/auth/me',
  '/auth/register',
  '/auth/password',
  '/auth/totp/disable',
];

export const api: AxiosInstance = axios.create({
  baseURL: '/api/v1',
  withCredentials: true,
  timeout: 120_000,
  headers: { Accept: 'application/json' },
});

function toApiError(error: unknown): ApiError {
  if (error instanceof ApiError) return error;

  if (axios.isAxiosError(error)) {
    const response = error.response;
    if (!response) {
      return new ApiError({
        status: 0,
        code: 'network_error',
        message: '网络错误, 请检查连接后重试',
      });
    }
    const body = (response.data ?? {}) as Partial<ApiErrorBody>;
    return new ApiError({
      status: response.status,
      code: typeof body.code === 'string' ? body.code : `http_${response.status}`,
      message:
        typeof body.message === 'string' && body.message.length > 0
          ? body.message
          : `请求失败 (${response.status})`,
      details: body.details ?? null,
    });
  }

  return new ApiError({
    status: 0,
    code: 'unknown_error',
    message: error instanceof Error ? error.message : '未知错误',
  });
}

api.interceptors.response.use(
  (response) => response,
  (error: unknown) => {
    const apiError = toApiError(error);
    const config = axios.isAxiosError(error)
      ? (error.config as AxiosRequestConfig | undefined)
      : undefined;
    const url = config?.url ?? '';

    if (apiError.isUnauthorized && !SILENT_401_PATHS.some((path) => url.startsWith(path))) {
      unauthorizedHandler?.(apiError);
      return Promise.reject(apiError);
    }

    if (!config?.silent) {
      errorNotifier?.(apiError.message, apiError);
    }

    return Promise.reject(apiError);
  },
);

export type { AxiosRequestConfig };
