# TCP隧道 + Token管理 方案 v1.0

> 日期: 2026-09-18
> 状态: 待执行

---

## 一、管理后台 — Agent管理增加 Token 查看/复制

### 典型场景

**场景：管理员为新服务器部署 wragent，需要复制 Token 写入配置文件。**

1. 管理后台 → Agent管理 → 找到目标 Agent
2. 点击 Token 列的 👁 按钮，Token 从 `••••••••` 变为可见
3. 点击 📋 按钮，Token 复制到剪贴板
4. 将 Token 粘贴到目标服务器的 `config.json` 的 `auth_token` 字段

### 改动范围

| 文件 | 改动 |
|------|------|
| `web/src/components/AdminPanel.vue` | Agent表格新增 Token 列 + 显示/隐藏 + 复制 |

**不改**: 密码管理Tab、后端API（已返回token）、其他组件。

### 表格改动

```
Before (7列):  ID | 名称 | IP | 状态 | 模式 | 共享 | 操作
After  (8列):  ID | 名称 | IP | Token | 状态 | 模式 | 共享 | 操作
```

空行 colspan 7→8。

### Token 列行为

- 默认: `••••••••`（遮罩，~80px）
- 👁: 切换显示完整 Token
- 📋: 复制到剪贴板，toast「已复制」
- 显示态: 等宽字体，绿色高亮，自动换行

### 新增代码（约30行）

```typescript
const tokenVisible = ref<Record<string, boolean>>({})

function toggleToken(agentId: string) {
  tokenVisible.value[agentId] = !tokenVisible.value[agentId]
}

async function copyAgentToken(token: string) {
  await navigator.clipboard.writeText(token)
  toast?.success('已复制到剪贴板')
}
```

---

## 二、wragent — 配置驱动 TCP 隧道

### 典型场景

#### 场景1：远程访问内网数据库

**背景**: DBA 需要从笔记本访问生产环境内网的 MySQL（`10.0.3.15:3306`），该服务器没有公网 IP。

**部署**: 2个 agent
- 笔记本部署 wragent（`agent-DBA`），配置隧道
- 内网 MySQL 旁部署 wragent（`agent-B`），正常模式

**配置文件** (`agent-DBA/config.json`):
```json
{
  "server_url": "wss://portal.example.com:5588/api/ws/webrtc",
  "agent_id": "agent-DBA",
  "auth_token": "abc123...",
  "tunnel_local_port": 3306,
  "tunnel_target_addr": "10.0.3.15:3306",
  "tunnel_agent_id": "agent-B"
}
```

**使用**:
```bash
./wragent -config config.json
# 输出: 隧道模式: :3306 → 10.0.3.15:3306 (目标Agent: agent-B)
```

**效果**: DBA 本地 `localhost:3306` 等同于内网 `10.0.3.15:3306`，直接用 MySQL 客户端连接。

#### 场景2：远程桌面 RDP 访问内网 Windows 服务器

**背景**: 运维需要从任意地点 RDP 到内网 Windows 服务器（`192.168.1.50:3389`）。

**部署**: 2个 agent
- 运维笔记本部署 wragent（`agent-ops`），配置隧道
- 内网一台 Linux 服务器部署 wragent（`agent-win-proxy`），能访问 Windows 机器

**配置文件** (`agent-ops/config.json`):
```json
{
  "server_url": "wss://portal.example.com:5588/api/ws/webrtc",
  "agent_id": "agent-ops",
  "auth_token": "def456...",
  "tunnel_local_port": 13389,
  "tunnel_target_addr": "192.168.1.50:3389",
  "tunnel_agent_id": "agent-win-proxy"
}
```

**效果**: 运维用 RDP 客户端连接 `localhost:13389`，即可远程桌面到内网 Windows。

#### 场景3：团队共享隧道入口

**背景**: 团队3人需要访问同一台内网 Redis（`10.0.2.5:6379`）。

**部署**: 内网 Redis 旁部署1个 wragent（`agent-redis`），3人各自笔记本运行 wragent 配隧道。

**3人的配置文件各自**:
```json
{
  "server_url": "wss://portal.example.com:5588/api/ws/webrtc",
  "agent_id": "user-A",
  "auth_token": "...",
  "tunnel_local_port": 6379,
  "tunnel_target_addr": "10.0.2.5:6379",
  "tunnel_agent_id": "agent-redis"
}
```

**效果**: 每人本地 `localhost:6379` 都能连接 Redis，互不干扰（各自独立 WebRTC 连接 + DataChannel）。

#### 场景4：网络抖动自动恢复

**行为**: 隧道建立后网络短暂中断。
1. WebSocket 断开 → `wsClient.reconnect()` 每 5 秒重试
2. 重连成功 → 重新 `connect_tunnel` → 重新创建 Peer + DataChannel
3. 本地 TCP 监听持续运行，不中断
4. 重连期间新到的 TCP 连接等待（DataChannel 未就绪时发送阻塞）
5. DataChannel 恢复后自动恢复转发

### 改动文件

| 文件 | 类型 | 说明 |
|------|------|------|
| `wragent/config/config.go` | 修改 | 新增隧道字段（无 mode） |
| `wragent/main.go` | 修改 | 有隧道字段时启动隧道 goroutine |
| `wragent/webrtc/signal.go` | 修改 | `OnDataChannel` 路由 `"tcp-tunnel"` + bridgeTCPTunnel |
| `wragent/webrtc/tunnel.go` | **新文件** | 隧道入口端逻辑 |
| `wragent/websocket/client.go` | 修改 | 新增 `SendConnectTunnel` |
| `app/api_isolated.py` | 修改 | 新增 `connect_tunnel` 信令处理 |

### 2.1 Config 结构

```go
type Config struct {
    // 通用
    ServerURL           string `json:"server_url"`
    AgentID             string `json:"agent_id"`
    AuthToken           string `json:"auth_token"`
    WSReconnectInterval int    `json:"ws_reconnect_interval"`
    WSHeartbeatInterval int    `json:"ws_heartbeat_interval"`
    ICECooldown         int    `json:"ice_cooldown"`
    // Tunnel（有值就启用，无值不启用）
    TunnelLocalPort  int    `json:"tunnel_local_port"`
    TunnelTargetAddr string `json:"tunnel_target_addr"`
    TunnelAgentID    string `json:"tunnel_agent_id"`
}
```

无 `Mode` 字段。隧道字段为零值时隧道不启用。

### 2.2 配置文件示例

只跑正常模式（现有配置完全兼容）：
```json
{
  "server_url": "wss://portal.example.com:5588/api/ws/webrtc",
  "agent_id": "local-agent",
  "auth_token": "98eada13...",
  "ws_reconnect_interval": 5,
  "ws_heartbeat_interval": 30,
  "ice_cooldown": 10
}
```

正常模式 + 隧道模式同时运行：
```json
{
  "server_url": "wss://portal.example.com:5588/api/ws/webrtc",
  "agent_id": "local-agent",
  "auth_token": "98eada13...",
  "ws_reconnect_interval": 5,
  "ws_heartbeat_interval": 30,
  "ice_cooldown": 10,
  "tunnel_local_port": 13389,
  "tunnel_target_addr": "10.0.0.5:3389",
  "tunnel_agent_id": "remote-agent-01"
}
```

### 2.3 main.go

```go
func main() {
    configPath := flag.String("config", "config.json", "配置文件路径")
    showVersion := flag.Bool("v", false, "显示版本信息")
    flag.Parse()

    if *showVersion { printVersion(); return }

    cfg, err := config.Load(*configPath)
    if err != nil { log.Fatalf("加载配置失败: %v", err) }

    if _, err := os.Stat(*configPath); os.IsNotExist(err) {
        cfg.Save(*configPath)
    }

    log.Printf("wragent %s starting...", version)

    if cfg.AuthToken == "" {
        runSetupMode(cfg, *configPath)
        cfg, _ = config.Load(*configPath)
    }

    // 有隧道配置 → 启动隧道监听 goroutine
    if cfg.TunnelLocalPort > 0 && cfg.TunnelAgentID != "" && cfg.TunnelTargetAddr != "" {
        go runTunnelMode(cfg)
    }

    // 正常模式（永远运行，含自动重连）
    runNormalMode(cfg, *configPath)
}
```

### 2.4 隧道消息协议

```go
const (
    MsgTunnelConnect    byte = 0x30  // Listener→Target: 请求TCP连接
    MsgTunnelConnectOK  byte = 0x33  // Target→Listener: 连接确认
    MsgTunnelData       byte = 0x31  // 双向: 原始TCP数据
    MsgTunnelDisconnect byte = 0x32  // 双向: TCP连接关闭
)
```

**多路复用**: 每个 TCP 连接分配 `connID`（uint16），消息格式：

```
[prefix][connID_hi][connID_lo][payload...]
```

| 前缀 | 方向 | Payload |
|------|------|---------|
| `0x30` | Listener→Target | `[connID][{"host":"10.0.0.5","port":3389}]` |
| `0x33` | Target→Listener | `[connID][{"ok":true}]` 或 `[connID][{"ok":false,"detail":"..."}]` |
| `0x31` | 双向 | `[connID][raw TCP bytes]` |
| `0x32` | 双向 | `[connID]` 或 `[connID][{"reason":"eof"}]` |

### 2.5 Target端路由（`signal.go`）

`handleOffer` 中的 `OnDataChannel` 改动：

```go
peer.OnDataChannel(func(name string, dc *webrtc.DataChannel) {
    log.Printf("收到数据通道: %s, room: %s", name, roomID)
    if name == "ssh-terminal" || name == "data" {
        go h.bridgeSSHToDataChannel(dc, roomID)
        return
    }
    if name == "tcp-tunnel" {
        go h.bridgeTCPTunnel(dc, roomID)
        return
    }
    if h.onReady != nil {
        go h.onReady()
    }
})
```

`bridgeTCPTunnel` 实现（多路复用）：
- 维护 `map[uint16]net.Conn` 管理活跃 TCP 连接
- `0x30` → 解析 connID + 目标地址 → TCP 连接 → 发回 `0x33` 确认 → 启动双向 goroutine
- `0x31` → 查找 connID → 写入 TCP
- `0x32` → 关闭 connID 的 TCP 连接，从 map 删除

### 2.6 Listener端（`wragent/webrtc/tunnel.go`）

```go
func runTunnelMode(cfg *config.Config) {
    defer func() {
        if r := recover(); r != nil {
            log.Printf("[TUNNEL] panic recovered: %v, restarting in 5s...", r)
            time.Sleep(5 * time.Second)
            go runTunnelMode(cfg)
        }
    }()

    log.Printf("隧道模式: :%d → %s (目标Agent: %s)",
        cfg.TunnelLocalPort, cfg.TunnelTargetAddr, cfg.TunnelAgentID)

    wsClient := ws.NewClient(cfg.ServerURL, cfg.AuthToken, cfg.AgentID)
    signalHandler := wrtc.NewSignalHandler(wsClient)

    signalHandler.OnReady(func() {
        log.Println("[TUNNEL] WebRTC隧道通道已建立")
        go startTCPListener(cfg, signalHandler)
    })

    signalHandler.OnClose(func() {
        log.Println("[TUNNEL] WebRTC隧道通道关闭，等待自动重连...")
    })

    signalHandler.OnAuthFailed(func() {
        log.Println("[TUNNEL] Token失效，等待重试...")
    })

    signalHandler.Start()
}
```

### 2.7 服务端改动（`app/api_isolated.py`）

新增 `connect_tunnel` 消息类型（`elif` 分支）：

```python
elif msg_type == "connect_tunnel":
    agent_id = msg.get("agent_id", "")
    target_agent_id = msg.get("target_agent_id", "")
    token = msg.get("token", "")

    db_token = await _get_agent_token_from_db(agent_id)
    if not db_token or db_token != token:
        await ws.send_text(json.dumps({"type": "error", "detail": "认证失败"}))
        continue
    if target_agent_id not in _online_agents:
        await ws.send_text(json.dumps({"type": "error", "detail": "目标Agent不在线"}))
        continue

    role = "browser"
    room_id = f"tunnel_{agent_id}_{target_agent_id}_{int(time.time())}"
    agent_ws = _online_agents[target_agent_id]["ws"]
    _agent_connections[room_id] = {
        "browser_ws": ws,
        "agent_id": target_agent_id,
        "agent_ws": agent_ws,
        "user_id": f"tunnel:{agent_id}",
        "created_at": time.time(),
    }
    await agent_ws.send_text(json.dumps({"type": "browser_connect", "room_id": room_id}))
    await ws.send_text(json.dumps({"type": "connect_success", "room_id": room_id, "agent_id": target_agent_id}))
```

### 2.8 信令流程

```
Listener wragent              信令服务器              Target wragent
      │                            │                        │
      │── connect_tunnel ─────────►│                        │
      │   {agent_id, target_id,    │                        │
      │    token}                  │                        │
      │                            │── browser_connect ────►│
      │◄─ connect_success ────────│                        │
      │                            │                        │
      │   [创建Peer+DataChannel]   │                        │
      │── offer ──────────────────►│── offer ─────────────►│
      │◄─ answer ─────────────────│◄── answer ────────────│
      │◄═══ ICE candidates ═══════│═══════════════════════│
      │                            │                        │
      │═══ DataChannel "tcp-tunnel" ═══════════════════════│
      │                            │                        │
      │── [0x30][ID][conn_req] ───┼───────────────────────►│
      │◄─ [0x33][ID][ok] ─────────┼───────────────────────│
      │◄═ [0x31][ID][data] ═══════╪═══════════════════════│
```

### 2.9 跨平台构建

```bash
GOOS=linux   GOARCH=amd64 go build -o wragent-linux-amd64
GOOS=windows GOARCH=amd64 go build -o wragent-windows-amd64.exe
GOOS=linux   GOARCH=arm64 go build -o wragent-linux-arm64
GOOS=windows GOARCH=arm64 go build -o wragent-windows-arm64.exe
```

### 2.10 使用方式

```bash
# 只跑正常模式（现有用法不变）
./wragent -config config.json

# 正常模式 + 隧道模式（配置里加隧道字段即可）
./wragent -config config.json
```

---

## 三、改动总览

| 改动 | 文件 | 量级 | 影响现有功能 |
|------|------|------|-------------|
| Agent Token 查看/复制 | `AdminPanel.vue` | ~30行 | **无** |
| Config 新增隧道字段 | `config/config.go` | ~6行 | **无**（零值=不启用） |
| main 有隧道就启动 | `main.go` | ~5行 | **无**（goroutine隔离） |
| `OnDataChannel` 路由 tcp-tunnel | `signal.go` | ~3行 | **无**（新增分支） |
| Target端隧道桥接 | `signal.go` | ~80行 | **无**（新函数） |
| Listener端隧道逻辑 | `tunnel.go`（新） | ~200行 | **无**（独立文件） |
| WebSocket 隧道消息 | `client.go` | ~15行 | **无**（新方法） |
| 服务端 connect_tunnel | `api_isolated.py` | ~25行 | **无**（新 elif） |

## 四、安全保障

| 现有功能 | 保护 |
|---------|------|
| SSH/SFTP/VNC | 隧道路由在 SSH 之后、VNC 之后新增分支，不改现有分支 |
| Agent 注册 | 隧道用独立 WebSocket 连接，不共享 |
| 浏览器连接 | 正常模式完全不动 |
| 密码管理 | 不动 |
| 管理后台弹窗 | 无新增弹窗 |
| 自动重连 | 复用现有 reconnect，隧道崩溃有 recover 兜底 |
