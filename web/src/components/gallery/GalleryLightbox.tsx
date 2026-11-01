import { useCallback, useEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { Info, X } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import PhotoSwipe from 'photoswipe';
import 'photoswipe/style.css';
import './gallery-lightbox.css';
import type { DeleteResult, ImageRecord } from '../../api/types';
import { useSiteInfo } from '../../hooks/useSite';
import { referrerPolicyFor } from '../../utils/referrer';
import { Button } from '../ui/button';
import { ImageDetailPanel } from './ImageDetailPanel';

/** 桌面查看器右侧信息栏宽度; 与 PhotoSwipe 的 padding 保持一致 */
const SIDEBAR_WIDTH = 400;

export interface GalleryLightboxProps {
  items: ImageRecord[];
  initialIndex: number;
  onClose: () => void;
  onDeleted?: (result: DeleteResult) => void;
  onFilterUser?: (email: string) => void;
}

/**
 * 图库图片查看器: PhotoSwipe 负责缩放, 手势与左右切换;
 * 桌面端在右侧固定信息栏, 移动端用底部按钮打开详情面板.
 */
export function GalleryLightbox({
  items,
  initialIndex,
  onClose,
  onDeleted,
  onFilterUser,
}: GalleryLightboxProps) {
  const { t } = useTranslation();
  const { data: siteInfo } = useSiteInfo();
  const referrerAllowlistRef = useRef<string[]>([]);
  referrerAllowlistRef.current = siteInfo?.referrer_allowlist ?? [];
  const viewerItems = useRef(items).current;
  const initial = useRef({
    index: Math.min(Math.max(initialIndex, 0), Math.max(viewerItems.length - 1, 0)),
  }).current;
  const [currentIndex, setCurrentIndex] = useState(initial.index);
  const [mobileDetailOpen, setMobileDetailOpen] = useState(false);
  const pswpRef = useRef<PhotoSwipe | null>(null);
  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;

  useEffect(() => {
    const pswp = new PhotoSwipe({
      dataSource: viewerItems.map((record) => ({
        src: record.url,
        width: record.width ?? 1600,
        height: record.height ?? 1200,
        alt: record.original_filename,
      })),
      index: initial.index,
      bgOpacity: 0,
      showHideAnimationType: 'fade',
      preload: [2, 2],
      paddingFn: () => ({
        top: 16,
        bottom: 16,
        left: 16,
        right: window.innerWidth >= 768 ? SIDEBAR_WIDTH + 16 : 16,
      }),
      trapFocus: false,
      wheelToZoom: true,
      closeOnVerticalDrag: true,
      pinchToClose: true,
      imageClickAction: 'zoom',
      tapAction: 'toggle-controls',
      doubleTapAction: 'zoom',
      escKey: true,
      arrowKeys: true,
      returnFocus: true,
    });
    pswpRef.current = pswp;

    const handleChange = () => setCurrentIndex(pswp.currIndex);
    const handleClose = () => onCloseRef.current();
    const handleContentLoadImage = (event: {
      content: { element?: HTMLElement; data?: { src?: unknown } };
    }) => {
      const element = event.content.element;
      if (element instanceof HTMLImageElement) {
        const source = typeof event.content.data?.src === 'string' ? event.content.data.src : '';
        element.referrerPolicy = referrerPolicyFor(source, referrerAllowlistRef.current);
      }
    };
    pswp.on('change', handleChange);
    pswp.on('close', handleClose);
    pswp.on('contentLoadImage', handleContentLoadImage);
    pswp.init();

    return () => {
      pswp.off('change', handleChange);
      pswp.off('close', handleClose);
      pswp.off('contentLoadImage', handleContentLoadImage);
      const element = pswp.element;
      // PhotoSwipe 的 destroy 第一次只触发关闭, 关闭态下再调用一次才会移除 DOM
      pswp.destroy();
      pswp.destroy();
      element?.remove();
      pswpRef.current = null;
    };
  }, [initial.index, viewerItems]);

  const requestClose = useCallback(() => {
    const pswp = pswpRef.current;
    if (pswp?.isOpen) {
      pswp.close();
      return;
    }
    onCloseRef.current();
  }, []);

  const current = viewerItems[currentIndex];
  if (!current) return null;

  return createPortal(
    <>
      <div className="fixed inset-0 z-[99999] bg-black/30 backdrop-blur-2xl" aria-hidden="true" />

      <div className="bg-popover fixed inset-y-0 right-0 z-[100001] hidden w-[400px] flex-col border-l md:flex">
        <div className="flex items-center justify-between border-b px-4 py-3">
          <h2 className="font-heading text-base font-medium">{t('gallery.detail.title')}</h2>
          <Button
            variant="ghost"
            size="icon-sm"
            onClick={requestClose}
            aria-label={t('common.action.close')}
          >
            <X className="size-4" />
          </Button>
        </div>
        <ImageDetailPanel
          imageId={current.id}
          open
          onClose={requestClose}
          onDeleted={onDeleted}
          onFilterUser={onFilterUser}
          showPreview={false}
          className="min-h-0 flex-1"
        />
      </div>

      <Button
        size="sm"
        className="fixed bottom-6 left-1/2 z-[100001] -translate-x-1/2 shadow-lg md:hidden"
        onClick={() => setMobileDetailOpen(true)}
      >
        <Info className="size-4" />
        {t('gallery.detail.title')}
      </Button>

      {mobileDetailOpen && (
        <>
          <div
            className="fixed inset-0 z-[100002] bg-black/40 md:hidden"
            onClick={() => setMobileDetailOpen(false)}
          />
          <div className="bg-popover fixed inset-x-0 bottom-0 z-[100003] flex max-h-[85dvh] flex-col rounded-t-xl border-t md:hidden">
            <div className="flex items-center justify-between border-b px-4 py-3">
              <h2 className="font-heading text-base font-medium">{t('gallery.detail.title')}</h2>
              <Button
                variant="ghost"
                size="icon-sm"
                onClick={() => setMobileDetailOpen(false)}
                aria-label={t('common.action.close')}
              >
                <X className="size-4" />
              </Button>
            </div>
            <ImageDetailPanel
              imageId={current.id}
              open
              onClose={requestClose}
              onDeleted={onDeleted}
              onFilterUser={onFilterUser}
              showPreview={false}
              className="min-h-0 flex-1"
            />
          </div>
        </>
      )}
    </>,
    document.body,
  );
}
