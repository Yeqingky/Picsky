import { useSyncExternalStore } from 'react';
import { STORAGE_KEYS } from '../utils/constants';

/** 压缩输出格式: jpg/jpeg 仅上传文件名后缀不同 (docs/webui.md 第 6 节) */
export type UploadOutputFormat = 'jpg' | 'jpeg' | 'webp' | 'avif';

/** 压缩失败处理: 直接上传原图或跳过 (跳过不产生记录与直链) */
export type CompressionFailure = 'upload' | 'skip';

export interface UploadSettings {
  /** 启用压缩 (默认关闭) */
  compression: boolean;
  /** 压缩质量 50-100 */
  quality: number;
  /** 静态图输出格式 */
  format: UploadOutputFormat;
  /** 动图压缩 (默认关闭), 开启后动图输出为 WebP */
  animated: boolean;
  /** 编码结果不小于原文件时保留原图 (默认开启) */
  keepOriginal: boolean;
  /** 选择文件后自动开始上传 (默认关闭) */
  autoUpload: boolean;
  /** 压缩失败时的处理方式 */
  failure: CompressionFailure;
}

export const QUALITY_MIN = 50;
export const QUALITY_MAX = 100;

/** 各输出格式的默认压缩率; 切换格式时同步应用 */
export const DEFAULT_QUALITY_BY_FORMAT: Record<UploadOutputFormat, number> = {
  jpg: 95,
  jpeg: 95,
  webp: 90,
  avif: 80,
};

export const UPLOAD_SETTINGS_DEFAULTS: UploadSettings = {
  compression: false,
  quality: DEFAULT_QUALITY_BY_FORMAT.webp,
  format: 'webp',
  animated: false,
  keepOriginal: true,
  autoUpload: false,
  failure: 'upload',
};

const FORMATS: readonly UploadOutputFormat[] = ['jpg', 'jpeg', 'webp', 'avif'];
const FAILURES: readonly CompressionFailure[] = ['upload', 'skip'];
/** 设置结构版本: 旧版本中的固定默认质量 95 迁移为各格式默认值 */
const SETTINGS_VERSION = 2;

function clampQuality(value: unknown): number {
  const parsed = typeof value === 'number' ? value : Number(value);
  if (!Number.isFinite(parsed)) return UPLOAD_SETTINGS_DEFAULTS.quality;
  return Math.min(QUALITY_MAX, Math.max(QUALITY_MIN, Math.round(parsed)));
}

function sanitize(patch: Partial<UploadSettings>): UploadSettings {
  const merged = { ...UPLOAD_SETTINGS_DEFAULTS, ...patch };
  return {
    compression: Boolean(merged.compression),
    quality: clampQuality(merged.quality),
    format: FORMATS.find((value) => value === merged.format) ?? UPLOAD_SETTINGS_DEFAULTS.format,
    animated: Boolean(merged.animated),
    keepOriginal: Boolean(merged.keepOriginal),
    autoUpload: Boolean(merged.autoUpload),
    failure: FAILURES.find((value) => value === merged.failure) ?? UPLOAD_SETTINGS_DEFAULTS.failure,
  };
}

function read(): UploadSettings {
  if (typeof window === 'undefined') return UPLOAD_SETTINGS_DEFAULTS;
  try {
    const raw = window.localStorage.getItem(STORAGE_KEYS.uploadSettings);
    if (raw === null) return UPLOAD_SETTINGS_DEFAULTS;
    const parsed: unknown = JSON.parse(raw);
    if (typeof parsed !== 'object' || parsed === null) return UPLOAD_SETTINGS_DEFAULTS;
    const record = parsed as Partial<UploadSettings> & { version?: number };
    const settings = sanitize(record);
    if (record.version !== SETTINGS_VERSION && settings.quality === 95) {
      return { ...settings, quality: DEFAULT_QUALITY_BY_FORMAT[settings.format] };
    }
    return settings;
  } catch {
    return UPLOAD_SETTINGS_DEFAULTS;
  }
}

let state = read();
const listeners = new Set<() => void>();

function emit() {
  for (const listener of listeners) listener();
}

function persist() {
  try {
    window.localStorage.setItem(
      STORAGE_KEYS.uploadSettings,
      JSON.stringify({ ...state, version: SETTINGS_VERSION }),
    );
  } catch {
    // 忽略存储失败 (隐私模式或配额不足)
  }
}

/** 更新并持久化上传/压缩设置; 所有使用方通过 useUploadSettings 同步 */
export function updateUploadSettings(patch: Partial<UploadSettings>) {
  state = sanitize({ ...state, ...patch });
  persist();
  emit();
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

if (typeof window !== 'undefined') {
  // 其他标签页修改设置时同步本页
  window.addEventListener('storage', (event) => {
    if (event.key !== STORAGE_KEYS.uploadSettings) return;
    state = read();
    emit();
  });
}

export function useUploadSettings(): UploadSettings {
  return useSyncExternalStore(
    subscribe,
    () => state,
    () => UPLOAD_SETTINGS_DEFAULTS,
  );
}
