/**
 * 存储策略 JSON 导入导出: 导出结构与管理接口写请求一致, 导入兼容导出的 JSON
 * 与管理接口返回的策略对象 (多余字段忽略). 见 docs/webui.md 第 8 节.
 */
import type { ConflictPolicy } from '../../api/types';

export interface PolicyTransferUrl {
  alias: string;
  url: string;
}

export interface PolicyTransferSource {
  name: string;
  hint: string;
  type: string;
  enabled: boolean;
  endpoint: string;
  username: string;
  password: string;
  pathTemplate: string;
  conflictPolicy: ConflictPolicy;
  reuseReleasedSeq: boolean;
  urls: PolicyTransferUrl[];
}

export interface PolicyTransferPayload {
  name: string;
  hint: string;
  type: string;
  enabled: boolean;
  path_template: string;
  conflict_policy: ConflictPolicy;
  reuse_released_seq: boolean;
  config: { endpoint: string };
  secrets?: { username?: string; password?: string };
  urls: { alias: string; url: string; is_default: boolean; sort: number }[];
}

/** 导入结果只包含 JSON 中出现的字段, 其余字段保留表单当前值 */
export interface PolicyTransferResult {
  name?: string;
  hint?: string;
  type?: string;
  enabled?: boolean;
  endpoint?: string;
  username?: string;
  password?: string;
  pathTemplate?: string;
  conflictPolicy?: ConflictPolicy;
  reuseReleasedSeq?: boolean;
  urls?: PolicyTransferUrl[];
}

export type PolicyTransferErrorKey =
  'invalidJson' | 'notObject' | 'empty' | 'typeUnsupported' | 'field';

export class PolicyTransferError extends Error {
  constructor(
    readonly key: PolicyTransferErrorKey,
    readonly field?: string,
    readonly detail?: string,
  ) {
    super(key);
    this.name = 'PolicyTransferError';
  }
}

const CONFLICT_VALUES: readonly ConflictPolicy[] = ['rename', 'skip'];

/** 边界解析: JSON 解析结果按字段路径转对象, 非对象直接报字段错误 */
function asObject(value: unknown, path: string): Record<string, unknown> {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    throw new PolicyTransferError('field', path);
  }
  return value as Record<string, unknown>;
}

function readString(
  record: Record<string, unknown>,
  key: string,
  path: string,
): string | undefined {
  const value = record[key];
  if (value === undefined || value === null) return undefined;
  if (typeof value !== 'string') throw new PolicyTransferError('field', path);
  return value;
}

/** 当前表单 -> 可复制的 JSON (与管理接口写请求字段一致, 凭据为明文) */
export function buildPolicyTransferPayload(source: PolicyTransferSource): PolicyTransferPayload {
  const secrets: { username?: string; password?: string } = {};
  if (source.username.trim() !== '') secrets.username = source.username.trim();
  if (source.password !== '') secrets.password = source.password;

  const payload: PolicyTransferPayload = {
    name: source.name,
    hint: source.hint,
    type: source.type,
    enabled: source.enabled,
    path_template: source.pathTemplate,
    conflict_policy: source.conflictPolicy,
    reuse_released_seq: source.reuseReleasedSeq,
    config: { endpoint: source.endpoint },
    urls: source.urls.map((row, index) => ({
      alias: row.alias,
      url: row.url,
      is_default: index === 0,
      sort: index + 1,
    })),
  };
  if (Object.keys(secrets).length > 0) payload.secrets = secrets;
  return payload;
}

/**
 * JSON 文本 -> 表单补丁. 顶层支持 config.endpoint 与 secrets.{username,password};
 * 也接受扁平 endpoint / username / password 便于手写. 仅出现未知字段时视为空.
 */
export function parsePolicyTransfer(
  raw: string,
  allowedTypes: readonly string[],
): PolicyTransferResult {
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch (error) {
    throw new PolicyTransferError(
      'invalidJson',
      undefined,
      error instanceof Error ? error.message : undefined,
    );
  }
  if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
    throw new PolicyTransferError('notObject');
  }
  const root = parsed as Record<string, unknown>;

  const result: PolicyTransferResult = {};
  let recognized = 0;

  const name = readString(root, 'name', 'name');
  if (name !== undefined) {
    result.name = name;
    recognized += 1;
  }

  const hint = readString(root, 'hint', 'hint');
  if (hint !== undefined) {
    result.hint = hint;
    recognized += 1;
  }

  const type = readString(root, 'type', 'type');
  if (type !== undefined) {
    if (!allowedTypes.includes(type)) {
      throw new PolicyTransferError('typeUnsupported', undefined, type);
    }
    result.type = type;
    recognized += 1;
  }

  if (root.enabled !== undefined) {
    if (typeof root.enabled !== 'boolean') throw new PolicyTransferError('field', 'enabled');
    result.enabled = root.enabled;
    recognized += 1;
  }

  if (root.sort !== undefined) {
    // 历史导出可能包含顶层排序; 管理界面已改为拖拽排序, 导入时忽略.
    recognized += 1;
  }

  const pathTemplate = readString(root, 'path_template', 'path_template');
  if (pathTemplate !== undefined) {
    result.pathTemplate = pathTemplate;
    recognized += 1;
  }

  const conflictPolicy = readString(root, 'conflict_policy', 'conflict_policy');
  if (conflictPolicy !== undefined) {
    const matched = CONFLICT_VALUES.find((value) => value === conflictPolicy);
    if (matched === undefined) throw new PolicyTransferError('field', 'conflict_policy');
    result.conflictPolicy = matched;
    recognized += 1;
  }

  if (root.reuse_released_seq !== undefined) {
    if (typeof root.reuse_released_seq !== 'boolean') {
      throw new PolicyTransferError('field', 'reuse_released_seq');
    }
    result.reuseReleasedSeq = root.reuse_released_seq;
    recognized += 1;
  }

  let endpoint = readString(root, 'endpoint', 'endpoint');
  const config = root.config;
  if (config !== undefined) {
    const configEndpoint = readString(asObject(config, 'config'), 'endpoint', 'config.endpoint');
    if (configEndpoint !== undefined) endpoint = configEndpoint;
  }
  if (endpoint !== undefined) {
    result.endpoint = endpoint;
    recognized += 1;
  }

  let username = readString(root, 'username', 'username');
  let password = readString(root, 'password', 'password');
  const secrets = root.secrets;
  if (secrets !== undefined) {
    const secretRecord = asObject(secrets, 'secrets');
    const secretUsername = readString(secretRecord, 'username', 'secrets.username');
    const secretPassword = readString(secretRecord, 'password', 'secrets.password');
    if (secretUsername !== undefined) username = secretUsername;
    if (secretPassword !== undefined) password = secretPassword;
  }
  if (username !== undefined) {
    result.username = username;
    recognized += 1;
  }
  if (password !== undefined) {
    result.password = password;
    recognized += 1;
  }

  const urls = root.urls;
  if (urls !== undefined) {
    if (!Array.isArray(urls)) throw new PolicyTransferError('field', 'urls');
    const items = urls.map((item, index) => {
      if (typeof item === 'string') {
        return { alias: '', url: item, isDefault: false, sort: undefined, index };
      }
      const record = asObject(item, `urls[${index}]`);
      const alias = readString(record, 'alias', `urls[${index}].alias`) ?? '';
      const url = readString(record, 'url', `urls[${index}].url`);
      if (url === undefined) throw new PolicyTransferError('field', `urls[${index}].url`);
      let sort: number | undefined;
      if (record.sort !== undefined) {
        if (typeof record.sort !== 'number' || !Number.isFinite(record.sort)) {
          throw new PolicyTransferError('field', `urls[${index}].sort`);
        }
        sort = record.sort;
      }
      return { alias, url, isDefault: record.is_default === true, sort, index };
    });
    items.sort((left, right) => {
      if (left.isDefault !== right.isDefault) return left.isDefault ? -1 : 1;
      if (left.sort !== undefined && right.sort !== undefined && left.sort !== right.sort) {
        return left.sort - right.sort;
      }
      return left.index - right.index;
    });
    result.urls = items.map(({ alias, url }) => ({ alias, url }));
    recognized += 1;
  }

  if (recognized === 0) throw new PolicyTransferError('empty');
  return result;
}
