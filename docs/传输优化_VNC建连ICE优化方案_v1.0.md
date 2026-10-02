# 传输优化：网关↔本地 Agent VNC 建连 ICE 优化方案 v1.0

> 状态：已被 v2.0 取代（仅存档）→ docs/传输优化_ICE优化与接口过滤方案_v2.0.md
> 原状态：方案（未实施，仅存档）。目标：把「WebRTC 通道建立」从 ~8.2s 降至 ≤1.5s，不影响功能成功率。

## 1. 背景与目标

- 场景：本地网关(local-gateway) + 本地 Agent(local-agent) 的 VNC 连接。
- 当前表现：E11 功能已通过（A/B/C 修复后），但建连第 6 步 WebRTC 通道建立长达 **8205ms**，占比 99.1%，体验不佳。
- 目标：压缩到 **≤1.5s**，且 **不降低功能成功率**（纯体验提速，E10/E11 已通过，_不依赖本优化_）。

## 2. 步骤6 耗时归因（网关/Agent 日志实测）

| 阶段 | 实测时间点 | 耗时 | 占比 | 证据 |
|---|---|---|---|---|
| ICE checking 开始 | 19:05:43（answer applied，候选已全量应用） | — | — | `[GA-DC] ICE room=…: checking` |
| ICE connected | 19:05:50 | **≈7s** | 89% | `ICE room=…: connected` |
| DC open（DTLS+SCTP） | 19:05:52 | ≈2s | 24% | `DataChannel open`（与上段重叠，合计≈8.2s） |
| conn_type | 19:05:54 | — | — | `local=host remote=host`（配对成功但晚了 7s） |

同一环境存在 **1s（16:30）/ 7s（19:05）/ 11s+prflx（19:00）** 波动 → **纯竞态/负载相关，非固定路径**。

## 3. 根因机制（已读 pion ice v4.0.5 / webrtc v4.0.8 源码确认）

| 项 | 当前状态 |
|---|---|
| 本机接口 | br0(LAN)、enp1s0(NO-CARRIER)、docker0、**32×br-xxxx(docker compose 网桥)**、tailscale0、veth*×N |
| 单侧 ICE 候选 | 实测 ≈35（30 host + 3 srflx + 2 relay） |
| 候选对 | 35 local × 35 remote ≈ **1200 对/端** |
| pion 联系节奏 | `connectivityChecks()`：`defaultCheckInterval = 200ms`，每 tick 对 checklist 全部 in-progress 对 `pingAllCandidates()` → ~6000pps 风暴 |
| 收敛链路 | 好对成功 → `getBestValidCandidatePair` → `isNominatable`（host=0 / srflx=500ms / prflx=1000ms / relay=2000ms）→ USE-CANDIDATE 提名 → 对端确认 → selected；**至少跨 2~4 个 200ms tick** |
| 结果 | ~1200 对风暴挤压好对（br0 host↔host）的响应 → 收敛 1~11s 漂移 |

**排雷要点（重要）**：主链路接口名为 **`br0`**（真实 LAN 网桥）。过滤必须排除 `br-`（docker compose 网桥 = `br-`+12hex）而**保留 `br0`**，绝不能按 `br` 前缀整体过滤或用“只留 eth*”白名单。
另外主链路好对历史上曾走 **Tailscale IPv6（fd7a:115c:a1e0::/48）** 而非物理 LAN，若接口过滤把 tailscale 全砍，需确认 br0 host↔host 对始终可达（同机必达）。

## 4. 优化方案对比

| # | 方案 | 机制 | 预期 step-6 | 成功率风险 | 改动面/工作量 |
|---|---|---|---|---|---|
| **O1★** | InterfaceFilter（docker/tailscale 黑名单） | `SettingEngine.SetInterfaceFilter`，候选 35→**3**（host br0 + srflx + relay），候选对 1200→**≈9** | **8.2s→<1.5s** | **极低** | 网关 `handler.go:763` + 本地 wragent `peer.go:26`；各加 config 开关；重建重启 4 容器 |
| O2 | NetworkTypes 仅 UDP4 | `SetNetworkTypes([UDP4])` | 微幅（v6 候选本就少） | 极低 | 与 O1 合并 |
| O3 | prflx/srflx 等待窗 1000→200 / 500→200ms | `SetPrflxAcceptanceMinWait` / `SetSrflxAcceptanceMinWait` | prflx 型会话 −0.8s | 极低 | 1 行 × 2 文件 |
| O4 | ICELite（Agent 端） | `SetLite(true)`（受控侧不主动检查） | −1~2s | **中**（与远端/二端行为耦合），备选 | 1 行 |
| O5 | 现状 | — | 7~11s 漂移 | — | 0 |

## 5. 推荐方案细节（O1 + 配置开关 + O3）

### 5.1 代码注入点
- 网关：`wrgateway/server/handler.go:763` `webrtc.NewPeerConnection(...)` → `api.NewPeerConnection(...)`
  - `api = webrtc.NewAPI(webrtc.WithSettingEngine(se))`，`se.SetInterfaceFilter(pred)` + `SetPrflxAcceptanceMinWait(200ms)` + `SetSrflxAcceptanceMinWait(200ms)`
- 本地 Agent：`wragent/webrtc/peer.go:26` `NewPeer` 内同样改用 api（三处业务共用）
- 过滤器谓词（两模块各一份，独立模块不宜共享）：
  - drop：`lo`、`docker0`、`tailscale0`、前缀 `br-`/`veth`/`docker`/`virbr`/`vbox`
  - keep：其余（含 `br0`）

### 5.2 配置开关（进程启动配置 JSON，非后台管理页里的 Agent 记录；改完重启对应容器生效）
- 网关：`wrgateway/config/config.go` 加 `*bool ICEInterfaceFilter json:"ice_interface_filter,omitempty"`，默认 **true**，env `WRG_ICE_INTERFACE_FILTER`（`1|true`）覆盖；写 `wrgateway/config.json`
- 本地 Agent：`wragent/config/config.go` 加同名字段，默认 true；写 `wragent/config.json`、`wragent/config2.json`、`/opt/wragent/config/config.json`
- `false` = 完全回到旧行为（1 键回退保险丝）

### 5.3 部署面
- 重建 `wrgateway`、`wragent` 二进制
- 重启容器：`pyterm_wrgateway`、`pyterm_wragent`、`pyterm_wragent2`、独立 `wragent`（/opt/wragent）
- **远端 node-* Agent 不推新二进制**（保持老行为，天然零回归）

## 6. 成功率保障

| 场景 | 路径候选 | 是否受影响 |
|---|---|---|
| 本地网关 ↔ 本地 Agent | br0 host↔host | 保留 ✓ 更快 |
| 互联网浏览器 ↔ 本地 Agent（直连/网关） | srflx(203.0.113.12)、relay(192.168.176.4) | 保留 ✓ |
| 网关 ↔ 远端 node-* Agent | 远端不更新；网关侧 srflx/relay 仍在 br0 上 | 零回归 ✓ |
| 兜底 | `ice_interface_filter:false` | 1 键回退 ✓ |

## 7. 验证计划
1. 谓词单测：`br0→keep`；`br-*`/`docker0`/`tailscale0`/`veth*`→drop。
2. 网关加 gather 完成计数日志 `[GA-DC] ICE local candidates gathered: N`，核验 35→3。
3. E11 改前/改后各 **3 次**计时（UI step-6 + 日志 ICE checking→connected）；E10 sftp 回归。
4. 走通一条远端 node-* via 网关会话，确认 relay/srflx 不回归。
5. 若窄场景仍慢，启用 O3 微调；O4 仅备选。

## 8. 相关代码位置备忘
- 网关建连：`wrgateway/server/handler.go:763`（`CreateDataChannel` 后、API 模式）
- 网关配置：`wrgateway/config/config.go`（`Config` 结构 + `Load`）
- Agent 建连：`wragent/webrtc/peer.go:26`（`NewPeer`），调用点 `wragent/webrtc/signal.go:439/684/783`
- Agent 配置：`wragent/config/config.go`（`Config` + `DefaultConfig`）
- pion 依据：`webrtc/v4@v4.0.8` `SettingEngine.SetInterfaceFilter/SetNetworkTypes/SetPrflxAcceptanceMinWait/SetSrflxAcceptanceMinWait`；`ice/v4@v4.0.x` `agent.go`（`defaultCheckInterval=200ms`、`pingAllCandidates`）、`selection.go`（`isNominatable` 等待窗）
- 本次排查还发现：`local-agent`/`local-agent-2` 于 2026-09-30 17:12:58 被admin删除（audit_log），已恢复 DB 行（owner=admin）；管理台删除这两个 Agent 会再次导致网关模式 VNC/SSH 报“无权访问该Agent”。

---

## 变更记录

| 版本 | 日期 | 变更 | 状态 |
|---|---|---|---|
| v1.0 | 2026-09-30 | 网关↔本地 Agent VNC 建连 ICE 优化：根因分析 + O1~O5 方案对比（未实施） | 已被 v2.0 取代 |
| v1.0-r1 | 2026-09-30 | 标记为已被 v2.0 取代（仅存档），补变更记录与版本管理规范 | 存档 |
