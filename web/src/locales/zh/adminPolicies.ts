/** 存储策略 (列表 + 编辑) 文案: docs/webui.md 第 8 节, docs/storage-policy.md 第 4/5/6/8 节 */
export const adminPolicies = {
  list: {
    title: '存储策略',
    description: '配置后端存储、魔法路径、分发域名与冲突策略; 策略由用户组授权后对组内用户可见。',
    create: '新建策略',
    scan: '扫描补录',
    scanSubmitted: '已提交扫描任务。',
    scanUnsupported: '该策略不支持扫描补录 (需要目录列举与内容读取能力)。',
    loadFailed: '存储策略加载失败',
    empty: '暂无存储策略',
    emptyHint: '新建一条策略后, 可在用户组中把策略加入白名单授权给用户。',
    deleteTitle: '删除策略 "{{name}}"?',
    urlsCount: '{{count}} 个域名',
    col: {
      name: '名称',
      type: '后端类型',
      enabled: '状态',
      conflict: '冲突策略',
      urls: '分发域名',
      capabilities: '后端能力',
      sort: '排序',
      updatedAt: '更新时间',
      actions: '操作',
    },
  },

  type: {
    webdav: 'WebDAV',
    s3: 'S3 兼容',
    aliyun_oss: '阿里云 OSS',
    tencent_cos: '腾讯云 COS',
    qiniu_kodo: '七牛云 Kodo',
    upyun: '又拍云',
  },

  conflict: {
    rename: 'rename (重命名)',
    skip: 'skip (跳过)',
    renameHint:
      '模板含 {seq} 时从已占用最大序号 + 1 开始; 冲突后只递增序号, 其他变量保持不变. 未使用 {seq} 时在文件名主体后追加 -1、-2 等数字。',
    skipHint: '同键对象内容相同则复用; 内容不同时返回冲突, 不覆盖对象。',
  },

  capability: {
    delete: '删除',
    overwrite: '覆盖',
    stat: '对象查询',
    list: '目录列举',
    read: '内容读取',
    mkdir: '创建目录',
    presign: '预签名',
    multipart: '分片上传',
  },

  edit: {
    titleNew: '新建存储策略',
    titleEdit: '编辑存储策略: {{name}}',
    descriptionNew: '新建时预填系统设置中的默认魔法路径与默认冲突策略; 后端类型创建后不可修改。',
    descriptionEdit:
      '修改分发域名或魔法路径后, 可在基本信息 / 分发域名标签下用对照表核对对象键与域名路径是否指向同一对象。',
    back: '返回列表',
    loadFailed: '存储策略加载失败',
    invalidId: '策略 ID 无效',
    presetFailed: '读取系统设置失败, 已使用内置默认值 ({{template}} / {{conflict}})。',
    save: '保存',
    created: '已创建策略 {{name}}',
    updated: '已保存策略 {{name}}',

    section: {
      basic: '基本信息',
      basicHint: '策略标识、后端类型与启用状态。',
      connection: '后端连接',
      connectionHint: '外部存储的服务地址与访问凭据; 凭据加密保存, 仅管理员可见。',
      path: '魔法路径',
      pathHint: '每个文件的相对路径生成规则, 必须包含内容哈希变量或 {seq}。',
      urls: '分发域名',
      conflict: '冲突策略',
      conflictHint: '目标路径已存在时的处理方式; rename 使用递增序号, skip 不覆盖。',
      scan: '扫描补录',
      scanHint:
        '递归列举存储路径下的文件入库 (幂等); 已入库路径会跳过, 补录路径也会参与 {seq} 序号计算。',
      reference: '对象键与域名 URL 对照表',
      referenceHint: '用于核对对象键与各分发域名路径是否指向同一对象。',
    },

    field: {
      name: '策略名称',
      namePlaceholder: '例如 主站 WebDAV',
      type: '后端类型',
      typeReadonly: '后端类型创建后不可修改。',
      enabled: '启用策略',
      enabledHint: '禁用后不再对用户可见; 已存在记录的直链不受影响。',
      sort: '排序',
      sortHint: '数字越小越靠前。',
      endpoint: '服务地址 (endpoint)',
      endpointPlaceholder: 'https://127.0.0.1:5244/dav',
      endpointHint: 'WebDAV 服务地址, 需包含路径部分。',
      username: '用户名',
      password: '密码',
      passwordHint: '留空表示不修改已保存的凭据。',
      passwordShow: '显示密码',
      passwordHide: '隐藏密码',
      pathTemplate: '路径模板',
      pathTemplatePlaceholder: '{md5:2}/{md5}.{ext}',
      conflictPolicy: '冲突策略',
      reuseReleasedSeq: '复用已释放的 {seq}',
      reuseReleasedSeqHint:
        '删除图片后序号会写入释放队列; 开启后按释放顺序优先复用, 关闭时仍记录但不复用。',
      scanPath: '扫描路径',
      scanPathOptional: '可选; 留空表示从存储根目录开始扫描。',
      dryRun: '仅试运行 (dry run)',
      dryRunHint: '仅执行检查, 不写入数据库; 结果保存在任务记录中。',
    },

    error: {
      nameRequired: '请填写策略名称',
      endpointRequired: '请填写服务地址',
      templateRequired: '请填写路径模板',
      templateVariable:
        '路径模板必须包含内容哈希变量 ({md5} / {sha256} / {sha512} 或其截断形式) 或 {seq}',
      templateInvalid: '模板不能以 / 开头, 不能包含连续 // 或 ..',
      urlAliasRequired: '请填写别名',
      urlRequired: '请填写域名 URL',
      urlInvalid: '地址必须以 http:// 或 https:// 开头',
      urlsRequired: '至少需要配置一个分发域名',
      sortInvalid: '排序必须是整数',
      seqRequiresRename: '包含 {seq} 的路径模板只支持 rename 冲突策略',
      saveFailed: '保存失败',
    },

    url: {
      add: '添加域名',
      empty: '尚未配置分发域名, 至少需要一项。',
      aliasLabel: '别名',
      aliasPlaceholder: '融合CDN',
      urlLabel: '域名 URL',
      urlPlaceholder: 'https://cdn.example.com/image/',
      defaultHint:
        '列表第一项为默认域名; 上传响应 url 字段使用该域名拼接; 拖动左侧把手可调整顺序。',
      dragHandle: '拖动排序; 也可用上下方向键调整顺序',
      remove: '删除该项',
    },

    variable: {
      label: '变量快捷插入',
      insert: '在光标处插入 {{variable}}',
      md5: '完整 MD5 (32 位十六进制)',
      md5Short: 'MD5 前 {{length}} 位',
      sha256: '完整 SHA-256 (64 位十六进制)',
      sha256Short: 'SHA-256 前 {{length}} 位',
      sha512: '完整 SHA-512 (128 位十六进制)',
      ext: '扩展名, 由真实格式推导出的小写后缀',
      seq: '从 1 开始的递增序号; rename 会从已分配最大序号之后继续, 扫描补录参与计算, 删除图片的序号可复用',
      uuid: '随机 UUIDv4; 本次路径生成后固定, 序号递增时不变',
      rand: '随机 8 位小写字母数字; 本次路径生成后固定, 序号递增时不变',
      randShort: '随机 {{length}} 位小写字母数字',
      note: '模板需含内容哈希或 {seq}; 相同 SHA-256 会优先复用已有记录, 序号递增时其他变量保持不变。',
    },

    preview: {
      title: '实时预览',
      invalid: '填写包含内容哈希变量或 {seq} 的合法模板后显示预览。',
      failed: '预览失败',
      sampleNote: '预览使用示例值渲染: seq 1, md5 0f1e2d3c4b5a69788796a5b4c3d2e1f0, ext png。',
      urls: '拼接后的域名 URL',
    },

    conflictSeqHint:
      '包含 {seq} 的模板按策略原子分配递增序号, 序号跨输出格式共享, 扫描补录参与对账; 删除图片释放的序号可选择性复用。',

    conflictExample: {
      title: '示例:',
      noDomain: '尚未配置分发域名, 无法生成示例 URL。',
      existing: '已有对象',
      rename: '冲突后保存为',
      skip: '冲突后返回 409, 不覆盖已有对象; 内容相同则直接复用。',
      pending: '填写合法路径模板后显示示例。',
    },

    test: {
      button: '连接测试',
      running: '测试中…',
      ok: '连接正常, 耗时 {{duration}}',
      failed: '连接测试失败',
      needSave: '保存策略后才能进行连接测试。',
    },

    transfer: {
      import: '导入',
      export: '导出',
      exportTitle: '导出策略 JSON',
      exportHint: '导出当前表单内容 (含未保存的修改); 凭据以明文输出, 请妥善保管。',
      importTitle: '导入策略 JSON',
      importHint:
        '粘贴导出的 JSON 或管理接口返回的策略对象; 仅覆盖 JSON 中出现的字段, 保存后生效。',
      placeholder: '在此粘贴存储策略 JSON',
      apply: '导入',
      importDone: '已导入策略内容, 保存后生效',
      copyJson: '复制',
      error: {
        invalidJson: 'JSON 解析失败: {{detail}}',
        notObject: 'JSON 顶层必须是对象。',
        empty: 'JSON 中没有可识别的策略字段。',
        typeUnsupported: '不支持的存储后端类型: {{type}}。',
        field: '字段 {{field}} 格式不正确。',
      },
    },

    conflictDisabled: {
      stat: '该后端未声明 stat 能力, 冲突检查只按记录判断, 不查询对象是否存在。',
    },

    scan: {
      button: '开始扫描',
      running: '提交中…',
      backgroundRunningButton: '后台扫描中…',
      submitted: '扫描任务 #{{id}} 已提交。',
      processing: '扫描任务 #{{id}} 正在后台处理。',
      done: '扫描任务 #{{id}} 已完成。',
      taskFailed: '扫描任务 #{{id}} 失败: {{error}}',
      failed: '扫描失败',
      failedTitle: '失败明细 ({{count}})',
      q21: '扫描补录的路径会参与 {seq} 的最大序号计算; 非图片文件只占用路径、不进图库, 也不计配额 (kind=file)。',
    },

    reference: {
      relPath: '相对路径 rel_path',
      objectKey: '对象键 object_key',
      urls: '各域名完整直链',
      capabilities: '后端能力',
      pending: '等待预览结果…',
      emptyUrls: '尚未配置合法域名',
    },
  },
};
