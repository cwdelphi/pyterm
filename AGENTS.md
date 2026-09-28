# AGENTS.md

泡鱼终端（pyterm）：FastAPI + Vue3 的 WebRTC 远程终端平台（SSH/SFTP/VNC，直连或 Agent 中继），Docker Compose 部署，宿主端口 `5588`（nginx TLS）。功能/架构/服务清单详见 [`README.md`](./README.md)。本机同类中文项目：`/home/ppy_freeswitch`、`/home/s2s`、`/home/ytzd`。

## 重要陷阱：git 双远端

- 本目录**就是**仓库根（`git rev-parse --show-toplevel` = 本目录），可正常 `git add` / `git commit`。
- 两个远端、内容不同，**禁止直接 `git push github`**：
  - `origin` = gitcode（完整版，可含敏感配置；日常提交推送这里）
  - `github` = github 上的 public 脱敏版仓库（URL 见 `git remote -v`；orphan 独立历史 `public` 分支）
- 同步公开版的唯一入口：`bash scripts/publish-github.sh`
  （导出 master 已跟踪文件 → 按 `scripts/publish-dict.tsv` 字面脱敏 → 敏感词扫描，**命中即中止不推送** → 提交并推 `public:main`）。
- 敏感运行时文件已被 `.gitignore` 排除（`.env`、`wragent/config.json`、`wrgateway/config.json`、`scripts/config-snapshot.json`、`docs/mariadb_backup*.sql`、`*.pem`、数据目录等），**不要取消忽略**。
- 新增敏感词（口令/域名/IP）时必须同步追加到 `scripts/publish-dict.tsv`，否则会被扫描 gate 拦下。
- 发布设施本身（`publish-github.sh`、`publish-dict.tsv`）含敏感词，脚本会自动从公开版剔除，不要移除该逻辑。
- commit message 用中文。

## 现状

- 功能四大块：📖 文档管理（MinIO 存储、按用户隔离）· 🔗 网址管理 · 💻 远程管理（SSH/SFTP/VNC，直连 + Agent 中继）· ⚙️ 系统管理（用户/角色/审计、Agent、网关、coturn）。
- Compose 服务：`nginx` / `md` / `mariadb` / `minio` / `coturn` / `wragent` / `wrgateway` / `test` / `test-ssh-server`（说明表见 README）。
- 镜像只装 Python 依赖；**代码与配置全部卷挂载**（`./app`、`./web/dist`、`./config`、`./md`），改宿主文件即生效：
  - 后端 / 静态资源改动 → `docker restart pyterm_md`
  - 前端改动先宿主构建 → `cd web && npm run build`（npm 源 `--registry=https://registry.npmmirror.com`）
  - 仅依赖变更时才 `docker compose up -d --build`
- 后端本地调试：仓库根执行 `DB_HOST=127.0.0.1 uvicorn app.main:app --reload`。
- `md/links.json`、`md/ssh_connections.json` 分别由网址管理、SSH 管理读写。

## Agents 开发指令

### 项目信息
- 框架：Vue 3 + `<script setup>` + TypeScript（Vite）。
- 包管理：**npm**（lockfile 为 `web/package-lock.json`，**非 pnpm**）。
- UI：**自研组件 + scoped CSS**，无 Element Plus、无 scss（README 亦载明）。无 lint/typecheck，改完跑测试。

### 设计系统引用

**⚠️ 重要**：本项目的界面设计规则已独立定义为设计系统文档。

- 完整规范见：[`docs/ui-design-system.md`](./docs/ui-design-system.md)
- **生成任何页面/组件前，必须先读取并遵守该文档。**
- 核心约束摘要：
  1. 页面以表格和卡片为核心组件
  2. 严禁大面积留白，必须 `flex: 1` 撑满视口
  3. 空状态必须渲染占位组件，禁止裸留白
  4. 所有样式必须 `scoped`，布局优先 Flexbox

### 代码风格
- 使用 `<script setup>` + TypeScript；组件名 PascalCase；样式为 scoped CSS。
- 文案改动必须中英同步：前端 `web/src/i18n/{zh-CN,en}.json`（键集须完全一致，636 键）、后端 `app/locale/{zh-CN,en}.json`。

## 常用命令

| 命令 | 说明 |
|---|---|
| `cd web && npm run build` | 前端构建（产物 `web/dist` 卷挂载进容器） |
| `cd web && npm test` | 前端 vitest 单测 |
| `DB_HOST=127.0.0.1 TESTING=1 python3 -m pytest app/tests` | 后端 pytest（`TESTING=1` 禁用连接池） |
| `cd autotest && npx playwright test` | 浏览器 E2E |
| `./run-baseline-tests.sh` | 基线三段：pytest + vitest + E2E |
| `docker restart pyterm_md` | 后端/静态改动生效 |
| `bash scripts/publish-github.sh` | 脱敏同步公开版到 github |
| `scripts/build-wragent.sh` / `build-wrgateway.sh` | Go 二进制构建（自动 bump patch，`GO_BIN=/usr/local/go1.27/bin/go`） |

- 集成测试（`test_integration_*`）依赖 `docker compose up -d test-ssh-server` 及 `SSH_HOST/SSH_PORT/SSH_USER/SSH_PASS`，缺服务会失败。

## 文档约定

- 方案/设计文档统一保存到 `docs/` 目录。
- 同一内容多份时用**版本号**区分文件名：`<主题>_v<主版本>.<次版本>.md`（如 `xx_v1.0.md`、`xx_v1.1.md`），禁止覆盖旧版。
- 部署/运维总览见 [`docs/部署方案_v1.0.md`](./docs/部署方案_v1.0.md)：服务器清单、**部署前 `uname -m` 确认 CPU 架构选二进制**、Agent 标准部署与待注册审批、凭据与发布脱敏。
- 端口缺省 `5588`（见 `.env` 的 `PORT`/`HOST_PORT`，模板见 `.env.example`），调整改 `.env` 即可。
