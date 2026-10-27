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
├── cmd/picsky/     服务入口 (main)
├── internal/
│   ├── api/        原生 REST handler 与路由
│   ├── apperr/     API 错误码
│   ├── config/     环境变量配置
│   ├── clientip/    请求 IP 来源探测与解析
│   ├── crypto/     AES-256-GCM 凭据加密
│   ├── db/         数据库连接 (SQLite WAL / PostgreSQL)
│   ├── hashing/    单遍多哈希
│   ├── httpupload/  multipart 上传解析
│   ├── imgdetect/  魔数格式探测与尺寸解析
│   ├── logx/       日志
│   ├── lsky/       Lsky Pro v1 兼容层
│   ├── middleware/ 认证与权限中间件
│   ├── model/      数据模型
│   ├── service/    业务逻辑 (上传, 图库, 策略, 用户组, 设置, 维护队列)
│   ├── spool/      上传缓冲 (内存 / 临时文件)
│   ├── storage/    适配器接口, 能力声明, 魔法路径渲染
│   │   └── webdav/ WebDAV 驱动 (v1)
│   └── web/        go:embed 前端产物 (build/ 由 scripts/build.sh 生成)
├── migrations/     数据库迁移与默认设置
├── scripts/        构建脚本
├── web/            前端工程 (独立 npm 项目, React + TypeScript + Vite + Tailwind CSS v4 + shadcn/ui, 产物输出 build/)
│   └── src/
│       ├── components/ui/  shadcn 基元 (CLI 生成, 已排除 Prettier)
│       ├── components/     业务组件 (DataTable, DatePicker, TagsInput, States, gallery/, upload/ 等)
│       ├── lib/utils.ts    类名合并 (cn)
│       ├── index.css       全局样式与设计令牌 (语义颜色, 圆角, 骨架扫光)
│       └── theme/          主题模式 (light / dark / system)
└── docs/           项目文档, 设计的事实来源
```

## 文档同步规则

- 表结构, 接口, 配置项, 权限位, 环境变量发生变化时, 必须同步更新 `docs/` 下对应文档.
- 新增文档必须登记到 `README.md` 的文档索引.
- 未定稿事项记录在 `docs/open-questions.md`; 确认后并入正式设计文档并从清单移除.
- 文档语言为中文, 使用半角标点; 命令, 路径, 字段名按原文保留.

## 开发约定

- Git 提交身份: 用户名 `YeqingKy`, 邮箱 `me@yeqing.im`.
- GitHub Actions 容器工作流在所有分支 push 与 PR 上构建; 分支 push 发布 `ghcr.io/yeqingky/picsky:<短 hash>`, `vX.Y.Z` tag 发布版本 tag 与 `latest`; PR 仅构建不推送; GHCR 认证使用 `GITHUB_TOKEN`.
- 不把 `README` 用作变更日志; 发布说明写入仓库根目录的 `.release-notes.md` 并随版本提交, 每次发布在文件顶部追加当前版本段落, 版本按倒序排列.
- 存储适配器必须声明能力 (`delete`, `overwrite`, `stat`, `presign`, `multipart`); 新增驱动不得假设后端支持删除或覆盖, 上层按能力降级.
- 魔法路径模板必须包含内容哈希变量或单个 `{seq}`; `rename` 从已入库匹配路径的最大序号之后分配, 扫描补录路径也占用序号; 未使用 `{seq}` 时冲突重命名追加 `-N`, 其他生成变量在重试中固定.
- 冲突策略只使用 `rename` / `skip`; `overwrite` 是后端能力声明, 不作为冲突策略.
- Lsky Pro 兼容层是适配层: 不得绕过认证, 权限, 配额与能力降级; 兼容字段有变更时先更新 `docs/lsky-compat.md`.
- 对外错误响应必须通过 `apperr.From` 归一; 所有 5xx 与 `backend_error` 使用通用消息并移除 `Details`, 原始错误仅写服务端日志; Lsky 5xx 返回实际状态码, 后端错误使用 502, 不按业务失败返回 HTTP 200.
- Lsky 上传在 multipart 文件缓冲前完成认证、上传权限与限流预检; 仅使用表单 `token` 的请求要求它是首个 multipart 字段, 通过认证后按用户组文件大小上限读取.
- 原生 multipart 上传必须在解析请求体前完成认证、上传权限与限流预检; 所有 multipart 非文件字段值每项最多 4 KiB, 最多 64 项, 字段名和值合计最多 64 KiB (Lsky `token` 计入预算).
- Base64 JSON 上传必须流式读取 `data` 并直接解码到共用上传缓冲, 禁止在内存完整保留编码字符串或解码副本; 不限组仍受 2 GiB 单次硬上限约束, `data` 字段前后区段各最多 32 KiB, data URL 头最多 4 KiB, 解码缓冲遵循 `PICSKY_UPLOAD_MEMORY_LIMIT`.
- 未设置 `PICSKY_TEMP_DIR` 时, spool 可将处理期溢出文件写入系统临时目录, 但 `CleanupStale` 必须跳过目录扫描; 仅对应用专用且隔离的临时目录执行启动残留清理.
- `/upload/check` 必须按当前 `user_id` 查询记录; 相同哈希仅由其他用户持有时返回 `exists: false` 且不包含记录 ID、URL 或大小; `/upload` 跨用户复用只返回当前用户自己的新记录.
- 不实现本地人机验证: 只接入 Cap 与 Cloudflare Turnstile; 注册, 人机验证与邮箱验证都可切换. 邮箱验证开启时新注册账号状态为 `1` (未验证), 实际组为 `只读`, `restore_group_id` 保存验证后的目标组; 验证邮件经有界进程内队列异步发送, 验证后改为正常并恢复目标组. 未验证账号可登录与重发邮件但无上传等权限; 重发按账号每小时 3 次、每来源 IP 每小时 10 次限流; 状态 `2` (封禁) 不可登录且使用只读组; `0` 为正常.
- 用户名去首尾空白后仅允许 1-64 位 ASCII 英文字母和数字; 邮箱写入, 登录, 找回与管理员邮箱搜索前统一去首尾空白并转小写; 登录只接受邮箱. 注册页用户名, 邮箱, 密码与确认密码校验提示显示在字段标签同行右侧; 用户名粘贴时过滤空格与非法字符, 邮箱输入自动转小写, 服务端仍须校验.
- 不存在邮箱, 封禁账号与密码错误必须返回相同 401 与通用提示; 不存在账号仍执行 bcrypt cost 12 虚拟散列校验. 公开注册的有效请求在查重前执行 bcrypt cost 12, 新建与重复请求统一返回相同 202 与通用响应, 且不下发会话 cookie.
- 启用 TOTP 的登录使用两步流程: 首次请求校验邮箱/密码与人机验证并返回短时挑战; 前端在模态框自动提交动态口令, 挑战必须一次性消费并绑定来源 IP / User-Agent; 启用 TOTP 必须验证当前密码, 登录动态口令计数器需原子消费且不可重放.
- 登录限流、TOTP 挑战、审计与上传记录统一使用 `client_ip_source` 解析的地址; 默认使用 `REMOTE_ADDR`, 只有可信反向代理会覆盖所选请求头且应用端口不对公网开放时才配置代理头.
- 会话 Cookie 在原生 TLS, HTTPS `site_url` 或 `X-Forwarded-Proto: https` 下必须设置 `Secure`; 反代场景要求可信代理覆盖该头且应用端口不对公网开放.
- 密码找回令牌必须限时、一次性且绑定当前密码散列; 邮件链接只在 URL fragment 携带令牌, WebUI 立即清理地址栏并通过 POST body 提交; 重置密码与清除 TOTP 秘钥必须在同一事务中完成; 登录页仅在公开站点信息报告 SMTP 发信已配置时显示找回入口.
- 所有应用 HTTP 响应必须设置 `Referrer-Policy: no-referrer`; WebUI HTML 入口声明同等 meta 策略.
- 会话令牌必须绑定 `users.session_version`; 登出、修改密码与密码重置递增版本并吊销该账号的全部旧会话; Cookie 修改密码成功后为当前浏览器签发新会话.
- 维护任务 (队列) 必须幂等可重试, 已完成任务可手动重跑, 不得绕过权限与能力降级; 手动策略扫描必须先列举并持久化对象清单, 返回任务 ID 后由后台队列读取解析, 前端通过任务状态接口查询结果; WebDAV 只是内部存储通道, 不得对外暴露.
- 内置用户组 (`管理员` / `只读`, 由 `groups.is_system` 标记) 的权限位固定, 不可删除, 不可设为默认组; 调整组规则时同步更新 `docs/permissions.md`.
- 删除用户组时必须同时检查 `users.group_id` 与 `users.restore_group_id`, 避免验证或解封后恢复到已删除的用户组.
- 用户组权限委派必须是调用者当前有效权限的子集; 创建用户/改组、创建/更新用户组与设置新用户默认组都校验此约束; API 密钥调用按 `scopes` 裁剪后的权限计算.
- 前端样式只使用 Tailwind CSS v4 与 `src/index.css` 的语义令牌 (`background`, `card`, `muted`, `primary`, `destructive`, `success`, `warning`, `info`, `border`, `ring`, `chart-*`), 禁止硬编码色值与 CSS-in-JS.
- 系统设置表单控件按字段内容限宽; 人机验证关闭时隐藏配置字段, 服务地址仅在选择 Cap 时显示; 注册邮箱白名单按完整域名精确匹配, 留空时不限制; 列表输入复用 `TagsInput`.
- 组件层是 shadcn/ui: 新增基元用 `npx shadcn@latest add <name>` (写入 `src/components/ui/`), 业务组件放 `src/components/`; 不引入 MUI, Emotion, 其它 UI 库或图标库, 图标统一 lucide-react, 全局提示统一 sonner.
- 表格统一用 `src/components/DataTable.tsx`, 日期选择统一用 `src/components/DatePicker.tsx`, 空/错状态统一用 `src/components/States.tsx`; 需要新交互时先扩展这些共享组件.
- 加载骨架必须贴合实际页面结构; 表格首屏与局部刷新复用 `DataTable` 的列定义、表头和行高, 瀑布流骨架复用 `MasonryGrid` 排布; 已知页面不得用通用表单/卡片骨架替代真实布局.
- 存储策略分发域名列表的“别名”与“域名 URL”列名与“添加域名”按钮在顶部同行, 只显示一次; 桌面端拖动手柄排序且首项为默认, 删除首项后新的首项接替, 小屏幕纵向排列.
- 维护队列表格与类型筛选必须覆盖后端支持的全部任务类型, 并使用中文名称显示.
- 图库筛选使用原始文件名, 格式与存储策略; 不提供开始时间和结束时间筛选; 仅存在筛选条件时渲染清空筛选按钮.
- `LazyImage` 的加载与失败状态必须绑定当前图片直链; 直链变化时应同步派生 `loading` 状态, 不依赖 effect 异步重置.
- 复制 HTML 格式时必须对 URL 与文件名执行 HTML 属性上下文转义; 其他复制格式保持各自的原始格式.
- 个人设置账户信息按昵称、用户名、邮箱、用户组、配额排列, 配额显示在账户信息卡中; 昵称标签、输入框和保存按钮保持同行; 昵称与密码输入限宽.
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
