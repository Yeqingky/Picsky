import { useTranslation } from 'react-i18next';
import { useSearchParams } from 'react-router';
import { useSiteInfo } from '../hooks/useSite';
import { OidcProviderIcon } from './OidcProviderIcon';
import { Button } from './ui/button';

/** 登录 / 注册页的第三方登录按钮组: 无启用提供方时不渲染. */
export function OidcLoginButtons() {
  const { t } = useTranslation();
  const { data } = useSiteInfo();
  const [searchParams] = useSearchParams();
  const providers = data?.oidc_providers ?? [];

  if (providers.length === 0) return null;

  const begin = (providerId: number) => {
    const redirect = searchParams.get('redirect');
    const safeRedirect =
      redirect && redirect.startsWith('/') && !redirect.startsWith('//') ? redirect : null;
    const start = `/api/v1/auth/oidc/${providerId}/start`;
    window.location.assign(
      safeRedirect ? `${start}?redirect=${encodeURIComponent(safeRedirect)}` : start,
    );
  };

  return (
    <div className="flex flex-col gap-2">
      <p className="text-muted-foreground text-center text-xs">{t('auth.oidc.title')}</p>
      {providers.map((provider) => (
        <Button
          key={provider.id}
          type="button"
          variant="outline"
          size="lg"
          className="w-full justify-center gap-2"
          onClick={() => begin(provider.id)}
        >
          <OidcProviderIcon url={provider.icon_url} className="size-5" />
          <span className="truncate">{t('auth.oidc.loginWith', { name: provider.name })}</span>
        </Button>
      ))}
    </div>
  );
}
