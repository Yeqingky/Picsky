import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { FormEvent, ReactNode } from 'react';
import { useNavigate, useParams, useSearchParams } from 'react-router';
import {
  ArrowDown,
  ArrowLeft,
  ArrowUp,
  Eye,
  EyeOff,
  FolderTree,
  Globe,
  Info,
  Play,
  Plus,
  Save,
  ScanSearch,
  Server,
  Shuffle,
  Trash2,
} from 'lucide-react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import { useTranslation } from 'react-i18next';
import { adminPoliciesApi, adminSettingsApi } from '../../api';
import type {
  AdminPolicy,
  AdminPolicyWriteRequest,
  ConflictPolicy,
  PolicyScanRequest,
} from '../../api/types';
import { CopyButton } from '../../components/CopyButton';
import { PageHeader } from '../../components/PageHeader';
import { ErrorState } from '../../components/States';
import { FormSkeleton, TextLineSkeleton, TextLinesSkeleton } from '../../components/skeletons';
import { Alert, AlertDescription } from '../../components/ui/alert';
import { Badge } from '../../components/ui/badge';
import { Button } from '../../components/ui/button';
import { Input } from '../../components/ui/input';
import { Label } from '../../components/ui/label';
import { RadioGroup, RadioGroupItem } from '../../components/ui/radio-group';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '../../components/ui/select';
import { Separator } from '../../components/ui/separator';
import { Switch } from '../../components/ui/switch';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '../../components/ui/tabs';
import { Tooltip, TooltipContent, TooltipTrigger } from '../../components/ui/tooltip';
import { useDebouncedValue } from '../../hooks/useDebouncedValue';
import { useDelayedLoading } from '../../hooks/useDelayedLoading';
import { cn } from '../../lib/utils';
import { formatDuration } from '../../utils/format';

/**
 * 可选后端类型: v1 只实现 webdav (docs/storage-policy.md 第 3 节其余类型为后续阶段),
 * 未注册的类型提交会被后端以 400 拒绝, 因此这里只列出可用驱动。
 */
const TYPE_OPTIONS = ['webdav'] as const;

const CONFLICT_OPTIONS: readonly ConflictPolicy[] = ['rename', 'skip', 'overwrite', 'fail'];

/** 新建时的能力表: 编辑模式以服务端返回的 capabilities 为准 */
const NEW_TYPE_CAPABILITIES: Record<string, string[]> = {
  webdav: ['list', 'read', 'delete', 'overwrite', 'stat', 'mkdir'],
};
const DEFAULT_NEW_CAPABILITIES = NEW_TYPE_CAPABILITIES.webdav;

const TAB_ICON_CLASS = 'size-4 shrink-0';

/** 面板容器: 与系统设置页一致, 字段单列且整体限宽 */
const TAB_PANEL_CLASS = 'mt-6 flex max-w-3xl flex-col gap-5';

/**
 * 顶部标签页: 每个标签对应一组策略配置;
 * 对象键与域名 URL 对照表是保存结果, 不占标签, 固定在标签下方.
 */
const POLICY_TABS = [
  { id: 'basic', labelKey: 'basic', icon: <Info className={TAB_ICON_CLASS} /> },
  { id: 'connection', labelKey: 'connection', icon: <Server className={TAB_ICON_CLASS} /> },
  { id: 'path', labelKey: 'path', icon: <FolderTree className={TAB_ICON_CLASS} /> },
  { id: 'urls', labelKey: 'urls', icon: <Globe className={TAB_ICON_CLASS} /> },
  { id: 'conflict', labelKey: 'conflict', icon: <Shuffle className={TAB_ICON_CLASS} /> },
  {
    id: 'scan',
    labelKey: 'scan',
    icon: <ScanSearch className={TAB_ICON_CLASS} />,
    editOnly: true,
  },
] as const;

type PolicyTabId = (typeof POLICY_TABS)[number]['id'];

/** 魔法路径变量快捷按钮 (docs/storage-policy.md 第 4 节) */
const TEMPLATE_VARIABLES = [
  '{md5}',
  '{md5:8}',
  '{md5:2}',
  '{sha256}',
  '{sha256:16}',
  '{sha512}',
  '{ext}',
  '{uuid}',
  '{rand}',
  '{rand:8}',
] as const;

const HASH_VARIABLE_RE = /\{(?:md5|sha256|sha512)(?::\d+)?\}/;
const URL_RE = /^https?:\/\//i;
const INTEGER_RE = /^-?\d+$/;

const DEFAULT_PATH_TEMPLATE = '{md5:2}/{md5}.{ext}';
const DEFAULT_CONFLICT_POLICY: ConflictPolicy = 'rename';

const PREVIEW_MD5 = '0f1e2d3c4b5a69788796a5b4c3d2e1f0';
const PREVIEW_SHA256 = `${PREVIEW_MD5}0f1e2d3c4b5a69788796a5b4c3d2e1f0`;
const PREVIEW_SHA512 = PREVIEW_MD5.repeat(4);

interface UrlRow {
  key: string;
  alias: string;
  url: string;
  is_default: boolean;
}

interface FormState {
  name: string;
  type: string;
  enabled: boolean;
  sort: string;
  endpoint: string;
  username: string;
  password: string;
  pathTemplate: string;
  conflictPolicy: ConflictPolicy;
  urls: UrlRow[];
}

let urlKeySeed = 0;

function nextUrlKey(): string {
  urlKeySeed += 1;
  return `url-${urlKeySeed}`;
}

function formStateFromPolicy(policy: AdminPolicy): FormState {
  return {
    name: policy.name,
    type: policy.type,
    enabled: policy.enabled,
    sort: String(policy.sort),
    endpoint: typeof policy.config.endpoint === 'string' ? policy.config.endpoint : '',
    username: policy.secrets?.username ?? '',
    password: policy.secrets?.password ?? '',
    pathTemplate: policy.path_template,
    conflictPolicy: policy.conflict_policy,
    urls: policy.urls.map((item) => ({
      key: nextUrlKey(),
      alias: item.alias,
      url: item.url,
      is_default: item.is_default,
    })),
  };
}

/** 后端错误 → 可展示文本 (ApiError 由 axios 拦截器转换) */
function errorText(error: unknown, fallback: string): string {
  if (error instanceof Error && error.message !== '') return error.message;
  return fallback;
}

/** 面板内分组: 说明与右侧操作 + 表单内容 (无卡片边框, 与设置页的标签面板一致) */
function PolicySection({
  title,
  hint,
  actions,
  className,
  children,
}: {
  title?: string;
  hint?: string;
  actions?: ReactNode;
  className?: string;
  children: ReactNode;
}) {
  const hasHeader = title !== undefined || hint !== undefined || actions !== undefined;
  return (
    <section className={cn('flex flex-col gap-5', className)}>
      {hasHeader && (
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="min-w-0">
            {title !== undefined && (
              <h2 className="font-heading text-base font-semibold">{title}</h2>
            )}
            {hint !== undefined && (
              <p className={cn('text-muted-foreground text-sm', title !== undefined && 'mt-1')}>
                {hint}
              </p>
            )}
          </div>
          {actions !== undefined && (
            <div className="flex shrink-0 flex-wrap items-center gap-2">{actions}</div>
          )}
        </div>
      )}
      {children}
    </section>
  );
}

/** 带标签的表单字段: 错误优先, 否则展示提示文案 */
function FormField({
  id,
  label,
  error,
  hint,
  className,
  children,
}: {
  id: string;
  label: string;
  error?: string;
  hint?: string;
  className?: string;
  children: ReactNode;
}) {
  return (
    <div className={cn('flex min-w-0 flex-col gap-2', className)}>
      <Label htmlFor={id}>{label}</Label>
      {children}
      {error !== undefined ? (
        <p className="text-destructive text-sm">{error}</p>
      ) : hint !== undefined ? (
        <p className="text-muted-foreground text-sm">{hint}</p>
      ) : null}
    </div>
  );
}

interface UrlReferenceItem {
  key: string;
  alias: string;
  url: string;
  is_default: boolean;
}

/** 相对路径与对象键 (预览区与对照表共用) */
function PathReferenceRows({ relPath, objectKey }: { relPath: string; objectKey: string }) {
  const { t } = useTranslation();
  const rows = [
    { label: t('adminPolicies.edit.reference.relPath'), value: relPath },
    { label: t('adminPolicies.edit.reference.objectKey'), value: objectKey },
  ];
  return (
    <div className="flex flex-col gap-2">
      {rows.map((row) => (
        <div key={row.label} className="flex items-center gap-2">
          <p className="text-muted-foreground w-[150px] shrink-0 text-sm">{row.label}</p>
          <p className="font-mono text-sm break-all">{row.value}</p>
          <CopyButton value={row.value} />
        </div>
      ))}
    </div>
  );
}

/** 各别名域名与 rel_path 拼接出的完整直链 */
function UrlReferenceList({
  title,
  items,
  emptyText,
}: {
  title: string;
  items: readonly UrlReferenceItem[];
  emptyText: string;
}) {
  return (
    <div className="flex flex-col gap-2">
      <p className="text-muted-foreground text-sm">{title}</p>
      {items.length === 0 ? (
        <p className="text-muted-foreground text-sm">{emptyText}</p>
      ) : (
        <div className="flex flex-col gap-3">
          {items.map((item) => (
            <div key={item.key} className="flex items-center gap-2">
              <Badge variant={item.is_default ? 'default' : 'outline'} className="shrink-0">
                {item.alias}
              </Badge>
              <p className="min-w-0 flex-1 truncate font-mono text-xs">{item.url}</p>
              <CopyButton value={item.url} />
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

export default function AdminPolicyEditPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const { id } = useParams<{ id: string }>();

  const rawId = id ?? '';
  const isNew = rawId === 'new' || rawId === '';
  const numericId = Number(rawId);
  const isEdit = !isNew && Number.isInteger(numericId) && numericId > 0;
  const invalidId = !isNew && !isEdit;

  const [form, setForm] = useState<FormState | null>(null);
  const [submitted, setSubmitted] = useState(false);
  const [urlTouched, setUrlTouched] = useState(false);
  const [savedOnce, setSavedOnce] = useState(false);
  const [scanPath, setScanPath] = useState('');
  const [dryRun, setDryRun] = useState(false);
  const [showPassword, setShowPassword] = useState(false);

  // 标签写入 ?tab=: 刷新与深链保持当前分组; 默认标签不落 URL, 扫描补录仅编辑模式可见
  const [searchParams, setSearchParams] = useSearchParams();
  const visibleTabs = isEdit ? POLICY_TABS : POLICY_TABS.filter((item) => item.id !== 'scan');
  const requestedTab = searchParams.get('tab');
  const tab: PolicyTabId = visibleTabs.find((item) => item.id === requestedTab)?.id ?? 'basic';

  const handleTabChange = (value: string) => {
    const next = new URLSearchParams(searchParams);
    if (value === 'basic') next.delete('tab');
    else next.set('tab', value);
    setSearchParams(next, { replace: true });
  };

  const pathInputRef = useRef<HTMLInputElement | null>(null);
  const syncedPolicyRef = useRef<number | null>(null);
  const presetAppliedRef = useRef(false);

  const policyQuery = useQuery({
    queryKey: ['admin', 'policy', numericId],
    queryFn: () => adminPoliciesApi.get(numericId),
    enabled: isEdit,
  });

  const settingsQuery = useQuery({
    queryKey: ['admin', 'settings', 'policy-presets'],
    queryFn: () => adminSettingsApi.get(),
    enabled: isNew,
    retry: false,
  });

  /* ---------------------------------------------------------------- 初始化 */

  useEffect(() => {
    const policy = policyQuery.data;
    if (!policy || syncedPolicyRef.current === policy.id) return;
    syncedPolicyRef.current = policy.id;
    setForm(formStateFromPolicy(policy));
    setSavedOnce(true);
  }, [policyQuery.data]);

  useEffect(() => {
    if (!isNew || presetAppliedRef.current || settingsQuery.isPending) return;
    presetAppliedRef.current = true;
    const payload = settingsQuery.data;
    const rawTemplate = payload?.default_path_template;
    const rawConflict = payload?.default_conflict_policy;
    setForm({
      name: '',
      type: TYPE_OPTIONS[0],
      enabled: true,
      sort: '0',
      endpoint: '',
      username: '',
      password: '',
      pathTemplate:
        typeof rawTemplate === 'string' && rawTemplate.trim() !== ''
          ? rawTemplate
          : DEFAULT_PATH_TEMPLATE,
      conflictPolicy:
        CONFLICT_OPTIONS.find((option) => option === rawConflict) ?? DEFAULT_CONFLICT_POLICY,
      urls: [{ key: nextUrlKey(), alias: '', url: '', is_default: true }],
    });
  }, [isNew, settingsQuery.data, settingsQuery.isPending]);

  /* ------------------------------------------------------------ 实时预览 */

  const {
    mutate: runPreview,
    data: preview,
    error: previewError,
    isPending: previewPending,
    reset: resetPreview,
  } = useMutation({ mutationFn: adminPoliciesApi.preview });

  const debouncedTemplate = useDebouncedValue(form?.pathTemplate ?? '', 400);
  const previewSkeleton = useDelayedLoading(previewPending);

  useEffect(() => {
    const template = debouncedTemplate.trim();
    if (template === '' || !HASH_VARIABLE_RE.test(template)) {
      // 模板非法时清空上一次结果, 避免对照表展示过期路径
      resetPreview();
      return;
    }
    runPreview({
      path_template: template,
      md5: PREVIEW_MD5,
      sha256: PREVIEW_SHA256,
      sha512: PREVIEW_SHA512,
      ext: 'png',
    });
  }, [debouncedTemplate, runPreview, resetPreview]);

  /* ---------------------------------------------------------------- 校验 */

  const errors = useMemo(() => {
    const next: Record<string, string> = {};
    if (form === null) return next;

    const template = form.pathTemplate.trim();
    if (template === '') next.pathTemplate = t('adminPolicies.edit.error.templateRequired');
    else if (!HASH_VARIABLE_RE.test(template))
      next.pathTemplate = t('adminPolicies.edit.error.templateHash');
    else if (template.startsWith('/') || template.includes('//') || template.includes('..')) {
      next.pathTemplate = t('adminPolicies.edit.error.templateInvalid');
    }

    if (form.name.trim() === '') next.name = t('adminPolicies.edit.error.nameRequired');
    if (form.endpoint.trim() === '') next.endpoint = t('adminPolicies.edit.error.endpointRequired');
    if (form.sort.trim() !== '' && !INTEGER_RE.test(form.sort.trim())) {
      next.sort = t('adminPolicies.edit.error.sortInvalid');
    }

    form.urls.forEach((row, index) => {
      if (row.alias.trim() === '')
        next[`urls.${index}.alias`] = t('adminPolicies.edit.error.urlAliasRequired');
      if (row.url.trim() === '')
        next[`urls.${index}.url`] = t('adminPolicies.edit.error.urlRequired');
      else if (!URL_RE.test(row.url.trim()))
        next[`urls.${index}.url`] = t('adminPolicies.edit.error.urlInvalid');
    });

    if (form.urls.length === 0) next.urls = t('adminPolicies.edit.error.urlsRequired');
    else if (form.urls.filter((row) => row.is_default).length !== 1) {
      next.urls = t('adminPolicies.edit.error.urlsDefaultRequired');
    }

    return next;
  }, [form, t]);

  const errorFor = (key: string) => {
    const message = errors[key];
    if (message === undefined) return undefined;
    if (key === 'pathTemplate') return message;
    if (key.startsWith('urls.') && urlTouched) return message;
    return submitted ? message : undefined;
  };

  /* ---------------------------------------------------------------- 保存 */

  const saveMutation = useMutation({
    mutationFn: (body: AdminPolicyWriteRequest) =>
      isEdit ? adminPoliciesApi.update(numericId, body) : adminPoliciesApi.create(body),
    onSuccess: async (saved) => {
      syncedPolicyRef.current = saved.id;
      setSavedOnce(true);
      setSubmitted(false);
      setForm(formStateFromPolicy(saved));
      await queryClient.invalidateQueries({ queryKey: ['admin', 'policies'] });
      await queryClient.invalidateQueries({ queryKey: ['admin', 'policy', saved.id] });
      toast.success(
        isNew
          ? t('adminPolicies.edit.created', { name: saved.name })
          : t('adminPolicies.edit.updated', { name: saved.name }),
      );
      if (isNew) navigate(`/admin/policies/${saved.id}`, { replace: true });
    },
  });

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (form === null) return;
    setSubmitted(true);
    if (Object.keys(errors).length > 0) return;

    const secrets: { username?: string; password?: string } = {};
    if (form.username.trim() !== '') secrets.username = form.username.trim();
    if (form.password !== '') secrets.password = form.password;

    const body: AdminPolicyWriteRequest = {
      name: form.name.trim(),
      type: form.type,
      enabled: form.enabled,
      path_template: form.pathTemplate.trim(),
      conflict_policy: form.conflictPolicy,
      config: { endpoint: form.endpoint.trim() },
      urls: form.urls.map((row, index) => ({
        alias: row.alias.trim(),
        url: row.url.trim(),
        is_default: row.is_default,
        sort: index + 1,
      })),
      sort: form.sort.trim() === '' ? 0 : Number(form.sort.trim()),
    };
    if (Object.keys(secrets).length > 0) body.secrets = secrets;
    saveMutation.mutate(body);
  };

  /* -------------------------------------------------- 连接测试 / 扫描补录 */

  const testMutation = useMutation({
    mutationFn: (targetId: number) => adminPoliciesApi.test(targetId),
  });

  const scanMutation = useMutation({
    mutationFn: (vars: { targetId: number; body: PolicyScanRequest }) =>
      adminPoliciesApi.scan(vars.targetId, vars.body),
  });

  /* ------------------------------------------------------------ 派生数据 */

  const policy = policyQuery.data ?? null;
  const capabilities =
    policy?.capabilities ?? NEW_TYPE_CAPABILITIES[form?.type ?? ''] ?? DEFAULT_NEW_CAPABILITIES;
  const canOverwrite = capabilities.includes('overwrite');
  const canStat = capabilities.includes('stat');

  const updateForm = useCallback((patch: Partial<FormState>) => {
    setForm((current) => (current === null ? current : { ...current, ...patch }));
  }, []);

  const insertVariable = (variable: string) => {
    if (form === null) return;
    const element = pathInputRef.current;
    const current = form.pathTemplate;
    if (element === null) {
      updateForm({ pathTemplate: current + variable });
      return;
    }
    const start = element.selectionStart ?? current.length;
    const end = element.selectionEnd ?? start;
    const nextTemplate = current.slice(0, start) + variable + current.slice(end);
    updateForm({ pathTemplate: nextTemplate });
    const caret = start + variable.length;
    window.requestAnimationFrame(() => {
      element.focus();
      element.setSelectionRange(caret, caret);
    });
  };

  const renderTemplateVariable = (variable: string) => {
    const length = variable.includes(':') ? variable.replace(/\D/g, '') : '8';
    const label = variable.startsWith('{md5')
      ? variable === '{md5}'
        ? t('adminPolicies.edit.variable.md5')
        : t('adminPolicies.edit.variable.md5Short', { length })
      : variable.startsWith('{sha256')
        ? variable === '{sha256}'
          ? t('adminPolicies.edit.variable.sha256')
          : t('adminPolicies.edit.variable.sha256Short', { length })
        : variable === '{sha512}'
          ? t('adminPolicies.edit.variable.sha512')
          : variable === '{ext}'
            ? t('adminPolicies.edit.variable.ext')
            : variable === '{uuid}'
              ? t('adminPolicies.edit.variable.uuid')
              : variable === '{rand}'
                ? t('adminPolicies.edit.variable.rand')
                : t('adminPolicies.edit.variable.randShort', { length });
    return (
      <Tooltip key={variable}>
        <TooltipTrigger asChild>
          <Button
            type="button"
            variant="outline"
            size="sm"
            className="font-mono"
            onClick={() => insertVariable(variable)}
          >
            {variable}
          </Button>
        </TooltipTrigger>
        <TooltipContent>
          {`${label} — ${t('adminPolicies.edit.variable.insert', { variable })}`}
        </TooltipContent>
      </Tooltip>
    );
  };

  const referenceUrls = useMemo(() => {
    const relPath = preview?.rel_path;
    if (relPath === undefined || relPath === '') return [];
    return (
      form?.urls
        .filter((row) => URL_RE.test(row.url.trim()))
        .map((row) => ({
          key: row.key,
          alias: row.alias.trim() === '' ? '—' : row.alias.trim(),
          url: `${row.url.trim().replace(/\/+$/, '')}/${relPath}`,
          is_default: row.is_default,
        })) ?? []
    );
  }, [form?.urls, preview?.rel_path]);

  const defaultUrlKey = form?.urls.find((row) => row.is_default)?.key ?? '';

  /* ---------------------------------------------------------------- 渲染 */

  if (invalidId) {
    return (
      <div>
        <PageHeader
          title={t('adminPolicies.list.title')}
          description={t('adminPolicies.edit.descriptionEdit')}
        />
        <ErrorState
          message={t('adminPolicies.edit.invalidId')}
          onRetry={() => navigate('/admin/policies')}
        />
      </div>
    );
  }

  if (isEdit && policyQuery.isError) {
    return (
      <div>
        <PageHeader
          title={t('adminPolicies.list.title')}
          description={t('adminPolicies.edit.descriptionEdit')}
        />
        <ErrorState
          message={errorText(policyQuery.error, t('adminPolicies.edit.loadFailed'))}
          onRetry={() => {
            void policyQuery.refetch();
          }}
        />
      </div>
    );
  }

  if (form === null) {
    return (
      <div>
        <PageHeader
          title={isNew ? t('adminPolicies.edit.titleNew') : t('adminPolicies.list.title')}
          description={
            isNew ? t('adminPolicies.edit.descriptionNew') : t('adminPolicies.edit.descriptionEdit')
          }
        />
        <FormSkeleton fields={6} grouped />
      </div>
    );
  }

  const hasOverride = form.conflictPolicy === 'overwrite' && !canOverwrite;

  return (
    <form onSubmit={handleSubmit} noValidate>
      <PageHeader
        title={
          isNew
            ? t('adminPolicies.edit.titleNew')
            : t('adminPolicies.edit.titleEdit', { name: form.name })
        }
        description={
          isNew ? t('adminPolicies.edit.descriptionNew') : t('adminPolicies.edit.descriptionEdit')
        }
        actions={
          <>
            <Button type="button" variant="ghost" onClick={() => navigate('/admin/policies')}>
              <ArrowLeft className="size-4" />
              {t('adminPolicies.edit.back')}
            </Button>
            <Button type="submit" disabled={saveMutation.isPending}>
              <Save className="size-4" />
              {saveMutation.isPending ? t('common.action.saving') : t('adminPolicies.edit.save')}
            </Button>
          </>
        }
      />

      {saveMutation.isError && (
        <Alert variant="destructive" className="mb-4">
          <AlertDescription>
            {errorText(saveMutation.error, t('adminPolicies.edit.error.saveFailed'))}
          </AlertDescription>
        </Alert>
      )}

      {isNew && settingsQuery.isError && (
        <Alert className="border-transparent bg-warning/10 text-warning mb-4">
          <AlertDescription>
            {t('adminPolicies.edit.presetFailed', {
              template: DEFAULT_PATH_TEMPLATE,
              conflict: t('adminPolicies.conflict.rename'),
            })}
          </AlertDescription>
        </Alert>
      )}

      {hasOverride && (
        <Alert className="border-transparent bg-warning/10 text-warning mb-4">
          <AlertDescription>{t('adminPolicies.edit.conflictDisabled.overwrite')}</AlertDescription>
        </Alert>
      )}

      <Tabs value={tab} onValueChange={handleTabChange} className="flex-col gap-0">
        <TabsList
          variant="line"
          className="group-data-horizontal/tabs:h-auto border-border w-full flex-wrap justify-start rounded-none border-b p-0 pb-[5px]"
        >
          {visibleTabs.map((item) => (
            <TabsTrigger key={item.id} value={item.id} className="h-9 flex-none gap-2 px-3">
              {item.icon}
              {t(`adminPolicies.edit.section.${item.labelKey}`)}
            </TabsTrigger>
          ))}
        </TabsList>

        <TabsContent value="basic" className={TAB_PANEL_CLASS}>
          <PolicySection hint={t('adminPolicies.edit.section.basicHint')}>
            <div className="flex flex-col gap-5">
              <FormField
                id="policy-name"
                label={t('adminPolicies.edit.field.name')}
                error={errorFor('name')}
                className="sm:w-80"
              >
                <Input
                  id="policy-name"
                  value={form.name}
                  onChange={(event) => updateForm({ name: event.target.value })}
                  placeholder={t('adminPolicies.edit.field.namePlaceholder')}
                  aria-invalid={errorFor('name') !== undefined}
                  required
                />
              </FormField>
              <FormField
                id="policy-type"
                label={t('adminPolicies.edit.field.type')}
                hint={isEdit ? t('adminPolicies.edit.field.typeReadonly') : undefined}
                className="sm:w-[240px]"
              >
                <Select
                  value={form.type}
                  onValueChange={(value) => updateForm({ type: value })}
                  disabled={isEdit}
                >
                  <SelectTrigger id="policy-type" className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {TYPE_OPTIONS.map((option) => (
                      <SelectItem key={option} value={option}>
                        {t(`adminPolicies.type.${option}`)}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </FormField>
              <FormField
                id="policy-sort"
                label={t('adminPolicies.edit.field.sort')}
                error={errorFor('sort')}
                hint={t('adminPolicies.edit.field.sortHint')}
                className="sm:w-[160px]"
              >
                <Input
                  id="policy-sort"
                  value={form.sort}
                  onChange={(event) => updateForm({ sort: event.target.value })}
                  inputMode="numeric"
                  aria-invalid={errorFor('sort') !== undefined}
                />
              </FormField>
              <div className="flex flex-col gap-1.5">
                <div className="flex items-center gap-2">
                  <Switch
                    id="policy-enabled"
                    checked={form.enabled}
                    onCheckedChange={(checked) => updateForm({ enabled: checked })}
                  />
                  <Label htmlFor="policy-enabled" className="font-normal">
                    {t('adminPolicies.edit.field.enabled')}
                  </Label>
                </div>
                <p className="text-muted-foreground pl-10 text-xs">
                  {t('adminPolicies.edit.field.enabledHint')}
                </p>
              </div>
            </div>
          </PolicySection>
        </TabsContent>

        <TabsContent value="connection" className={TAB_PANEL_CLASS}>
          <PolicySection
            hint={t('adminPolicies.edit.section.connectionHint')}
            actions={
              isEdit ? (
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  disabled={testMutation.isPending}
                  onClick={() => testMutation.mutate(numericId)}
                >
                  {testMutation.isPending
                    ? t('adminPolicies.edit.test.running')
                    : t('adminPolicies.edit.test.button')}
                </Button>
              ) : (
                <p className="text-muted-foreground text-xs">
                  {t('adminPolicies.edit.test.needSave')}
                </p>
              )
            }
          >
            <div className="flex flex-col gap-5">
              <FormField
                id="policy-endpoint"
                label={t('adminPolicies.edit.field.endpoint')}
                error={errorFor('endpoint')}
                hint={t('adminPolicies.edit.field.endpointHint')}
              >
                <Input
                  id="policy-endpoint"
                  value={form.endpoint}
                  onChange={(event) => updateForm({ endpoint: event.target.value })}
                  placeholder={t('adminPolicies.edit.field.endpointPlaceholder')}
                  aria-invalid={errorFor('endpoint') !== undefined}
                  required
                />
              </FormField>
              <FormField id="policy-username" label={t('adminPolicies.edit.field.username')}>
                <Input
                  id="policy-username"
                  value={form.username}
                  onChange={(event) => updateForm({ username: event.target.value })}
                  autoComplete="off"
                />
              </FormField>
              <FormField
                id="policy-password"
                label={t('adminPolicies.edit.field.password')}
                hint={t('adminPolicies.edit.field.passwordHint')}
              >
                <div className="relative">
                  <Input
                    id="policy-password"
                    type={showPassword ? 'text' : 'password'}
                    value={form.password}
                    onChange={(event) => updateForm({ password: event.target.value })}
                    autoComplete="new-password"
                    className="pr-9"
                  />
                  <button
                    type="button"
                    aria-label={t(
                      showPassword
                        ? 'adminPolicies.edit.field.passwordHide'
                        : 'adminPolicies.edit.field.passwordShow',
                    )}
                    className="text-muted-foreground hover:text-foreground absolute top-1/2 right-2 -translate-y-1/2 transition-colors"
                    onClick={() => setShowPassword((current) => !current)}
                  >
                    {showPassword ? <EyeOff className="size-4" /> : <Eye className="size-4" />}
                  </button>
                </div>
              </FormField>
              {testMutation.data !== undefined &&
                (testMutation.data.ok ? (
                  <Alert className="border-transparent bg-success/10 text-success">
                    <AlertDescription>
                      {t('adminPolicies.edit.test.ok', {
                        duration: formatDuration(testMutation.data.elapsed_ms),
                      })}
                    </AlertDescription>
                  </Alert>
                ) : (
                  <Alert variant="destructive">
                    <AlertDescription>
                      {testMutation.data.error ?? t('adminPolicies.edit.test.failed')}
                    </AlertDescription>
                  </Alert>
                ))}
              {testMutation.isError && (
                <Alert variant="destructive">
                  <AlertDescription>
                    {errorText(testMutation.error, t('adminPolicies.edit.test.failed'))}
                  </AlertDescription>
                </Alert>
              )}
            </div>
          </PolicySection>
        </TabsContent>

        <TabsContent value="path" className={TAB_PANEL_CLASS}>
          <PolicySection hint={t('adminPolicies.edit.section.pathHint')}>
            <div className="flex flex-col gap-4">
              <FormField
                id="policy-path-template"
                label={t('adminPolicies.edit.field.pathTemplate')}
                error={errorFor('pathTemplate')}
              >
                <Input
                  id="policy-path-template"
                  ref={pathInputRef}
                  value={form.pathTemplate}
                  onChange={(event) => updateForm({ pathTemplate: event.target.value })}
                  placeholder={t('adminPolicies.edit.field.pathTemplatePlaceholder')}
                  aria-invalid={errorFor('pathTemplate') !== undefined}
                  className="font-mono"
                />
              </FormField>
              <div>
                <p className="text-muted-foreground mb-1.5 text-sm">
                  {t('adminPolicies.edit.variable.label')}
                </p>
                <div className="flex flex-wrap items-center gap-1">
                  {TEMPLATE_VARIABLES.map(renderTemplateVariable)}
                </div>
              </div>
              <p className="text-muted-foreground text-xs">
                {t('adminPolicies.edit.variable.note')}
              </p>

              <Separator />
              <div className="flex items-center justify-between gap-2">
                <h3 className="font-heading text-sm font-semibold">
                  {t('adminPolicies.edit.preview.title')}
                </h3>
                <p className="text-muted-foreground text-xs">
                  {t('adminPolicies.edit.preview.sampleNote')}
                </p>
              </div>

              {previewSkeleton ? (
                <TextLinesSkeleton lines={3} />
              ) : errors.pathTemplate !== undefined ? (
                <p className="text-muted-foreground text-sm">
                  {t('adminPolicies.edit.preview.invalid')}
                </p>
              ) : previewError !== null ? (
                <Alert variant="destructive">
                  <AlertDescription>
                    {t('adminPolicies.edit.preview.failed')}:{' '}
                    {errorText(previewError, t('common.state.failed'))}
                  </AlertDescription>
                </Alert>
              ) : preview === undefined ? (
                <TextLineSkeleton width="60%" />
              ) : (
                <div className="flex flex-col gap-3">
                  <PathReferenceRows relPath={preview.rel_path} objectKey={preview.object_key} />
                  <Separator />
                  <UrlReferenceList
                    title={t('adminPolicies.edit.preview.urls')}
                    items={referenceUrls}
                    emptyText={t('adminPolicies.edit.reference.emptyUrls')}
                  />
                </div>
              )}
            </div>
          </PolicySection>
        </TabsContent>

        <TabsContent value="urls" className={TAB_PANEL_CLASS}>
          <PolicySection
            hint={t('adminPolicies.edit.section.urlsHint')}
            actions={
              <Button
                type="button"
                size="sm"
                onClick={() =>
                  updateForm({
                    urls: [
                      ...form.urls,
                      { key: nextUrlKey(), alias: '', url: '', is_default: form.urls.length === 0 },
                    ],
                  })
                }
              >
                <Plus className="size-4" />
                {t('adminPolicies.edit.url.add')}
              </Button>
            }
          >
            <div className="flex flex-col gap-4">
              {form.urls.length === 0 && (
                <Alert className="border-transparent bg-info/10 text-info">
                  <AlertDescription>{t('adminPolicies.edit.url.empty')}</AlertDescription>
                </Alert>
              )}

              {form.urls.length > 0 && (
                <RadioGroup
                  value={defaultUrlKey}
                  onValueChange={(value) => {
                    updateForm({
                      urls: form.urls.map((row) => ({ ...row, is_default: row.key === value })),
                    });
                  }}
                  className="gap-3"
                >
                  {form.urls.map((row, index) => (
                    <div key={row.key} className="flex flex-col gap-2 sm:flex-row sm:items-start">
                      <label className="flex min-w-24 items-center gap-2 text-sm">
                        <RadioGroupItem value={row.key} />
                        {t('adminPolicies.edit.url.default')}
                      </label>
                      <div className="flex min-w-0 flex-1 flex-col gap-2 sm:flex-row sm:items-start">
                        <FormField
                          id={`url-alias-${row.key}`}
                          label={t('adminPolicies.edit.url.aliasLabel')}
                          error={errorFor(`urls.${index}.alias`)}
                          className="sm:w-[220px]"
                        >
                          <Input
                            id={`url-alias-${row.key}`}
                            value={row.alias}
                            onChange={(event) => {
                              const alias = event.target.value;
                              updateForm({
                                urls: form.urls.map((item) =>
                                  item.key === row.key ? { ...item, alias } : item,
                                ),
                              });
                            }}
                            onBlur={() => setUrlTouched(true)}
                            placeholder={t('adminPolicies.edit.url.aliasPlaceholder')}
                            aria-invalid={errorFor(`urls.${index}.alias`) !== undefined}
                          />
                        </FormField>
                        <FormField
                          id={`url-value-${row.key}`}
                          label={t('adminPolicies.edit.url.urlLabel')}
                          error={errorFor(`urls.${index}.url`)}
                          className="flex-1"
                        >
                          <Input
                            id={`url-value-${row.key}`}
                            value={row.url}
                            onChange={(event) => {
                              const url = event.target.value;
                              updateForm({
                                urls: form.urls.map((item) =>
                                  item.key === row.key ? { ...item, url } : item,
                                ),
                              });
                            }}
                            onBlur={() => setUrlTouched(true)}
                            placeholder={t('adminPolicies.edit.url.urlPlaceholder')}
                            aria-invalid={errorFor(`urls.${index}.url`) !== undefined}
                          />
                        </FormField>
                      </div>
                      <div className="flex shrink-0 items-center gap-1">
                        <Tooltip>
                          <TooltipTrigger asChild>
                            <span className="inline-flex">
                              <Button
                                type="button"
                                variant="ghost"
                                size="icon-sm"
                                disabled={index === 0}
                                aria-label={t('adminPolicies.edit.url.moveUp')}
                                onClick={() => {
                                  const next = [...form.urls];
                                  const previous = next[index - 1];
                                  next[index - 1] = next[index];
                                  next[index] = previous;
                                  updateForm({ urls: next });
                                }}
                              >
                                <ArrowUp className="size-4" />
                              </Button>
                            </span>
                          </TooltipTrigger>
                          <TooltipContent>{t('adminPolicies.edit.url.moveUp')}</TooltipContent>
                        </Tooltip>
                        <Tooltip>
                          <TooltipTrigger asChild>
                            <span className="inline-flex">
                              <Button
                                type="button"
                                variant="ghost"
                                size="icon-sm"
                                disabled={index === form.urls.length - 1}
                                aria-label={t('adminPolicies.edit.url.moveDown')}
                                onClick={() => {
                                  const next = [...form.urls];
                                  const following = next[index + 1];
                                  next[index + 1] = next[index];
                                  next[index] = following;
                                  updateForm({ urls: next });
                                }}
                              >
                                <ArrowDown className="size-4" />
                              </Button>
                            </span>
                          </TooltipTrigger>
                          <TooltipContent>{t('adminPolicies.edit.url.moveDown')}</TooltipContent>
                        </Tooltip>
                        <Tooltip>
                          <TooltipTrigger asChild>
                            <Button
                              type="button"
                              variant="ghost"
                              size="icon-sm"
                              className="text-destructive"
                              aria-label={t('adminPolicies.edit.url.remove')}
                              onClick={() => {
                                const remaining = form.urls.filter((item) => item.key !== row.key);
                                const needsDefault = !remaining.some((item) => item.is_default);
                                updateForm({
                                  urls:
                                    needsDefault && remaining.length > 0
                                      ? remaining.map((item, position) => ({
                                          ...item,
                                          is_default: position === 0,
                                        }))
                                      : remaining,
                                });
                              }}
                            >
                              <Trash2 className="size-4" />
                            </Button>
                          </TooltipTrigger>
                          <TooltipContent>{t('adminPolicies.edit.url.remove')}</TooltipContent>
                        </Tooltip>
                      </div>
                    </div>
                  ))}
                </RadioGroup>
              )}

              {submitted && errors.urls !== undefined && (
                <p className="text-destructive text-sm">{errors.urls}</p>
              )}
              <p className="text-muted-foreground text-xs">
                {t('adminPolicies.edit.url.defaultHint')}
              </p>
            </div>
          </PolicySection>
        </TabsContent>

        <TabsContent value="conflict" className={TAB_PANEL_CLASS}>
          <PolicySection
            hint={t('adminPolicies.edit.section.conflictHint')}
            actions={
              <div className="flex flex-wrap items-center gap-1">
                {capabilities.map((capability) => (
                  <Badge key={capability} variant="outline">
                    {t(`adminPolicies.capability.${capability}`, { defaultValue: capability })}
                  </Badge>
                ))}
              </div>
            }
          >
            <div className="flex flex-col gap-4">
              <div className="flex max-w-[320px] flex-col gap-2">
                <Label htmlFor="policy-conflict-policy">
                  {t('adminPolicies.edit.field.conflictPolicy')}
                </Label>
                <Select
                  value={form.conflictPolicy}
                  onValueChange={(value) => updateForm({ conflictPolicy: value as ConflictPolicy })}
                >
                  <SelectTrigger id="policy-conflict-policy" className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {CONFLICT_OPTIONS.map((option) => (
                      <SelectItem
                        key={option}
                        value={option}
                        disabled={option === 'overwrite' && !canOverwrite}
                      >
                        {t(`adminPolicies.conflict.${option}`)}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <p className="text-muted-foreground text-sm">
                {t(`adminPolicies.conflict.${form.conflictPolicy}Hint`)}
              </p>
              {!canOverwrite && (
                <p className="text-warning text-sm">
                  {t('adminPolicies.edit.conflictDisabled.overwrite')}
                </p>
              )}
              {!canStat && (
                <p className="text-warning text-sm">
                  {t('adminPolicies.edit.conflictDisabled.stat')}
                </p>
              )}
            </div>
          </PolicySection>
        </TabsContent>

        {isEdit && (
          <TabsContent value="scan" className={TAB_PANEL_CLASS}>
            <PolicySection
              hint={t('adminPolicies.edit.section.scanHint')}
              actions={
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  disabled={scanMutation.isPending}
                  onClick={() => {
                    if (!isEdit) return;
                    const trimmed = scanPath.trim();
                    scanMutation.mutate({
                      targetId: numericId,
                      body: {
                        ...(trimmed === '' ? {} : { path: trimmed }),
                        dry_run: dryRun,
                      },
                    });
                  }}
                >
                  <Play className="size-4" />
                  {scanMutation.isPending
                    ? t('adminPolicies.edit.scan.running')
                    : t('adminPolicies.edit.scan.button')}
                </Button>
              }
            >
              <div className="flex flex-col gap-4">
                <FormField
                  id="policy-scan-path"
                  label={t('adminPolicies.edit.field.scanPath')}
                  hint={t('adminPolicies.edit.field.scanPathOptional')}
                >
                  <Input
                    id="policy-scan-path"
                    value={scanPath}
                    onChange={(event) => setScanPath(event.target.value)}
                    className="font-mono"
                  />
                </FormField>
                <div>
                  <label className="flex items-center gap-2 text-sm">
                    <Switch checked={dryRun} onCheckedChange={setDryRun} />
                    {t('adminPolicies.edit.field.dryRun')}
                  </label>
                  <p className="text-muted-foreground text-xs">
                    {t('adminPolicies.edit.field.dryRunHint')}
                  </p>
                </div>
                <p className="text-muted-foreground text-xs">{t('adminPolicies.edit.scan.q21')}</p>
                {scanMutation.isError && (
                  <Alert variant="destructive">
                    <AlertDescription>
                      {errorText(scanMutation.error, t('adminPolicies.edit.scan.failed'))}
                    </AlertDescription>
                  </Alert>
                )}
                {scanMutation.data !== undefined && (
                  <Alert className="border-transparent bg-info/10 text-info">
                    <AlertDescription>
                      <p className="text-sm">
                        {scanMutation.variables?.body.dry_run === true
                          ? t('adminPolicies.edit.scan.dryRunPrefix')
                          : ''}
                        {t('adminPolicies.edit.scan.result', {
                          images: scanMutation.data.images,
                          files: scanMutation.data.files,
                          skipped: scanMutation.data.skipped,
                        })}
                      </p>
                      {scanMutation.data.failed.length > 0 && (
                        <div className="mt-2 flex flex-col gap-1">
                          <p className="text-sm font-semibold">
                            {t('adminPolicies.edit.scan.failedTitle', {
                              count: scanMutation.data.failed.length,
                            })}
                          </p>
                          {scanMutation.data.failed.map((item) => (
                            <p
                              key={`${item.path}-${item.error}`}
                              className="font-mono text-xs break-all"
                            >
                              {item.path} — {item.error}
                            </p>
                          ))}
                        </div>
                      )}
                    </AlertDescription>
                  </Alert>
                )}
              </div>
            </PolicySection>
          </TabsContent>
        )}
      </Tabs>

      {/* 对照表 (保存结果): 固定在标签下方, 不占标签 */}
      {(isEdit || savedOnce) && (
        <PolicySection
          className="mt-8"
          title={t('adminPolicies.edit.section.reference')}
          hint={t('adminPolicies.edit.section.referenceHint')}
        >
          <div className="flex flex-col gap-4">
            {previewSkeleton ? (
              <TextLinesSkeleton lines={4} />
            ) : preview === undefined ? (
              <p className="text-muted-foreground text-sm">
                {t('adminPolicies.edit.reference.pending')}
              </p>
            ) : (
              <div className="flex flex-col gap-3">
                <PathReferenceRows relPath={preview.rel_path} objectKey={preview.object_key} />
                <Separator />
                <UrlReferenceList
                  title={t('adminPolicies.edit.reference.urls')}
                  items={referenceUrls}
                  emptyText={t('adminPolicies.edit.reference.emptyUrls')}
                />
              </div>
            )}
            <div className="flex flex-wrap items-center gap-1">
              <p className="text-muted-foreground mr-2 text-sm">
                {t('adminPolicies.edit.reference.capabilities')}
              </p>
              {capabilities.map((capability) => (
                <Badge key={capability} variant="outline">
                  {t(`adminPolicies.capability.${capability}`, {
                    defaultValue: capability,
                  })}
                </Badge>
              ))}
            </div>
          </div>
        </PolicySection>
      )}
    </form>
  );
}
