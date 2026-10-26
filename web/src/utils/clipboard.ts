/** 复制到剪贴板; 返回是否成功 (非安全上下文或权限被拒时退化为 execCommand) */
export async function copyText(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch {
    // 继续尝试后备方案
  }

  try {
    const textarea = document.createElement('textarea');
    textarea.value = text;
    textarea.setAttribute('readonly', '');
    textarea.style.position = 'fixed';
    textarea.style.opacity = '0';
    document.body.append(textarea);
    textarea.select();
    const ok = document.execCommand('copy');
    textarea.remove();
    return ok;
  } catch {
    return false;
  }
}

export type CopyFormat = 'direct' | 'markdown' | 'html';

export interface CopyFormatInput {
  url: string;
  filename?: string;
}

function escapeHtmlAttribute(value: string): string {
  return value.replace(/[&<>"']/g, (character) => {
    switch (character) {
      case '&':
        return '&amp;';
      case '<':
        return '&lt;';
      case '>':
        return '&gt;';
      case '"':
        return '&quot;';
      case "'":
        return '&#39;';
      default:
        return character;
    }
  });
}

/** 按目标格式渲染复制文本 (docs/webui.md 第 6 节复制格式菜单) */
export function renderCopyText(format: CopyFormat, input: CopyFormatInput): string {
  const { url, filename } = input;
  const name = filename ?? '';
  switch (format) {
    case 'direct':
      return url;
    case 'markdown':
      return `![${name}](${url})`;
    case 'html':
      return `<img src="${escapeHtmlAttribute(url)}" alt="${escapeHtmlAttribute(name)}" />`;
  }
}
