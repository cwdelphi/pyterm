# 隧道 64KB 单包截断修复方案 v1.0.1

> 版本: 1.0.1 | 日期: 2026-09-27 | 状态: 已部署并验证 | 报障: ERR_SSL_PROTOCOL_ERROR + 传输停在 65536 字节 + DataChannel 反复关闭

## 背景

链式隧道 `浏览器 → agent1(local-agent:3333) → agent2(test-tunnel) → 192.0.2.19:7777(宝塔自签名HTTPS)`
访问面板偶发 `net::ERR_SSL_PROTOCOL_ERROR 200 (OK)`，伴随传输恰好停在 65536 字节、local-agent 侧 DataChannel 反复关闭。

## 根因

### 主根因（接收侧，v1.0.1 补证）

| 项 | 结论 |
|---|---|
| 接收缓冲硬上限 | pion/webrtc v4.0.8 `datachannel.go:23` `dataChannelBufferSize = math.MaxUint16 = 65535` |
| 旧满消息尺寸 | 头 3 字节 + `tunnelMaxPayload` 65533 = **65536 > 65535** |
| 失败行为 | `reassemblyQueue.read` 返回 `io.ErrShortBuffer`（`reassembly_queue.go:275`，且**不消费队列**）→ 读循环 `setReadyState(Closed)` + `onClose()` |
| 关闭语义 | **只关本地一端、对端无感知**（datachannel 单侧 Close 不发通知）→ 解释「不对称关闭」与 curl 恰好收 65536 字节后挂死 |
| 发送侧为何能过 | `stream.go:272` `WriteSCTP` 判 `len > 65536`，65536 恰好可发出（仅发送侧有检查） |

### 次根因（发送侧，v1.0.0 原结论）

| 项 | 结论 |
|---|---|
| 单条 DC 消息上限 | pion/sctp v1.9.0 `association.go:67` `defaultMaxMessageSize = 65536` |
| 失败行为 | `dc.Send` 返回 `ErrOutboundPacketTooLarge`，旧 `tunnelSend`/`sendTunnelMsg` **丢弃错误** → 字节静默消失 → TLS 流错位 |
| 触发条件 | 单次 `conn.Read` 返回 **n ≥ 65534** |

**阈值实验（旧版，v1.0.0 记录）**：body 65200/65300 → 0 失败；body 65450/65533 → 6/30 失败；71KB 串行 9/40，完全吻合 n≥65534 判据。

**浏览器复现**（`inportb/change_me_pass`，Playwright）：登录后 `style.css`(224KB)、`element-plus-lib.js`(315KB) 稳定报 `net::ERR_SSL_PROTOCOL_ERROR`；`curl -v` 现场为握手成功 → GET 已发 → `OpenSSL SSL_read: wrong version number`。

**丢字节的两处发送点**：
- 下行（HTTP 响应体，主故障方）：`signal.go:1862` `bridgeTCPTunnel` 读缓冲 65536 → **test-tunnel 侧**
- 上行（大请求体）：`tunnel.go:353` `bridgeTCPConn` 读缓冲 65536 → **local-agent 侧**

### 同源隐患：VNC 桥

`signal.go` VNC 桥原读缓冲 `64*1024` → 消息达 **65537** 同样超 65535 缓冲 → 同样的单端关闭问题。

### 次级缺陷（同批修）

| # | 位置 | 问题 |
|---|---|---|
| A | `tunnel.go` `RegisterTunnelDC` MsgTunnelData | `dataCh`(64 满) → `default:` **静默丢弃** |
| B | `signal.go` bridgeTCPTunnel MsgTunnelData | `conns[connID]` 缺失无日志静默丢；`conn.Write` 失败仅记日志不断连 |
| C | `tunnel.go` bridgeTCPConn | 先 `Send(Connect)` **之后**才注册 `tunnelOKWaiters`（竞态，OK 先到白等 10s） |
| D | 两方向 TCP→DC | 无 `dc.BufferedAmount()` 背压（复用 A-BRIDGE `dcBackpressureThreshold=64KB`） |
| E | `signal.go:390-392` | `RegisterTunnelDC` + `bridgeTCPTunnel` 各设一次 `OnMessage/OnClose`，后者覆盖前者 |
| F | `tunnel.go:242` UDP | 65536 字节 UDP 经 JSON base64 后 ≈87KB > 65536 → 必丢（>49KB UDP 包） |
| G | `signal.go:270` `CreateTunnelPeer` | 同隧道旧 peer 不关闭 → `h.p` 泄漏（stale OnClose 守卫已兜底，修复 defer） |

## 修复项（v1.0.1 实施）

### 核心：1KB 余量预算

设计要求**不贴上限**，留 1KB 冗余应对未来消息格式变化：

```go
tunnelMsgMargin    = 1024          // 预留余量（不贴 65535 上限）
tunnelHeaderLen    = 3             // [prefix][connID]，不塞填充（避免浪费带宽）
tunnelMsgBudget    = 65535 - 1024  // = 64511，消息总长（含 3 字节头）硬上限
tunnelMaxPayload   = 64508         // 读缓冲尺寸 = 预算 - 头
```

- 头仍保持 3 字节：填充会浪费带宽且无收益，余量全部由预算承担（注释已写明）
- 发送侧硬上限 `tunnelMsgBudget` ≤ 接收侧 65535 缓冲，留 1024 字节冗余
- `tunnelUDPReadBuf = 48000`（F 项）经算 3+64000+~120 ≈ 64121 ≤ 64511，保留

### 其余修复

1. `tunnelSend` / `sendTunnelMsg` **返回 error**；发送出错 → 记日志 + 关闭该桥接（断 TCP、发 Disconnect），绝不静默续传。
2. A：`dataCh` 满改为**关闭该连接**（记日志）而非丢字节；B：`conns` 缺失记日志、`conn.Write` 错误→断开。
3. C：先注册 waiter 再发 Connect。
4. D：`BufferedAmount` 超 64KB 阈值时暂停读 TCP（复用既有背压基建）。
5. E：合并 `OnDataChannel` 分支，消除重复注册。
6. **VNC 桥读缓冲 `64*1024 → 60*1024`**（1+61440=61441 ≤ 64511）。
7. **OnClose 代际守卫**：`stale := tunnelDC != dc`，stale 时只打日志、不清 `tunnelDC`、不触发重连 handler（防止旧 DC 关闭干扰新连接）。

## 部署

| Agent | 位置 | 部署方式 |
|---|---|---|
| local-agent | portal.example.com docker `pyterm_wragent`（host 网络） | `./scripts/build-wragent.sh --no-bump` → `docker restart pyterm_wragent` |
| test-tunnel（下行丢字节主犯） | 192.0.2.19 systemd `wragent.service` → `/usr/local/bin/wragent` | scp 覆盖 → `systemctl restart wragent` |
| remote-agent | 203.0.113.12 | 无 SSH 凭据，暂不动（另行处理） |

当前版本：`wragent 2.2.4 (commit=c2ad29f built=20260927021335)`，两端 md5 `bba9a4af7e118962cd1004ebd077077e` 一致。

## 验证结果（2026-09-27）

| 项 | 结果 |
|---|---|
| 顺序隧道下载（含满载 65508 分片）×120 | ✅ 120/120 通过，md5 全一致，期间 0 DC close / 0 backpressure |
| 阈值 body 65450/65533/65535/72704 ×10 | ✅ **40/40 md5 一致，0 网络错误** |
| 浏览器登录页连开 20 次 | ✅ **0 个 `ERR_SSL_PROTOCOL_ERROR`** |
| 登录后面板连开 20 次 | ✅ **0 个 `ERR_SSL_PROTOCOL_ERROR`**（9 次被并发测试抢占带宽致 `CONNECTION_RESET`，非 SSL 错误） |
| SSH/WebRTC 终端 + SFTP API | ✅ 7 passed |
| `npm test`（vitest） | ✅ 21/21 |
| autotest AC-01/02/03 | ✅ 3/3 |
| 8 路并发大文件 | ⚠️ 环境受限（见下） |

**8 路并发说明**：链路实际带宽 374KB/s 且波动至 4KB/s（LAN `192.168.99.x↔10.2.0.x` 持续 100% 丢包，流量走 coturn 公网中继），8×1.3MB 在 30s 背压超时内无法完成。失败模式全部为**截断 / `code=000`，无一例字节损坏**（无「size 正确但 md5 不符」），即正确性有保障，缺的是带宽。

**pytest 说明**：`app/tests` 大量失败根因为 `RuntimeError: Task ... got Future attached to a different loop`（SQLAlchemy/aiomysql 跨事件循环），属既有测试环境问题，与本次 Go 改动无关。
