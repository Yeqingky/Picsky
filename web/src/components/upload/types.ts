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
