import { api } from './client';
import type {
  Base64UploadRequest,
  UploadCheckRequest,
  UploadCheckResponse,
  UploadResult,
} from './types';

export interface UploadFileOptions {
  policyId?: number;
  originalFilename?: string;
  onProgress?: (percent: number) => void;
  signal?: AbortSignal;
}

export const uploadApi = {
  /** multipart 上传; 秒传时后端返回 200, 正常上传返回 201, 两者响应体一致 */
  uploadFile: (
    { policyId, originalFilename, onProgress, signal }: UploadFileOptions,
    file: File,
  ) => {
    const form = new FormData();
    form.append('file', file, file.name);
    if (policyId !== undefined) form.append('policy_id', String(policyId));
    if (originalFilename) form.append('original_filename', originalFilename);

    return api
      .post<UploadResult>('/upload', form, {
        signal,
        onUploadProgress: (event) => {
          if (!onProgress) return;
          const total = event.total ?? file.size;
          if (!total) return;
          onProgress(Math.min(100, Math.round((event.loaded / total) * 100)));
        },
      })
      .then((r) => r.data);
  },

  check: (body: UploadCheckRequest) =>
    api.post<UploadCheckResponse>('/upload/check', body).then((r) => r.data),

  uploadBase64: (body: Base64UploadRequest) =>
    api.post<UploadResult>('/upload/base64', body).then((r) => r.data),
};
