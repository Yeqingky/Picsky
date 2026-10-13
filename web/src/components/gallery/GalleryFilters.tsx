import { useTranslation } from 'react-i18next';
import type { Dayjs } from 'dayjs';
import { Search as SearchIcon } from 'lucide-react';
import { IMAGE_FORMATS } from '../../utils/constants';
import { DatePicker } from '../DatePicker';
import { Button } from '../ui/button';
import { Input } from '../ui/input';
import { Label } from '../ui/label';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../ui/select';

export interface GalleryFiltersValue {
  keyword: string;
  format: string;
  from: Dayjs | null;
  to: Dayjs | null;
}

/** Radix Select 不支持空字符串值, 用哨兵表示 "全部格式" */
const ALL_FORMATS = '__all__';

/** 图库筛选: 只做 原始文件名关键字 / 格式 / 时间段 (docs/open-questions.md Q10) */
export function GalleryFilters({
  value,
  onChange,
  onClear,
}: {
  value: GalleryFiltersValue;
  onChange: (patch: Partial<GalleryFiltersValue>) => void;
  onClear: () => void;
}) {
  const { t } = useTranslation();
  const hasFilters =
    value.keyword !== '' || value.format !== '' || value.from !== null || value.to !== null;

  return (
    <div className="mb-4 flex flex-wrap items-end gap-3">
      <div className="flex w-full flex-col gap-1.5 md:w-64">
        <Label htmlFor="gallery-filter-keyword">{t('gallery.filters.keyword')}</Label>
        <div className="relative">
          <SearchIcon className="text-muted-foreground pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2" />
          <Input
            id="gallery-filter-keyword"
            className="pl-8"
            value={value.keyword}
            onChange={(event) => onChange({ keyword: event.target.value })}
            placeholder={t('gallery.filters.keywordPlaceholder')}
          />
        </div>
      </div>

      <div className="flex w-full flex-col gap-1.5 md:w-36">
        <Label htmlFor="gallery-filter-format">{t('gallery.filters.format')}</Label>
        <Select
          value={value.format === '' ? ALL_FORMATS : value.format}
          onValueChange={(next) => onChange({ format: next === ALL_FORMATS ? '' : next })}
        >
          <SelectTrigger id="gallery-filter-format" className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={ALL_FORMATS}>{t('gallery.filters.allFormats')}</SelectItem>
            {IMAGE_FORMATS.map((format) => (
              <SelectItem key={format} value={format}>
                {format}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>

      <DatePicker
        label={t('common.time.from')}
        value={value.from}
        onChange={(next) => onChange({ from: next })}
        clearable
        className="w-full md:w-44"
      />
      <DatePicker
        label={t('common.time.to')}
        value={value.to}
        onChange={(next) => onChange({ to: next })}
        clearable
        className="w-full md:w-44"
      />

      <Button variant="ghost" size="sm" onClick={onClear} disabled={!hasFilters}>
        {t('gallery.filters.clear')}
      </Button>
    </div>
  );
}
