# pyterm · 泡鱼终端

多用户远程运维与文档协作门户：**Markdown 文档库 · 网址收藏 · SSH/SFTP/VNC 远程终端 · Agent/网关/TURN 集中管理**。

A multi-user remote-operations & documentation portal: **Markdown docs, link bookmarks, SSH/SFTP/VNC remote terminals, and centralized management of agents, gateways and TURN servers**.

FastAPI 后端 + Vue 3 / Vite 前端，Docker Compose 部署，对外仅暴露 **HTTPS `5588`** 端口。

FastAPI backend + Vue 3 / Vite frontend, deployed with Docker Compose, exposing **HTTPS `5588`** only.

> 本 README 为中英双语：每节先中文、后 English。
> This README is bilingual: Chinese first, then English, in every section.

## 功能 / Features

| 模块 Module | 说明 Description |
|---|---|
| 📖 文档管理 Documents | 文件树 CRUD，CodeMirror 6 编辑器 + 实时预览，内容存储于 MinIO、按用户隔离。<br>File-tree CRUD, CodeMirror 6 editing with live preview; content stored in MinIO, isolated per user. |
| 🔗 网址管理 Links | 分组列表 + 卡片网格的链接收藏。<br>Link bookmarks as groups + card grid. |
| 💻 远程管理 Remote | SSH 终端（xterm.js）、SFTP 文件管理器、VNC（noVNC）；连接支持**直连**与 **Agent 中继**两种模式；RDP 配置已预留、暂未开放。<br>SSH terminal (xterm.js), SFTP file manager, VNC (noVNC); connections support **direct** and **agent** modes; RDP config is reserved but not yet enabled. |
| ⚙️ 系统管理 Admin | 用户/角色/审计日志、Agent、网关、coturn、连接诊断、程序下载、系统配置。<br>Users / roles / audit log, agents, gateways, coturn, connection diagnostics, binary downloads, settings. |

- 账户与权限：注册/登录（JWT）、admin / user 角色、资源按「所有人 / 私有 / 指定用户」共享。<br>Accounts: register/login (JWT), admin & user roles, resources shared to *all / private / selected users*.
- 中英双语界面（顶栏切换）+ 暗色主题与主题色。<br>Bilingual UI (switch in top bar) + dark mode and theme colors.
- WebRTC 双通道：P2P 直连或经网关中继，媒体流经 coturn STUN/TURN。<br>WebRTC connectivity: P2P or relay via gateway, media via coturn STUN/TURN.
- 管理后台可视化管理 Agent / 网关 / coturn，支持在线升级、Token 复制、STUN/TURN 连通性测试。<br>Manage agents / gateways / coturn from the admin console, with online upgrade, token copy and STUN/TURN connectivity tests.

## 架构 / Architecture

```
                        Browser (HTTPS :5588)
                               │
                        ┌──────▼──────┐
                        │    nginx    │  TLS 反代 / reverse proxy
                        └──────┬──────┘
                        ┌──────▼──────┐
                        │  md (app)   │  FastAPI + Vue 静态资源 / static frontend
                        └──┬───┬───┬──┘
              ┌────────────┘   │   └────────────┐
        ┌─────▼─────┐   ┌──────▼──────┐   ┌─────▼──────┐
        │  MariaDB  │   │    MinIO    │   │   coturn   │
        │  元数据    │   │  文档内容    │   │ STUN/TURN  │
        └───────────┘   └─────────────┘   └────────────┘

   被管主机 / managed hosts                公网 / public
   ┌─────────────┐  WebSocket + WebRTC   ┌──────────────┐
   │   wragent   │◄─────────────────────►│  wrgateway   │
   │ (SSH/SFTP/  │      P2P 或中继        │  信令中继      │
   │  VNC 隧道)  │◄········ coturn ······►│  signaling   │
   └─────────────┘      (媒体中继)         └──────────────┘
```

Compose 服务一览 / services in `docker-compose.yaml`：

| 服务 Service | 容器 Container | 说明 Description |
|---|---|---|
| `nginx` | `pyterm_nginx` | TLS 反代，宿主 `5588`；证书位于 `nginx/ssl/`。<br>TLS reverse proxy on host `5588`; certs in `nginx/ssl/`. |
| `md` | `pyterm_md` | 应用主容器：FastAPI + `web/dist` 静态资源。<br>Main app container: FastAPI + `web/dist` static files. |
| `mariadb` | `pyterm_mariadb` | 数据库（宿主 `127.0.0.1:3306`）。Database (host `127.0.0.1:3306`). |
| `minio` | `pyterm_minio` | 文档对象存储，控制台 `9001`。Doc object storage; console on `9001`. |
| `coturn` | `pyterm_coturn` | WebRTC STUN/TURN（`19302`/`5349`/中继端口段）。WebRTC STUN/TURN (`19302`, `5349`, relay range). |
| `wragent` | `pyterm_wragent` | 远程接入代理（host 网络，需经门户审批）。Remote access agent (host network; needs approval). |
| `wrgateway` | `pyterm_wrgateway` | WebRTC 信令网关（host 网络）。WebRTC signaling gateway (host network). |
| `test-ssh-server` | `pyterm_test_ssh` | 测试用 SSH（`2222`）/ VNC / RDP，仅开发用。Test SSH (`2222`) / VNC / RDP, dev only. |

> 镜像只安装 Python 依赖，**业务代码、配置、文档全部通过卷挂载**进容器（`./app`、`./web/dist`、`./config`、`./md`），改宿主文件即生效。
> The image contains only Python dependencies; **app code, config and docs are volume-mounted** (`./app`, `./web/dist`, `./config`, `./md`), so host-side edits take effect directly.

## 快速开始 / Quick Start

```bash
# 1) 前端首次构建 Build the frontend (first time)
cd web && npm install --registry=https://registry.npmmirror.com && npm run build
cd ..

# 2) 启动 Start
docker compose up -d --build   # 首次或依赖变更 first run / dependency changes
docker compose up -d           # 日常：仅改代码/配置 routine: code/config changes only

# 3) 访问 Visit
# https://<host>:5588   (自签证书，浏览器信任即可 / self-signed cert, accept it in the browser)
```

常用运维命令 / routine operations：

```bash
docker restart pyterm_md                       # 后端代码变更 after backend code changes
cd web && npm run build                        # 前端变更后重建 rebuild after frontend changes
docker compose logs -f md                      # 查看日志 logs
docker compose up -d                           # 修改 .env 后重载 reload after editing .env
```

## 配置 / Configuration

`.env`（节选 / excerpt）：

| 变量 Variable | 缺省 Default | 说明 Description |
|---|---|---|
| `PORT` / `HOST_PORT` | `5588` | 容器内应用端口 / 宿主映射端口。In-container app port / host-mapped port. |
| `SITE_NAME` | 泡鱼终端 | 站点名称。Site name. |
| `BASE_URL` / `PUBLIC_URL` | — | 门户对外地址，安装脚本/签名链接使用。Public URL used by install scripts & signed links. |
| `SERVER_PUBLIC_IP` | — | WebRTC/TURN 公网 IP。Public IP for WebRTC/TURN. |
| `TURN_SECRET` | — | coturn 长期凭证密钥。coturn long-term credential secret. |
| `JWT_SECRET` / `JWT_EXPIRES_HOURS` | — / `24` | JWT 签名与有效期。JWT signing secret & expiry. |
| `DB_HOST` / `DB_PORT` / `DB_USER` / `DB_PASS` / `DB_NAME` | `mariadb` / `3306` / `ppy` / — / `ppy_tools` | MariaDB 连接。MariaDB connection. |
| `LATEST_PYAGENT_VERSION` | 同 `pyagent/VERSION` | 门户公告的最新 pyagent 版本（单二进制，agent/gateway 共用）。Latest pyagent version advertised by the portal. |
| `MD_ROOT` | `/app/md` | 默认 SFTP 共享目录（历史文档已迁移至 MinIO）。Default SFTP share dir (legacy docs migrated to MinIO). |

端口调整只需修改 `.env` 中的 `HOST_PORT` 并 `docker compose up -d`。
To change the port, edit `HOST_PORT` in `.env` and run `docker compose up -d`.

## 开发 / Development

```bash
# 前端 Frontend（5173，/api 代理到 127.0.0.1:8000）
cd web && npm run dev

# 后端 Backend（在仓库根目录 from repo root）
DB_HOST=127.0.0.1 uvicorn app.main:app --reload
```

- 后端源码 `./app` 与前端产物 `./web/dist` 均已挂载进容器，日常改动用 `docker restart pyterm_md` 即可。
  Both `./app` and `./web/dist` are mounted into the container; `docker restart pyterm_md` picks up everyday changes.
- 本地起后端时 MinIO（`9000`）未映射到宿主，文档类接口需在容器网络内验证。
  When running the backend on the host, MinIO (`9000`) is not published; document APIs must be verified inside the container network.
- 无 lint / typecheck 配置；前端语言 TypeScript + `<script setup>`，组件 PascalCase，样式为 scoped CSS（**非** Element Plus / scss）。
  No lint/typecheck is configured; frontend is TypeScript + `<script setup>`, PascalCase components, scoped CSS (**not** Element Plus / scss).

## 测试 / Tests

| 命令 Command | 说明 Description |
|---|---|
| `cd web && npm test` | 前端单元测试 vitest（jsdom）。Frontend unit tests (vitest, jsdom). |
| `DB_HOST=127.0.0.1 TESTING=1 python3 -m pytest app/tests` | 后端 pytest（`TESTING=1` 禁用连接池，避免事件循环冲突）。Backend pytest (`TESTING=1` disables the pool to avoid event-loop conflicts). |
| `./run-baseline-tests.sh` | 基线三段：pytest + vitest + Playwright E2E。Baseline: pytest + vitest + Playwright E2E. |
| `cd autotest && npx playwright test` | 浏览器 E2E（`admin-panel.config.ts`）。Browser E2E (`admin-panel.config.ts`). |

- 集成测试（`test_integration_*`）依赖 `docker compose up -d test-ssh-server` 及相应环境变量（`SSH_HOST/SSH_PORT/SSH_USER/SSH_PASS`）；缺服务时会失败。
  Integration tests (`test_integration_*`) need `docker compose up -d test-ssh-server` and matching env vars; they fail without the service.

## 远程接入 / Remote Access（pyagent 单二进制）

- **pyagent**：Go 编写的单二进制，`mode` 决定角色（`agent` 接入代理 / `gateway` 信令网关），同一份包跑两种角色。版本见 `pyagent/VERSION`（`1.0.x`，仅第三位递增）。
  Single Go binary; `mode` selects the role (`agent` or `gateway`). Version in `pyagent/VERSION` (`1.0.x`, patch-only increments).
  - 部署在被管主机上时与门户建立 WebSocket/WebRTC 通道，提供 SSH/SFTP/VNC 隧道。
  - 无公网地址的主机经它中继 WebRTC 连接。
- 两种安装模式 Two install modes：
  1. **模式一 · 公开安装**（Tailscale 式）：`curl -fsSL https://<host>:5588/install-agent | bash`，随后在管理后台「Agent 管理 → 待审批」中批准。
     **Mode 1 · public install**: run the curl above, then approve the pending agent in *Admin → Agents*.
  2. **模式二 · 签名链接**：管理员生成一次性签名 URL（30 分钟有效、`jti` 一次性消费）。
     **Mode 2 · signed URL**: admin generates a one-time URL (30-min validity, single-use `jti`).
- 发布构建 Release builds：

  ```bash
  PATH=$PATH:/usr/local/go/bin bash scripts/build-pyagent.sh          # 第三位 +1，写入 pyagent/VERSION 与 .env
  PATH=$PATH:/usr/local/go/bin bash scripts/build-pyagent.sh --no-bump # 按当前 VERSION 重跑
  ```

  仅第三位（`1.0.x`）递增。产物下载见管理后台「程序下载」或 `/api/deploy/pyagent/{platform}`（兼容保留 `/api/deploy/wragent/{platform}`、`/api/deploy/wrgateway/{platform}`）。
  Only the patch digit increments. Binaries: *Admin → Downloads* or `/api/deploy/pyagent/{platform}` (legacy `wragent`/`wrgateway` paths still work).

## API 概览 / API Overview

完整定义见 `app/`（FastAPI 路由）。主要分组 / see `app/` for the full definitions:

| 分组 Group | 前缀 Prefix | 说明 Description |
|---|---|---|
| 认证 Auth | `/api/auth` | `register` / `login` / `logout` / `me` / `password` / `verify`（JWT）。JWT-based. |
| 管理 Admin | `/api/admin` | 用户、角色、审计、Agent、网关、coturn、升级、共享、待审批。Users, roles, audit, agents, gateways, coturn, upgrades, sharing, pending approval. |
| 文档 Docs | `/api/docs/*` | `tree` `content` `write` `mkdir` `rename` `delete` `move`（需登录，MinIO 存储）。Auth required; MinIO-backed. |
| 网址 Links | `/api/links/*` | 链接与分组 CRUD。Link & group CRUD. |
| 远程 Remote | `/api/ssh/*`, `/api/sftp/*` | 连接 CRUD/测试/排序、密码读取、SFTP 服务与文件客户端。Connections CRUD/test/reorder, SFTP server & file client. |
| 信令 Signaling | `WS /api/ws/webrtc`, `/api/webrtc/ice-servers` | WebRTC 信令与 ICE 服务器下发。WebRTC signaling & ICE servers. |
| 部署 Deploy | `/api/deploy/*` | `install-agent`（模式一公开脚本）、`agent/signed-url`（模式二）、`wragent/{platform}`、`wrgateway/{platform}` 下载。Mode-1 script, mode-2 signed URL, binary downloads. |
| Agent 回调 Agent | `/agent/s/{sid}` | Agent 侧会话回调（无前缀）。Agent-side session callbacks (no prefix). |
| 时间线 Timeline | `/api/timeline/*` | Agent 上报 `report` / `records` / `detail` / `stats`。Agent telemetry reports. |

## 目录结构 / Project Layout

```
app/            FastAPI 后端 / backend (main.py + api_isolated / auth_api / database ...)
web/            Vue 3 + Vite 前端源码 / frontend source
web/dist/       构建产物，挂载为 /app/static / built assets, mounted at /app/static
wragent/        接入代理 (Go)，VERSION 与二进制 / Go agent, VERSION + binaries
wrgateway/      信令网关 (Go) / Go signaling gateway
nginx/          反代配置与 TLS 证书 / reverse proxy config + TLS certs
config/         coturn 等服务配置 / coturn & service configs
md/             默认 SFTP 共享目录（历史文档已迁移至 MinIO）/ default SFTP share dir
docs/           方案/设计文档（带版本号）/ design docs (versioned)
autotest/       Playwright E2E / E2E tests
scripts/        构建与初始化脚本 / build & init scripts
run-baseline-tests.sh   基线测试包 / baseline test suite
```

## 文档约定 / Docs Conventions

- 生成的方案/设计文档统一存 `docs/`；同主题多版本用 `<主题>_v<主版本>.<次版本>.md` 区分，**不覆盖旧版**。
  Generated design docs go to `docs/`; multiple versions of one topic use `<topic>_v<major>.<minor>.md` and old versions are never overwritten.
- 生成任何页面/组件前，先读 [`docs/ui-design-system.md`](docs/ui-design-system.md)（表格/卡片为核心、严禁大面积留白、空状态用占位组件、样式必须 scoped）。
  Before generating any page/component, read [`docs/ui-design-system.md`](docs/ui-design-system.md).
- 端口调整改 `.env` 即可；Git 仓库根目录为本目录（`origin` → GitCode）。
  Ports are changed in `.env`; this directory is the git repo root (`origin` → GitCode).
