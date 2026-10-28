import type { UploadOutputFormat } from '../hooks/useUploadSettings';

export type CompressionFormat = 'jpeg' | 'webp' | 'avif';

export interface CompressionRequest {
  id: number;
  file: File;
  quality: number;
  format: CompressionFormat;
  animation: boolean;
}

export interface CompressionResponse {
  id: number;
  ok: boolean;
  /** 编码后的字节, 以可转移的 ArrayBuffer 返回 */
  data?: ArrayBuffer;
  error?: string;
  /** 编码器实际输出的格式与是否动图, 用于校验降级结果 */
  format?: string;
  animation?: boolean;
  /** 编码器实例已不可用, 收到后重建压缩线程 */
  reset?: boolean;
}

export interface CompressImageOptions {
  quality: number;
  format: UploadOutputFormat;
  /** 动图压缩: 目标固定为 WebP (docs/webui.md 第 6 节) */
  forceWebp: boolean;
}

const MIME: Record<CompressionFormat, string> = {
  jpeg: 'image/jpeg',
  webp: 'image/webp',
  avif: 'image/avif',
};

const EXTENSION: Record<UploadOutputFormat, string> = {
  jpg: 'jpg',
  jpeg: 'jpeg',
  webp: 'webp',
  avif: 'avif',
};

const FALLBACK_ERROR = '图片压缩失败';

/** 替换文件名末尾扩展名; 无扩展名时直接追加 */
export function replaceFileExtension(name: string, extension: string): string {
  const base = name.replace(/\.[^./\\]+$/, '');
  return `${base === '' ? name : base}.${extension}`;
}

let worker: Worker | null = null;
let sequence = 0;
const pending = new Map<
  number,
  { resolve: (response: CompressionResponse) => void; reject: (error: Error) => void }
>();

function rejectAll(message: string) {
  for (const waiter of pending.values()) waiter.reject(new Error(message));
  pending.clear();
}

function getWorker(): Worker {
  if (worker !== null) return worker;
  const created = new Worker(new URL('../workers/imageCompression.worker.ts', import.meta.url), {
    type: 'module',
  });
  created.onmessage = (event: MessageEvent<CompressionResponse>) => {
    const waiter = pending.get(event.data.id);
    if (waiter === undefined) return;
    pending.delete(event.data.id);
    if (event.data.reset) {
      worker = null;
      created.terminate();
    }
    if (event.data.ok) waiter.resolve(event.data);
    else waiter.reject(new Error(event.data.error ?? FALLBACK_ERROR));
  };
  created.onerror = () => {
    worker = null;
    created.terminate();
    rejectAll('压缩线程异常退出');
  };
  created.onmessageerror = () => {
    rejectAll('压缩线程返回了无法解析的数据');
  };
  worker = created;
  return created;
}

/**
 * 在 Web Worker 中把图片转码为指定格式; 动图强制 WebP 以保留动画与透明通道.
 * 输入 File 原样传入, 输出为新 File (扩展名跟随目标格式).
 */
export async function compressImage(
  file: File,
  options: CompressImageOptions,
): Promise<{ file: File; format: UploadOutputFormat }> {
  const format: CompressionFormat = options.forceWebp
    ? 'webp'
    : options.format === 'jpg' || options.format === 'jpeg'
      ? 'jpeg'
      : options.format;
  const id = ++sequence;
  const target = getWorker();
  const request: CompressionRequest = {
    id,
    file,
    quality: options.quality,
    format,
    animation: options.forceWebp,
  };

  const response = await new Promise<CompressionResponse>((resolve, reject) => {
    pending.set(id, { resolve, reject });
    target.postMessage(request);
  });

  if (response.data === undefined || response.data.byteLength === 0) {
    throw new Error(FALLBACK_ERROR);
  }
  if (format === 'avif' && response.format !== 'avif') {
    throw new Error('当前浏览器无法输出 AVIF');
  }

  const mime = MIME[format];
  const blob = new Blob([response.data], { type: mime });
  const extension = options.forceWebp ? 'webp' : EXTENSION[options.format];
  return {
    file: new File([blob], replaceFileExtension(file.name, extension), {
      type: mime,
      lastModified: Date.now(),
    }),
    format,
  };
}
