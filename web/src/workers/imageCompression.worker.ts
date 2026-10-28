/// <reference lib="webworker" />
import { optimizeImage } from 'wasm-image-optimization';
import { encodeAnimatedWebp } from './animatedWebp';
import type { CompressionRequest, CompressionResponse } from '../lib/imageCompression';

const scope = self as DedicatedWorkerGlobalScope;

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

/**
 * 压缩线程: 每次真正开始压缩时才创建, WASM 编码器 (Skia + libavif/libwebp) 随该线程加载,
 * 不进入主包. 动图输入在 animation=true 时由编码器输出动图 WebP;
 * 主编码器无法处理动图输入 (如动图 AVIF) 时回退到 ImageDecoder + libwebp 动画编码.
 * 主编码器抛错后其 WASM 实例可能已损坏, 响应通过 reset 通知主线程重建线程.
 */
scope.onmessage = (event: MessageEvent<CompressionRequest>) => {
  const request = event.data;
  void (async () => {
    try {
      const image = new Uint8Array(await request.file.arrayBuffer());
      try {
        const result = await optimizeImage({
          image,
          format: request.format,
          quality: request.quality,
          animation: request.animation,
        });
        const response: CompressionResponse = {
          id: request.id,
          ok: true,
          data: result.data.buffer as ArrayBuffer,
          format: result.format,
          animation: result.animation,
        };
        scope.postMessage(response, [response.data as ArrayBuffer]);
      } catch (error) {
        if (request.animation && request.format === 'webp') {
          try {
            const encoded = await encodeAnimatedWebp(
              await request.file.arrayBuffer(),
              request.file.type,
              request.quality,
            );
            const response: CompressionResponse = {
              id: request.id,
              ok: true,
              data: encoded.buffer as ArrayBuffer,
              format: 'webp',
              animation: true,
              reset: true,
            };
            scope.postMessage(response, [response.data as ArrayBuffer]);
            return;
          } catch (fallbackError) {
            throw new Error(`${errorMessage(error)}; 动图回退失败: ${errorMessage(fallbackError)}`);
          }
        }
        throw error;
      }
    } catch (error) {
      const response: CompressionResponse = {
        id: request.id,
        ok: false,
        error: errorMessage(error),
        reset: true,
      };
      scope.postMessage(response);
    }
  })();
};
