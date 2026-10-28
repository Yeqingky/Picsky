import { ImageDown, SlidersHorizontal } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import {
  DEFAULT_QUALITY_BY_FORMAT,
  QUALITY_MAX,
  QUALITY_MIN,
  updateUploadSettings,
  useUploadSettings,
} from '../../hooks/useUploadSettings';
import type { CompressionFailure, UploadOutputFormat } from '../../hooks/useUploadSettings';
import { Button } from '../ui/button';
import { Tooltip, TooltipContent, TooltipTrigger } from '../ui/tooltip';
import { Label } from '../ui/label';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../ui/select';
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from '../ui/sheet';
import { Switch } from '../ui/switch';

const FORMAT_OPTIONS: readonly UploadOutputFormat[] = ['jpg', 'jpeg', 'webp', 'avif'];

/** 页头“压缩”按钮: 右侧抽屉展示压缩参数 (docs/webui.md 第 6 节) */
export function CompressionSettingsButton() {
  const { t } = useTranslation();
  const settings = useUploadSettings();

  return (
    <Sheet>
      <Tooltip>
        <TooltipTrigger asChild>
          <SheetTrigger asChild>
            <Button
              type="button"
              variant="ghost"
              size="icon"
              aria-label={t('upload.compression.button')}
            >
              <ImageDown className="size-4" />
            </Button>
          </SheetTrigger>
        </TooltipTrigger>
        <TooltipContent>{t('upload.compression.button')}</TooltipContent>
      </Tooltip>
      <SheetContent side="right" className="gap-0" aria-describedby={undefined}>
        <SheetHeader className="border-b p-4 pr-12">
          <SheetTitle>{t('upload.compression.title')}</SheetTitle>
        </SheetHeader>
        <div className="flex flex-1 flex-col gap-5 overflow-y-auto p-4">
          <div className="flex items-start justify-between gap-4">
            <div className="min-w-0">
              <Label htmlFor="upload-compression-enabled" className="font-normal">
                {t('upload.compression.enable')}
              </Label>
              <p className="text-muted-foreground mt-1 text-xs">
                {t('upload.compression.enableHint')}
              </p>
            </div>
            <Switch
              id="upload-compression-enabled"
              checked={settings.compression}
              onCheckedChange={(checked) => updateUploadSettings({ compression: checked })}
            />
          </div>

          <div className="flex flex-col gap-2">
            <div className="flex items-center justify-between gap-3">
              <Label htmlFor="upload-compression-quality">{t('upload.compression.quality')}</Label>
              <span className="text-sm tabular-nums">{settings.quality}</span>
            </div>
            <input
              id="upload-compression-quality"
              type="range"
              min={QUALITY_MIN}
              max={QUALITY_MAX}
              step={1}
              value={settings.quality}
              disabled={!settings.compression}
              className="accent-primary w-full disabled:opacity-50"
              onChange={(event) => updateUploadSettings({ quality: Number(event.target.value) })}
            />
            <p className="text-muted-foreground text-xs">
              {t('upload.compression.qualityHint', { min: QUALITY_MIN, max: QUALITY_MAX })}
            </p>
          </div>

          <div className="flex items-start justify-between gap-4">
            <div className="min-w-0">
              <Label htmlFor="upload-compression-keep-original" className="font-normal">
                {t('upload.compression.keepOriginal')}
              </Label>
              <p className="text-muted-foreground mt-1 text-xs">
                {t('upload.compression.keepOriginalHint')}
              </p>
            </div>
            <Switch
              id="upload-compression-keep-original"
              checked={settings.keepOriginal}
              disabled={!settings.compression}
              onCheckedChange={(checked) => updateUploadSettings({ keepOriginal: checked })}
            />
          </div>

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="upload-compression-format">{t('upload.compression.format')}</Label>
            <Select
              value={settings.format}
              disabled={!settings.compression}
              onValueChange={(value) => {
                const format = value as UploadOutputFormat;
                updateUploadSettings({ format, quality: DEFAULT_QUALITY_BY_FORMAT[format] });
              }}
            >
              <SelectTrigger id="upload-compression-format" className="w-full sm:w-36">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {FORMAT_OPTIONS.map((format) => (
                  <SelectItem key={format} value={format}>
                    {format.toUpperCase()}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          <div className="flex items-start justify-between gap-4">
            <div className="min-w-0">
              <div className="flex flex-wrap items-center gap-2">
                <Label htmlFor="upload-compression-animated" className="font-normal">
                  {t('upload.compression.animated')}
                </Label>
                <span className="text-muted-foreground rounded-full border px-2 py-0.5 text-[11px]">
                  {t('upload.compression.animatedTarget')}
                </span>
              </div>
              <p className="text-muted-foreground mt-1 text-xs">
                {t('upload.compression.animatedHint')}
              </p>
            </div>
            <Switch
              id="upload-compression-animated"
              checked={settings.animated}
              disabled={!settings.compression}
              onCheckedChange={(checked) => updateUploadSettings({ animated: checked })}
            />
          </div>

          <div className="text-muted-foreground flex flex-col gap-1 text-xs">
            <span>{t('upload.compression.animatedSizeHint')}</span>
            <span aria-hidden="true" className="mx-auto size-1 rounded-full bg-current" />
            <span>{t('upload.compression.transparencyHint')}</span>
          </div>
        </div>
      </SheetContent>
    </Sheet>
  );
}

/** 页头“设置”按钮: 自动上传与压缩失败处理 */
export function UploadSettingsButton() {
  const { t } = useTranslation();
  const settings = useUploadSettings();

  return (
    <Sheet>
      <Tooltip>
        <TooltipTrigger asChild>
          <SheetTrigger asChild>
            <Button
              type="button"
              variant="ghost"
              size="icon"
              aria-label={t('upload.settings.button')}
            >
              <SlidersHorizontal className="size-4" />
            </Button>
          </SheetTrigger>
        </TooltipTrigger>
        <TooltipContent>{t('upload.settings.button')}</TooltipContent>
      </Tooltip>
      <SheetContent side="right" className="gap-0">
        <SheetHeader className="border-b p-4 pr-12">
          <SheetTitle>{t('upload.settings.title')}</SheetTitle>
          <SheetDescription>{t('upload.settings.description')}</SheetDescription>
        </SheetHeader>
        <div className="flex flex-1 flex-col gap-5 overflow-y-auto p-4">
          <div className="flex items-start justify-between gap-4">
            <div className="min-w-0">
              <Label htmlFor="upload-settings-auto" className="font-normal">
                {t('upload.settings.autoUpload')}
              </Label>
              <p className="text-muted-foreground mt-1 text-xs">
                {t('upload.settings.autoUploadHint')}
              </p>
            </div>
            <Switch
              id="upload-settings-auto"
              checked={settings.autoUpload}
              onCheckedChange={(checked) => updateUploadSettings({ autoUpload: checked })}
            />
          </div>

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="upload-settings-failure">{t('upload.settings.failure')}</Label>
            <Select
              value={settings.failure}
              onValueChange={(value) =>
                updateUploadSettings({ failure: value as CompressionFailure })
              }
            >
              <SelectTrigger id="upload-settings-failure" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="upload">{t('upload.settings.failureUpload')}</SelectItem>
                <SelectItem value="skip">{t('upload.settings.failureSkip')}</SelectItem>
              </SelectContent>
            </Select>
            <p className="text-muted-foreground text-xs">
              {settings.failure === 'skip'
                ? t('upload.settings.failureSkipHint')
                : t('upload.settings.failureHint')}
            </p>
          </div>
        </div>
      </SheetContent>
    </Sheet>
  );
}
