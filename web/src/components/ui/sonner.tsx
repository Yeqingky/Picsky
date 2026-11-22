import { Toaster as Sonner } from 'sonner';
import type { ToasterProps } from 'sonner';
import { CircleAlert, CircleCheck, Info, Loader2Icon, TriangleAlert } from 'lucide-react';
import { useThemeMode } from '@/theme/ThemeModeProvider';

/** 同时存在的通知上限, 超出时自动挤掉最早的一条 */
const MAX_NOTICES = 5;

/**
 * 顶部居中的通知容器: 收起的通知向下叠放, 悬停展开.
 * 色调, 图标与时长由 index.css 的 `[data-sonner-toast]` 规则按类型统一控制,
 * 调用方直接使用 toast.success / info / warning / error.
 */
function Toaster({ ...props }: ToasterProps) {
  const { resolvedMode } = useThemeMode();

  return (
    <Sonner
      theme={resolvedMode}
      position="top-center"
      gap={8}
      offset={16}
      visibleToasts={MAX_NOTICES}
      closeButton={false}
      containerAriaLabel="通知"
      className="toaster group sonner-shell"
      icons={{
        success: <CircleCheck className="size-4" />,
        info: <Info className="size-4" />,
        warning: <TriangleAlert className="size-4" />,
        error: <CircleAlert className="size-4" />,
        loading: <Loader2Icon className="size-4 animate-spin" />,
      }}
      style={
        {
          '--width': '340px',
        } as React.CSSProperties
      }
      {...props}
    />
  );
}

export { Toaster };
