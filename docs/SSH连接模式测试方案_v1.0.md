# SSH 连接模式自动化测试方案 v1.0

> 日期: 2026-09-11
> 状态: 待执行
> 关联功能: SSH连接配置支持直连模式/Agent模式可配置和编辑

## 测试范围

| 测试层 | 框架 | 目标 |
|--------|------|------|
| 后端单元测试 | pytest | `connection_mode` / `agent_id` 字段的 CRUD、默认值、兼容性 |
| 前端单元测试 | vitest | `SshConn` 接口字段、模式选择逻辑 |
| E2E 测试 | Playwright | 表单交互、配置面板、模式持久化、弹窗移除 |
| 集成测试 | pytest + Playwright | 直连模式 SSH/SFTP 连接、Agent 模式 WebSocket/WebRTC 路径 |

## 一、后端单元测试 (`app/tests/test_ssh_connection_mode.py`)

| 编号 | 用例 | 输入 | 预期 | 层级 |
|------|------|------|------|------|
| B-01 | 添加连接含 `connection_mode=direct` | `POST /api/ssh/add` 带 `connection_mode: "direct"` | `ok:true`, 返回 id | L2 |
| B-02 | 添加连接含 `connection_mode=agent` | `POST /api/ssh/add` 带 `connection_mode: "agent"`, `agent_id: "ag-01"` | `ok:true`, 列表中 `connection_mode == "agent"`, `agent_id == "ag-01"` | L2 |
| B-03 | 列表返回 `connection_mode` 字段 | 添加后 `GET /api/ssh` | 每个连接包含 `connection_mode` 字段 | L2 |
| B-04 | 列表返回 `agent_id` 字段 | 添加含 `agent_id` 的连接后查列表 | `agent_id` 值正确返回 | L2 |
| B-05 | 更新 `connection_mode` | `POST /api/ssh/update` 将 direct 改为 agent | 列表中 `connection_mode` 更新为 `"agent"` | L2 |
| B-06 | 更新时 `password` 空保留 + 新字段保留 | 更新时 `password=""`, 不传 `connection_mode` | 密码保留, `connection_mode` 保持原值 | L2 |
| B-07 | 旧数据默认 `connection_mode=direct` | 直接读取旧 `ssh_connections.json`（无新字段） | API 返回 `connection_mode: "direct"` | L2 |
| B-08 | 旧数据默认 `agent_id=""` | 同上 | API 返回 `agent_id: ""` | L2 |
| B-09 | `agent_id` 为空串时 Agent 模式 | 添加 `connection_mode="agent"`, `agent_id=""` | `ok:true`, 列表中 `agent_id == ""` | L2 |
| B-10 | 用户隔离：模式字段不跨用户 | 用户 A 添加 agent 模式连接，用户 B 查列表 | 用户 B 列表中不含 A 的连接 | L2 |
| B-11 | WebSocket 连接含 `connection_mode` 字段 | 通过 conn_id 查找密码时验证字段存在 | `_read_ssh()` 返回含新字段的 dict | L3 |
| B-12 | SshTestReq 不含新模式字段 | `POST /api/ssh/test` | 测试接口不受影响，正常返回 `ok` | L2 |

## 二、前端单元测试 (`web/src/__tests__/ssh-mode.test.ts`)

| 编号 | 用例 | 输入 | 预期 | 层级 |
|------|------|------|------|------|
| F-01 | `SshConn` 接口含 `connection_mode` | 类型定义 | `connection_mode` 可选，类型 `'direct' \| 'agent'` | L1 |
| F-02 | `SshConn` 接口含 `agent_id` | 类型定义 | `agent_id` 可选，类型 `string` | L1 |
| F-03 | 默认模式值为 `direct` | 新建 `SshConn` 对象不传 mode | `connection_mode` 为 `undefined`（前端用 `|| 'direct'` 兜底） | L1 |
| F-04 | API 请求体含新字段 | `sshAdd()` 传入含 `connection_mode` 的对象 | `fetch` 收到的 body 包含 `connection_mode` 和 `agent_id` | L1 |

## 三、E2E 测试 (`autotest/tests/ssh-connection-mode.spec.ts`)

| 编号 | 用例 | 操作步骤 | 预期 | 层级 |
|------|------|----------|------|------|
| E-01 | 新建表单显示"连接模式" | 点击 ＋ → 检查表单 | 表单中存在"连接模式"标签，显示"直连"和"Agent"两个按钮 | L4 |
| E-02 | 默认选中"直连" | 打开新建表单 | "直连"按钮有 `active` class | L4 |
| E-03 | 切换到"Agent"显示下拉框 | 点击"Agent"按钮 | 出现"关联 Agent"下拉框（`<select>`） | L4 |
| E-04 | 切回"直连"隐藏下拉框 | 点击"直连"按钮 | "关联 Agent"下拉框消失 | L4 |
| E-05 | 创建直连模式连接 | 填写表单 → 选"直连" → 保存 | 连接出现在列表中 | L4 |
| E-06 | 创建 Agent 模式连接 | 填写表单 → 选"Agent" → 选择 agent → 保存 | 连接出现在列表中 | L4 |
| E-07 | 配置面板显示"模式"列 | 点击 ☰ → 检查表头 | 表头包含"模式"列 | L4 |
| E-08 | 配置面板直连连接显示"直连" | 查看直连连接行 | 模式列显示"直连" | L4 |
| E-09 | 配置面板 Agent 连接显示"Agent" | 查看 Agent 连接行 | 模式列显示"Agent" | L4 |
| E-10 | 详情面板显示连接模式 | 选中一个连接 | 详情区域显示"连接模式: 直连模式"或"Agent模式" | L4 |
| E-11 | Agent 连接详情显示 Agent ID | 选中 Agent 模式连接 | 详情区域显示"Agent: xxx" | L4 |
| E-12 | 编辑连接修改模式 | 点击编辑 → 切换模式 → 保存 | 列表中模式更新 | L4 |
| E-13 | 模式选择弹窗已移除 | 打开已有连接的终端 | 不弹出模式选择弹窗，直接打开终端 | L4 |
| E-14 | 保存后模式持久化 | 创建 agent 模式连接 → 刷新页面 → 编辑该连接 | 表单中"Agent"按钮为 active，agent_id 正确回显 | L4 |
| E-15 | 状态栏显示模式 | 打开直连终端 | 底部状态栏显示"直连" | L4 |
| E-16 | Agent 终端状态栏显示 Agent | 打开 Agent 模式终端 | 底部状态栏显示"Agent" | L4 |
| E-17 | Tab 上 Agent 图标 | 打开 Agent 模式终端 | Tab 上显示 🌐 图标 | L4 |
| E-18 | `connection_mode` 字段通过 API 拦截验证 | 创建连接时拦截 `ssh/add` 请求 | `requestBody` 包含 `connection_mode` 和 `agent_id` 字段 | L4 |

## 四、集成测试 — 直连模式 SSH/SFTP

### 4a. 后端集成 (`app/tests/test_integration_connection_mode.py`)

| 编号 | 用例 | 操作 | 预期 | 层级 |
|------|------|------|------|------|
| I-01 | 直连模式 SSH 连接真实服务器 | 添加 `connection_mode=direct` 连接 → WebSocket `/ws/ssh` | 终端能正常交互（发送 `echo` 命令有输出） | L4 |
| I-02 | 直连模式 SFTP 列目录 | 通过 SFTP Client API 操作直连连接 | 返回文件列表 | L4 |

### 4b. E2E 集成 (`autotest/tests/ssh-direct/ssh-mode-direct.spec.ts`)

| 编号 | 用例 | 操作 | 预期 | 层级 |
|------|------|------|------|------|
| D-01 | 直连模式创建+终端 | 添加直连连接 → 双击 SSH 终端 | 终端打开，xterm 渲染，输入 `echo DIRECT_TEST` 有输出 | L4 |
| D-02 | 直连模式 SFTP | 添加直连连接 → 双击 SFTP 文件 | 文件浏览器打开，显示根目录 | L4 |
| D-03 | 直连模式终端关闭重连 | 打开终端 → 关闭 Tab → 重新打开 | 新终端正常连接 | L4 |
| D-04 | 编辑后模式保持为直连 | 创建直连连接 → 编辑（改名）→ 保存 → 打开终端 | 终端直连成功，状态栏显示"直连" | L4 |

## 五、集成测试 — Agent 模式

> 前置条件: 需要 wragent 在线（`docker-compose.test.yml` 中的 agent 服务）

### 5a. 后端集成

| 编号 | 用例 | 操作 | 预期 | 层级 |
|------|------|------|------|------|
| I-03 | Agent 模式连接参数传递 | 添加 `connection_mode=agent`, `agent_id` 连接 → WebSocket | WebSocket 收到连接参数含 `agent_id` | L4 |
| I-04 | WebRTC 信令房间创建 | 浏览器通过 `/ws/webrtc` 发 `connect_agent` | Agent 收到连接请求，信令消息交换正常 | L4 |

### 5b. E2E 集成 (`autotest/tests/ssh-webrtc/ssh-mode-agent.spec.ts`)

| 编号 | 用例 | 操作 | 预期 | 层级 |
|------|------|------|------|------|
| A-01 | Agent 模式创建+终端 | 添加 agent 连接 → 选择 agent → 双击 SSH 终端 | WebRTC DataChannel 建立，终端显示 agent 输出 | L4 |
| A-02 | Agent 模式 SFTP | 添加 agent 连接 → 双击 SFTP 文件 | WebRTC SFTP 请求/响应正常，显示文件列表 | L4 |
| A-03 | Agent 模式断线重连 | 打开 Agent 终端 → 模拟断线 → 点重连 | WebRTC 重新建立，终端恢复 | L4 |
| A-04 | Agent 离线时的错误提示 | Agent 不在线时打开终端 | 终端显示"连接失败"错误信息 | L4 |
| A-05 | Tab 图标和状态栏 | 打开 Agent 模式终端 | Tab 显示 🌐，状态栏显示"Agent" | L4 |

## 六、测试文件清单

| 文件 | 类型 | 新增/修改 | 用例数 |
|------|------|-----------|--------|
| `app/tests/test_ssh_connection_mode.py` | pytest 后端 | **新增** | 12 |
| `web/src/__tests__/ssh-mode.test.ts` | vitest 前端 | **新增** | 4 |
| `autotest/tests/ssh-connection-mode.spec.ts` | Playwright E2E | **新增** | 18 |
| `autotest/tests/ssh-direct/ssh-mode-direct.spec.ts` | Playwright E2E | **新增** | 4 |
| `autotest/tests/ssh-webrtc/ssh-mode-agent.spec.ts` | Playwright E2E | **新增** | 5 |
| `app/tests/test_integration_connection_mode.py` | pytest 集成 | **新增** | 2 |

**总计**: 6 个新文件，**45 个测试用例**

## 七、执行命令

```bash
# 后端单元测试
cd /ppy_prj/pyterm && python -m pytest app/tests/test_ssh_connection_mode.py -v --tb=short

# 前端单元测试
cd /ppy_prj/pyterm/web && npm test -- src/__tests__/ssh-mode.test.ts

# E2E 测试（需 docker-compose.test.yml 环境）
cd /ppy_prj/pyterm/autotest && npx playwright test tests/ssh-connection-mode.spec.ts

# 全量回归
cd /ppy_prj/pyterm && python -m pytest app/tests -v --tb=short
cd /ppy_prj/pyterm/web && npm test
cd /ppy_prj/pyterm/autotest && npx playwright test
```

## 八、测试数据准备

| 数据 | 值 | 用途 |
|------|-----|------|
| 直连测试服务器 | `sshd:22` (user: `testuser`, pass: `testpass123`) | docker-compose.test.yml 内置 |
| Agent 测试 ID | `local-agent` | wragent 容器注册 |
| 测试用户名 | `mode_test_{timestamp}` | 避免冲突 |
| 测试连接名 | `直连测试-{ts}` / `Agent测试-{ts}` | 区分两种模式 |
