# wrgateway WebRTC 数据桥接设计方案

**版本**: v1.2  
**日期**: 2026-09-15  
**状态**: 实施中

---

## 1. 背景与约束

| 约束 | 说明 |
|---|---|
| 浏览器不支持 WebRTC | 浏览器只能使用 HTTP/HTTPS/WS/SOCKS5 |
| Agent 无公网地址 | Agent 在 NAT 后，无法被直接连接 |
| 网关有公网地址 | 网关可以被浏览器 WSS 连接，也可以主动发起 WebRTC |
| coturn 已部署 | TURN relay 作为 ICE 穿透兜底 |

## 2. 架构

```
Browser --WSS--> wrgateway <--WebRTC DataChannel--> Agent (NAT后)
                     |
                     +--WSS--> Backend (信令中继 + ICE/TURN配置)
```

- **浏览器 -- 网关**: WSS（浏览器不能 WebRTC）
- **网关 -- Agent**: WebRTC DataChannel（穿透 NAT），**双端 Detached 模式**
- **网关 -- 后端**: WSS（信令中继 + ICE 配置下发）
- 网关是协议桥：WSS <--> WebRTC DataChannel

## 3. DataChannel I/O 模式约定

**关键约束：pion/webrtc DataChannel 有两种 I/O 模式，不可混用。**

| 模式 | 发送 | 接收 | 适用 |
|---|---|---|---|
| API 模式 | `dc.Send(data)` | `dc.OnMessage` | 双端都用 API |
| Detached 模式 | `rwc.Write(data)` | `rwc.Read(buf)` | 双端都用 Detached |

**本方案约定：网关和 Agent 统一使用 Detached 模式。**

调用链：
1. 网关 `dc.OnOpen` → `dc.Detach()` → 获得 `rwc io.ReadWriteCloser`
2. Agent `dc.OnOpen` → `dc.Detach()` → 获得 `rwc io.ReadWriteCloser`
3. 双方通过 `rwc.Read()` / `rwc.Write()` 交换原始 SCTP 字节流

## 4. 消息协议

### 4.1 信令消息（通过后端中继）

| 消息类型 | 方向 | 说明 |
|---|---|---|
| `connect_gateway` | 浏览器→网关→后端 | 浏览器请求通过网关连接 Agent |
| `browser_connect` | 后端→Agent / 后端→网关 | 通知准备就绪 |
| `connect_success` | 后端→网关 | 网关可以创建 PeerConnection |
| `offer` | 网关→后端→Agent | WebRTC Offer |
| `answer` | Agent→后端→网关 | WebRTC Answer |
| `candidate` | 双向 | ICE Candidate |

### 4.2 数据消息（网关模式：浏览器<-->网关 WSS JSON）

| 消息类型 | 方向 | 字段 | 说明 |
|---|---|---|---|
| `terminal_data` | 双向 | `{type, room_id, data: base64}` | 终端输入/输出 |
| `ssh_connect` | 浏览器→Agent | `{type, room_id, host, port, username, auth_type, password, cols, rows}` | SSH 连接指令 |
| `resize` | 浏览器→Agent | `{type, room_id, cols, rows}` | 终端窗口大小 |
| `sftp_request` | 浏览器→Agent | `{type, room_id, op, req_id, ...}` | SFTP 请求 |
| `sftp_response` | Agent→浏览器 | `{type, room_id, op, req_id, ok, ...}` | SFTP 响应 |
| `error` | Agent→浏览器 | `{type, room_id, detail}` | 错误信息 |

### 4.3 数据消息（Agent 侧：WebRTC DataChannel 二进制前缀）

| 前缀 | 十六进制 | 说明 |
|---|---|---|
| `MSG_TERMINAL` | `0x00` | 终端数据（raw bytes） |
| `MSG_SSH_CONNECT` | `0x01` | SSH 连接指令（JSON） |
| `MSG_RESIZE` | `0x02` | 窗口大小（JSON） |
| `MSG_SFTP_REQUEST` | `0x10` | SFTP 请求（JSON） |
| `MSG_SFTP_RESPONSE` | `0x11` | SFTP 响应（JSON） |
| `MSG_ERROR` | `0xFF` | 错误（JSON） |

### 4.4 网关数据格式转换（Detached 模式）

| 方向 | WSS JSON | DataChannel 二进制（rwc.Write） |
|---|---|---|
| 浏览器→Agent | `{"type":"terminal_data","data":"<b64>"}` | `[0x00][raw bytes]` |
| 浏览器→Agent | `{"type":"ssh_connect","host":"...",...}` | `[0x01][JSON bytes]` |
| 浏览器→Agent | `{"type":"resize","cols":80,...}` | `[0x02][JSON bytes]` |
| 浏览器→Agent | `{"type":"sftp_request","op":"list",...}` | `[0x10][JSON bytes]` |
| Agent→浏览器 | `{"type":"terminal_data","data":"<b64>"}` | `[0x00][raw bytes]` |
| Agent→浏览器 | `{"type":"sftp_response","ok":true,...}` | `[0x11][JSON bytes]` |
| Agent→浏览器 | `{"type":"error","detail":"..."}` | `[0xFF][JSON bytes]` |

## 5. 完整消息流转

| 步骤 | 发送方 | 消息 | 路径 | 接收方 |
|---|---|---|---|---|
| 1 | 浏览器 | `connect_gateway` | WSS→网关→WSS→后端 | 后端处理 |
| 2 | 后端 | `browser_connect` | WSS→Agent + WSS→网关 | 双方准备 |
| 3 | 后端 | `connect_success` | WSS→网关 | 网关创建 PeerConnection |
| 4 | **网关** | `offer` | WSS→后端→WSS→Agent | Agent 创建 PeerConnection |
| 5 | Agent | `answer` | WSS→后端→WSS→网关 | 网关设置 remote desc |
| 6 | 双方 | `candidate` | 通过后端双向中继 | ICE 连通 |
| 7 | -- | **DataChannel 建立（双端 Detach）** | 网关 <--> Agent | -- |
| 8 | 浏览器 | `ssh_connect` | WSS→网关→**rwc.Write**→Agent | Agent 建立 SSH |
| 9 | 浏览器 | `terminal_data` | WSS→网关→**rwc.Write**→Agent | SSH stdin |
| 10 | Agent | `terminal_data` | **rwc.Write**→网关→WSS→浏览器 | SSH stdout→xterm |

## 6. 文件修改清单

### 6.1 `app/api_isolated.py` -- 后端

| 改动点 | 说明 | 状态 |
|---|---|---|
| `register_gateway` 响应 | 加入 `"ice_servers": _ice_servers` | ✅ |
| `connect_gateway` 处理 | 新增：发 `browser_connect` 给 `agent_ws` | ✅ |

### 6.2 `web/src/utils/webrtc.ts` -- 前端

| 改动点 | 说明 | 状态 |
|---|---|---|
| `createPeer()` | 网关模式跳过 PeerConnection，直接 onOpen | ✅ |
| `sendTerminal()` | 网关模式走 `signalWs.send()` (JSON+base64) | ✅ |
| `sendSshConnect()` | 网关模式走 `signalWs.send()` (JSON) | ✅ |
| `sendResize()` | 网关模式走 `signalWs.send()` (JSON) | ✅ |
| `sendSftpRequest()` | 网关模式走 `signalWs.send()` (JSON) | ✅ |
| `handleSignal()` | 新增 terminal_data / sftp_response 处理 | ✅ |
| `isReady()` | 网关模式检查 WSS 而非 DataChannel | ✅ |
| `sendTerminal()` 大数据 | 改用分块 base64 避免栈溢出 | 🔲 |

### 6.3 `wrgateway/server/handler.go` -- 网关（核心重写）

| 改动点 | 说明 | 状态 |
|---|---|---|
| `Session` 结构体 | 新增 `rawDC io.ReadWriteCloser` 字段 | 🔲 |
| `readPump` | 收到 `connect_gateway` 时提取 room_id 绑定 session | 🔲 |
| `readPump` defer | 关闭 PeerConnection 时同步关闭 rawDC | 🔲 |
| `bridgeWSToDataChannel` | **改用 `session.rawDC.Write()` 替代 `dc.Send()`** | 🔲 |
| 新增 `bridgeDataChannelToWSLoop` | 循环读 `rwc.Read()` → 转 JSON → `session.SendCh` | 🔲 |
| `handleConnectSuccess` | 用 session.RoomID 精确匹配查找 | 🔲 |
| `handleConnectSuccess` | ICE 断开时发送 error 通知浏览器 | 🔲 |
| `SetClient` | `browser_connect` 时设置 session.RoomID | 🔲 |

### 6.4 `wrgateway/go.mod` -- 无改动

`pion/webrtc/v4` 已在依赖中。

### 6.5 `wragent/webrtc/signal.go` -- Agent

| 改动点 | 说明 | 状态 |
|---|---|---|
| `h.peer` → `h.p map[string]*Peer` | 按 room_id 管理多个 PeerConnection | ✅ |
| `handleOffer` | 按 room_id 存 Peer，加 mutex | 🔲 加 mutex |
| `handleAnswer/handleCandidate` | 按 room_id 查找 Peer | ✅ |
| `sendAnswer/sendCandidate` | 加 roomID 参数 | ✅ |
| `Close()` | 遍历关闭所有 Peer | ✅ |
| `sendSftpResponse` / `sendSftpOk` / `sendSftpError` | JSON 字段 `_type` → `type` | 🔲 |

### 6.6 `web/src/components/SshManager.vue` -- 无改动

## 7. 变更记录

| 版本 | 日期 | 变更内容 |
|---|---|---|
| v1.0 | 2026-09-15 | 初版方案，架构设计 + 文件修改清单 |
| v1.1 | 2026-09-15 | Agent 多会话支持；WSS 独立连接决策分析；直连模式分析 |
| v1.2 | 2026-09-15 | **深度审查**：DataChannel 双模式不兼容根因分析；P0/P1/P2/P3 分级问题清单；Detached 模式统一约定；SFTP 字段名统一；Session rawDC 生命周期管理；完整修复方案 |
