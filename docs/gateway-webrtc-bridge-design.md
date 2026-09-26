# wrgateway WebRTC 数据桥接设计方案

**版本**: v2.0  
**日期**: 2026-09-15  
**状态**: 实施中

---

## 1. 背景与定位

### 1.1 网关定位

| 属性 | wrgateway | wragent |
|------|-----------|---------|
| 角色 | 浏览器的代理通道 | 执行终端/SFTP 操作 |
| 部署位置 | 公网服务器 | NAT 后 / 公网均可 |
| 鉴权方式 | 验证浏览器 JWT，自身注册带 token | Tailscale 风格注册认证 |
| WebRTC 角色 | **唯一的 WebRTC 发起方**（Detach 端） | **被动接收 DataChannel**（API 模式） |
| 多连接 | 支持（每浏览器 Session 1 个 PeerConn） | 支持（map[roomID]*Peer） |

### 1.2 架构约束

```
Browser --WS--> wrgateway --WebRTC DC--> Agent --SSH--> 目标服务器
                     |
                     +--WS--> Backend (信令中继 + ICE/TURN)
```

- 浏览器不能 WebRTC，只能 WS
- Agent 在 NAT 后，无法被直连
- Gateway 有公网地址，是唯一的 WebRTC 发起方
- coturn 已部署作为 ICE 穿透兜底

## 2. 核心架构图

```
+---------------------------------------------------------------------+
|                      多账户多连接架构                                 |
|                                                                     |
|  +----------+     +----------------------------+     +----------+   |
|  | Browser1 |--WS1-|                            |--DC1-|          |   |
|  | (admin)  |     |      Gateway               |     |  Agent   |   |
|  +----------+     |                            |     |          |   |
|  +----------+     |   Sessions:                |     |  Peers:  |   |
|  | Browser2 |--WS2-|   {ws1->Session{room1}}   |--DC2-|  {room1: |   |
|  | (admin)  |     |   {ws2->Session{room2}}   |     |   Peer{  |   |
|  +----------+     |   {ws3->Session{room3}}   |     |    dc,   |   |
|  +----------+     |                            |--DC3-|    ssh,  |   |
|  | Browser3 |--WS3-|   每个 Session:           |     |    ...}} |   |
|  | (viewer) |     |   - ID (browser_xxx)      |     |  {room2: |   |
|  +----------+     |   - RoomID                |     |   Peer{} |   |
|                   |   - PeerConn (1:1)        |     |  }       |   |
|                   |   - DataChannel (1:1)     |     |          |   |
|                   |   - rawDC (1:1)           |     |          |   |
|                   |   - user_id (from JWT)    |     |          |   |
|                   +----------------------------+     +----------+   |
|                                                                     |
|  数据流:                                                            |
|  Browser <-WS(JSON)-> Gateway <-DC(二进制前缀)-> Agent <-SSH-> 目标  |
+---------------------------------------------------------------------+
```

## 3. DataChannel I/O 模式（关键改动）

### 3.1 v1.2 方案（已废弃）

```
Gateway:  dc.Detach() -> rwc.Read/Write   <- Detached 模式
Agent:    dc.Detach() -> rwc.Read/Write   <- Detached 模式
                    |
    两端同时 Detach 同一个 SCTP association -> SCTP ABORT (abort chunk)
```

### 3.2 v2.0 方案（当前）

```
Gateway:  dc.Detach() -> rwc.Read/Write   <- Detached 模式（唯一 Detach 端）
Agent:    dc.OnMessage + dc.Send          <- API 模式（不 Detach）
                    |
    Gateway 是唯一的 Detach 端，Agent 用标准 API -> 无冲突
```

### 3.3 I/O 模式对比

| 端 | 读取方式 | 写入方式 | 模式 |
|----|----------|----------|------|
| **Gateway** | `rwc.Read(buf)` | `rwc.Write(data)` | Detached（通过 SettingEngine） |
| **Agent** | `dc.OnMessage(func(data))` | `dc.Send(data)` | API（标准回调） |

### 3.4 协议兼容性

两端使用**相同的二进制前缀协议**，只是 I/O 方式不同：

```
消息格式: [1字节前缀][payload字节]

Gateway -> Agent (rwc.Write):
  rwc.Write([0x01][JSON])   <- ssh_connect
  rwc.Write([0x00][raw])    <- terminal_data
  rwc.Write([0x02][JSON])   <- resize

Agent -> Gateway (dc.Send):
  dc.Send([0x00][raw])      <- terminal_data (SSH stdout)
  dc.Send([0x11][JSON])     <- sftp_response
  dc.Send([0xFF][JSON])     <- error
```

## 4. 消息协议

### 4.1 信令消息（通过后端中继）

| 消息类型 | 方向 | 说明 |
|---|---|---|
| `connect_gateway` | 浏览器->网关->后端 | 浏览器请求通过网关连接 Agent |
| `browser_connect` | 后端->Agent / 后端->网关 | 通知准备就绪 |
| `connect_success` | 后端->网关 | 网关可以创建 PeerConnection |
| `offer` | 网关->后端->Agent | WebRTC Offer |
| `answer` | Agent->后端->网关 | WebRTC Answer |
| `candidate` | 双向 | ICE Candidate |

### 4.2 数据消息（网关模式：浏览器<->网关 WSS JSON）

| 消息类型 | 方向 | 字段 | 说明 |
|---|---|---|---|
| `terminal_data` | 双向 | `{type, room_id, data: base64}` | 终端输入/输出 |
| `ssh_connect` | 浏览器->Agent | `{type, room_id, host, port, username, auth_type, password, cols, rows}` | SSH 连接指令 |
| `resize` | 浏览器->Agent | `{type, room_id, cols, rows}` | 终端窗口大小 |
| `sftp_request` | 浏览器->Agent | `{type, room_id, op, req_id, ...}` | SFTP 请求 |
| `sftp_response` | Agent->浏览器 | `{type, room_id, op, req_id, ok, ...}` | SFTP 响应 |
| `error` | Agent->浏览器 | `{type, room_id, detail}` | 错误信息 |

### 4.3 数据消息（Agent 侧：WebRTC DataChannel 二进制前缀）

| 前缀 | 十六进制 | 说明 |
|---|---|---|
| `MSG_TERMINAL` | `0x00` | 终端数据（raw bytes） |
| `MSG_SSH_CONNECT` | `0x01` | SSH 连接指令（JSON） |
| `MSG_RESIZE` | `0x02` | 窗口大小（JSON） |
| `MSG_SFTP_REQUEST` | `0x10` | SFTP 请求（JSON） |
| `MSG_SFTP_RESPONSE` | `0x11` | SFTP 响应（JSON） |
| `MSG_ERROR` | `0xFF` | 错误（JSON） |

### 4.4 网关数据格式转换

| 方向 | WSS JSON | DataChannel 二进制（rwc.Write / dc.Send） |
|---|---|---|
| 浏览器->Agent | `{"type":"terminal_data","data":"<b64>"}` | `[0x00][raw bytes]` |
| 浏览器->Agent | `{"type":"ssh_connect","host":"...",...}` | `[0x01][JSON bytes]` |
| 浏览器->Agent | `{"type":"resize","cols":80,...}` | `[0x02][JSON bytes]` |
| 浏览器->Agent | `{"type":"sftp_request","op":"list",...}` | `[0x10][JSON bytes]` |
| Agent->浏览器 | `{"type":"terminal_data","data":"<b64>"}` | `[0x00][raw bytes]` |
| Agent->浏览器 | `{"type":"sftp_response","ok":true,...}` | `[0x11][JSON bytes]` |
| Agent->浏览器 | `{"type":"error","detail":"..."}` | `[0xFF][JSON bytes]` |

## 5. 两层 Token 体系

| 层级 | Token | 作用 | 数量 | 验证方式 |
|------|-------|------|------|----------|
| **Layer 1: Gateway 注册** | `register_gateway.token` | 证明网关实例有权注册到平台 | 每实例1个 | 后端查 DB `gateway_token` 表 |
| **Layer 2: 浏览器 JWT** | `?token=eyJhbG...` | 证明浏览器用户身份 | 每连接1个 | Gateway 调 `/api/auth/verify` |

```
Gateway 注册: 1个 token (gateway 实例级)
浏览器连接: N个 JWT (每个 Tab 独立)

用户A Tab1 + Tab2 -> 2个 JWT (可能相同，因为同一用户)
用户B Tab3 -> 1个 JWT (不同用户)
Gateway 注册 token -> 1个 (与用户无关)
```

## 6. 完整消息流转

### 6.1 建立连接流程

```
Browser          Gateway           Backend           Agent
  |                 |                 |                 |
  |--connect_gw---->|--connect_gw---->|                 |
  |                 |                 | 验证JWT         |
  |                 |                 | 存储conn        |
  |                 |                 | browser_connect |
  |                 |<----------------|---------------->| 设置roomID
  |                 |                 |                 |
  |                 | connect_success |                 |
  |                 |<----------------|                 |
  |                 |                 |                 |
  |                 | handleConnectSuccess              |
  |                 | -> api.NewPeerConnection           |
  |                 | -> CreateDataChannel("data")       |
  |                 | -> CreateOffer                     |
  |                 |--offer--------->|--offer---------->|
  |                 |                 |                 | NewPeer
  |                 |                 |                 | SetRemoteDesc
  |                 |                 |                 | CreateAnswer
  |                 |<--answer---------|<--answer---------|
  |                 | SetRemoteDesc   |                 |
  |                 |                 |                 |
  |                 | ICE candidates (双向)              |
  |                 |                 |                 |
  |                 | DC("data") open |                 | DC("data") open
  |                 | -> dc.Detach()  |                 | -> OnMessage 注册
  |                 | -> rawDC        |                 |
  |                 |                 |                 |
  | connect_success |                 |                 |
  |<----------------|                 |                 |
  | (onOpen 回调)   |                 |                 |
```

### 6.2 数据传输流程

```
浏览器 -> Agent (SSH 连接):

Browser                Gateway                 Agent
  |                       |                       |
  | WS: {type:ssh_connect}|                       |
  |---------------------->|                       |
  |                       | bridgeWSToDataChannel  |
  |                       | rawDC.Write([0x01][JSON])
  |                       |---------------------->|
  |                       |                       | dc.OnMessage
  |                       |                       | 解析前缀 0x01
  |                       |                       | 建立 SSH 连接
  |                       |                       |
Agent -> 浏览器 (终端输出):

Agent                   Gateway                 Browser
  |                       |                       |
  | dc.Send([0x00][raw])  |                       |
  |---------------------->|                       |
  |                       | bridgeDataChannelToWSLoop
  |                       | base64 -> JSON        |
  |                       | WS: {type:terminal_data}
  |                       |---------------------->|
  |                       |                       | xterm.write()
```

## 7. 文件修改清单

### 7.1 `wragent/webrtc/signal.go` -- Agent（核心改动）

| 改动项 | 当前代码 | 改为 | 原因 |
|--------|----------|------|------|
| `bridgeSSHToDataChannel` | `dc.Detach()` + `rwc.Read()` | `dc.OnMessage` 回调 + 前缀解析 | **避免双端 Detach 冲突** |
| SSH 连接后桥接 | `go io.Copy(rwc, stdout)` + `go io.Copy(stdin, rwc)` | `dc.OnMessage` 分发 + `dc.Send()` 发送 | API 模式写入 |
| `sendTerminalData` | `sendMsg(rwc, ...)` | `dc.Send([prefix][data])` | API 模式 |
| `sendErrorMsg` | `sendMsg(rwc, ...)` | `dc.Send([prefix][data])` | API 模式 |
| `sendSftpResponse/Ok/Error` | `sendMsg(rwc, ...)` | `dc.Send([prefix][data])` | API 模式 |
| `handleSFTPData` | `sendSftpError(rwc, ...)` | `sendSftpError(dc, ...)` 参数改为 dc | 适配 API 模式 |

### 7.2 `wrgateway/server/handler.go` -- Gateway

| 改动项 | 当前代码 | 改为 | 原因 |
|--------|----------|------|------|
| `SettingEngine` | 已有 `DetachDataChannels()` | 保持不变 | Gateway 是唯一 Detach 端 |
| `readPump` default | 转发给 signaling server | **不转发，仅 log** | 防止消息循环 |
| `bridgeDataChannelToWSLoop` | 读 rawDC -> JSON -> SendCh | 保持不变 | -- |
| `bridgeWSToDataChannel` | 写 rawDC | 保持不变 | -- |

### 7.3 `app/api_isolated.py` -- Backend

| 改动项 | 当前代码 | 改为 | 原因 |
|--------|----------|------|------|
| `register_gateway` | token 验证 + 存储 | **保留** | 安全性 |
| `connect_gateway` 路由 | offer/answer/candidate | 保持不变 | -- |

### 7.4 `web/src/utils/webrtc.ts` -- Frontend

| 改动项 | 当前代码 | 改为 | 原因 |
|--------|----------|------|------|
| Gateway 模式 | 跳过 createPeer，WS 发送 | 保持不变 | -- |
| `sendTerminal` 大数据 | 直接 btoa | 改用分块 base64 | 避免栈溢出 |

## 8. 变更记录

| 版本 | 日期 | 变更内容 |
|---|---|---|
| v1.0 | 2026-09-15 | 初版方案，架构设计 + 文件修改清单 |
| v1.1 | 2026-09-15 | Agent 多会话支持；WSS 独立连接决策分析 |
| v1.2 | 2026-09-15 | 深度审查：DataChannel 双模式不兼容根因分析 |
| **v2.0** | **2026-09-15** | **架构重定义：Gateway 唯一 Detach 端 + Agent API 模式；多账户多连接架构；两层 Token 体系；abort chunk 根因修复** |
