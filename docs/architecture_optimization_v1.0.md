# 泡鱼终端架构优化方案 v1.3

> 版本: v1.3 | 日期: 2026-09-14 | 状态: 全部完成

## 版本历史

| 版本 | 日期 | 变更说明 |
|------|------|----------|
| v1.0 | 2026-09-14 | 初版：取消直连模式 + 新增 wrgateway + 网关多用户管理 |
| v1.1 | 2026-09-14 | Phase 1-3 实施完成：后端删除直连代码、wrgateway 子项目创建、网关 CRUD API + 前端 |
| v1.2 | 2026-09-14 | Phase 4 实施完成：WebRTCManager 网关支持、后端 register_gateway/connect_gateway、SshManager 网关下拉框 |
| v1.3 | 2026-09-14 | Phase 5-8 实施完成：网关共享、gateway_id 持久化、wrgateway 缺省部署、local-gateway 自动创建 |

---

## 一、总体目标

| 目标 | 说明 |
|------|------|
| **取消直连模式** | 删除 FastAPI 的 SSH/VNC WebSocket 桥，统一走 Agent |
| **管理面/数据面解耦** | FastAPI 仅做认证+信令，数据流全在 wragent |
| **新增 wrgateway** | 跨服务器部署的接入网元，为防火墙环境提供 WebSocket 接入 |
| **网关多用户管理** | 每个账户维护自己的 wrgateway，支持三种范围分享 (private/all/select) |

---

## 二、整体架构

```
┌─────────────────────────────────────────────────────────────────┐
│                     管理平台 (FastAPI)                           │
│  认证 · CRUD · WebRTC信令 · 网关注册中心                        │
└──────────┬──────────────────┬──────────────────┬────────────────┘
           │                  │                  │
           │ WSS (信令)       │ WSS (信令)       │ WSS (信令)
           │                  │                  │
    ┌──────▼──────┐   ┌──────▼──────┐   ┌──────▼──────┐
    │  wragent-1  │   │  wragent-2  │   │  wrgateway  │
    │  目标网络    │   │  另一网络    │   │  DMZ/公网   │
    │  (Go, 数据面)│   │  (Go, 数据面)│   │  (Go, 网关) │
    └──────┬──────┘   └──────┬──────┘   └──────┬──────┘
           │                  │                  │
           │ WebRTC P2P       │ WebRTC P2P       │ WebRTC P2P
           │                  │                  │ (服务端终结)
    ┌──────▼──────┐   ┌──────▼──────┐   ┌──────▼──────┐
    │   Browser   │   │   Browser   │   │   Browser   │
    │  (正常网络)  │   │  (正常网络)  │   │  (防火墙内)  │
    └─────────────┘   └─────────────┘   └─────────────┘
```

---

## 三、Phase 1: 取消直连模式，统一 Agent

### 3.1 后端删除清单

| 文件 | 行号 | 操作 | 说明 |
|------|------|------|------|
| `app/api_isolated.py` | L1171-1195 | **删除** | `SSHTermSession` 类 (24行) |
| `app/api_isolated.py` | L1197-1339 | **删除** | `/api/ws/ssh` 端点 (143行) |
| `app/api_isolated.py` | L1346-1441 | **删除** | `/api/ws/vnc` 端点 (96行) |
| `app/api_isolated.py` | L1444-1447 | **删除** | `_open_connection_with_timeout` (4行) |
| `app/api_isolated.py` | L1450-1475 | **删除** | `/api/vnc/test` 端点 (19行) |
| `app/api_isolated.py` | L18 | **删除** | `import asyncssh` |
| `app/api_isolated.py` | L81 | **修改** | 移除 `"connection_mode": r.connection_mode` |
| `app/api_isolated.py` | L99 | **修改** | `connection_mode` 默认值改 `"agent"` |
| `app/api.py` | L674-698 | **删除** | 旧 `SSHTermSession` 类 (24行) |
| `app/api.py` | L700-822 | **删除** | 旧 `/api/ws/ssh` 端点 (123行) |
| `app/api.py` | L360-371 | **修改** | 旧 `SshConn` 模型移除 `connection_mode` |
| `app/api.py` | L13 | **删除** | `import asyncssh` |

### 3.2 数据模型修改

| 文件 | 行号 | 操作 | 说明 |
|------|------|------|------|
| `app/database.py` | L69 | **修改** | `connection_mode` 默认值 `"direct"` → `"agent"` |
| `app/database.py` | L465 | **修改** | 迁移代码默认值 `"direct"` → `"agent"` |
| `app/models.py` | L113 | **修改** | `connection_mode: str = "direct"` → `"agent"` |
| `app/database.py` | init_db() | **新增** | 迁移: `UPDATE ssh_connections SET connection_mode='agent' WHERE connection_mode='direct'` |
| `app/database.py` | init_db() | **新增** | 迁移: 自动创建本地默认 Agent (`local-001`) |
| `app/database.py` | init_db() | **新增** | 迁移: 将 `agent_id=''` 的连接绑定到 `local-001` |

### 3.3 前端删除清单

| 文件 | 行号 | 操作 | 说明 |
|------|------|------|------|
| `web/src/api.ts` | L260-265 | **删除** | `api.wsUrl()` 函数 |
| `web/src/api.ts` | L266-271 | **删除** | `api.vncUrl()` 函数 |
| `web/src/api.ts` | L272-273 | **删除** | `api.vncTest()` 函数 |
| `web/src/api.ts` | L40 | **修改** | `connection_mode?: 'direct' \| 'agent'` → 仅保留 `'agent'` |
| `web/src/components/SshManager.vue` | L161-181 | **删除** | `connectWebSocket()` 函数 (21行) |
| `web/src/components/SshManager.vue` | L97-158 | **修改** | `connectSshWithMode()` / `connectFileWithMode()` 移除 direct 分支 |
| `web/src/components/SshManager.vue` | L134-158 | **修改** | `createTerm()` 移除 `connectWebSocket` 调用 |
| `web/src/components/SshManager.vue` | L30, L258 | **修改** | form 默认值 `connection_mode: 'direct'` → `'agent'` |
| `web/src/components/SshManager.vue` | L90, L94, L227, L263, L332 | **修改** | 移除 `\|\| 'direct'` 回退 |
| `web/src/components/SshManager.vue` | L321, L409, L430 | **修改** | 移除 "直连" 文案 |
| `web/src/components/SshManager.vue` | L552-553 | **删除** | "直连" 按钮 |
| `web/src/components/SshManager.vue` | L631-633 | **删除** | `.ssh-mode-tag.direct` CSS |
| `web/src/components/SshFileBrowser.vue` | L88, L142, L160, L208, L229, L245 | **修改** | 移除 `if (connection_mode === 'agent')` 分支，全部走 Agent |
| `web/src/components/SshFileBrowser.vue` | L274 | **删除** | `isAgent` computed |
| `web/src/components/SshFileBrowser.vue` | L281, L303, L319, L333 | **修改** | 移除 isAgent 判断 |
| `web/src/components/VncViewer.vue` | L24-35 | **重写** | `getWsUrl()` 改为通过 Agent DataChannel 连接 |

### 3.4 测试清理

| 文件 | 操作 | 行数 |
|------|------|------|
| `app/tests/test_connection_mode_direct.py` | **删除** | 227行 |
| `app/tests/test_connection_mode_agent.py` | **删除** | 233行 |
| `app/tests/test_connection_mode_lifecycle.py` | **删除** | 241行 |
| `app/tests/test_connection_mode_sftp.py` | **删除** | 176行 |
| `app/tests/test_ssh_connection_mode.py` | **删除** | 283行 |
| `app/tests/test_integration_connection_mode.py` | **删除** | 84行 |
| `app/tests/test_api_isolated.py` | **删除** ws/ssh 引用 | ~10行 |
| `app/tests/test_ssh_sftp_api.py` | **删除** ws/ssh 测试 | ~130行 |
| `app/tests/test_integration_real_ssh.py` | **删除** ws/ssh 测试 | ~20行 |
| `app/tests/test_integration_vnc.py` | **删除** ws/vnc 测试 | ~70行 |
| `app/tests/sync_db.py` | **修改** 默认值 | 2行 |
| `web/src/__tests__/ssh-mode.test.ts` | **删除** | 87行 |
| `autotest/tests/ssh-direct/` | **删除** 整目录 | 136行 |
| `autotest/tests/connection-mode-baseline.spec.ts` | **删除** | 336行 |
| `autotest/tests/ssh-connection-mode.spec.ts` | **删除** | 302行 |
| `autotest/tests/ssh-sftp-e2e.spec.ts` | **修改** 移除 direct | ~20行 |
| `autotest/tests/helpers.ts` | **修改** 移除 direct | ~5行 |
| **合计** | | **~1,962行删除 + ~60行修改** |

### 3.5 Phase 1 影响量化

| 指标 | 改动前 | 改动后 | 变化 |
|------|--------|--------|------|
| 后端代码 (api_isolated.py) | 1965行 | ~1680行 | **-285行** |
| 后端代码 (api.py) | 1195行 | ~1035行 | **-160行** |
| 前端代码 (SshManager.vue) | 790行 | ~700行 | **-90行** |
| 测试文件 | 6个 connection_mode 测试 | 0个 | **-6文件 (-1,244行)** |
| autotest 文件 | 4个 direct 测试 | 0个 | **-4文件 (-774行)** |
| WebSocket 端点 | 3个 | 1个 (ws/webrtc) | **-2端点** |
| 每 SSH 会话服务端内存 | ~2-5MB (Python 协程) | ~0 (P2P DataChannel) | **接近 0** |

---

## 四、Phase 2: wrgateway 独立子项目

### 4.1 架构定位

```
正常路径:
  Browser ←──── WebRTC P2P DataChannel ────→ wragent → Target

防火墙路径:
  Browser ←─WSS─→ wrgateway ←─WebRTC DC─→ wragent → Target
             :443    (Go)     (服务端)       (Go)
```

wrgateway 的角色：**服务端 WebRTC 终结点**，为防火墙内的浏览器提供 WebSocket 接入，将 WebSocket 帧透明桥接到与 wragent 的 WebRTC DataChannel 上。

### 4.2 连接流程

```
Browser                wrgateway              管理服务器              wragent
  │                       │                      │                      │
  │ 1. WSS 连接            │                      │                      │
  │ (token=jwt)           │                      │                      │
  │──────────────────────►│                      │                      │
  │                       │ 2. 转发认证           │                      │
  │                       │ connect_agent         │                      │
  │                       │─────────────────────►│                      │
  │                       │                      │ 3. browser_connect    │
  │                       │                      │─────────────────────►│
  │                       │ 4. connect_success    │                      │
  │                       │◄─────────────────────│                      │
  │                       │                      │ 5. WebRTC Offer      │
  │                       │                      │◄─────────────────────│
  │                       │ 6. 转发 Offer         │                      │
  │                       │◄─────────────────────│                      │
  │                       │ 7. 创建 Answer        │                      │
  │                       │    建立 PeerConnection│                      │
  │                       │─────────────────────►│                      │
  │                       │ 8. 转发 Answer        │                      │
  │                       │                      │─────────────────────►│
  │                       │ 9. ICE Candidates    │                      │
  │                       │◄────────────────────►│◄────────────────────►│
  │                       │                      │                      │
  │                       │ 10. DataChannel 建立  │                      │
  │                       │◄────────────────────────────────────────────►│
  │                       │                      │                      │
  │ 11. SSH 连接请求       │ 12. 转发到 DC         │ 13. SSH 连接         │
  │ {host,port,auth}      │ {0x01 + JSON}        │ gossh.Dial()        │
  │──────────────────────►│─────────────────────►│────────────────────►│
  │                       │                      │                      │
  │ 14. 终端数据           │ 15. 透传              │ 16. PTY 数据         │
  │ {0x00 + bytes}        │ {0x00 + bytes}       │ {0x00 + bytes}      │
  │◄─────────────────────►│◄────────────────────►│◄────────────────────►│
```

### 4.3 目录结构

```
wrgateway/
├── go.mod                      # 独立 Go module: github.com/ppy-tools/wrgateway
├── go.sum
├── main.go                     # 入口: 加载配置，启动 WSS + 信令客户端
├── config/
│   ├── config.go               # 配置结构体
│   └── config.json             # 缺省配置
├── server/
│   ├── wss.go                  # WSS 服务器 (接受浏览器连接)
│   ├── handler.go              # WebSocket 消息路由
│   └── session.go              # 浏览器会话管理
├── signaling/
│   ├── client.go               # 管理平台信令客户端
│   └── messages.go             # 信令消息类型定义
├── relay/
│   ├── webrtc.go               # WebRTC PeerConnection (pion/webrtc)
│   └── bridge.go               # WebSocket ↔ DataChannel 双向桥接
├── auth/
│   └── jwt.go                  # JWT 验证
├── Dockerfile
├── docker-compose.yaml
└── README.md
```

### 4.4 核心模块设计

#### WSS 服务器 (`server/wss.go`)

```go
type WSServer struct {
    config  *config.Config
    handler *Handler
}

func (s *WSServer) Start() error {
    mux := http.NewServeMux()
    mux.HandleFunc("/ws", s.handleWebSocket)
    mux.HandleFunc("/health", s.handleHealth)
    // ...
}
```

#### 信令客户端 (`signaling/client.go`)

```go
type SignalingClient struct {
    serverURL string    // wss://管理平台:5588/api/ws/webrtc
    token     string    // wrgateway 注册 token
    agentID   string    // 目标 agent
    roomID    string    // 房间 ID
    ws        *websocket.Conn

    onOffer      func(sdp webrtc.SessionDescription)
    onCandidate  func(candidate webrtc.ICECandidateInit)
}
```

#### WebRTC 中继 (`relay/webrtc.go`)

```go
type WebRTCRelay struct {
    peerConn   *webrtc.PeerConnection
    dataChan   io.ReadWriteCloser  // detached DataChannel
}

func (r *WebRTCRelay) CreatePeer(iceServers []webrtc.ICEServer) error {
    var s webrtc.SettingEngine
    s.DetachDataChannels()
    api := webrtc.NewAPI(webrtc.WithSettingEngine(s))
    // 创建 PeerConnection, 等待 DataChannel...
}
```

#### 桥接器 (`relay/bridge.go`)

```go
type Bridge struct {
    wsConn   *websocket.Conn   // 浏览器 WebSocket
    dcReader io.ReadCloser     // DataChannel reader (detached)
    dcWriter io.WriteCloser    // DataChannel writer (detached)
}

func (b *Bridge) Start() {
    go b.wsToDC()   // WebSocket → DataChannel (二进制透传)
    go b.dcToWS()   // DataChannel → WebSocket (二进制透传)
}
```

### 4.5 独立性保证

| 维度 | 设计 |
|------|------|
| **Go module** | `github.com/ppy-tools/wrgateway`，独立 `go.mod`，不依赖 wragent |
| **构建** | 独立 `Dockerfile`，独立镜像 `pyterm/wrgateway:latest` |
| **部署** | 独立服务器，独立端口 (443) |
| **配置** | 独立 `config.json`，不共享 wragent 配置 |
| **代码** | 零耦合：不 import wragent 的任何包，仅通过 WebSocket/信令协议交互 |
| **测试** | 独立测试套件，可独立运行 |

### 4.6 配置设计

```json
{
  "listen": ":443",
  "tls_cert": "/etc/wrgateway/ssl/cert.pem",
  "tls_key": "/etc/wrgateway/ssl/key.pem",
  "server_url": "wss://管理平台IP:5588/api/ws/webrtc",
  "gateway_id": "gw-20260914-001",
  "gateway_token": "64位hex注册token",
  "jwt_secret": "与管理平台共享的JWT密钥",
  "log_level": "info",
  "log_file": "",
  "heartbeat_interval": 30,
  "read_timeout": 60,
  "write_timeout": 60
}
```

### 4.7 Dockerfile

```dockerfile
FROM golang:1.24-bookworm AS builder
WORKDIR /src
COPY go.mod go.sum ./
ENV GOPROXY=https://goproxy.cn,direct
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /wrgateway .

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*
COPY --from=builder /wrgateway /usr/local/bin/wrgateway
ENTRYPOINT ["wrgateway"]
```

### 4.8 安全设计

| 维度 | 措施 |
|------|------|
| **认证** | 浏览器连接时必须携带 JWT token，wrgateway 验证后才转发 |
| **TLS** | 支持 TLS 证书配置，企业环境可使用内部 CA 签发的证书 |
| **代理兼容** | 支持 `X-Forwarded-For` / `X-Real-IP` 头，适配 Nginx/HAProxy |
| **速率限制** | 可配置每 IP 最大连接数 |
| **审计日志** | 记录所有连接事件 |
| **凭据隔离** | wrgateway 不存储密码，密码仅在 DataChannel 中端到端传输 |

### 4.9 性能目标

| 指标 | 目标 |
|------|------|
| 并发连接 | 单实例 500+ |
| 内存占用 | 每连接 ~200KB (Go goroutine + pion buffer) |
| 延迟 | 浏览器↔wrgateway <1ms，wrgateway↔wragent <5ms |
| CPU | 主要在帧拷贝，极轻量 |

---

## 五、Phase 3: 管理平台网关多用户管理

### 5.1 设计原则

完全复用 Agent/Coturn 的多用户所有权 + 三种范围分享模式：
- **private**: 仅自己可查看和使用
- **all**: 所有用户都可以查看和使用
- **select**: 指定用户 (JSON 数组存储)
- 共享用户只能查看和使用，不能编辑/删除
- 所有修改操作强制 ownership check

### 5.2 数据模型

#### 新增 `gateways` 表

```sql
CREATE TABLE gateways (
    id          VARCHAR(64) PRIMARY KEY,    -- gw-20260914-001
    name        VARCHAR(255) NOT NULL,      -- "DMZ 网关"
    url         VARCHAR(512) NOT NULL,      -- wss://gateway.corp.com:443
    token       VARCHAR(255) UNIQUE,        -- wrgateway 注册 token (64位hex)
    remark      TEXT DEFAULT '',
    is_active   BOOLEAN DEFAULT TRUE,
    created_at  VARCHAR(64) NOT NULL,
    owner_id    VARCHAR(64) DEFAULT '',     -- 创建者用户 ID
    shared_with VARCHAR(512) DEFAULT 'private'  -- private / all / ["usr_xxx"]
);
```

#### `ssh_connections` 表新增字段

```sql
ALTER TABLE ssh_connections ADD COLUMN gateway_id VARCHAR(64) DEFAULT '';
-- gateway_id 为空: 浏览器直连管理平台 WebRTC
-- gateway_id 有值: 浏览器通过指定网关接入
```

#### 运行时状态 (内存)

```python
_online_gateways: dict[str, dict] = {}  # gateway_id -> {ws, name, url, last_seen}
```

### 5.3 数据库迁移 (`init_db()`)

```python
# 添加 gateway_id 到 ssh_connections
try:
    await conn.execute(text("ALTER TABLE ssh_connections ADD COLUMN gateway_id VARCHAR(64) DEFAULT ''"))
except Exception:
    pass

# 创建 gateways 表 (如果不存在)
from sqlalchemy import Column, String, Text, Boolean
class Gateway(Base):
    __tablename__ = "gateways"
    id = Column(String(64), primary_key=True)
    name = Column(String(255), nullable=False)
    url = Column(String(512), nullable=False)
    token = Column(String(255), unique=True)
    remark = Column(Text, default="")
    is_active = Column(Boolean, default=True)
    created_at = Column(String(64), nullable=False)
    owner_id = Column(String(64), default="")
    shared_with = Column(String(512), default="private")
```

### 5.4 后端 API (复用 Agent 模式)

| 端点 | 方法 | 说明 |
|------|------|------|
| `/api/admin/gateway-id/next` | GET | 自动生成下一个 ID (gw-YYYYMMDD-XX) |
| `/api/admin/gateways` | GET | 列出网关 (owner + shared_with 可见性) |
| `/api/admin/gateways` | POST | 创建网关 (自动生成 token, owner_id = 当前用户) |
| `/api/admin/gateways/{id}` | PUT | 更新网关 (owner only) |
| `/api/admin/gateways/{id}` | DELETE | 删除网关 (owner only) |
| `/api/admin/gateways/{id}/token` | POST | 重新生成 token (owner only) |
| `/api/admin/gateways/{id}/status` | POST | 启用/禁用 (owner only) |
| `/api/admin/gateways/{id}/share` | POST | 设置共享范围 (owner only) |
| `/api/admin/gateways/{id}/shares` | GET | 获取共享详情 (owner only) |

#### 列出网关 (复用 Agent 可见性查询)

```python
@admin_router.get("/gateways")
async def admin_list_gateways(user: dict = Depends(require_permission("agent:manage")), db: AsyncSession = Depends(get_db)):
    uid = user["id"]
    result = await db.execute(select(Gateway).where(
        (Gateway.owner_id == uid) |
        (Gateway.shared_with == "all") |
        (Gateway.shared_with.contains(f'"{uid}"'))
    ).order_by(Gateway.created_at.desc()))
    gateways = result.scalars().all()
    # 获取用户名 + 在线状态
    from .api_isolated import _online_gateways
    # ... 返回列表，含 is_owner, owner_name, online
```

#### 创建网关

```python
@admin_router.post("/gateways")
async def admin_add_gateway(req: GatewayAddReq, user: dict = Depends(require_permission("agent:manage")), db: AsyncSession = Depends(get_db)):
    token = secrets.token_hex(32)
    db.add(Gateway(
        id=req.id, name=req.name, url=req.url, token=token,
        remark=req.remark, is_active=True,
        created_at=datetime.now(timezone.utc).isoformat(),
        owner_id=user["id"],
    ))
    await db.commit()
```

#### 分享网关 (复用 Agent 分享模式)

```python
@admin_router.post("/gateways/{gateway_id}/share")
async def admin_share_gateway(gateway_id: str, user: dict = Depends(require_permission("agent:manage")), db: AsyncSession = Depends(get_db), body: dict = None):
    gateway = await db.execute(select(Gateway).where(Gateway.id == gateway_id))
    gw = gateway.scalar_one_or_none()
    if not gw:
        raise HTTPException(status_code=404, detail="Gateway not found")
    if gw.owner_id != user["id"]:
        raise HTTPException(status_code=403, detail="无权操作他人的网关")
    body = body or {}
    shared_with = body.get("shared_with", "private")
    if isinstance(shared_with, list):
        gw.shared_with = json.dumps(shared_with)
    else:
        gw.shared_with = str(shared_with)
    await db.commit()
```

### 5.5 信令协议扩展

wrgateway 注册消息：
```json
{
  "type": "register_gateway",
  "gateway_id": "gw-20260914-001",
  "token": "64位hex"
}
```

管理平台响应：
```json
{
  "type": "register_success",
  "gateway_id": "gw-20260914-001"
}
```

### 5.6 连接响应扩展

当浏览器请求连接时，管理平台根据 `gateway_id` 返回网关信息：

```json
{
  "type": "connect_success",
  "room_id": "room_xxx",
  "agent_id": "wragent-xxx",
  "gateway": {
    "url": "wss://gateway.corp.com:443",
    "token": "用于浏览器认证的临时token"
  }
}
```

如果 `gateway_id` 为空，`gateway` 字段为 `null`，浏览器直接走 WebRTC P2P。

### 5.7 前端改动

#### 新增 `GatewayManager.vue`

管理网关的 CRUD 界面，复用 AgentManager.vue 布局：
- 网关列表表格 (状态、名称、URL、备注、在线状态、来源、操作)
- 添加/编辑/删除/Token 管理弹窗
- 共享按钮 → 复用 AdminPanel.vue 的共享弹窗模式

#### 修改 `AdminPanel.vue`

在共享管理中增加 `gateway` 类型支持：

```javascript
// openShareModal 扩展
async function openShareModal(item: any, type: 'agent' | 'coturn' | 'gateway') {
  // 复用现有逻辑，type 为 'gateway' 时调用 gateway API
}

// saveShare 扩展
async function saveShare() {
  if (shareType.value === 'gateway') {
    await api.adminShareGateway(shareTarget.value.id, sharedWith)
  }
  // ...
}
```

#### 修改 `SshManager.vue`

连接表单新增网关选择：

```html
<div class="form-field">
  <label>接入网关</label>
  <select v-model="form.gateway_id">
    <option value="">直连 (不使用网关)</option>
    <option v-for="g in gateways" :key="g.id" :value="g.id">
      {{ g.name }} ({{ g.url }})
    </option>
  </select>
</div>
```

#### 修改 `WebRTCManager`

```typescript
async connect(agentId: string, gateway?: {url: string, token: string}) {
  if (gateway) {
    this.connectViaGateway(agentId, gateway)
  } else {
    this.connectSignal(agentId)
  }
}

private connectViaGateway(agentId: string, gateway: {url: string, token: string}) {
  const ws = new WebSocket(`${gateway.url}/ws?token=${gateway.token}`)
  // 通过网关中转信令和数据
}
```

---

## 六、部署架构

```
服务器 A (管理平台):          服务器 B (wrgateway):         服务器 C (wragent):
  ├── md (FastAPI)              ├── wrgateway (Go)           ├── wragent (Go)
  ├── nginx                     │   监听 :443 (WSS)          │   host network
  ├── mariadb                   │   连接服务器A信令            │   连接服务器A信令
  ├── coturn                    │                            │
  └── minio                     │                            │
                                │                            │
  IP: 10.0.1.100               IP: 203.0.113.10            IP: 目标网络
  (内网)                        (公网/DMZ)                  (内网)
```

---

## 七、执行计划

| 阶段 | 内容 | 工作量 | 产出 |
|------|------|--------|------|
| **Phase 1** | 取消直连模式，统一 Agent | 5天 | 后端 -445行, 前端 -90行, 测试 -1,962行 |
| **Phase 2** | wrgateway 独立子项目 | 8天 | 新增 ~1,650行 Go 代码 |
| **Phase 3** | 管理平台网关多用户管理 | 5天 | 后端 CRUD + 前端 UI + 信令扩展 |
| **Phase 4** | 集成测试 + E2E 验证 | 2天 | 完整流程验证 |
| **总计** | | **~20天** | 净减少 ~750行, 新增 ~2,200行 |

---

## 八、风险与缓解

| 风险 | 影响 | 缓解 |
|------|------|------|
| wrgateway 与 wragent 信令协议不兼容 | 高 | 复用现有消息格式，保持协议一致 |
| 防火墙环境 WebSocket 也受限 | 中 | 支持 HTTP/HTTPS 代理配置 |
| wrgateway 重启丢失房间 | 低 | 房间是临时的，双方自动重连 |
| 现有测试覆盖不足 | 中 | Phase 4 补充集成测试 |

---

## 九、实施完成总结

### Phase 1: 取消直连模式 ✅
- 后端: 删除 SSHTermSession + `/api/ws/ssh` + `/api/ws/vnc` + `/api/vnc/test` (~450行)
- 数据模型: `connection_mode` 默认值 → `"agent"` + 迁移逻辑
- 前端: 删除 `wsUrl()`/`vncUrl()`/`vncTest()`, 移除直连按钮
- 测试: 删除 6 个 connection_mode 测试文件 + 2 个 autotest 文件

### Phase 2: wrgateway 独立子项目 ✅
- Go module: `github.com/ppy-tools/wrgateway`
- 模块: config, auth, signaling, relay, server
- Dockerfile: 多阶段构建 (golang:1.23 → debian:bookworm-slim)

### Phase 3: 管理平台网关 CRUD API ✅
- 9 个 Gateway 端点 (CRUD + token + status + share + shares)
- 前端: GatewayManager.vue + api.ts + AdminPanel 导航

### Phase 4: WebRTC 网关支持 ✅
- 前端: WebRTCManager 支持 gatewayUrl 参数
- 后端: register_gateway + connect_gateway + list_gateways 消息类型
- 测试: 新增 gateway 注册测试

### Phase 5: 网关共享 + 持久化 ✅
- AdminPanel 共享支持 gateway 类型
- GatewayManager 共享按钮
- models.py SshConn 新增 gateway_id

### Phase 6: 连接流程 gateway_id 传递 ✅
- SshManager resolveGatewayUrl() 辅助函数
- WebRTCManager.connect(agentId, gatewayUrl) 支持

### Phase 7: wrgateway 缺省部署 ✅
- docker-compose.yaml 添加 wrgateway 服务
- database.py init_db() 自动创建 local-gateway
- wrgateway/config.json 默认配置

### Phase 8: 代码审查 + 验证 ✅
- Python 语法: 全部通过
- 前端 Vite 构建: 成功
- Go 代码: 完整 (编译需要 Go 1.23+)

### 部署架构
```
docker-compose up -d
├── mariadb          # 数据库
├── md               # FastAPI 后端 (端口 5588)
├── wragent          # 本地 Agent (SSH/SFTP 桥接)
├── wrgateway        # 本地网关 (远程浏览器 WebRTC 桥接)
├── coturn           # TURN 服务器
├── nginx            # 反向代理
└── test-ssh-server  # 测试 SSH 服务器
```
