import { useEffect, useRef, useState } from 'react';
import { CircleAlert } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import type { CaptchaConfig } from '../api/types';
import { useThemeMode } from '../theme/ThemeModeProvider';
import { Alert, AlertDescription } from './ui/alert';
import { Button } from './ui/button';

declare global {
  interface Window {
    turnstile?: {
      render: (
        container: HTMLElement,
        options: {
          sitekey: string;
          callback: (token: string) => void;
          'error-callback'?: () => void;
          'expired-callback'?: () => void;
          theme?: 'light' | 'dark' | 'auto';
        },
      ) => string;
      reset: (widgetId?: string) => void;
      remove: (widgetId: string) => void;
    };
  }
}

const scriptPromises: Record<string, Promise<void>> = {};

function loadScript(src: string): Promise<void> {
  const existing = scriptPromises[src];
  if (existing) return existing;

  const promise = new Promise<void>((resolve, reject) => {
    const script = document.createElement('script');
    script.src = src;
    script.async = true;
    script.defer = true;
    script.onload = () => resolve();
    script.onerror = () => reject(new Error(`脚本加载失败: ${src}`));
    document.head.append(script);
  });

  scriptPromises[src] = promise;
  // 加载失败时移除缓存, 允许重试 (否则一次瞬断会永久失效)
  promise.catch(() => {
    delete scriptPromises[src];
  });
  return promise;
}

const TURNSTILE_SCRIPT = 'https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit';
const CAP_SCRIPT = 'https://cdn.jsdelivr.net/npm/@cap.js/widget';

export interface CaptchaWidgetProps {
  config: CaptchaConfig | undefined;
  /** 人机验证票据; 校验失败后置空并重置控件 */
  onToken: (token: string) => void;
  /** 递增该值会重置控件 (例如 captcha_failed 之后) */
  resetSignal?: number;
}

/**
 * 人机验证控件 (只接入官方脚本, 不实现本地验证码).
 * - turnstile: site_key 为 Cloudflare Turnstile site key;
 * - cap: site_key 为 Cap 的 API endpoint (widget 的 data-cap-api-endpoint).
 */
export function CaptchaWidget({ config, onToken, resetSignal = 0 }: CaptchaWidgetProps) {
  const { resolvedMode } = useThemeMode();
  const containerRef = useRef<HTMLDivElement | null>(null);
  const widgetIdRef = useRef<string | null>(null);
  const capElementRef = useRef<HTMLElement | null>(null);
  const onTokenRef = useRef(onToken);
  const prevResetRef = useRef(resetSignal);
  const { t } = useTranslation();
  const [failed, setFailed] = useState(false);
  const [attempt, setAttempt] = useState(0);
  onTokenRef.current = onToken;

  const provider = config?.provider ?? 'none';
  const siteKey = config?.site_key ?? '';
  const apiEndpoint = config?.api_endpoint;

  useEffect(() => {
    if (provider !== 'turnstile' || !siteKey) return;
    let cancelled = false;

    setFailed(false);
    void loadScript(TURNSTILE_SCRIPT)
      .then(() => {
        if (cancelled || !containerRef.current || !window.turnstile) return;
        setFailed(false);
        widgetIdRef.current = window.turnstile.render(containerRef.current, {
          sitekey: siteKey,
          theme: resolvedMode,
          callback: (token) => onTokenRef.current(token),
          'error-callback': () => onTokenRef.current(''),
          'expired-callback': () => onTokenRef.current(''),
        });
      })
      .catch(() => {
        if (cancelled) return;
        setFailed(true);
        onTokenRef.current('');
      });

    return () => {
      cancelled = true;
      const id = widgetIdRef.current;
      if (id && window.turnstile) window.turnstile.remove(id);
      widgetIdRef.current = null;
    };
  }, [provider, siteKey, resolvedMode, attempt]);

  useEffect(() => {
    if (provider !== 'cap' || !siteKey) return;
    let cancelled = false;

    setFailed(false);
    void loadScript(CAP_SCRIPT)
      .then(() => {
        if (cancelled || !containerRef.current) return;
        setFailed(false);
        const element = document.createElement('cap-widget');
        element.setAttribute('data-cap-api-endpoint', apiEndpoint ?? siteKey);
        element.setAttribute('data-cap-theme', resolvedMode);
        element.addEventListener('solve', (event) => {
          const detail = (event as CustomEvent<{ token?: string }>).detail;
          onTokenRef.current(detail?.token ?? '');
        });
        element.addEventListener('error', () => onTokenRef.current(''));
        containerRef.current.replaceChildren(element);
        capElementRef.current = element;
      })
      .catch(() => {
        if (cancelled) return;
        setFailed(true);
        onTokenRef.current('');
      });

    const container = containerRef.current;
    return () => {
      cancelled = true;
      container?.replaceChildren();
      capElementRef.current = null;
    };
  }, [provider, siteKey, apiEndpoint, resolvedMode, attempt]);

  useEffect(() => {
    if (prevResetRef.current === resetSignal) return;
    prevResetRef.current = resetSignal;
    if (provider === 'turnstile' && window.turnstile && widgetIdRef.current) {
      window.turnstile.reset(widgetIdRef.current);
      return;
    }
    if (provider === 'cap') {
      const element = capElementRef.current as (HTMLElement & { reset?: () => void }) | null;
      element?.reset?.();
    }
  }, [resetSignal, provider]);

  if (provider === 'none' || !siteKey) return null;

  return (
    <div className="min-h-[65px]" data-captcha-provider={provider}>
      {failed && (
        <Alert variant="destructive" className="flex items-center justify-between gap-2">
          <CircleAlert />
          <AlertDescription>{t('common.captcha.loadFailed')}</AlertDescription>
          <Button variant="ghost" size="sm" onClick={() => setAttempt((value) => value + 1)}>
            {t('common.action.retry')}
          </Button>
        </Alert>
      )}
      <div ref={containerRef} className={failed ? 'hidden' : 'block'} />
    </div>
  );
}
