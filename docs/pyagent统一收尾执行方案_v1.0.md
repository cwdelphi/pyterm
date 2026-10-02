# pyagent 统一收尾执行方案

> 版本管理：本文档为主方案，改动须更新版本历史表；子方案（若有）以 `_vX.Y` 后缀独立成文并在此登记。

## 版本历史

| 版本 | 日期 | 变更 | 作者 |
|------|------|------|------|
| v1.0 | 2026-10-02 | 初版定稿：7 阶段串行（A0→A1→B→C→D→E→F）；确认项①串行执行②样例 JSON 采用静态双卡（Agent+网关，无后端接口） | pyagent 收尾会话 |
| v1.1 | 2026-10-02 | B 项落地：四修复（残留重置/快速多轮探测/补报3次退避/服务端 pending 合并）上线并真实浏览器回归通过；**H1（网关 reportConnType 静默退出）与 H4（enqueue 满丢弃）移交 E 阶段**（与 handler.go 拆分同批出 1.0.3，避免二次部署） | pyagent 收尾会话 |
| v1.2 | 2026-10-02 | D 阶段 S4/S6/S8 完成：数据入 `docs/wr合并性能实测_v1.0.md` §9–§12、单二进制方案 §8.1 回填；**新 P0（E 首位）：VNC inputCh 关闭后写入致 wragent panic ×8**（`signal.go:2710` vs `:1867`，修后重跑 S4 N=10/20）；另登记 md `connection_timeline` 并发写 1020 冲突；S7 长稳挂起、S5 未覆盖 | pyagent 收尾会话 |
| v1.3 | 2026-10-02 | E0–E6+F 收尾完成：T1/V0/V1/H1/H4/R1/R2 全量入 1.0.3（R3 前序已入）；E4 按 pprof 三方案 A/B 定案帧池回 4KB（16KB +27%、分档池 +83%）、P1/P3/P5/R4 profile 无证据不动；出包 1.0.3→1.0.4，全网 7 agent+网关 version=1.0.4 / needs_upgrade=False；S4 N=10/20 回归零 panic（D 阶段 8 次）、md 1020/500 归零；TCR 双架构镜像 ccr.ccs.tencentyun.com/cw2026/pyagent:{1.0.4,latest} 公开可拉；gitcode/github 推送随收尾 commit | pyagent 收尾会话 |

## 待办全景与优先级

| # | 事项 | 类型 | 优先级 | 前置 | 验收标准 |
|---|------|------|--------|------|----------|
| A0 | 删 `pathcache.go` 探针 → 全量门禁（server 包 flake 复验） | 阻塞 | P0 | 无 | `gofmt -l` 空 + build/vet/test 11 包绿，server 包连跑 3 次通过 |
| A1 | 出包 1.0.2 → `.env` LATEST_*=1.0.2 → force-recreate → 推 7×agent 升级 | 部署 | P0 | A0 | 容器 env=1.0.2、DB 7 agent version=1.0.2 / needs_upgrade=False、下载路由 200/206 |
| B | DC 链路空值（"-")排查 + 修复 | 排查+修 | P0 | 无 | 诊断面板 `webrtc_path=none` 新增记录归零 |
| C | 下载页样例 JSON（可复制/可下载、卡片式、UX 合规） | 前端 | P1 | 无 | 三卡渲染、复制/下载可用、`npm run build`+vitest 60/60、i18n 键数同步 |
| D | 压测 S4/S6/S8 + pprof 基线 → §8 回填 | 性能 | P1 | A1 | 吞吐/内存数据入 `docs/wr合并性能实测_v1.0.md` §8 |
| E | 重构 R1/R2/R3 + T1 GOMEMLIMIT 自适应 + P 项按 profile 择优 | 深度 | P2 | D | 每批过门禁、压测对比回填 |
| F | arm64 buildx 镜像 + 中文 commit 推 gitcode（github 走脱敏脚本） | 收尾 | P2 | A1/D | 镜像可拉取、origin 有新 commit |

## 阶段 1→7 执行顺序

| 阶段 | 步骤 | 关键动作 | 验收 |
|------|------|----------|------|
| 1 | A0 门禁收口 | 删 `[PCDBG]` 探针 → rsync → 远端全量门禁 `gofmt -l . && go build ./... && go vet ./... && go test ./...`，server 包连跑 3 次验 flake 根除 | 11 包全绿、无探针残留 |
| 2 | A1 出包部署 | commit（中文）→ `build-pyagent.sh` 出 1.0.2 → `.env` 四键 LATEST_*=1.0.2 → `docker compose up -d --force-recreate md wragent wragent2 wrgateway` → admin 短票推 7×agent 升级 | 线上 7 agent=1.0.2、needs_upgrade=False、下载 200/206 |
| 3 | B DC 排查修复 | V1 DB 查 1546/1548/1550/1562 → V2/V3 md+gateway 日志 → V4 浏览器复现 → 按归因矩阵修（预期核心 H1/H2/H3） | 诊断面板 `none` 新增记录归零 |
| 4 | C 下载页样例 | `AdminPanel.vue` downloads 区新增卡2/卡3（静态 JSON、`location.host` 填 server_url、复制+下载、复用 `download-card` 与 `copyCommand` 交互）→ `zh-CN.json`/`en.json` 加键（780/780 保持相等）→ `npm run build` + vitest | 三卡渲染、复制/下载可用、键数同步 |
| 5 | D 压测+pprof | 探针 `/tmp/opencode/perf-probe.js` rsync `autotest/` → S4/S6/S8 → 同期采集 `127.0.0.1:6060/debug/pprof/{profile,heap}` 基线 → 回填 §8 | 数据入性能实测文档 §8 |
| 6 | E 优化（profile 驱动） | 无风险先行：R1 signal.go 拆域 / R2 handler.go 拆分 / R3 合并双 `classifyConnType` / T1 `GOMEMLIMIT` 自适应（env 优先，cgroup→RAM×0.5 上限 2GiB，启动日志打印）/ GOMAXPROCS 只验证打日志；P1–P5 按 profile 择优 | 每批过门禁、对比回填 |
| 7 | F 收尾 | arm64 buildx 镜像 → gitcode 直推 → github 走 `scripts/publish-github.sh` | 镜像可拉、origin 有 commit |

## B 项：DC 链路空值归因矩阵

### 两条上报链路

| 模式 | 检测方 | 路径 | 落库点 |
|------|--------|------|--------|
| 直连 | 浏览器 `detectConnType`（connected 后等 1.5s，最多 3 次 getStats） | 首报随 `/report`；晚到走 `POST /api/timeline/webrtc-path` 补报（仅重试 1 次） | `connection_timeline.webrtc_path` |
| 网关 | 网关 `reportConnType`（选中 pair 后稳定 1.5s，10s 上限；session Done/PC 关闭即静默 return） | WS `connection_type` → 浏览器同上补报 | 同上（另经 `[AS-WS]` 落 `agents.conn_type`） |

### 假设矩阵

| 假设 | 命中行 | 依据 | 验证 | 修复方向 |
|------|--------|------|------|----------|
| H1 网关静默退出（会话 <1.5s settle，session.Done 先触发→一条没发） | 1562（217ms 网关） | `reportConnType` 注释明示静默退出 | gateway 日志 grep `[GA-DC] conn_type` | 首拿 pair 立即首报，稳定期发修正；关闭前 best-effort flush |
| H2 浏览器检测静默放弃（1.5s 后 `!pc`/非 connected → 不发不补） | 1550/1548/1546（直连） | `detectConnType` 两处 return 无兜底 | console 复现看 `BA-DC` 日志 | 放弃前尽力一次；dcOpen 时刻即取一次 stats |
| H3 补报 404（记录未写入/user_id 不匹配，仅重试 1 次） | 4 行均可能 | `/webrtc-path` 记录不存在即 404 | md 日志 grep 4xx | 3 次退避；**服务端 pending 合并**（记录未到先挂起，`/report` 到达时套用） |
| H4 网关 enqueue 满丢弃无重试 | 1562 可能 | `dropped (send buffer full)` 分支 | gateway 日志 grep `dropped` | 关键小消息丢弃进重试队列 |
| H5 行 1546 目标 `:0` 字段异常 | 1546 | host 空+port 0 | DB 查原始值 | 另案 |
| H6 10s 超时判 BUG 前会话已关 | 与 H1 叠加 | 10s deadline | 同 H1 | 同 H1 |

推荐修复组合：服务端 pending 合并（根治时序竞态）+ 网关首报立即发 + 浏览器放弃前尽力一次 + 补报 3 次退避。

### 执行结果（v1.1，2026-10-02 已上线）

| 修复 | 文件 | 内容 |
|------|------|------|
| 残留重置 | `web/src/utils/webrtc.ts` `connect()` | 重置 `diag.connTypeDetected`——旧逻辑漏清，direct 行会错标上一次 gateway 的测量值（1554/1560 污染实锤） |
| 快速多轮探测 | 同上 `detectConnType` | 固定等 1.5s → 立即/500ms/1s/2s 四轮；`state≠connected` 也试 getStats；全程 unknown 且已断开则静默不误报 BUG |
| 补报退避 | 同上 `reportWebRtcPath` | 重试 1 次 → 3 次指数退避（1.5s/3s），404/409 与网络错误均重试 |
| pending 合并 | `app/api_timeline.py` | `/webrtc-path` 记录不存在改挂 pending（TTL 5min/上限 500）返回 200；`/report` 落库时该列仍空则套用 |

回归证据：直连短会话（276ms，修复前同类 1550 为空）→ `webrtc_path=P2P`；网关会话 → `[GA-DC] conn_type P2P` + 记录 P2P；pending→report 闭环（relay 套用入库）；vitest 60/60 + `npm run build` + 线上 bundle `main-C76DaZaa.js` 已含新代码。

## C 项：下载页样例 JSON 与 UX

### 页面结构（沿用 `download-card`，`downloads-grid` 自适应扩列）

| 卡片 | 内容 | 操作 |
|------|------|------|
| 卡1（现有） | pyagent 二进制 | Linux amd64 / Linux arm64 / Windows amd64 |
| 卡2（新增） | Agent 配置样例 `config.json` | 复制 + 下载 config.json |
| 卡3（新增） | 网关配置样例 `config-gateway.json` | 复制 + 下载 config-gateway.json |

### 样例内容（占位符；`server_url` 用 `location.host` 自动填充；**绝不嵌真实 token**）

```json
{
  "mode": "agent",
  "server_url": "wss://<你的门户>/api/ws/webrtc",
  "agent_id": "<AGENT_ID>",
  "auth_token": "<AUTH_TOKEN>"
}
```

```json
{
  "mode": "gateway",
  "listen": ":5599",
  "tls_cert": "/etc/pyagent/ssl/cert.pem",
  "tls_key": "/etc/pyagent/ssl/key.pem",
  "server_url": "wss://<你的门户>/api/ws/webrtc",
  "gateway_id": "<GATEWAY_ID>",
  "gateway_token": "<GATEWAY_TOKEN>",
  "log_level": "info",
  "heartbeat_interval": 30,
  "read_timeout": 60,
  "write_timeout": 60
}
```

### UX 落地

| 原则 | 落地 |
|------|------|
| 一致性 | 复用 `download-card` 头/身/按钮；与卡1 同栅格 |
| 可扫描 | icon+标题+tag 头部；一句描述；操作右对齐 |
| 反馈 | 复制→短暂「✓ 已复制」（复用既有 `copyCommand` 交互模式）；hover/focus 复用 accent |
| 容错 | 占位符 `<...>` 高亮；说明“从管理后台 Agent 详情获取 id/token” |
| 信息层级 | JSON `pre` 限高滚动等宽；高级字段（ice_*）默认收起 |
| 响应式 | `grid auto-fit minmax(320px,1fr)`；3 卡放宽 max-width 640→960px |
| i18n | 新键入 `zh-CN.json`/`en.json`（json 非 ts），780/780 保持相等 |
| 安全 | 静态占位符，对照 `pyagent/config.json` 实 token 教训 |

改动文件：`web/src/components/AdminPanel.vue` + `web/src/i18n/zh-CN.json` + `web/src/i18n/en.json` → `npm run build` + `npx vitest run`。

## E 项：架构/性能/内存/并发优化池

### 新增 P0（D 阶段压测发现，v1.2 登记，1.0.3 首位）

| # | 项 | 证据 | 方案 | 验收 |
|---|----|------|------|------|
| **V0** | **VNC inputCh 关闭后仍写入 → `panic: send on closed channel`** | S4 窗口 wragent 8 次 panic、restarts=11（wragent2 0 次）；`signal.go:2710` `defer close(inputCh)` 与 `:1867` `select case vnc.inputCh <- data` 竞态，`:1856` 判空不挡已关通道 | ~~bridgeVNC 退出置 nil/加关标志~~ **已落地：删除 `close(inputCh)`**（消费者本由 done/ctx.Done 终止不依赖 close，通道随 session GC；残留 ≤64 帧无害）——最小 diff 根除 panic | ~~S4 N=10/20 同口径回归零 panic；~~ 门禁已绿；**待 1.0.3 出包后跑 S4 N=10/20 回归** |
| V1 | md `connection_timeline` 并发写 `(1020, Record has changed…)` | S4 25 分钟 10 次 | 落库 upsert/重试（1020 冲突静默重试 1 次） | 高并发 S4 下 1020 归零 |

> S6/S8 不经 inputCh 路径，压测结论不受 V0 污染；V0 修复优先于一切 R/P 项。

### 现状体检

| 维度 | 现状 | 评价 |
|------|------|------|
| 文件规模 | signal.go 3017 / handler.go 1360 / tunnel.go 977 / peer.go 502 / main.go 405 | 单文件过大，可拆 |
| 资源池 | `dcFramePool` 1 处已做；UDP 隧道每包 `make`；handler 每条消息 `make([]byte,1+n)`×9 | 有优化点 |
| 定时器/日志/索引/锁 | #13–#18 全部完成 | 已收敛 |
| SFTP | #7 流式 + #10 前缀帧零二次拷贝 | 已优化 |
| 可观测 | pprof 已暴露（compose `PYAGENT_PPROF_ADDR` 6060） | 具备测量条件 |
| 内存策略 | 容器 `GOMEMLIMIT=256MiB` 固定；裸机无限制；GOMAXPROCS Go1.27 cgroup-aware 默认 | 缺口在 GOMEMLIMIT 自适应（S8 实测软限未触发、RSS 9–19MB——自适应主要补裸机场景，勿改 GOGC） |
| 重复实现 | `classifyConnType` handler.go 与 peer.go 各一份 + 浏览器 `parseConnType` | 应合并 |

### 重构机会（低风险纯拆分先行）

| # | 项 | 收益 | 风险 | 优先级 |
|---|----|------|------|--------|
| R1 | signal.go 按域拆文件（sftp/vnc/ssh/speedtest/tunnel/frames/ws） | 可维护性 | 低（纯移动） | P2 |
| R2 | handler.go 拆（session/diag/conn_type/写循环） | 同上 | 低 | P2 |
| R3 | 合并双 `classifyConnType` → `webrtc.ClassifyConnType` | 消除规则漂移 | 低 | P2 |
| R4 | handler 9 处 `make` → `frameJSON(v)` helper/池化 | 少量分配 | 低 | P3 |

### 性能/内存/并发（先 pprof 后动）

| # | 项 | 现状 | 方案 | 预期 |
|---|----|------|------|------|
| P1 | UDP 隧道每包分配 | `udpPkt{data: make}` per packet | sync.Pool 复用读缓冲+pkt | 高速隧道 GC↓ |
| P2 | 每连接读缓冲 | SSH 32KB/VNC 60KB/SFTP 24KB 每连接一次 | 合理，不动 | — |
| P3 | `SendCh 4096` 深队列 | 满时已有 drop | 按 pprof 决定降 1024 | 峰值内存可预期 |
| P4 | `dcFramePool` 初始 cap 4KB | 大帧多次增长 | New 改 16KB（视 heap profile） | 分配次数↓ |
| P5 | WS JSON 解码分配 | 每消息 Marshal/Unmarshal | RawMessage 复用（低收益） | 小 |
| P6 | 并发模型 | goroutine-per-conn，spawn 点少 | 无需 worker 池 | — |

### 硬件自适应（env 显式值永远优先）

| 旋钮 | 现值 | 方案 | 落点 |
|------|------|------|------|
| GOMEMLIMIT | 容器固定 256MiB；裸机无 | env 未设 → cgroup v2 `memory.max`（无则宿主 RAM）→ min(limit×0.5, 2GiB) 下限 128MiB → `debug.SetMemoryLimit`，启动日志打印 | main.go 启动早期 |
| GOMAXPROCS | Go1.27 cgroup-aware 默认 | 只验证不改：启动日志打 `runtime.GOMAXPROCS(0)` vs `nproc`（本机 4 核），结论入文档 | main.go 日志 |
| 隧道 UDP SO_RCVBUF | 默认 | 按 netinfo 已有 `Scan/attachStats/PushStats` 速率分档；**排在 P1/P4 后视 profile 决定** | tunnel.go |
| 背压阈值 | 64KB 固定 | 协议约束，压测显示瓶颈才动 | — |
| 可观测 | — | 启动日志 `tune: GOMEMLIMIT=.. GOMAXPROCS=..` | main.go |

### 验证闭环

| 手段 | 用途 |
|------|------|
| gofmt + build/vet/test | 每批门禁 |
| pprof CPU/heap（D 基线 vs E 改进后） | 决定 P1–P5 做不做、收益多少 |
| 压测前后吞吐/内存对比 | 回填 §8 收益置信度 |
| 浏览器/网关复现 + DB 统计 | B 项回归 |

## 风险控制

- 阶段 6 重构按 R1/R2/R3/T1 拆独立 commit，单批过门禁再下一批，便于回退。
- 前端任何改动必带 `npm run build`（否则线上 bundle 不变）。
- 阶段 1 若 flake 仍复现，先单独收口 pathcache 再往下走，不带病部署。
- 远程 rsync 禁 `--delete`、路径写全、必须带 sshpass `-e` 参数；push 仅 gitcode，github 走 `scripts/publish-github.sh`。

## 确认项记录

| # | 问题 | 决议 | 时间 |
|---|------|------|------|
| 1 | 执行顺序：串行 or 并行 | 按推荐串行 1→7 | 2026-10-02 |
| 2 | C 样例范围：静态 or 个性化下载 | 按推荐静态双卡（无后端接口） | 2026-10-02 |

## 收尾执行结果（v1.3 登记，2026-10-02）

### 阶段状态

| 阶段 | 状态 | 关键产出 |
|---|---|---|
| E0 T1 | ✅ `380c98a` | GOMEMLIMIT 自适应（env 显式值优先，cgroup→宿主 RAM 取来源×0.5，上限 2GiB 下限 128MiB）+ `[TUNE]` 启动单行；GOMAXPROCS 只打日志不改值 |
| E1 V1 | ✅ `f27b895` | `/webrtc-path` 改单语句 UPDATE 根除 read-then-modify，1020/1213 静默重试 1 次、失败回落 pending；pytest 29 failed/153 passed（WP04 红转绿，无回归） |
| E2 H1+H4 | ✅ `7311bf0` | reportConnType 首拿 pair 立即首报、分类变化才发修正、close 前 best-effort 补发、10s 未选中才 BUG；sendConnType 有界重试 50ms×20；`server/conn_type_test.go` 两回归（-count=3 无 flake） |
| E3 R1 | ✅ `0eb8ca7` | `webrtc/signal.go` 3022→984 行，拆出 signal_{speedtest,sftp,tunnel,vnc,ssh,dc}.go：整块移动 + 声明行集合校验 + goimports，门禁全绿 |
| E3 R2 | ✅ `9d99dca` | `server/handler.go` 1341→219 行，拆出 handler_{pump,bridge,connect,auth,conn}.go，同上纯移动 |
| E3 R3 | ✅ `6f46921` | 唯一实现 `webrtc.ClassifyConnType`（前序已入） |
| E4 P 项 | ✅ `5adcb6e`→`42dc52e`→`0f9db8a` | pprof 三方案 A/B 后帧池回 4KB；P1/P3/P5/R4 按 profile 不动（性能实测文档 §13.2 留证） |
| E5 出包部署 | ✅ | 1.0.3 出包推送 → 1.0.4 收敛帧池回退；`.env` LATEST_*=1.0.4；DB 7 agent + 网关 version=1.0.4、needs_upgrade=False；`[TUNE]` 日志确认 env 优先 |
| E6 S4 回归 | ✅ | N=10（23s）ssh 10/10 + vnc 10/10、N=20（97s）×2 轮、1.0.4 复验 N=10：全部 0 failed worker、**三容器 panic=0**（D 阶段 8 次）、md 1020/500=0、timeline 全 P2P |
| F 镜像 | ✅ | `ccr.ccs.tencentyun.com/cw2026/pyagent:{1.0.4,latest}` manifest 含 linux/amd64+linux/arm64，TCR 设公开、匿名 pull 验证通过（Dockerfile 无 COPY/RUN 故免 QEMU；binfmt 装 arm64；buildx 容器拉 Docker Hub 超时改走默认 builder 的 daemon mirror） |
| F 推送 | ✅ | gitcode 直推（中文 commit）+ github 走 `scripts/publish-github.sh` |

### P 项决策（profile 驱动，先测后动）

- **做了**：P4 帧池——三方案同口径 A/B（单池 4KB 18.94MB / 单池 16KB 24.10MB / 三档分池 34.57MB，总量 1.6GB 噪声内），取最优 4KB；1.0.3 曾带 16KB 出包，1.0.4 回退收敛，避坑结论写入 `signal_dc.go` 池上方注释。
- **不做**：P1（本轮无 UDP 隧道流量样本）、P3（负载峰值 heap 无 SendCh 堆积证据，降深徒增丢帧风险）、P5（低收益未进 top）、R4（直连口径不经网关，无样本，留待 S5 中继覆盖时评估）。
