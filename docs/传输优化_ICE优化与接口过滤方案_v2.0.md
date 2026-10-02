# 传输优化：ICE 接口过滤与连接优化方案 v2.0

> 版本：v2.3 | 状态：已定稿·**P0 + P1 + P2 全部实施并实测** | 日期：2026-10-01
> P0 提交：`6ecf962` · 发布：wragent **2.3.5** / wrgateway **1.1.7**
> P1 提交：`396080e`（发版 `bff2d22`）· 发布：wragent **2.3.7**
> P2 提交：`feat(ice)` P2（发版 `chore(release)`）· 发布：wragent **2.3.8** / wrgateway **1.1.8**
> 取代：`docs/传输优化_VNC建连ICE优化方案_v1.0.md`
> 决策：D1 缓存 P0–P2 本期 / D2 `allow_tailscale` 缺省 false / D3 网关本地 / D4 `ice_cooldown` 落地 / D5 本文件版本管理

---

## 0. 版本管理规范

| 规则 | 说明 |
|---|---|
| 命名 | `docs/<域>_<主题>方案_vX.Y.md`（版本号显式后缀） |
| 版本号语义 | **Y** = 措辞/表格修订（同文件更新并追加变更记录）；**X** = 结构性变更（新建文件，旧版保留不删） |
| 文档头 | 必含：`版本 / 状态 / 日期 / 取代关系` |
| 变更记录 | 文末固定 `## 变更记录` 表，任何编辑必须追加一行 |
| 取代链 | 新版头部写 `取代：<前版>`；前版状态改为 `已被 vX.Y 取代（仅存档）` |
| Git | **一版一 commit**，subject `docs(方案): <主题> vX.Y 存档`；方案 commit 与实施代码 commit 分离，可独立回滚 |

### 变更记录

| 版本 | 日期 | 变更 | 状态 |
|---|---|---|---|
| v1.0 | 2026-09-30 | 网关↔本地 Agent VNC 建连 ICE 优化：根因分析 + O1~O5 方案对比（未实施） | 已被 v2.0 取代 |
| v2.0 | 2026-09-30 | 新增后台「ICE 优化」页签、自动接口过滤规则生成、成功路径缓存与三级回退、预期收益量化、成熟方案对标；定稿 5 项决策 | 已定稿·待实施 |
| **v2.1** | 2026-09-30 | **P0 实测回填**：① §1.2 补 pion 实际枚举口径（有地址接口 38、非回环地址候选 46，修正原「有 IPv4 非 lo=29」）；② §5.1 收益表改「预测 + **实测**」双列；③ §7.3 门禁加实测结果列（**T4 step-6 8205→1217ms ✓**、T5 18✓、T10 全绿）；④ 新增 §7.5 P0 实施记录与运维备注（含重启暴露的 `auth_token` 漂移）；⑤ 落地字段/env 名回填 §4.1/§4.2 | **P0 已实施** |
| **v2.2** | 2026-09-30 | **P1 实施回填**：① §4.1 `config_json.ice` / `net_info` 通道由「待实施」改 ✅ 并回填实现落点；② §4.2 上下行 4 条消息全部 ✅，Admin→Agent 入口细化为 **HTTP 扫描端点驱动 WS `ice_scan_req`**；③ §7.3 T7 门禁由 ◐ 改 **✓ 6/6**；④ 新增 §7.6 P1 实施记录（规则优先级 / D4 节流 / 任务 1.12 看板 / 回归与发版） | **P1 已实施** |
| **v2.3** | 2026-10-01 | **P2 实施回填**：① §4.5 三级回退阶梯 / 成功路径缓存 / `if_hash` 由「待实施」改 ✅ 并回填实现落点（`ice_ladder.go` + `pathcache` + `netinfo.if_hash`）；② §4.4 阶梯实现基于 `IceRestart`（大门票）；③ 新增 §7.7 P2 实施记录（异步 Close 修复 / T8 证据链收口 / TX 部署 gx 还原 / 门禁全绿）；④ §7.3 补 P2 门禁：**E11 1/1、SFTP 18/18、T7 6/6、AC 全绿、三受影响 spec 8/8、vitest 60、pytest 26**；⑤ 确立「软件适应环境而非改环境」方针（封存防火墙手段）；⑥ 前端「Agent 控制台」分组缺省折叠 | **P2 已实施** |

---

## 1. 背景与根因

### 1.1 目标

把「WebRTC 通道建立（step-6）」从 **8205ms** 降到 **≤1.5s**，不降低功能成功率（E10/E11 已通过，不依赖本优化）。

### 1.2 实测数据（2026-09-30，宿主 `ip -o link` / `ip -o -4 addr`）

| 类别 | 数量 | 实例 | 是否产生候选 |
|---|---|---|---|
| loopback | 1 | `lo` | 排除 |
| 物理网卡 | 3 | `enp1s0`(NO-CARRIER)、`enp3s0`、`wlp2s0` | 无 IPv4 → 0 |
| 主链路桥 | 1 | **`br0` 192.0.2.50/24** | ✅ 必保留 |
| Tailscale | 1 | `tailscale0` 203.0.113.30/32 | ⚠️ D2：**默认过滤** |
| docker0 | 1 | `docker0` 172.17.0.1/16 | ❌ 噪声 |
| **docker compose 网桥** | **26** | `br-1a2f8ec15fb1` … | ❌ **风暴主源** |
| veth | 9 | `vetha8da3e2@if2` … | 无 IPv4，仍占扫描 |
| **合计** | **42** | 有 IPv4 的非 lo 接口 **29 个** | — |

> **v2.1 实测口径修正（P0 部署后核对）**：pion 的候选数按**地址**计（每个非回环单播地址 1 个，
> 含 v6 link-local），不是按接口计。宿主实测非回环地址 **46** 个、有地址接口 **38** 个
> （原表「有 IPv4 非 lo = 29」是 IPv4-only 口径，偏保守）。
> 故**真实基线**：host 候选 46、带 STUN+TURN 总候选 **51**、候选对 **2601/端（≈13000 pps）**，
> 比 §5.1 原预测（35 / 1200 / 6000pps）**更严重**——P0 收益相应更大。
>
> 与 v1.0 结论吻合：单侧候选 ≈35（30 host + 3 srflx + 2 relay），候选对 ≈1200/端。

### 1.3 根因链（pion/ice v4.0.10 源码核对）

| 环节 | 源码位置 | 事实 |
|---|---|---|
| 接口枚举 | `ice/v4@v4.0.10/net.go:79-83` | UP 检查后调用 `interfaceFilter(iface.Name)`，**只传接口名** |
| 检查节奏 | `ice/v4@v4.0.10/agent_config.go:17-18` | `defaultCheckInterval = 200ms` |
| 扫描动作 | `ice/v4@v4.0.10/agent.go:486` | 每 tick 对 checklist 全部 in-progress 对 `pingAllCandidates()` |
| 提名等待窗 | `ice/v4@v4.0.10/selection.go:34-41` | `isNominatable`：srflx/prflx 有最小等待 → 收敛至少跨 2~4 个 tick |
| 实测波动 | v1.0 §2 | 同环境 1s / 7s / 11s+prflx → 纯竞态/负载相关 |

**计算**：1200 对 × 5 次/s ≈ **6000 pps**（单端）连通性检查，挤压好对（br0↔br0）响应 → 收敛 1~11s 漂移。

### 1.4 排雷要点（继承 v1.0）

- 主链路接口名是 **`br0`**；硬黑名单必须是 `br-`（带连字符），**绝不命中 `br0`**；不得用「只留 eth\*」白名单。
- `tailscale0` 历史上曾承载好对（fd7a:115c:…/48）；D2 决定默认过滤，回归风险由 §5 回退阶梯 + UI 告警兜底。

---

## 2. 总体设计（三层能力）

```
                        ┌────────────────────────────────────┐
   后台管理 UI          │  Agent 配置 → 新页签「ICE 优化」      │
   (AgentConfigModal)   │  开关 / 模式 / 接口清单 / 缓存 / 看板  │
                        └───────────────┬────────────────────┘
                                        │ PUT /api/admin/agents/{id}/config
                                        │  (config_json.ice)
                        ┌───────────────▼────────────────────┐
                        │  FastAPI  app/auth_api.py           │
                        │  + WS 通道 api_isolated.py          │
                        │  register_success.config /          │
                        │  config_update（已有通道，热推送）     │
                        │  + 新增 network_info 上行 / 扫描下行   │
                        └───────┬─────────────────────┬───────┘
                                │ WS config_update    │ WS register_success
                   ┌────────────▼─────────┐   ┌───────▼──────────────┐
                   │  wragent (answerer)   │   │  wrgateway (offerer)  │
                   │  peer.go:26 → NewAPI  │   │  handler.go:763       │
                   │  + netinfo 扫描        │   │  → NewAPI             │
                   │  + icefilter 谓词      │   │  + netinfo 本地扫描    │
                   │  + path_cache 缓存     │   │  + path_cache 缓存     │
                   └───────────────────────┘   └──────────────────────┘
                         两模块各一份独立实现（沿用 v1.0「独立模块不宜共享」）
```

| 层 | 名称 | 缓存/复用对象 | 复用方式 | 未命中回退 |
|---|---|---|---|---|
| **L0** | 连接复用（**延后二期**，D1） | 已建立的 PeerConnection + DataChannel | 会话结束后保留 30s 空闲窗 | 关旧 PC → L1 |
| **L1** | 路径缓存（快路径） | 上次 selected pair 的**胜出本地接口** + `if_hash` | 只放行胜出接口，ICE 窗口 800ms | 超时/failed → L2 |
| **L2** | 规则缓存（标准） | 扫描自动生成的过滤规则（随 `config_json` 常驻） | 常规过滤建连 | ICE Failed → L3 |
| **L3** | 全量 ICE（兜底） | 无（= 今日行为） | 不过滤 | 真失败 → 报错 + 记指标 |

> **"复用不成功再走 ICE"** = L1→L2→L3 单向放宽阶梯，每级独立超时与上限；连续 3 次成功自动收紧一级；`if_hash`（接口清单指纹）变化时**立即**回落 L2，不等失败。

---

## 3. UX 界面设计

### 3.1 入口与页签

沿用 `AgentConfigModal.vue`（Agent 列表 → 「配置」→ 弹窗），在自定义页签条追加**第 4 个页签**（保留 `.cfg-tab` 类，e2e `autotest/tests/agent-config-regression.spec.ts:33` 定位不受影响）。

| 页签 | 现状 | 新增 |
|---|---|---|
| 基本信息 / 基础参数 / 隧道管理 | 已有 | — |
| — | — | **ICE 优化**（状态点：已启用 / 已降级 / 规则无效） |

### 3.2 线框图

```
┌─ Agent 配置 ────────────────────────────── Agent: local-agent ───────┐
│ [基本信息] [基础参数] [隧道管理·2] [ICE 优化 ●]                        │
├─────────────────────────────────────────────────────────────────────┤
│ ┌─ 1 总开关 ─────────────────────────────────────────────────────┐  │
│ │ 启用 ICE 接口过滤优化              [●━━━] 已启用                │  │
│ │ 减少候选与连通性检查量，预计建连 8.2s → 1.5s                     │  │
│ │ 生效方式：Agent 在线→保存即推送；离线→上线时生效；下一次建连生效    │  │
│ │ 兜底：进程级熔断（Agent 服务端配置 / 网关 env）只读提示            │  │
│ └──────────────────────────────────────────────────────────────────┘ │
│ ┌─ 2 过滤规则 ──────────────────────┐ ┌─ 3 生成与预览 ──────────────┐ │
│ │ 模式（四选 pill）                   │ │ [🔍 扫描并生成规则]          │ │
│ │  (●)自动推荐  ( )自定义白名单       │ │  Agent 离线 → 按钮置灰      │ │
│ │  ( )仅黑名单  ( )关闭不过滤         │ │  上次扫描 19:47（3 分钟前） │ │
│ │                                   │ │  接口 42 → 保留 1           │ │
│ │ 允许 Tailscale 接口   [ ━━━ ]     │ │  候选 35 → 3   (−91%)       │ │
│ │ （缺省关闭）                        │ │  候选对 1200 → 9 (−99%)     │ │
│ │ 失败自动放宽(三级回退) [●━━━]      │ │  检查 6000 → 45 pps (−99%)  │ │
│ │ 复用上次成功路径       [●━━━]      │ │  [查看规则 JSON ▾]          │ │
│ │ 连接级复用(L0,试验)   [ ━━━ ]     │ │                             │ │
│ └───────────────────────────────────┘ └────────────────────────────┘ │
│ ┌─ 4 网络接口清单（自动评分，随「扫描」刷新）──────────────────────────┐ │
│ │ 接口          类型    状态  IPv4               默认  评分  处置     │ │
│ │ br0          bridge  UP   192.0.2.50/24     ✓    90   [保留] ⭐  │ │
│ │ tailscale0   tun     UP   203.0.113.30/32      –    65   [过滤] ⚠   │ │
│ │ enp3s0       phys    DOWN –                   –    40   [过滤]     │ │
│ │ br-1a2f8ec…  docker  UP   172.30.0.1/16       –     0   [过滤] 🛡  │ │
│ │ vetha8da3e…  veth    UP   –                    –     0   [过滤] 🛡  │ │
│ │ …（共 42，虚拟滚动）                          [重新扫描]            │ │
│ │ 自动模式下「处置」只读；自定义模式逐行可切换（至少保留 1 个）          │ │
│ │ 🛡 = 硬黑名单命中（docker/veth/br-/virbr/vbox…），不可手工放开        │ │
│ │ ⚠ = 被过滤但历史曾为胜出接口 → 顶部黄色告警「建议开启允许 Tailscale」  │ │
│ └────────────────────────────────────────────────────────────────────┘ │
│ ┌─ 5 缓存与回退阶梯 ─────────────────────────────────────────────────┐ │
│ │ 当前级别 L1 ·快路径(上次胜出 br0, 800ms)  ▸ L2 自动规则 ▸ L3 全量   │ │
│ │ 缓存条目                                    命中/时间              │ │
│ │ local-agent · br0 → 192.0.2.50 ↔ …       12 次 · 19:41 [清除]   │ │
│ │                    [清空全部缓存]                                 │ │
│ │ ⚠ 接口指纹已变化（19:52 检出），已自动回落 L2                      │ │
│ └────────────────────────────────────────────────────────────────────┘ │
│ ┌─ 6 效果（近 7 天 connection_timeline 实测）────────────────────────┐ │
│ │ ICE P50 0.42s │ ICE P95 1.31s │ DC P95 1.62s │ 成功率 99.4%       │ │
│ │ 回退触发 3 次 │ L3 兜底 0 次  │ 规则无效 0 次  │ 上报 19:47        │ │
│ └────────────────────────────────────────────────────────────────────┘ │
│                                            [放弃]      [保存配置]       │
└───────────────────────────────────────────────────────────────────────┘
```

### 3.3 字段与交互规格

| # | 控件 | 绑定字段 | 默认 | 校验/边界 | i18n key（`agentConfig.*`，zh/en 双写） |
|---|---|---|---|---|---|
| 1 | 开关 `.cfg-toggle` | `ice.enabled` | `true` | 关闭后 2~6 置灰只读 | `iceEnable` / `iceEnableDesc` |
| 2 | 四选 pill | `ice.mode` | `auto` | `auto\|custom\|blacklist\|off` | `iceMode` `iceModeAuto` `iceModeCustom` `iceModeBlacklist` `iceModeOff` |
| 3 | 开关 | `ice.allow_tailscale` | **`false`（D2）** | 打开时提示回归风险 | `iceAllowTailscale` `iceAllowTailscaleDesc` |
| 4 | 开关 | `ice.auto_fallback` | `true` | 关闭 = 单级，失败即报错 | `iceAutoFallback` |
| 5 | 开关 | `ice.path_cache` | `true` | 与 #4 联动 | `icePathCache` |
| 6 | 开关 | `ice.conn_reuse` | `false` | 标注「试验·二期」，本期无后端行为 | `iceConnReuse` |
| 7 | 按钮 | — | — | Agent 离线置灰 + tooltip | `iceScan` `iceScanOffline` |
| 8 | 表格 | `ice.keep[]` | 自动 | `custom` 可改；硬黑名单行不可改；**keep 为空 → 红色告警并强制回落 `blacklist`** | `iceIfaces` `iceKeep` `iceDrop` `iceScore` `iceDefault` |
| 9 | 按钮 | — | — | 二次确认 | `iceClearCache` |
| 10 | 数字输入 | `ice_cooldown` | 2 | 0–30，语义改为「ICE 失败后重试节流（秒）」 | `iceCooling`（沿用）+ `iceCoolingDesc` |
| 11 | 统计条 | 只读 | — | 无数据显 `—` | `iceStatsP50` `iceStatsP95` `iceStatsFallback` `iceTailscaleWarn` |

**保存流程**（复用 `AgentConfigModal.vue:200 save()` 与脏检查）：新字段进 `payloadOf()`（`:59-72`）→ `adminUpdateAgentConfig` → `config_json.ice` → `_push_config_to_agent` 热推送。

- toast：`pushed:true` → 「已保存并推送到在线 Agent」；`pushed:false` → 「已保存，Agent 离线，上线后生效」。
- 只读态（共享 Agent）全部 `:disabled="readOnly"`。
- 提示文案：「已建立的连接不受影响，下一次建连生效」（`NewPeer` 每次新建时读取过滤器原子快照）。

### 3.4 状态与异常

| 场景 | 表现 |
|---|---|
| Agent 离线 | 扫描置灰；开关可改（存库，上线 `register_success.config` 下发） |
| 扫描超时（5s） | 内联错误「Agent 无响应」，保留上次清单 |
| 自动结果 keep 为空 | 红色告警「规则无效」，mode 强制 `blacklist`，上报 `rule_invalid` |
| 胜出接口被过滤 | 黄色告警 + 建议开启 `allow_tailscale`；**不自动改规则**，靠回退阶梯兜底 |
| 接口指纹变化 | 顶部横幅「已自动回落 L2」并提示重新扫描 |
| 保存失败 400 | 复用现有错误 toast |
| 旧 Agent 不识别 `ice` | `json.Unmarshal` 忽略未知字段 → 无害；按 `version` 显示「需升级」 |

---

## 4. 技术分析

### 4.1 配置模型（**零 DDL**，全部走 `agents.config_json`）

```jsonc
// agents.config_json（app/database.py:63，Text 默认 "{}"）
{
  "ws_reconnect_interval": 5, "ws_heartbeat_interval": 30,
  "ice_cooldown": 2, "log_level": "info", "plugins": { },
  "ice": {                                    // ← 新增（后台可写）
    "enabled": true,
    "mode": "auto",                           // auto | custom | blacklist | off
    "keep": ["br0"],
    "drop_prefix": ["br-", "docker", "veth", "virbr", "vbox", "vmnet",
                    "zt", "cni", "flannel", "cali", "tailscale"],
    "allow_tailscale": false,                 // D2 缺省 false
    "auto_fallback": true, "path_cache": true, "conn_reuse": false
  },
  "net_info": {                               // ← 新增（Agent 上报，只读）
    "if_hash": "a3f1…", "scanned_at": "…",
    "interfaces": [ {"name":"br0","state":"UP","addrs":["192.0.2.50/24"],
                     "is_default":true,"score":90,"keep":true} ],
    "rule": { },
    "last_pair": {"local_iface":"br0","conn_type":"P2P","rtt_ms":1,"ts":"…"},
    "stats": {"p50_ice":0.42,"p95_ice":1.31,"fallback":3,"rule_invalid":0}
  }
}
```

**必须处理的既有实现细节**：

1. `PUT /agents/{id}/config`（`app/auth_api.py:654-661`）**按请求体重建整个 dict** → 必须先读 `prev = loads(agent.config_json)` 保留 `net_info`，否则会被抹掉。
2. `_migrate_agent_config`（`:603-612`）用 `dict(config)` 浅拷贝 → GET 侧不丢键，补断言测试防回归。
3. `ice_cooldown`（`AgentConfigModal.vue:378` 有输入）Go 端零消费 → **D4 落地为「ICE 失败后重试节流」**，消除假开关。
4. **P0 现状**：`icefilter.Rule` 的 json 字段名已按上表对齐（`enabled/mode/keep/drop_prefix/allow_tailscale`），
   但 Go 端**尚未读取 `config_json.ice`**（P1 接线），当前规则一律来自本地 `netinfo` 自扫描 + 进程级熔断。

### 4.2 上下行消息通道

| 方向 | 消息 | 载体 | 现状 | 改动 |
|---|---|---|---|---|
| Admin→Server | `GET/PUT /api/admin/agents/{id}/config` | HTTP | ✅ | `IceOptReq` + `AgentConfigReq.ice`（`auth_api.py`），PUT 先读 `prev` 保留 `net_info` 与未传的 `ice` |
| Server→Agent | `{ice:{…}}` | `register_success.config` + `config_update` | ✅ | `ServerConfig.Ice *icefilter.Rule` + `ICECooldown *int`；`handleConfigUpdate` → `icecfg.Apply` |
| Admin→Agent | `{type:"ice_scan_req"}` | WS | ✅ | 入口细化：`POST /api/admin/agents/{id}/ice-scan`（HTTP，便于前端 await/超时）→ `_online_agents[aid].ws` 发 `ice_scan_req` → 等 `network_info` 回包（`asyncio.Future` + 5s 超时，离线/超时/被新请求取代 → 409）；agent 断开时未回包的 waiter 取消 |
| Agent→Server | `{type:"network_info"}` | WS | ✅ | `network_info` 分支 → `_persist_net_info` 合并进 `config_json.net_info` → 唤醒扫描 waiter；上报时机＝注册后 / 每 5min 且 `if_hash` 变化 / 扫描回包（`netinfo.ReportIfChanged` 去重） |
| Gateway | 本地扫描 + 文件/env | 无下发通道 | ✅ **P0 已落地** | `wrgateway/config/config.go` 加 `ice_interface_filter` + `ice_allow_tailscale`，env `WRG_ICE_INTERFACE_FILTER` / `WRG_ICE_ALLOW_TAILSCALE`；启动同步首扫 + 5min 本地重扫（D3） |

> **进程级通道（P0 已实施）**：网关用文件/env（D3 不接后台），Agent 用 `WRAGENT_ICE_INTERFACE_FILTER`（缺省 1）、
> `WRAGENT_ICE_ALLOW_TAILSCALE`（缺省 0），两端各自本地自扫描生成规则兜底。
> **下发通道（P1 已实施）**：`config_json.ice` 走 `register_success.config` / `config_update` 到 Agent，
> `IceOptReq` 经 `PUT /agents/{id}/config` 落库；服务端规则**覆盖**本地自扫描（`icefilter.ConfigureServer`），
> 本地 5min 重扫在覆盖期内变为 no-op，仅作断连兜底；`mode=auto` 解除覆盖恢复本地扫描。
> **熔断双通道**：后台 `enabled=false`（P1/T9，热生效，无需重启）；
> 进程级 `WRAGENT_ICE_INTERFACE_FILTER=0` / 网关 `WRG_ICE_INTERFACE_FILTER=false`（P0，需重启），
> env 熔断优先级最高，压过服务端规则。
| 指标 | `duration_ice` / `duration_dc` | `connection_timeline`（`app/database.py:239-249`、`api_timeline.py:66-67`） | ✅ | 新增聚合查询给看板 |

### 4.3 自动规则生成算法

```
输入: net.Interfaces() + InterfaceAddrs() + /proc/net/route(默认路由)
     + 历史胜出接口集合 H + 上次 if_hash

对每个接口 i:
  ① 硬否决 (score=-1, 不可手工放开):
       loopback | !FlagUp
       name 命中硬黑名单前缀: docker, veth, br-, virbr, vbox, vmnet, zt,
                              cni, flannel, cali, kube, weave
       ★ 硬黑名单是 "br-" (带连字符)，绝不匹配 "br0"
       tailscale/tailscale0（当 allow_tailscale == false 时，D2）
  ② 打分:
       是默认路由出口            +50
       有全局单播 IPv4           +20
       有全局单播 IPv6           +10
       物理网卡 (非 bridge/tun)  +15
       MTU ≥ 1400               + 5
       name ∈ H (历史胜出)       +30   // 仅记录展示，不越过 ① 的 tailscale 否决
  ③ 入选: score ≥ 60 的前 K 个 (K=3)，且必须 ≥ 1 个
  ④ 保底: 默认路由接口若存在 → 强制保留;
          结果为空 → rule_invalid，降级 mode=blacklist 并上报

输出 rule: { mode, keep[], drop_prefix[], min_keep:1, if_hash, generated_at }
谓词: keep(name) = !match(drop_prefix,name) && (mode=="blacklist" || name∈keep)
```

**本机预期输出**：`keep = ["br0"]`；`tailscale0` 因 D2 进入 `drop_prefix`（UI 显示 ⚠ 与黄色告警）。

### 4.4 过滤器注入（pion v4.0.8 API 已确认存在）

| 位置 | 现状 | 改动 |
|---|---|---|
| `wragent/webrtc/peer.go:26` | `webrtc.NewPeerConnection(config)` | → `webrtc.NewAPI(webrtc.WithSettingEngine(se)).NewPeerConnection(config)`，`se.SetInterfaceFilter(pred)` |
| `wrgateway/server/handler.go:763` | 同上 | 同上 |
| 备选（窄场景仍慢时启用） | — | `se.SetPrflxAcceptanceMinWait(200ms)` / `SetSrflxAcceptanceMinWait(200ms)` |

**覆盖面**：`NewPeer` 是 agent 三处调用的唯一入口（`signal.go:443` speedtest / `:685` tunnel / `:790` handleOffer）→ 一次注入覆盖 VNC/SSH/SFTP/tunnel/speedtest；网关仅 `handler.go:763` 一处。`SettingEngine` 为每次建连读取的原子快照 → 热更新只影响下一次建连，无需重启。

**版本差异**：`wragent` 用 `ice/v4@v4.0.10`、`wrgateway` 用 `ice/v4@v4.0.5` → 同步升到 v4.0.10，避免两端行为不一致，并确认含 pion/ice#899（srflx STUN 遵守过滤）。

### 4.5 缓存与回退阶梯

| 级别 | 触发条件 | ICE 行为 | 窗口 | 失败动作 |
|---|---|---|---|---|
| **L1** 快路径 | `path_cache=true` 且 `if_hash` 一致 且 缓存 <30min 且 有 `last_pair` **且胜出接口未被 D2 过滤** | `SetInterfaceFilter` 只放行 `last_pair.local_iface` | 800ms 无选中对 → 放弃 | 重建 → L2 |
| **L2** 标准 | 默认 | 自动规则过滤 | ≤1.5s 目标 | `OnICEConnectionStateChange==Failed` → L3 |
| **L3** 全量 | `enabled=false` 或规则无效或 L2 失败 | 不过滤（= 今日行为） | 现状 7~11s | 报错（复用 `handler.go:845-852`） |

- **放宽（降级）**：挂在网关 `handler.go:843-852` 现有 Failed 分支——由「关 PC + 报错」改为「`level++` → 重建 PC → 重发 offer」，复用前端既有 3 次重试链路。
- **收紧（升级）**：连续 3 次成功 → `level--`；`if_hash` 变化 → **立即**回落 L2 并重扫。
- **缓存键**：`(agent_id, if_hash)`；值 `{local_iface, local_ip, remote_ip, conn_type, rtt_ms, ts, hits}`。
- **缓存位置**：进程内存为主 + `wragent/data/ice_cache.json` 落盘；`last_pair` 上报到 `net_info` 供 UI 展示，清空经 `config_update` 附 `ice_cache_clear:true`。
- **L0（连接级复用）**：D1 决定延后二期，本期仅保留 UI 开关占位。

### 4.6 风险与上游已知问题

| 风险 | 来源 | 影响 | 对策 |
|---|---|---|---|
| **过滤全部 host 候选 → socket demux 竞态，answerer 侧 ICE 通但 SCTP 80% 失败** | [pion/webrtc#3176](https://github.com/pion/webrtc/issues/3176) | **高**：agent 恰是 answerer（`signal.go:752`） | 硬约束 `keep ≥ 1` 个有地址接口；禁用「全过滤」；单测覆盖空集降级 |
| 过滤后 srflx STUN 可能不遵守过滤（旧版） | [pion/ice#727](https://github.com/pion/ice/issues/727) PR #899 | 中：仍暴露公网 IP | 升级到含 #899 的 ice 版本并核对 |
| 收敛下界由 200ms tick + `isNominatable` 等待窗决定 | `agent_config.go:18` / `selection.go:34` | 目标只能到 1.5s 而非 0.1s | 不追求 0；必要时启用 O3 缩短等待窗 |
| 网关 Failed 即关 PC 报错，无第二次机会 | `handler.go:843-852` | 阻断回退阶梯 | §4.5 改造 |
| `PUT` 重建 config 丢未知键 | `auth_api.py:654-661` | `net_info` 被抹 | §4.1 合并写入 |
| `Config.Save` 只持久化 3 个身份字段 | `wragent/config/config.go:59-75` | 本地开关无法落盘 | 开关以服务端 `config_json` 为准（**修正 v1.0 §5.2**） |
| D2 默认过滤 tailscale 的回归 | v1.0 §3 排雷要点 | 好对历史曾走 tailscale | 回退阶梯 + UI 告警 + 不自动改规则 |
| e2e 选择器 | `agent-config-regression.spec.ts:33` | — | 只加 tab 不改类；补新用例 |

---

## 5. 预期收益分析

### 5.1 量化收益

| 指标 | 现状（P0 前实测） | P0 接口过滤（**预测**） | P0 接口过滤（**2026-09-30 部署实测**） | P0+P2 缓存/回退 |
|---|---|---|---|---|
| 有地址接口数 | 38（含 26 个 docker 桥） | **1** | **38 → 1**（`keep=[br0]`） | 1（L1）~1（L2） |
| host 候选（地址数） | 46 | — | **46 → 2**（br0 v4 + v6-linklocal） | 2 |
| 单侧 ICE 候选（含 STUN+TURN） | ≈51 | **≈3** | **51 → 8~9** | ≈8 |
| 候选对/端 | **2601** | **≈9（−99%）** | **2601 → 64~81（−96.9%）** | ≈64 |
| 连通性检查速率 | ≈13000 pps/端 | **≈45 pps（−99%）** | **≈405 pps（−96.9%）** | ≈320 |
| **step-6 WebRTC 通道** | **8205ms**（21 次均值 5164ms，max 13894ms） | **≤1.5s（−82%）** | **1211 / 1214 / 1226 ms，均值 1217ms（−85%）✓** | 命中 ≤1.2s（−85%） |
| 建连耗时波动 | 1s / 7s / 11s | 稳定 <1.5s | 3/3 ≤1.3s，无长尾 | 稳定 <1.2s |
| 功能成功率 | 100%（E10/E11） | 100% | **E11 3/3 ✓、SFTP 18/18 ✓** | 100%（三级回退兜底） |
| DC 链路类型 | P2P | P2P | **P2P（local=host remote=prflx）3/3 ✓** | P2P |
| 内网拓扑暴露面 | 46 个内网地址进候选 | **1 个** | **46 → 2 个** | 2 个 |
| 双端 CPU / 日志量 | 高 | 显著下降 | 显著下降 | 更低 |

> 备注：srflx/relay 候选（≈3+2）不由接口数决定，随 `keep` 一并保留。**v2.1 实测**：STUN+TURN 侧候选 6~7 个（高于原估 5），
> 故过滤后总候选 8~9、候选对 64~81（高于原估 9/36），但仍在 **−97%** 量级，且 step-6 已实测 1217ms ≤ 1.5s，收益结论不变。
> 若需再压到候选对 ≤9，需另行收敛后台下发的 3 个 STUN/TURN server（非 P0 范围，列 P1 观测项）。

### 5.2 业务与运维收益

| 维度 | 收益 |
|---|---|
| 用户体验 | VNC/SSH/SFTP 建连从可感知卡顿 8s → 约 1.5s，P95 收敛 |
| 稳定性 | 消除竞态/负载相关的随机长尾；并发会话不再互相挤占 |
| 资源 | STUN 包量 −99%，conntrack、CPU、日志体积同步下降 |
| 安全 | 候选从暴露 29 个内网地址降到 1 个（pion #843 明确动机之一） |
| 可运维 | 后台可视化 + 一键扫描 + 1 键熔断 + 自动回退，无需登机改配置 |
| 可度量 | 复用已有 `connection_timeline.duration_ice/dc`，收益可量化、可回归 |

### 5.3 投入产出

| 阶段 | 内容 | 工作量 | 风险 | 收益 |
|---|---|---|---|---|
| **P0** | 双端 `SetInterfaceFilter` + 进程级开关 + gather 计数日志 + 单测 | 1.5d | 低 | 建连 −82%，检查 −99% |
| **P1** | 后台页签 + `ice`/`net_info` 存储 + 扫描通道 + 自动规则 + `ice_cooldown` 落地 | 2d | 低 | 可视化、可控、可自动 |
| **P2** | 路径缓存 + 三级回退 + `if_hash` 失效检测 + 效果看板 | 2d | 中 | 再降 + 兜底保证成功率 |
| 合计（本期） | | **5.5d** | | |
| Phase 4（备选，不排期） | P0 延后项：P3/L0 连接复用（3d）；网关接后台下发（+1d） | — | 中高 | 重连 0ms |

---

## 6. 决策记录

| # | 决策点 | 定稿结果 |
|---|---|---|
| D1 | 缓存深度 | **P0~P2 本期（5.5 人日），P3「L0 连接级复用」延后二期** |
| D2 | Tailscale 接口 | **`ice.allow_tailscale` 缺省 `false`（默认过滤 `tailscale0`）**；默认 `keep=["br0"]`，候选 35→3、候选对 1200→9 |
| D2' | D2 回归护栏 | 规则生成不因历史胜出放开 tailscale；若 `last_pair.local_iface==tailscale0` 被过滤 → UI 黄色告警 + 回退阶梯自动升 L3 兜底，**不自动改规则** |
| D3 | 网关开关 | 本地扫描 + 文件/env 开关，不接后台；后台接入列 Phase 4 备选 |
| D4 | `ice_cooldown` | **落地为真实行为**：ICE 失败后的重试节流窗口（默认 2s，0–30s） |
| D5 | 方案存档 | 按 §0 规范版本管理，一版一 commit |

---

## 7. 执行计划

### 7.1 提交切分

```
commit 1  docs(方案): 传输优化_VNC建连ICE优化方案 v1.0 补提交
commit 2  docs(方案): ICE优化与接口过滤方案 v2.0 存档
commit 3  feat(ice): P0 双端 SetInterfaceFilter + netinfo 扫描 + 进程级开关 + 单测
commit 4  feat(ice): P1 后台 ICE优化页签 + 自动规则 + network_info 通道
commit 5  feat(ice): P2 路径缓存 + 三级回退 + 效果看板
```

### 7.2 任务分解

**P0（1.5d）**

| 序 | 任务 | 位置 | 产出 |
|---|---|---|---|
| 0.1 | 归档 v1.0 + 新建本文件 | `docs/` | 一版一 commit |
| 0.2 | `icefilter` 包（双端各一份） | `wragent/icefilter/filter.go`、`wrgateway/icefilter/filter.go` | 硬黑名单 `br-`/`docker`/`veth`/`virbr`/`vbox`/`vmnet`/`zt`/`cni`/`flannel`/`cali`；`tailscale0` 默认 drop；**空集 → rule_invalid → 降级 blacklist** |
| 0.3 | `netinfo` 扫描 + 评分 + `if_hash` | `wragent/netinfo/scan.go`、`wrgateway/netinfo/scan.go` | §4.3 算法 |
| 0.4 | Agent 注入 SettingEngine | `wragent/webrtc/peer.go:26` | 覆盖 `signal.go:443/685/790` |
| 0.5 | 网关注入 SettingEngine | `wrgateway/server/handler.go:763` | 同上 |
| 0.6 | 网关进程级开关 | `wrgateway/config/config.go:8-20` | `ice_interface_filter` + `WRG_ICE_INTERFACE_FILTER`（新增 `strconv.ParseBool`） |
| 0.7 | 网关本地扫描周期 | `wrgateway/main.go` 启动 + 5min | 接口变更重算 |
| 0.8 | gather 计数日志 | `handler.go:912`、`peer.go:26` 后 | `[GA-DC] ICE local candidates gathered: N` |
| 0.9 | 版本对齐 | `wrgateway/go.mod` `ice v4.0.5→v4.0.10` | 与 agent 一致 |
| 0.10 | 单测 T1/T2 | 双模块 `*_test.go` | 42 接口 fixture → `keep==["br0"]` |

**P1（2d）**

| 序 | 任务 | 位置 |
|---|---|---|
| 1.1 | 页签 | `AgentConfigModal.vue:27` union 加 `'ice'`、`:290-300` 第 4 个 `cfg-tab`、`:401` 前插入面板 |
| 1.2 | 字段接线 | `payloadOf()` `:59-72`、loader `:119-136`、默认值 `:18-24` |
| 1.3 | 类型与 API | `web/src/api.ts:103-118` + `adminScanAgentIce` |
| 1.4 | 开关控件 | 复用 `:423-426` + `:838-865` `.cfg-toggle` CSS |
| 1.5 | i18n | `web/src/i18n/{zh-CN,en}.json` → `agentConfig` 段 |
| 1.6 | 请求模型 | `app/auth_api.py:552` 加 `ice: IceOptReq` |
| 1.7 | **合并写入** | `app/auth_api.py:654-661` 保留 `net_info` |
| 1.8 | 下行扫描 | `app/api_isolated.py` 新增 `ice_scan_req` 分支（离线/5s 超时 → 409） |
| 1.9 | 上行网络信息 | 新消息 `network_info` → `config_json.net_info` |
| 1.10 | Agent 消费配置 | `server_config.go:19` 加 `Ice`；`signal.go:968` 应用 |
| 1.11 | `ice_cooldown` 落地（D4） | agent 失败重试节流，默认 2s |
| 1.12 | 统计看板接口 | 聚合 `connection_timeline.duration_ice/dc` |

**P2（2d）**

| 序 | 任务 | 位置 |
|---|---|---|
| 2.1 | 路径缓存 | 双端内存 + `wragent/data/ice_cache.json`；键 `(agent_id, if_hash)` |
| 2.2 | 回退阶梯 | `handler.go:843-852` Failed 分支改造 |
| 2.3 | L1 快路径 | 只放行 `last_pair.local_iface`；若被 D2 过滤则跳过 L1 |
| 2.4 | 缓存上报/清空 | `network_info.last_pair` → `net_info`；`ice_cache_clear:true` |
| 2.5 | 看板 UI | 页签 §6 卡片 |
| 2.6 | e2e | 扩展 `agent-config-regression.spec.ts` |

### 7.3 验证门禁

| # | 用例 | 标准 | P0 实测结果（2026-09-30） |
|---|---|---|---|
| T1 | 谓词单测 | `br0→keep`；`br-*`/`docker0`/`veth*`/**`tailscale0`（默认）**→drop；空集→降级 | **✓ 8/8**（`icefilter`） |
| T2 | 自动规则（42 接口 fixture） | `keep==["br0"]`；`br-` 不误伤 `br0` | **✓ 8/8**（`netinfo`，`host 29→1` `pairs 1156→36`） |
| T3 | gather 计数 | ~~35 → ≤4~~ → **v2.1 修订**：host 候选 46→**≤4**；含 STUN+TURN 总候选 51→**≤10**；候选对→**≤100** | **✓ host 46→2；总候选 51→8~9；候选对 2601→64~81** |
| T4 | E11 VNC 建连 ×3 | step-6 ≤1.5s（基线 8205ms） | **✓ 1211.4 / 1213.6 / 1225.5 ms（均值 1217ms，−85%）** |
| T5 | E10 SFTP 回归 | 全通过 | **✓ 18/18**（`tests/sftp` + `sftp-stability`） |
| T6 | 远端 node-\* via 网关 | relay/srflx 不回归，`conn_type` 正常 | **◐ 本地 3/3 P2P ✓**；远端 node-\* 未发新二进制（本就不该动），待有远端会话复核 |
| T7 | 页签 e2e | 4 tab 可点、保存 `pushed:true`、离线扫描 409/置灰 | **✓ 6/6**（`agent-config-regression`：AC-04 渲染 + 4 模式 pill + 接口清单、AC-05 关总开关→pill 禁用 + dirty、AC-06 保存→重开回读→写后还原；AC-01/03 顺带修复——原引用已删除的 `remote-agent`，改指 `node-297b94443a6621e402df35ea`） |
| T8 | 回退阶梯 | `if_hash` 变更 / L2 失败 → 自动升 L3 成功，3 次成功后降回 | ◐ **P2 未实施** |
| T9 | 熔断 | `enabled=false` → 行为与现状一致 | **✓ 单测**（`TestICEFilterFuse` / `TestDisable` / `TestEnvGate`） |
| T10 | 静态门禁 | `go test ./...` + `go vet`、`cd web && npm test`、`npm run build`（tsc 0 error） | **✓** 双模块 `go test` 全绿；`vitest 60✓`；`vite build ✓`；`go vet` 仅 `signal.go:2255/2419` **2 条 IPv6 提示为改动前既有**（`signal.go` 未被 P0 修改） |

### 7.4 回滚

1. 后台 1 键 `enabled=false`（已建连接不受影响，下次建连恢复旧行为）。
2. 网关 `WRG_ICE_INTERFACE_FILTER=false` + 重启。
3. 代码级 `git revert`（方案与代码分 commit，可独立回滚）。
4. 远端 node-\* Agent 不发新二进制，天然零回归。

### 7.5 P0 实施记录（2026-09-30）

**代码（commit `6ecf962`，方案与代码分 commit）**

| 落点 | 内容 |
|---|---|
| `wragent/icefilter/`、`wrgateway/icefilter/` | 规则模型 + `Match` 谓词 + 进程级 `Configure/Current/Predicate/Summary` + env 熔断 `SetEnvGate`；`Normalize` 硬黑名单强制合并、空 keep 降级 `blacklist` |
| `wragent/netinfo/`、`wrgateway/netinfo/` | `net.Interfaces` + `/proc/net/route` 默认路由；评分/硬否决/`selectKeep`（默认路由强制保留 + Top-K）；`Report` 含 `if_hash` 与收益估计；`StartRefresher` 同步首扫 + 5min 重扫；`EnvBool` |
| `wragent/webrtc/peer.go` `NewPeer` | `webrtc.NewAPI(webrtc.WithSettingEngine(se)).NewPeerConnection`，`se.SetInterfaceFilter(icefilter.Predicate())` |
| `wrgateway/server/handler.go` | 同上；追加 `OnICEGatheringStateChange` → `[ICE-FILTER] gathering complete … local_candidates=N`；`countLocalCandidates`（stats 优先，SDP 兜底） |
| `wragent/main.go` | `WRAGENT_ICE_INTERFACE_FILTER`（缺省 1）/ `WRAGENT_ICE_ALLOW_TAILSCALE`（缺省 0）+ 首扫日志 |
| `wrgateway/main.go`、`config/config.go` | `IceInterfaceFilter`（**缺省 true**）/ `IceAllowTailscale`（缺省 false），json 键 `ice_interface_filter` / `ice_allow_tailscale`，env `WRG_ICE_INTERFACE_FILTER` / `WRG_ICE_ALLOW_TAILSCALE`（`strconv.ParseBool`） |
| `wrgateway/go.mod` | `pion/ice v4.0.5 → v4.0.10`（与 wragent 对齐）；`pion/logging v0.2.2 → v0.2.3` |

**发布**：`build-wragent.sh` → **2.3.5**、`build-wrgateway.sh` → **1.1.7**；
`/opt/wragent/wragent` 同步刷新；`docker restart pyterm_wrgateway pyterm_wragent pyterm_wragent2 wragent`。

**运行时证据**

```
[ICE-FILTER] host=38->1 pairs=2601->49 mode=auto keep=[br0] drop_prefix=13 if_hash=970436f76be59b2d
[ICE-FILTER] gathering complete: ... local_candidates=8~9
[GA-DC] conn_type room=vnc-... P2P (local=host remote=prflx)
timeline_steps step_index=6 -> 1211.4 / 1213.6 / 1225.5 ms
```

**⚠ 运维备注（非 P0 引入）**：重启暴露 `wragent/config.json`、`config2.json` 的 `auth_token`
与 DB `agents.token` **漂移**（md 日志 `token mismatch/rejected`，agent 掉进 setup 模式 →
E11 前 3 次失败于 **step 7「Agent未连接目标服务器」**，step 6 本身 `ok`）。
处置：按 DB token 回填两份 config 后重启，注册成功，E11 复跑 **3/3 ✓**。
根因是历史 token 轮转后本地文件未同步，**与本次代码无关**；后续可在后台加
「注册失败 → 拉取 DB token 自愈」或把 token 纳入 `config_update` 下发。

### 7.6 P1 实施记录（2026-09-30）

**代码（commit `396080e`；发版 `bff2d22` = wragent 2.3.7）**

| 落点 | 内容 |
|---|---|
| `app/auth_api.py` | `IceOptReq`（`mode` 用 `Literal[auto,custom,blacklist,off]`，非法值 **422**）+ `AgentConfigReq.ice`；PUT 写库前读 `prev`，**`net_info` 与未传的 `ice` 一律保留**；新增 `POST /api/admin/agents/{agent_id}/ice-scan`（`agent:manage` 权限） |
| `app/api_isolated.py` | `_ice_scan_waiters`（`agent_id → Future`）、`request_ice_scan`（离线 **409** / 5s 超时 **409** / 被新请求取代 **409**）、`_persist_net_info`（合并写 `config_json.net_info`）、WS elif 链 `network_info` 分支；agent 断开的 finally 清理时 cancel 未回包 waiter |
| `wragent/icefilter/filter.go` | `remote`（`serverOverride`）+ `ConfigureServer`/`ClearServerOverride`/`ServerOverride`；覆盖期内 `Configure` 变 **no-op**；`Summary()` 追加 `src=server|local` |
| `wragent/icecfg/apply.go`（新包） | `Apply(*icefilter.Rule)(src,error)`、`Cooldown(sec)`（0–30s 夹取）、`NormalizeMode`；`src` ∈ `auto|off|remote|env` |
| `wragent/netinfo/scan.go` | `SetOptions`/`CurrentOptions`/`SetReporter`/`LastReport`/`ReportIfChanged(r,force)`；`StartRefresher` 每 tick 用 `CurrentOptions(opt)`（选项可热更） |
| `wragent/webrtc/signal.go` | `register_success` 后 `go SendNetworkInfo(LastReport())`；`case "ice_scan_req"` → 回包；`handleConfigUpdate` 应用 `SetICECooldown` + `icecfg.Apply`，打 `[ICE-CFG] applied src=%s %s` |
| `wragent/webrtc/ice_cooldown.go`（新） | **D4 落地**：`NewPeer` 首行 `waitICECooldown()`，两个 `Failed` 分支 `markICEFailure()`；缺省 **2s**、0–30s、`0` 关闭 |
| `wragent/main.go` | `netinfo.SetReporter(signalHandler.SendNetworkInfo)`（`runNormalMode` 内） |
| `app/api_timeline.py` | 任务 **1.12**：`/api/timeline/stats` 按 `protocol` 聚合 → `ice_stage`/`dc_stage`/`webrtc_stage`（`count/avg/p50/p95`） |
| `web/src/components/AgentConfigModal.vue` | 第 4 个页签「ICE优化」：总开关 / 4 模式 pill / `allow_tailscale` / keep + drop_prefix 文本框 / 本机接口清单表 + 「立即扫描」；`.cfg-toggle input{opacity:0}` 导致 Playwright 判不可见 → e2e 点 `.cfg-toggle-slider`、状态用 `input.isChecked()` |
| `web/src/{api.ts,i18n/*}` | `AgentIceConfig`/`AgentNetInfo`/`adminScanAgentIce`；i18n 中英各 **+42 键（718→761，两语言键集一致）**；后端 `app/locale/*` 补 3 个 409 文案键；`AGENTS.md:50` 键数同步 761 |

**规则优先级（实现口径）**

```
env 熔断 SetEnvGate(false)            → 最高，压过一切
mode=off 或 enabled=false             → ConfigureServer(DisableRule())   src=off
mode=custom / blacklist               → ConfigureServer(rule)           src=remote（本地 5min 重扫挂起）
mode=auto（默认）                     → ClearServerOverride()           src=local（本地自扫驱动）
```

**运行时证据**

```
[ICE-CFG] applied src=off disabled
[ICE-CFG] applied src=auto mode=auto keep=[br0] src=local
[ICE-FILTER] host=38->1 pairs=2601->49 mode=auto keep=[br0] drop_prefix=13 if_hash=e81c5a0d9b05bbd0
POST /agents/local-agent/ice-scan → {"ok":true,"net_info":{...}}      if_hash=e81c5a0d9b05bbd0 ifs=42 host=38->1
POST /agents/node-5d26bd.../ice-scan → 409 {"detail":"ICE扫描超时(5s)，请检查Agent连接"}   （旧版二进制不回包）
/api/timeline/stats → ice_stage{count:603,avg:1222.2,p50:148.0,p95:7786.8}
                       dc_stage {count:1320,avg:70.1,p50:22.3,p95:301.3}
                       webrtc_stage{count:69,avg:1736.8,p50:196.8,p95:9509.1}
```

**门禁（P1 段）**：双模块 `go test ./...` ✓（新增 `icecfg` 6 例 / `icefilter` 覆盖 3 例 / `webrtc` 节流 2 例）；`vitest 60✓`；`vite build ✓`；**T7 e2e 6/6 ✓**；**T5 SFTP 18/18 ✓**；**T4 E11 ✓**（未回退）；`POST .../ice-scan` 离线 409 / 在线 200 实测 ✓。

**发布**：`build-wragent.sh` → **2.3.7**（2.3.6→2.3.7 仅收敛 `[ICE-CFG]` 日志去重）；
`cp wragent/wragent /opt/wragent/wragent && docker restart pyterm_wragent pyterm_wragent2 wragent pyterm_md`。
wrgateway 本次无改动（仍 1.1.7）。**未发新二进制给远端 node-\*** → 它们忽略未知 `ice`/`ice_scan_req` 键，天然零回归。

**待办（P1 未覆盖）**：T6 远端 node-\* via 网关仍 ◐（无远端会话）；统计看板前端页面（任务 1.12 的接口已就绪，**未做页面**）。

---

### 7.7 P2 实施记录（2026-10-01）

**代码（commit `feat(ice)` P2 主跃；发版 `chore(release)` = wragent **2.3.8** / wrgateway **1.1.8**）**

| 落点 | 内容 |
|---|---|
| `wragent/netinfo/scan.go` | `if_hash` 计算（接口集哈希，`ifup/ifdown`/换网即变化）随 `network_info` 上报 |
| `wragent/pathcache/`（新） | 成功路径内存缓存 + `ice_cache.json` 落盘，30min TTL，`if_hash` 变化即失效；`last_pair` 记入 `net_info` |
| `wragent/webrtc/ice_ladder.go` | **三级回退阶梯**：L1 快路径（`last_pair` 只放行单接口，800ms 窗口超时 → 回落 L2 `cause=l1_window_timeout`）/ L2 标准自动过滤 / L3 全量（L2 `Failed` → `cause=ice_failed` 重建）；基于 **`IceRestart`（重建 PC + 重发 offer）** 而非粗暴断开；连续 3 次成功 L2 升级 L1 |
| `wragent/webrtc/peer.go` | 阶梯状态机 + 成功/失败计数 + `IceRestart` 后的 offer 重发；`level` 上报 |
| `wrgateway/server/ice_ladder.go` | 网关侧阶梯驱动：`Failed` 分支由「关 PC + 报错」改「`level++` → 重建 → 重发 offer」；`connect_success` 携带 agent 侧 `config_json.ice{auto_fallback,path_cache}` 策略，网关按 agent 记忆 |
| `wrgateway/server/handler.go` | 重建 PC + 重发 offer（复用前端既有 3 次重试链路）；`[ICE-LADDER]` 阶梯日志（`cause=` / `命中接口/cache_hits`） |
| `wragent/webrtc/signal.go` / `peer.go` | **异步 Close 修复**（P2 首项，未随 P1 发版的补丁）：关闭路径不再同步阻塞读循环，`OnICEConnectionStateChange` 竞态与 `1006` 误判收敛 |
| `app/api_isolated.py` | `_agent_ice_policy`：`connect_success` 时随包下发 `auto_fallback/path_cache` 策略（缺省 true/true） |
| `web/src/api.ts` / `AgentConfigModal.vue` / i18n | `AgentNetInfo` 增 `last_pair`/`ladder`/`path_cache`；ICE优化页签展示路径缓存与阶梯自统计；i18n 中英各 **+21 键（761→782）**；`AGENTS.md` 键数同步 782 |
| `autotest/tests/agent/t8-ice-ladder.spec.ts`（新） | T8 阶梯连通性断言（可重复跑） |
| `autotest/tests/agent-config-regression.spec.ts` | ICE优化页签 + 隧道面板回归用例扩展（含 AC-01/02/03） |

**T8 证据链收口（用户选定方案 C：不强制注入 L2 失败，改证据链）**

```
run3（2026-10-01 11:04，wrgateway 日志）：
  [GA-DC] ICE room=vnc-542d9d39-1790823813751: failed (level=L2)      ← 30s 后超时失败
  [ICE-LADDER] 重建为L3 (cause=ice_failed)                             ← L2→L3 升级证据
  offer sent level=L3 /  [ICE-FILTER] gathering complete local_candidates=53 level=L3
run4/5（15:11 会话，同一时段）：
  [ICE-LADDER] …L2 命中本地接口 br0 (conn_type=P2P, 缓存 1 条)          ← 缓存命中证据
  [ICE-LADDER] …L1 窗口 800ms 无选中对 → 回落 L2 (cause=l1_window_timeout)  ← L1→L2 回落证据
  [ICE-LADDER] if_hash 变化 → 清理 2 条路径缓存, 回落到L2                ← if_hash 失效证据
单元测试：wragent/webrtc/ice_ladder_test.go、wrgateway/server/ice_ladder_test.go、netinfo/stats_test.go
TX 部署后远端会话复核：node-c601536bf3ef7778b5e2028f 在线 + 3 服务器 ICE 配置接收 ✓
```

**TX 部署 & gx 还原（2026-10-01）**

- **TX（portal.example.com，Ubuntu 22.04）**：`wragent` 2.3.4 → **2.3.8**（gx 构建 `commit=8df03a5 built=20261001032226`，md5 `3d71a4299e8c47de79762c4115146546`；配置 `wss://gx.example.com:5588`、agent_id `node-c601536bf3ef7778b5e2028f`）；平台「升级指令 → 持久化 /wragent/wragent → 自重启」，注册成功 + 接收服务端 ICE 配置 3 服务器 ✓。
- **gx 污染还原**：8 条 T8 iptables（7×`T8-LADDER` + `T8PROBE_CG3`）**全部删除**，`OUTPUT` 复原为原生 `-P OUTPUT ACCEPT` + `-j OUT_BT`（T8 时探针计数=1 的疑惑一并封存）；`local-agent` `config_json.ice` 由 `custom keep=[nonexistent0]` 还原 `mode=auto`（agent 热更新 `applied src=auto mode=auto` ✓）；md `.env` 去掉 `APP_LOG_LEVEL=DEBUG` 并 `--force-recreate`；`/tmp/{t8cap.pcap,ipt_*_*.txt,*_t8.log}` 清理。**`zz-debug-ice.spec.ts` 已删除**。
- **防火墙手段封存**（分布式强调「软件适应环境而非改环境」）：本机探针命中集团不中规则⑤之谜、nft/nat DNAT/hairpin 结构不再深挖。纯配置制造 L2 失败路径分析归档（`len(msg.ICEServers)>2` 才 Set、`keep=[nonexistent0]` 不拦 srflx/relay、icefilter.Rule 无 STUN 开关）。

**门禁（P2 段，全绿）**：双模块 `go test ./...` ✓（新增 `ice_ladder` 单测 / `pathcache` / `stats_test`）；`go vet` 仅 2 条既有 IPv6 警告；`vitest 60/60 ✓`；`vite build ✓`；**受影响 spec：webterm-local 4/4 + speedtest 3/3 + tunnel-hotconfig 1/1 = 8/8 ✓**（webterm-local 首测反转为「缺省折叠上下文」）；**E11 1/1 ✓、SFTP 18/18 ✓、T7 6/6 ✓、AC 全绿 ✓**；后端 pytest 定向 **26/26 ✓**（`test_stun_ice + test_api_isolated + test_config_api + test_agent_config_plugins + test_auth_api`）。

**FEAT（web）Agent 控制台分组缺省折叠**：`web/src/components/SshManager.vue:46` `collapsedGroups` 默认集加 `'agents'`（控制台不默认展开 Agent 分组，仅有自己 Agent 的界面更清爽）；`autotest/tests/helpers.ts` 新增 `expandAgentGroup(page)`（`.agent-group .group-header` 选择器，避开 i18n 文案）；三个 spec 适配后 **8/8 ✓**。

**提交切分（一次一 commit）**
```
feat(ice): P2 路径缓存+三级回退阶梯+if_hash                    （P2 主代码 + 单测 + 前端 ICE 页签 + AC 回归）
chore(release): wragent 2.3.8 / wrgateway 1.1.8
feat(web): Agent控制台分组缺省折叠
docs(方案): 传输优化 ICE优化与接口过滤方案 v2.3（本文件）
```

---

## 8. 参考的成熟方案

| # | 方案 | 出处 | 借鉴点 / 我们的用法 |
|---|---|---|---|
| 1 | **Pion `SettingEngine.SetInterfaceFilter`** | [pion/webrtc#843](https://github.com/pion/webrtc/issues/843) → PR #844（2019）；`settingengine.go` 官方注释；[Pion ICE 配置文档](https://pion-webrtc.mintlify.app/advanced/ice-configuration) 含"排除 docker/veth"示例 | **直接采用**为过滤机制；配套 `SetIPFilter`/`SetNetworkTypes`。官方动机与我们一致：大量网卡拉起过多 goroutine、减少向对端暴露内网拓扑 |
| 2 | **W3C `iceTransportPolicy: 'all'\|'relay'`** | [W3C WebRTC PC](https://www.w3.org/TR/webrtc/#dom-rtcconfiguration-icetransportpolicy) | 更粗粒度兜底；项目已在 speedtest 使用（`signal.go:441/786`）。建连中收窄 policy 会作废现有候选对，故只允许建连前设置 |
| 3 | **RFC 8305 Happy Eyeballs v2** | [rfc-editor.org/rfc/rfc8305](https://www.rfc-editor.org/rfc/rfc8305.html) §4/§5 | staggered attempts + "历史数据优先 used addresses，但 MUST NOT 跨网卡使用、换网时 flush" → `if_hash` 失效即刷新缓存的标准化依据；250ms/100ms/2s 尝试间隔上下界借鉴到 L1 的 800ms 窗口 |
| 4 | **RFC 8445 §9 ICE Restart** | [rfc-editor.org/rfc/rfc8445](https://datatracker.ietf.org/doc/html/rfc8445.html) | 重启期间旧候选对继续承载直到新对被提名 → 回退阶梯用 `IceRestart` 而非粗暴断开；[RTMA 实战分析](https://www.real-time-media-architecture.com/webrtc-protocol-stack-signaling-servers/connection-recovery-ice-restart/triggering-ice-restart-without-dropping-media/) 给出旧对保底 + `selectedCandidatePairChanges` 作完成信号的成熟做法 |
| 5 | **Tailscale magicsock `bestAddr` / `trustBestAddrUntil`** | [wgengine/magicsock/endpoint.go](https://github.com/tailscale/tailscale/blob/v1.102.0/wgengine/magicsock/endpoint.go) | **主参考**：生产级「记住上次可用路径 → 信任窗口内直接用 → 过期/失败则全量 disco 探测 → DERP 兜底」。L1/L2/L3 阶梯 + 缓存 TTL + 失败放宽即移植此模型到 ICE |
| 6 | **Chrome `WebRtcIpHandlingPolicy` 企业策略** | Chromium 文档 | `default / default_public_interface_only / default_public_and_private_links / disable_non_proxied_udp` → 由管理端集中下发接口过滤策略的成熟先例（我们做 per-agent 粒度） |
