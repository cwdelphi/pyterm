# 连接诊断 DC 链路标注（P2P / Relay / BUG）v1.1

> 日期: 2026-09-28
> 状态: 已实施并通过测试（v1.0 直连；v1.1 网关模式，见第七章）

---

## 一、背景

前端已有 `parseConnType()` / `detectConnType()` 判定浏览器↔Agent 的 WebRTC 直连能力
（`network-candidates` 的 `xssy` 生成的 `wzr` 字段），但**结果没有落库**，诊断记录里看不到。
本方案把该判定写入 `connection_timeline`，并在诊断面板上做**筛选 + 统计**。

## 二、数据模型

| 列 | 类型 | 取值 |
|----|------|------|
| `connection_timeline.webrtc_path` | varchar(16), 默认 `""` | `P2P` / `relay` / `BUG` / 空(未检测) |

- 迁移：启动时 `ALTER TABLE connection_timeline ADD COLUMN webrtc_path VARCHAR(16) DEFAULT ''`
  （已存在则跳过，幂等）
- `""` = 尚未判定；**检测失败一律标 `BUG`**（前端 `detectConnType()` 的 catch 分支）

## 三、后端接口

| 接口 | 说明 |
|------|------|
| `POST /api/timeline/report` | body 新增可选 `webrtc_path`，随首报一并落库 |
| `POST /api/timeline/webrtc-path` | **补报**：`{"room_id","webrtc_path"}`，只更新该列，**不重算 steps/success**；空值不覆盖已有值；他人记录 404；非法值 400 |
| `POST /api/timeline/records` | 返回字段 + `webrtc_path` 筛选参数：`P2P` / `relay` / `BUG` / `none`(=空串) |
| `POST /api/timeline/detail` | `connection.webrtc_path` |
| `POST /api/timeline/stats` | 新增 `p2p_count` / `relay_count` / `p2p_rate`(0~100，分母=已判定记录数) |

> 时序说明：`detectConnType()` 需要 RTC connected + 1.5~4.5s，**晚于** `reportDiagnostic()`
> （一次性），因此首报常带不到值 → 检测完成后走 `webrtc-path` 补报；若检测先完成则首报直带值。

## 四、前端

| 文件 | 改动 |
|------|------|
| `web/src/utils/webrtc.ts` | 首报 body 加 `webrtc_path`；新增 `reportWebRtcPath()`；`detectConnType()` 成功/失败均落值（失败置 `BUG`） |
| `web/src/components/DiagnosisPanel.vue` | ① 筛选下拉（全部类型/P2P/Relay/BUG/未检测）② 列表新增「DC 链路」列 + 徽标 ③ 详情 `.dp-meta` 徽标 ④ 统计卡「P2P 占比」 ⑤ 空列表 `colspan` 10→11 |
| `web/src/i18n/{zh-CN,en}.json` | `diagnosis` 新增 7 键：`colWebrtcPath` `allPaths` `pathFilterP2P` `pathFilterRelay` `pathFilterBug` `pathFilterUnknown` `p2pRate`（zh/en 键集完全一致；v1.0 时 714 键，v1.1 再增 2 键 → 716） |

徽标样式：`.dp-p2p`(绿) / `.dp-relay`(黄) / `.dp-bug`(红) / `.dp-wp-none`(灰「未检测」)。

## 五、测试

`app/tests/test_timeline_webrtc_path.py`（TC-WP01~06）：

- 补报只写该列（steps 不变）、列表/筛选 `P2P` 与 `none` 互斥命中
- 首报直带值；非法值 400；他人 room 404；`BUG` 不被覆盖
- `stats` 返回 `p2p_count/relay_count/p2p_rate` 且 rate ∈ [0,100]

## 六、改动文件

```
app/database.py        + webrtc_path 列 / ALTER 迁移
app/api_timeline.py    + 字段、筛选、补报接口、stats
app/tests/test_timeline_webrtc_path.py   （新增）
web/src/utils/webrtc.ts
web/src/components/DiagnosisPanel.vue
web/src/i18n/{zh-CN,en}.json
```

## 七、网关模式（v1.1）

### 7.1 问题与根因

网关模式（`path_mode=gateway`）下**浏览器不创建 RTCPeerConnection**——`webrtc.ts` 只在直连分支
`createPeer()`（`:688`），网关分支（`:691-718`）只等待 `datachannel_ready`，终端数据走 WS 二进制帧。
判定逻辑 `detectConnType()` 无从执行。

实测：网关记录 **47 行 `webrtc_path` 全空**；直连 25 行有值（P2P13 / relay6 / BUG6）。

### 7.2 方案：由网关判定「网关↔Agent」链路，浏览器零协议改动

网关在**自己的** `PeerConnection`（网关↔Agent，`wrgateway/server/handler.go:734`）上读取 ICE 选中候选对，
经该浏览器会话的 `SendCh` 下发 JSON 文本帧，浏览器复用既有 `POST /api/timeline/webrtc-path` 补报。
**Python 后端零改动。**

| 环节 | 实现 |
|------|------|
| 判定 | `reportConnType()`：`dc.OnOpen` 后起 goroutine，轮询 250ms `peerConn.SCTP().Transport().ICETransport().GetSelectedCandidatePair()`（pion/webrtc v4.0.8），首见 pair 后 1.5s 稳定期，10s 无 pair → `BUG` |
| 分类 | 与浏览器 `parseConnType()` 对齐：任一端 `relay` → `relay`；两端 host/srflx/prflx → `P2P`；其余 → `BUG` |
| 下发 | `{"type":"connection_type","room_id","conn_type"}` → `session.SendCh` → writePump（≤0x2F 前缀二进制，本消息走 JSON 文本帧） |
| 接收 | `webrtc.ts` `handleSignal` 新增 `case "connection_type"`：写 `diag.connTypeDetected`；已上报则 `reportWebRtcPath(ct)`，否则随 `reportDiagnostic` 首报带出 |
| 重试 | `reportWebRtcPath(path, attempt=0)`：网络异常或 404/409（首报竞态）时 1.5s 后重试 **1 次** |

### 7.3 展示

`path_mode` 已随 list/detail 返回，徽标 `:title` 按其区分（`DiagnosisPanel.wpHint()`）：

- `gateway` → `diagnosis.wpHintGateway`：「DC 链路：网关 ↔ Agent（浏览器经 WSS 连接网关）」
- `direct` → `diagnosis.wpHintDirect`：「DC 链路：浏览器 ↔ Agent 直连（直连模式）」

i18n 新增 2 键，zh/en 同步，**714 → 716**。

### 7.4 验证（实机）

| 项 | 结果 |
|----|------|
| Go 单测 | `handler_conn_type_test.go` `TestClassifyConnType` **9/9 PASS**；`go vet ./server` 通过 |
| 构建发布 | `./scripts/build-wrgateway.sh` → **1.1.5**（3 平台 + canonical 副本），`.env LATEST_GATEWAY_VERSION=1.1.5`；重启 `pyterm_wrgateway` 后日志 `version=1.1.5` |
| T4 网关落库 | 新记录 id=585 `path_mode=gateway` `webrtc_path=P2P`（历史 47 行全空 → 首条有值）；网关日志 `conn_type room=… P2P (local=host remote=host)` |
| T5 面板展示 | 过滤「P2P」命中该行，第 7 列徽标 `P2P`，tooltip=网关提示 |
| T6 直连回归 | 直连记录 id=586 `webrtc_path=P2P`，直连行为不变 |
| 前端 | `npm run build` 成功、`npm test` **21/21** |

### 7.5 v1.1 改动文件

```
wrgateway/server/handler.go                + reportConnType / selectedCandidatePair / classifyConnType / sendConnType
wrgateway/server/handler_conn_type_test.go  （新增）
web/src/utils/webrtc.ts                     + case "connection_type"、reportWebRtcPath 重试
web/src/components/DiagnosisPanel.vue       + wpHint()，徽标 title 按 path_mode 区分
web/src/i18n/{zh-CN,en}.json                + wpHintDirect / wpHintGateway（714 → 716）
```
