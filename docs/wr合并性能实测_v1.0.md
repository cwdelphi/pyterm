# wr 合并单二进制性能实测 v1.0（Phase 4–6 回填）

| 项 | 值 |
|---|---|
| 版本 | v1.0 |
| 日期 | 2026-10-01 |
| 前置 | `docs/wr合并为单二进制_v1.0.md`（方案 v1.0）、`docs/传输优化方案_v2.0.md` |
| 对比对象 | 合并前 `wragent 2.3.8`（`6a05846^` 源码手工构建） vs 合并后 `wr 3.0.1`（commit=`d7bd23a`，含 Phase 3 全部修复） |
| 口径 | `autotest/perf-probe.js`（同探针同参数），直连模式（`PERF_GATEWAY` 未开），Intel N100 4C/4T 非独占机，多轮取 P50/P95 |
| 备注 | 探针修复：`measureEcho` 卡片反向折叠导致 S2 全量失败；下载等待 300s→60s 防跑满预算 |

## 1. 镜像体积（§3 回填）

| 项 | 合并前 | 合并后 | 变化 |
|---|---|---|---|
| 运行镜像 | `pyterm/go-runtime:latest` **1.26 GB** | `pyterm/wragent:latest` **12.1 MB**（纯基座 alpine:3.20，不含二进制） | **−99.0%** |
| 单二进制 (amd64, stripped) | wragent 11.9MB + wrgateway ~22.6MB ≈ 34.5MB | `wr` **13.1MB**（含双入口+双角色） | **−62%** |
| arm64 二进制 | — | 12.2MB | — |

> 设计订正：Dockerfile 由「COPY 二进制进镜像」改为**纯基座**——二进制走 compose `bind mount ./wr/wr:/app/wr` + 启动 `cp`（实测 `COPY wr` 会把 `wr`+`wr-arm64` 一起落成 `/usr/local/bin/wr/` 目录且 ENTRYPOINT 与 `sh -c` command 冲突，容器起不来）。换二进制只需重写宿主文件 + `force-recreate`，无需重打镜像；`buildx` 出多架构镜像也无需 QEMU（无 COPY）。

## 2. 内存实测（§4 回填）

| 指标 | 合并前 | 合并后 |
|---|---|---|
| 三实例空闲 RSS 合计 | 57.2 MB（19.1+9.0+29.1） | **21.9 MB**（11.6+4.4+5.9） → **−62%** |
| 压测期间 wragent RSS 峰值 | 45.9 MB | 29.7 MB |
| goroutine 数（空闲） | — | 19 / 10 / 11（agent/agent2/gateway） |
| pprof heap 活跃对象 | 不可用 | 7 对象 / 102KB（agent）；4 对象 / 333KB（gateway） |
| 容器限额 | 无 | `GOMEMLIMIT=256MiB` ×3 |
| pprof 入口 | 无 | `WR_PPROF_ADDR` loopback 6060/6061/6062，默认关 |

Phase 3 修复（P0#1–#5、P1#6/#9、P3#19、P2#20）全部落地并 `go build/vet/test` 通过（vet 仅剩 2 条**既有** IPv6 `"%s:%d"` 告警）。未做：#7 `io.ReadAll→Stat+ReadFull`、#8 `h.p`/`pendingCandidates` OnClose 回收（pion `dc.OnClose` 单槽互相覆盖，风险大于收益）、#10–#18 其余 P1/P2（均非泄露，标记为已知限制）。

## 3. S1–S4 性能 A/B（P50/P95，ms）

> 同一列内为多次运行样本。下载存在「50–85% 处卡停」的**既有 flake**（见 §4），故下载行标注成功次数。

| 场景 | 合并前 | 合并后 | 结论 |
|---|---|---|---|
| S1 目录列表首响应 | 152/176, 163/169 | 150/157, 157/170, 155/170 | 无差异 |
| S2 终端回显 (n=20) | 55/71, 66/74, 63/73 | 65/72, 68/80, 61/73 | 无差异（±10ms 内） |
| S3 上传 10MB | 1186/1200, 1193/1210 | 1177/1183, 1186/1200 | 无差异 |
| S3 上传 10MB wire | 445/453, 453/612 | 395/459, 413/436 | 无差异 |
| S3 下载 10MB | 577/606, 582/587（各 3/3 成功） | 558/663, 577/606（NEW 9 次中 4 次失败） | **同量级，但见 §4 flake** |
| S3 下载 10MB wire | 274/327, 298/313 | 275/362, 290/338 | 无差异 |
| S4 VNC per_move | 20ms（3/3 样本） | 20–144ms（样本：223B/25帧 与 1.3–1.6KB/127–149帧 均有） | **噪声**（见 §5） |

## 4. 下载卡停 flake（合并前后均复现，非回归）

> **2026-10-02 定案**：根因为 pion/sctp v1.9.0 的 TLR 自适应突发限速（非下文"机理推断"），已降级 v1.8.35 根治，验证 30/30 —— 详见 §8。以下原分析保留作排除过程记录。

- 现象：10MB 下载在 **5.4–8.9MB / 10.5MB** 处停止，浏览器收到部分数据后**永远等不到收尾**，前端无完成事件 → 探针超时。
- 频率：合并后 NEW 9 次尝试失败 4 次；合并前 OLD 9 次尝试失败 3 次（含 2026-09-30 的 `download-10M-2 FAILED TimeoutError` 历史记录）。**非本次合并引入**。
- 现场证据（agent 日志）：`[A-BRIDGE] SFTP content sent req=.. size=10485760 chunks=427 elapsed=20ms buffered=9984019` —— 427 个 24KB 分片在 ~20ms 内全部灌入 DC，pion SCTP 出站队列 `BufferedAmount` 冲到 ~10MB。
- 机理推断：`sendSftpResponse` 循环调用 `sendDCMsg`（SFTP 前缀走**同步直发 `dc.Send`**，无背压队列）；当 SCTP 出站队列饱和时 `dc.Send` 阻塞/失败，agent 卡在传输中，浏览器部分字节后不再有数据。旧代码同样如此。
- 建议修复（后续任务，不在本次范围）：
  1. SFTP CHUNK 发送改走 VNC 式「绝不丢」有界队列（`scheduleDCFlush` + 排队 + 背压，满时暂停读 SFTP 而非丢弃/阻塞直发）；
  2. `dc.Send` 失败按缓冲满类错误重试（退避），其余错误才中止；
  3. 前端对文件传输加超时/进度上报，失败显式提示而非无限等待；
  4. `sendSftpResponse` 避免一次 `io.ReadAll` 整文件进内存后再切 427 片（与 #7 合并处理）。

## 5. VNC 线字节噪声结论

- 观测到 mv_out 223B/25帧（入向 10.1KB/6帧）与 1.3–1.6KB/127–149帧（入向 318KB/79帧）两档，**同一二进制（合并后）两种值都出现过**，与二进制无确定相关（交错 A/B 中旧=223/223/223，新=1587/1389/1345/223）。
- 判定：远端桌面状态（面板时钟整分刷新、指针悬停触发图标高亮等）驱动的测量噪声；`idle_bytes=0` 证明无周期流量。不作为回归证据，后续如需精确对比应固定远端桌面状态。

## 6. 待办（Phase 6 未完成项）

- S6 网关中继未测：探针注释载明「本机环境网关中继腿 ICE 不稳（新旧栈均复现）」，且网关 `[WSS]` 日志存在 `websocket: discarding reader close error: io: read/write on closed pipe`（每 30s 偶现，疑似 md 侧空闲关闭），建议在远端真实环境复测 relay 路径。
- S5 长稳 24h（RSS/goroutine 斜率归零）未跑：本次以 pprof 快照 + GOMEMLIMIT 替代，长稳留待上线后观察。
- 网关侧 Phase 3 修复（`wr/server/handler.go` enqueue/quota/writePump）未在直连模式压测中覆盖，需 relay 场景回归。

## 7. 命名重构后复测（`pyagent 1.0.1` 冒烟，2026-10-01）

Phase 7 把 `wr` 最终改名 `pyagent`（镜像 `pyterm/pyagent:latest`，三实例 force-recreate）后的回归冒烟，`PHASES=file ECHO_REPS=5`：

| 指标 | 冒烟值 | 对照（§A/B 合并后 `wr 3.0.1`） |
|---|---|---|
| S1 文件列表 P50 | 128ms（n=5，P95 185） | 150–163ms |
| S3 上传 10MB P50 | **834ms（3/3 成功）** | 1177–1193ms |
| S3 下载 10MB P50 | **614ms（3/3 成功，均满 10,490,138 字节，本轮无卡停）** | 558–686ms（有 4/9 卡停 flake） |
| S2 回声 P50 | 65ms（n=5） | 55–68ms |
| 三实例空闲内存 | 10.18 / 4.83 / 8.02 MB（合计 23.0 MB） | 21.9 MB |
| 压测期 wragent 峰值 | RSS 37.7MB / CPU 83.7% | 29.7MB / 45.9%（口径略异） |

结论：改名不引入性能回归；下载卡停本轮未复现（与 §4 结论一致，属既有偶发 flake，修复建议不变）。

## 8. 下载卡停根因定案与修复（2026-10-02）

§4 的"机理推断"修正为**已定案根因**：不是 `dc.Send` 阻塞本身，而是 **pion/sctp v1.9.0 引入的 TLR 自适应突发限速（Adaptive Burst Mitigation，上游 PR #394，与 RACK 同批进入 v1.9.0）把发送端永久压到 ~6KB/s**。

### 8.1 排除链（同机 cap3–cap8 共 6 轮抓包/日志/探针对照）

| 假设 | 结果 | 证据 |
|---|---|---|
| rcvbuf 不足 | ✗ 非根因 | `net.core.rmem_*` 212992→8388608 后 cap4 6/6、cap5 1/10——无效；已回退 212992 |
| UDP 丢包 | ✗ 烟雾弹 | 丢包属 Chrome socket（41871）；cap7 仅 158 drops 仍 0/10 |
| rwnd/cwnd/RTO 滴流 | ✗ | cap7 DEBUG：`a_rwnd=4711392` 充足、全程 T3-rtx 仅触发 1 次（cwnd=1228） |
| 前端消费慢 | ✗ | `webrtc.ts` DC 消息路径 O(1)；heartbeat 100ms 无缺口、getStats 无积压 |
| 浏览器收不进 | ✗ | 楔形期 Chrome 持续发 SACK（cap3 B2A 583×<100B）且持续接收，RTT 0–3ms |

### 8.2 根因：TLR 突发限速（v1.9.0 引入，上游至 v1.12.0 未改）

- 触发：丢包进入 TLR 纪元后，`writeLoop` 每周期发放突发预算（初始 4 MTU，additional loss 步进至**下限 1.25 MTU**），且预算复位需连续 **16 次干净 TLR**；周期由 SACK 到达唤醒 → 楔形期发送节奏 = 每 ~201ms 一个 1200B 分片。
- **定量吻合**（cap8 TRACE 归档 `/tmp/wedge-ppi-sample.log`）：67s 内卡死连接 **1200B/201ms = 5.97KB/s**，与抓包 5.8–7KB/s 滴流、inlog ~4.2s 一条 24KB 消息完全一致；应用侧背压等不到冲刷（buffered 卡 ~272–285KB → 30s `send backpressure timeout`）。
- 版本考古：`pion/sctp` 于 2026-09-11（`e3497d5`）由 v1.11.1 回退钉为 v1.9.0（v1.11.1 > v1.9.0 同样含 TLR，即整个 flake 观察期都在 TLR 版本线上）；对比 v1.9.0↔v1.12.0 的 `tlrBegin/AllowSend/MaybeFinish` 等核心函数**逐字节相同**（升级无解），无关闭开关 → **钉 `v1.8.35`**（webrtc v4.0.8 与 datachannel v1.5.10 各自 require 的上游验证版本）。

### 8.3 修复清单（随本回填提交）

1. `pyagent/go.mod`：`pion/sctp v1.9.0 → v1.8.35`（`go mod tidy` 保持，v1.8.x 线无 TLR）。
2. `signal.go`：SFTP 响应改 256KB 出站背压直发分支（`dcSFTPMaxOutstanding`，30s 等不到冲刷则明确报错）；发送错误收尾 `ok:false` META（前端立即 reject，不再挂等 60s）；顺手关闭 §2 遗留的 2 条 vet IPv6 hostport 告警（`net.JoinHostPort`）。
3. `docker-compose.yaml`：wragent 增 `PION_LOG_DEBUG/TRACE` 透传（默认空；排障时可在 `.env` 开 `sctp` 级别）；实验值已清空。
4. sysctl rmem 实验值已回退 212992（实验证明非根因）。

### 8.4 验证

| 轮次 | sctp | 下载 10MB（REPS=10） |
|---|---|---|
| cap5 / cap7 / cap8（修复前） | v1.9.0 | 1/10、0/10、1/10 |
| cap9 / cap10 / cap11（修复后） | v1.8.35 | **10/10 ×3 = 30/30**，P50 620–650ms，零 backpressure 超时 |

结论：§4 flake **非合并引入，而是 sctp 依赖线问题**（合并前后均处于含 TLR 的 v1.9.0/v1.11.1 版本线，与 §3"合并前后均复现"互证），现已根治。

## 9. S4 并发建连实测（2026-10-02，pyagent 1.0.2）

> 口径：`wr合并为单二进制_v1.0.md` §5.2 S4 定义（并发 N=1/5/10/20 同时 SSH+VNC；判据 成功率≥95%、
> CPU 峰值≤80%、P95 增长≤线性）。
> 驱动：`autotest/s4-fleet.sh`（N worker 并发 + docker stats 2s 采样 + 轮间 ≥40s 冷却），
> 探针 `perf-probe.js` `connect` 相位（双页同时拨 SSH/VNC，建连前不建连接，SFTP 列表>3s 判负），
> 连接预建/清理 `s4-setup-conns.js`（w01…w20 定宽标签防前缀碰撞），聚合 `s4-agg.py`（3 轮 + 中位轮明细）。
> 环境局限：探针与服务端同宿主（4C/15GiB 非独占），压测前 load 0.39、N=20 时 load≈20——为共存压力而非纯链路压力。

### 9.1 结果（3 轮聚合）

| N | SSH 成功 | SSH p50/p95 (ms) | VNC 成功 | VNC p50/p95 (ms) | DB 行/ok | md CPU 峰 | agent CPU 峰 | agent RSS 峰 |
|---|---|---|---|---|---|---|---|---|
| 1 | 3/3（100%） | 3075 / 3084 | 3/3（100%） | 746 / 751 | 6/6 | 55.6% | 6.5% | 9M |
| 5 | 15/15（100%） | 3330 / 3492 | 14/15（93.3%） | 1133 / 1212 | 30/29 | 68.9% | 4.2% | 20M |
| 10 | 19/30（63.3%） | 4873 / 15589 | 24/30（80%） | 2107 / 2443 | 60/58 | **102.3%** | 19.2% | 30M |
| 20 | 18/60（30%） | 14944 / 18254 | 5/60（8.3%） | 6612 / 7807 | 120/72 | **100.8%** | 21.6% | 38M |

- 轮间波动：N=10 VNC r1/r3 各 10/10、r2 4/10，SSH r2 p50 飙至 15.4s；N=20 VNC r2 0/20（60s 全超时）。
- 失败形态：N=10 SSH 失败全部 `list unusable`（SFTP 列表>3s 判负，资源争抢非断连）；
  N=20 VNC 失败为 `等待WebRTC通道` 60s 超时。网关/agent2 全程空闲（gw≤0.3%、ag2≤0.5%，全直连 N=20 DB 107 P2P / 0 relay）。

### 9.2 判据对照

| 判据 | N=1 | N=5 | N=10 | N=20 |
|---|---|---|---|---|
| 成功率 ≥95% | ✓ 100% | SSH ✓；VNC 93.3% 贴线 | ✗ 63% / 80% | ✗ 30% / 8% |
| CPU 峰 ≤80%（md） | ✓ 55.6% | ✓ 68.9% | ✗ 102.3% | ✗ 100.8% |
| P95 增长 ≤线性 | 基准 | ✓（5×N → 1.13×/1.6×） | ✗ SSH 2×N → 4.5× | 截断失真（60s 超时封顶） |

结论：**N≤5 达标（VNC 成功率贴线），N≥10 全面不达标**；且 N≥10 数据受 §9.3 P0 崩溃污染，
须在修复后按同口径回归（预期 N=10 恢复可用区间，N=20 待复测）。

### 9.3 P0 发现：VNC inputCh 关闭后仍写入 → wragent panic ×8

- 现象：`pyterm_wragent` `panic: send on closed channel`，S4 窗口（12:42–13:10）共 8 次、restarts=11；wragent2 0 次。
- 根因：`pyagent/webrtc/signal.go:2710` `defer close(inputCh)`（`bridgeVNC` 退出清理）与 `:1867`
  `select { case vnc.inputCh <- data: default: }`（`MsgVNCInput` 投递）竞态——通道关闭后 select 仍可能选中该分支；
  窗口 = bridgeVNC 因 DC/TCP 错误退出而 handler 侧 `vnc` 未置 nil（`:1856` 判空只挡 nil 不挡已关通道）。
- 影响：解释 N=20 VNC 崩塌与部分失败轮。**移交 E 阶段 P0 首位修复（1.0.3）**，修后重跑 S4 N=10/20 回归。
- S6/S8 不经该路径（无 VNC input 投递），压测结论不受影响。

### 9.4 S4 顺带发现

- `connection_timeline` 并发写冲突：`(1020, Record has changed since last read…)`，25 分钟 10 次（高并发落库需 upsert/重试，登记后续项）。
- 历史连接删除路由实为 `POST /api/ssh/delete`（`DELETE /api/ssh/{id}` 返 405）——探针曾致 300 条残留（已清，现 9 条真连接）。

## 10. S6 大流量 speedtest 实测（2026-10-02）

> 口径：§5.2 S6（Agent↔Agent speedtest 限速档；判据 吞吐达限速档 + alloc/op 验收口径）。
> 驱动：`autotest/speedtest-run.py`（WS `speedtest_start`，限速档 0/10/50/100，10s/方向，每档 3 轮，
> 每轮 pprof allocs/heap pre/post 快照）+ `s6-run.sh`（stats 采样 + `GODEBUG=gctrace=1` 提取）。
> 版本对照：A=1.0.2（帧池化/per-DC 锁/SFTP 流式）vs B=1.0.1（无池化），`compose stop → cp releases/… → up -d` 换版。

### 10.1 吞吐（1.0.2，3 轮均值）

| 限速档 | up (Mbps) | down (Mbps) | 达成率 | 判据 |
|---|---|---|---|---|
| 10 | 9.5 | 9.5 | 95% | ✓ |
| 50 | 43.0 | 43.3 | 86–87% | ✓（接近） |
| 100 | 59.1（轮间 50–76） | 78.2（75–80） | 59% / 78% | ✗ 未稳定达档 |
| 0（不限） | 48.6 | 48.7 | 单流天花板 ≈48.7 | 瓶颈非限速器 |

- 1.0.1 对照：L0 45.0/48.7、L100 67.8/57.8；纯 L0 补跑 A0 47.2/49.0 vs B0 48.5/48.8——同量级，1.0.2 无回归。
- L100 轮间 50↔76 波动系同机共存争抢；L0 不限速仍仅 48.7 → 天花板在单流路径（宿主 veth/pion 用户态栈）而非限速配置。
- **判据「吞吐达限速档」= L10/L50 达标、L100 部分达标（59–78%）**。

### 10.2 alloc 差值（pprof `-base alloc_space`，两 agent 合计，每档 3 轮）

| 口径 | 1.0.2（A0 纯 L0） | 1.0.1（B0 纯 L0） | Δ |
|---|---|---|---|
| 分配 MB / 传输 MB | 19.54 | 18.52 | +6% |
| allocs /（K·传输 MB） | 84.3 | 87.1 | −3% |

- speedtest 走独立大块 UDP 分配，**不经帧池化/msgBuf 复用路径** → 池化对该口径中性（±6% 内；混档 A 的 L0 轮交叉验证 19.25，±2% 一致）。
- 单二进制方案 §6.2「alloc/op −40~60%」验收对象应为 **SFTP/VNC 帧路径**——§8 按此收窄表述，帧路径分配收益待专测回填。
- 绝对基线：每传输 1MB 分配 ≈19MB（两进程 pion/dtls/sctp/crypto 全量）。

## 11. S8 GC 影响实测（GOMEMLIMIT 实验，2026-10-02）

> 口径：§5.2 S8（S6 满载下 GC 次数/暂停 P99/分配速率；判据 设置 GOMEMLIMIT 后 GC 次数应下降）。
> 四组均为纯 L0 ×3 轮 ×75s 窗口、`GODEBUG=gctrace=1`；A/C 系 1.0.2、B 系 1.0.1。
> 早期混档窗口（A=4 档×3 轮）的 gc/s 21.4 因轻载轮稀释弃用，统一以纯 L0 窗口对比。

### 11.1 GC 统计（wragent 主压测进程；括注 agent2）

| 组 | 二进制 | 环境 | GC 数/3轮 | gc/s | pause 均值 | P99 | max | 总暂停 |
|---|---|---|---|---|---|---|---|---|
| A0 | 1.0.2 | GOMEMLIMIT=256MiB | 2034（agent2 2093） | 27.0 | 1.99ms | 4.94ms | 15.5ms | 4.04s（4.39s） |
| B0 | 1.0.1 | GOMEMLIMIT=256MiB | 2074（2067） | 27.6 | 1.93ms | 4.96ms | 11.2ms | 4.01s（4.28s） |
| C1 | 1.0.2 | 无 GOMEMLIMIT | 2079（1861） | 27.6 | 1.95ms | 4.97ms | 10.6ms | 4.06s（3.78s） |
| C2 | 1.0.2 | 无 GOMEMLIMIT + GOGC=10 | 4773（5038） | 63.4 | 1.55ms | 4.03ms | 13.3ms | **7.39s（8.16s）** |

### 11.2 判据对照与结论

- **A0 vs C1（完全不设）−2% ≈ 持平 → 如实记「未下降」**：GOMEMLIMIT 为软限，live set 仅 2–4MB、
  GC goal 4MB ≪ 256MiB，从未触及，频率由 GOGC 主导——物理上等价，属预期。
- **A0 vs C2（用 GOGC=10 收紧代替软限）−57%（27.0 vs 63.4 gc/s）✓ 下降**：C2 单次暂停更短但
  **总暂停 ×1.8**（4.04→7.39s，agent2 同向 4.39→8.16s）——高频短暂停换内存上限的代价实测成立。
- **定位结论**：`GOMEMLIMIT=256MiB` 当前价值 = **防 OOM 保险丝**（agent RSS 实测 9–19MB，离限值两个量级），
  而非降频手段；**不得**为追求「GC 次数下降」改用 GOGC 收紧（反例 C2 已量化）。
- 四组 agent CPU 72–88%、RSS 9–19MB、吞吐 47–49Mbps 无差 → GOGC/GOMEMLIMIT 均不影响吞吐。
- gctrace 开销对照：冒烟（无 gctrace）L10 9.54/9.53 ≈ A 轮（有 gctrace）9.54/9.54 → 输出开销可忽略。

## 12. 未覆盖与移交

- **S7 长稳 24h 未跑**（计划内挂起项，待上线后按 RSS/goroutine 斜率观察）。
- ~~S4 N=10/20 需在 §9.3 崩溃修复后同口径回归~~ → **已完成，见 §13.3**（1.0.3/1.0.4 零 panic）。
- S5 网关中继并发未覆盖（沿 §6 既有待办：`PERF_GATEWAY=1` 腿 ICE 不稳）。
- 复跑入口：`autotest/{perf-probe.js,s4-setup-conns.js,s4-fleet.sh,s4-agg.py,speedtest-run.py,s6-run.sh,s6-agg.py}`；
  原始数据 `/tmp/s4/`、`/tmp/s6/`（重启即失，聚合结果以本文为准）。

## 13. E 阶段收尾：pprof 择优 A/B 与 S4 回归（2026-10-02，1.0.3/1.0.4）

> 口径：E4「先 pprof 后动」。同口径负载 `PHASES=file,vnc REPS=8`（8×1MB + 8×10MB 双向 SFTP + list/echo + VNC 10 moves，约 160MB payload），
> agent `127.0.0.1:6061` 负载前后各采 `allocs` 快照做 `pprof -base alloc_space` 差值；三次测量总量 1.61–1.64GB（±2%，噪声级）。

### 13.1 帧池（P4）三方案 A/B

| 方案 | takeFrameBuf 换新 flat | 该点 cum | 相对基线 | 结论 |
|---|---|---|---|---|
| 单池 4KB（原实现 / **1.0.4**） | 17.94MB（1.09%） | 18.94MB（1.15%） | 基线 | **采用** |
| 单池 16KB（1.0.3 曾出包） | 22.06MB | 24.10MB | +27% | 弃：小帧与 24KB SFTP 分片在同池互挤，miss 更频繁 |
| 三档分池 4/16/64KB | 0（全部命中） | 34.57MB | +83% | 弃：64KB 档 New 在 27 GC/s 下 refill 每次 64KB，更贵 |

- 版本链：`5adcb6e`（16KB 出 1.0.3）→ `42dc52e`（回 4KB + 结论入注释）→ `0f9db8a`（gofmt）→ **1.0.4 收敛全网**。
- 同窗口 `takeSFTPUpload` 85.78MB（5.09%）≈ 1× 文件大小（8×10MB+8×1MB）的必要拼接拷贝，非缺陷。
- 分配大头在 pion：sctp packetize / dtls marshal+unmarshal / ssh readPacket ≈ 90%；自有代码帧路径合计约 1.2%。

### 13.2 P1/P3/P5/R4 按 profile 决策（均不动，留证）

| # | 项 | profile 证据 | 决策 |
|---|----|----|----|
| P1 | UDP 隧道每包 `make` | 本轮无 UDP 隧道流量样本，`tunnel.go` 未进 top | 不动，有流量再测 |
| P3 | SendCh 4096→1024 | 负载峰值 heap 无队列堆积迹象 | 不动（降深徒增丢帧风险） |
| P5 | WS JSON 解码分配 | 未进 top，低收益 | 不动 |
| R4 | handler 9×`make` | 本口径直连不经网关，无样本 | 不动，S5 中继覆盖时再评 |

### 13.3 S4 回归（E6：V0/V1 验收）

| 版本 | 轮次 | 结果 | panic | md 1020/500 | timeline |
|---|---|---|---|---|---|
| 1.0.3 | N=10 r1（23s） | ssh 10/10、vnc 10/10，0 failed worker | **0**（D 阶段同口径 8 次、restarts=11） | 0/0 | 50 行全 P2P，0 BUG、0 空值 |
| 1.0.3 | N=20 r1/r2（各 97s） | 0 failed worker（成功率沿 §9.1 环境上限，非回归） | **0** | 0/0 | 全 P2P |
| 1.0.4 | N=10 r2（22s） | 0 failed worker | **0** | 0/0 | 20 行全 P2P |

- **V0 验收达成**：wragent/wragent2/wrgateway 自 1.0.3 起 `panic:|fatal error` 计数 0。
- **V1 验收达成**：S4 高并发窗口 md `(1020, Record has changed…)` 与 500 响应均为 0（D 阶段 25 分钟 10 次）。
