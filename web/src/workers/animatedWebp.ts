import { encodeAnimation } from 'wasm-webp';
import type { WebPAnimationFrame } from 'wasm-webp';

/** 帧数与总像素上限: 动图逐帧保留 RGBA, 防止超长动画占满内存 */
const MAX_FRAMES = 240;
const MAX_PIXELS = 20_000_000;

/**
 * 用浏览器 ImageDecoder 解码动图帧, 再通过 libwebp 编码为动图 WebP.
 * 仅作为 WASM 编码器无法处理动图输入 (例如动图 AVIF) 时的回退路径.
 */
export async function encodeAnimatedWebp(
  buffer: ArrayBuffer,
  mime: string,
  quality: number,
): Promise<Uint8Array> {
  if (typeof ImageDecoder === 'undefined') {
    throw new Error('当前浏览器不支持动图解码');
  }
  const decoder = new ImageDecoder({ data: buffer, type: mime });
  try {
    await decoder.tracks.ready;
    const track = decoder.tracks.selectedTrack;
    const frameCount = track?.frameCount ?? 0;
    if (frameCount < 1) throw new Error('动图没有可解码的帧');
    if (frameCount > MAX_FRAMES) {
      throw new Error(`动图帧数超过上限 (${MAX_FRAMES})`);
    }

    const first = await decoder.decode({ frameIndex: 0 });
    const width = first.image.codedWidth;
    const height = first.image.codedHeight;
    first.image.close();
    if (width <= 0 || height <= 0) throw new Error('动图尺寸无效');
    if (width * height * frameCount > MAX_PIXELS) {
      throw new Error('动图总像素超过上限');
    }

    const canvas = new OffscreenCanvas(width, height);
    const context = canvas.getContext('2d', { willReadFrequently: true });
    if (context === null) throw new Error('无法创建动图画布');

    const frames: WebPAnimationFrame[] = [];
    let hasAlpha = false;
    for (let index = 0; index < frameCount; index += 1) {
      const frame = await decoder.decode({ frameIndex: index });
      const { image } = frame;
      try {
        context.clearRect(0, 0, width, height);
        context.drawImage(image, 0, 0, width, height);
        const imageData = context.getImageData(0, 0, width, height);
        const pixels = imageData.data;
        if (!hasAlpha) {
          for (let offset = 3; offset < pixels.length; offset += 4) {
            if ((pixels[offset] ?? 255) < 255) {
              hasAlpha = true;
              break;
            }
          }
        }
        frames.push({
          data: new Uint8Array(pixels.buffer, pixels.byteOffset, pixels.byteLength),
          duration: Math.max(10, Math.round((image.duration ?? 100_000) / 1000)),
          config: { lossless: 0, quality },
        });
      } finally {
        image.close();
      }
    }

    const encoded = await encodeAnimation(width, height, hasAlpha, frames);
    if (encoded === null || encoded.byteLength === 0) throw new Error('动图 WebP 编码失败');
    return encoded;
  } finally {
    decoder.close();
  }
}
