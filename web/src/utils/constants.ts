/** 主题模式与各类本地存储键 (集中定义, 避免散落的魔法字符串) */
export const STORAGE_KEYS = {
  themeMode: 'picsky.theme-mode',
  galleryView: 'picsky.gallery-view',
  uploadPolicy: 'picsky.upload-policy',
  uploadSettings: 'picsky.upload-settings',
} as const;

export type GalleryView = 'masonry' | 'list';

/** 瀑布流容器参数 (docs/webui.md 4.1) */
export const MASONRY = {
  gap: 12,
  rowHeight: 8,
  columns: { xs: 2, sm: 3, md: 4, lg: 5, xl: 6 },
} as const;

/** 超过该数量时图库启用 react-virtuoso (docs/webui.md 4.3) */
export const VIRTUALIZE_THRESHOLD = 500;

/** 虚拟滚动分组大小: 每组内部仍使用瀑布流网格 */
export const VIRTUAL_CHUNK_SIZE = 60;

/** 追加数据时末尾骨架卡片数量 */
export const APPEND_SKELETON_COUNT = 3;

/** 局部刷新骨架: 延迟显示与最短展示时长 (ms) */
export const SKELETON_DELAY_MS = 200;
export const SKELETON_MIN_MS = 300;

/** 表格默认每页条数 */
export const DEFAULT_TABLE_PAGE_SIZE = 20;

/** 图库默认每页条数 (设置项 gallery_page_size 缺省值) */
export const DEFAULT_GALLERY_PAGE_SIZE = 40;

/** 占位色板 (亮色 / 暗色), 由 sha256 高位映射 (docs/webui.md 4.2) */
export const PLACEHOLDER_PALETTE = {
  light: [
    '#dbe4f0',
    '#e3ddf2',
    '#f0dde6',
    '#f2e2d6',
    '#e6efd8',
    '#d6efea',
    '#d9e8f2',
    '#eee0f0',
    '#f0e6d2',
    '#dfe6da',
    '#e8dde0',
    '#d8e2ec',
  ],
  dark: [
    '#2b3440',
    '#332f42',
    '#402f38',
    '#3d342c',
    '#303d2a',
    '#25403c',
    '#283a44',
    '#3a2f42',
    '#403a2c',
    '#333f31',
    '#3d3136',
    '#2c3844',
  ],
} as const;

/** 允许上传与筛选的图片格式 (docs/open-questions.md Q20, 不含 svg) */
export const IMAGE_FORMATS = [
  'jpeg',
  'png',
  'gif',
  'webp',
  'bmp',
  'tiff',
  'avif',
  'heic',
  'ico',
] as const;
