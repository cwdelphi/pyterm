# 稳定性修复 + VNC 修复 + 测试方案

> 版本: v1.0
> 日期: 2026-09-16
> 状态: 实施中

---

## 一、问题清单与修复方案

### P0 - 必须修复（功能不可用/数据丢失）

| # | 模块 | 问题 | 文件:行号 | 修复方案 |
|---|------|------|----------|---------|
| 1 | SFTP | Agent 端 dc.Send 无互斥 | `signal.go:326-331` | 加 mutex |
| 2 | SFTP | 大文件读取无限制 | `signal.go:663-670` | 限制 1MB |
| 3 | SFTP | SFTP 客户端每次重建 | `signal.go:631-636` | 连接池复用 |
| 4 | Gateway | SendCh 仅 64 满则丢弃 | `handler.go:111,352-356` | 增大到 1024 |
| 5 | VNC | resize/clipboard JSON 直接写入 TCP | `signal.go:781` | 删除 0x23/0x24，统一走 0x22 |
| 6 | VNC | openVncTab 发送虚假 SSH connect | `SshManager.vue:225` | 删除 |

### P1 - 建议修复（稳定性提升）

| # | 模块 | 问题 | 文件:行号 | 修复方案 |
|---|------|------|----------|---------|
| 7 | Gateway | pendingMsgs 无上限 | `handler.go:211-216` | 限制 1000 条 |
| 8 | Gateway | handleBrowserConnect 竞态 | `handler.go:363-383` | 精确匹配 |
| 9 | SFTP | SFTP 30s 固定超时 | `webrtc.ts:564-569` | 动态超时 |
| 10 | Agent | dc.Send 无错误处理 | `signal.go:326-331` | 检查状态 |

---

## 二、VNC 修复核心思路

noVNC 是完整 RFB 客户端，Agent 只需透明代理。

```
浏览器 noVNC
  │ transport.send(rfb_bytes)  ← noVNC 自动发送 RFB 协议
  ▼
DataChannelTransport.send()
  │ sendVncInput(rfb_bytes)   ← 统一走 0x22
  ▼
Agent bridgeVNC
  │ tcpConn.Write(rfb_bytes)  ← 纯转发，不解析
  ▼
VNC Server 收到正确的 RFB 消息
```

删除 MsgVNCResize (0x23) 和 MsgVNCClipboard (0x24) 的路由，让 noVNC 通过 transport 自动处理。

---

## 三、测试方案

### 测试环境

`pyterm_test_ssh` 容器（`nexterm_test_ubuntu-test-server` 镜像）已提供：
- SSH: 2222 → sshuser / change_me_sshpass
- VNC: 5900 → vncPass123
- RDP: 3389 → rdpuser / rdpPass123

### 测试用例

#### SFTP 稳定性测试

| 用例 ID | 测试场景 | 验证点 | 截图 |
|---------|---------|--------|------|
| S-01 | SFTP 客户端复用 | 连续点击 10 个目录，响应 <2s | `sftp-连续浏览.png` |
| S-02 | 大文件限制 | >1MB 文件预览返回错误提示 | `sftp-大文件限制.png` |
| S-03 | 终端+SFTP 并发 | 终端无卡顿 | `sftp-并发终端.png` |
| S-04 | dc.Send 互斥 | 3个终端输出独立，无乱序 | `sftp-并发dc.png` |

#### VNC E2E 测试

| 用例 ID | 测试场景 | 验证点 | 截图 |
|---------|---------|--------|------|
| V-01 | VNC 连接建立 | RFB 握手成功，桌面显示 | `vnc-连接成功.png` |
| V-02 | VNC 窗口调整 | 分辨率同步变化 | `vnc-窗口调整.png` |
| V-03 | VNC 剪贴板 | 浏览器↔远程剪贴板同步 | `vnc-剪贴板.png` |
| V-04 | VNC 输入 | 鼠标键盘事件正确传递 | `vnc-输入事件.png` |
| V-05 | VNC 质量切换 | 画面质量变化 | `vnc-质量切换.png` |
| V-06 | VNC 截图 | 下载 PNG 文件 | `vnc-截图.png` |
| V-07 | 无虚假 SSH connect | Agent 日志无 SSH 错误 | — |

#### 多连接隔离测试

| 用例 ID | 测试场景 | 验证点 | 截图 |
|---------|---------|--------|------|
| M-01 | 3个终端并发 | 各 Tab 独立运行 | `multi-3tabs.png` |
| M-02 | 终端+VNC 并发 | 互不干扰 | `multi-term-vnc.png` |
| M-03 | 终端+SFTP 并发 | 终端不卡顿 | `multi-term-sftp.png` |
| M-04 | Tab 关闭清理 | 资源正确释放 | `multi-tab-close.png` |

#### Gateway 稳定性测试

| 用例 ID | 测试场景 | 验证点 | 截图 |
|---------|---------|--------|------|
| G-01 | SendCh 缓冲 | 终端高速输出无丢失 | `gw-sendch.png` |
| G-02 | pendingMsgs 上限 | 不超过 1000 条 | `gw-pending.png` |
| G-03 | 多用户并发 | Session 正确匹配 | `gw-multi-user.png` |

### 测试脚本结构

```
autotest/tests/
├── sftp-stability/
│   ├── sftp-client-reuse.spec.ts      # S-01
│   ├── sftp-large-file.spec.ts        # S-02
│   └── sftp-concurrent.spec.ts        # S-03, S-04
├── vnc-e2e/
│   ├── vnc-basic.spec.ts              # V-01~V-04
│   ├── vnc-features.spec.ts           # V-05~V-06
│   └── vnc-no-fake-ssh.spec.ts        # V-07
├── multi-connection/
│   ├── multi-tab.spec.ts              # M-01, M-04
│   └── multi-mode.spec.ts             # M-02, M-03
└── gateway-stability/
    ├── gw-buffer.spec.ts              # G-01, G-02
    └── gw-multi-user.spec.ts          # G-03
```

### 浏览器控制台日志捕获

使用 Playwright 的 `page.on('console')` 捕获所有控制台输出，用于：
- 验证无 JS 错误
- 验证无 WebRTC 连接错误
- 验证无协议解析错误
- 验证无超时/内存溢出错误

### 截图策略

每个测试用例在关键步骤截图，用于：
- 视觉验证功能是否达到预期
- 回归测试对比
- 问题排查
