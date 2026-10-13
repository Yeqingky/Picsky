/** 通用文案: 动作, 状态, 表格, 确认框, 配额, 副本格式等 */
export const common = {
  appName: 'Picsky',
  loading: '加载中…',

  action: {
    save: '保存',
    saving: '保存中…',
    cancel: '取消',
    delete: '删除',
    deleting: '删除中…',
    edit: '编辑',
    create: '新建',
    creating: '新建中…',
    refresh: '刷新',
    close: '关闭',
    confirm: '确定',
    copy: '复制',
    copied: '已复制',
    copyFailed: '复制失败, 请手动选择文本',
    copyAll: '复制全部',
    copyAlias: '复制 {{alias}} 链接',
    copyMarkdown: '复制 Markdown',
    retry: '重试',
    search: '搜索',
    reset: '重置',
    back: '返回',
    view: '查看',
    detail: '详情',
    upload: '上传',
    add: '添加',
    remove: '移除',
    run: '执行',
    purge: '清理',
    test: '测试',
    submit: '提交',
    filter: '筛选',
    more: '更多',
    less: '收起',
    selectAll: '全选',
    clear: '清空',
    moveUp: '上移',
    moveDown: '下移',
    setDefault: '设为默认',
    logout: '退出登录',
  },

  state: {
    empty: '暂无数据',
    noMore: '没有更多了',
    loadFailed: '加载失败',
    notFound: '未找到',
    success: '操作成功',
    failed: '操作失败',
    pending: '等待',
    running: '进行中',
    done: '已完成',
    unknown: '未知',
  },

  table: {
    total: '共 {{count}} 条',
    pageSize: '每页 {{count}} 条',
    actions: '操作',
    noRows: '暂无数据',
    loading: '数据加载中',
    keyword: '关键字',
    prev: '上一页',
    next: '下一页',
    pageOf: '第 {{page}} / {{pages}} 页',
    selected: '已选 {{count}} 条',
  },

  confirm: {
    title: '确认操作',
    dangerTitle: '危险操作确认',
    irreversible: '此操作不可撤销, 请确认后继续。',
    deleteImage: '确定删除该图片记录?',
    batchDelete: '确定删除选中的 {{count}} 条记录?',
    deleteKey: '吊销后使用该密钥的客户端将立即失效, 确定继续?',
    deletePolicy: '删除策略后无法恢复, 引用该策略的记录不会被删除。确定继续?',
    deleteGroup: '删除用户组后无法恢复, 组内用户需重新分配。确定继续?',
    deleteUser: '删除用户会级联软删其图片记录, 确定继续?',
    purge: '将立即清理已到期的软删记录, 确定继续?',
    objectRetainHint: '外部存储不支持删除或已关闭删除时, 仅从 Picsky 删除记录, 外部文件保留。',
  },

  quota: {
    modeCount: '图片数量',
    modeBytes: '存储空间',
    unlimited: '不限',
    unset: '未设置',
    empty: '不限',
  },

  status: {
    enabled: '已启用',
    disabled: '已禁用',
    normal: '正常',
    banned: '已禁用',
    running: '运行中',
    pending: '待执行',
    done: '已完成',
    failed: '失败',
  },

  source: {
    web: '网页',
    api: '接口',
    scan: '扫描补录',
  },

  object: {
    deleted: '外部对象已删除',
    retained: '外部对象保留',
    deletedAt: '对象删除时间',
  },

  copyFormat: {
    label: '复制格式',
    direct: '直链',
    markdown: 'Markdown',
    html: 'HTML',
    bbcode: 'BBCode',
    json: 'JSON',
  },

  form: {
    required: '此项为必填',
    invalidEmail: '邮箱格式不正确',
    passwordTooShort: '密码至少 8 位',
    passwordMismatch: '两次输入的密码不一致',
    invalidNumber: '请输入有效数字',
    sizeUnit: '体积单位',
    invalidUrl: '请输入以 http:// 或 https:// 开头的地址',
    selectPlaceholder: '请选择',
    optional: '可选',
  },

  time: {
    range: '时间段',
    from: '开始时间',
    to: '结束时间',
    all: '全部时间',
    createdAt: '创建时间',
    updatedAt: '更新时间',
  },

  error: {
    network: '网络错误, 请检查连接后重试',
    forbidden: '你没有访问该页面的权限',
    unauthorized: '登录状态已失效, 请重新登录',
    notFound: '页面不存在',
    unknown: '发生未知错误',
  },

  theme: {
    label: '主题',
    light: '亮色',
    dark: '暗色',
    system: '跟随系统',
  },

  lang: {
    label: '语言',
    zh: '简体中文',
    en: 'English (未实现)',
  },

  captcha: {
    loadFailed: '人机验证控件加载失败, 请检查网络后重试',
  },

  skeleton: {
    pageHeader: '页面标题加载中',
    page: '页面加载中',
    table: '表格加载中',
    stats: '统计加载中',
    chart: '图表加载中',
    form: '表单加载中',
    settings: '设置加载中',
    list: '列表加载中',
    authCard: '登录表单加载中',
    gallery: '图片加载中',
    appended: '追加加载中',
  },

  a11y: {
    loading: '内容加载中',
    sidebar: '主导航',
    openMenu: '打开导航',
    closeMenu: '关闭导航',
  },

  status403: {
    title: '403 无权限',
    description: '当前账号没有访问该页面的权限, 如需授权请联系管理员。',
  },

  status404: {
    title: '404 页面不存在',
    description: '你访问的地址不存在或已被移除。',
  },
};
