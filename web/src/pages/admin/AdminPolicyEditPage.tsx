import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { FormEvent, ReactNode } from 'react';
import { useNavigate, useParams, useSearchParams } from 'react-router';
import {
  ArrowLeft,
  Check,
  Download,
  Eye,
  EyeOff,
  FolderTree,
  GripVertical,
  Globe,
  Info,
  Play,
  Plus,
  RotateCcw,
  Save,
  ScanSearch,
  Server,
  Shuffle,
  Trash2,
  Upload,
} from 'lucide-react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import { useTranslation } from 'react-i18next';
import { adminPoliciesApi, adminSettingsApi } from '../../api';
import type {
  AdminPolicy,
  AdminPolicyWriteRequest,
  CloudreveLoginResult,
  ConflictPolicy,
  PolicyScanRequest,
  PolicyScanResult,
} from '../../api/types';
import { CopyButton } from '../../components/CopyButton';
import { PageHeader } from '../../components/PageHeader';
import { ErrorState } from '../../components/States';
import {
  PolicyEditorSkeleton,
  TextLineSkeleton,
  TextLinesSkeleton,
} from '../../components/skeletons';
import { Alert, AlertDescription } from '../../components/ui/alert';
import { Badge } from '../../components/ui/badge';
import { Button } from '../../components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '../../components/ui/dialog';
import { Input } from '../../components/ui/input';
import { Label } from '../../components/ui/label';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '../../components/ui/select';
import { Separator } from '../../components/ui/separator';
import { Skeleton } from '../../components/ui/skeleton';
import { Switch } from '../../components/ui/switch';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '../../components/ui/tabs';
import { Textarea } from '../../components/ui/textarea';
import { Tooltip, TooltipContent, TooltipTrigger } from '../../components/ui/tooltip';
import { useDebouncedValue } from '../../hooks/useDebouncedValue';
import { useDelayedLoading } from '../../hooks/useDelayedLoading';
import { cn } from '../../lib/utils';
import { copyText } from '../../utils/clipboard';
import { formatDateTime, formatDuration } from '../../utils/format';
import {
  buildPolicyTransferPayload,
  parsePolicyTransfer,
  PolicyTransferError,
} from './policyTransfer';
import type { PolicyTransferResult } from './policyTransfer';

/**
 * 可选后端类型: 只列出已注册且管理界面支持的驱动 (docs/storage-policy.md 第 3 节),
 * 未注册的类型提交会被后端以 400 拒绝.
 */
const TYPE_OPTIONS = ['webdav', 'sobot', 'celine', 'metaso', 'oneimg', 'cloudreve'] as const;

const CONFLICT_OPTIONS: readonly ConflictPolicy[] = ['rename', 'skip'];

/** 新建时的驱动特征: 编辑模式以服务端返回的 capabilities / url_from_backend 为准 */
const NEW_TYPE_TRAITS: Record<string, { capabilities: string[]; urlFromBackend: boolean }> = {
  webdav: {
    capabilities: ['upload', 'list', 'read', 'delete', 'overwrite', 'stat', 'mkdir'],
    urlFromBackend: false,
  },
  sobot: { capabilities: ['upload'], urlFromBackend: true },
  celine: { capabilities: ['upload'], urlFromBackend: true },
  metaso: { capabilities: ['upload'], urlFromBackend: true },
  oneimg: { capabilities: ['upload'], urlFromBackend: true },
  cloudreve: { capabilities: ['upload'], urlFromBackend: true },
};

/** 新建策略选择后端后自动填入的提示文字 (普通用户可见的简单限制) */
const DEFAULT_HINT_KEYS: Record<string, string> = {
  sobot: 'adminPolicies.edit.defaultHint.sobot',
  celine: 'adminPolicies.edit.defaultHint.celine',
  metaso: 'adminPolicies.edit.defaultHint.metaso',
  oneimg: 'adminPolicies.edit.defaultHint.oneimg',
};

/** 由后端分配直链的驱动不显示路径, 域名, 冲突与扫描标签 */
const URL_BACKEND_HIDDEN_TABS: readonly string[] = ['path', 'urls', 'conflict', 'scan'];

/** Cloudreve 需要连接配置与魔法路径, 但仍由后端分配直链 */
const TYPE_HIDDEN_TABS: Record<string, readonly string[]> = {
  sobot: ['connection', ...URL_BACKEND_HIDDEN_TABS],
  celine: ['connection', ...URL_BACKEND_HIDDEN_TABS],
  metaso: ['connection', ...URL_BACKEND_HIDDEN_TABS],
  oneimg: URL_BACKEND_HIDDEN_TABS,
  cloudreve: ['urls', 'conflict', 'scan'],
};

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

/** 对象键与域名 URL 对照表只在基本信息与分发域名标签下显示; 魔法路径已有实时预览 */
const REFERENCE_TABS: readonly PolicyTabId[] = ['basic', 'urls'];

/** 魔法路径变量快捷按钮 (docs/storage-policy.md 第 4 节) */
const TEMPLATE_VARIABLES = [
  '{md5}',
  '{md5:8}',
  '{md5:2}',
  '{sha256}',
  '{sha256:16}',
  '{sha512}',
  '{ext}',
  '{seq}',
  '{uuid}',
  '{rand}',
  '{rand:8}',
] as const;

const PATH_VARIABLE_RE = /\{(?:md5|sha256|sha512)(?::\d+)?\}|\{seq\}/;
const SEQ_RE = /\{seq\}/;
const URL_RE = /^https?:\/\//i;

const DEFAULT_PATH_TEMPLATE = '{md5:2}/{md5}.{ext}';
const CLOUDREVE_DEFAULT_PATH_TEMPLATE = '{md5:2}/{md5}.{ext}';
const CLOUDREVE_DEFAULT_DIRECTORY = '/picsky';
const PATH_EXT_SUFFIX = '.{ext}';
const DEFAULT_CONFLICT_POLICY: ConflictPolicy = 'rename';

const PREVIEW_MD5 = '0f1e2d3c4b5a69788796a5b4c3d2e1f0';
const PREVIEW_SHA256 = `${PREVIEW_MD5}0f1e2d3c4b5a69788796a5b4c3d2e1f0`;
const PREVIEW_SHA512 = PREVIEW_MD5.repeat(4);

interface UrlRow {
  key: string;
  alias: string;
  url: string;
}

interface FormState {
  name: string;
  hint: string;
  type: string;
  enabled: boolean;
  endpoint: string;
  token: string;
  bucketId: string;
  directory: string;
  autoLogin: boolean;
  username: string;
  password: string;
  cloudreveLoggedIn: boolean;
  cloudreveRefreshToken: string;
  cloudreveRefreshExpires: string;
  cloudreveAccessExpires: string;
  pathTemplate: string;
  conflictPolicy: ConflictPolicy;
  reuseReleasedSeq: boolean;
  urls: UrlRow[];
}

let urlKeySeed = 0;

function nextUrlKey(): string {
  urlKeySeed += 1;
  return `url-${urlKeySeed}`;
}
function moveUrlRow(urls: UrlRow[], sourceKey: string, targetKey: string): UrlRow[] {
  const sourceIndex = urls.findIndex((row) => row.key === sourceKey);
  const targetIndex = urls.findIndex((row) => row.key === targetKey);
  if (sourceIndex < 0 || targetIndex < 0 || sourceIndex === targetIndex) return urls;

  const next = [...urls];
  const [moved] = next.splice(sourceIndex, 1);
  if (moved === undefined) return urls;
  next.splice(targetIndex, 0, moved);
  return next;
}

function formStateFromPolicy(policy: AdminPolicy): FormState {
  const urls = [...policy.urls]
    .sort((left, right) => Number(right.is_default) - Number(left.is_default))
    .map((item) => ({
      key: nextUrlKey(),
      alias: item.alias,
      url: item.url,
    }));

  return {
    name: policy.name,
    hint: policy.hint,
    type: policy.type,
    enabled: policy.enabled,
    endpoint: typeof policy.config.endpoint === 'string' ? policy.config.endpoint : '',
    token: policy.secrets?.token ?? '',
    bucketId: typeof policy.config.bucket_id === 'string' ? policy.config.bucket_id : '',
    directory:
      typeof policy.config.directory === 'string' && policy.config.directory.trim() !== ''
        ? policy.config.directory
        : CLOUDREVE_DEFAULT_DIRECTORY,
    autoLogin: policy.config.auto_login === 'true',
    username: policy.secrets?.username ?? '',
    password: policy.secrets?.password ?? '',
    cloudreveLoggedIn: policy.login_ready === true,
    cloudreveRefreshToken: policy.secrets?.refresh_token ?? '',
    cloudreveRefreshExpires: policy.secrets?.refresh_expires ?? '',
    cloudreveAccessExpires: policy.secrets?.access_expires ?? '',
    pathTemplate: policy.path_template,
    conflictPolicy: policy.conflict_policy,
    reuseReleasedSeq: policy.reuse_released_seq,
    urls,
  };
}

/** 后端错误 -> 可展示文本 (ApiError 由 axios 拦截器转换) */
function errorText(error: unknown, fallback: string): string {
  if (error instanceof Error && error.message !== '') return error.message;
  return fallback;
}

function scanResultFromTask(raw: string | undefined): PolicyScanResult | null {
  if (!raw) return null;
  try {
    const parsed: unknown = JSON.parse(raw);
    if (typeof parsed !== 'object' || parsed === null) return null;
    const result = parsed as Partial<PolicyScanResult>;
    if (
      typeof result.images !== 'number' ||
      typeof result.files !== 'number' ||
      typeof result.skipped !== 'number' ||
      !Array.isArray(result.failed)
    ) {
      return null;
    }
    return result as PolicyScanResult;
  } catch {
    return null;
  }
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
  labelAction,
  error,
  hint,
  className,
  children,
}: {
  id: string;
  label: string;
  labelAction?: ReactNode;
  error?: string;
  hint?: string;
  className?: string;
  children: ReactNode;
}) {
  return (
    <div className={cn('flex min-w-0 flex-col gap-2', className)}>
      <div className="flex items-center justify-between gap-2">
        <Label htmlFor={id}>{label}</Label>
        {labelAction}
      </div>
      {children}
      {error !== undefined ? (
        <p className="text-destructive text-sm">{error}</p>
      ) : hint !== undefined ? (
        <p className="text-muted-foreground text-sm">{hint}</p>
      ) : null}
    </div>
  );
}

/** 开关字段: 标签与说明在左, 开关固定右侧 */
function SwitchRow({
  id,
  label,
  hint,
  checked,
  onCheckedChange,
}: {
  id: string;
  label: string;
  hint: string;
  checked: boolean;
  onCheckedChange: (checked: boolean) => void;
}) {
  return (
    <div className="flex items-start justify-between gap-4">
      <div className="min-w-0">
        <Label htmlFor={id} className="font-normal">
          {label}
        </Label>
        <p className="text-muted-foreground mt-2 text-xs">{hint}</p>
      </div>
      <Switch id={id} checked={checked} onCheckedChange={onCheckedChange} className="shrink-0" />
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
  const [bucketMenuOpened, setBucketMenuOpened] = useState(false);
  const [urlTouched, setUrlTouched] = useState(false);
  const [draggedUrlKey, setDraggedUrlKey] = useState<string | null>(null);
  const [savedOnce, setSavedOnce] = useState(false);
  const [scanPath, setScanPath] = useState('');
  const [dryRun, setDryRun] = useState(false);
  const [showPassword, setShowPassword] = useState(false);
  const [showRefreshToken, setShowRefreshToken] = useState(false);
  const [exportOpen, setExportOpen] = useState(false);
  const [importOpen, setImportOpen] = useState(false);
  const [twoFactorOpen, setTwoFactorOpen] = useState(false);
  const [twoFactorSessionID, setTwoFactorSessionID] = useState('');
  const [twoFactorCode, setTwoFactorCode] = useState('');
  const [twoFactorError, setTwoFactorError] = useState<string | null>(null);

  // 标签写入 ?tab=: 刷新与深链保持当前分组; 默认标签不落 URL, 扫描补录仅编辑模式可见
  const [searchParams, setSearchParams] = useSearchParams();
  const formURLFromBackend = NEW_TYPE_TRAITS[form?.type ?? '']?.urlFromBackend ?? false;
  const urlBackendHint = formURLFromBackend
    ? t(`adminPolicies.edit.field.urlBackendHint.${form?.type ?? ''}`, { defaultValue: '' })
    : '';
  const availableTabs = isEdit ? POLICY_TABS : POLICY_TABS.filter((item) => item.id !== 'scan');
  const hiddenTabs = formURLFromBackend
    ? (TYPE_HIDDEN_TABS[form?.type ?? ''] ?? URL_BACKEND_HIDDEN_TABS)
    : [];
  const visibleTabs = availableTabs.filter((item) => !hiddenTabs.includes(item.id));
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

  const bucketCredentialsReady =
    form?.type === 'oneimg' && form.endpoint.trim() !== '' && form.token.trim() !== '';
  const bucketsQuery = useQuery({
    queryKey: ['admin', 'policy-buckets', form?.endpoint ?? '', form?.token ?? ''],
    queryFn: () =>
      adminPoliciesApi.buckets({
        type: 'oneimg',
        config: { endpoint: form?.endpoint.trim() ?? '' },
        secrets: { token: form?.token.trim() ?? '' },
      }),
    enabled: bucketCredentialsReady && (isEdit || bucketMenuOpened),
    retry: false,
    staleTime: 5 * 60_000,
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
      hint: '',
      type: TYPE_OPTIONS[0],
      enabled: true,
      endpoint: '',
      token: '',
      bucketId: '',
      directory: CLOUDREVE_DEFAULT_DIRECTORY,
      autoLogin: false,
      username: '',
      password: '',
      cloudreveLoggedIn: false,
      cloudreveRefreshToken: '',
      cloudreveRefreshExpires: '',
      cloudreveAccessExpires: '',
      pathTemplate:
        typeof rawTemplate === 'string' && rawTemplate.trim() !== ''
          ? rawTemplate
          : DEFAULT_PATH_TEMPLATE,
      conflictPolicy:
        CONFLICT_OPTIONS.find((option) => option === rawConflict) ?? DEFAULT_CONFLICT_POLICY,
      reuseReleasedSeq: false,
      urls: [{ key: nextUrlKey(), alias: '', url: '' }],
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
  const usesPathTemplate = !formURLFromBackend || form?.type === 'cloudreve';

  useEffect(() => {
    const template = debouncedTemplate.trim();
    if (!usesPathTemplate || template === '' || !PATH_VARIABLE_RE.test(template)) {
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
  }, [debouncedTemplate, usesPathTemplate, runPreview, resetPreview]);

  /* ---------------------------------------------------------------- 校验 */

  const errors = useMemo(() => {
    const next: Record<string, string> = {};
    if (form === null) return next;

    if (!formURLFromBackend || form.type === 'cloudreve') {
      const template = form.pathTemplate.trim();
      if (template === '') next.pathTemplate = t('adminPolicies.edit.error.templateRequired');
      else if (!PATH_VARIABLE_RE.test(template))
        next.pathTemplate = t('adminPolicies.edit.error.templateVariable');
      else if (template.startsWith('/') || template.includes('//') || template.includes('..')) {
        next.pathTemplate = t('adminPolicies.edit.error.templateInvalid');
      } else if (!template.endsWith(PATH_EXT_SUFFIX)) {
        next.pathTemplate = t('adminPolicies.edit.error.templateExtSuffix');
      }

      if (SEQ_RE.test(template) && form.conflictPolicy !== 'rename') {
        next.conflictPolicy = t('adminPolicies.edit.error.seqRequiresRename');
      }
    }

    if (
      (!formURLFromBackend || form.type === 'cloudreve' || form.type === 'oneimg') &&
      form.endpoint.trim() === ''
    ) {
      next.endpoint = t('adminPolicies.edit.error.endpointRequired');
    }

    if (form.type === 'oneimg' && form.token.trim() === '') {
      next.token = t('adminPolicies.edit.error.tokenRequired');
    }

    if (form.type === 'cloudreve' && form.autoLogin && !form.cloudreveLoggedIn) {
      if (form.username.trim() === '') {
        next.username = t('adminPolicies.edit.error.cloudreveEmailRequired');
      }
      if (form.password === '') {
        next.password = t('adminPolicies.edit.error.cloudrevePasswordRequired');
      }
    }

    if (form.name.trim() === '') next.name = t('adminPolicies.edit.error.nameRequired');
    if ([...form.hint.trim()].length > 500) {
      next.hint = t('adminPolicies.edit.error.hintTooLong');
    }

    if (!formURLFromBackend) {
      form.urls.forEach((row, index) => {
        if (row.alias.trim() === '')
          next[`urls.${index}.alias`] = t('adminPolicies.edit.error.urlAliasRequired');
        if (row.url.trim() === '')
          next[`urls.${index}.url`] = t('adminPolicies.edit.error.urlRequired');
        else if (!URL_RE.test(row.url.trim()))
          next[`urls.${index}.url`] = t('adminPolicies.edit.error.urlInvalid');
      });

      if (form.urls.length === 0) next.urls = t('adminPolicies.edit.error.urlsRequired');
    }

    return next;
  }, [form, formURLFromBackend, t]);

  const sequenceTemplate = form !== null && SEQ_RE.test(form.pathTemplate.trim());

  const errorFor = (key: string) => {
    const message = errors[key];
    if (message === undefined) return undefined;
    if (key === 'pathTemplate' || key === 'conflictPolicy') return message;
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

    const isCloudreve = form.type === 'cloudreve';
    const isOneImg = form.type === 'oneimg';

    const secrets: {
      username?: string;
      password?: string;
      token?: string;
      refresh_token?: string;
      refresh_expires?: string;
      access_expires?: string;
    } = {};
    if (isOneImg) {
      if (form.token.trim() !== '') secrets.token = form.token.trim();
    } else if (!isCloudreve) {
      if (form.username.trim() !== '') secrets.username = form.username.trim();
      if (form.password !== '') secrets.password = form.password;
    } else if (form.autoLogin) {
      if (form.username.trim() !== '') secrets.username = form.username.trim();
      if (form.password !== '') secrets.password = form.password;
    } else if (isEdit) {
      // 手动令牌模式不保存账号凭据; 空串通知后端删除旧凭据.
      secrets.username = '';
      secrets.password = '';
    }
    if (isCloudreve) {
      // 空串表示清除旧令牌: 修改地址或凭据后必须让后端删除已失效的 RefreshToken.
      secrets.refresh_token = form.cloudreveRefreshToken;
      secrets.refresh_expires = form.cloudreveRefreshExpires;
      secrets.access_expires = form.cloudreveAccessExpires;
    }

    const body: AdminPolicyWriteRequest = formURLFromBackend
      ? {
          name: form.name.trim(),
          hint: form.hint.trim(),
          type: form.type,
          enabled: isCloudreve ? form.cloudreveLoggedIn : form.enabled,
          path_template: isCloudreve ? form.pathTemplate.trim() : DEFAULT_PATH_TEMPLATE,
          conflict_policy: DEFAULT_CONFLICT_POLICY,
          reuse_released_seq: isCloudreve ? form.reuseReleasedSeq : false,
          config: isCloudreve
            ? {
                endpoint: form.endpoint.trim(),
                directory: form.directory.trim() || CLOUDREVE_DEFAULT_DIRECTORY,
                auto_login: form.autoLogin ? 'true' : 'false',
              }
            : isOneImg
              ? {
                  endpoint: form.endpoint.trim(),
                  ...(form.bucketId !== '' ? { bucket_id: form.bucketId } : {}),
                }
              : {},
          urls: [],
        }
      : {
          name: form.name.trim(),
          hint: form.hint.trim(),
          type: form.type,
          enabled: form.enabled,
          path_template: form.pathTemplate.trim(),
          conflict_policy: form.conflictPolicy,
          reuse_released_seq: form.reuseReleasedSeq,
          config: { endpoint: form.endpoint.trim() },
          urls: form.urls.map((row, index) => ({
            alias: row.alias.trim(),
            url: row.url.trim(),
            is_default: index === 0,
            sort: index + 1,
          })),
        };
    if ((!formURLFromBackend || isCloudreve || isOneImg) && Object.keys(secrets).length > 0) {
      body.secrets = secrets;
    }
    saveMutation.mutate(body);
  };

  /* -------------------------------------------------- 连接测试 / 扫描补录 */

  const testMutation = useMutation({
    mutationFn: (targetId: number) => adminPoliciesApi.test(targetId),
  });

  const applyCloudreveLogin = (result: CloudreveLoginResult): boolean => {
    if (
      result.status !== 'ok' ||
      result.refresh_token === undefined ||
      result.refresh_token === ''
    ) {
      toast.error(t('adminPolicies.edit.login.failed'));
      return false;
    }
    setForm((current) =>
      current === null
        ? current
        : {
            ...current,
            cloudreveLoggedIn: true,
            cloudreveRefreshToken: result.refresh_token ?? '',
            cloudreveRefreshExpires: result.refresh_expires ?? '',
            cloudreveAccessExpires: result.access_expires ?? '',
          },
    );
    return true;
  };

  const loginMutation = useMutation({
    mutationFn: () => {
      if (form === null) throw new Error(t('adminPolicies.edit.login.formNotReady'));
      return adminPoliciesApi.cloudreveLogin({
        endpoint: form.endpoint.trim(),
        email: form.username.trim(),
        password: form.password,
      });
    },
    onSuccess: (result) => {
      if (result.status === '2fa') {
        if (result.session_id === undefined || result.session_id === '') {
          toast.error(t('adminPolicies.edit.login.failed'));
          return;
        }
        setTwoFactorSessionID(result.session_id);
        setTwoFactorCode('');
        setTwoFactorError(null);
        setTwoFactorOpen(true);
        return;
      }
      if (applyCloudreveLogin(result)) {
        toast.success(t('adminPolicies.edit.login.success'));
      }
    },
  });

  const twoFactorMutation = useMutation({
    mutationFn: (vars: { sessionID: string; otp: string }) =>
      adminPoliciesApi.cloudreveTwoFactor({
        endpoint: form?.endpoint.trim() ?? '',
        session_id: vars.sessionID,
        otp: vars.otp,
      }),
    onSuccess: (result) => {
      if (!applyCloudreveLogin(result)) return;
      setTwoFactorOpen(false);
      setTwoFactorCode('');
      setTwoFactorError(null);
      toast.success(t('adminPolicies.edit.login.success'));
    },
    onError: (error) => {
      setTwoFactorError(errorText(error, t('adminPolicies.edit.login.twoFactorFailed')));
    },
  });

  const refreshTokenMutation = useMutation({
    mutationFn: () => {
      if (form === null) throw new Error(t('adminPolicies.edit.login.formNotReady'));
      return adminPoliciesApi.cloudreveRefresh({
        endpoint: form.endpoint.trim(),
        refresh_token: form.cloudreveRefreshToken.trim(),
      });
    },
    onSuccess: (result) => {
      if (!applyCloudreveLogin(result)) return;
      toast.success(t('adminPolicies.edit.login.refreshSuccess'));
    },
  });

  const validateRefreshToken = () => {
    if (form === null || form.autoLogin) return;
    if (form.endpoint.trim() === '' || form.cloudreveRefreshToken.trim() === '') return;
    refreshTokenMutation.mutate();
  };

  const updateRefreshToken = (value: string) => {
    refreshTokenMutation.reset();
    setForm((current) =>
      current === null
        ? current
        : {
            ...current,
            cloudreveLoggedIn: false,
            cloudreveRefreshToken: value,
            cloudreveRefreshExpires: '',
            cloudreveAccessExpires: '',
          },
    );
  };

  const scanMutation = useMutation({
    mutationFn: (vars: { targetId: number; body: PolicyScanRequest }) =>
      adminPoliciesApi.scan(vars.targetId, vars.body),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ['admin', 'tasks'] });
    },
  });
  const scanTaskId = scanMutation.data?.task_id;
  const scanTaskQuery = useQuery({
    queryKey: ['admin', 'policy-scan-task', numericId, scanTaskId],
    queryFn: () => adminPoliciesApi.scanStatus(numericId, scanTaskId!),
    enabled: isEdit && scanTaskId !== undefined,
    refetchInterval: (query) => {
      const status = query.state.data?.status;
      return status === 'pending' || status === 'running' ? 2000 : false;
    },
  });
  const scanTask = scanTaskQuery.data;
  const scanTaskStatus = scanTask?.status;
  const scanTaskActive =
    scanMutation.data !== undefined &&
    (scanTaskStatus === 'pending' ||
      scanTaskStatus === 'running' ||
      (scanTaskStatus === undefined && !scanTaskQuery.isError));
  const scanResult = scanResultFromTask(scanTask?.result);

  /* ------------------------------------------------------------ 派生数据 */

  const policy = policyQuery.data ?? null;
  const capabilities =
    policy?.capabilities ?? NEW_TYPE_TRAITS[form?.type ?? '']?.capabilities ?? [];
  const canStat = capabilities.includes('stat');

  const updateForm = useCallback((patch: Partial<FormState>) => {
    setForm((current) => (current === null ? current : { ...current, ...patch }));
  }, []);
  const updateCloudreveCredentials = useCallback((patch: Partial<FormState>) => {
    setForm((current) =>
      current === null
        ? current
        : {
            ...current,
            ...patch,
            cloudreveLoggedIn: false,
            cloudreveRefreshToken: '',
            cloudreveRefreshExpires: '',
            cloudreveAccessExpires: '',
          },
    );
  }, []);
  const driverDefaultHint = (type: string) => {
    const key = DEFAULT_HINT_KEYS[type];
    return key === undefined ? '' : t(key);
  };
  const resetHint = () => {
    setForm((current) =>
      current === null ? current : { ...current, hint: driverDefaultHint(current.type) },
    );
  };
  /** 新建策略切换后端: 仅在提示为空或仍是已知默认值时替换, 不覆盖自定义内容 */
  const handleTypeChange = (nextType: string) => {
    setForm((current) => {
      if (current === null) return current;
      const hint = current.hint.trim();
      const defaultHints = Object.keys(DEFAULT_HINT_KEYS).map((key) => driverDefaultHint(key));
      const replaceHint = hint === '' || defaultHints.includes(hint);
      let pathTemplate = current.pathTemplate;
      if (nextType === 'cloudreve') {
        pathTemplate = CLOUDREVE_DEFAULT_PATH_TEMPLATE;
      } else if (pathTemplate === CLOUDREVE_DEFAULT_PATH_TEMPLATE) {
        pathTemplate = DEFAULT_PATH_TEMPLATE;
      }
      return {
        ...current,
        type: nextType,
        hint: replaceHint ? driverDefaultHint(nextType) : current.hint,
        pathTemplate,
        directory: CLOUDREVE_DEFAULT_DIRECTORY,
        autoLogin: false,
        cloudreveLoggedIn: false,
        cloudreveRefreshToken: '',
        cloudreveRefreshExpires: '',
        cloudreveAccessExpires: '',
      };
    });
  };
  const moveUrl = useCallback((sourceKey: string, targetKey: string) => {
    setForm((current) =>
      current === null
        ? current
        : { ...current, urls: moveUrlRow(current.urls, sourceKey, targetKey) },
    );
  }, []);

  const exportJson = useMemo(
    () =>
      form === null
        ? ''
        : JSON.stringify(
            buildPolicyTransferPayload({
              name: form.name,
              hint: form.hint,
              type: form.type,
              enabled: form.enabled,
              endpoint: form.endpoint,
              token: form.token,
              bucketId: form.bucketId,
              directory: form.directory,
              autoLogin: form.autoLogin,
              username: form.username,
              password: form.password,
              cloudreveRefreshToken: form.cloudreveRefreshToken,
              cloudreveRefreshExpires: form.cloudreveRefreshExpires,
              cloudreveAccessExpires: form.cloudreveAccessExpires,
              pathTemplate: form.pathTemplate,
              conflictPolicy: form.conflictPolicy,
              reuseReleasedSeq: form.reuseReleasedSeq,
              urls: form.urls,
            }),
            null,
            2,
          ),
    [form],
  );

  const transferErrorText = (error: PolicyTransferError): string =>
    t(`adminPolicies.edit.transfer.error.${error.key}`, {
      detail: error.detail ?? '',
      type: error.detail ?? '',
      field: error.field ?? '',
    });

  /** 导入为表单补丁: 只覆盖 JSON 中出现的字段; 编辑模式后端类型不可修改 */
  const applyImport = (raw: string): string | null => {
    let patch: PolicyTransferResult;
    try {
      patch = parsePolicyTransfer(raw, TYPE_OPTIONS);
    } catch (error) {
      if (error instanceof PolicyTransferError) return transferErrorText(error);
      return transferErrorText(new PolicyTransferError('invalidJson'));
    }

    // URL 行在渲染时需要稳定的 key, 不能在 setState 更新函数里生成
    const urls =
      patch.urls === undefined
        ? undefined
        : patch.urls.map((row) => ({ key: nextUrlKey(), alias: row.alias, url: row.url }));
    const nextType = isNew ? patch.type : undefined;

    setForm((current) => {
      if (current === null) return current;
      const next: FormState = { ...current };
      if (patch.name !== undefined) next.name = patch.name;
      if (patch.hint !== undefined) next.hint = patch.hint;
      if (nextType !== undefined) next.type = nextType;
      if (patch.enabled !== undefined) next.enabled = patch.enabled;
      if (patch.directory !== undefined) next.directory = patch.directory;
      if (patch.endpoint !== undefined) {
        next.endpoint = patch.endpoint;
        next.cloudreveLoggedIn = false;
        next.cloudreveRefreshToken = '';
        next.cloudreveRefreshExpires = '';
        next.cloudreveAccessExpires = '';
      }
      if (patch.token !== undefined) next.token = patch.token;
      if (patch.bucketId !== undefined) next.bucketId = patch.bucketId;
      if (patch.directory !== undefined) next.directory = patch.directory;
      if (patch.autoLogin !== undefined) next.autoLogin = patch.autoLogin;
      if (patch.username !== undefined) {
        next.username = patch.username;
        next.cloudreveLoggedIn = false;
        next.cloudreveRefreshToken = '';
        next.cloudreveRefreshExpires = '';
        next.cloudreveAccessExpires = '';
      }
      if (patch.password !== undefined) {
        next.password = patch.password;
        next.cloudreveLoggedIn = false;
        next.cloudreveRefreshToken = '';
        next.cloudreveRefreshExpires = '';
        next.cloudreveAccessExpires = '';
      }
      if (patch.cloudreveRefreshToken !== undefined) {
        next.cloudreveRefreshToken = patch.cloudreveRefreshToken;
        next.cloudreveLoggedIn = patch.cloudreveRefreshToken !== '';
      }
      if (patch.cloudreveRefreshExpires !== undefined) {
        next.cloudreveRefreshExpires = patch.cloudreveRefreshExpires;
      }
      if (patch.cloudreveAccessExpires !== undefined) {
        next.cloudreveAccessExpires = patch.cloudreveAccessExpires;
      }
      if (patch.pathTemplate !== undefined) next.pathTemplate = patch.pathTemplate;
      if (patch.conflictPolicy !== undefined) next.conflictPolicy = patch.conflictPolicy;
      if (patch.reuseReleasedSeq !== undefined) next.reuseReleasedSeq = patch.reuseReleasedSeq;
      if (urls !== undefined) next.urls = urls;
      return next;
    });
    setSubmitted(false);
    setUrlTouched(false);
    toast.success(t('adminPolicies.edit.transfer.importDone'));
    return null;
  };

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
            : variable === '{seq}'
              ? t('adminPolicies.edit.variable.seq')
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
          {`${label} - ${t('adminPolicies.edit.variable.insert', { variable })}`}
        </TooltipContent>
      </Tooltip>
    );
  };

  const referenceUrls = useMemo(() => {
    const relPath = preview?.rel_path;
    if (relPath === undefined || relPath === '') return [];
    return (
      form?.urls.map((row, index) => ({
        key: row.key,
        alias: row.alias.trim() === '' ? t('common.defaultAlias') : row.alias.trim(),
        url: `${row.url.trim().replace(/\/+$/, '')}/${relPath}`,
        is_default: index === 0,
      })) ?? []
    );
  }, [form?.urls, preview?.rel_path, t]);

  // 冲突策略示例只使用列表首项 (默认域名)
  const defaultDomainUrl = form?.urls[0]?.url.trim().replace(/\/+$/, '') ?? '';

  /* ---------------------------------------------------------------- 渲染 */

  if (invalidId) {
    return (
      <div>
        <PageHeader title={t('adminPolicies.list.title')} />
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
        <PageHeader title={t('adminPolicies.list.title')} />
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
          description={isNew ? t('adminPolicies.edit.descriptionNew') : undefined}
          actions={
            <>
              <Skeleton className="h-9 w-20 rounded-lg" />
              <Skeleton className="h-9 w-16 rounded-lg" />
              <Skeleton className="h-9 w-16 rounded-lg" />
              <Skeleton className="h-9 w-20 rounded-lg" />
            </>
          }
        />
        <PolicyEditorSkeleton tabs={visibleTabs.length} />
      </div>
    );
  }

  const isCloudreve = form.type === 'cloudreve';
  const isOneImg = form.type === 'oneimg';

  const credentialFields = (
    <>
      <FormField
        id="policy-username"
        label={
          isCloudreve
            ? t('adminPolicies.edit.field.cloudreveEmail')
            : t('adminPolicies.edit.field.username')
        }
        error={errorFor('username')}
        hint={isCloudreve ? t('adminPolicies.edit.field.cloudreveEmailHint') : undefined}
      >
        <Input
          id="policy-username"
          value={form.username}
          onChange={(event) =>
            isCloudreve
              ? updateCloudreveCredentials({ username: event.target.value })
              : updateForm({ username: event.target.value })
          }
          autoComplete={isCloudreve ? 'username' : 'off'}
        />
      </FormField>
      <FormField
        id="policy-password"
        label={t('adminPolicies.edit.field.password')}
        error={errorFor('password')}
        hint={
          isCloudreve
            ? t('adminPolicies.edit.field.cloudrevePasswordHint')
            : t('adminPolicies.edit.field.passwordHint')
        }
      >
        <div className="relative">
          <Input
            id="policy-password"
            type={showPassword ? 'text' : 'password'}
            value={form.password}
            onChange={(event) =>
              isCloudreve
                ? updateCloudreveCredentials({ password: event.target.value })
                : updateForm({ password: event.target.value })
            }
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
    </>
  );

  return (
    <form onSubmit={handleSubmit} noValidate>
      <PageHeader
        title={
          isNew
            ? t('adminPolicies.edit.titleNew')
            : t('adminPolicies.edit.titleEdit', { name: form.name })
        }
        description={
          isNew
            ? formURLFromBackend
              ? t('adminPolicies.edit.descriptionNewUrlBackend')
              : t('adminPolicies.edit.descriptionNew')
            : undefined
        }
        actions={
          <>
            <Button type="button" variant="ghost" onClick={() => navigate('/admin/policies')}>
              <ArrowLeft className="size-4" />
              {t('adminPolicies.edit.back')}
            </Button>
            <Button type="button" variant="outline" onClick={() => setImportOpen(true)}>
              <Upload className="size-4" />
              {t('adminPolicies.edit.transfer.import')}
            </Button>
            <Button type="button" variant="outline" onClick={() => setExportOpen(true)}>
              <Download className="size-4" />
              {t('adminPolicies.edit.transfer.export')}
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

      <Tabs value={tab} onValueChange={handleTabChange} className="flex-col gap-0">
        <TabsList
          variant="line"
          className="group-data-horizontal/tabs:h-auto border-border w-full flex-wrap justify-start rounded-none border-b p-0 pb-[5px]"
        >
          {visibleTabs.map((item) => (
            <TabsTrigger
              key={item.id}
              value={item.id}
              className="data-[pointer-focus=true]:focus-visible:border-transparent data-[pointer-focus=true]:focus-visible:ring-0 data-[pointer-focus=true]:focus-visible:outline-none h-9 flex-none gap-2 px-3"
              onPointerDown={(event) => {
                // Radix 在 mousedown 中主动 focus, 刷新后首次点击会被判定为 focus-visible;
                // 仅对指针操作隐藏焦点环, 键盘导航仍然保留.
                event.currentTarget.setAttribute('data-pointer-focus', 'true');
              }}
              onKeyDown={(event) => {
                event.currentTarget.removeAttribute('data-pointer-focus');
              }}
              onBlur={(event) => {
                event.currentTarget.removeAttribute('data-pointer-focus');
              }}
            >
              {item.icon}
              {t(`adminPolicies.edit.section.${item.labelKey}`)}
            </TabsTrigger>
          ))}
        </TabsList>

        <TabsContent value="basic" className={TAB_PANEL_CLASS}>
          <PolicySection>
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
              <div className="flex flex-col gap-2">
                <FormField
                  id="policy-type"
                  label={
                    isEdit
                      ? t('adminPolicies.edit.field.typeEdit')
                      : t('adminPolicies.edit.field.type')
                  }
                  className="sm:w-[240px]"
                >
                  <Select value={form.type} onValueChange={handleTypeChange} disabled={isEdit}>
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
                {urlBackendHint !== '' && (
                  <p className="text-muted-foreground text-sm">{urlBackendHint}</p>
                )}
              </div>
              <FormField
                id="policy-hint"
                label={t('adminPolicies.edit.field.hint')}
                labelAction={
                  DEFAULT_HINT_KEYS[form.type] === undefined ? undefined : (
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      className="h-7 px-2 text-xs"
                      onClick={resetHint}
                      disabled={form.hint.trim() === driverDefaultHint(form.type)}
                      title={t('adminPolicies.edit.field.hintResetHint')}
                    >
                      <RotateCcw className="size-3.5" />
                      {t('adminPolicies.edit.field.hintReset')}
                    </Button>
                  )
                }
                hint={t('adminPolicies.edit.field.hintHint')}
                error={errorFor('hint')}
                className="sm:max-w-xl"
              >
                <Textarea
                  id="policy-hint"
                  value={form.hint}
                  onChange={(event) => updateForm({ hint: event.target.value })}
                  rows={3}
                  maxLength={500}
                  aria-invalid={errorFor('hint') !== undefined}
                />
              </FormField>
              <Separator />
              <div className="flex flex-wrap items-center gap-1">
                <p className="text-muted-foreground mr-2 text-sm">
                  {t('adminPolicies.edit.field.capabilities')}
                </p>
                {capabilities.map((capability) => (
                  <Badge key={capability} variant="outline">
                    {t(`adminPolicies.capability.${capability}`, { defaultValue: capability })}
                  </Badge>
                ))}
              </div>
            </div>
          </PolicySection>
        </TabsContent>

        <TabsContent value="connection" className={TAB_PANEL_CLASS}>
          <PolicySection
            actions={
              <>
                {!isCloudreve &&
                  (isEdit ? (
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
                  ))}
              </>
            }
          >
            <div className="flex flex-col gap-5">
              <FormField
                id="policy-endpoint"
                label={t('adminPolicies.edit.field.endpoint')}
                error={errorFor('endpoint')}
                hint={
                  isCloudreve
                    ? t('adminPolicies.edit.field.cloudreveEndpointHint')
                    : isOneImg
                      ? t('adminPolicies.edit.field.oneimgEndpointHint')
                      : t('adminPolicies.edit.field.endpointHint')
                }
              >
                <Input
                  id="policy-endpoint"
                  value={form.endpoint}
                  onChange={(event) =>
                    isCloudreve
                      ? updateCloudreveCredentials({ endpoint: event.target.value })
                      : updateForm({
                          endpoint: event.target.value,
                          ...(isOneImg ? { bucketId: '' } : {}),
                        })
                  }
                  placeholder={
                    isCloudreve
                      ? t('adminPolicies.edit.field.cloudreveEndpointPlaceholder')
                      : isOneImg
                        ? t('adminPolicies.edit.field.oneimgEndpointPlaceholder')
                        : t('adminPolicies.edit.field.endpointPlaceholder')
                  }
                  aria-invalid={errorFor('endpoint') !== undefined}
                  required
                />
              </FormField>
              {isCloudreve && (
                <>
                  <FormField
                    id="policy-directory"
                    label={t('adminPolicies.edit.field.directory')}
                    hint={t('adminPolicies.edit.field.directoryHint')}
                  >
                    <Input
                      id="policy-directory"
                      value={form.directory}
                      onChange={(event) => updateForm({ directory: event.target.value })}
                      placeholder={CLOUDREVE_DEFAULT_DIRECTORY}
                      className="font-mono"
                      required
                    />
                  </FormField>
                  <div className="flex flex-col rounded-lg border p-4">
                    <SwitchRow
                      id="policy-auto-login"
                      label={t('adminPolicies.edit.field.autoLogin')}
                      hint={t('adminPolicies.edit.field.autoLoginHint')}
                      checked={form.autoLogin}
                      onCheckedChange={(checked) => {
                        refreshTokenMutation.reset();
                        updateForm(
                          checked
                            ? {
                                autoLogin: true,
                                cloudreveLoggedIn: false,
                                cloudreveRefreshToken: '',
                                cloudreveRefreshExpires: '',
                                cloudreveAccessExpires: '',
                              }
                            : { autoLogin: false },
                        );
                      }}
                    />
                    <div
                      className={cn(
                        'grid transition-[grid-template-rows,opacity] duration-300 ease-out',
                        form.autoLogin
                          ? 'grid-rows-[1fr] opacity-100'
                          : 'grid-rows-[0fr] opacity-0',
                      )}
                      aria-hidden={!form.autoLogin}
                      inert={!form.autoLogin}
                    >
                      <div className="min-h-0 overflow-hidden">
                        <div className="flex flex-col gap-4 pt-4">
                          <Alert className="border-transparent bg-warning/10 text-warning">
                            <AlertDescription>
                              {t('adminPolicies.edit.field.cloudreveCaptchaHint')}
                            </AlertDescription>
                          </Alert>
                          {loginMutation.isError && (
                            <Alert variant="destructive">
                              <AlertDescription>
                                {errorText(
                                  loginMutation.error,
                                  t('adminPolicies.edit.login.failed'),
                                )}
                              </AlertDescription>
                            </Alert>
                          )}
                          {credentialFields}
                          <div className="flex justify-end">
                            <Button
                              type="button"
                              variant="outline"
                              size="sm"
                              disabled={loginMutation.isPending}
                              onClick={() => loginMutation.mutate()}
                            >
                              {loginMutation.isPending
                                ? t('adminPolicies.edit.login.running')
                                : form.cloudreveLoggedIn
                                  ? t('adminPolicies.edit.login.relogin')
                                  : t('adminPolicies.edit.login.button')}
                            </Button>
                          </div>
                        </div>
                      </div>
                    </div>
                  </div>
                </>
              )}
              {isOneImg && (
                <FormField
                  id="policy-token"
                  label={t('adminPolicies.edit.field.oneimgToken')}
                  error={errorFor('token')}
                  hint={t('adminPolicies.edit.field.oneimgTokenHint')}
                >
                  <Input
                    id="policy-token"
                    type="password"
                    value={form.token}
                    onChange={(event) => updateForm({ token: event.target.value, bucketId: '' })}
                    autoComplete="off"
                    spellCheck={false}
                  />
                </FormField>
              )}
              {isOneImg && (
                <FormField
                  id="policy-bucket"
                  label={t('adminPolicies.edit.field.bucket')}
                  hint={
                    bucketsQuery.isError
                      ? errorText(
                          bucketsQuery.error,
                          t('adminPolicies.edit.field.bucketLoadFailed'),
                        )
                      : t('adminPolicies.edit.field.bucketHint')
                  }
                >
                  <Select
                    value={form.bucketId === '' ? 'default' : form.bucketId}
                    onOpenChange={(open) => {
                      if (!open) return;
                      setBucketMenuOpened(true);
                      if (bucketsQuery.isError) void bucketsQuery.refetch();
                    }}
                    onValueChange={(value) =>
                      updateForm({ bucketId: value === 'default' ? '' : value })
                    }
                  >
                    <SelectTrigger id="policy-bucket" className="w-full">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="default">
                        {t('adminPolicies.edit.field.bucketDefault')}
                      </SelectItem>
                      {bucketsQuery.isFetching && (
                        <SelectItem value="__loading" disabled>
                          {t('adminPolicies.edit.field.bucketLoading')}
                        </SelectItem>
                      )}
                      {bucketsQuery.data?.map((bucket) => (
                        <SelectItem key={bucket.id} value={String(bucket.id)}>
                          {bucket.name}
                          {bucket.type !== '' ? ` (${bucket.type})` : ''}
                        </SelectItem>
                      ))}
                      {form.bucketId !== '' &&
                        bucketsQuery.data !== undefined &&
                        !bucketsQuery.data.some(
                          (bucket) => String(bucket.id) === form.bucketId,
                        ) && (
                          <SelectItem value={form.bucketId}>
                            {t('adminPolicies.edit.field.bucketMissing', { id: form.bucketId })}
                          </SelectItem>
                        )}
                    </SelectContent>
                  </Select>
                </FormField>
              )}
              {!isCloudreve && !isOneImg && credentialFields}
              {isCloudreve && (
                <FormField
                  id="policy-refresh-token"
                  label={t('adminPolicies.edit.field.refreshToken')}
                  error={
                    refreshTokenMutation.isError
                      ? errorText(
                          refreshTokenMutation.error,
                          t('adminPolicies.edit.login.refreshFailed'),
                        )
                      : undefined
                  }
                >
                  <div className="relative">
                    <Input
                      id="policy-refresh-token"
                      type={showRefreshToken ? 'text' : 'password'}
                      value={form.cloudreveRefreshToken}
                      readOnly={form.autoLogin}
                      onChange={(event) => {
                        if (!form.autoLogin) updateRefreshToken(event.target.value);
                      }}
                      onBlur={validateRefreshToken}
                      autoComplete="off"
                      spellCheck={false}
                      className={cn('pr-9', form.autoLogin && 'bg-muted')}
                    />
                    <button
                      type="button"
                      aria-label={t(
                        showRefreshToken
                          ? 'adminPolicies.edit.field.refreshTokenHide'
                          : 'adminPolicies.edit.field.refreshTokenShow',
                      )}
                      className="text-muted-foreground hover:text-foreground absolute top-1/2 right-2 -translate-y-1/2 transition-colors"
                      onClick={() => setShowRefreshToken((current) => !current)}
                    >
                      {showRefreshToken ? (
                        <EyeOff className="size-4" />
                      ) : (
                        <Eye className="size-4" />
                      )}
                    </button>
                  </div>
                  {(form.cloudreveAccessExpires !== '' || form.cloudreveRefreshExpires !== '') && (
                    <div className="text-muted-foreground flex flex-wrap gap-x-4 gap-y-1 text-xs">
                      {form.cloudreveAccessExpires !== '' && (
                        <span>
                          {t('adminPolicies.edit.field.accessTokenExpires', {
                            time: formatDateTime(form.cloudreveAccessExpires),
                          })}
                        </span>
                      )}
                      {form.cloudreveRefreshExpires !== '' && (
                        <span>
                          {t('adminPolicies.edit.field.refreshTokenExpires', {
                            time: formatDateTime(form.cloudreveRefreshExpires),
                          })}
                        </span>
                      )}
                    </div>
                  )}
                </FormField>
              )}
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
              {isCloudreve &&
                (isEdit ? (
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    className="self-end"
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
                ))}
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

              <SwitchRow
                id="policy-reuse-released-seq"
                label={t('adminPolicies.edit.field.reuseReleasedSeq')}
                hint={t('adminPolicies.edit.field.reuseReleasedSeqHint')}
                checked={form.reuseReleasedSeq}
                onCheckedChange={(checked) => updateForm({ reuseReleasedSeq: checked })}
              />

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
                  {!formURLFromBackend && (
                    <>
                      <Separator />
                      <UrlReferenceList
                        title={t('adminPolicies.edit.preview.urls')}
                        items={referenceUrls}
                        emptyText={t('adminPolicies.edit.reference.emptyUrls')}
                      />
                    </>
                  )}
                </div>
              )}
            </div>
          </PolicySection>
        </TabsContent>

        <TabsContent value="urls" className={TAB_PANEL_CLASS}>
          <PolicySection>
            <div className="flex flex-col gap-4">
              <div className="grid w-full grid-cols-1 items-center gap-2 sm:grid-cols-[2rem_minmax(0,220px)_minmax(0,1fr)_auto]">
                {form.urls.length > 0 ? (
                  <>
                    <span className="hidden sm:block" aria-hidden="true" />
                    <Label
                      className="sm:col-start-2"
                      htmlFor={`url-alias-${form.urls[0]?.key ?? ''}`}
                    >
                      {t('adminPolicies.edit.url.aliasLabel')}
                    </Label>
                    <Label
                      className="sm:col-start-3"
                      htmlFor={`url-value-${form.urls[0]?.key ?? ''}`}
                    >
                      {t('adminPolicies.edit.url.urlLabel')}
                    </Label>
                  </>
                ) : (
                  <span className="hidden sm:col-span-3 sm:block" aria-hidden="true" />
                )}
                <Button
                  type="button"
                  size="sm"
                  className="w-fit justify-self-end sm:col-start-4"
                  onClick={() =>
                    updateForm({
                      urls: [...form.urls, { key: nextUrlKey(), alias: '', url: '' }],
                    })
                  }
                >
                  <Plus className="size-4" />
                  {t('adminPolicies.edit.url.add')}
                </Button>
              </div>
              {form.urls.length === 0 && (
                <Alert className="border-transparent bg-info/10 text-info">
                  <AlertDescription>{t('adminPolicies.edit.url.empty')}</AlertDescription>
                </Alert>
              )}

              {form.urls.length > 0 && (
                <div className="flex w-full flex-col gap-3">
                  {form.urls.map((row, index) => {
                    const aliasError = errorFor(`urls.${index}.alias`);
                    const urlError = errorFor(`urls.${index}.url`);

                    return (
                      <div
                        key={row.key}
                        className={cn(
                          'grid min-w-0 grid-cols-1 items-center gap-2 sm:grid-cols-[2rem_minmax(0,220px)_minmax(0,1fr)_auto]',
                          draggedUrlKey === row.key && 'opacity-50',
                        )}
                        onDragOver={(event) => {
                          if (draggedUrlKey === null || draggedUrlKey === row.key) return;
                          event.preventDefault();
                          event.dataTransfer.dropEffect = 'move';
                        }}
                        onDrop={(event) => {
                          event.preventDefault();
                          const sourceKey =
                            event.dataTransfer.getData('text/plain') || draggedUrlKey;
                          if (sourceKey !== null) moveUrl(sourceKey, row.key);
                          setDraggedUrlKey(null);
                        }}
                      >
                        <Tooltip>
                          <TooltipTrigger asChild>
                            <Button
                              type="button"
                              variant="ghost"
                              size="icon-sm"
                              className="cursor-grab active:cursor-grabbing"
                              aria-label={t('adminPolicies.edit.url.dragHandle')}
                              draggable
                              onDragStart={(event) => {
                                event.dataTransfer.effectAllowed = 'move';
                                event.dataTransfer.setData('text/plain', row.key);
                                setDraggedUrlKey(row.key);
                              }}
                              onDragEnd={() => setDraggedUrlKey(null)}
                              onKeyDown={(event) => {
                                if (event.key !== 'ArrowUp' && event.key !== 'ArrowDown') return;
                                event.preventDefault();
                                const targetIndex = index + (event.key === 'ArrowUp' ? -1 : 1);
                                const target = form.urls[targetIndex];
                                if (target !== undefined) moveUrl(row.key, target.key);
                              }}
                            >
                              <GripVertical className="size-4" />
                            </Button>
                          </TooltipTrigger>
                          <TooltipContent>{t('adminPolicies.edit.url.dragHandle')}</TooltipContent>
                        </Tooltip>
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
                          aria-label={t('adminPolicies.edit.url.aliasLabel')}
                          aria-invalid={aliasError !== undefined}
                        />
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
                          aria-label={t('adminPolicies.edit.url.urlLabel')}
                          aria-invalid={urlError !== undefined}
                        />
                        <div className="flex shrink-0 items-center justify-end gap-1">
                          <Tooltip>
                            <TooltipTrigger asChild>
                              <Button
                                type="button"
                                variant="ghost"
                                size="icon-sm"
                                className="text-destructive"
                                aria-label={t('adminPolicies.edit.url.remove')}
                                onClick={() => {
                                  updateForm({
                                    urls: form.urls.filter((item) => item.key !== row.key),
                                  });
                                }}
                              >
                                <Trash2 className="size-4" />
                              </Button>
                            </TooltipTrigger>
                            <TooltipContent>{t('adminPolicies.edit.url.remove')}</TooltipContent>
                          </Tooltip>
                        </div>
                        {aliasError !== undefined && (
                          <p className="text-destructive text-sm sm:col-start-2">{aliasError}</p>
                        )}
                        {urlError !== undefined && (
                          <p className="text-destructive text-sm sm:col-start-3">{urlError}</p>
                        )}
                      </div>
                    );
                  })}
                </div>
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
                  <SelectTrigger
                    id="policy-conflict-policy"
                    className="w-full"
                    aria-invalid={errorFor('conflictPolicy') !== undefined}
                  >
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {CONFLICT_OPTIONS.map((option) => (
                      <SelectItem
                        key={option}
                        value={option}
                        disabled={option === 'skip' && sequenceTemplate}
                      >
                        {t(`adminPolicies.conflict.${option}`)}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                {errorFor('conflictPolicy') !== undefined && (
                  <p className="text-destructive text-sm">{errorFor('conflictPolicy')}</p>
                )}
              </div>
              <p className="text-muted-foreground text-sm">
                {t(`adminPolicies.conflict.${form.conflictPolicy}Hint`)}
              </p>
              {sequenceTemplate && errorFor('conflictPolicy') === undefined && (
                <p className="text-muted-foreground text-sm">
                  {t('adminPolicies.edit.conflictSeqHint')}
                </p>
              )}

              <Separator />
              <div className="flex flex-col gap-2">
                <p className="text-sm font-medium">
                  {t('adminPolicies.edit.conflictExample.title')}
                </p>
                {previewSkeleton ? (
                  <TextLinesSkeleton lines={2} />
                ) : preview === undefined ? (
                  <p className="text-muted-foreground text-sm">
                    {t('adminPolicies.edit.conflictExample.pending')}
                  </p>
                ) : defaultDomainUrl === '' ? (
                  <p className="text-muted-foreground text-sm">
                    {t('adminPolicies.edit.conflictExample.noDomain')}
                  </p>
                ) : (
                  <div className="flex flex-col gap-1.5 text-sm">
                    <div className="grid grid-cols-[auto_minmax(0,1fr)] items-start gap-x-3 gap-y-1.5">
                      <span className="text-muted-foreground text-center whitespace-nowrap">
                        {t('adminPolicies.edit.conflictExample.existing')}:
                      </span>
                      <span className="font-mono break-all">
                        {`${defaultDomainUrl}/${preview.rel_path}`}
                      </span>
                      {form.conflictPolicy === 'rename' && (
                        <>
                          <span className="text-muted-foreground text-center whitespace-nowrap">
                            {t('adminPolicies.edit.conflictExample.rename')}:
                          </span>
                          <span className="font-mono break-all">
                            {`${defaultDomainUrl}/${preview.conflict_rel_path}`}
                          </span>
                        </>
                      )}
                    </div>
                    {form.conflictPolicy !== 'rename' && (
                      <p className="text-muted-foreground">
                        {t('adminPolicies.edit.conflictExample.skip')}
                      </p>
                    )}
                  </div>
                )}
              </div>

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
                  disabled={scanMutation.isPending || scanTaskActive}
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
                    : scanTaskActive
                      ? t('adminPolicies.edit.scan.backgroundRunningButton')
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
                <SwitchRow
                  id="policy-scan-dry-run"
                  label={t('adminPolicies.edit.field.dryRun')}
                  hint={t('adminPolicies.edit.field.dryRunHint')}
                  checked={dryRun}
                  onCheckedChange={setDryRun}
                />
                <p className="text-muted-foreground text-xs">{t('adminPolicies.edit.scan.q21')}</p>
                {scanMutation.isError && (
                  <Alert variant="destructive">
                    <AlertDescription>
                      {errorText(scanMutation.error, t('adminPolicies.edit.scan.failed'))}
                    </AlertDescription>
                  </Alert>
                )}
                {scanMutation.data !== undefined && (
                  <Alert
                    variant={scanTaskStatus === 'failed' ? 'destructive' : 'default'}
                    className={
                      scanTaskStatus === 'failed'
                        ? undefined
                        : 'border-transparent bg-info/10 text-info'
                    }
                  >
                    <AlertDescription>
                      {scanTask === undefined ? (
                        <p className="text-sm">
                          {t('adminPolicies.edit.scan.submitted', {
                            id: scanMutation.data.task_id,
                          })}
                        </p>
                      ) : scanTaskStatus === 'running' ? (
                        <p className="text-sm">
                          {t('adminPolicies.edit.scan.processing', { id: scanTask.id })}
                        </p>
                      ) : scanTaskStatus === 'pending' ? (
                        <p className="text-sm">
                          {t('adminPolicies.edit.scan.submitted', { id: scanTask.id })}
                        </p>
                      ) : scanTaskStatus === 'failed' ? (
                        <p className="text-sm">
                          {t('adminPolicies.edit.scan.taskFailed', {
                            id: scanTask.id,
                            error: scanTask.last_error || t('adminPolicies.edit.scan.failed'),
                          })}
                        </p>
                      ) : scanTaskStatus === 'done' ? (
                        <>
                          <p className="text-sm">
                            {t('adminPolicies.edit.scan.done', { id: scanTask.id })}
                          </p>
                          {scanResult !== null && scanResult.failed.length > 0 && (
                            <div className="mt-2 flex flex-col gap-1">
                              <p className="text-sm font-semibold">
                                {t('adminPolicies.edit.scan.failedTitle', {
                                  count: scanResult.failed.length,
                                })}
                              </p>
                              {scanResult.failed.map((item) => (
                                <p
                                  key={`${item.path}-${item.error}`}
                                  className="font-mono text-xs break-all"
                                >
                                  {item.path} - {item.error}
                                </p>
                              ))}
                            </div>
                          )}
                        </>
                      ) : (
                        <p className="text-sm">
                          {t('adminPolicies.edit.scan.done', { id: scanTask.id })}
                        </p>
                      )}
                      {scanTaskQuery.isError && (
                        <p className="mt-2 text-sm">
                          {errorText(scanTaskQuery.error, t('adminPolicies.edit.scan.failed'))}
                        </p>
                      )}
                    </AlertDescription>
                  </Alert>
                )}
              </div>
            </PolicySection>
          </TabsContent>
        )}
      </Tabs>

      {/* 对照表 (保存结果): 仅基本信息 / 分发域名显示; 魔法路径已有实时预览 */}
      {(isEdit || savedOnce) && REFERENCE_TABS.includes(tab) && !formURLFromBackend && (
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
          </div>
        </PolicySection>
      )}

      <Dialog
        open={twoFactorOpen}
        onOpenChange={(open) => {
          setTwoFactorOpen(open);
          if (!open) {
            setTwoFactorCode('');
            setTwoFactorError(null);
          }
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('adminPolicies.edit.login.twoFactorTitle')}</DialogTitle>
          </DialogHeader>
          <div className="flex flex-col gap-3">
            <p className="text-muted-foreground text-sm">
              {t('adminPolicies.edit.login.twoFactorHint')}
            </p>
            <Input
              value={twoFactorCode}
              onChange={(event) => setTwoFactorCode(event.target.value)}
              inputMode="numeric"
              autoComplete="one-time-code"
              maxLength={6}
              autoFocus
              placeholder={t('adminPolicies.edit.login.twoFactorPlaceholder')}
              onKeyDown={(event) => {
                if (event.key === 'Enter' && !twoFactorMutation.isPending) {
                  event.preventDefault();
                  twoFactorMutation.mutate({
                    sessionID: twoFactorSessionID,
                    otp: twoFactorCode.trim(),
                  });
                }
              }}
            />
            {twoFactorError !== null && (
              <Alert variant="destructive">
                <AlertDescription>{twoFactorError}</AlertDescription>
              </Alert>
            )}
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => setTwoFactorOpen(false)}>
              {t('common.action.cancel')}
            </Button>
            <Button
              type="button"
              disabled={twoFactorMutation.isPending || twoFactorCode.trim() === ''}
              onClick={() =>
                twoFactorMutation.mutate({
                  sessionID: twoFactorSessionID,
                  otp: twoFactorCode.trim(),
                })
              }
            >
              {twoFactorMutation.isPending
                ? t('adminPolicies.edit.login.twoFactorRunning')
                : t('adminPolicies.edit.login.twoFactorSubmit')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {exportOpen && <PolicyExportDialog json={exportJson} onClose={() => setExportOpen(false)} />}
      {importOpen && (
        <PolicyImportDialog onImport={applyImport} onClose={() => setImportOpen(false)} />
      )}
    </form>
  );
}

function PolicyExportDialog({ json, onClose }: { json: string; onClose: () => void }) {
  const { t } = useTranslation();
  const [copied, setCopied] = useState(false);

  const handleCopy = async () => {
    if (!(await copyText(json))) {
      toast.error(t('common.action.copyFailed'));
      return;
    }
    setCopied(true);
    toast.success(t('common.action.copied'));
    window.setTimeout(() => setCopied(false), 1500);
  };

  return (
    <Dialog
      open
      onOpenChange={(next) => {
        if (!next) onClose();
      }}
    >
      <DialogContent className="sm:max-w-2xl" aria-describedby={undefined}>
        <DialogHeader>
          <DialogTitle>{t('adminPolicies.edit.transfer.exportTitle')}</DialogTitle>
        </DialogHeader>
        <p className="text-muted-foreground text-sm">
          {t('adminPolicies.edit.transfer.exportHint')}
        </p>
        <Textarea
          readOnly
          value={json}
          rows={16}
          className="field-sizing-fixed h-80 resize-none overflow-auto font-mono text-xs"
        />
        <DialogFooter>
          <Button type="button" variant="outline" onClick={onClose}>
            {t('common.action.close')}
          </Button>
          <Button type="button" onClick={() => void handleCopy()}>
            {copied ? <Check className="size-4" /> : <Download className="size-4" />}
            {t('adminPolicies.edit.transfer.copyJson')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function PolicyImportDialog({
  onImport,
  onClose,
}: {
  onImport: (raw: string) => string | null;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const [text, setText] = useState('');
  const [error, setError] = useState<string | null>(null);

  const handleImport = () => {
    const message = onImport(text);
    if (message !== null) {
      setError(message);
      return;
    }
    onClose();
  };

  return (
    <Dialog
      open
      onOpenChange={(next) => {
        if (!next) onClose();
      }}
    >
      <DialogContent className="sm:max-w-2xl" aria-describedby={undefined}>
        <DialogHeader>
          <DialogTitle>{t('adminPolicies.edit.transfer.importTitle')}</DialogTitle>
        </DialogHeader>
        <p className="text-muted-foreground text-sm">
          {t('adminPolicies.edit.transfer.importHint')}
        </p>
        <Textarea
          autoFocus
          value={text}
          rows={16}
          aria-label={t('adminPolicies.edit.transfer.placeholder')}
          placeholder={t('adminPolicies.edit.transfer.placeholder')}
          aria-invalid={error !== null}
          className="field-sizing-fixed h-80 resize-none overflow-auto font-mono text-xs"
          onChange={(event) => {
            setText(event.target.value);
            setError(null);
          }}
        />
        {error !== null && <p className="text-destructive text-sm">{error}</p>}
        <DialogFooter>
          <Button type="button" variant="outline" onClick={onClose}>
            {t('common.action.cancel')}
          </Button>
          <Button type="button" disabled={text.trim() === ''} onClick={handleImport}>
            <Upload className="size-4" />
            {t('adminPolicies.edit.transfer.apply')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
