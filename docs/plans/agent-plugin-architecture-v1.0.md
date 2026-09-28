# Agent 插件化架构方案 —— SSH / SOCKS5 独立插件设计

> **⚠️ 已被取代**:本文档的 SSH 服务章节(D5/阶段B/TC-H/8822)已由 [`agent-webterm-speedtest_v1.0.md`](./agent-webterm-speedtest_v1.0.md) 取代——内嵌 SSH 服务移除,改为 webterm 本地 shell 控制台,并新增固定 Agent 分组与 Agent↔Agent 测速;插件框架(阶段A)、SOCKS5 迁出(阶段C)、接线(阶段D)、P4 发版内容仍由新方案延续。本文档保留作历史存档。

| 项 | 值 |
|----|----|
| 版本 | **v1.0** |
| 日期 | 2026-09-27 |
| 状态 | 已批准,待实施 |
| 范围 | wragent(Agent 端) + md 配置协议 + web 配置 UI |
| 基线 commit | `7d9e250`(P1 SOCKS5 已发布) |
| 关联版本 | VERSION=2.3.0(P4 统一发版) |

## 修订记录

| 版本 | 日期 | 变更 | 状态 |
|------|------|------|------|
| v1.0 | 2026-09-27 | 初版存档:插件框架 + **端到端 `plugins` 配置协议**(用户决策) + A→F 编排;SSH 缺省 8822/both 认证、SOCKS5 缺省 2080 均定稿 | 已批准 |

## 决策记录

| # | 决策点 | 结论 |
|---|--------|------|
| D1 | 配置协议形态 | **端到端通用 `plugins` 字典**:md 存储/API/UI/Agent 全链路统一 |
| D2 | 模块形态 | 编译期 Interface 插件框架(非 .so、非子进程),进程内全嵌入,零外部依赖 |
| D3 | 实施顺序 | A 框架 → B SSH插件+协议改造 → C SOCKS5迁出 → D 接线 → E 联调 → F 提交(每阶段独立 commit,可回滚到 7d9e250) |
| D4 | 兼容策略 | 推送期**双格式并存**(`plugins` + 镜像 `tunnels`),旧 Agent(remote-agent 2.2.x)零影响 |
| D5 | SSH 服务 | 端口缺省 8822、auth_mode=both(密码+authorized_keys)、max_sessions=5、热部署 |
| D6 | SOCKS5 | 端口缺省 2080、可选 RFC1929 口令认证、动态目标经目标 Agent 拨号 |
| D7 | 测速(P3 衔接) | 固定 10 秒/模式、仅内存最近 20 条、**必须不影响正常服务**、执行顺序 P1→P2→P3→P4 |

---

## 1. 目标与设计原则

| # | 原则 | 说明 |
|---|------|------|
| P1 | 进程内插件,非动态加载 | 插件=独立 Go 包+统一接口;不用 Go `.so`(版本绑定/CGO/分发困难),不用子进程(违背全内嵌约束) |
| P2 | 端到端配置协议统一 | md→UI→存储→Agent 全链路 `plugins` 字典;键名=插件名 |
| P3 | 幂等 Reconcile | 相同配置跳过、变更热替换、键缺失=禁用;杜绝重复监听/端口泄漏 |
| P4 | 故障隔离 | 单插件 panic/启动失败只标记该插件,核心信令与其他插件不受影响,下次配置推送自动重试 |
| P5 | 依赖注入,禁止反向依赖 | 核心(webrtc)不 import 任何插件;插件可依赖核心;共享能力经 `Deps` 注入 |

## 2. 方案选型

| 方案 | 机制 | 优点 | 缺点 | 结论 |
|------|------|------|------|------|
| **A. Interface 插件框架** | 编译期接口+注册表+Reconcile 分发 | 零新依赖、可测试、热部署自然 | 插件需重编译(单二进制分发,可接受) | ✅ **选用** |
| B. Go plugin(.so) | `plugin.Open` 动态加载 | 运行时装载 | Go 版本强绑定、CGO/平台坑、远程升级复杂 | ❌ |
| C. 子进程插件 | 外部可执行文件 | 崩溃完全隔离 | 违背"服务全内嵌、不依赖外部程序"硬约束 | ❌ |

---

## 3. 架构设计

### 3.1 分层架构图

```
┌────────────────────────── pyterm-md (端到端改造) ──────────────────────────┐
│  AgentConfigReq {core, plugins{tunnel,socks5,ssh}}                        │
│  GET: 旧config_json 懒迁移 ──► plugins          PUT: 校验 ──► config_json  │
│  下发: {core, plugins, tunnels:合并镜像}  (双格式)                          │
└─────────────────────────────────┬──────────────────────────────────────────┘
                                  │ ws JSON(键新增,旧键保留)
┌─────────────────────────────────▼──────────────────────────────────────────┐
│ wragent 核心层                                                             │
│  signal.handleConfigUpdate ─► normalize(plugins|legacy) ─► plugin.Manager  │
│                                  ┌── 分发: 键名=插件名 ───┐                 │
│  Deps{Bridge, SendConnectTunnel} │  串行Reconcile·隔离    │                 │
│  webrtc(ICE/DC/看门狗) ◄─────────┴──────────────────────┘                 │
└───────┬─────────────────┬─────────────────┬────────────────────────────────┘
        ▼                 ▼                 ▼
  ┌───────────┐    ┌────────────┐    ┌────────────┐        ┌──────────────┐
  │ tunnel插件 │    │ socks5插件  │    │  ssh插件    │  预留  │ speedtest插件 │
  │ tcp/udp   │    │ :2080      │    │ :8822      │ ◄──── │ (P3消息驱动)  │
  │ +看门狗    │    │ 握手/认证   │    │ PTY/shell  │        └──────────────┘
  └─────┬─────┘    └─────┬──────┘    └─────┬──────┘
        └─────────── ICE DC 共享 ──────────┘ (ssh纯TCP不经ICE)
```

### 3.2 目录结构(迁移后)

| 路径 | 类型 | 内容 |
|------|------|------|
| `wragent/plugin/plugin.go` | 新增 | `Plugin` 接口、`Config`、`Status`、`Deps` 定义 |
| `wragent/plugin/manager.go` | 新增 | 注册表、通用字典分发、串行 Reconcile、panic 隔离、状态汇总 |
| `wragent/plugins/ssh/` | 迁入 | `server.go`+`hostkey.go`(x/crypto/ssh + creack/pty) |
| `wragent/plugins/socks5/` | 迁出 | 监听/握手/RFC1929 认证/CONNECT 解析(自 `webrtc/socks5.go` 216 行 + tunnel.go `case "socks5"`) |
| `wragent/plugins/tunnel/` | 新增 adapter | 包装 `webrtc.TunnelManager`:tcp/udp Reconcile、connect_tunnel 去重、看门狗归属 |
| `wragent/webrtc/` | 瘦身 | 信令/ICE/DC/桥接;`bridgeTCPConn` 导出;删除 socks5 分支(913→约750行) |
| `wragent/config/server_config.go` | 已改+扩展 | `SSHServiceConfig` + `Plugins map[string]json.RawMessage` + normalize |
| `wragent/webrtc/signal.go` | 改接线 | handleConfigUpdate → normalize → Manager(删散落 globalSSHService 直调) |

### 3.3 插件接口(框架唯一契约)

```go
// plugin/plugin.go —— 仅依赖 stdlib + config,禁止 import webrtc
type Plugin interface {
    Name() string                          // 与 plugins 字典键一一对应
    Reconcile(cfg json.RawMessage) error   // 幂等热部署; nil(键缺失)=禁用并Stop
    Stop() error                           // 优雅停止,可重复调用
    Status() Status                        // running/detail/since/lastErr
}

type Deps struct {                         // 核心能力注入(构造时传入)
    Bridge            func(conn net.Conn, addr string) // 经目标Agent桥接TCP(ICE DC)
    SendConnectTunnel func(agentID string) error       // 申请ICE通道(去重)
    Logf              func(format string, args ...any)
}
```

| 方法 | 调用时机 | 约束 |
|------|----------|------|
| `Reconcile(cfg)` | 配置推送时,manager **串行**调用 | 幂等;相同配置快速跳过;`nil`=禁用 |
| `Stop()` | 禁用/进程退出 | 关端口/断会话/释放资源 |
| `Status()` | 每次 reconcile 后 | 只读、不阻塞 |

### 3.4 插件清单

| 插件 | 配置键 | 职责 | 端口 | 核心依赖 | 现状 |
|------|--------|------|------|----------|------|
| `tunnel` | `plugins.tunnel` | tcp/udp 端口转发 + 30s 看门狗 + connect_tunnel 去重 | local_port 任意 | ICE/DC、SendConnectTunnel | 已上线,抽 adapter |
| `socks5` | `plugins.socks5` | SOCKS5 监听/握手/可选口令认证/CONNECT 动态目标 | 缺省 **2080** | `Deps.Bridge` | P1 已上线(`7d9e250`),迁出独立 |
| `ssh` | `plugins.ssh` | 内嵌 SSH server:密码/公钥(both)、PTY、shell/exec、会话上限 | 缺省 **8822** | 无(纯 TCP) | 代码已写,接入中 |
| `speedtest`(预留) | 无配置(ws消息驱动) | P2P 测速接收端 | 无 | DC | P3,框架预留 `HandleMessage` 扩展位 |

---

## 4. 核心流程

### 4.1 启动与首次配置下发

```
main runNormalMode
  → signal 连接 → register_success{config}
  → handleConfigUpdate(config)                    [signal.go]
  → normalize: 有 plugins → 直用; 仅 legacy tunnels → 按 protocol 拆成 plugins
  → plugin.Manager.Reconcile(plugins)             [唯一入口, 串行]
      ├─ 逐注册插件: p.Reconcile(plugins[p.Name()])  // 键缺失→nil→Stop
      └─ 未知键: 日志告警并忽略(前向兼容)
  → 各插件: 校验 → 关旧监听 → 起新监听 → goroutine
  → Status 汇总: [PLUGIN] tunnel=running socks5=running ssh=running
```

### 4.2 配置热更新(面板保存)

```
面板保存 payload{core, plugins{tunnel,socks5,ssh}}
  → md 校验: 已知插件键白名单{tunnel,socks5,ssh},其余 400;逐插件 schema 校验
  → 落库 config_json {core, plugins}                (新格式,懒迁移在此完成)
  → push = {core, plugins, tunnels: tunnel.tunnels ∪ socks5.tunnels 合并镜像}
  → 新Agent: plugins → Manager 逐键分发
       tunnel插件: diff→仅变更隧道重启; connect_tunnel 按目标去重(单次)
       socks5插件: diff→端口/口令变更才重启; 目标不变则零中断
       ssh插件:   diff→端口/认证变更重启; enabled=false→Stop(断开所有会话)
       未变更插件: 直接 return(不碰端口、不杀会话)
  → 旧Agent(2.2.x): Unmarshal 忽略 plugins, 读 tunnels 镜像 → 行为不变 ✓
```

> 关键:只有变更的插件受影响 —— 改 SSH 端口不动 SOCKS5/隧道,改隧道不断 SSH 会话。

### 4.3 SOCKS5 代理访问(插件内数据流)

```
本地应用 ──TCP──► socks5插件 listener(:2080)
  ① 协商方法(无口令→0x00直连; 有→0x02 RFC1929 用户名口令)
  ② 认证失败 → reply 0x01 关闭(不影响其他连接)
  ③ 解析 CONNECT → host:port (BIND/UDP-ASSOCIATE → 0x07 拒绝)
  ④ Deps.Bridge(acceptedConn, host:port)          [注入自 webrtc 核心]
       → 查/建 目标Agent 的 ICE DC(connect_tunnel 去重)
       → 0x30 Connect{host,port} → 对端 net.DialTimeout(host:port)
  ⑤ reply 0x00 成功 → 双向 bridge(背压+onResult 统计)
  失败: 0x05 Connection Refused
```

### 4.4 SSH 登录(插件内数据流)

```
ssh 客户端 ──TCP──► ssh插件 listener(8822)
  ① x/crypto/ssh 握手: password(恒时比较)/authorized_keys(每次读取,免重启生效)
     auth=both 任一通过; MaxAuthTries=4; 会话数≥max_sessions 直接拒
  ② session channel: pty-req → creack/pty 分配伪终端(记录窗口大小)
  ③ shell(-l 登录shell) 或 exec(shell -c cmd)
  ④ 桥接 ch↔pty: stdin 断开注入EOT/写错误kill; stdout(pty EIO)结束→取退出码
  ⑤ window-change → TIOCSWINSZ 热更新; 结束发 exit-status + 关闭
  进程 panic → recover → 该会话退出,监听继续
  主机密钥: ~/.wragent/ssh_host_ed25519 持久化(重启指纹不变)
```

### 4.5 故障隔离与自愈

```
插件 Reconcile panic ──► manager recover ──► Status{Running:false, LastErr}
插件启动失败(端口占用) ──► 记录错误,不占死 ──► 下次配置推送重试
核心 ws 断线 ──► 插件监听不受影响; 重连后配置重推 → 幂等收敛
```

---

## 5. 配置协议(端到端)

### 5.1 新 Schema

```json
{
  "ws_reconnect_interval": 5,
  "ws_heartbeat_interval": 30,
  "ice_cooldown": 2,
  "log_level": "info",
  "plugins": {
    "tunnel": { "tunnels": [ {"id":"t1","protocol":"tcp","local_port":10022,"target_agent_id":"..."} ] },
    "socks5": { "tunnels": [ {"id":"s1","protocol":"socks5","local_port":2080,"socks_username":"","socks_password":"..."} ] },
    "ssh":    { "enabled": true, "port": 8822, "bind": "0.0.0.0",
                "auth_mode": "both", "password": "***", "max_sessions": 5 }
  }
}
```

### 5.2 字段路由表

| 键 | 插件 | 形态 | 缺省 | md 校验 |
|----|------|------|------|---------|
| `plugins.tunnel.tunnels[]` | tunnel | 列表,仅 `protocol∈{tcp,udp}` | 空列表 | protocol 过滤;target_agent_id 必填 |
| `plugins.socks5.tunnels[]` | socks5 | 列表,仅 `protocol==socks5` | 空列表 | local_port 1-65535 |
| `plugins.ssh` | ssh | 对象 | key 缺失=禁用 | enabled⇒端口 1-65535 + auth_mode∈{password,key,both} + 非 key 模式密码必填 |
| `ws_*` / `log_level` | 核心 | 数值/字符串 | 5s/30s/info | 原有 Field 校验 |

### 5.3 存储迁移(旧 config_json → 新)

| 场景 | 处理 |
|------|------|
| 旧数据 `{tunnels:[tcp,udp,socks5...]}` 无 `plugins` | **懒迁移**:GET 按 protocol 拆分合成 `plugins{tunnel,socks5}` 返回;下次 PUT 落库新格式 |
| 新数据含 `plugins` | 原样读写;顶层 `tunnels` 不再落库(仅推送镜像) |
| 回滚 | GET 有 `plugins` 用新,无则拆 `tunnels`,两向兼容 |

### 5.4 兼容矩阵

| 发送方 ↓ / 接收方 → | 新 Agent (≥2.3) | 旧 Agent (2.2.x, remote-agent) |
|----------------------|------------------|--------------------------------|
| 新 md(同仓库部署) | 走 `plugins` → PluginManager | 走镜像 `tunnels` → 旧逻辑;`plugins` 被 Unmarshal 忽略 ✓ |
| 新 md GET 给新 UI | 返回 `plugins` | — |
| 新 md GET(旧数据) | 懒迁移合成 `plugins` | — |

---

## 6. 改动清单(文件级)

| 模块 | 文件 | 改动内容 | 阶段 |
|------|------|----------|------|
| wragent | `plugin/plugin.go` `plugin/manager.go` | 框架+通用字典分发 | A |
| wragent | `plugins/ssh/*` | sshsvc 改包名+接口适配 | B |
| wragent | `config/server_config.go` | `SSHServiceConfig` + `Plugins map[string]json.RawMessage` + normalize | B |
| wragent | `webrtc/signal.go` | handleConfigUpdate→normalize→Manager(删散落接线) | B/D |
| wragent | `plugins/socks5/*` | 迁出 `webrtc/socks5.go` + `tunnel.go case "socks5"` | C |
| wragent | `webrtc/tunnel.go` | 过滤 socks5;`bridgeTCPConn` 导出;去重逻辑移交插件 | C/D |
| wragent | `plugins/tunnel/*`、`main.go` | adapter+看门狗归属+初始化 | D |
| wragent | `go.mod` `go.sum` | creack/pty v1.1.24(已拉取) | B |
| md | `app/auth_api.py` | `AgentConfigReq.plugins`(白名单+逐插件校验);GET 懒迁移;PUT 存新格式;push 双格式(tunnels 镜像) | B |
| web | `api.ts` | `AgentPluginsConfig` 接口 | B |
| web | `AgentConfigModal.vue` | 第4页签 SSH;payloadOf/loadConfig 按 plugins 拆合(隧道列表仍统一,按 protocol 拆分存储) | B |
| web | `zh-CN.json` `en.json` | `agentConfig.ssh*` + 插件文案 | B |
| 不变 | ws 信令协议/隧道逻辑/看门狗/P1 已发布行为/md 隧道结构 | — | — |

---

## 7. 实施阶段与验收

| 阶段 | 内容 | 验收 | 回滚点 |
|------|------|------|--------|
| **A** 框架 | `plugin/` 包+假插件单测(分发/幂等/隔离) | TC-PL01/02 | 新增无风险 |
| **B** SSH插件+端到端协议 | plugins/ssh;md AgentConfigReq/GET懒迁移/PUT/双格式push;UI SSH页签+plugins payload;i18n | TC-H01~06 + TC-CFG01~04 | 独立 commit |
| **C** SOCKS5迁出 | 独立包化,清 tunnel.go socks5 分支 | TC-S01~05 全回归 | 独立 commit(逻辑零改动) |
| **D** 接线 | Manager 接 signal/main;tunnel adapter;normalize 双通路 | TC-PL03/04 | — |
| **E** 联调 | 双端构建部署(tx+m1k)+全量回归 | 下表全部 | — |
| **F** 提交 | 分阶段 push gitcode;`VERSION=2.3.0` 随 P4 | 清洁历史 | `7d9e250` |

### 测试用例

| 用例 | 阶段 | 内容 | 期望 |
|------|------|------|------|
| TC-PL01 | A | 假插件 启用/禁用/同配置重复 Reconcile | 无重复启动 |
| TC-PL02 | A | 假插件 Reconcile 内 panic | 标记失败,其余插件正常 |
| TC-CFG01 | B | 旧 config_json(混合 tunnels)GET | 合成 plugins,UI 正常 |
| TC-CFG02 | B | PUT 后落库 | 新格式;`tunnels` 镜像仅 push 不落库 |
| TC-CFG03 | B | push 双格式给旧版 remote-agent | 行为与 2.2 一致 |
| TC-CFG04 | B | 新 Agent 收双格式 | 仅走 plugins,不重复启动两套 |
| TC-H01 | B | enable → 8822 监听 | `ss -ltn` 见 8822 |
| TC-H02 | B | 密码登录+`ssh -p8822 root@host 'echo hi'` | 交互/执行正常,exit-status 正确 |
| TC-H03 | B | 错误密码×2 | 拒绝(MaxAuthTries=4) |
| TC-H04 | B | auth_mode=key 用 authorized_keys | 免重启生效 |
| TC-H05 | B | 热换端口 8822→8823 | 8822 关/8823 开;**SOCKS5与隧道无中断** |
| TC-H06 | B | enabled=false | 监听关闭、在途会话断开 |
| TC-S01~04 | C | SOCKS5 错口令拒/IP 200/关端口0x05/域名 200 | 与 P1 结果一致 |
| TC-S05 | C | 同目标双隧道去重重建 | 通过(沿用 P1 修复) |
| TC-PL03 | D | 改隧道配置 | 仅 tunnel 插件动,SSH 会话不断 |
| TC-PL04 | D | 面板保存后 agent_info/终端/3333 | 全部正常 |
| TC-P08 | E | 隧道+SSH+SOCKS+终端并行 | 互不影响 |

---

## 8. 风险与对策

| 风险 | 等级 | 对策 |
|------|------|------|
| 配置协议改造牵动 md+UI+Agent 三方 | 中高 | 阶段 B 独立提交;双格式 push 保旧 Agent 零影响;懒迁移可回滚 |
| 旧数据 GET 拆分错误(protocol 缺失等) | 中 | 缺省按 tcp 归 tunnel;拆分失败回退原样返回+日志 |
| socks5 迁出回归(已上线) | 中 | 阶段 C 独立提交,纯移动+依赖注入,TC-S 全回归 |
| Reconcile 并发竞态 | 中 | manager 串行分发+每插件互斥锁+幂等 diff |
| import 环(插件↔webrtc) | 中 | `plugin/` 纯净包;webrtc 不 import 插件;单向依赖:插件→webrtc |
| 双格式推送不一致 | 低 | 镜像=plugins 合并的纯函数产物,同一处代码生成 |
| sshsvc 半成品遗留 | 低 | 阶段 B 第一步归位 `plugins/ssh`,删散落直调 |

---

## 9. 后续衔接

- **P3 P2P 测速**: 接收端以 `speedtest` 插件形式装入框架(消息驱动,`HandleMessage` 扩展位);relay 强制(ICETransportPolicyRelay)/复用 `_agent_connections` 房间方案不变;**不影响正常服务**:独立 PC/房间、严格背压+超时清理、panic 隔离、单并发、限速选项、进度限频 2Hz、可立即中止、并行回归 TC-P08。
- **P4 发版**: 插件化+SSH 合入后统一 `VERSION=2.3.0`,双端部署全量回归(TC-S/H/PL/CFG/P)→ 远程 Agent 升级 → 最终 push。
