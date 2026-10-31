import { useEffect, useRef, useState } from 'react';
import { Info } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Popover, PopoverAnchor, PopoverContent } from '../ui/popover';

/** 存储策略提示: 悬浮或点击信息图标时显示策略配置的提示文字 */
export function PolicyHint({ text }: { text: string }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const pinnedRef = useRef(false);
  const closeTimerRef = useRef<number | null>(null);

  const cancelClose = () => {
    if (closeTimerRef.current !== null) {
      window.clearTimeout(closeTimerRef.current);
      closeTimerRef.current = null;
    }
  };

  const scheduleClose = () => {
    if (pinnedRef.current) return;
    cancelClose();
    closeTimerRef.current = window.setTimeout(() => {
      closeTimerRef.current = null;
      setOpen(false);
    }, 120);
  };

  const show = () => {
    cancelClose();
    setOpen(true);
  };

  const handleOpenChange = (next: boolean) => {
    setOpen(next);
    if (!next) {
      pinnedRef.current = false;
      cancelClose();
    }
  };

  const togglePinned = () => {
    cancelClose();
    pinnedRef.current = !pinnedRef.current;
    setOpen(pinnedRef.current);
  };

  useEffect(
    () => () => {
      if (closeTimerRef.current !== null) window.clearTimeout(closeTimerRef.current);
    },
    [],
  );

  return (
    <Popover open={open} onOpenChange={handleOpenChange}>
      <PopoverAnchor asChild>
        <button
          type="button"
          className="text-muted-foreground hover:text-foreground inline-flex size-8 shrink-0 items-center justify-center rounded-md"
          aria-label={t('upload.policy.hintLabel')}
          aria-expanded={open}
          onMouseEnter={show}
          onMouseLeave={scheduleClose}
          onFocus={show}
          onBlur={scheduleClose}
          onClick={togglePinned}
        >
          <Info className="size-4" />
        </button>
      </PopoverAnchor>
      <PopoverContent
        side="bottom"
        align="end"
        sideOffset={8}
        className="w-64 text-sm whitespace-pre-wrap"
        onMouseEnter={show}
        onMouseLeave={scheduleClose}
        onOpenAutoFocus={(event) => event.preventDefault()}
      >
        {text}
      </PopoverContent>
    </Popover>
  );
}
