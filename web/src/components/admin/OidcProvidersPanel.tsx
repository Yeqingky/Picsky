import { useState } from 'react';
import type { FormEvent, ReactNode } from 'react';
import { ChevronDown, LoaderCircle, Plus, Trash2, X } from 'lucide-react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import { useTranslation } from 'react-i18next';
import type { TFunction } from 'i18next';
import { adminOidcApi } from '../../api';
import type {
  AdminGroup,
  AdminOIDCProvider,
  AdminOIDCProviderAllocation,
  AdminOIDCProviderWriteRequest,
  OIDCGroupMapping,
} from '../../api';
import { CopyButton } from '../../components/CopyButton';
import { useConfirm } from '../../components/ConfirmDialog';
import { OidcProviderIcon } from '../../components/OidcProviderIcon';
import { SecretField } from '../../components/SecretField';
import { EmptyState, ErrorState } from '../../components/States';
import { SkeletonSurface } from '../../components/skeletons';
import { Alert, AlertDescription } from '../../components/ui/alert';
import { Button } from '../../components/ui/button';
import { Collapsible, CollapsibleContent } from '../../components/ui/collapsible';
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '../../components/ui/dialog';
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
import { Textarea } from '../../components/ui/textarea';
import { cn } from '../../lib/utils';

const PROVIDER_QUERY_KEY = ['admin', 'oidc', 'providers'] as const;

interface Draft {
  name: string;
  icon_url: string;
  discovery_url: string;
  discovery_document: string;
  client_id: string;
  scopes: string;
  allow_unbound: boolean;
  sub_path: string;
  email_path: string;
  name_path: string;
  picture_path: string;
  group_claim: string;
  group_mappings: OIDCGroupMapping[];
}

const DEFAULT_DRAFT: Draft = {
  name: '',
  icon_url: '',
  discovery_url: '',
  discovery_document: '',
  client_id: '',
  scopes: 'openid email profile',
  allow_unbound: false,
  sub_path: 'sub',
  email_path: 'email',
  name_path: 'name',
  picture_path: 'picture',
  group_claim: '',
  group_mappings: [],
};

function providerDraft(provider: AdminOIDCProvider | null): Draft {
  if (!provider) return { ...DEFAULT_DRAFT, group_mappings: [] };
  return {
    name: provider.name,
    icon_url: provider.icon_url,
    discovery_url: provider.discovery_url,
    discovery_document: provider.discovery_document,
    client_id: provider.client_id,
    scopes: provider.scopes,
    allow_unbound: provider.allow_unbound,
    sub_path: provider.sub_path,
    email_path: provider.email_path,
    name_path: provider.name_path,
    picture_path: provider.picture_path,
    group_claim: provider.group_claim,
    group_mappings: provider.group_mappings.map((mapping) => ({ ...mapping })),
  };
}

type DraftErrors = Partial<Record<'name' | 'discovery_document' | 'client_id' | 'scopes', string>>;

function validateDraft(draft: Draft, t: TFunction): DraftErrors {
  const errors: DraftErrors = {};
  if (draft.name.trim() === '') errors.name = t('adminOidc.form.validation.name');
  const document = draft.discovery_document.trim();
  if (document === '') {
    errors.discovery_document = t('adminOidc.form.validation.discoveryDocumentRequired');
  } else {
    try {
      const parsed = JSON.parse(document) as Record<string, unknown>;
      const required = ['issuer', 'authorization_endpoint', 'token_endpoint', 'jwks_uri'];
      if (required.some((key) => typeof parsed[key] !== 'string' || parsed[key] === '')) {
        errors.discovery_document = t('adminOidc.form.validation.discoveryDocumentFields');
      }
    } catch {
      errors.discovery_document = t('adminOidc.form.validation.discoveryDocumentInvalid');
    }
  }
  if (draft.client_id.trim() === '') errors.client_id = t('adminOidc.form.validation.clientId');
  const scopes = draft.scopes
    .split(/[\s,]+/)
    .map((token) => token.trim())
    .filter(Boolean);
  if (!scopes.includes('openid')) errors.scopes = t('adminOidc.form.validation.scope');
  return errors;
}

/** 表单字段: 标签 + 控件 + 帮助/错误文案 */
function Field({
  id,
  label,
  error,
  className,
  children,
}: {
  id: string;
  label: string;
  error?: string;
  className?: string;
  children: ReactNode;
}) {
  return (
    <div className={cn('flex flex-col gap-1.5', className)}>
      <Label htmlFor={id}>{label}</Label>
      {children}
      {error !== undefined && <p className="text-destructive text-xs">{error}</p>}
    </div>
  );
}

export function OidcProvidersPanel({
  groups,
  groupsError,
}: {
  groups: AdminGroup[];
  groupsError: boolean;
}) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const confirm = useConfirm();
  const [expanded, setExpanded] = useState<number | 'new' | null>(null);
  const [allocation, setAllocation] = useState<AdminOIDCProviderAllocation | null>(null);

  const providersQuery = useQuery({
    queryKey: PROVIDER_QUERY_KEY,
    queryFn: adminOidcApi.list,
    staleTime: Infinity,
    refetchOnWindowFocus: false,
    retry: false,
  });
  const allocateMutation = useMutation({
    mutationFn: adminOidcApi.allocate,
    onSuccess: (result) => {
      setAllocation(result);
      setExpanded('new');
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : t('common.state.failed'));
    },
  });

  const toggleMutation = useMutation({
    mutationFn: ({ id, enabled }: { id: number; enabled: boolean }) =>
      adminOidcApi.update(id, { enabled }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: PROVIDER_QUERY_KEY });
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : t('common.state.failed'));
    },
  });

  const removeMutation = useMutation({
    mutationFn: (id: number) => adminOidcApi.remove(id),
    onSuccess: async () => {
      toast.success(t('adminOidc.deleteSuccess'));
      await queryClient.invalidateQueries({ queryKey: PROVIDER_QUERY_KEY });
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : t('common.state.failed'));
    },
  });

  const handleDelete = async (provider: AdminOIDCProvider) => {
    const confirmed = await confirm({
      danger: true,
      title: t('adminOidc.deleteTitle', { name: provider.name }),
      description: t('adminOidc.deleteDescription'),
    });
    if (!confirmed) return;
    removeMutation.mutate(provider.id);
    if (expanded === provider.id) setExpanded(null);
  };

  const handleSaved = async () => {
    await queryClient.invalidateQueries({ queryKey: PROVIDER_QUERY_KEY });
    setExpanded(null);
    setAllocation(null);
  };

  const handleCancelNew = () => {
    setExpanded(null);
    setAllocation(null);
  };

  const handleAdd = () => {
    allocateMutation.mutate();
  };

  if (providersQuery.isError) {
    return (
      <ErrorState
        message={providersQuery.error instanceof Error ? providersQuery.error.message : undefined}
        onRetry={() => {
          void providersQuery.refetch();
        }}
      />
    );
  }

  if (providersQuery.isPending) {
    return (
      <div className="flex flex-col gap-2">
        {Array.from({ length: 3 }, (_, index) => (
          <SkeletonSurface key={index} label={t('common.skeleton.settings')}>
            <div className="border-border flex items-center gap-3 rounded-xl border p-4">
              <Skeleton className="size-8 rounded-lg" />
              <div className="flex flex-1 flex-col gap-2">
                <Skeleton className="h-4 w-32 rounded-sm" />
                <Skeleton className="h-3 w-24 rounded-sm" />
              </div>
              <Skeleton className="h-5 w-9 rounded-full" />
              <Skeleton className="size-7 rounded-lg" />
            </div>
          </SkeletonSurface>
        ))}
      </div>
    );
  }

  const providers = providersQuery.data ?? [];

  return (
    <div className="flex flex-col gap-2">
      {expanded === 'new' && allocation && (
        <ProviderEditor
          key="new"
          provider={null}
          allocation={allocation}
          groups={groups}
          groupsError={groupsError}
          open
          onOpenChange={(open) => {
            if (!open) handleCancelNew();
          }}
          onToggle={() => undefined}
          onDelete={handleCancelNew}
          onSaved={handleSaved}
          onCancel={handleCancelNew}
        />
      )}
      {providers.map((provider) => (
        <ProviderEditor
          key={provider.id}
          provider={provider}
          groups={groups}
          groupsError={groupsError}
          open={expanded === provider.id}
          onOpenChange={(open) => setExpanded(open ? provider.id : null)}
          onToggle={(enabled) => toggleMutation.mutate({ id: provider.id, enabled })}
          onDelete={() => void handleDelete(provider)}
          onSaved={handleSaved}
          onCancel={() => setExpanded(null)}
        />
      ))}
      {providers.length === 0 && expanded !== 'new' && (
        <EmptyState title={t('adminOidc.emptyTitle')} />
      )}
      <div className="flex justify-start pt-1">
        <Button
          variant="default"
          onClick={handleAdd}
          disabled={allocateMutation.isPending || expanded === 'new'}
        >
          {allocateMutation.isPending ? (
            <LoaderCircle className="size-4 animate-spin" />
          ) : (
            <Plus className="size-4" />
          )}
          {t('adminOidc.add')}
        </Button>
      </div>
    </div>
  );
}

function ProviderEditor({
  provider,
  allocation,
  groups,
  groupsError,
  open,
  onOpenChange,
  onToggle,
  onDelete,
  onSaved,
  onCancel,
}: {
  provider: AdminOIDCProvider | null;
  allocation?: AdminOIDCProviderAllocation;
  groups: AdminGroup[];
  groupsError: boolean;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onToggle: (enabled: boolean) => void;
  onDelete: () => void;
  onSaved: () => void;
  onCancel: () => void;
}) {
  const { t } = useTranslation();
  const isNew = provider === null;
  return (
    <Collapsible open={isNew ? true : open}>
      <div className="border-border rounded-xl border">
        <div
          className={cn(
            'flex items-center gap-3 rounded-t-xl p-4 transition-colors',
            !isNew && 'hover:bg-muted/30 cursor-pointer',
          )}
          onClick={isNew ? undefined : () => onOpenChange(!open)}
        >
          {isNew ? (
            <>
              <OidcProviderIcon url="" className="size-8" />
              <div className="min-w-0 flex-1">
                <p className="truncate text-sm font-semibold">{t('adminOidc.newTitle')}</p>
                <p className="text-muted-foreground text-xs">{t('adminOidc.subtitle')}</p>
              </div>
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                aria-label={t('common.action.cancel')}
                onClick={onCancel}
              >
                <X />
              </Button>
            </>
          ) : (
            <>
              <div className="flex min-w-0 flex-1 items-center gap-3">
                <OidcProviderIcon url={provider.icon_url} className="size-8" />
                <div className="min-w-0">
                  <p className="truncate text-sm font-semibold">{provider.name}</p>
                  <p className="text-muted-foreground text-xs">{t('adminOidc.subtitle')}</p>
                </div>
              </div>
              <div onClick={(event) => event.stopPropagation()}>
                <Switch
                  checked={provider.enabled}
                  onCheckedChange={onToggle}
                  aria-label={t('adminOidc.form.enabled')}
                />
              </div>
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                aria-label={t('common.action.delete')}
                onClick={(event) => {
                  event.stopPropagation();
                  onDelete();
                }}
              >
                <Trash2 className="text-destructive" />
              </Button>
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                aria-label={open ? t('adminOidc.form.collapse') : t('adminOidc.form.expand')}
                onClick={(event) => {
                  event.stopPropagation();
                  onOpenChange(!open);
                }}
              >
                <ChevronDown className={cn('size-4 transition-transform', open && 'rotate-180')} />
              </Button>
            </>
          )}
        </div>
        <CollapsibleContent className="border-border border-t p-4">
          <ProviderForm
            provider={provider}
            allocation={allocation}
            groups={groups}
            groupsError={groupsError}
            onSaved={onSaved}
          />
        </CollapsibleContent>
      </div>
    </Collapsible>
  );
}

function ProviderForm({
  provider,
  allocation,
  groups,
  groupsError,
  onSaved,
}: {
  provider: AdminOIDCProvider | null;
  allocation?: AdminOIDCProviderAllocation;
  groups: AdminGroup[];
  groupsError: boolean;
  onSaved: () => void;
}) {
  const { t } = useTranslation();
  const [draft, setDraft] = useState<Draft>(() => providerDraft(provider));
  const [secret, setSecret] = useState('');
  const [secretTouched, setSecretTouched] = useState(false);
  const [clearSecret, setClearSecret] = useState(false);
  const [secretSet, setSecretSet] = useState(provider?.client_secret_set ?? false);
  const [errors, setErrors] = useState<DraftErrors>({});
  const [importOpen, setImportOpen] = useState(false);
  const [importURL, setImportURL] = useState('');
  const [importError, setImportError] = useState<string | null>(null);

  const isNew = provider === null;
  const redirectURI = provider?.redirect_uri ?? allocation?.redirect_uri ?? '';
  const initial = providerDraft(provider);
  const dirty =
    isNew ||
    JSON.stringify(draft) !== JSON.stringify(initial) ||
    (secretTouched && secret !== '') ||
    clearSecret;

  const saveMutation = useMutation({
    mutationFn: () => {
      const body: AdminOIDCProviderWriteRequest = {
        name: draft.name.trim(),
        icon_url: draft.icon_url.trim(),
        discovery_url: draft.discovery_url.trim(),
        discovery_document: draft.discovery_document.trim(),
        client_id: draft.client_id.trim(),
        scopes: draft.scopes.trim(),
        enabled: provider?.enabled ?? false,
        allow_unbound: draft.allow_unbound,
        sub_path: draft.sub_path.trim(),
        email_path: draft.email_path.trim(),
        name_path: draft.name_path.trim(),
        picture_path: draft.picture_path.trim(),
        group_claim: draft.group_claim.trim(),
        group_mappings: draft.group_mappings,
      };
      if (isNew && allocation) {
        body.id = allocation.provider_id;
        body.allocation_token = allocation.allocation_token;
      }
      if (secretTouched && secret !== '') body.client_secret = secret;
      if (clearSecret) body.clear_client_secret = true;
      return provider ? adminOidcApi.update(provider.id, body) : adminOidcApi.create(body);
    },
    onSuccess: (result) => {
      toast.success(isNew ? t('adminOidc.createSuccess') : t('adminOidc.saveSuccess'));
      setSecret('');
      setSecretTouched(false);
      setClearSecret(false);
      setSecretSet(result.client_secret_set);
      onSaved();
    },
  });

  const importMutation = useMutation({
    mutationFn: (url: string) => adminOidcApi.importDiscovery(url),
    onSuccess: (result) => {
      setField('discovery_document', result.discovery_document);
      setField('discovery_url', result.discovery_url);
      setImportOpen(false);
      setImportURL('');
      setImportError(null);
    },
    onError: (error) => {
      setImportError(error instanceof Error ? error.message : t('common.state.failed'));
    },
  });

  const handleImport = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const url = importURL.trim();
    if (url === '') {
      setImportError(t('adminOidc.form.importUrlRequired'));
      return;
    }
    setImportError(null);
    importMutation.mutate(url);
  };

  const setField = <K extends keyof Draft>(key: K, value: Draft[K]) => {
    setDraft((prev) => ({ ...prev, [key]: value }));
    setErrors((prev) => {
      if (!(key in prev)) return prev;
      const next = { ...prev };
      delete next[key as keyof DraftErrors];
      return next;
    });
  };

  const reset = () => {
    setDraft(providerDraft(provider));
    setSecret('');
    setSecretTouched(false);
    setClearSecret(false);
    setSecretSet(provider?.client_secret_set ?? false);
    setErrors({});
  };

  const submit = () => {
    const nextErrors = validateDraft(draft, t);
    setErrors(nextErrors);
    if (Object.keys(nextErrors).length > 0) return;
    saveMutation.mutate();
  };

  const updateMapping = (index: number, patch: Partial<OIDCGroupMapping>) => {
    setDraft((prev) => ({
      ...prev,
      group_mappings: prev.group_mappings.map((mapping, current) =>
        current === index ? { ...mapping, ...patch } : mapping,
      ),
    }));
  };

  const addMapping = () => {
    const group = groups[0];
    if (!group) return;
    setDraft((prev) => ({
      ...prev,
      group_mappings: [...prev.group_mappings, { value: '', group_id: group.id }],
    }));
  };

  return (
    <>
      <form
        className="flex flex-col gap-5"
        onSubmit={(event) => {
          event.preventDefault();
          submit();
        }}
        noValidate
      >
        <Field id="oidc-redirect-uri" label={t('adminOidc.form.redirectUri')}>
          <div className="flex items-center gap-1.5">
            <Input
              id="oidc-redirect-uri"
              value={redirectURI}
              readOnly
              className="min-w-0 flex-1 font-mono text-xs"
            />
            <CopyButton value={redirectURI} size="icon-sm" disabled={redirectURI === ''} />
          </div>
        </Field>

        {saveMutation.isError && (
          <Alert variant="destructive">
            <AlertDescription>
              {saveMutation.error instanceof Error
                ? saveMutation.error.message
                : t('common.state.failed')}
            </AlertDescription>
          </Alert>
        )}

        <Field id="oidc-name" label={t('adminOidc.form.name')} error={errors.name}>
          <Input
            id="oidc-name"
            value={draft.name}
            aria-invalid={Boolean(errors.name)}
            onChange={(event) => setField('name', event.target.value)}
          />
        </Field>

        <Field id="oidc-icon" label={t('adminOidc.form.icon')}>
          <Input
            id="oidc-icon"
            value={draft.icon_url}
            placeholder="https://example.com/icon.png"
            onChange={(event) => setField('icon_url', event.target.value)}
          />
        </Field>

        <div className="flex flex-col gap-1.5">
          <div className="flex items-center justify-between gap-2">
            <Label htmlFor="oidc-discovery-document">{t('adminOidc.form.discoveryDocument')}</Label>
            <Button type="button" variant="outline" size="sm" onClick={() => setImportOpen(true)}>
              {t('adminOidc.form.importFromUrl')}
            </Button>
          </div>
          <Textarea
            id="oidc-discovery-document"
            value={draft.discovery_document}
            onChange={(event) => {
              setField('discovery_document', event.target.value);
              setField('discovery_url', '');
            }}
            aria-invalid={Boolean(errors.discovery_document)}
            rows={10}
            spellCheck={false}
            className="h-[178px] resize-none overflow-y-auto font-mono text-xs"
          />
          {errors.discovery_document && (
            <p className="text-destructive text-xs">{errors.discovery_document}</p>
          )}
        </div>

        <Field id="oidc-client-id" label={t('adminOidc.form.clientId')} error={errors.client_id}>
          <Input
            id="oidc-client-id"
            value={draft.client_id}
            aria-invalid={Boolean(errors.client_id)}
            onChange={(event) => setField('client_id', event.target.value)}
          />
        </Field>

        <SecretField
          id="oidc-client-secret"
          label={t('adminOidc.form.clientSecret')}
          value={secret}
          isSet={secretSet && !clearSecret}
          onChange={(value) => {
            setSecret(value);
            setSecretTouched(true);
            setClearSecret(false);
          }}
          onClear={() => {
            setSecret('');
            setSecretTouched(true);
            setClearSecret(true);
          }}
        />

        <Field id="oidc-scopes" label={t('adminOidc.form.scopes')} error={errors.scopes}>
          <Input
            id="oidc-scopes"
            value={draft.scopes}
            aria-invalid={Boolean(errors.scopes)}
            onChange={(event) => setField('scopes', event.target.value)}
          />
        </Field>

        <div className="flex items-center gap-2">
          <Switch
            id="oidc-allow-unbound"
            checked={draft.allow_unbound}
            onCheckedChange={(checked) => setField('allow_unbound', checked)}
          />
          <Label htmlFor="oidc-allow-unbound" className="font-normal">
            {t('adminOidc.form.allowUnbound')}
          </Label>
        </div>

        <div className="flex flex-col gap-3">
          <h3 className="text-sm font-semibold">{t('adminOidc.form.userMapping')}</h3>
          <div className="grid gap-3 sm:grid-cols-2">
            <Field id="oidc-sub-path" label={t('adminOidc.form.subPath')}>
              <Input
                id="oidc-sub-path"
                value={draft.sub_path}
                onChange={(event) => setField('sub_path', event.target.value)}
              />
            </Field>
            <Field id="oidc-email-path" label={t('adminOidc.form.emailPath')}>
              <Input
                id="oidc-email-path"
                value={draft.email_path}
                onChange={(event) => setField('email_path', event.target.value)}
              />
            </Field>
            <Field id="oidc-name-path" label={t('adminOidc.form.namePath')}>
              <Input
                id="oidc-name-path"
                value={draft.name_path}
                onChange={(event) => setField('name_path', event.target.value)}
              />
            </Field>
            <Field id="oidc-picture-path" label={t('adminOidc.form.picturePath')}>
              <Input
                id="oidc-picture-path"
                value={draft.picture_path}
                onChange={(event) => setField('picture_path', event.target.value)}
              />
            </Field>
          </div>
        </div>

        <div className="flex flex-col gap-3">
          <h3 className="text-sm font-semibold">{t('adminOidc.form.groupMapping')}</h3>
          <Field id="oidc-group-claim" label={t('adminOidc.form.groupClaim')}>
            <Input
              id="oidc-group-claim"
              value={draft.group_claim}
              onChange={(event) => setField('group_claim', event.target.value)}
            />
          </Field>
          {groupsError ? (
            <p className="text-destructive text-xs">{t('adminOidc.form.groupsLoadFailed')}</p>
          ) : (
            <div className="flex flex-col gap-2">
              {draft.group_mappings.map((mapping, index) => (
                <div key={index} className="flex items-center gap-2">
                  <Input
                    value={mapping.value}
                    placeholder={t('adminOidc.form.groupValue')}
                    aria-label={t('adminOidc.form.groupValue')}
                    className="min-w-0 flex-1"
                    onChange={(event) => updateMapping(index, { value: event.target.value })}
                  />
                  <Select
                    value={String(mapping.group_id)}
                    onValueChange={(value) => updateMapping(index, { group_id: Number(value) })}
                  >
                    <SelectTrigger
                      className="w-36 shrink-0"
                      aria-label={t('adminOidc.form.groupSelect')}
                    >
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {groups.map((group) => (
                        <SelectItem key={group.id} value={String(group.id)}>
                          {group.name}
                        </SelectItem>
                      ))}
                      {mapping.group_id > 0 &&
                        !groups.some((group) => group.id === mapping.group_id) && (
                          <SelectItem value={String(mapping.group_id)}>
                            {`#${mapping.group_id}`}
                          </SelectItem>
                        )}
                    </SelectContent>
                  </Select>
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon-sm"
                    aria-label={t('adminOidc.form.removeMapping')}
                    onClick={() =>
                      setDraft((prev) => ({
                        ...prev,
                        group_mappings: prev.group_mappings.filter(
                          (_, current) => current !== index,
                        ),
                      }))
                    }
                  >
                    <X />
                  </Button>
                </div>
              ))}
              <Button
                type="button"
                variant="outline"
                size="sm"
                className="w-fit"
                onClick={addMapping}
                disabled={groups.length === 0}
              >
                <Plus className="size-4" />
                {t('adminOidc.form.addMapping')}
              </Button>
            </div>
          )}
        </div>

        <div className="flex items-center justify-end gap-2">
          {!isNew && (
            <Button
              type="button"
              variant="outline"
              onClick={reset}
              disabled={saveMutation.isPending}
            >
              {t('common.action.reset')}
            </Button>
          )}
          <Button
            type="submit"
            disabled={saveMutation.isPending || (isNew && !allocation) || (!isNew && !dirty)}
          >
            {saveMutation.isPending
              ? t('common.action.saving')
              : isNew
                ? t('adminOidc.form.create')
                : t('common.action.save')}
          </Button>
        </div>
      </form>

      <Dialog
        open={importOpen}
        onOpenChange={(open) => {
          if (!importMutation.isPending) setImportOpen(open);
        }}
      >
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{t('adminOidc.form.importFromUrl')}</DialogTitle>
          </DialogHeader>
          <form className="flex flex-col gap-3" onSubmit={handleImport}>
            <Field
              id="oidc-import-url"
              label={t('adminOidc.form.importUrl')}
              error={importError ?? undefined}
            >
              <Input
                id="oidc-import-url"
                value={importURL}
                onChange={(event) => setImportURL(event.target.value)}
                autoFocus
              />
            </Field>
            <div className="flex items-center justify-end gap-2">
              <Button
                type="button"
                variant="outline"
                onClick={() => setImportOpen(false)}
                disabled={importMutation.isPending}
              >
                {t('common.action.cancel')}
              </Button>
              <Button type="submit" disabled={importMutation.isPending}>
                {importMutation.isPending
                  ? t('adminOidc.form.importing')
                  : t('adminOidc.form.import')}
              </Button>
            </div>
          </form>
        </DialogContent>
      </Dialog>
    </>
  );
}
