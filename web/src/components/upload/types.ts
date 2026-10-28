import type { UploadResult } from '../../api/types';

export type UploadItemStatus =
  'pending' | 'compressing' | 'hashing' | 'uploading' | 'done' | 'failed' | 'duplicate' | 'skipped';

export interface UploadItem {
  id: string;
  /** 用户选择的原始文件, 用于预览与重新压缩 */
  file: File;
  name: string;
  size: number;
  /** 压缩后的大小; 压缩成功时记录, 即使因体积更大而保留原图 */
  compressedSize?: number;
  status: UploadItemStatus;
  /** 0-100 */
  progress: number;
  /** 自动上传关闭时, 等待用户点击开始上传 */
  armed: boolean;
  /** 压缩后的上传文件; 不存在时上传原文件 */
  uploadFile?: File;
  /** 压缩失败原因 (处理方式为直接上传时仍继续上传原图) */
  compressionError?: string;
  error?: string;
  result?: UploadResult;
  /** 秒传命中的已有记录 (/upload/check) */
  dedupRecord?: { id: number; url: string; size: number };
}

export function createUploadItem(file: File, armed: boolean): UploadItem {
  return {
    id: `${Date.now()}-${Math.random().toString(36).slice(2, 10)}`,
    file,
    name: file.name,
    size: file.size,
    status: 'pending',
    progress: 0,
    armed,
  };
}

/** 实际参与校验与上传的文件 */
export function uploadItemPayload(item: UploadItem): File {
  return item.uploadFile ?? item.file;
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
  return item.result?.original_filename ?? item.uploadFile?.name ?? item.name;
}
