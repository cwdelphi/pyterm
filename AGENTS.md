# AGENTS.md

新项目目录(当前为空)。本机同类项目见 `/home/ppy_freeswitch`、`/home/s2s`、`/home/ytzd` 等(文档均为中文,遵循同样约定)。

## 重要陷阱:git 仓库根目录

- `/home/pyterm` 本身**不是** git 仓库;`git rev-parse --show-toplevel` 解析到文件系统根 `/`(那里有一个无提交、无 remote 的 `git init`)。
- 因此不要在此目录盲目执行 `git add .` / `git commit`,会把整个文件系统根目录的内容(staging 了所有 `/home`、`/root`、`/www` 等)提交进去。
- 若此项目需要版本管理,应先在 `/home/pyterm` 单独 `git init` 再操作。

## 现状

- 项目为「无账户 Markdown 文档浏览器后台系统」:FastAPI 后端 + Vue3/Vite 前端,以 Docker Compose 部署,服务端口缺省 `5588`。
- 文档内容位于 `md/`(挂载卷,只读),由 `www` 用户拥有(与 `/home` 下其他项目一致)。
- 部署方式:**镜像只装 Python 依赖,不含业务代码**;配置(`.env`) / 代码(`app/`、`web/dist/`) / 资源(`md/`) 全部通过 docker-compose 卷挂载进容器,改宿主文件即生效。
- 前端改动后需先在宿主构建:`cd web && npm run build`(npm 用镜像源 `--registry=https://registry.npmmirror.com`),产出 `web/dist/` 供挂载。
- 常用命令:`docker compose up -d`(改 .env/文档/后端代码)→ `docker compose up -d --build`(仅依赖变更时)。
- 后端本地调试:`cd app && uvicorn main:app --reload`(缺省读 `md`)。
- **功能(3视图):**📖 文档管理(文件树CRUD + 浏览/编辑切换) · 🔗 网址管理(分组+卡片) · 💻 SSH管理(连接列表+终端页签)
- `md/links.json` 由网址管理读写(分组结构,支持旧数组自动迁移)。
- `md/ssh_connections.json` 由SSH管理读写(连接配置)。
- SSH/VNC 终端: WebRTC 双模式(agent 直连 `/api/ws/webrtc` 信令 + DataChannel / gateway 中继 `:5599?token=`)，前端 `web/src/utils/webrtc.ts` + xterm。

## Agents 开发指令

### 项目信息
- 框架：Vue 3.0 + `<script setup>`
- UI 库：Element Plus
- 包管理：pnpm

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
- 使用 `<script setup>` + TypeScript
- 组件名使用 PascalCase
- CSS 使用 `scss` 预处理器（如已配置）

## 文档约定

- 生成的 markdown 方案/设计文档统一保存到 `docs/` 目录。
- 同一内容存在多份时,必须用**版本号**区分文件名,格式 `<主题>_v<主版本>.<次版本>.md`(如 `xx_v1.0.md`、`xx_v1.1.md`),禁止覆盖或改名覆盖旧版。
- 端口缺省 `5588`(见 `.env` 的 `PORT`/`HOST_PORT`),如需调整改 `.env` 即可。
