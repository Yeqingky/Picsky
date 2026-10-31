import type { ImageRecord } from '../../api/types';
import { LazyImage } from '../LazyImage';
import { Badge } from '../ui/badge';
import { Checkbox } from '../ui/checkbox';
import { cn } from '../../lib/utils';
import { formatBytes } from '../../utils/format';

export interface GalleryImageCardProps {
  record: ImageRecord;
  /** 由瀑布流网格按列宽与记录宽高算出的像素高度 */
  height: number;
  selectionMode: boolean;
  selected: boolean;
  onToggleSelect: (id: number) => void;
  onOpen: (record: ImageRecord) => void;
}

export function GalleryImageCard({
  record,
  height,
  selectionMode,
  selected,
  onToggleSelect,
  onOpen,
}: GalleryImageCardProps) {
  return (
    <div
      className={cn(
        'group relative overflow-hidden rounded-lg border transition-colors',
        selected ? 'border-primary' : 'border-border',
      )}
      style={{ height }}
    >
      <LazyImage
        src={record.url}
        alt={record.original_filename}
        hash={record.sha256}
        onClick={() => onOpen(record)}
      />

      <div className="pointer-events-none absolute inset-x-1 top-1 flex items-start justify-between">
        {selectionMode ? (
          <Checkbox
            checked={selected}
            onCheckedChange={() => onToggleSelect(record.id)}
            aria-label={record.original_filename}
            className="bg-card/90 pointer-events-auto rounded"
          />
        ) : (
          <span />
        )}
        <Badge variant="secondary" className="pointer-events-none opacity-85">
          {record.format}
        </Badge>
      </div>

      <div
        className={cn(
          'pointer-events-none absolute inset-x-0 bottom-0 bg-gradient-to-t from-black/70 to-transparent p-2 transition-opacity',
          selectionMode ? 'opacity-100' : 'opacity-0 group-hover:opacity-100',
        )}
      >
        <p className="truncate text-xs text-white">{record.original_filename}</p>
        <p className="text-[11px] text-white/75">
          {formatBytes(record.size)}
          {record.width && record.height ? ` - ${record.width}x${record.height}` : ''}
        </p>
      </div>
    </div>
  );
}
