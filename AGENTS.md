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
│       ├── components/     业务组件 (DataTable, DatePicker, States, gallery/, upload/ 等)
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
- 不把 `README` 用作变更日志; 发布说明写入仓库根目录的 `.release-notes.md`, 并通过 `.git/info/exclude` 保持在本地忽略.
- 存储适配器必须声明能力 (`delete`, `overwrite`, `stat`, `presign`, `multipart`); 新增驱动不得假设后端支持删除或覆盖, 上层按能力降级.
- Lsky Pro 兼容层是适配层: 不得绕过认证, 权限, 配额与能力降级; 兼容字段有变更时先更新 `docs/lsky-compat.md`.
- 不实现本地人机验证: 只接入 Cap 与 Cloudflare Turnstile; 注册, 邮箱验证, 人机验证都必须是可开关项.
- 启用 TOTP 的登录使用两步流程: 首次请求校验用户名/密码与人机验证并返回短时挑战; 前端在模态框自动提交动态口令, 挑战必须一次性消费并绑定来源 IP / User-Agent.
- 密码找回令牌必须限时、一次性且绑定当前密码散列; 重置密码与清除 TOTP 秘钥必须在同一事务中完成; 登录页仅在公开站点信息报告 SMTP 发信已配置时显示找回入口.
- 维护任务 (队列) 必须幂等可重试, 不得绕过权限与能力降级; WebDAV 只是内部存储通道, 不得对外暴露.
- 内置用户组 (`管理员` / `只读`, 由 `groups.is_system` 标记) 的权限位固定, 不可删除, 不可设为默认组; 调整组规则时同步更新 `docs/permissions.md`.
- 前端样式只使用 Tailwind CSS v4 与 `src/index.css` 的语义令牌 (`background`, `card`, `muted`, `primary`, `destructive`, `success`, `warning`, `info`, `border`, `ring`, `chart-*`), 禁止硬编码色值与 CSS-in-JS.
- 组件层是 shadcn/ui: 新增基元用 `npx shadcn@latest add <name>` (写入 `src/components/ui/`), 业务组件放 `src/components/`; 不引入 MUI, Emotion, 其它 UI 库或图标库, 图标统一 lucide-react, 全局提示统一 sonner.
- 表格统一用 `src/components/DataTable.tsx`, 日期选择统一用 `src/components/DatePicker.tsx`, 空/错状态统一用 `src/components/States.tsx`; 需要新交互时先扩展这些共享组件.
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
cp .env.example .env                 # 至少填写 PICSKY_SECRET_KEY (openssl rand -base64 32) 与 POSTGRES_PASSWORD
mkdir -p data                        # 应用容器以 uid 1000 运行, 需要可写 ./data
docker compose up -d                 # 生产: 使用 ghcr.io/yeqingky/Picsky:latest + 编排内 PostgreSQL
docker compose -f docker-compose-dev.yml up -d --build   # 开发: 本地构建
```

编排细节: 两个编排都包含 `postgres` 服务 (`postgres:18-alpine`), 数据目录挂载在 `./data/_postgres`, 应用通过 `depends_on` 等待其健康; 应用自身的 `./data` 用于上传临时文件 (`/data/tmp`). 默认使用 PostgreSQL, 设置 `PICSKY_DB_DRIVER=sqlite` 与 `PICSKY_DB_DSN=/data/picsky.db` 可切回单文件 SQLite.

运行要求: `PICSKY_SECRET_KEY` 必填 (32 字节 base64); 其余环境变量见 `docs/tech-stack.md` 第 7 节与 `.env.example`.
