# wr 合并为单二进制方案 v1.0 —— wrgateway 并入 wragent，JSON `mode` 决定启动角色

| 项 | 值 |
|---|---|
| 版本 | **v1.0** |
| 日期 | 2026-10-01 |
| 状态 | 已批准，执行中 |
| 基线 | wragent **2.3.8** / wrgateway **1.1.8** / `563291c`（工作区 clean） |
| 范围 | `wragent/` + `wrgateway/` + `docker-compose.yaml` + `scripts/` + `app/auth_api.py` + `docs/` |
| 承接 | `docs/architecture_optimization_v1.0.md` §四 Phase 2（把 wrgateway 拆出去的方案）—— **本方案是其逆向操作** |
| 关联 | `docs/传输优化方案_v2.0.md`、`docs/部署方案_v1.1.md`、`docs/传输优化_ICE优化与接口过滤方案_v2.0.md` |

## 修订记录

| 版本 | 日期 | 变更 | 状态 |
|------|------|------|------|
| v1.0 | 2026-10-01 | 初版：只读勘察（硬件/代码/镜像/内存隐患/性能基线）+ 合并方案 + Phase 0–7 执行计划 | 已批准，执行中 |
| v1.0-α | 2026-10-01 | Phase 1+2（commit `6a05846`）完成：包归一、单 Config+`mode`、`github.com/ppy-tools/wr`、双入口 main、`build-wr.sh` | 已执行 |
| v1.0-β | 2026-10-01 | Phase 3（commit `d7bd23a`）完成：P0#1–#5、P1#6/#9、P3#19、P2#20 全部落地并双端通过 | 已执行 |
| v1.0-γ | 2026-10-01 | Phase 4+5（commit `61cba88`/`57d2b56`）：镜像定为**纯基座 alpine:3.20 12.1MB**，最终命名 `pyterm/wragent:latest`，三实例已切换（详见 §9 修订注记） | 已执行 |
| v1.0-δ | 2026-10-01 | Phase 6 实测回填 → 独立文档 `docs/wr合并性能实测_v1.0.md`；发现既有下载卡停 flake（合并前后均复现） | 已执行 |
| v1.0-ε | 2026-10-01 | Phase 7 最终命名 **`wr` → `pyagent`**：module `github.com/ppy-tools/pyagent`、镜像 `pyterm/pyagent:latest`、构建脚本 `scripts/build-pyagent.sh`、配置/挂载/env 全换 pyagent 路径（旧路由 `/api/deploy/wragent|wrgateway` 与 env `WR_MODE` 兼容保留）；**版本基准 1.0.1，只允许 1.0.x 第三位递增**；三实例已切换并冒烟通过（commit `977881a`/`2b5c2a0`） | 已执行 |

## 用户决策记录

| # | 决策点 | 结论 |
|---|--------|------|
| C1 | 版本线 | **合并为单一版本线**（一个二进制 = 一个 `LATEST_WR_VERSION`），同步改 `app/auth_api.py`、build 脚本、README、部署文档 |
| C2 | 基座镜像（scratch / distroless / alpine / debian） | 见 **§0 选型**，结论 **`alpine:3.20`**（`nsenter` + shell 是硬约束，scratch/distroless 不满足） |
| C3 | 压测 | **允许**在 gx.example.com 上执行 §5.2 多场景压测，多轮取分位数并记录干扰进程 |
| C4 | 兼容窗口 | **直接硬切**，不做新旧二进制并行过渡；部署顺序 网关 → Agent → 前端，回滚 = `git revert` + 同序重部署 |

---

## §0 基座镜像选型（scratch vs distroless 差异）

### 0.1 两者的实质差异

| 维度 | `FROM scratch` | `gcr.io/distroless/static` |
|---|---|---|
| 基础层 | **0 字节** | ≈2 MB（`/etc/passwd`、`/etc/group`、`/etc/nsswitch.conf`、CA bundle、nonroot 用户） |
| CA 根证书 | **必须自己 `COPY ca-certificates.crt`**（182,140 B / gzip 106,682 B） | 内置 `/etc/ssl/certs/ca-certificates.crt` |
| 默认用户 | **root (uid 0)** | **`nonroot` (uid/gid 65532)** |
| `/etc/passwd` | 无 → `os/user` 类查询失败 | 有 |
| shell | **无** | **无** |
| tzdata | 无 | 无（`base` 才有） |
| OS 层 CVE 面 | 0 包 | 0 包（同样只有 CA/passwd） |
| 外部供应链依赖 | **无**（纯 `COPY`，无需拉取任何 base） | 依赖 `gcr.io` 可达性（国内需 mirror） |
| 成品体积（本项目） | **≈7.2 MB** pull | ≈9.1 MB pull |
| 差距 | — | 多 2 MB 换来「内置 CA + 非 root + passwd」 |

### 0.2 本项目的硬约束（决定两者都不能用）

实测证据：

| # | 约束 | 位置 | scratch | distroless/static |
|---|---|---|---|---|
| 1 | **`nsenter`**：`HOST_CONSOLE=1` 宿主控制台模式靠 `exec.LookPath("nsenter")` 探测 | `wragent/webrtc/localterm.go:26` | ❌ 无 | ❌ 无 |
| 2 | **`/bin/sh`**：非 hostMode 回退 `exec.Command(shell)`，`LookPath("/bin/bash")` 失败后落到 `/bin/sh` | `localterm.go:47-51` | ❌ 无 | ❌ 无 |
| 3 | **shell 启动命令**：compose `command: bash -c "cp /app/wr /usr/local/bin/wr && exec …"` | `docker-compose.yaml:187` | ❌ 无 | ❌ 无 |
| 4 | 安装脚本同样用 `sh -c "cp … && exec"`（三件套宿主控制台部署） | `app/auth_api.py:1016` | ❌ | ❌ |
| 5 | **`PERSIST_BIN` 热升级**要 `os.Rename(os.Args[0], backup)`，`os.Args[0]` 不能是 bind-mount 文件 → 必须先 `cp` 到容器内路径 | `signal.go:1203,1210,1254` / `handler.go:638,645,689` | ❌ 需 shell 配合 | ❌ |
| 6 | **TZ=Asia/Shanghai**：代码零 `time.LoadLocation`，靠 `time.Local` 读 tzdata；scratch/distroless **均无** `/usr/share/zoneinfo` → **静默降级 UTC，日志时间差 8 小时** | `docker-compose.yaml:184,200,216` | ❌ | ❌ |

> **约束 1/2/3/4/5 与体积无关**：要满足就必须再塞一个 busybox（+≈1 MB 且要自己维护 `nsenter`/`sh` 符号链接），那不如直接用已经验证过的基座。

### 0.3 候选基座对比

| 方案 | 基座体积 | nsenter | `/bin/sh` | CA | tzdata | 成品 pull | 结论 |
|---|---|---|---|---|---|---|---|
| **`alpine:3.20`（选定）** | 12.2 MB（rootfs 实测 8.1M） | ✅ `/usr/bin/nsenter`（busybox 1.36.1） | ✅ `/bin/sh` | ⚠️ `/etc/ssl/certs/` 存在但**空**，需 `COPY` 或 `apk add ca-certificates` | ❌ 需 `apk add tzdata` 或 Go `import _ "time/tzdata"` | **≈11.1 MB** | ✅ **已验证**：现有独立 `wragent` 容器就是 `alpine:3.20` + `HOST_CONSOLE=1`，`nsenter` 实测可用 |
| `FROM scratch` | 0 | ❌ | ❌ | 自己 COPY | Go `_ "time/tzdata"` | ≈7.2 MB | ❌ 违反约束 1–5 |
| `distroless/static` | ≈2 MB | ❌ | ❌ | 内置 | 同上 | ≈9.1 MB | ❌ 同上 + `nonroot` 与 `PERSIST_BIN`/`privileged` 冲突 |
| `debian:bookworm-slim` | ≈29 MB | ✅ | ✅ | ✅ | ✅ | ≈38 MB | 🟡 功能全，但比 alpine 大 3.4×；**仅 `wragent/Dockerfile` 现状在用** |
| `pyterm/go-runtime`（compose 现状） | — | ✅ | ✅ | ✅ | ✅ | **1.26 GB** | ❌ 仅为拿 shell，体积失控 |

**结论（C2）**：选 **`alpine:3.20`**，构建时 `COPY --from=builder /etc/ssl/certs/ca-certificates.crt` 或 `apk add --no-cache ca-certificates`；tzdata 用 **Go 标准库 `import _ "time/tzdata"`**（+≈450 KB raw / ≈200 KB gz，零文件依赖，属「Go 自身已有的功能」）。

**若后续要极致体积**：`scratch + busybox-static`（含 sh + nsenter applet）≈ **8.2 MB**，比 alpine 省 3 MB，但需自行维护 applet 符号链接与版本更新，**收益 3 MB 换脆弱性 → 不建议**。

---

## §1 硬件与运行环境评估【实测】

| 维度 | 实测值 | 评估 |
|---|---|---|
| CPU | **Intel N100**，4C/4T，1 桌 4 核，L3 6MB / L2 2MB / L1d 128KB | 低功耗桌面级；无超线程，4 个 Go P 调度即满载 |
| 频率 | min 700MHz / max 3400MHz，governor **`powersave`**（4 核全部） | ⚠️ **延迟波动主因**：空闲降频→突发升频，冷启动/首帧尾部拉长 |
| 内存 | 16GB（MemTotal 16151564 kB），可用 13.4GB，**Swap 2GB 已用 867MB** | 有历史内存压力痕迹；容器**未设 mem_limit、未设 GOMEMLIMIT** |
| 磁盘 | `/dev/sda2` 468G，**N900-512 SSD（ROTA=0）**，`mq-deadline`，已用 37% | SSD，pathcache 同步写不构成硬瓶颈，但仍占用建连关键路径 |
| 负载 | loadavg 0.26/0.41/0.50，`top` 80.3% idle，603 进程 / 3130 线程 / 2 zombie | 常态空闲但**非独占** |
| **嘈杂邻居** | 同机：Xtigervnc+xfce、firefox、freeswitch(3.37GB)、BT-Panel(瞬时 37%CPU)、mariadb、minio、coturn、tailscaled、buildkitd、uvicorn | ⚠️ 压测数据受干扰 → **必须 ≥3 轮取分位数并记录干扰进程** |
| nofile | 交互 shell **1024**；dockerd **524287** | 容器内并发无碍；宿主直跑 Go 压测需 `ulimit -n 65535` |
| TLS | `gx.example.com:5588` = **TrustAsia DV 公信 CA**，有效期 2026-09-27 ~ 12-26 | → 镜像**必须内置 CA 根证书** |
| Go 工具链 | 宿主 `/usr/local/go` = go1.24.4，`GOTOOLCHAIN=auto`，module cache 已含 `go1.27.0`/`go1.27.1` | go.mod 声明 `go 1.27` → 自动切换，**离线可复现** |
| buildx | 仅 `linux/amd64(+3)`，**无 arm64/QEMU 节点** | 影响见 §3.4 |

---

## §2 合并可行性技术分析【代码实证】

### 2.1 现状规模

| 项目 | Go 文件 | 代码行 | 模块 | 关键依赖 |
|---|---|---|---|---|
| wragent | 23 | **10,783** | `ppy-tools/wragent` (go 1.27) | pty / sftp / x/crypto / **pion-webrtc v4.0.8** / gorilla ws |
| wrgateway | 19 | **4,347** | `ppy-tools/wrgateway` (go 1.27.1) | golang-jwt / **pion-webrtc v4.0.8** / gorilla ws |
| 合计 | 42（含 test 17） | 15,130 | — | **pion 版本完全一致 = 合并最大有利条件** |

### 2.2 重复包归一（≈85% 可零风险合并）

| 包 | Agent 行 | GW 行 | diff | 判定 | 合并动作 | 风险 |
|---|---|---|---|---|---|---|
| `logger/logger.go` | 161 | 161 | **0**（MD5 相同） | 字节级完全相同 | 删一边 | 🟢 0 |
| `pathcache/` | 269 | 247 | 23 | **GW 是 Agent 纯子集** | 取 Agent 版 | 🟢 0 |
| `icefilter/` | 278 | 222 | 72 | Agent 超集（+59 行 `ServerOverride`/`AutoFallback`），GW 仅 3 行差异 | 取 Agent 版 | 🟡 `Summary()` 多 ` src=local\|server`，需同步匹配方 |
| `netinfo/scan.go` | 696 | 490 | 223 | Agent 大幅扩展，GW 内容 100% 被包含 | 取 Agent 版 | 🟢 `StartRefresher` 签名未变 |
| `config/` | 81+74+87 | 83 | — | **实质不同，仅 `server_url`/`log_level` 重叠** | **唯一需做字段并集的包** | 🔴 见 2.5 |
| **净删重复** | | | **≈1,010 行 / 6.7%** | | | |

### 2.3 冲突矩阵

| # | 冲突 | 严重度 | 处理 |
|---|---|---|---|
| C1 | 5 个同名目录双份 | 🔴 无法编译 | 各留 1 份 |
| C2 | 两个 `main.go` 都声明 `version/buildTime/gitCommit/init()/main()` | 🔴 符号冲突 | 合并单文件，按 mode 分支 |
| C3 | import 路径 62 处 / 24 文件 | 🟡 | 一次 sed → 新模块 `github.com/ppy-tools/wr` |
| C4 | `webrtc/signal.go:149` `init(){ go cleanupIdleSSH() }` 在 gateway 模式空转 | 🟡 | 改懒启动 |
| C5 | `webrtc/deploymode.go:7` 包级 `var DeployMode = detectDeployMode("/")` 在 main 前执行 | 🟢 | 无害，保留 |
| C6 | 全局单例（pathcache/icefilter/netinfo/logger） | 🟡 | 单进程**单 mode** 安全；**禁止 `mode=both`** |
| C9 | env 前缀 `WRAGENT_ICE_*` vs `WRG_ICE_*` | 🟡 | 双前缀兼容，不改部署脚本 |
| C13 | `auth/jwt.go` **零 import = 死代码** | 🟢 | 直接删，**移除 `golang-jwt` 依赖** |
| — | `restart_linux.go`/`restart_windows.go` 双份逐行相同 | 🟢 | 抽 `internal/selfrestart` |
| — | `wrgateway/docker-compose.yaml` healthcheck 探 `:8080`，实际监听 `:5599` | 🟡 既有必挂 bug | 顺带修 |

**初始化顺序约束（必须保持）**：`config.Load → logger.Init → pathcache.SetDir → (GW) server.NewHandler → icefilter.SetEnvGate + netinfo.StartRefresher → 各自分支`

### 2.4 依赖统一（离线可完成）

| 模块 | Agent | GW | MVS 收敛 | wragent/go.sum 已含高版本 h1 |
|---|---|---|---|---|
| pion/webrtc/v4 | v4.0.8 | v4.0.8 | v4.0.8 | ✅ |
| pion/interceptor | 0.1.47 | 0.1.37 | **0.1.47** | ✅ |
| pion/rtp | 1.10.5 | 1.8.11 | **1.10.5** | ✅ |
| pion/srtp/v3 | 3.0.9 | 3.0.4 | **3.0.9** | ✅ |
| pion/sctp | 1.9.0 | 1.8.35 | **1.9.0** | ✅ |
| pion/sdp/v3 | 3.0.19 | 3.0.10 | **3.0.19** | ✅ |
| x/net / x/sys | 0.34.0 / 0.41.0 | 0.33.0 / 0.29.0 | **0.34.0 / 0.41.0** | ✅ |
| golang-jwt | — | v5.3.1 | 删除 `auth/` 则**不需要** | ⚠️ 仅存在于 wrgateway/go.sum |

**结论**：go.sum 取并集 + `go mod tidy`，MVS 必然收敛到 Agent 侧整套高版本，**零新增网络哈希需求**（前提 = 删死代码 `auth/`）。

### 2.5 配置并集设计（JSON `mode` 驱动）

```jsonc
{
  "mode": "agent",                    // ★ 新增：agent | gateway，决定启动分支
  "server_url": "wss://…", "log_level": "info",        // 共用（2）
  "ice_interface_filter": true, "ice_allow_tailscale": false,
  "ice_auto_fallback": true, "ice_path_cache": true,   // ICE 归一（4），env 双前缀兜底
  // —— mode=agent 专属（7）
  "agent_id", "auth_token", "host_key_policy", "known_hosts_file",
  "tunnel_local_port", "tunnel_target_addr", "tunnel_agent_id",
  // —— mode=gateway 专属（9）
  "listen", "tls_cert", "tls_key", "gateway_id", "gateway_token",
  "heartbeat_interval", "read_timeout", "write_timeout", "insecure_skip_verify"
}
```

**共 23 字段，字段名零真冲突**（仅 2 个语义一致的重叠）。

| 必须解决的坑 | 说明 |
|---|---|
| 🔴 **`Config.Save()` slim 结构丢字段**（`wragent/config/config.go:59-81` 只写 3 个身份字段） | Agent 认证回写后 **`mode` 被抹成默认值** → 下次启动进错模式。**必须把 `mode` 加进 slim 结构 + 往返单测** |
| 🔴 配置缺失行为不一致 | Agent=返回默认值自建；GW=`log.Fatalf` → 按 mode 分支保持语义 |
| 🟡 `-v` vs `-version` | 两个都接受 |
| 🟡 GW 默认 listen 三方不一致 | `config.Load=:443` / `wss.go` 回退 `:5599` / 线上 config `:5599` → **统一 `:5599`** |
| 🟡 `ServerConfig`（服务端下发）**不入本地 JSON** | 保持 `config_update` 热部署通路 |

---

## §3 最小 Docker 镜像体积评估

### 3.1 实测基础数据

| 二进制 | raw | gzip -6 | gzip -9 | 可剥离 (`.debug_*`+`.symtab`+`.strtab`) | `-s -w` 后 | 削减 |
|---|---|---|---|---|---|---|
| wragent amd64 | 17,523,782 | 9,571,849 | 9,545,101 | 5,575,459 | **11,948,323** | **31.8%** |
| wragent arm64 | 16,372,047 | 8,751,162 | 8,730,772 | 5,230,637 | **11,141,410** | 31.9% |
| wrgateway amd64 | 17,030,113 | — | 9,235,768 | 5,384,893 | **11,645,220** | 31.6% |
| wrgateway arm64 | 15,864,062 | — | 8,441,089 | 5,050,330 | **10,813,732** | 31.8% |
| `ca-certificates.crt` | 182,140 | — | **106,682** | — | 182,140 | — |

> gzip -6 与 -9 差仅 0.3%（Go 二进制已近随机数据）→ **压缩级别不值得调**。
> `.gopclntab`（4.3 MB）被 `-w` 保留 → **panic 堆栈仍有 file:line**，仅 Delve 源码级调试失效。

### 3.2 合并后单二进制【估算】

| 阶段 | amd64 | arm64 | 方法 |
|---|---|---|---|
| 合并未剥离 raw | **≈19.0 MB**（18.5–20.0） | **≈17.7 MB** | wragent 17.52 + 网关独有（server/handler+signaling）≈+1.0~1.5 − 重复 config/logger |
| `-s -w` 后 raw | **≈13.0 MB**（12.6–13.7） | **≈12.1 MB** | 实测剥离率 31.7% 线性折算 |
| 层 gzip 后 | **≈7.1 MB** | **≈6.6 MB** | 实测压缩比 0.543 |

### 3.3 各基座方案成品对比

| 方案 | 基座层 | 成品 pull (amd64) | `docker images` SIZE | 相对现状 |
|---|---|---|---|---|
| **`alpine:3.20` + CA + tzdata（选定）** | 12.2 MB | **≈11.1 MB** | ≈25 MB | **−99.1%** |
| `FROM scratch` + CA（若放弃 nsenter/shell） | 0 | ≈7.2 MB | ≈13 MB | −99.4% |
| `distroless/static` | ≈2 MB | ≈9.1 MB | ≈15 MB | −99.3% |
| `debian:bookworm-slim`（`wragent/Dockerfile` 现状） | ≈29 MB | ≈38 MB | ≈91 MB | −97.0% |
| **`pyterm/go-runtime`（compose 现状）** | — | **1.26 GB** | 1.26 GB | 基线 |

**最小可达值（回答"最小镜像能做多大"）**：
- **理论最小（放弃 nsenter/webterm，纯 scratch+CA）= 7.2 MB pull / 13 MB uncompressed**
- **功能完整（选定 alpine）= 11.1 MB pull / 25 MB uncompressed**

### 3.4 多架构（amd64 + arm64）

- 本机 buildx **无 arm64/QEMU 节点**，但本方案 Dockerfile **只有 `COPY`（`FROM alpine` 也不含 `RUN`）→ 无需模拟器即可交叉出 arm64 镜像**；Go 侧用 `GOARCH=arm64` 原生交叉编译。
- 备选：分别 `docker build` 两份 + `docker manifest create` 组合。
- **manifest list push 总量 ≈ 22 MB**（两 arch blob 各自上传）；单架构 pull 只取自己那一份 ≈ 11.1 MB。

**构建参数（纯 Go 标准能力）**：
```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64|arm64 \
  go build -trimpath -buildvcs=false -ldflags "-s -w -X main.gitCommit=… -X main.buildTime=…"
```
- 未剥离版本保留在 `releases/` 供调试，**镜像内只放剥离版**
- ⚠️ **不建议 UPX**：arm64 支持差、加壳触发 AV 误报、启动需解压 → 过度优化

---

## §4 内存泄露与碎片隐患（高频路径）【代码实证】

### 4.1 隐患清单

| # | 位置 | 类型 | 严重度 | 频率 | 修复方案 | Go 原生 | 阶段 |
|---|---|---|---|---|---|---|---|
| 1 | `signaling/client.go:258-260` | `Send()` 裸 `sendCh<-`，writePump 退后**永久阻塞调用方** | 🔴 | 写失败后第 65 条起 | `select{case ch<-d: case <-c.done:}` | ✅ | P0 |
| 2 | `signaling/client.go:138,174` | 每次重连重建 `done/sendCh`，旧 `defer close(c.done)` 可能关掉**新的** → 新 writePump 秒退 | 🔴 | 每次重连 | done/sendCh 只建一次；`conn` 置换持 `mu` | ✅ | P0 |
| 3 | `websocket/client.go:151,113-121` | 每次 `Connect()` 起新 heartbeat loop，`restartHeartbeat` 只能停 1 个 → **goroutine+Ticker 随重连线性累积**，`missedAcks` 翻倍诱发误判重连（正反馈） | 🔴 | 每次断线 +1 | stop 通道随连接生命周期重建 / `atomic.Bool` 单例 | ✅ | P0 |
| 4 | `tunnel.go:390,351` | **每 UDP 包 1 goroutine** + 传共享 `buf[:n]`（**数据竞争**）+ 每包 `json.Marshal`+base64(+33%) + `tunnelSend` 再拷 | 🔴 | 每个 UDP 包 | 先 copy 独立 payload；有界 channel + 固定 worker；载荷改二进制帧 | ✅ | P0 |
| 5 | `handler.go:515,915` | 无 select 阻塞投递 `session.SendCh<-j` → 卡死 pion `OnMessage/OnOpen` | 🔴 | DC 断连瞬间 | 补 `select + session.Done` | ✅ | P0 |
| 6 | `signal.go:2014-2046` | SFTP 上传重组 map **只在下一分片到达时才扫 TTL**，中途放弃的上传（=整文件体积）永久驻留 | 🔴 | 每次中断上传 | 独立 `time.Ticker` 清扫 + `OnClose` 全清 + 全局字节上限 | ✅ | P1 |
| 7 | `signal.go:2314,2279` | `io.ReadAll(整文件)` + 整文件重组 → 峰值 = 文件体积，每 24KB 分片 2×`make` | 🔴 | 每次 SFTP 读写 | `Stat` 得 size → 循环 `io.ReadFull` 到固定 buffer | ✅ | P1 |
| 8 | `signal.go:813,706,934` | `h.p[roomID]` / `pendingCandidates` **只增不删**（无人发 `bye`） | 🟠 | 每浏览器连接 1 条 | `peer.OnClose` 内 `delete` | ✅ | P1 |
| 9 | `handler.go:167` | `SendCh` **按帧数**限流 4096 × VNC 60KB ≈ **240MB 峰值** | 🟠 | 慢消费者 | 改**字节配额** 4–8MB | ✅ | P1 |
| 10 | `signal.go:1404,1412` | `sendDCMsg` 每帧 1 次 `make+copy`；SFTP 分片已造帧**此处再拷**（24KB×2） | 🟠 | 终端/VNC 每读循环 | 直发分支 per-DC scratch / `sync.Pool`；SFTP 帧自带 prefix | ✅ | P1 |
| 11 | `signal.go:2664`、`tunnel.go:752` | 隧道每包 `make(3+len)`（最高 64.5KB） | 🟠 | 高吞吐数千/s | 与读 `buf` 一起分配 `msgBuf` 复用 | ✅ | P1 |
| 12 | `pathcache.go:125,130,135,159,192` | `Lookup/Record` **每次全量落盘**，且**持锁 + 阻塞 ICE 建连关键路径** | 🟠 | 每建连 2–3 次磁盘写 | 脏标记 + 5s debounce `Ticker` 批量落盘 | ✅ | P2 |
| 13 | `signal.go:613,597,2515` | **循环内 `time.After`**，`613` 每帧一次（100Mbps≈760/s） | 🟠 | 背压/测速期 | `time.NewTimer`+`Reset` 或 `NewTicker` | ✅ | P2 |
| 14 | `handler.go:366,582,242` | DC 未就绪/队列满**每条打一行日志**（绕过级别短路，全局锁+syscall） | 🟠 | 未就绪期洪水 | 计数 + 5s 限频（照抄 `signal.go:1447` `lastDropLog`） | ✅ | P2 |
| 15 | `signal.go:1463,1473,1583` | `h.dcSendMu` **全局互斥** → 所有会话 DC 串行发送 | 🟠 性能 | 每次直发 | 并入 per-DC `st.mu` | ✅ | P2 |
| 16 | `handler.go:1052,1088,1111` | 信令按 RoomID **O(n) 线性扫** sessions 并逐个加锁 | 🟠 性能 | 每条 answer/candidate | 加 `roomToSession` 索引 | ✅ | P2 |
| 17 | `handler.go:1141-1145` | 每次 WS 连接新建 `http.Client/Transport` | 🟡 | 每次连接 | 包级共享 `*http.Client` | ✅ | P2 |
| 18 | `signal.go:1382,1785`+`1602,2504` | `dcWriteStates` 读路径 `LoadOrStore` **重建孤儿条目** | 🟡 | DC 关闭后偶发 | 改 `Load` | ✅ | P3 |
| 19 | 全项目 | **无 pprof / expvar**（`wss.go` 仅 `/` `/ws` `/health`） | 🟠 可观测 | — | `net/http/pprof` 独立 mux，**绑 loopback 或加鉴权** | ✅ | P3 |
| 20 | 全项目 | 无 `GOMEMLIMIT`/`GOGC` | 🟠 | 容器限额场景 | 容器 env `GOMEMLIMIT=<limit>×0.9` | ✅ env | P3 |
| 21 | `signal.go:1688-1691` | `hexStr += fmt.Sprintf("%02x ")` O(n²)+每字节 1 次 Sprintf | 🟡 | 仅 debug 级 | `strings.Builder` / `hex.EncodeToString` | ✅ | 顺手改 |
| 22 | `signal.go:1933,2429`、`localterm.go:128`、`speedtest.go:194` | **正面范式**：读缓冲全在循环外分配 | 🟢 | — | 保持 | — | — |

### 4.2 核验到的关键事实（决定方案边界）

1. **`dc.Send()` 返回即可复用缓冲** — `pion/sctp@v1.9.0/stream.go:340-343` 内部 `userData := make(...); copy(...)`，调用方 buffer **无所有权** → 复用安全，**无需 refcount 池**。
2. **接收侧每消息分配由 pion 产生**（`pion/webrtc/v4@v4.0.8/datachannel.go:407` `Data: make([]byte,n)`），无 pool → **无法消除也不应消除**（`Detach()` 重写回调式路由 = 风险远大于收益）。
3. `regexp.MustCompile` **全项目 0 处**；`strings.Split` 均在建连/5 分钟扫描级路径，不在每包路径。
4. 所有 `time.Ticker` 均 `defer Stop` ✅；`time.AfterFunc` 正确 `Stop` ✅；**无手动 `runtime.GC()`** ✅；日志**无无限缓冲** ✅；**无深循环 `defer`** ✅。
5. `os/user` **0 引用**；`os.Hostname`/`net.Lookup` **0 引用** → 镜像无需 `/etc/passwd`、无需 nsswitch。

### 4.3 明确「不做」的过度优化

| 想法 | 为什么不做 |
|---|---|
| 第三方内存池（`fastcache`/`bytebufferpool`） | 标准库 `sync.Pool` + 循环外分配已够；引入依赖违背精简目标 |
| 自建 DC 帧对象池抵消 pion 分配 | 分配在 pion 内部，**重复造轮子无收益** |
| `Detach()` 替代 `OnMessage` 做零拷贝读 | 禁用 `OnMessage` → 三个回调式路由全部重写 |
| socks5 握手 1~255B `make` 做共享 scratch | **每连接仅一次**，共享反引入竞态 |
| 自研异步环形日志缓冲 | 常驻内存反升，先做限频已解决 90% |
| `defer st.mu.Unlock()` 改显式 | 每消息几十纳秒，纯可读性损失 |
| `strings.Split` → `netip.ParseAddr` | 每建连一次，不在热路径 |
| `pathcache` 引入 LRU 库 | 键空间 `{"default"}`/agentID 本来就有界 |
| 每帧 `make([]byte,0,n)` 预分配防扩容 | 尺寸本就多变，收益为负 |
| UPX 压缩 | arm64 差、AV 误报、启动解压 |

---

## §5 多场景性能评估

### 5.1 已有实测基线（仓库文档，Phase 0 采集「合并前」侧）

**建连延迟**

| 场景 | 样本 | 平均 | P50 | P95 | 成功率 |
|---|---|---|---|---|---|
| SSH · P2P 本地 | 342 | 450 ms | **380 ms** | **820 ms** | 98.5% |
| SSH · P2P 远程 | 128 | 680 ms | **620 ms** | **1.2 s** | 95.3% |
| SSH · 网关 本地 | 256 | 520 ms | **450 ms** | **950 ms** | 97.2% |
| SSH · 网关 远程 | 98 | 890 ms | **810 ms** | **1.8 s** | 91.8% |
| VNC 首帧 · P2P 本地 | — | 1.2 s | **1.0 s** | **2.5 s** | — |
| VNC 首帧 · P2P 远程 | — | 2.1 s | **1.8 s** | **4.2 s** | — |
| VNC 首帧 · 网关 本地 | — | 1.5 s | **1.3 s** | **3.1 s** | — |
| VNC 首帧 · 网关 远程 | — | 3.2 s | **2.8 s** | **6.5 s** | — |
| 建连直连汇总 | 7 | **1060.7 ms** | — | — | 7/7 |
| 建连网关汇总 | 4 | **2905.2 ms** | — | — | 1/4（**3 失败**） |
| 端到端 L6（P2P 占 4.9%） | — | 149 ms | — | — | — |

**传输与回显**

| 指标 | 实测 | 口径 |
|---|---|---|
| SFTP 10MB 上传 | 2,244 → **1,163 ms（−48.2%）** | T1.3 改造后 |
| SFTP 10MB 下载 | 2,066 → **734 ms（−64.5%）** | 同上 |
| SFTP 1MB 上传线字节 | 1,873,700 → **1,049,099 B（−44.00%）** | 帧 59→45 |
| VNC 上行每事件线字节 | 80.8 → **10.7 B（−86.8%）** | 网关模式 A/B |
| 终端回显 P95 | 75 → 77 ms（n=15，空载）；**负载 81–99 ms** | 回归无回退 |
| SFTP list 首响应 P95 | 170 → 169 ms | 连接池未做 |
| 网关 ICE 占比 | **6.6–8.5 s，占建连 95–99%** | 最大瓶颈 |

**驻留资源（2026-10-01 `docker stats` 快照）**

| 容器 | CPU | 内存 | 内存% |
|---|---|---|---|
| `pyterm_wragent` | 0.00% | **18.64 MiB** | 0.12% |
| `pyterm_wrgateway` | 0.00% | **17.08 MiB** | 0.11% |
| `pyterm_wragent2` | 0.00% | **7.44 MiB** | 0.05% |
| `pyterm_md`（Python） | 0.42% | 88.76 MiB | 0.56% |

> Go 侧常驻 7–19 MiB；合并后预计 **≈20–24 MiB**（两进程 35.7 MiB → 一份）。

### 5.2 多场景压测设计（C3 已批准）

| 场景 | 负载模型 | 指标 | 采集手段 | 判据 |
|---|---|---|---|---|
| **S1 空载驻留** | 启动后 5 min 无连接 | CPU%、RSS、goroutine、heap inuse | `docker stats` 1s 采样 + pprof | CPU <0.5%，RSS 波动 <±10%，goroutine 不爬升 |
| **S2 单会话终端** | SSH 交互回显，n≥100 | P50 / P95 / max / σ | `/api/timeline/stats`（已有 `_percentile`） | P50 ≈75–80 ms，P95 <100 ms |
| **S3 大文件 SFTP** | 1/10/100 MB 上下行，n=3 | 耗时、MB/s、线字节、RSS 峰值 | `autotest/perf-probe.js`（`PHASES=upload,download`） | 10MB ≈1.16s/0.73s；**100MB 验证整文件驻留是否爆内存** |
| **S4 并发建连** | 并发 N = 1/5/10/20 同时 SSH+VNC | 建连 P50/P95、成功率、CPU 峰值、RSS | `connection_timeline` 聚合 + `docker stats` | 成功率 ≥95%；CPU 峰值 ≤80%；P95 增长 ≤ 线性 |
| **S5 网关中继并发** | `PERF_GATEWAY=1`，M 会话 × N | 桥接附加延迟、SendCh 深度、网关 CPU/RSS | `perf-probe.js` + pprof goroutine | 单帧附加 <1 ms；SendCh 深度 < 配额 50% |
| **S6 大流量隧道/测速** | Agent↔Agent speedtest 上下行限速档 | 吞吐 Mbps、CPU%、GC pause P99、alloc/op | `pprof/allocs` + `runtime/metrics /gc/cycles:total` + speedtest result | 吞吐达限速档；**alloc/op 为内存优化验收口径** |
| **S7 长稳 24h** | 循环建连/断开 500 次 + 定时 SFTP | RSS 斜率、goroutine 斜率、heap 趋势 | pprof 定时抓取 | **RSS 斜率 ≈0**；goroutine 回基线 = 无泄露 |
| **S8 GC 影响** | S6 满载下 | GC 次数/暂停 P99、分配速率 | `GODEBUG=gctrace=1` 或 pprof | 设置 `GOMEMLIMIT` 后 GC 次数应下降 |

**波动控制**（机器非独占 + `powersave` 调频）：
- 每场景 **≥3 轮**，取**中位轮**，报 `P50/P95/max/±σ`；
- 压测前 `uptime`/`top` 快照，剔除 BT-Panel、freeswitch 等明显抢占轮；
- 宿主跑进程先 `ulimit -n 65535`；
- **预期波动**：CPU 类 ±20–35%、延迟 P50 ±10–15%、P95 ±25–40% —— 属正常，**不做硬件层"优化"**。

---

## §6 大流量传输场景优化技术分析

### 6.1 当前链路开销

| 环节 | 现状开销 | 影响 |
|---|---|---|
| SFTP 分片 | 每 24KB：`buildSftpChunkFrame` 造帧 + `sendDCMsg` **再拷一次** = 2×24KB 分配 | 100MB ≈4300 片 ≈**200MB 分配流量 churn** |
| 隧道 TCP→DC | 每包 `make(3+n)`，最高 64.5KB；pion 内部**再拷一次** | 高吞吐时数千 alloc/s |
| 隧道 UDP | 每包 1 goroutine + `json.Marshal` + base64(+33%) + 共享 buf 竞争 | 正确性 + 带宽 + CPU 三重损失 |
| 网关 `SendCh` | 按**帧数** 4096 限额，不按字节 | 慢消费者下 **240MB 峰值** |
| 网关背压队列 | 头部 `s[1:]` 弹出，被丢帧仍被底层数组引用至 2× 扩容；每 25ms 一轮 2 次分配 | 持续背压时碎片 |
| 全局 `dcSendMu` | 所有会话 DC 串行发送 | **多会话大流量互拖**（并发吞吐天花板） |
| 日志 | 队列满/未就绪**每条一行**，全局 log 锁 + syscall | 洪水期进一步拉低吞吐 |

### 6.2 优化路径（按 ROI 排序，全部 Go 原生）

| 优先级 | 方案 | 预期收益 | 成本 |
|---|---|---|---|
| **A（P0）** | 修 `signaling.Send` select + `done` 单例 + UDP 去每包 goroutine | 消除**卡死/泄露**，大流量下信令不掉线 | 0.5 天 |
| **B（P1）** | 发送缓冲复用（`msgBuf` 随连接分配 / `sync.Pool`），SFTP 帧自带 prefix 去二次拷贝 | **alloc/op −40~60%**，GC 频率下降 → 吞吐 P95 改善 | 1 天 |
| **C（P1）** | SFTP 整文件**流式化**（`Stat` + 循环 `ReadFull` 固定 buffer） | 100MB 文件 RSS 峰值 **~200MB → ~1MB** | 1–1.5 天 |
| **D（P1）** | `SendCh` 改**字节配额** 4–8MB；所有投递补 `select+Done` | 网关 RSS 峰值 **240MB → 8MB** | 0.5 天 |
| **E（P2）** | `dcSendMu` 降为 **per-DC `st.mu`** | **多会话并发吞吐解除串行瓶颈** | 0.5 天 |
| **F（P2）** | UDP 载荷改二进制帧（去 JSON+base64） | UDP 隧道线字节 **−25%**，CPU 明显下降 | 1 天 |
| **G（P2）** | `pathcache` 落盘 debounce；循环内 `time.After` → `NewTimer` | 建连关键路径去掉同步磁盘 IO；GC 待收 timer 不堆积 | 0.5 天 |
| **H（P2）** | 日志限频；`roomToSession` 索引 | 洪水期 CPU −10~20%；O(n) → O(1) | 0.5 天 |
| **I（P3）** | loopback pprof + `GOMEMLIMIT` | 可观测性，长稳验收前提 | 0.5 天 |
| **既有待做** | 后端 SFTP `asyncssh` 连接池（T3.1）、DC 四路分流（T2.0）、网关纯中继化 | 见 `传输优化方案_v2.0` §5 | 已量化未实施 |

> **不建议**：引入 `io_uring`/用户态网络库、自研拥塞控制、KCP 替代 —— 当前瓶颈在**分配/锁/JSON**，不在内核网络栈。

---

## §7 风险评估

| # | 风险 | 概率 | 影响 | 缓解 |
|---|---|---|---|---|
| R1 | **`Config.Save()` slim 结构丢 `mode`** → 认证回写后进错模式 | 高 | 🔴 服务不可用 | slim 结构加 `mode` + Save→Load 往返单测 |
| R2 | 出包 **ETXTBSY**（容器正挂载二进制时覆盖） | 中 | 🔴 出包失败 | 按 `部署方案_v1.1.md:231`：先 `docker compose stop wragent wragent2` |
| R3 | **三端版本必须同窗**，混跑帧协议不匹配 | 高 | 🔴 建连失败 | 帧 magic 校验 + 版本号 gate；先网关→再 Agent→最后前端 |
| R4 | `icefilter.Summary()` 格式变化破坏下游匹配 | 中 | 🟠 解析错 | grep 全仓匹配点并同步 |
| R5 | 单进程单 mode 假设被打破 | 低 | 🔴 配置错乱 | `mode` 仅允许 `agent`/`gateway`，**不支持 `both`** |
| R6 | `init()` goroutine + 包级 `DeployMode` 在 gateway 模式空转 | 中 | 🟡 空转/噪声 | 改懒启动 |
| R7 | 依赖统一到高版本引入 pion 回归 | 低 | 🟠 传输异常 | pion/webrtc 本就同为 v4.0.8；`go build`+`go vet`+E2E 全量回归 |
| R8 | 镜像无 CA → TLS 握手失败 | 中 | 🟠 连不上 | 必拷 `ca-certificates.crt`；保留 `/health` 探针 |
| R9 | `-s -w` 后无法 Delve 源码级调试 | 确定 | 🟡 排障受限 | `releases/` 保留未剥离版；panic 堆栈仍有 file:line |
| R10 | arm64 镜像构建无 QEMU 节点 | 中 | 🟠 出包受阻 | Dockerfile 仅 `COPY`，**无需模拟器**；备选 `docker manifest create` |
| R11 | **双版本线 → 单一版本线**，Python 下载端点/安装脚本失效 | 高 | 🟠 升级流程断 | 同步改 `app/auth_api.py`（379/408/388/417 + 脚本模板 971–1160）、build 脚本、README、部署文档 |
| R12 | 压测在非独占 + powersave 机器上波动大 | 高 | 🟡 结论可信度 | ≥3 轮取中位、报 P50/P95/±σ、记录干扰进程 |
| R13 | `git` 双远端，误推 public 泄露敏感词 | 中 | 🔴 安全 | **禁止 `git push github`**；只走 `scripts/publish-github.sh`；敏感词同步 `publish-dict.tsv` |
| R14 | 安装脚本生成的 `config.json` 缺 `mode` → 宿主三件套部署起来即进错模式 | 高 | 🔴 部署即故障 | `auth_api.py` 脚本模板同步加 `"mode":"agent"` |

---

## §8 预期收益评估

| 维度 | 现状 | 合并后 | 收益 | 置信度 |
|---|---|---|---|---|
| **代码** | 15,130 行 / 5 个包双份同步维护 | ≈14,120 行 / 单份 | 删重 ~1,010 行；**双份同步提交成本归零** | 高（实测 diff） |
| **构建产物** | 2 二进制 × 3 平台 = 6 个/版本 | **1 × 3 = 3 个/版本** | 出包时间 −50%，版本线 2 → 1 | 高 |
| **镜像体积** | `pyterm/go-runtime` **1.26 GB** | **≈11.1 MB**（alpine，amd64） | **−99.1%**；多架构 push 合计 ≈22 MB | 高（实测 section + gzip） |
| **部署复杂度** | 3 service × bind-mount + `cp` 再 exec | 1 service，`-config` + JSON `mode` | 配置点 2 份 config.json → 1 份 | 高 |
| **常驻内存** | 2 进程 18.64 + 17.08 = **35.72 MiB** | 1 进程 ≈ **20–24 MiB** | **−33~44%** | 中 |
| **泄露/卡死** | 5 处 🔴 高危 | 修复后 | 长稳 RSS 斜率归零；重连不再 goroutine 线性累积 | 高（代码实证） |
| **大流量吞吐** | 全局 `dcSendMu` 串行 + 每包 2×24KB 分配 | per-DC 锁 + 缓冲复用 | **alloc/op −40~60%**；多会话并发解除串行瓶颈 | 中（待压测回填） |
| **SFTP 大文件峰值** | RSS ≈ 文件体积（100MB → ~200MB） | 流式固定 buffer ≈1MB | **−99%** | 高 |
| **网关背压峰值** | 4096 帧 ≈240MB | 字节配额 4–8MB | **−96%** | 高 |
| **运维** | 双版本号 / 双下载端点 / 双 build 脚本 | 单版本线 | 认知与回归成本减半 | 高 |
| **依赖** | 2 份 go.mod，pion 间接版本不一致 | 1 份，MVS 收敛到高版本 | 消除版本漂移风险 | 高（go.sum 已核验） |

---

## §9 执行计划

### Phase 0 — 基线冻结与方案存档
1. 存档本方案 `docs/wr合并为单二进制_v1.0.md` 并提交
2. 记录基线 commit、`go build ./... && go vet ./...` 双端结果
3. 采集性能基线（§5.2 S1–S6「合并前」侧）
4. 订正 `AGENTS.md` 中已失效的 `GO_BIN=/usr/local/go1.27/bin/go`

### Phase 1 — 包归一（🟢 低风险）
5. 删 `logger/`；`pathcache/`、`icefilter/`、`netinfo/` 取 wragent 版；GW 侧改 import
6. 删死代码 `auth/` → 去掉 `golang-jwt`
7. 抽 `internal/selfrestart`；`init()` goroutine 改懒启动
8. **门禁**：`go build ./... && go vet ./...` 双端通过

### Phase 2 — 配置与入口合并（🔴 核心风险区）
9. `config/` 做 23 字段并集 + `mode`；**`Save()` slim 结构加 `mode`** + 往返单测
10. 合并两 `main.go`：mode 分支、统一 `-v/-version`、配置缺失语义、env 双前缀、统一 graceful shutdown
11. 统一 GW 默认 listen → `:5599`；修 `wrgateway/docker-compose.yaml` healthcheck
12. 新建 `go.mod`（`github.com/ppy-tools/wr`）+ go.sum 并集 + `go mod tidy`
13. import 路径重写 62 处 / 24 文件

### Phase 3 — 内存隐患修复（P0→P2）
14. **P0**：`signaling.Send` select + `done` 单例；心跳 loop 单例；UDP 去包级 goroutine + 修数据竞争；`handler.go:515,915` 补 select
15. **P1**：SFTP 流式化 + 重组 map 独立 Ticker；`h.p`/`pendingCandidates` OnClose 删除；`SendCh` 字节配额；发送缓冲复用
16. **P2**：pathcache 落盘 debounce；`time.After`→`NewTimer`；日志限频；`dcSendMu`→per-DC；`roomToSession` 索引
17. **P3**：loopback-only pprof + 容器 `GOMEMLIMIT`

### Phase 4 — 镜像与多架构出包
18. 统一 `Dockerfile`：`FROM alpine:3.20` + CA + 多阶段构建；Go 加 `import _ "time/tzdata"`
19. 构建参数 `CGO_ENABLED=0 -trimpath -buildvcs=false -ldflags "-s -w -X …"`
20. `buildx --platform linux/amd64,linux/arm64`（COPY-only 无需 QEMU）→ `docker manifest`
21. **验收**：`docker images` SIZE **≤ 26 MB**；两架构 `-v` 正常；TLS 握手成功；`nsenter` 可用
22. 合并 build 脚本 → `build-wr.sh`；`.env` 单一版本线 + `app/auth_api.py` + README + 部署文档

### Phase 5 — compose 与部署切换（⚠️ ETXTBSY）
23. `docker compose stop wragent wragent2 wrgateway` → 改挂载 → `up -d`（**一个 service 取代三个**）
24. 硬切（C4）：不并行过渡；回滚 = `git revert` + 同序重部署

### Phase 6 — 验证与压测回填
25. 回归：`./run-baseline-tests.sh` + 双端 vet + E2E + `bash scripts/test-clean-users.sh`
26. 跑 §5.2 **S1–S8**，回填「合并后」列，产出 P50/P95/±σ 对比表
27. 长稳 S7（24h）验证 RSS/goroutine 斜率归零
28. 大流量 S6 对比 alloc/op 与吞吐，回填 §6.2 收益

### Phase 7 — 文档收尾
29. 更新 `AGENTS.md`（命令表、`GO_BIN` 路径、单二进制说明）
30. 更新 `docs/部署方案_v1.1.md` 服务表；新增版本 `v1.2`（不覆盖 v1.1）
31. 敏感词同步 `scripts/publish-dict.tsv`

**总计 ≈ 8–9 人日**，分 7 个可独立回滚的 commit 序列。

---

## §10 门禁与回滚

| 阶段 | 门禁 | 回滚方式 |
|---|---|---|
| 0 | 双端 `go build` + `go vet` 通过；基线 commit 记录 | — |
| 1 | 同上 + `go test ./...` 双端通过 | `git revert` 单 commit |
| 2 | 同上 + `config` Save/Load 往返单测通过 + 两 mode 各自 `--dry-run` 起得来 | `git revert` |
| 3 | 同上 + `go test -race ./...` 通过（重点 tunnel/signaling） | `git revert` |
| 4 | 镜像 `docker run -v` 双架构通过；TLS 握手成功；`nsenter` 可用；SIZE ≤26 MB | `git revert` + 恢复旧 compose |
| 5 | 容器 healthy、`/health` 200、Agent 注册成功、Gateway 信令连上 | **`git revert` + 同序重部署**（网关→Agent→前端） |
| 6 | §5.2 S1–S8 全部达标；P95 不回退；RSS/goroutine 斜率 ≈0 | 按场景单独修，不回退整体 |
| 7 | `publish-github.sh` 敏感词扫描通过 | — |
