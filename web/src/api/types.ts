/**
 * Picsky REST API 类型定义 (/api/v1)。
 * 事实来源: docs/api.md, docs/permissions.md, docs/database.md, docs/storage-policy.md。
 */

/** 失败响应体: {"code","message","details"} */
export interface ApiErrorBody {
  code: string;
  message: string;
  details?: Record<string, unknown> | null;
}

/** 分页响应 */
export interface Paginated<T> {
  total: number;
  page: number;
  page_size: number;
  items: T[];
}

/** GET /site 的站点公开信息 */
export interface SiteInfo {
  site_name: string;
  register_enabled: boolean;
  password_reset_enabled: boolean;
  captcha: CaptchaConfig;
}

/* ------------------------------------------------------------------ 认证 */

export type QuotaMode = 'count' | 'bytes';

export interface QuotaInfo {
  /** null 表示未设置配额 */
  mode: QuotaMode | null;
  /** 单位由 mode 决定: count 为图片数量, bytes 为存储空间 */
  used: number;
  /** null 表示不限 */
  limit: number | null;
  /** 已占用存储空间 (字节), 与 mode 无关 */
  used_bytes: number;
}

export interface GroupRef {
  id: number;
  name: string;
}

export interface CurrentUser {
  id: number;
  username: string;
  nickname: string;
  email: string;
  email_verified: boolean;
  group: GroupRef;
  permissions: string[];
  quota: QuotaInfo;
  default_policy_id: number | null;
  totp_enabled: boolean;
}

export type LoginRequest =
  | {
      identifier: string;
      password: string;
      captcha_token?: string;
      login_challenge?: never;
    }
  | {
      login_challenge: string;
      totp: string;
    };

export interface RegisterRequest {
  username: string;
  email: string;
  password: string;
  captcha_token?: string;
}

export interface PasswordChangeRequest {
  old_password: string;
  new_password: string;
}

export interface PasswordResetRequest {
  email: string;
  captcha_token?: string;
}

export interface PasswordResetConfirmRequest {
  token: string;
  password: string;
}

export interface ProfileUpdateRequest {
  nickname: string;
}

export interface TotpSetupResponse {
  secret: string;
  otpauth_url: string;
}

export type CaptchaProvider = 'none' | 'cap' | 'turnstile';

export interface CaptchaConfig {
  provider: CaptchaProvider;
  /** turnstile 为 site key; cap 下为 key 片段 */
  site_key: string;
  /** 仅 provider 为 cap 时下发: Cap 实例的 API 前缀, 直接用于 widget 的 data-cap-api-endpoint */
  api_endpoint?: string;
}

/* ------------------------------------------------------------------ 上传 */

export interface UploadUrlAlias {
  alias: string;
  url: string;
  is_default: boolean;
}

export interface UploadHashes {
  md5: string;
  sha256: string;
  sha512: string;
}

export interface UploadResult {
  id: number;
  policy_id: number;
  rel_path: string;
  object_key: string;
  url: string;
  urls: UploadUrlAlias[];
  hashes: UploadHashes;
  size: number;
  width: number | null;
  height: number | null;
  format: string;
  ext: string;
  original_filename: string;
  /** true 表示命中秒传 */
  dedup: boolean;
  /** false 表示复用, 不占配额 */
  quota_charged: boolean;
  created_at: string;
}

export interface UploadCheckRequest {
  sha256: string;
  policy_id?: number;
}

export interface UploadCheckResponse {
  exists: boolean;
  record?: {
    id: number;
    url: string;
    size: number;
  };
}

export interface UploadRequest {
  file: File;
  policy_id?: number;
  original_filename?: string;
}

export interface Base64UploadRequest {
  filename: string;
  data: string;
  policy_id?: number;
}

/* ------------------------------------------------------------------ 图片 */

export type DeleteReason =
  'last_reference' | 'no_capability' | 'setting_disabled' | 'shared' | 'queued' | 'backend_error';

export interface ImageRecord {
  id: number;
  policy_id: number;
  policy_name: string;
  /** 策略驱动的能力位 (如 delete / overwrite / stat) */
  policy_capabilities: string[];
  rel_path: string;
  object_key: string;
  url: string;
  urls: UploadUrlAlias[];
  md5: string;
  sha1: string;
  sha256: string;
  sha512: string;
  size: number;
  format: string;
  ext: string;
  mime: string;
  width: number | null;
  height: number | null;
  original_filename: string;
  /** 上传用户邮箱 (管理端可见) */
  user_email: string;
  /** web / api / scan */
  source: string;
  client_ip: string | null;
  quota_charged: boolean;
  created_at: string;
}

export interface ImageListParams {
  page?: number;
  page_size?: number;
  keyword?: string;
  format?: string;
  /** RFC3339 */
  from?: string;
  /** RFC3339 */
  to?: string;
  policy_id?: number;
  /** 上传用户邮箱模糊搜索 */
  user?: string;
}

export interface DeleteResult {
  id: number;
  deleted: boolean;
  object_deleted: boolean;
  reason: DeleteReason;
}

export interface BatchDeleteResult {
  items: DeleteResult[];
}

/* ------------------------------------------------------------ 可用策略 */

export interface PolicySummary {
  id: number;
  name: string;
  type: string;
  urls: UploadUrlAlias[];
  is_default: boolean;
}

/* ------------------------------------------------------------ API 密钥 */

export interface ApiKeyRecord {
  id: number;
  name: string;
  access_key: string;
  /** null 表示跟随用户权限 */
  scopes: string[] | null;
  expires_at: string | null;
  last_used_at: string | null;
  revoked_at: string | null;
  created_at: string;
}

export type ApiKeyCreated = ApiKeyRecord & { secret: string };

export interface ApiKeyCreateRequest {
  name: string;
  scopes?: string[];
  expires_at?: string;
}

/* ------------------------------------------------------------------ 管理 */

/** 账号状态: 0 正常, 1 未验证, 2 封禁 */
export type UserStatus = 0 | 1 | 2;

export interface AdminUser {
  id: number;
  username: string;
  nickname: string;
  email: string;
  email_verified: boolean;
  group_id: number;
  group_name: string;
  status: UserStatus;
  restore_group_id: number | null;
  quota_mode: QuotaMode | null;
  quota_value: number | null;
  used_images: number;
  used_bytes: number;
  default_policy_id: number | null;
  last_login_at: string | null;
  created_at: string;
}

export interface AdminUserListParams {
  page?: number;
  page_size?: number;
  keyword?: string;
}

export interface AdminUserCreateRequest {
  username: string;
  email: string;
  password: string;
  nickname?: string;
  group_id: number;
}

export interface AdminUserUpdateRequest {
  group_id?: number;
  status?: UserStatus;
  /** '' 表示清除用户级覆盖 (继承组/系统默认); null/缺省表示不修改 */
  quota_mode?: QuotaMode | '' | null;
  quota_value?: number | null;
  /** 0 表示清除 (继承组内策略); null/缺省表示不修改 */
  default_policy_id?: number | null;
  password?: string;
}

export interface AdminGroup {
  id: number;
  name: string;
  description: string;
  is_default: boolean;
  is_system: boolean;
  permissions: string[];
  permission_bits: number;
  quota_mode: QuotaMode | null;
  quota_value: number | null;
  max_file_size_bytes: number | null;
  upload_rate_limit: number | null;
  sort: number;
  user_count: number;
  policy_ids: number[];
}

export interface AdminGroupWriteRequest {
  name: string;
  description?: string;
  is_default?: boolean;
  permissions?: string[];
  quota_mode?: QuotaMode | null;
  quota_value?: number | null;
  max_file_size_bytes?: number | null;
  upload_rate_limit?: number | null;
  sort?: number;
}

export type ConflictPolicy = 'rename' | 'skip';

export interface AdminPolicyUrl {
  alias: string;
  url: string;
  is_default: boolean;
  sort?: number;
}

export interface AdminPolicy {
  id: number;
  name: string;
  type: string;
  enabled: boolean;
  path_template: string;
  conflict_policy: ConflictPolicy;
  /** 是否复用已删除图片释放的 {seq}; 默认 false */
  reuse_released_seq: boolean;
  config: {
    endpoint?: string;
    region?: string;
    bucket?: string;
    path_style?: boolean;
    [key: string]: unknown;
  };
  secrets_present: boolean;
  /** 读: 解密后的凭据 (仅管理接口返回) */
  secrets?: {
    username?: string;
    password?: string;
  };
  urls: AdminPolicyUrl[];
  sort: number;
  capabilities: string[];
  created_at: string;
  updated_at: string;
}

export interface AdminPolicyWriteRequest {
  name: string;
  type: string;
  enabled: boolean;
  path_template: string;
  conflict_policy: ConflictPolicy;
  reuse_released_seq?: boolean;
  config: Record<string, unknown>;
  /** 仅写: {"username","password"} */
  secrets?: {
    username?: string;
    password?: string;
  };
  urls: AdminPolicyUrl[];
  sort?: number;
}

export interface PolicyTestResult {
  ok: boolean;
  elapsed_ms: number;
  error: string | null;
}

export interface PolicyScanRequest {
  path?: string;
  dry_run?: boolean;
}

export interface PolicyScanEnqueued {
  task_id: number;
  status: MaintenanceTaskStatus;
  objects: number;
}

export interface PolicyScanResult {
  images: number;
  files: number;
  skipped: number;
  failed: { path: string; error: string }[];
}

export interface PolicyPreviewRequest {
  path_template: string;
  md5?: string;
  sha256?: string;
  sha512?: string;
  ext?: string;
}

export interface PolicyPreviewResult {
  rel_path: string;
  object_key: string;
  urls: { alias: string; url: string }[];
}

/** GET /admin/settings/client-ip-sources */
export interface ClientIPSource {
  source: string;
  ip: string;
}

/** 系统设置: key -> value; 加密项始终返回空串并附带 *_set 布尔 */
export type SettingsPayload = Record<string, string | boolean | undefined> & {
  captcha_secret_set?: boolean;
  smtp_password_set?: boolean;
};

export type MaintenanceTaskType = 'object_delete' | 'log_purge' | 'policy_scan' | 'quota_reconcile';
export type MaintenanceTaskStatus = 'pending' | 'running' | 'done' | 'failed';

export interface MaintenanceTask {
  id: number;
  type: MaintenanceTaskType | string;
  payload: string | null;
  result?: string;
  status: MaintenanceTaskStatus | string;
  attempts: number;
  last_error: string | null;
  created_at: string;
  finished_at: string | null;
}

export interface TaskListParams {
  type?: string;
  status?: string;
  page?: number;
  page_size?: number;
}

export interface AuditLog {
  id: number;
  user_id: number | null;
  username: string | null;
  action: string;
  target_type: string | null;
  target_id: number | null;
  detail: string | null;
  ip: string | null;
  user_agent: string | null;
  created_at: string;
}

export interface LogListParams {
  action?: string;
  user_id?: number;
  from?: string;
  to?: string;
  page?: number;
  page_size?: number;
}

export interface StatsByFormat {
  format: string;
  count: number;
  size: number;
}

export interface StatsByPolicy {
  policy_id: number;
  policy_name: string;
  count: number;
  size: number;
}

export interface AdminStats {
  image_count: number;
  used_bytes: number;
  user_count: number;
  today_uploads: number;
  by_format: StatsByFormat[];
  by_policy: StatsByPolicy[];
}

/* ---------------------------------------------------------------- 权限位 */

/** docs/permissions.md 第 2 节的权限位名称 */
export const PERMISSION_NAMES = [
  'upload',
  'delete_own',
  'manage_keys',
  'view_all',
  'delete_all',
  'manage_policies',
  'manage_users',
  'manage_groups',
  'manage_settings',
  'view_logs',
] as const;

export type PermissionName = (typeof PERMISSION_NAMES)[number];

export type PermissionBits = Record<PermissionName, number>;
