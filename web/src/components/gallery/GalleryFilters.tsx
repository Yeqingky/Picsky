import { useTranslation } from 'react-i18next';
import type { PolicySummary } from '../../api/types';
import { Search as SearchIcon } from 'lucide-react';
import { IMAGE_FORMATS } from '../../utils/constants';
import { Button } from '../ui/button';
import { Input } from '../ui/input';
import { Label } from '../ui/label';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../ui/select';

export interface GalleryFiltersValue {
  keyword: string;
  format: string;
  policyId: number | null;
}

/** Radix Select 不支持空字符串值, 用哨兵表示 "全部" */
const ALL_FORMATS = '__all_formats__';
const ALL_POLICIES = '__all_policies__';

/** 图库筛选: 原始文件名 / 格式 / 存储策略 */
export function GalleryFilters({
  value,
  policies,
  onChange,
  onClear,
}: {
  value: GalleryFiltersValue;
  policies: PolicySummary[];
  onChange: (patch: Partial<GalleryFiltersValue>) => void;
  onClear: () => void;
}) {
  const { t } = useTranslation();
  const hasFilters = value.keyword !== '' || value.format !== '' || value.policyId !== null;

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
      <div className="flex w-full flex-col gap-1.5 md:w-44">
        <Label htmlFor="gallery-filter-policy">{t('gallery.filters.storagePolicy')}</Label>
        <Select
          value={value.policyId === null ? ALL_POLICIES : String(value.policyId)}
          onValueChange={(next) =>
            onChange({ policyId: next === ALL_POLICIES ? null : Number(next) })
          }
        >
          <SelectTrigger id="gallery-filter-policy" className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={ALL_POLICIES}>{t('gallery.filters.allPolicies')}</SelectItem>
            {policies.map((policy) => (
              <SelectItem key={policy.id} value={String(policy.id)}>
                {policy.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>

      {hasFilters && (
        <Button variant="ghost" size="sm" onClick={onClear}>
          {t('gallery.filters.clear')}
        </Button>
      )}
    </div>
  );
}
