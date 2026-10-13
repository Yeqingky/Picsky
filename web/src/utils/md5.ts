/** RFC 1321 MD5, 返回 32 位小写十六进制; 仅用于 Gravatar/Cravatar 头像地址等非安全用途 */
export function md5Hex(input: string): string {
  const data = new TextEncoder().encode(input);

  // 补位: 追加 0x80 与若干 0x00, 末尾 8 字节写入小端位长度, 使总长为 64 的倍数
  const padded = new Uint8Array((((data.length + 8) >> 6) + 1) << 6);
  padded.set(data);
  padded[data.length] = 0x80;
  const view = new DataView(padded.buffer);
  const bitLength = data.length * 8;
  view.setUint32(padded.length - 8, bitLength >>> 0, true);
  view.setUint32(padded.length - 4, Math.floor(bitLength / 2 ** 32), true);

  // 每轮循环左移位数 (RFC 1321 表格)
  const shifts = [
    7, 12, 17, 22, 7, 12, 17, 22, 7, 12, 17, 22, 7, 12, 17, 22, 5, 9, 14, 20, 5, 9, 14, 20, 5, 9,
    14, 20, 5, 9, 14, 20, 4, 11, 16, 23, 4, 11, 16, 23, 4, 11, 16, 23, 4, 11, 16, 23, 6, 10, 15, 21,
    6, 10, 15, 21, 6, 10, 15, 21, 6, 10, 15, 21,
  ];
  // 常量表 K[i] = floor(2^32 * abs(sin(i + 1)))
  const sine = Array.from({ length: 64 }, (_, i) =>
    Math.floor(Math.abs(Math.sin(i + 1)) * 2 ** 32),
  );

  let a = 0x67452301;
  let b = 0xefcdab89;
  let c = 0x98badcfe;
  let d = 0x10325476;

  for (let offset = 0; offset < padded.length; offset += 64) {
    const words = Array.from({ length: 16 }, (_, i) => view.getUint32(offset + i * 4, true));
    let [aa, bb, cc, dd] = [a, b, c, d];

    for (let i = 0; i < 64; i += 1) {
      let f: number;
      let g: number;
      if (i < 16) {
        f = (bb & cc) | (~bb & dd);
        g = i;
      } else if (i < 32) {
        f = (dd & bb) | (~dd & cc);
        g = (5 * i + 1) % 16;
      } else if (i < 48) {
        f = bb ^ cc ^ dd;
        g = (3 * i + 5) % 16;
      } else {
        f = cc ^ (bb | ~dd);
        g = (7 * i) % 16;
      }
      const sum = (f + aa + sine[i] + words[g]) | 0;
      const rotated = (sum << shifts[i]) | (sum >>> (32 - shifts[i]));
      aa = dd;
      dd = cc;
      cc = bb;
      bb = (bb + rotated) | 0;
    }

    a = (a + aa) | 0;
    b = (b + bb) | 0;
    c = (c + cc) | 0;
    d = (d + dd) | 0;
  }

  // 输出为小端字节序的十六进制
  return [a, b, c, d]
    .map((word) => {
      const hex = (word >>> 0).toString(16).padStart(8, '0');
      return `${hex.slice(6, 8)}${hex.slice(4, 6)}${hex.slice(2, 4)}${hex.slice(0, 2)}`;
    })
    .join('');
}
