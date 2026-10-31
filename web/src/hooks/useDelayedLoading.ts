import { useEffect, useState } from 'react';
import { SKELETON_DELAY_MS, SKELETON_MIN_MS } from '../utils/constants';

/**
 * 局部刷新骨架: 延迟 200ms 才显示, 且展示时长不短于 300ms (docs/webui.md 第 3 节).
 * 路由级首屏应直接使用 loading, 不经此 Hook.
 */
export function useDelayedLoading(
  loading: boolean,
  delay = SKELETON_DELAY_MS,
  minDuration = SKELETON_MIN_MS,
): boolean {
  const [visible, setVisible] = useState(loading);
  const [shownAt, setShownAt] = useState<number | null>(loading ? Date.now() : null);

  useEffect(() => {
    if (loading) {
      if (visible) return;
      const timer = window.setTimeout(() => {
        setVisible(true);
        setShownAt(Date.now());
      }, delay);
      return () => window.clearTimeout(timer);
    }

    if (!visible) {
      setShownAt(null);
      return;
    }

    const elapsed = shownAt === null ? minDuration : Date.now() - shownAt;
    const remaining = Math.max(0, minDuration - elapsed);
    if (remaining === 0) {
      setVisible(false);
      setShownAt(null);
      return;
    }
    const timer = window.setTimeout(() => {
      setVisible(false);
      setShownAt(null);
    }, remaining);
    return () => window.clearTimeout(timer);
  }, [loading, visible, shownAt, delay, minDuration]);

  return visible;
}
