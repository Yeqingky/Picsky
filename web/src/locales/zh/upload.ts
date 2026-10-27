/** 上传页文案 */
export const upload = {
  title: '上传图片',
  description:
    '拖拽到页面任意位置, 或按 Ctrl+V 粘贴, 也可以点击选择文件; 图片写入外部存储后由分发域名提供直链。',
  noPermission: {
    title: '当前账号没有上传权限',
    description: '请联系管理员为你所在的用户组开启 upload 权限位后再试。',
  },
  dropzone: {
    hint: '拖入文件到这里, 或点击选择文件',
    hintActive: '松开鼠标即可开始上传',
    pasteHint: '支持批量选择, 拖拽和 Ctrl+V 粘贴',
  },
  policy: {
    label: '存储策略',
    defaultHint: '默认策略',
    none: '当前用户组没有可用策略, 请联系管理员授权。',
  },
  summary: {
    total: '共 {{count}} 项',
    done: '成功 {{count}}',
    uploading: '上传中 {{count}}',
    queued: '排队 {{count}}',
    failed: '失败 {{count}}',
  },
  filter: {
    label: '筛选文件',
    all: '全部文件',
    done: '已完成',
    uploading: '上传中',
    queued: '排队中',
    failed: '失败',
    empty: '当前筛选下没有文件',
  },
  format: {
    label: '复制格式',
    url: 'URL',
    md: 'MD',
    html: 'HTML',
  },
  copyAll: '复制全部 ({{count}})',
  copyOne: '复制链接',
  preview: '查看大图',
  queue: {
    statusPending: '等待中',
    statusHashing: '校验中',
    statusDone: '完成',
    statusDuplicate: '已存在',
    statusFailed: '失败',
    retry: '重试',
  },
  error: {
    hashFailed: '无法计算文件校验值',
    uploadFailed: '上传失败',
    noPolicy: '请先选择存储策略',
    notImage: '已忽略非图片文件',
  },
};
