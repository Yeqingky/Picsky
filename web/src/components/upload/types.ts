import type { UploadResult } from '../../api/types';

export type UploadItemStatus =
  'pending' | 'hashing' | 'uploading' | 'done' | 'failed' | 'duplicate';

export interface UploadItem {
  id: string;
  file: File;
  name: string;
  size: number;
  status: UploadItemStatus;
  /** 0-100 */
  progress: number;
  error?: string;
  result?: UploadResult;
  /** 秒传命中的已有记录 (/upload/check) */
  dedupRecord?: { id: number; url: string; size: number };
}

export function createUploadItem(file: File): UploadItem {
  return {
    id: `${Date.now()}-${Math.random().toString(36).slice(2, 10)}`,
    file,
    name: file.name,
    size: file.size,
    status: 'pending',
    progress: 0,
  };
}

/** 上传项的直链: 完成或秒传命中时可用, 取默认别名 */
export function uploadItemUrl(item: UploadItem): string | null {
  if (item.result) {
    const urls = item.result.urls;
    const preferred = urls.find((entry) => entry.is_default) ?? urls[0];
    return preferred?.url ?? item.result.url;
  }
  return item.dedupRecord?.url ?? null;
}

/** 上传项用于复制格式的原始文件名 */
export function uploadItemFilename(item: UploadItem): string {
  return item.result?.original_filename ?? item.name;
}
