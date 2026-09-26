# 日志前缀命名规范 v1.0

## 设计原则

前缀 = **通信双方首字母** + **协议标识**，清晰标识"谁和谁通讯、用什么协议"。

## 参与者代号

| 代号 | 角色 | 说明 |
|------|------|------|
| **B** | Browser | 浏览器前端 |
| **G** | Gateway | 本地网关 (Go) |
| **S** | Server | 信令服务器 (FastAPI) |
| **A** | Agent | 远程Agent (Go) |

## 通信链路与前缀

| 链路 | 协议 | 前缀 | 说明 |
|------|------|------|------|
| 浏览器 ↔ 网关 | WebSocket | `[BG-WS]` | 浏览器连接网关 `/ws` 端点 |
| 浏览器 ↔ 信令服务器 | WebSocket | `[BS-WS]` | 浏览器直连模式连接 `/api/ws/webrtc` |
| 网关 ↔ 信令服务器 | WebSocket | `[GS-WS]` | 网关注册、消息中转 |
| Agent ↔ 信令服务器 | WebSocket | `[AS-WS]` | Agent注册、消息中转 |
| 浏览器 ↔ Agent（直连） | WebRTC DC | `[BA-DC]` | 直连模式，DataChannel二进制 |
| 网关 ↔ Agent | WebRTC DC | `[GA-DC]` | 网关模式，DataChannel二进制 |
| 网关内部桥接 | 本地 | `[G-BRIDGE]` | 网关内WS↔DC协议转换 |
| Agent内部桥接 | 本地 | `[A-BRIDGE]` | Agent内DC↔SSH/VNC/TCP桥接 |

## 模式判断逻辑

- `gatewayUrl` 存在 → 网关模式：`[BG-WS]`（信号）+ `[GA-DC]`（网关↔Agent）
- `gatewayUrl` 不存在 → 直连模式：`[BS-WS]`（信令）+ `[BA-DC]`（浏览器↔Agent）

## 修改文件清单

| 文件 | 修改处数 | 组件 |
|------|---------|------|
| `web/src/utils/webrtc.ts` | 53处 | 浏览器前端 |
| `wrgateway/server/handler.go` | 15处 | 网关后端 |
| `wragent/webrtc/signal.go` | 31处 | Agent信令 |
| `wragent/webrtc/peer.go` | 6处 | Agent PeerConnection |
| `wragent/websocket/client.go` | 9处 | Agent WebSocket |
| `app/api_isolated.py` | 13处 | 信令服务器 |
| **合计** | **127处** | |

## 示例输出

### 网关模式
```
[BG-WS] connected, sending connect_gateway
[BG-WS] connect_success, room: room_gw_xxx
[BG-WS] Gateway mode: waiting for DataChannel ready signal
[BG-WS] sendSshConnect: root@192.168.1.100:22
[BG-WS] sshConnected=true
```

### 直连模式
```
[BS-WS] connected, sending connect_agent
[BA-DC] created DC: ssh-terminal
[BA-DC] offer sent
[BA-DC] DC open: ssh-terminal
[BA-DC] sendSshConnect: root@192.168.1.100:22
```
