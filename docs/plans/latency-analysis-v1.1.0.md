# 延迟分析系统设计方案

| 字段 | 值 |
|------|-----|
| 版本 | v1.1.0 |
| 状态 | 草案 |
| 日期 | 2026-09-17 |
| 作者 | opencode |
| 批准 | 待用户确认 |

---

## 版本历史

| 版本 | 日期 | 变更 |
|------|------|------|
| v1.0.0 | 2026-09-17 | 初版：完整的延迟分析系统设计（管理员专属） |
| v1.1.0 | 2026-09-17 | 所有用户功能 + 自身影响分析 + 数据聚合策略 |

---

## 一、目标

对 pyterm 的 SSH / SFTP / VNC 三种功能，在四种连接模式下，建立端到端的延迟度量体系，帮助定位瓶颈、量化优化效果。**每个用户可以查看自己的延迟分析数据，管理员额外可查看全局概览。**

---

## 二、连接模式定义

| 模式代码 | 含义 | 数据通路 |
|----------|------|----------|
| `p2p_local` | P2P直连本地Agent | Browser ↔ WebRTC DC ↔ Local Agent |
| `p2p_remote` | P2P直连远程Agent | Browser ↔ WebRTC DC ↔ Remote Agent |
| `gw_local` | 网关→本地Agent | Browser ↔ WS ↔ Gateway ↔ DC ↔ Local Agent |
| `gw_remote` | 网关→远程Agent | Browser ↔ WS ↔ Gateway ↔ DC ↔ Remote Agent |

---

## 三、延迟打点标准化

### 3.1 SSH 连接延迟阶段

| 阶段ID | 名称 | 含义 | 采集端 |
|--------|------|------|--------|
| `t0` | 用户点击 | 用户点击SSH连接按钮 | Browser |
| `t1` | WS连接完成 | 信令WebSocket握手完成 | Browser |
| `t2` | 信令请求发出 | connect_agent/connect_gateway 发出 | Browser |
| `t3` | 信令响应到达 | 收到 connect_success 或 datachannel_ready | Browser |
| `t4` | 通道就绪 | DataChannel open（P2P）或 WS桥接就绪（网关） | Browser |
| `t5` | SSH指令发出 | MSG_SSH_CONNECT 通过 DC/WS 发出 | Browser |
| `t6` | Agent收到指令 | Agent 收到 ssh_connect 消息 | Agent |
| `t7` | TCP拨号完成 | gossh.Dial() 完成（含TCP握手+SSH握手+认证） | Agent |
| `t8` | PTY Shell就绪 | session.Shell() 执行完成 | Agent |
| `t9` | 首字节到达 | 浏览器收到第一条 terminal_data | Browser |

**总延迟 = t9 - t0**

### 3.2 SFTP 首次交互延迟阶段

| 阶段ID | 名称 | 含义 | 采集端 |
|--------|------|------|--------|
| `t0` | 用户点击 | 用户点击文件管理按钮 | Browser |
| `t1` | 通道就绪 | SSH通道已建立（复用）或新WebRTC通道建立 | Browser |
| `t2` | LIST请求发出 | MSG_SFTP_REQUEST(list) 发出 | Browser |
| `t3` | Agent收到请求 | Agent 收到 sftp_request 消息 | Agent |
| `t4` | SFTP客户端创建 | sftp.NewClient() 完成 | Agent |
| `t5` | 远端IO完成 | ReadDir 完成 | Agent |
| `t6` | 首字节响应到达 | 浏览器收到第一条 sftp_response | Browser |

**总延迟 = t6 - t0**

### 3.3 VNC 首帧延迟阶段

| 阶段ID | 名称 | 含义 | 采集端 |
|--------|------|------|--------|
| `t0` | 用户点击 | 用户点击VNC连接按钮 | Browser |
| `t1` | 通道就绪 | DataChannel open | Browser |
| `t2` | VNC指令发出 | MSG_VNC_CONNECT 发出 | Browser |
| `t3` | Agent收到指令 | Agent 收到 vnc_connect 消息 | Agent |
| `t4` | TCP连接完成 | Agent TCP拨号到VNC Server完成 | Agent |
| `t5` | RFB握手完成 | VNC协议握手（版本+认证+像素格式协商） | Agent |
| `t6` | 首帧数据到达 | 浏览器收到第一条 MSG_VNC_DATA | Browser |
| `t7` | 首帧渲染完成 | noVNC canvas首帧渲染完成 | Browser |

**总延迟 = t7 - t0**

---

## 四、阶段耗时映射

### SSH
| 阶段耗时 | 计算 | 含义 |
|----------|------|------|
| `stage_01` | t1 - t0 | 前端准备（加载xterm、创建tab） |
| `stage_12` | t2 - t1 | 信令WebSocket握手 |
| `stage_23` | t3 - t2 | 信令往返（connect → success） |
| `stage_34` | t4 - t3 | WebRTC通道建立（ICE+DC） |
| `stage_45` | t5 - t4 | 指令序列化+发送 |
| `stage_56` | t6 - t5 | DC/WS传输到Agent |
| `stage_67` | t7 - t6 | Agent处理（SSH拨号+认证） |
| `stage_78` | t8 - t7 | PTY+Shell初始化 |
| `stage_89` | t9 - t8 | 首字节回传到浏览器 |

### SFTP
| 阶段耗时 | 计算 | 含义 |
|----------|------|------|
| `stage_01` | t1 - t0 | 前端准备 |
| `stage_12` | t2 - t1 | 通道就绪等待 |
| `stage_23` | t3 - t2 | DC/WS传输到Agent |
| `stage_34` | t4 - t3 | SFTP客户端创建 |
| `stage_45` | t5 - t4 | 远端文件系统IO |
| `stage_56` | t6 - t5 | 首字节响应回传 |

### VNC
| 阶段耗时 | 计算 | 含义 |
|----------|------|------|
| `stage_01` | t1 - t0 | 前端准备 |
| `stage_12` | t2 - t1 | 通道就绪等待 |
| `stage_23` | t3 - t2 | DC/WS传输到Agent |
| `stage_34` | t4 - t3 | Agent TCP拨号到VNC Server |
| `stage_45` | t5 - t4 | VNC RFB协议握手 |
| `stage_56` | t6 - t5 | 首帧数据回传到浏览器 |
| `stage_67` | t7 - t6 | noVNC解码+Canvas渲染 |

---

## 五、数据采集架构

```
┌──────────┐     ┌──────────┐     ┌──────────┐
│  Browser  │     │ Gateway  │     │  Agent   │
│  (t0-t5)  │     │ (bridge) │     │ (t6-t8)  │
│           │     │          │     │          │
│ WebRTC    │     │ handler  │     │ signal   │
│ Manager   │     │ .go      │     │ .go      │
│ 插桩点    │     │ 插桩点   │     │ 插桩点   │
└─────┬─────┘     └────┬─────┘     └────┬─────┘
      │                │                │
      │  HTTP POST     │   HTTP POST    │
      │  /api/latency  │   /api/latency │
      │  /report       │   /report      │
      └───────────────►└───────────────►│
                  │
                  ▼
           ┌────────────┐
           │  Backend   │
           │  FastAPI   │
           │  写入 DB   │
           └────────────┘
```

### 采集方式

1. **Browser端**：`WebRTCManager` 在每个阶段记录 `performance.now()` 时间戳，连接完成后（成功或失败）通过 `POST /api/latency/report` 上报
2. **Agent端**：`signal.go` 在关键函数入口/出口记录 `time.Now()`，连接完成后通过HTTP回调上报到服务器
3. **Gateway端**：`handler.go` 记录桥接操作时间戳，通过信令服务器转发上报

### 上报数据结构

```json
{
  "feature": "ssh",
  "conn_mode": "p2p_local",
  "conn_type": "P2P",
  "agent_id": "local-agent",
  "gateway_id": "",
  "room_id": "room_xxx",
  "host": "192.0.2.50",
  "port": 22,
  "success": true,
  "stages": {
    "t0": 1726543210000,
    "t1": 1726543210050,
    "t2": 1726543210060,
    "t3": 1726543210200,
    "t4": 1726543210250,
    "t5": 1726543210260,
    "t6": 1726543210280,
    "t7": 1726543210500,
    "t8": 1726543210550,
    "t9": 1726543210600
  },
  "error": ""
}
```

---

## 六、数据库设计

### 6.1 原始记录表 `latency_records`（保留7天）

```sql
CREATE TABLE latency_records (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id     INTEGER NOT NULL,
    username    VARCHAR(64) NOT NULL,
    feature     VARCHAR(16) NOT NULL,       -- ssh / sftp / vnc
    conn_mode   VARCHAR(16) NOT NULL,       -- p2p_local / p2p_remote / gw_local / gw_remote
    conn_type   VARCHAR(10),                -- P2P / relay
    agent_id    VARCHAR(100),
    gateway_id  VARCHAR(100),
    room_id     VARCHAR(100),
    target_host VARCHAR(255),
    target_port INTEGER,
    success     BOOLEAN DEFAULT 1,
    error_msg   TEXT,
    -- 原始时间戳 (ms since epoch)
    ts_t0       BIGINT,
    ts_t1       BIGINT,
    ts_t2       BIGINT,
    ts_t3       BIGINT,
    ts_t4       BIGINT,
    ts_t5       BIGINT,
    ts_t6       BIGINT,
    ts_t7       BIGINT,
    ts_t8       BIGINT,
    ts_t9       BIGINT,
    -- 预计算的阶段耗时 (ms)
    stage_01_ms INTEGER,  -- t0→t1: 前端准备
    stage_12_ms INTEGER,  -- t1→t2: WS连接 / 通道就绪等待
    stage_23_ms INTEGER,  -- t2→t3: 信令往返 / DC传输到Agent
    stage_34_ms INTEGER,  -- t3→t4: 通道建立 / Agent处理
    stage_45_ms INTEGER,  -- t4→t5: 指令发送 / Agent后续处理
    stage_56_ms INTEGER,  -- t5→t6: Agent处理 / 回传
    stage_67_ms INTEGER,  -- t6→t7: 回传 / 渲染
    stage_78_ms INTEGER,  -- t7→t8: VNC特有
    stage_89_ms INTEGER,  -- t8→t9: VNC特有
    total_ms    INTEGER,  -- 总延迟
    -- 元信息
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- 仅3个索引，写入友好
CREATE INDEX idx_latency_user_created ON latency_records(user_id, created_at);
CREATE INDEX idx_latency_feature_created ON latency_records(feature, created_at);
CREATE INDEX idx_latency_created ON latency_records(created_at);
```

### 6.2 每日聚合表 `latency_daily`（保留90天）

```sql
CREATE TABLE latency_daily (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id     INTEGER NOT NULL,
    feature     VARCHAR(16) NOT NULL,
    conn_mode   VARCHAR(16) NOT NULL,
    conn_type   VARCHAR(10) NOT NULL,
    day         DATE NOT NULL,
    count       INTEGER NOT NULL DEFAULT 0,
    success_count INTEGER NOT NULL DEFAULT 0,
    avg_ms      INTEGER,
    min_ms      INTEGER,
    max_ms      INTEGER,
    p50_ms      INTEGER,
    p95_ms      INTEGER,
    -- 各阶段平均耗时
    avg_stage_01 INTEGER,
    avg_stage_12 INTEGER,
    avg_stage_23 INTEGER,
    avg_stage_34 INTEGER,
    avg_stage_45 INTEGER,
    avg_stage_56 INTEGER,
    avg_stage_67 INTEGER,
    avg_stage_78 INTEGER,
    avg_stage_89 INTEGER,
    UNIQUE INDEX idx_latency_daily_key (user_id, feature, conn_mode, conn_type, day)
);
```

### 6.3 每月聚合表 `latency_monthly`（永久保留）

```sql
CREATE TABLE latency_monthly (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id     INTEGER NOT NULL,
    feature     VARCHAR(16) NOT NULL,
    conn_mode   VARCHAR(16) NOT NULL,
    conn_type   VARCHAR(10) NOT NULL,
    year_month  VARCHAR(7) NOT NULL,       -- '2026-09'
    count       INTEGER NOT NULL DEFAULT 0,
    success_count INTEGER NOT NULL DEFAULT 0,
    avg_ms      INTEGER,
    min_ms      INTEGER,
    max_ms      INTEGER,
    p50_ms      INTEGER,
    p95_ms      INTEGER,
    UNIQUE INDEX idx_latency_monthly_key (user_id, feature, conn_mode, conn_type, year_month)
);
```

---

## 七、后端 API 设计

### 1. 上报接口

```
POST /api/latency/report
```

浏览器/Gateway/Agent 上报延迟数据。服务端从 JWT token 中提取 `user_id`。

### 2. 查询接口（自动用户隔离）

所有查询接口自动按当前登录用户过滤，管理员可通过 `?user_id=X` 查看任意用户。

#### 延迟记录列表

```
GET /api/latency/records?feature=ssh&conn_mode=p2p_local&days=7&limit=50&offset=0
```

#### 聚合统计

```
GET /api/latency/stats?days=7
```

返回：
- 每种 feature × conn_mode 的平均/中位/P95/最大延迟
- 每个阶段的平均耗时

#### 阶段分析

```
GET /api/latency/stages?feature=ssh&conn_mode=p2p_local
```

#### 趋势数据

```
GET /api/latency/trend?feature=ssh&days=30
```

---

## 八、界面 UX 设计

### 8.1 菜单位置

**所有用户可见**，独立菜单项"我的延迟分析"：

```
📁 顶部导航
  ├── 文档管理
  ├── 网址管理
  ├── 远程管理
  ├── 我的延迟分析      ← 新增（所有用户）
  └── 系统管理           ← 仅管理员
```

管理员额外有"全局延迟概览"视图，可查看所有用户的汇总数据。

### 8.2 页面布局：我的延迟分析

```
┌─────────────────────────────────────────────────────────────┐
│  我的延迟分析                                                │
│                                                             │
│  ┌─────────────────────────────────────────────────────────┐│
│  │ 概览卡片 (4列)                                          ││
│  │ ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐   ││
│  │ │ SSH平均   │ │ SFTP平均  │ │ VNC平均   │ │ 总连接数  │   ││
│  │ │ 450ms    │ │ 280ms    │ │ 1.2s     │ │ 1,234    │   ││
│  │ │ ▲12% ⚠️  │ │ ▼5% ✅   │ │ ▲3%  ✅  │ │ 本月     │   ││
│  │ └──────────┘ └──────────┘ └──────────┘ └──────────┘   ││
│  └─────────────────────────────────────────────────────────┘│
│                                                             │
│  ┌─────────────────────────────────────────────────────────┐│
│  │ 筛选器                                                   ││
│  │ [功能: SSH ▾] [模式: 全部 ▾] [时间: 近7天 ▾] [搜索]      ││
│  └─────────────────────────────────────────────────────────┘│
│                                                             │
│  ┌─────────────────────────────────────────────────────────┐│
│  │ Tab: 延迟明细 | 阶段分析 | 趋势对比 | 模式对比矩阵       ││
│  └─────────────────────────────────────────────────────────┘│
│                                                             │
│  ┌──────────── 延迟明细 Tab ──────────────────────────────┐ │
│  │                                                        │ │
│  │  ┌────────────────────────────────────────────────────┐│ │
│  │  │ 瀑布图 (最近一次SSH连接的延迟分布)                   ││ │
│  │  │                                                    ││ │
│  │  │  前端准备  ████ 50ms                               ││ │
│  │  │  WS连接    ████████ 80ms                           ││ │
│  │  │  信令往返  ██████████████████ 180ms                ││ │
│  │  │  通道建立  ████████████ 120ms                      ││ │
│  │  │  Agent处理 ████████████████████████████ 260ms      ││ │
│  │  │  回传      █████ 40ms                              ││ │
│  │  │            ────────────────────────                 ││ │
│  │  │            总计: 730ms                              ││ │
│  │  └────────────────────────────────────────────────────┘│ │
│  │                                                        │ │
│  │  ┌────────────────────────────────────────────────────┐│ │
│  │  │ 记录表格                                            ││ │
│  │  │ ┌───┬──────┬───────┬──────┬──────┬──────┬──────┐  ││ │
│  │  │ │ # │ 时间  │ 功能  │ 模式 │ 目标  │ 总耗时│ 状态 │  ││ │
│  │  │ ├───┼──────┼───────┼──────┼──────┼──────┼──────┤  ││ │
│  │  │ │ 1 │ 09:42│ SSH   │ P2P  │192..│ 730ms│ ✅   │  ││ │
│  │  │ │ 2 │ 09:38│ SSH   │ GW   │192..│ 1.2s│ ✅   │  ││ │
│  │  │ │ 3 │ 09:35│ VNC   │ P2P  │192..│ 2.1s│ ✅   │  ││ │
│  │  │ │ 4 │ 09:30│ SFTP  │ GW   │192..│ 5.3s│ ❌   │  ││ │
│  │  │ └───┴──────┴───────┴──────┴──────┴──────┴──────┘  ││ │
│  │  │ [分页: < 1 2 3 ... 12 >]                           ││ │
│  │  └────────────────────────────────────────────────────┘│ │
│  │                                                        │ │
│  │  ┌────────── 展开行：阶段详情 ──────────────────────┐   │ │
│  │  │ ┌─────────────┬────────┬────────┬──────────────┐│   │ │
│  │  │ │ 阶段         │ 耗时   │ 占比   │ 状态          ││   │ │
│  │  │ ├─────────────┼────────┼────────┼──────────────┤│   │ │
│  │  │ │ 前端准备     │ 50ms  │ 6.8%  │ ✅ 正常       ││   │ │
│  │  │ │ WS连接       │ 80ms  │ 11.0% │ ✅ 正常       ││   │ │
│  │  │ │ 信令往返     │ 180ms │ 24.7% │ ⚠️ 偏高       ││   │ │
│  │  │ │ 通道建立     │ 120ms │ 16.4% │ ✅ 正常       ││   │ │
│  │  │ │ Agent处理    │ 260ms │ 35.6% │ 🔴 瓶颈       ││   │ │
│  │  │ │ 回传         │ 40ms  │ 5.5%  │ ✅ 正常       ││   │ │
│  │  │ └─────────────┴────────┴────────┴──────────────┘│   │ │
│  │  └─────────────────────────────────────────────────┘   │ │
│  └────────────────────────────────────────────────────────┘ │
│                                                             │
│  ┌──────────── 阶段分析 Tab ─────────────────────────────┐  │
│  │                                                        │  │
│  │  功能: [SSH ▾]  模式: [P2P本地 ▾]                      │  │
│  │                                                        │  │
│  │  ┌──────────────────────────────────────────────────┐  │  │
│  │  │ 阶段性能概览（箱线图）                             │  │  │
│  │  │                                                  │  │  │
│  │  │  前端准备  ├────[====|====]────┤  min:20  p50:45  │  │  │
│  │  │  WS连接    ├──[=====|=====]───┤  min:30  p50:80  │  │  │
│  │  │  信令往返  ├─[=====|======]──┤  min:100 p50:180  │  │  │
│  │  │  通道建立  ├──[====|====]────┤  min:80  p50:120  │  │  │
│  │  │  Agent处理 ├──[======|=======]┤  min:150 p50:260  │  │  │
│  │  │  回传      ├──[==|==]────────┤  min:20  p50:40   │  │  │
│  │  │                                                  │  │  │
│  │  │  ── min ── p50 ── avg ── p95 ── max ──           │  │  │
│  │  └──────────────────────────────────────────────────┘  │  │
│  │                                                        │  │
│  │  ┌──────────────────────────────────────────────────┐  │  │
│  │  │ 瓶颈分析                                          │  │  │
│  │  │                                                  │  │  │
│  │  │ 🔴 Agent处理 (avg 260ms, 占比 35.6%)             │  │  │
│  │  │    ↳ SSH TCP拨号+握手是主要耗时                    │  │  │
│  │  │    ↳ 建议：考虑SSH连接池预建                       │  │  │
│  │  │                                                  │  │  │
│  │  │ ⚠️ 信令往返 (avg 180ms, 占比 24.7%)              │  │  │
│  │  │    ↳ 可能受网络延迟影响                            │  │  │
│  │  │    ↳ 建议：检查ICE候选收集效率                     │  │  │
│  │  └──────────────────────────────────────────────────┘  │  │
│  └────────────────────────────────────────────────────────┘  │
│                                                             │
│  ┌──────────── 趋势对比 Tab ─────────────────────────────┐  │
│  │                                                        │  │
│  │  近30天延迟趋势                                         │  │
│  │                                                        │  │
│  │  1200 ┤                                                │  │
│  │  1000 ┤     ╱╲                                         │  │
│  │   800 ┤────╱──╲──── VNC (P2P)                          │  │
│  │   600 ┤   ╱    ╲──╲                                    │  │
│  │   400 ┤──╱──────╲──╲── SSH (P2P)                       │  │
│  │   200 ┤─────────────────── SFTP (P2P)                  │  │
│  │     0 ┤────┬────┬────┬────┬────┬────                   │  │
│  │        7/1  7/7  7/14 7/21 7/28 8/3                   │  │
│  │                                                        │  │
│  │  [功能: 全部 ▾] [模式: 全部 ▾] [时间: 30天 ▾]          │  │
│  └────────────────────────────────────────────────────────┘  │
│                                                             │
│  ┌──────────── 模式对比矩阵 Tab ─────────────────────────┐  │
│  │                                                        │  │
│  │  SSH 连接延迟对比                                       │  │
│  │  ┌──────────┬──────────┬──────────┬──────────┐        │  │
│  │  │          │ P2P本地  │ P2P远程  │ GW本地   │ GW远程  │  │
│  │  ├──────────┼──────────┼──────────┼──────────┤        │  │
│  │  │ 平均     │ 450ms   │ 680ms   │ 520ms   │ 890ms  │  │
│  │  │ P50      │ 380ms   │ 620ms   │ 450ms   │ 810ms  │  │
│  │  │ P95      │ 820ms   │ 1.2s    │ 950ms   │ 1.8s   │  │
│  │  │ 样本数   │ 342     │ 128     │ 256     │ 98     │  │
│  │  │ 成功率   │ 98.5%   │ 95.3%   │ 97.2%   │ 91.8%  │  │
│  │  └──────────┴──────────┴──────────┴──────────┘        │  │
│  │                                                        │  │
│  │  VNC 首帧延迟对比                                       │  │
│  │  ┌──────────┬──────────┬──────────┬──────────┐        │  │
│  │  │          │ P2P本地  │ P2P远程  │ GW本地   │ GW远程  │  │
│  │  ├──────────┼──────────┼──────────┼──────────┤        │  │
│  │  │ 平均     │ 1.2s    │ 2.1s    │ 1.5s    │ 3.2s   │  │
│  │  │ P50      │ 1.0s    │ 1.8s    │ 1.3s    │ 2.8s   │  │
│  │  │ P95      │ 2.5s    │ 4.2s    │ 3.1s    │ 6.5s   │  │
│  │  └──────────┴──────────┴──────────┴──────────┘        │  │
│  └────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────┘
```

### 8.3 管理员额外视图

管理员在"系统管理 → 延迟分析"可查看全局概览：
- 所有用户的平均延迟排行
- 按用户/Agent/Gateway维度的聚合
- 异常用户检测（某用户延迟异常高）

---

## 九、功能自身影响分析

### 9.1 数据量评估

每条记录约 **400 bytes**。

| 部署规模 | 并发用户 | 日记录数 | 年存储(原始) | 年存储(聚合后) |
|----------|---------|---------|-------------|--------------|
| 小型 | 20 | 2,400 | 350 MB | **50 MB** |
| 中型 | 100 | 24,000 | 3.5 GB | **250 MB** |
| 大型 | 500 | 240,000 | 35 GB | **2.5 GB** |

### 9.2 性能影响

| 维度 | 影响 | 说明 |
|------|------|------|
| **连接延迟** | **零影响** | 采集发生在连接完成后（异步上报），不阻塞主流程 |
| **写入压力** | **极低** | INSERT only，3个索引，写放大系数 1.14x |
| **查询压力** | **低** | 用户只查自己数据，索引命中 <1ms |
| **网络开销** | **~500B/次** | 每次连接多一个HTTP POST |

### 9.3 写入吞吐量

| 场景 | INSERT/秒 | 影响 |
|------|----------|------|
| 小型 (20用户) | 0.03 | 可忽略 |
| 中型 (100用户) | 0.28 | 可忽略 |
| 大型 (500用户) | 2.8 | 低——MariaDB 轻松处理数千次/秒 |

### 9.4 查询性能

| 查询场景 | 数据量 | 预计耗时 |
|----------|--------|----------|
| 用户查自己最近50条记录 | 50行 | <1ms |
| 用户查7天聚合统计 | ~168K行 | 10-50ms |
| 管理员查全局30天趋势 | ~1.68M行 | 100-500ms |

---

## 十、数据聚合策略

### 10.1 保留策略

```
原始记录 → 保留 7天 → 自动聚合为每日摘要
每日摘要 → 保留 90天 → 自动聚合为每月摘要
每月摘要 → 永久保留
```

### 10.2 聚合任务

每日凌晨2:00自动执行：

1. 查询 `latency_records` 中 7天前的数据
2. 按 `user_id × feature × conn_mode × conn_type × day` 分组聚合
3. 计算 avg/min/max/p50/p95
4. 写入 `latency_daily`
5. DELETE 原始记录（已聚合）

每月1号额外执行：
1. 聚合 90天前的每日摘要 → 月度摘要
2. DELETE 旧的每日摘要

### 10.3 存储节省

| 窗口 | 不聚合 | 聚合后 | 节省 |
|------|--------|--------|------|
| 97天 | 1,061 MB | 93 MB | **91.3%** |
| 1年 | 3.5 GB | 250 MB | **93%** |

### 10.4 聚合任务性能影响

- 执行频率：每日1次
- 处理数据量：约24K行/次（中型场景）
- 预计执行时间：<5秒
- 对系统影响：可忽略

---

## 十一、优化建议（基于架构分析）

| 瓶颈环节 | 现状 | 优化方案 |
|----------|------|----------|
| Agent SSH拨号 | 每次连接 TCP+SSH+Auth 全流程 | Agent端SSH连接池，预建到常用目标的连接 |
| Gateway桥接 | WS↔DC两层协议转换 | 减少JSON序列化开销，改用二进制帧 |
| ICE收集 | STUN/TURN候选收集耗时 | 预配置ICE server缓存（已有4分钟缓存） |
| SFTP客户端 | 每次请求 `sftp.NewClient()` | Agent端缓存SFTP客户端实例 |
| VNC首帧 | Tight编码首帧较大 | Agent端支持缩放/降质首帧 |
| 信令服务器 | 所有消息经过中心服务器 | Agent端直接回复（P2P模式已实现） |

---

## 十二、实施计划

| 步骤 | 内容 | 修改文件 |
|------|------|----------|
| 1 | 新建 `latency_records` + `latency_daily` + `latency_monthly` 数据库表 | `app/models.py` |
| 2 | 新建延迟上报+查询API | `app/api_latency.py` (新建) |
| 3 | 注册路由到主应用 | `app/main.py` |
| 4 | Browser端插桩：WebRTCManager记录阶段时间戳并上报 | `web/src/utils/webrtc.ts` |
| 5 | Agent端插桩：signal.go记录Agent侧阶段时间戳并上报 | `wragent/webrtc/signal.go` |
| 6 | Gateway端插桩：handler.go记录桥接延迟并上报 | `wrgateway/server/handler.go` |
| 7 | 前端界面："我的延迟分析"页面 | `web/src/components/LatencyAnalysis.vue` (新建) |
| 8 | 管理员额外视图：全局延迟概览 | `web/src/components/AdminLatencyOverview.vue` (新建) |
| 9 | 侧边栏添加菜单项（所有用户可见） | `app/models.py` (menus表) + `web/src/App.vue` |
| 10 | 聚合清理定时任务 | `app/tasks/latency_aggregate.py` (新建) |
| 11 | 前端构建部署 | `npm run build` + 重启容器 |
