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
