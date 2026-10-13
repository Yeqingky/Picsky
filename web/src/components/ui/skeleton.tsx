import * as React from 'react';
import { cn } from 'cn';

/** 骨架屏基元: 扫光动画与主题色见 src/index.css 的 skeleton-wave */
function Skeleton({ className, ...props }: React.ComponentProps<'div'>) {
  return (
    <div
      data-slot="skeleton"
      aria-busy="true"
      className={cn('skeleton-wave rounded-sm', className)}
      {...props}
    />
  );
}

export { Skeleton };
