import { useEffect, useMemo, useState } from 'react';
import type { ReactNode } from 'react';
import {
  Globe,
  HardDrive,
  LoaderCircle,
  Mail,
  ShieldCheck,
  SlidersHorizontal,
  UserPlus,
  X,
} from 'lucide-react';
import { useSearchParams } from 'react-router';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import { useTranslation } from 'react-i18next';
import type { TFunction } from 'i18next';
import { adminGroupsApi, adminSettingsApi } from '../../api';
import type { SettingsPayload } from '../../api';
import { PageHeader } from '../../components/PageHeader';
import { ErrorState } from '../../components/States';
import { SizeInput } from '../../components/SizeInput';
import { FormSkeleton, SkeletonSurface } from '../../components/skeletons';
import { SITE_QUERY_KEY } from '../../hooks/useSite';
import { Alert, AlertDescription } from '../../components/ui/alert';
import { Badge } from '../../components/ui/badge';
import { Button } from '../../components/ui/button';
import { Input } from '../../components/ui/input';
import { Label } from '../../components/ui/label';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '../../components/ui/select';
import { Skeleton } from '../../components/ui/skeleton';
import { Switch } from '../../components/ui/switch';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '../../components/ui/tabs';
import { Tooltip, TooltipContent, TooltipTrigger } from '../../components/ui/tooltip';
import { cn } from '../../lib/utils';
import { formatBytes } from '../../utils/format';

/** 系统设置项 (docs/database.md 第 7 节); 全部以字符串与后端交互 */
const SETTING_KEYS = [
  'site_name',
  'site_url',
  'timezone',
  'register_enabled',
  'email_verification_enabled',
  'captcha_provider',
  'captcha_site_key',
  'captcha_endpoint',
  'captcha_secret',
  'default_group_id',
  'smtp_host',
  'smtp_port',
  'smtp_username',
  'smtp_password',
  'smtp_from',
  'smtp_encryption',
  'default_allowed_formats',
  'max_file_size_bytes',
  'default_conflict_policy',
  'default_path_template',
  'delete_object_on_last_reference',
  'record_retention_days',
  'gallery_page_size',
  'log_retention_days',
] as const;

type SettingKey = (typeof SETTING_KEYS)[number];

/** 未在 SettingsPayload 中出现的键按数据库默认值展示 (docs/database.md 第 7 节) */
const DEFAULTS: Record<SettingKey, string> = {
  site_name: 'Picsky',
  site_url: '',
  timezone: 'Asia/Shanghai',
  register_enabled: 'true',
  email_verification_enabled: 'false',
  captcha_provider: 'none',
  captcha_site_key: '',
  captcha_endpoint: '',
  captcha_secret: '',
  default_group_id: '',
  smtp_host: '',
  smtp_port: '587',
  smtp_username: '',
  smtp_password: '',
  smtp_from: '',
  smtp_encryption: 'starttls',
  default_allowed_formats: 'png,jpg,jpeg,gif,webp,bmp,avif,tiff,ico,heic',
  max_file_size_bytes: '10485760',
  default_conflict_policy: 'rename',
  default_path_template: '{md5:2}/{md5}.{ext}',
  delete_object_on_last_reference: 'true',
  record_retention_days: '30',
  gallery_page_size: '40',
  log_retention_days: '90',
};

/** 不回显的敏感项: 编辑过即视为改动 (清空输入 = 清除) */
const SECRET_KEYS: readonly SettingKey[] = ['captcha_secret', 'smtp_password'];

const TIMEZONES = [
  'Asia/Shanghai',
  'Asia/Hong_Kong',
  'Asia/Taipei',
  'Asia/Tokyo',
  'Asia/Seoul',
  'Asia/Singapore',
  'Asia/Kolkata',
  'Asia/Dubai',
  'UTC',
  'Europe/London',
  'Europe/Paris',
  'Europe/Berlin',
  'Europe/Moscow',
  'America/New_York',
  'America/Chicago',
  'America/Denver',
  'America/Los_Angeles',
  'America/Sao_Paulo',
  'Australia/Sydney',
  'Pacific/Auckland',
] as const;

const CAPTCHA_PROVIDERS = ['none', 'cap', 'turnstile'] as const;
const FILE_SIZE_UNITS = ['KB', 'MB'] as const;
const SMTP_ENCRYPTIONS = ['starttls', 'ssl', 'none'] as const;
const CONFLICT_POLICIES = ['rename', 'skip', 'overwrite', 'fail'] as const;

/** Select 不接受空字符串值: 用哨兵表示"未设置", 提交前映射回空串 */
const GROUP_UNSET = 'unset';

const TAB_ICON_CLASS = 'size-4 shrink-0';

/** 面板容器: 单列窄栏 */
const TAB_PANEL_CLASS = 'mt-6 flex max-w-3xl flex-col gap-5';

/**
 * 顶部标签页: 每个标签对应一组设置项, 布局是单列窄栏;
 * 标签为纯展示分组, 不改变设置项含义与提交行为.
 */
const SETTING_TABS = [
  { id: 'site', labelKey: 'site', icon: <Globe className={TAB_ICON_CLASS} /> },
  { id: 'register', labelKey: 'register', icon: <UserPlus className={TAB_ICON_CLASS} /> },
  { id: 'captcha', labelKey: 'captcha', icon: <ShieldCheck className={TAB_ICON_CLASS} /> },
  { id: 'email', labelKey: 'email', icon: <Mail className={TAB_ICON_CLASS} /> },
  { id: 'quota', labelKey: 'quota', icon: <HardDrive className={TAB_ICON_CLASS} /> },
  { id: 'other', labelKey: 'other', icon: <SlidersHorizontal className={TAB_ICON_CLASS} /> },
] as const;

type SettingTabId = (typeof SETTING_TABS)[number]['id'];

/** 无效或缺失的 ?tab= 回落到首个标签 */
function readTab(param: string | null): SettingTabId {
  const matched = SETTING_TABS.find((tab) => tab.id === param);
  return matched ? matched.id : 'site';
}

type Draft = Record<SettingKey, string>;
type Errors = Partial<Record<SettingKey, string>>;

function normalize(payload: SettingsPayload): Draft {
  const draft: Draft = { ...DEFAULTS };
  for (const key of SETTING_KEYS) {
    const raw = payload[key];
    if (raw === undefined || raw === null) continue;
    draft[key] = typeof raw === 'boolean' ? (raw ? 'true' : 'false') : String(raw);
  }
  return draft;
}

function isInteger(value: string): boolean {
  return /^\d+$/.test(value.trim());
}

function splitFormats(value: string): string[] {
  return value
    .split(',')
    .map((token) => token.trim())
    .filter((token) => token.length > 0);
}

function validateSettings(values: Draft, t: TFunction): Errors {
  const errors: Errors = {};
  const integer = (key: SettingKey, min: number, max?: number, message?: string) => {
    const raw = values[key].trim();
    if (!isInteger(raw)) {
      errors[key] = message ?? t('common.form.invalidNumber');
      return;
    }
    const parsed = Number(raw);
    if (parsed < min || (max !== undefined && parsed > max)) {
      errors[key] = message ?? t('common.form.invalidNumber');
    }
  };

  const siteUrl = values.site_url.trim();
  if (siteUrl.length > 0 && !/^https?:\/\//i.test(siteUrl)) {
    errors.site_url = t('common.form.invalidUrl');
  }

  if (values.default_group_id.trim().length > 0 && !isInteger(values.default_group_id)) {
    errors.default_group_id = t('adminSystem.settings.validation.groupId');
  }

  integer('smtp_port', 1, 65535, t('adminSystem.settings.validation.port'));

  integer('max_file_size_bytes', 1, undefined, t('adminSystem.settings.validation.positive'));
  integer('record_retention_days', 0, undefined, t('adminSystem.settings.validation.nonNegative'));
  integer('gallery_page_size', 1, undefined, t('adminSystem.settings.validation.positive'));
  integer('log_retention_days', 0, undefined, t('adminSystem.settings.validation.nonNegative'));

  const formats = splitFormats(values.default_allowed_formats);
  if (formats.length === 0) {
    errors.default_allowed_formats = t('adminSystem.settings.validation.formatsRequired');
  } else if (formats.some((token) => token.toLowerCase() === 'svg')) {
    errors.default_allowed_formats = t('adminSystem.settings.validation.formatsSvg');
  } else if (formats.some((token) => !/^[A-Za-z0-9]+$/.test(token))) {
    errors.default_allowed_formats = t('adminSystem.settings.validation.formatsToken');
  }

  const pathTemplate = values.default_path_template.trim();
  if (!/\{md5|\{sha256|\{sha512/.test(pathTemplate)) {
    errors.default_path_template = t('adminSystem.settings.validation.pathHash');
  }

  return errors;
}

/** 表单字段: 标签 + 控件 + 帮助/错误文案 */
function Field({
  id,
  label,
  hint,
  error,
  children,
}: {
  id: string;
  label: string;
  hint?: ReactNode;
  error?: string;
  children: ReactNode;
}) {
  const message = error ?? hint;
  return (
    <div className="flex flex-col gap-1.5">
      <Label htmlFor={id}>{label}</Label>
      {children}
      {message !== undefined && (
        <p className={cn('text-xs', error ? 'text-destructive' : 'text-muted-foreground')}>
          {message}
        </p>
      )}
    </div>
  );
}

/** 布尔开关字段: 开关 + 标签 + 说明 */
function SwitchField({
  id,
  label,
  hint,
  checked,
  onChange,
}: {
  id: string;
  label: string;
  hint: string;
  checked: boolean;
  onChange: (next: boolean) => void;
}) {
  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex items-center gap-2">
        <Switch id={id} checked={checked} onCheckedChange={onChange} />
        <Label htmlFor={id} className="font-normal">
          {label}
        </Label>
      </div>
      <p className="text-muted-foreground pl-10 text-xs">{hint}</p>
    </div>
  );
}

/** 不回显的敏感项: 密码输入 + 清除按钮 + 是否已设置状态 */
function SecretField({
  id,
  label,
  hint,
  value,
  isSet,
  onChange,
  onClear,
}: {
  id: string;
  label: string;
  hint: string;
  value: string;
  isSet: boolean;
  onChange: (next: string) => void;
  onClear: () => void;
}) {
  const { t } = useTranslation();
  const clearLabel = t('adminSystem.settings.clearSecret');
  return (
    <div className="flex flex-col gap-1.5">
      <Label htmlFor={id}>{label}</Label>
      <div className="flex items-center gap-1.5">
        <Input
          id={id}
          type="password"
          autoComplete="new-password"
          value={value}
          onChange={(event) => onChange(event.target.value)}
          className="min-w-0 flex-1"
        />
        <Tooltip>
          <TooltipTrigger asChild>
            <Button
              type="button"
              variant="ghost"
              size="icon-sm"
              aria-label={clearLabel}
              onClick={onClear}
              disabled={!isSet}
            >
              <X />
            </Button>
          </TooltipTrigger>
          <TooltipContent>{clearLabel}</TooltipContent>
        </Tooltip>
      </div>
      <p className="text-muted-foreground text-xs">{hint}</p>
      <Badge
        variant="outline"
        className={cn('w-fit', isSet && 'border-transparent bg-success/10 text-success')}
      >
        {isSet ? t('adminSystem.settings.secretSet') : t('adminSystem.settings.secretUnset')}
      </Badge>
    </div>
  );
}

/** 首屏骨架: 标签栏与表单行, 与真实内容同尺寸 */
function SettingsSkeleton() {
  const { t } = useTranslation();
  return (
    <SkeletonSurface label={t('common.skeleton.settings')}>
      <div className="border-border flex flex-wrap gap-2 border-b pb-[5px]">
        {SETTING_TABS.map((tab) => (
          <Skeleton key={tab.id} className="h-9 w-24 rounded-sm" />
        ))}
      </div>
      <div className="mt-6 max-w-3xl">
        <FormSkeleton fields={6} />
      </div>
    </SkeletonSurface>
  );
}

export default function AdminSettingsPage() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();

  // 标签写入 ?tab=: 刷新与深链保持当前分组; 默认标签不落 URL
  const [searchParams, setSearchParams] = useSearchParams();
  const tab = readTab(searchParams.get('tab'));

  const handleTabChange = (value: string) => {
    const next = new URLSearchParams(searchParams);
    if (value === 'site') next.delete('tab');
    else next.set('tab', value);
    setSearchParams(next, { replace: true });
  };

  const settingsQuery = useQuery({
    queryKey: ['admin', 'settings'],
    queryFn: adminSettingsApi.get,
    staleTime: Infinity,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    retry: false,
  });

  const groupsQuery = useQuery({
    queryKey: ['admin', 'groups'],
    queryFn: adminGroupsApi.list,
    staleTime: Infinity,
    refetchOnWindowFocus: false,
    retry: false,
  });

  const [draft, setDraft] = useState<Draft | null>(null);
  const [secretTouched, setSecretTouched] = useState<Partial<Record<SettingKey, boolean>>>({});
  const [errors, setErrors] = useState<Errors>({});

  const serverData = settingsQuery.data;

  useEffect(() => {
    if (!serverData) return;
    setDraft(normalize(serverData));
    setSecretTouched({});
    setErrors({});
  }, [serverData]);

  const baseline = useMemo<Draft | null>(
    () => (serverData ? normalize(serverData) : null),
    [serverData],
  );

  const changedKeys = useMemo<SettingKey[]>(() => {
    if (!draft || !baseline) return [];
    return SETTING_KEYS.filter(
      (key) =>
        draft[key] !== baseline[key] || (SECRET_KEYS.includes(key) && secretTouched[key] === true),
    );
  }, [draft, baseline, secretTouched]);

  const saveMutation = useMutation({
    mutationFn: (patch: Record<string, string>) => adminSettingsApi.update(patch),
    onSuccess: async (_result, patch) => {
      toast.success(t('adminSystem.settings.saved'));
      // 站点名称属于公开信息: 保存后让侧边栏品牌区立即刷新
      void queryClient.invalidateQueries({ queryKey: SITE_QUERY_KEY });
      const refreshed = await settingsQuery.refetch();
      if (!refreshed.data) {
        // 重新拉取失败时以提交值更新本地基线, 避免"已保存"仍显示为未保存
        setDraft((prev) => (prev ? { ...prev, ...patch } : prev));
        setSecretTouched({});
      }
    },
  });

  const setField = (key: SettingKey, value: string) => {
    setDraft((prev) => (prev ? { ...prev, [key]: value } : prev));
    setErrors((prev) => {
      if (!prev[key]) return prev;
      const next = { ...prev };
      delete next[key];
      return next;
    });
  };

  const setSecretField = (key: SettingKey, value: string) => {
    setField(key, value);
    setSecretTouched((prev) => (prev[key] ? prev : { ...prev, [key]: true }));
  };

  const secretIsSet = (key: SettingKey): boolean => {
    if (secretTouched[key] === true && draft) return draft[key].length > 0;
    return serverData?.[`${key}_set`] === true;
  };

  const handleReset = () => {
    if (!baseline) return;
    setDraft({ ...baseline });
    setSecretTouched({});
    setErrors({});
    toast.info(t('adminSystem.settings.resetDone'));
  };

  const handleSave = () => {
    if (!draft) return;
    const nextErrors = validateSettings(draft, t);
    setErrors(nextErrors);
    if (Object.keys(nextErrors).length > 0) return;
    if (changedKeys.length === 0) return;

    const patch: Record<string, string> = {};
    for (const key of changedKeys) patch[key] = draft[key];
    saveMutation.mutate(patch);
  };

  const dirty = changedKeys.length > 0;
  const saving = saveMutation.isPending;

  const saveActions = (
    <>
      {dirty && (
        <span className="text-muted-foreground text-sm" aria-live="polite">
          {t('adminSystem.settings.unsaved', { count: changedKeys.length })}
        </span>
      )}
      <Button variant="outline" onClick={handleReset} disabled={!dirty || saving}>
        {t('common.action.reset')}
      </Button>
      <Button variant="default" onClick={handleSave} disabled={!dirty || saving}>
        {saving && <LoaderCircle className="size-4 animate-spin" />}
        {saving ? t('common.action.saving') : t('common.action.save')}
      </Button>
    </>
  );

  const header = (
    <PageHeader
      title={t('adminSystem.settings.title')}
      description={t('adminSystem.settings.description')}
      actions={draft ? saveActions : undefined}
    />
  );

  if (settingsQuery.isError) {
    return (
      <>
        {header}
        <ErrorState
          message={settingsQuery.error instanceof Error ? settingsQuery.error.message : undefined}
          onRetry={() => {
            void settingsQuery.refetch();
          }}
        />
      </>
    );
  }

  if (!draft || !baseline) {
    return (
      <>
        {header}
        <SettingsSkeleton />
      </>
    );
  }

  const zones: string[] =
    draft.timezone.length > 0 && !TIMEZONES.includes(draft.timezone as (typeof TIMEZONES)[number])
      ? [draft.timezone, ...TIMEZONES]
      : [...TIMEZONES];
  const groups = groupsQuery.data ?? [];
  const formatTokens = splitFormats(draft.default_allowed_formats);
  const maxFileSize = isInteger(draft.max_file_size_bytes)
    ? Number(draft.max_file_size_bytes)
    : null;
  const defaultGroupUnknown =
    !groupsQuery.isPending &&
    draft.default_group_id.length > 0 &&
    !groups.some((group) => String(group.id) === draft.default_group_id);

  return (
    <>
      {header}

      {saveMutation.isError && (
        <Alert variant="destructive" className="mb-4">
          <AlertDescription>
            {saveMutation.error instanceof Error
              ? saveMutation.error.message
              : t('common.state.failed')}
          </AlertDescription>
        </Alert>
      )}
      {Object.keys(errors).length > 0 && (
        <Alert className="border-warning/30 bg-warning/10 mb-4">
          <AlertDescription className="text-warning">
            {t('adminSystem.settings.invalidForm')}
          </AlertDescription>
        </Alert>
      )}

      <Tabs value={tab} onValueChange={handleTabChange} className="flex-col gap-0">
        <TabsList
          variant="line"
          className="group-data-horizontal/tabs:h-auto border-border w-full flex-wrap justify-start rounded-none border-b p-0 pb-[5px]"
        >
          {SETTING_TABS.map((item) => (
            <TabsTrigger key={item.id} value={item.id} className="h-9 flex-none gap-2 px-3">
              {item.icon}
              {t(`adminSystem.settings.group.${item.labelKey}`)}
            </TabsTrigger>
          ))}
        </TabsList>

        <TabsContent value="site" className={TAB_PANEL_CLASS}>
          <Field id="setting-site-name" label={t('adminSystem.settings.field.siteName')}>
            <Input
              id="setting-site-name"
              value={draft.site_name}
              onChange={(event) => setField('site_name', event.target.value)}
            />
          </Field>
          <Field
            id="setting-site-url"
            label={t('adminSystem.settings.field.siteUrl')}
            hint={t('adminSystem.settings.field.siteUrlHint')}
            error={errors.site_url}
          >
            <Input
              id="setting-site-url"
              value={draft.site_url}
              placeholder="https://picsky.example.com"
              aria-invalid={Boolean(errors.site_url)}
              onChange={(event) => setField('site_url', event.target.value)}
            />
          </Field>
          <Field
            id="setting-timezone"
            label={t('adminSystem.settings.field.timezone')}
            hint={t('adminSystem.settings.field.timezoneHint')}
          >
            <Select value={draft.timezone} onValueChange={(value) => setField('timezone', value)}>
              <SelectTrigger id="setting-timezone" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {zones.map((zone) => (
                  <SelectItem key={zone} value={zone}>
                    {zone}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
        </TabsContent>

        <TabsContent value="register" className={TAB_PANEL_CLASS}>
          <SwitchField
            id="setting-register-enabled"
            label={t('adminSystem.settings.field.registerEnabled')}
            hint={t('adminSystem.settings.field.registerEnabledHint')}
            checked={draft.register_enabled === 'true'}
            onChange={(next) => setField('register_enabled', next ? 'true' : 'false')}
          />
          <SwitchField
            id="setting-email-verification"
            label={t('adminSystem.settings.field.emailVerificationEnabled')}
            hint={t('adminSystem.settings.field.emailVerificationHint')}
            checked={draft.email_verification_enabled === 'true'}
            onChange={(next) => setField('email_verification_enabled', next ? 'true' : 'false')}
          />
          {groupsQuery.isError ? (
            <Field
              id="setting-default-group"
              label={t('adminSystem.settings.field.defaultGroup')}
              hint={
                groupsQuery.error instanceof Error
                  ? groupsQuery.error.message
                  : t('adminSystem.settings.groupsLoadFailed')
              }
              error={errors.default_group_id}
            >
              <Input
                id="setting-default-group"
                value={draft.default_group_id}
                aria-invalid={Boolean(errors.default_group_id)}
                onChange={(event) => setField('default_group_id', event.target.value)}
              />
            </Field>
          ) : (
            <Field
              id="setting-default-group"
              label={t('adminSystem.settings.field.defaultGroup')}
              hint={t('adminSystem.settings.field.defaultGroupHint')}
              error={errors.default_group_id}
            >
              <Select
                value={draft.default_group_id.length > 0 ? draft.default_group_id : GROUP_UNSET}
                onValueChange={(value) =>
                  setField('default_group_id', value === GROUP_UNSET ? '' : value)
                }
              >
                <SelectTrigger
                  id="setting-default-group"
                  className="w-full"
                  aria-invalid={Boolean(errors.default_group_id)}
                >
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value={GROUP_UNSET}>{t('common.quota.unset')}</SelectItem>
                  {groups.map((group) => (
                    <SelectItem key={group.id} value={String(group.id)} disabled={group.is_system}>
                      {group.name}
                    </SelectItem>
                  ))}
                  {defaultGroupUnknown && (
                    <SelectItem value={draft.default_group_id}>
                      {`#${draft.default_group_id}`}
                    </SelectItem>
                  )}
                </SelectContent>
              </Select>
            </Field>
          )}
        </TabsContent>

        <TabsContent value="captcha" className={TAB_PANEL_CLASS}>
          <Field
            id="setting-captcha-provider"
            label={t('adminSystem.settings.field.captchaProvider')}
          >
            <Select
              value={draft.captcha_provider}
              onValueChange={(value) => setField('captcha_provider', value)}
            >
              <SelectTrigger id="setting-captcha-provider" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {CAPTCHA_PROVIDERS.map((provider) => (
                  <SelectItem key={provider} value={provider}>
                    {t(`adminSystem.options.captcha.${provider}`)}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
          <Field
            id="setting-captcha-site-key"
            label={t('adminSystem.settings.field.captchaSiteKey')}
            hint={t('adminSystem.settings.field.captchaSiteKeyHint')}
          >
            <Input
              id="setting-captcha-site-key"
              value={draft.captcha_site_key}
              onChange={(event) => setField('captcha_site_key', event.target.value)}
            />
          </Field>
          <Field
            id="setting-captcha-endpoint"
            label={t('adminSystem.settings.field.captchaEndpoint')}
            hint={t('adminSystem.settings.field.captchaEndpointHint')}
          >
            <Input
              id="setting-captcha-endpoint"
              value={draft.captcha_endpoint}
              onChange={(event) => setField('captcha_endpoint', event.target.value)}
            />
          </Field>
          <SecretField
            id="setting-captcha-secret"
            label={t('adminSystem.settings.field.captchaSecret')}
            hint={t('adminSystem.settings.field.captchaSecretHint')}
            value={draft.captcha_secret}
            isSet={secretIsSet('captcha_secret')}
            onChange={(next) => setSecretField('captcha_secret', next)}
            onClear={() => setSecretField('captcha_secret', '')}
          />
        </TabsContent>

        <TabsContent value="email" className={TAB_PANEL_CLASS}>
          <Field id="setting-smtp-host" label={t('adminSystem.settings.field.smtpHost')}>
            <Input
              id="setting-smtp-host"
              value={draft.smtp_host}
              onChange={(event) => setField('smtp_host', event.target.value)}
            />
          </Field>
          <Field
            id="setting-smtp-port"
            label={t('adminSystem.settings.field.smtpPort')}
            error={errors.smtp_port}
          >
            <Input
              id="setting-smtp-port"
              value={draft.smtp_port}
              aria-invalid={Boolean(errors.smtp_port)}
              onChange={(event) => setField('smtp_port', event.target.value)}
            />
          </Field>
          <Field id="setting-smtp-username" label={t('adminSystem.settings.field.smtpUsername')}>
            <Input
              id="setting-smtp-username"
              value={draft.smtp_username}
              onChange={(event) => setField('smtp_username', event.target.value)}
            />
          </Field>
          <SecretField
            id="setting-smtp-password"
            label={t('adminSystem.settings.field.smtpPassword')}
            hint={t('adminSystem.settings.field.smtpPasswordHint')}
            value={draft.smtp_password}
            isSet={secretIsSet('smtp_password')}
            onChange={(next) => setSecretField('smtp_password', next)}
            onClear={() => setSecretField('smtp_password', '')}
          />
          <Field
            id="setting-smtp-from"
            label={t('adminSystem.settings.field.smtpFrom')}
            hint={t('adminSystem.settings.field.smtpFromHint')}
          >
            <Input
              id="setting-smtp-from"
              value={draft.smtp_from}
              onChange={(event) => setField('smtp_from', event.target.value)}
            />
          </Field>
          <Field
            id="setting-smtp-encryption"
            label={t('adminSystem.settings.field.smtpEncryption')}
          >
            <Select
              value={draft.smtp_encryption}
              onValueChange={(value) => setField('smtp_encryption', value)}
            >
              <SelectTrigger id="setting-smtp-encryption" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {SMTP_ENCRYPTIONS.map((encryption) => (
                  <SelectItem key={encryption} value={encryption}>
                    {t(`adminSystem.options.encryption.${encryption}`)}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
        </TabsContent>

        <TabsContent value="quota" className={TAB_PANEL_CLASS}>
          <Field
            id="setting-allowed-formats"
            label={t('adminSystem.settings.field.allowedFormats')}
            hint={
              formatTokens.length > 0
                ? t('adminSystem.settings.field.allowedFormatsPreview', {
                    count: formatTokens.length,
                    formats: formatTokens.join(', '),
                  })
                : t('adminSystem.settings.field.allowedFormatsHint')
            }
            error={errors.default_allowed_formats}
          >
            <Input
              id="setting-allowed-formats"
              value={draft.default_allowed_formats}
              aria-invalid={Boolean(errors.default_allowed_formats)}
              onChange={(event) => setField('default_allowed_formats', event.target.value)}
            />
          </Field>
          <Field
            id="setting-max-file-size"
            label={t('adminSystem.settings.field.maxFileSize')}
            hint={
              maxFileSize !== null
                ? t('adminSystem.settings.field.maxFileSizeHint', {
                    size: formatBytes(maxFileSize),
                  })
                : undefined
            }
            error={errors.max_file_size_bytes}
          >
            <SizeInput
              id="setting-max-file-size"
              value={maxFileSize}
              units={FILE_SIZE_UNITS}
              defaultUnit="MB"
              invalid={Boolean(errors.max_file_size_bytes)}
              onChange={(bytes) =>
                setField('max_file_size_bytes', bytes === null ? '' : String(bytes))
              }
            />
          </Field>
        </TabsContent>

        <TabsContent value="other" className={TAB_PANEL_CLASS}>
          <Field
            id="setting-conflict-policy"
            label={t('adminSystem.settings.field.conflictPolicy')}
          >
            <Select
              value={draft.default_conflict_policy}
              onValueChange={(value) => setField('default_conflict_policy', value)}
            >
              <SelectTrigger id="setting-conflict-policy" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {CONFLICT_POLICIES.map((policy) => (
                  <SelectItem key={policy} value={policy}>
                    {t(`adminSystem.options.conflict.${policy}`)}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
          <Field
            id="setting-path-template"
            label={t('adminSystem.settings.field.pathTemplate')}
            hint={t('adminSystem.settings.field.pathTemplateHint')}
            error={errors.default_path_template}
          >
            <Input
              id="setting-path-template"
              value={draft.default_path_template}
              aria-invalid={Boolean(errors.default_path_template)}
              onChange={(event) => setField('default_path_template', event.target.value)}
            />
          </Field>
          <SwitchField
            id="setting-delete-object"
            label={t('adminSystem.settings.field.deleteObject')}
            hint={t('adminSystem.settings.field.deleteObjectHint')}
            checked={draft.delete_object_on_last_reference === 'true'}
            onChange={(next) =>
              setField('delete_object_on_last_reference', next ? 'true' : 'false')
            }
          />
          <Field
            id="setting-record-retention"
            label={t('adminSystem.settings.field.recordRetention')}
            hint={t('adminSystem.settings.field.recordRetentionHint')}
            error={errors.record_retention_days}
          >
            <Input
              id="setting-record-retention"
              value={draft.record_retention_days}
              aria-invalid={Boolean(errors.record_retention_days)}
              onChange={(event) => setField('record_retention_days', event.target.value)}
            />
          </Field>
          <Field
            id="setting-gallery-page-size"
            label={t('adminSystem.settings.field.galleryPageSize')}
            error={errors.gallery_page_size}
          >
            <Input
              id="setting-gallery-page-size"
              value={draft.gallery_page_size}
              aria-invalid={Boolean(errors.gallery_page_size)}
              onChange={(event) => setField('gallery_page_size', event.target.value)}
            />
          </Field>
          <Field
            id="setting-log-retention"
            label={t('adminSystem.settings.field.logRetention')}
            error={errors.log_retention_days}
          >
            <Input
              id="setting-log-retention"
              value={draft.log_retention_days}
              aria-invalid={Boolean(errors.log_retention_days)}
              onChange={(event) => setField('log_retention_days', event.target.value)}
            />
          </Field>
        </TabsContent>
      </Tabs>
    </>
  );
}
