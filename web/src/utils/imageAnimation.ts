/**
 * 动图探测: 只读取文件头, 不依赖浏览器解码能力.
 * WebP 读取 VP8X 的 ANIMATION 标志; AVIF 检查 ftyp 兼容品牌是否包含 avis;
 * GIF 视为动图 (动图压缩开启时由编码器按实际帧处理).
 */
const WEBP_ANIMATION_FLAG = 0x02;
const HEADER_BYTES = 1024;

function readAscii(bytes: Uint8Array, start: number, length: number): string {
  if (start + length > bytes.length) return '';
  let text = '';
  for (let index = 0; index < length; index += 1) {
    text += String.fromCharCode(bytes[start + index] ?? 0);
  }
  return text;
}

function ftypBrands(bytes: Uint8Array): string[] {
  const brands: string[] = [];
  const major = readAscii(bytes, 8, 4);
  if (major !== '') brands.push(major);
  const boxSize = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength).getUint32(0);
  const end = Math.min(bytes.length, boxSize === 0 ? bytes.length : boxSize);
  for (let offset = 16; offset + 4 <= end; offset += 4) {
    const brand = readAscii(bytes, offset, 4);
    if (brand !== '') brands.push(brand);
  }
  return brands;
}

export async function isAnimatedImage(file: File): Promise<boolean> {
  if (file.type === 'image/gif') return true;

  const head = new Uint8Array(await file.slice(0, HEADER_BYTES).arrayBuffer());

  if (
    head.length >= 21 &&
    readAscii(head, 0, 4) === 'RIFF' &&
    readAscii(head, 8, 4) === 'WEBP' &&
    readAscii(head, 12, 4) === 'VP8X'
  ) {
    return ((head[20] ?? 0) & WEBP_ANIMATION_FLAG) !== 0;
  }

  if (head.length >= 12 && readAscii(head, 4, 4) === 'ftyp') {
    return ftypBrands(head).includes('avis');
  }

  return false;
}
