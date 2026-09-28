# Agent webterm 控制台 / 固定 Agent 分组 / P2P 测速 · 插件化收口 完整方案

| 项 | 值 |
|----|----|
| 版本 | **v1.0** |
| 日期 | 2026-09-27 |
| 状态 | 已批准,执行中 |
| 范围 | wragent + md + web(远程管理/配置 UI) |
| 基线 commit | `b46c915`(阶段A 插件框架已提交;工作区含阶段B SSH 半成品) |
| 取代关系 | **取代** `agent-plugin-architecture-v1.0.md` 的 SSH 服务章节(D5/阶段B/TC-H);延续其插件框架(阶段A已落地)、SOCKS5 迁出(阶段C)、接线(阶段D)、P4 发版(2.3.0) |
| 关联版本 | VERSION=2.3.0(P4 统一发版) |

## 修订记录

| 版本 | 日期 | 变更 | 状态 |
|------|------|------|------|
| v1.0 | 2026-09-27 | 初版存档:合并三需求——①webterm 本地 shell 控制台(取代内嵌 SSH 服务) ②远程管理固定 Agent 分组 ③Agent↔Agent P2P 测速完整设计;plugins 配置协议保留(tunnel/socks5 键);阶段编排 B′→W→C→D→S→E→F | 已批准 |

## 决策记录

| # | 决策点 | 结论 |
|---|--------|------|
| N1 | webterm shell 来源 | **Agent 本地 shell**:浏览器→WebRTC DC→Agent 直起本地 PTY(creack/pty),不监听端口、不走 SSH 协议、Agent 机器无需 sshd;复用首帧 `ssh_connect`(0x01) JSON 加 `mode:"local"` 字段,**网关整体重打包自动透传,网关零改动** |
| N2 | 工作区 SSH 半成品处置 | **删 SSH 服务留协议**:删内嵌 8822 插件与 UI SSH 页签,保留 `plugins` 端到端配置协议(tunnel/socks5 键、懒迁移、双格式下发);PTY 桥接代码改造给 webterm 复用 |
| N3 | 自动分组形态 | 远程管理侧栏**置顶一个固定不可删除的 Agent 分组**,列出当前用户可见的所有 Agent(owner/共享过滤),卡片单击直开 webterm;现有 SSH/FILE、VNC、RDP 分组保留 |
| N4 | 测速形态与入口 | **新方案内完整设计**;入口 = Agent 分组卡片「测速」按钮 → 选目标 Agent;**双向各 10 秒**(上行 A→B + 下行 B→A,兑现 D7「固定 10 秒/模式」) |
| N5 | 测速接收端形态 | 接收端以 `speedtest` 插件装入框架(plugin 包新增可选 `HandleMessage` 扩展 + panic 隔离);发起端房间编排在 signal.go(relay-only Peer 变体);复用 `_agent_connections` 房间路由 |
| N6 | 安全边界 | webterm 分组仅显示 owner/共享 Agent(接口既有过滤);打开 webterm 记一条 `AuditLog`;方案文档明确"可见即有 shell 权限"边界 |
| N7 | 文档版本管理 | 本文档为最新完整方案;`agent-plugin-architecture-v1.0.md` 头部加"已被取代"指针,不覆盖旧版(AGENTS.md 文档约定) |

---

## 1. 背景与目标

v1.0 插件化方案落地阶段A 后,三点变化收敛为本文档:

1. **内嵌 SSH 服务(8822)取消**——其价值由 webterm 完全覆盖:点击 Agent 直接进 Web 控制台,Agent 本地 shell 经 WebRTC DC 直起,免端口、免凭据、免 sshd。
2. **远程管理新增固定 Agent 分组**——所有可见 Agent 置顶聚合,控制台一键直达。
3. **Agent↔Agent 测速**——v1.0 仅在 D7/§9 留了衔接要点,本文档给出完整设计(协议、编排、UI、用例)。

保留 v1.0 已批准内容:插件框架(阶段A 已提交)、`plugins` 配置协议、SOCKS5 迁出、Manager 接线、P4 统一发版 2.3.0。

### 目标清单

| # | 目标 | 验收 |
|---|------|------|
| G1 | 移除内嵌 SSH 服务,plugins 协议保留 | `ss -ltn` 无 8822;GET/PUT/懒迁移/双格式下发全通过 |
| G2 | webterm:Agent 本地 shell 控制台 | 分组点击 → PTY shell,whoami=Agent 主机用户,resize/断开/重连正常 |
| G3 | 固定 Agent 分组 | 置顶、不可删、空态占位、离线置灰、单击直开 |
| G4 | Agent↔Agent 测速 | 双向各 10s 结果、2Hz 进度、内存 20 条、可中止、单并发、不影响正常服务 |
| G5 | 插件化收口 | SOCKS5 独立包、Manager 接线、tunnel adapter、normalize 双通路 |

---

## 2. 架构设计

### 2.1 webterm 协议(零网关改动)

```
浏览器 ── ssh_connect{mode:"local",cols,rows} ──► 网关(整体重打包,字段自动透传)
       或直连 DC(0x01 前缀 + 同一 JSON)
                                                      │
                                                      ▼
wragent bridgeSSHToDataChannel 首帧 0x01 ──► connectSSH 分支:
  mode=="local" → localterm.connectLocal():
    pty.StartWithSize(exec.Command($SHELL,"-l"), Winsize{rows,cols})
    桥接 DC ↔ PTY(master fd 双向 io.Copy)
    MsgResize(0x02) → pty.Setsize (TIOCSWINSZ)
    R6 幂等重连: 关旧 pty + 杀进程组后重建
```

- `SSHConnectMsg` 新增 `Mode string json:"mode"`(`signal.go:766`)
- `sshSession` 新增 `pty *os.File` + `cmd *exec.Cmd` 字段;`MsgResize` 分支:有 session 走 `WindowChange`,否则 `pty.Setsize`
- 新文件 `wragent/webrtc/localterm.go`——由 `plugins/ssh/server.go` 的 `runProcess`/pty 桥接改造(按 DC 通道重写,删 gossh 层)
- 首帧诊断 0xFE(agent_connect_ms 等)沿用同一发送路径
- SFTP/FILE **不在本期范围**(本地 shell 无 sshConn,SFTP 需另建本地服务;后续可扩展)

### 2.2 固定 Agent 分组(前端)

- `SshManager.vue` 侧栏置顶新分组 key `agents`:默认展开、**无编辑/删除/排序按钮**、永远渲染(0 个时渲染 `EmptyState` 占位,遵守设计系统"空状态必须占位")
- 数据 = 既有 `agents` ref(`api.adminListAgents()`,`app/auth_api.py:284`,owner/共享过滤,含 `online`)
- 卡片:名称 / 在线点 / 备注;**单击直开 webterm**(伪连接复用 `createTerm/connectWebRTC`);展开行放「测速」按钮
- 伪连接:`{id: agent.id, name: agent.name, host: "", port: 0, username: "", agent_id: agent.id, mode: "local"}`;同 Agent 已开终端则聚焦已有 tab
- 离线点击 → toast;`is_active=0` 置灰
- webterm 打开记 `AuditLog`(`app/database.py:200`,复用 `auth.py:265` 写入模式)

### 2.3 Agent↔Agent 测速

#### 消息流

```
浏览器 --speedtest_start{target,mbps_limit}--> md
  md: 校验两端在线 + 全局单并发锁;建房间 room=speedtest_{src}_{dst}_{ts}
  md --speedtest_connect{room,duration,mbps_limit}--> 源Agent
  md --browser_connect{room}--> 目标Agent            (复用 _agent_connections 路由)
源Agent: CreateTunnelPeer 变体
  = relay-only Peer(ICETransportPolicyRelay) + DC label "speedtest" + offer
offer/answer/candidate 经既有房间路由互通(浏览器不再参与 SDP)

阶段1 上行 A→B 10s: 源按限速发帧(背压: BufferedAmount<64KB 才发) / 目标 HandleMessage 计字节
  目标 --speedtest_progress(≤2Hz)--> md --> 浏览器(房间 browser_ws)
阶段2 下行 B→A 10s: 对称
目标 --speedtest_result{up_mbps,down_mbps,bytes,duration}--> md
  md: 入内存 deque(maxlen=20); 转发浏览器; GET /api/webrtc/speedtest/history

浏览器 --speedtest_cancel--> md --speedtest_stop--> 两端: 关 PC/DC + 清房间
md: 房间 30s TTL 兜底清理(无进度即超时)
```

#### 设计约束对照(v1.0 §9/D7 逐条兑现)

| 约束 | 落地 |
|------|------|
| 固定 10 秒/模式 | 上行 10s + 下行 10s,硬时限,到点即停 |
| 仅内存最近 20 条 | md `collections.deque(maxlen=20)`,进程内存,不落库 |
| 必须不影响正常服务 | 独立 PC/房间 + 背压 + 30s TTL + 单并发 + 限速档 + 可立即中止 |
| relay 强制 | 两端 `ICETransportPolicyRelay`;缺 TURN 明确报错「需配置Coturn」 |
| 复用 `_agent_connections` | 房间注册/SDP 路由完全复用,仅新增 speedtest_* 消息分支 |
| 独立 PC/房间 | 每次测速新建 Peer,不碰终端/隧道 PC |
| panic 隔离 | `HandleMessage` 分发包裹 recover(复用 Manager 隔离模式) |
| 单并发 | md 全局锁,同一时刻仅 1 个测速会话,冲突返回 busy |
| 限速选项 | 10 / 50 / 100 / 无限制 Mbps 档 |
| 进度限频 2Hz | 接收端 500ms 聚合一次上报 |
| 可立即中止 | cancel → 双端 stop → 关 PC 清房间 |
| 并行回归 TC-P08 | 阶段E 基线含终端+隧道+SOCKS5+测速并行用例 |

#### speedtest 插件接口扩展(plugin 包)

```go
// 可选接口:实现它的插件接收消息分发(type assertion, 不改 Plugin 主接口)
type MessageHandler interface {
    HandleMessage(kind string, payload []byte)  // kind: "data"/"ctrl"; panic 由 Manager recover
}
// Deps 扩展
Deps.SendSignal func(payload []byte)  // 上报 progress/result 到 md(信令 WS)
```

- 接收端 `wragent/plugins/speedtest/`:计字节、限频上报、阶段状态机
- 发起端编排在 `wragent/webrtc/signal.go`:`speedtest_connect` 分支 + relay-only Peer 变体 + 发送循环(限速/背压)
- DC 帧格式:首字节类型(`0x60`=ctrl JSON / `0x61`=数据负载),标签 `speedtest`

#### UI(遵守设计系统)

- Agent 卡「测速」→ 紧凑弹窗(卡片式、flex 撑满、无大留白):目标 Agent 选择、限速档、进度条+实时 Mbps、上行/下行结果卡、最近 20 条历史表(`flex:1` 滚动,空态占位)

### 2.4 plugins 协议收口(阶段B′)

| 位置 | 改动 |
|------|------|
| `app/auth_api.py` | `_PLUGIN_KEYS={"tunnel","socks5"}`;PUT 收到存量 `ssh` 键**接受并剥离**(不 400,兼容旧数据回写);`_migrate/_split` 不再注入 `"ssh": {}`;删 `_validate_plugins` ssh 段 |
| `wragent/config/server_config.go` | 删 `SSHServiceConfig`;保留 `Plugins map[string]json.RawMessage` |
| `wragent/webrtc/signal.go` | 删 sshplugin import、`Register(sshplugin.New())`、热更新 ssh 分支与日志;保留 Manager 框架(阶段D 注册 tunnel/socks5) |
| `wragent/plugins/ssh/` | **整目录删除**;pty 依赖(creack/pty)转为 localterm 直接依赖 |
| `web/src/api.ts` | 删 `SSHServiceConfig`、`AgentPluginsConfig.ssh` |
| `web/src/components/AgentConfigModal.vue` | 删 SSH 页签、`rawSSH` 逻辑、payload ssh 键 |
| `web/src/i18n/{zh-CN,en}.json` | 删 19 个 `agentConfig.ssh*` 键(两文件键集保持完全一致) |

---

## 3. 阶段编排(每阶段独立 commit,中文 message,可回滚)

| 阶段 | 内容 | 测试门禁 |
|------|------|----------|
| **B′ 收口** | §2.4 去SSH留协议 | pytest config API(GET懒迁移/PUT校验/存量ssh剥离) + vitest + `go build ./...` + `ss -ltn` 无8822 |
| **W webterm+分组** | §2.1 + §2.2 + AuditLog + i18n | TC-W01 开shell、W02 resize、W03 exit/断开态、W04 离线toast、W05 双tab并存、W06 i18n键集一致 + `go test ./...` |
| **C SOCKS5迁出** | v1.0 原阶段C:独立包化,清 tunnel.go socks5 分支,逻辑零改动 | TC-S01~05 全回归 |
| **D 接线** | Manager 接 signal/main;tunnel adapter;normalize 双通路(tunnel/socks5 键) | TC-PL03/04:热改隧道,webterm/终端/SOCKS5 不断 |
| **S 测速** | §2.3 全量 | TC-ST01~07 + TC-P08 并行回归 |
| **E 联调** | `./run-baseline-tests.sh` 全量 + E2E | 基线全绿 |
| **F 发版** | `VERSION=2.3.0` 统一 bump,双端部署回归 → 远程 Agent 升级 → push origin(按指示) | 全量复测 |

### 测试用例

| 用例 | 阶段 | 步骤 | 预期 |
|------|------|------|------|
| TC-CFG01 | B′ | GET 旧 config_json(顶层 tunnels) | 懒迁移视图 plugins{tunnel,socks5},无 ssh 键 |
| TC-CFG02 | B′ | PUT 含存量 plugins.ssh 键 | 接受并剥离落库,响应不含 ssh |
| TC-CFG03 | B′ | PUT plugins 未知键 | 400 |
| TC-CFG04 | B′ | GET→PUT 回写 | 双格式(tunnels 镜像)一致 |
| TC-W01 | W | 分组点击 Agent 卡 | 开终端 tab,shell 就绪,whoami=Agent 主机用户 |
| TC-W02 | W | 拖动窗口尺寸 | `stty size` 与前端一致(TIOCSWINSZ) |
| TC-W03 | W | shell 内 exit / 断网 | tab 状态 disconnected,重连可恢复 |
| TC-W04 | W | 离线 Agent 卡点击 | toast 提示,不开 tab |
| TC-W05 | W | 同 Agent 开两个 tab | 两个独立 PTY 互不干扰;重复点击聚焦已有 |
| TC-W06 | W | i18n 键集比对 | zh/en 键完全一致 |
| TC-S01~04 | C | SOCKS5 错口令拒/IP 200/关端口 0x05/域名 200 | 与迁出前结果一致 |
| TC-S05 | C | 同目标双隧道去重重建 | 通过(沿用 P1 修复) |
| TC-PL03 | D | 改隧道配置 | 仅 tunnel 插件动,webterm 会话不断 |
| TC-PL04 | D | 面板保存后终端/隧道/SOCKS5 | 全部正常 |
| TC-ST01 | S | 双 Agent 测速跑完 | 上行/下行 Mbps 均返回,10s×2 结束 |
| TC-ST02 | S | 测速中点中止 | 两端 PC/DC 关闭,房间清理,浏览器立即收到终止 |
| TC-ST03 | S | 并发第二次发起 | 返回 busy,首个不受影响 |
| TC-ST04 | S | 目标 Agent 离线发起 | 明确报错,不建房间 |
| TC-ST05 | S | 无 TURN 环境发起 | 明确报错「需配置Coturn」 |
| TC-ST06 | S | 连续 21 次测速 | 历史接口恒 ≤20 条 |
| TC-ST07 | S | 观察 progress 频率 | ≤2Hz(500ms) |
| TC-P08 | E | 终端+隧道+SOCKS5+测速并行 | 互不影响 |

---

## 4. 验证与部署

```bash
DB_HOST=127.0.0.1 TESTING=1 python3 -m pytest app/tests   # 后端
cd web && npm test && npm run build                        # 前端单测+构建
cd wragent && go test ./... && go build ./...              # Go 单测+编译
./run-baseline-tests.sh                                    # 基线三段(pytest+vitest+E2E)
docker restart pyterm_md                                   # 后端/静态生效
bash scripts/build-wragent.sh                              # Agent 二进制(F 阶段 bump 2.3.0)
```

集成测试(`test_integration_*`)依赖 `docker compose up -d test-ssh-server`。

---

## 5. 风险与对策

| 风险 | 等级 | 对策 |
|------|------|------|
| webterm = 免密 shell,权限面扩大 | 高 | 仅 owner/共享可见(接口既有过滤) + AuditLog 记录;文档明确安全边界 |
| 存量 DB 含 `plugins.ssh` 键 | 中 | PUT 接受并剥离而非拒绝;Agent 侧忽略未知键;阶段B′ 独立提交可回滚 |
| relay 强制依赖 coturn | 中 | 缺 TURN 快速失败并给明确提示;联调环境 compose 自带 coturn |
| 测速占带宽影响业务 | 中 | 限速档缺省收敛、单并发、10s 硬时限、可中止、TC-P08 并行验证 |
| 删 SSH 半成品残留 | 中 | B′ 一步归位(`git diff` 逐文件核对 10 文件去向),不留散落引用 |
| DC 通道桥接错误(PTY 生命周期) | 中 | TC-W01~05 覆盖;R6 幂等重连分支同步处理 pty+进程组 |
| i18n 键集漂移 | 低 | TC-W06 键集比对;两文件同批增删 |

## 6. 后续衔接

- **SFTP/FILE for webterm**:本地 shell 无 sshConn,需 Agent 端本地 SFTP 服务(可作为 `sftp` 插件)——后续版本。
- **P4 发版**:阶段F `VERSION=2.3.0` 统一 bump,双端部署全量回归 → 远程 Agent 升级 → 最终 push(按指示执行 `scripts/publish-github.sh` 同步公开版)。
