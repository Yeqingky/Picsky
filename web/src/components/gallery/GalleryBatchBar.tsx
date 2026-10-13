import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { Copy, Trash2, X } from 'lucide-react';
import type { ImageRecord } from '../../api/types';
import { Button } from '../ui/button';
import { copyText } from '../../utils/clipboard';

/** 批量操作条: 多选后固定在底部 */
export function GalleryBatchBar({
  records,
  canDelete,
  onClear,
  onExit,
  onDelete,
  deleting,
}: {
  records: ImageRecord[];
  /** 无 delete_own / delete_all 权限时不渲染删除入口 */
  canDelete: boolean;
  onClear: () => void;
  onExit: () => void;
  onDelete: () => void;
  deleting: boolean;
}) {
  const { t } = useTranslation();
  const [copying, setCopying] = useState(false);

  const copyLinks = async () => {
    setCopying(true);
    try {
      const text = records.map((record) => record.url).join('\n');
      const ok = await copyText(text);
      if (ok) {
        toast.success(t('common.action.copied'));
      } else {
        toast.error(t('common.action.copyFailed'));
      }
    } finally {
      setCopying(false);
    }
  };

  if (records.length === 0) return null;

  return (
    <div className="bg-card fixed bottom-4 left-1/2 z-40 flex max-w-[calc(100%-2rem)] -translate-x-1/2 flex-wrap items-center gap-3 rounded-lg border px-4 py-2 shadow-lg">
      <span className="text-sm font-medium">
        {t('gallery.selection.selected', { count: records.length })}
      </span>
      <div className="bg-border h-6 w-px" />
      <Button
        variant="outline"
        size="sm"
        disabled={copying}
        onClick={() => {
          void copyLinks();
        }}
      >
        <Copy className="size-4" />
        {t('gallery.selection.copyLinks')}
      </Button>
      {canDelete && (
        <Button variant="destructive" size="sm" onClick={onDelete} disabled={deleting}>
          <Trash2 className="size-4" />
          {t('gallery.selection.delete')}
        </Button>
      )}
      <Button variant="ghost" size="sm" onClick={onClear}>
        {t('gallery.selection.clear')}
      </Button>
      <Button variant="ghost" size="sm" onClick={onExit} aria-label={t('gallery.selection.exit')}>
        <X className="size-4" />
        {t('gallery.selection.exit')}
      </Button>
    </div>
  );
}
