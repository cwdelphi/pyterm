# 新架构 E2E 测试方案 v1.0

> 版本: v1.0 | 日期: 2026-09-14 | 状态: 执行中

## 一、术语定义

| 术语 | 含义 |
|------|------|
| 直连 Agent | 浏览器通过管理平台信令直连 Agent（场景 A/B） |
| 本地网关 | 与管理平台同机部署的 wrgateway（场景 C/D 使用） |
| 远程网关 | 与其他服务器部署的 wrgateway（本方案**不测试**） |
| 本地 Agent | 与管理平台同机的 wragent（场景 A/C） |
| 远程 Agent | 其他机器的 wragent（场景 B/D） |

## 二、连接路径矩阵

| 场景 | 路径 | 本地网关 | Agent |
|------|------|:--------:|-------|
| **A** | 浏览器 → 信令 → 本地 Agent | ✗ | 本地 |
| **B** | 浏览器 → 信令 → 远程 Agent | ✗ | 远程 |
| **C** | 浏览器 → **本地网关** → 信令 → 本地 Agent | ✓ | 本地 |
| **D** | 浏览器 → **本地网关** → 信令 → 远程 Agent | ✓ | 远程 |

## 三、测试目录结构

```
autotest/tests/
├── helpers.ts                              ← 增强
├── fixtures/
│   └── test-data.ts                        ← 测试数据工厂
│
├── agent-direct/                           ← 直连 Agent (场景 A+B)
│   ├── agent-local-ssh.spec.ts             # A: 本地 Agent SSH
│   ├── agent-local-sftp.spec.ts            # A: 本地 Agent SFTP
│   ├── agent-remote-ssh.spec.ts            # B: 远程 Agent SSH
│   └── agent-remote-sftp.spec.ts           # B: 远程 Agent SFTP
│
├── gateway-agent/                          ← 本地网关模式 (场景 C+D)
│   ├── gw-local-agent-ssh.spec.ts          # C: 本地网关+本地 Agent SSH
│   ├── gw-local-agent-sftp.spec.ts         # C: 本地网关+本地 Agent SFTP
│   ├── gw-remote-agent-ssh.spec.ts         # D: 本地网关+远程 Agent SSH
│   └── gw-remote-agent-sftp.spec.ts        # D: 本地网关+远程 Agent SFTP
│
└── gateway-lifecycle/                      ← 本地网关生命周期
    └── gateway-online.spec.ts              # 在线/断线/重连
```

## 四、完整测试用例表（32 用例）

### 场景 A: 直连 + 本地 Agent（11 用例）

| ID | 业务 | 用例名称 | 操作 | 预期 | 优先级 |
|----|------|----------|------|------|--------|
| A-S-01 | SSH | 添加本地Agent连接 | 添加→选本地Agent→填信息→保存 | 连接出现在列表 | P0 |
| A-S-02 | SSH | 打开SSH终端 | 双击→SSH | 标签页打开，status=connected | P0 |
| A-S-03 | SSH | 终端输入输出 | `echo A_TEST_OK` | 输出含 `A_TEST_OK` | P0 |
| A-S-04 | SSH | 终端resize | 视口 800x600→1280x720 | 无报错，可继续输入 | P1 |
| A-S-05 | SSH | 断线重连 | offline→恢复→重连 | 终端恢复 | P1 |
| A-F-01 | SFTP | 打开文件浏览器 | 双击→文件管理 | 文件列表正确 | P0 |
| A-F-02 | SFTP | 面包屑导航 | 点击路径目录 | 跳转目录 | P1 |
| A-F-03 | SFTP | 新建目录 | 工具栏→新建目录 | 目录出现 | P1 |
| A-F-04 | SFTP | 新建文件 | 工具栏→新建文件 | 文件出现 | P1 |
| A-F-05 | SFTP | 重命名 | 右键→重命名 | 名称更新 | P2 |
| A-F-06 | SFTP | 删除 | 右键→删除→确认 | 文件消失 | P2 |

### 场景 B: 直连 + 远程 Agent（6 用例）

| ID | 业务 | 用例名称 | 操作 | 预期 | 优先级 |
|----|------|----------|------|------|--------|
| B-S-01 | SSH | 添加远程Agent连接 | 添加→选远程Agent→填信息→保存 | 连接创建成功 | P0 |
| B-S-02 | SSH | 打开SSH终端 | 双击→SSH | 终端 connected | P0 |
| B-S-03 | SSH | 终端输入输出 | `echo B_TEST_OK` | 输出含 `B_TEST_OK` | P0 |
| B-F-01 | SFTP | 打开文件浏览器 | 双击→文件管理 | 文件列表正确 | P0 |
| B-F-02 | SFTP | 新建目录/文件 | 工具栏操作 | 创建成功 | P1 |
| B-F-03 | SFTP | 重命名/删除 | 右键菜单 | 操作成功 | P2 |

### 场景 C: 本地网关 + 本地 Agent（6 用例）

| ID | 业务 | 用例名称 | 操作 | 预期 | 优先级 |
|----|------|----------|------|------|--------|
| C-S-01 | SSH | 通过本地网关添加连接 | 添加→选本地Agent→**选本地网关**→保存 | `gateway_id=local-gateway` | P0 |
| C-S-02 | SSH | 通过本地网关打开终端 | 双击→SSH | 终端 connected（路径: 浏览器→本地网关→信令→Agent） | P0 |
| C-S-03 | SSH | 通过本地网关终端交互 | `echo C_GW_LOCAL` | 输出含 `C_GW_LOCAL` | P0 |
| C-F-01 | SFTP | 通过本地网关文件浏览器 | 双击→文件管理 | 文件列表正确 | P0 |
| C-F-02 | SFTP | 通过本地网关新建目录/文件 | 工具栏操作 | 创建成功 | P1 |
| C-F-03 | SFTP | 通过本地网关上传文件 | 拖拽上传 | 上传成功 | P1 |

### 场景 D: 本地网关 + 远程 Agent（4 用例）

| ID | 业务 | 用例名称 | 操作 | 预期 | 优先级 |
|----|------|----------|------|------|--------|
| D-S-01 | SSH | 本地网关+远程Agent终端 | 添加→选远程Agent→选本地网关→打开终端 | 终端 connected | P0 |
| D-S-02 | SSH | 终端输入输出 | `echo D_GW_REMOTE` | 输出含 `D_GW_REMOTE` | P0 |
| D-F-01 | SFTP | 本地网关+远程Agent文件 | 打开文件浏览器 | 文件列表正确 | P0 |
| D-F-02 | SFTP | 文件操作 | 新建/删除 | 操作成功 | P1 |

### 本地网关生命周期（5 用例）

| ID | 用例名称 | 操作 | 预期 | 优先级 |
|----|----------|------|------|--------|
| GW-01 | 本地网关在线状态 | 启动→查API | `online=true` | P0 |
| GW-02 | 本地网关断线检测 | 停止→查API | `online=false` | P1 |
| GW-03 | 本地网关重启恢复 | 重启→查API | `online=true` | P1 |
| GW-04 | 本地网关Agent转发 | 通过网关连接→执行命令 | 命令正常执行 | P0 |
| GW-05 | 本地网关多会话 | 同时开2个终端标签页 | 两个终端独立工作 | P1 |

## 五、helpers.ts 增强

```typescript
// ── 认证 ──
export async function loginAsAdmin(page: Page)        // 已有
export async function apiLogin(page: Page): Promise<string>

// ── 连接管理 ──
export async function addConnection(page: Page, opts: {
  name: string; agentId: string; host: string; port?: number;
  username: string; password: string; gatewayId?: string;
})
export async function cleanupByPrefix(page: Page, prefix: string)

// ── 终端 ──
export async function openTerminalTab(page: Page, connName: string)
export async function assertTerminalEcho(page: Page, cmd: string, expected: string)

// ── SFTP ──
export async function openFileBrowserTab(page: Page, connName: string)
```

## 六、playwright.config.ts

```typescript
projects: [
  { name: 'agent-direct',       use: { browserName: 'chromium' } },
  { name: 'gateway-agent',      use: { browserName: 'chromium' } },
  { name: 'gateway-lifecycle',  use: { browserName: 'chromium' } },
]
```

## 七、UX 测试仪表盘

```
┌─────────────────────────────────────────────────────────────────────┐
│              🐟 泡鱼终端 - 新架构 E2E 测试报告                       │
│              2026-09-14 13:45  耗时: 2m 34s                        │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  ┌─────────┐ ┌─────────┐ ┌─────────┐ ┌─────────┐ ┌─────────┐    │
│  │ 总用例  │ │ 通过 ✓  │ │ 失败 ✗  │ │ 跳过 ○  │ │ 通过率  │    │
│  │   32    │ │   28    │ │    3    │ │    1    │ │ 87.5%   │    │
│  └─────────┘ └─────────┘ └─────────┘ └─────────┘ └─────────┘    │
│                                                                     │
│  ┌───────────────────────────────────────────────────────────┐     │
│  │ 场景覆盖度                                                 │     │
│  │                                                           │     │
│  │  A  直连+本地Agent   ████████████████████  11/11  100%   │     │
│  │  B  直连+远程Agent   ██████████████░░░░░░   6/6   100%   │     │
│  │  C  本地网关+本地    ██████████████░░░░░░   6/6   100%   │     │
│  │  D  本地网关+远程    ████████░░░░░░░░░░░░   2/4    50%   │     │
│  │  GW 本地网关生命周期  ████████████░░░░░░░░   3/5    60%   │     │
│  └───────────────────────────────────────────────────────────┘     │
│                                                                     │
│  ┌───────────────────────────────────────────────────────────┐     │
│  │ 业务覆盖度                                                 │     │
│  │                                                           │     │
│  │  SSH 终端       ████████████████████  11/11  100%        │     │
│  │  SFTP 文件      ████████████████████  14/14  100%        │     │
│  │  网关生命周期    ████████████░░░░░░░░   3/5    60%        │     │
│  └───────────────────────────────────────────────────────────┘     │
└─────────────────────────────────────────────────────────────────────┘
```
