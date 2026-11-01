# AGENTS.md

本文件是 Picsky 仓库的规则文件, 约束所有在本仓库中工作的代理与协作者.

## 项目概述

Picsky 是运行在服务器上的图片上传中间层与管理系统, 通过 WebUI 和 REST API 提供服务.
图片写入外部存储后端, 由外部 CDN 域名分发; 本服务不存储图片, 也不公开分发图片.

## 不可违背的约束

1. 不新增图片本地存储能力
   - 禁止引入本地文件系统存储驱动.
   - 临时缓冲文件只允许存在于上传处理期间, 必须在处理结束后删除.
2. 不新增公开访问能力
   - 禁止添加公开的图片内容路由或匿名数据接口.
   - 除健康检查, 登录与注册外, 所有接口必须认证.
   - 禁止为图片内容做代理或转发 (含图库预览与缩略图), 前端一律直连分发域名.

## 目录结构

```text
Picsky/
|-- cmd/picsky/     服务入口 (main)
|-- internal/
|   |-- api/        原生 REST handler 与路由
|   |-- apperr/     API 错误码
|   |-- config/     环境变量配置
|   |-- clientip/    请求 IP 来源探测与解析
|   |-- crypto/     AES-256-GCM 凭据加密
|   |-- db/         数据库连接 (SQLite WAL / PostgreSQL)
|   |-- hashing/    单遍多哈希
|   |-- httpupload/  multipart 上传解析
|   |-- imgdetect/  魔数格式探测与尺寸解析
|   |-- logx/       日志
|   |-- lsky/       Lsky Pro v1 兼容层
|   |-- middleware/ 认证与权限中间件
|   |-- model/      数据模型
|   |-- service/    业务逻辑 (上传, 图库, 策略, 用户组, 设置, 维护队列)
|   |-- spool/      上传缓冲 (内存 / 临时文件)
|   |-- storage/    适配器接口, 能力声明, 魔法路径渲染
|   |   |-- acgfaka/   异次元店铺系统驱动 (站点地址, 手动 Cookie 或账号登录, 仅上传)
|   |   |-- celine/    思琳中国驱动 (固定地址, 无配置, 仅上传)
|   |   |-- cloudreve/ Cloudreve v4 驱动 (账号密码登录, 上传)
|   |   |-- colorful/  彩虹外链网盘驱动 (站点地址, 公开上传, 仅上传)
|   |   |-- littlepan/ LittlePan 驱动 (站点地址, 公开上传与删除)
|   |   |-- metaso/    秘塔驱动 (固定地址与公开前缀, 无配置, 仅上传)
|   |   |-- oneimg/    初春图床驱动 (站点地址与 Token, 可选存储桶, 上传与远端缩略图)
|   |   |-- sobot/     Sobot 驱动 (固定租户, 无配置, 仅上传)
|   |   `-- webdav/    WebDAV 驱动 (v1)
|   `-- web/        go:embed 前端产物 (build/ 由 scripts/build.sh 生成)
|-- migrations/     数据库迁移与默认设置
|-- scripts/        构建脚本
|-- web/            前端工程 (独立 npm 项目, React + TypeScript + Vite + Tailwind CSS v4 + shadcn/ui, 产物输出 build/)
|   `-- src/
|       |-- components/ui/  shadcn 基元 (CLI 生成, 已排除 Prettier)
|       |-- components/     业务组件 (DataTable, DatePicker, TagsInput, States, gallery/, upload/ 等)
|       |-- lib/utils.ts    类名合并 (cn)
|       |-- index.css       全局样式与设计令牌 (语义颜色, 圆角, 骨架扫光)
|       `-- theme/          主题模式 (light / dark / system)
`-- docs/           项目文档, 设计的事实来源
```

## 文档同步规则

- 表结构, 接口, 配置项, 权限位, 环境变量发生变化时, 必须同步更新 `docs/` 下对应文档.
- 新增文档必须登记到 `README.md` 的文档索引.
- 未定稿事项记录在 `docs/open-questions.md`; 确认后并入正式设计文档并从清单移除.
- 文档语言为中文, 使用半角标点; 命令, 路径, 字段名按原文保留.

## 开发约定

- Git 提交身份: 用户名 `YeqingKy`, 邮箱 `me@yeqing.im`.
- GitHub Actions 容器工作流在所有分支 push 与 PR 上构建; 分支 push 发布 `ghcr.io/yeqingky/picsky:<短 hash>`, `vX.Y.Z` tag 发布版本 tag 与 `latest`; PR 仅构建不推送; GHCR 认证使用 `GITHUB_TOKEN`.
- 不把 `README` 用作变更日志; 变更日志写入仓库根目录的 `CHANGELOG.md` 并随版本提交, 每次发布在文件顶部追加版本段落, 版本倒序排列; 版本数量较多或单文件过长时改用 `CHANGELOG/` 目录, 每个版本一个文件.
- 存储适配器必须声明能力 (`upload`, `delete`, `overwrite`, `stat`, `presign`, `multipart`); 新增驱动不得假设后端支持删除或覆盖, 上层按能力降级. 驱动未声明 `delete` 时删除记录直接返回 403, 不删除记录与外部对象.
- `sobot` 驱动固定租户与上传地址, 无配置项, 只接受图片, 由后端返回直链 (`url_from_backend`), 只声明 `upload` 能力; 策略不配置分发域名, 记录 `rel_path` / `object_key` 取返回 URL 的路径.
- `celine` 驱动固定思琳中国上传地址与公开路径, 无鉴权与配置项, 只接受图片且仅支持 PNG / JPEG / GIF, 由后端返回直链 (`url_from_backend`), 只声明 `upload` 能力; 单文件上限 20,971,326 字节 (20 MiB 扣除 multipart 开销), 服务端会重编码图片并清理元数据, 记录的哈希与大小仍按原始上传内容计算.
- `metaso` 驱动固定秘塔上传地址与公开前缀, 无鉴权与配置项, 只接受图片, 由后端返回直链 (`url_from_backend`), 只声明 `upload` 能力; 请求体上限 200 MiB (209,715,200 字节), 驱动文件上限 209,715,000 字节 (扣除 multipart 开销), 超限请求不会提前返回 413, 而是收完整个请求后返回 `errCode` 500; 错误一律 HTTP 200 + `errCode`, 必须按 `errCode` 判定失败; 服务端会把图片转码为 WebP, 并把最长边超过 2048 px 的图片等比缩小到最长边 2048 px, 但图片响应仍声明 `image/png;charset=UTF-8`, 记录的尺寸, 哈希与大小按原始上传内容计算.
- `oneimg` 驱动对接初春图床 (OneImg): `config` 保存 `endpoint` 与可选 `bucket_id`, `secrets` 保存 `token`; 上传 `POST /api/upload/images`, multipart 字段为 `images[]`, 未选择存储桶时不发送 `bucket_id`, 后端返回的 `/uploads/...` 相对路径按站点地址补全为绝对直链; 声明 `upload`, `thumbnail`, `url_from_backend` 与 `url_prefix_override`; 策略可选配置分发域名, 配置默认域名时记录直链为默认域名拼接后端返回路径, 未配置时使用站点直链; 缩略图直链由原图同目录追加 `thumbnails/` 得到, 图库卡片与列表使用该地址, 查看器与详情使用原图; 初春 API 删除权限尚未完善, 当前不声明删除能力; 单文件上限与允许格式以实例 `/api/uploadConfig` 为准, 驱动不写死. 存储桶列表通过独立管理接口 `POST /admin/policies/buckets` 按需读取, 不并入策略详情或列表; 新建策略首次展开下拉框时请求, 编辑页加载后预取, 请求超时 15 秒, 站点地址或 Token 变化时清空已选存储桶; 远端查询失败只影响该字段提示, 不得阻塞页面其他数据.
- `colorful` 驱动对接彩虹外链网盘 5.6 的公开上传接口: `config` 保存 `endpoint`; 上传 `POST {endpoint}/api.php`, multipart 字段为 `file`, 文件名取魔法路径文件名; 站点必须开启上传 API, 无需登录; 响应 `code=0` 为成功, 优先使用 `viewurl` (inline), 缺失时回退 `downurl`; 只声明 `upload` 与 `url_from_backend`, 不支持扫描, 删除与分发域名.
- `littlepan` 驱动对接 LittlePan_v2 的公开上传接口: `config` 保存 `endpoint`; 上传 `POST {endpoint}/api.php`, multipart 字段 `file`, `format=json`, `show=0`; 无需登录; 响应 `code=0` 为成功, 优先使用 `viewurl`, 缺失时回退 `downurl`; 只声明 `upload` 与 `url_from_backend`, 不支持删除, 扫描, 状态查询与分发域名. 公开 API 的 DELETE 对已存在文件返回 5xx 且无授权模型; 前端删除依赖登录会话, CSRF 与 `$_SESSION['fileids']`, 只允许删除同一浏览器会话上传的文件, 无法由无状态 API 复用. 连接测试只上传探测图, 探测对象会保留.
- `acgfaka` 驱动对接异次元店铺系统 (acg-faka): `config` 保存 `endpoint` 与 `auto_login` (`true` / `false`, 默认 `false`), `secrets` 保存 `cookie` (USER_SESSION 原值或完整 Cookie), 自动登录时另存 `username` / `password` / `cookie_expires`; 上传 `POST {endpoint}/user/api/ticket/upload?mime=image`, multipart 字段 `file`, 必须携带与站点同主机的 `Referer` 和 Cookie; 响应 `code=200` 时 `data.url` / `data.path` 为站点内相对路径, 驱动按站点地址补全为绝对直链; 只声明 `upload` 与 `url_from_backend`, 不支持扫描, 删除, 状态查询与分发域名. 单文件上限 10 MiB (10,485,760 字节), 仅支持 PNG / JPEG / WebP.
- `acgfaka` 自动登录使用 `POST /user/api/authentication/login` (账号支持用户名, 邮箱或手机号), 支持 `42001` 两步验证并通过 `POST /user/api/authentication/totp` 完成; 登录后调用 `POST /user/api/security/deviceSessions` 读取 `current=true` 的 `expires_time` 作为 Cookie 到期时间. 站点必须关闭登录人机验证, 否则只能手动填写 Cookie. 上传返回会话失效时使用保存的账号密码重新登录并重试一次, 刷新后的 Cookie 在进程内缓存供后续请求复用; 两步验证账号在 Cookie 过期后无法自动刷新, 需重新登录. 管理后台手动模式提供 Cookie 验证按钮, 自动登录模式登录后 Cookie 输入框只读.
- `cloudreve` 驱动对接 Cloudreve v4: `config` 保存 `endpoint`, `directory` (默认 `/picsky`), `auto_login` (`true` / `false`, 默认 `false`), `secrets` 保存账号邮箱, 密码与 `refresh_token` / `refresh_expires` / `access_expires`; 自动登录关闭时只显示手填 Refresh Token 并调用刷新接口显示两个到期时间, 开启时显示账号密码并强制登录获取 Refresh Token, 输入框只读; 开启自动登录后 Refresh Token 刷新失败可回退账号密码登录; 账号密码登录支持 2FA; 登录成功后保存 RefreshToken 并默认启用策略, 未登录可保存但必须禁用, 列表启用开关在有令牌时才可用. 登录响应缺少 `direct_link_batch_size` 或为 0 时拒绝登录; 上传通过创建目录, 上传会话, 分片与 `PUT /file/source` 直链接口完成, 只支持本机, relay 或 remote 中转策略, 其他存储策略明确报错; 驱动声明 `upload`, `stat`, `delete`, 由后端返回直链; 删除通过 `DELETE /file` 将对象移入 Cloudreve 回收站 (`skip_soft_delete: false`), 文件不存在或已在回收站视为已删除, 不做物理删除; 魔法路径默认 `{md5:2}/{md5}.{ext}`, 实际对象键为目录加魔法路径, 记录 `rel_path` 不含目录而 `object_key` 含目录.
- 由后端下发直链 (`url_from_backend`) 的驱动不支持扫描补录, 扫描接口与列表按钮必须拒绝或禁用; 可扫描驱动在创建任务前执行可选预检 (`storage.ScanChecker`), WebDAV 使用根目录 PROPFIND 校验凭据, 预检失败不入队; 扫描任务最长运行 30 分钟.
- 魔法路径模板必须包含内容哈希变量或单个 `{seq}`, 且必须以 `.{ext}` 结尾; `{seq}` 只允许 `rename`, 使用策略级原子计数器分配并跨输出格式共享, 已有匹配路径与扫描补录参与初始化与对账; 删除图片且外部对象确认删除后序号写入释放队列, 策略开关 `reuse_released_seq` 控制是否按先进先出复用, 关闭时仍记录; 模板变化时重置计数器并清空队列; 未使用 `{seq}` 时冲突重命名追加 `-N`, 其他生成变量在重试中固定.
- 冲突策略只使用 `rename` / `skip`; `overwrite` 是后端能力声明, 不作为冲突策略.
- Lsky Pro 兼容层是适配层: 不得绕过认证, 权限, 配额与能力降级; 兼容字段有变更时先更新 `docs/lsky-compat.md`.
- 对外错误响应必须通过 `apperr.From` 归一; 所有 5xx 与 `backend_error` 对普通用户使用通用消息并移除 `Details`, 原始错误仅写服务端日志; 管理员请求 (任一管理权限) 返回未清洗的原始消息与 `Details` 便于排查; Lsky 5xx 返回实际状态码, 后端错误使用 502, 不按业务失败返回 HTTP 200.
- Lsky 上传在 multipart 文件缓冲前完成认证, 上传权限与限流预检; 仅使用表单 `token` 的请求要求它是首个 multipart 字段, 通过认证后按用户组文件大小上限读取.
- 原生 multipart 上传必须在解析请求体前完成认证, 上传权限与限流预检; 所有 multipart 非文件字段值每项最多 4 KiB, 最多 64 项, 字段名和值合计最多 64 KiB (Lsky `token` 计入预算).
- 非上传接口按路径限制 JSON 请求体 1 MiB, 不按 `Content-Type` 豁免; 携带请求体的非上传接口只接受 `application/json` (含 `application/*+json`), 其他媒体类型返回 415 `unsupported_media_type`; 上传接口按路径豁免并自行限额.
- Base64 JSON 上传必须在读取请求体前完成认证, 上传权限与限流预检, 并流式读取 `data` 直接解码到共用上传缓冲, 禁止在内存完整保留编码字符串或解码副本; 不限组仍受 2 GiB 单次硬上限约束, `data` 字段前后区段各最多 32 KiB, data URL 头最多 4 KiB, 解码缓冲遵循 `PICSKY_UPLOAD_MEMORY_LIMIT`.
- 驱动可声明单对象体积上限 (`max_file_size_bytes`, `0` 表示不限); 上传先校验用户组单文件上限, 通过后再校验策略驱动上限, 仅策略上限触发时返回 `file_too_large` 与 "此存储策略最大支持xxMB"; Sobot 固定 50 MiB (52,428,800 字节), Celine 20,971,326 字节 (20 MiB 扣除 multipart 开销), Metaso 209,715,000 字节 (200 MiB 扣除 multipart 开销), 异次元店铺系统 10,485,760 字节 (10 MiB), WebUI 以右下角 toast 提示.
- 驱动可声明支持的图片格式 (`formats`, 空表示不限); 上传在系统格式白名单后校验策略格式, 不匹配返回 `unsupported_format`; Celine 仅支持 `png` / `jpeg` / `gif`, 异次元店铺系统仅支持 `png` / `jpeg` / `webp`; 前端压缩对受限策略自动改用 JPEG, 未压缩原图的格式不支持时直接提示失败且不发请求.
- 未设置 `PICSKY_TEMP_DIR` 时, spool 可将处理期溢出文件写入系统临时目录, 但 `CleanupStale` 必须跳过目录扫描; 仅对应用专用且隔离的临时目录执行启动残留清理.
- `/upload/check` 必须按当前 `user_id` 查询记录; 相同哈希仅由其他用户持有时返回 `exists: false` 且不包含记录 ID, URL 或大小; `/upload` 跨用户复用只返回当前用户自己的新记录.
- 注册页启用注册邮箱域名白名单时, 邮箱字段由左侧本地部分输入框与右侧域名下拉框组成, 提交时拼接为完整邮箱; 未启用白名单时为完整邮箱输入框; 白名单域名由 `GET /site` 的 `registration_email_domains` 提供.
- 不实现本地人机验证: 只接入 Cap 与 Cloudflare Turnstile; 注册, 人机验证与邮箱验证都可切换. 邮箱验证开启时新注册账号状态为 `1` (未验证), 实际组为 `只读`, `restore_group_id` 保存验证后的目标组; 验证邮件经有界进程内队列异步发送, 验证后改为正常并恢复目标组. 未验证账号可登录与重发邮件但无上传等权限; 重发按账号每小时 3 次, 每来源 IP 每小时 10 次限流; 状态 `2` (封禁) 不可登录且使用只读组; `0` 为正常.
- 用户名去首尾空白后仅允许 1-64 位 ASCII 英文字母和数字; 邮箱写入, 登录, 找回与管理员邮箱搜索前统一去首尾空白并转小写; 登录只接受邮箱. 注册页用户名, 邮箱, 密码与确认密码校验提示显示在字段标签同行右侧; 用户名粘贴时过滤空格与非法字符, 邮箱输入自动转小写, 服务端仍须校验.
- 登录邮箱去首尾空白并转小写后最多 255 字节, 超长直接拒绝且不写审计; 审计详情序列化后超过 16 KiB 时替换为截断标记, 上传审计中的原始文件名最多 255 字节.
- 不存在邮箱, 封禁账号与密码错误必须返回相同 401 与通用提示; 不存在账号仍执行 bcrypt cost 12 虚拟散列校验. 公开注册的有效请求在查重前执行 bcrypt cost 12, 新建与重复请求统一返回相同 202 与通用响应, 且不下发会话 cookie.
- 启用 TOTP 的登录使用两步流程: 首次请求校验邮箱/密码与人机验证并返回短时挑战; 前端在模态框自动提交动态口令, 挑战必须一次性消费并绑定来源 IP / User-Agent; 启用 TOTP 必须验证当前密码, 登录动态口令计数器需原子消费且不可重放.
- 登录限流, TOTP 挑战, 审计与上传记录统一使用 `client_ip_source` 解析的地址; 默认使用 `REMOTE_ADDR`, 只有可信反向代理会覆盖所选请求头且应用端口不对公网开放时才配置代理头.
- 进程内限流器最多保存 10000 个键; 表满且无过期项时拒绝新键; 密码找回先检查来源 IP 键, 超限立即返回, 不再插入邮箱键.
- 会话 Cookie 在原生 TLS, HTTPS `site_url` 或 `X-Forwarded-Proto: https` 下必须设置 `Secure`; 反代场景要求可信代理覆盖该头且应用端口不对公网开放.
- 密码找回令牌必须限时, 一次性且绑定当前密码散列; 邮件链接只在 URL fragment 携带令牌, WebUI 立即清理地址栏并通过 POST body 提交; 重置密码与清除 TOTP 秘钥必须在同一事务中完成; 登录页仅在公开站点信息报告 SMTP 发信已配置时显示找回入口.
- 所有应用 HTTP 响应必须设置 `Referrer-Policy: no-referrer`; WebUI HTML 入口声明同等 meta 策略.
- 会话令牌必须绑定 `users.session_version`; 登出, 修改密码, 密码重置与管理员重置用户密码递增版本并吊销该账号的全部旧会话; Cookie 修改密码成功后为当前浏览器签发新会话.
- 维护任务 (队列) 必须幂等可重试, 已完成任务可手动重跑, 不得绕过权限与能力降级; 外部对象删除成功与失败都必须写入维护队列, 驱动必须校验删除响应; 失败的对象删除任务保留为 `failed`, 不自动重试也不自动清理, 必须由管理员手动重试或删除, 重试成功后释放 `{seq}`; 手动策略扫描必须立即创建任务并返回任务 ID, 由后台队列列举并持久化对象清单后读取解析, 前端通过任务状态接口查询状态; WebDAV 只是内部存储通道, 不得对外暴露.
- 内置用户组 (`管理员` / `只读`, 由 `groups.is_system` 标记) 的权限位固定, 不可删除, 不可设为默认组; 调整组规则时同步更新 `docs/permissions.md`.
- 删除用户组时必须同时检查 `users.group_id` 与 `users.restore_group_id`, 避免验证或解封后恢复到已删除的用户组.
- 后台区域入口按任一管理权限位判定 (`view_all`, `delete_all`, `manage_policies`, `manage_users`, `manage_groups`, `manage_settings`, `view_logs`), 各管理接口仍按自身权限位校验; `/admin/stats` 只要求后台入口.
- 用户组权限委派必须是调用者当前有效权限的子集; 创建用户/改组, 创建/更新用户组与设置新用户默认组都校验此约束; API 密钥调用按 `scopes` 裁剪后的权限计算, 且 API 密钥创建新密钥时未提交 `scopes` 默认继承当前有效权限. 管理员更新或删除用户前也校验目标用户实际权限组, 未验证/封禁账号按 `restore_group_id` 判定, 超出调用者权限返回 403.
- 更新/删除用户组或覆盖其策略白名单前校验目标组当前权限, 超出调用者权限返回 403; 组 PATCH 未出现的字段保持不变, 显式 `null` 清除可空字段, 移除 `upload` 权限时自动清除配额与上传限额.
- 前端样式只使用 Tailwind CSS v4 与 `src/index.css` 的语义令牌 (`background`, `card`, `muted`, `primary`, `destructive`, `success`, `warning`, `info`, `border`, `ring`, `chart-*`), 禁止硬编码色值与 CSS-in-JS.
- 系统设置表单控件按字段内容限宽; 人机验证关闭时隐藏配置字段, 服务地址仅在选择 Cap 时显示; 注册邮箱白名单按完整域名精确匹配, 留空时不限制; 列表输入复用 `TagsInput`.
- 组件层是 shadcn/ui: 新增基元用 `npx shadcn@latest add <name>` (写入 `src/components/ui/`), 业务组件放 `src/components/`; 禁止 MUI, Emotion 与 CSS-in-JS; 在成熟专用库明显优于自研且已登记到 `docs/tech-stack.md` 时可以引入其他组件库 (当前: 图片查看使用 `photoswipe`), 图标统一 lucide-react, 全局提示统一 sonner.
- 表格统一用 `src/components/DataTable.tsx`, 日期选择统一用 `src/components/DatePicker.tsx`, 空/错状态统一用 `src/components/States.tsx`; 需要新交互时先扩展这些共享组件.
- 加载骨架必须贴合实际页面结构; 表格首屏与局部刷新复用 `DataTable` 的列定义, 表头和行高, 瀑布流骨架复用 `MasonryGrid` 排布; 已知页面不得用通用表单/卡片骨架替代真实布局.
- 移动端与低高度视口下的对话框最大高度为 `100dvh - 2rem`, 内容超出时在弹窗内纵向滚动, 不得裁切表单或底部操作按钮.
- 存储策略分发域名列表的"别名"与"域名 URL"列名与"添加域名"按钮在顶部同行, 只显示一次; 桌面端拖动手柄排序且首项为默认, 删除首项后新的首项接替, 小屏幕纵向排列.
- 读取图片列表, 详情与秒传检查时, 只要策略存在默认分发域名, 直链必须按当前默认域名加记录 `rel_path` 重新拼接, 不得直接返回记录保存的旧 URL; 修改或补配默认域名后已有记录立即生效; 无默认域名时回退记录 URL. 该规则适用于所有支持分发域名的驱动. 初春图床的域名说明显示在 "添加域名" 按钮下方, 仅在没有域名时显示, 添加首个域名后隐藏.
- 存储策略编辑页的对象键与域名 URL 对照表只在基本信息 / 分发域名标签下显示 (魔法路径已有实时预览, 不重复展示); 基本信息底部对所有驱动显示后端能力徽标; 分发域名标签名为"分发域名"; 冲突策略示例只使用默认分发域名.
- 存储策略列表页每行操作列的 "扫描补录" 按钮位于编辑按钮左侧, 只显示图标, 为该策略单独提交扫描任务; 缺少 `list` 或 `read` 能力或声明 `url_from_backend` 时按钮禁用.
- 存储策略列表每行最左侧为三横线拖拽把手, 长按后拖动调整展示顺序, 提交完整策略 ID 顺序到 `PATCH /api/v1/admin/policies`; 编辑页不提供排序输入, 新建策略默认追加到列表末尾.
- 存储策略列表的 "使用情况" 列显示该策略下 `kind=image` 的记录数, 点击跳转到 `/gallery?policy_id=<id>`, 由图库按策略筛选.
- 存储策略提示文字在基本信息中配置 (最长 500 字符); 上传页在策略选择器左侧显示信息图标, 悬浮或点击时以 Popover 展示, 留空不显示; 静态文案与 `aria-label` 必须使用 i18n key.
- 存储策略编辑页中开关类字段 (复用已释放的 `{seq}`, 扫描试运行) 统一标签与说明在左, 开关在右; 基本信息顺序为策略名称, 后端类型, 提示文字; 启用策略开关位于列表操作列首项; 新建策略选择后端时按驱动自动填入普通用户可见的简单提示, 仅当提示为空或仍为已知默认值时替换.
- Cloudreve 策略连接页只使用账号密码登录, 提供登录按钮; 账号密码需要关闭 Cloudreve 登录人机验证, 2FA 使用二级对话框输入动态口令; 登录成功或凭据变化时同步更新表单登录状态, 未登录保存时提交 `enabled=false`; 编辑页保留魔法路径标签.
- 图库删除记录成功后统一提示 "已删除图片" / "已删除选中的 N 条图片", 不区分外部对象已删除, 进入维护队列或保留; 外部对象状态只在维护队列查看.
- 图库筛选使用原始文件名, 格式与存储策略; 不提供开始时间和结束时间筛选; 仅存在筛选条件时渲染清空筛选按钮.
- 图库瀑布流多选模式下, 点击卡片任意位置切换选中状态, 不打开图片查看器; 左上角选中框保留, 点击它只切换一次.
- 图库图片查看器使用 `photoswipe`: 桌面端左侧大图 + 右侧 400px 信息栏, 移动端全屏图片 + 底部 "图片详情" 面板; 背景为模糊后的图库网页 (全屏 `backdrop-filter`), 不使用图片放大模糊; 预加载当前图片前面两张与后面两张 (共四张, 不含当前图); 信息栏加载态使用居中旋转圆环, 不使用骨架屏; 切换图片时保留上一张详情直到新数据到达, 避免加载态闪烁; PhotoSwipe 的右侧控件必须避让信息栏; 删除确认等 `AlertDialog` 层级必须高于查看器, 否则确认框不可见; 关闭后不得残留 `.pswp` 节点.
- 图库瀑布流卡片外层使用 `content-visibility: auto` 与 `contain-intrinsic-block-size`, 离屏卡片跳过布局与绘制; 不得卸载 `<img>` 节点, 避免滚动回看时重新加载.
- 弹层遮罩不使用 `backdrop-filter` 模糊, 只使用半透明背景与透明度动画; 全屏模糊会在图库上方每帧重算, 导致抽屉展开掉帧. 图片查看器背景例外: 按要求模糊图库网页, 使用全屏 `backdrop-filter`, 不叠加图片放大背景.
- `LazyImage` 的加载与失败状态必须绑定当前图片直链; 直链变化时应同步派生 `loading` 状态, 不依赖 effect 异步重置.
- `TabsTrigger` 的指针焦点环由 `ui/tabs.tsx` 统一处理: `pointerdown` 设置 `data-pointer-focus`, `keydown` 与 `blur` 清除; 指针点击不显示焦点环, 键盘导航保留焦点环; 页面不得重复实现该逻辑.
- 所有外部图片必须设置 `referrerPolicy`: `LazyImage` 统一处理图库卡片, 列表与预览, PhotoSwipe 在 `contentLoadImage` 事件中设置动态创建的图片; 命中 Referrer 白名单时发送 Referer, 否则 `no-referrer`.
- `referrer_allowlist` 按行保存, 一行一个域名, 后端规范化并去重 (最多 100 个); 匹配使用域名后缀, 例如 `a.com` 命中 `x.a.com`; 留空表示全部禁止 Referer.
- 复制 HTML 格式时必须对 URL 与文件名执行 HTML 属性上下文转义; 其他复制格式保持各自的原始格式.
- 个人设置账户信息按昵称, 用户名, 邮箱, 用户组, 配额排列, 配额显示在账户信息卡中; 昵称标签, 输入框和保存按钮保持同行; 昵称与密码输入限宽.
- 桌面侧栏占位宽度必须与固定侧栏共用 `DRAWER_WIDTH`; 侧栏标题行在定高容器内绘制下边框, 与顶栏下边框共线.
- 前端验证顺序: `npm run typecheck` -> `npm run lint` -> `npm run format:check` -> `npm run build`; 代码逻辑变更后必须同步更新 `docs/webui.md` 与 `docs/tech-stack.md` 的对应描述.

## 构建与运行

后端 (Go 1.27):

```sh
go run ./cmd/picsky                  # 开发启动 (需 PICSKY_SECRET_KEY)
go build -o picsky ./cmd/picsky      # 构建二进制 (不内嵌前端时 WebUI 不可用)
go test ./...                        # 全部测试
go vet ./...
```

前端 (Node 24, 独立工程目录 web/):

```sh
cd web && npm install
cd web && npm run dev                # dev server, 代理 /api 与 /lsky 到 127.0.0.1:5214
cd web && npm run build              # 产物输出 web/build/
cd web && npm run typecheck          # tsc --noEmit
cd web && npm run lint               # ESLint
cd web && npm run format:check       # Prettier 检查 (src/components/ui/ 已排除)
```

内嵌前端产物的完整构建 (前端 -> internal/web/build -> 二进制):

```sh
./scripts/build.sh                   # 等价于 npm build + 拷贝 + go build
PICSKY_VERSION=0.1.0 ./scripts/build.sh picsky
```

容器:

```sh
cp .env.example .env                 # 至少填写 PICSKY_SECRET_KEY (openssl rand -base64 32); 生产部署前请更换默认 PostgreSQL 密码
mkdir -p data postgres_data          # 应用数据目录需 uid 1000; PostgreSQL 数据目录按镜像中 postgres 用户 UID 设置
docker compose up -d                 # 生产: 使用 ghcr.io/yeqingky/picsky:latest + 编排内 PostgreSQL
docker compose -f docker-compose-dev.yml up -d --build   # 开发: 本地构建
```

生产编排使用 `ghcr.io/yeqingky/picsky:latest` 与 `postgres:18-alpine`, 宿主端口绑定 `127.0.0.1:5214:5214`, PostgreSQL 数据目录为 `./postgres_data`; 开发编排使用本地应用镜像与 `postgres:18-alpine`, 端口为 `5214:5214`, PostgreSQL 数据目录为 `./data/postgres`. 两者将应用数据挂载到 `./data`; `.env.example` 中 PostgreSQL 默认密码为 `picsky`, 实际部署前必须更换. 默认使用 PostgreSQL, 设置 `PICSKY_DB_DRIVER=sqlite` 与 `PICSKY_DB_DSN=/data/picsky.db` 可切回单文件 SQLite.

运行要求: `PICSKY_SECRET_KEY` 必填 (32 字节 base64); 其余环境变量见 `docs/tech-stack.md` 第 7 节与 `.env.example`.
