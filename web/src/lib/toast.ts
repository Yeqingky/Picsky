/**
 * 统一通知入口: 包装 sonner 的 toast, 为每种类型固定停留时长.
 * 成功 3s, 信息 4s, 警告与错误 5s; 调用方只传消息即可.
 * 视觉样式 (色调底, 图标与边框) 与位置由 index.css 与 toaster 容器统一控制.
 */
import { toast as sonnerToast } from 'sonner';

type Message = Parameters<typeof sonnerToast.success>[0];
type Options = Parameters<typeof sonnerToast.success>[1];

const SUCCESS = 3000;
const INFO = 4000;
const PROBLEM = 5000;

export const toast = {
  success: (message: Message, options?: Options) =>
    sonnerToast.success(message, { duration: SUCCESS, ...options }),
  info: (message: Message, options?: Options) =>
    sonnerToast.info(message, { duration: INFO, ...options }),
  warning: (message: Message, options?: Options) =>
    sonnerToast.warning(message, { duration: PROBLEM, ...options }),
  error: (message: Message, options?: Options) =>
    sonnerToast.error(message, { duration: PROBLEM, ...options }),
  dismiss: sonnerToast.dismiss,
};
