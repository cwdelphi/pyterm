# TCP 隧道测试方案 v1.5（执行结果）

> 日期: 2026-09-18
> 状态: 全部通过 ✅

## 拓扑

```
用户笔记本
  │ SSH root/cw
  ▼
203.0.113.31 ──wragent入口(:18422)──WebRTC──▶ 192.0.2.50 remote-agent ──TCP──▶ 192.0.2.39:22
```

| 节点 | 地址 | 角色 | SSH | 版本 |
|------|------|------|-----|------|
| 入口 | 203.0.113.31 | wragent test-tunnel, 监听 :18422 | root / cw | v1.1.8 |
| 出口 | 192.0.2.50 | remote-agent（systemd wragent.service） | root / change_me_pass | v1.1.8 |
| 目标 | 192.0.2.39 | SSH 服务 (22端口) | root / change_me_pass | - |

## 测试结果

### Phase 0: 管理后台 ✅

| # | 用例 | 状态 | 备注 |
|---|------|------|------|
| A1 | 新建 Agent test-tunnel | ✅ | Token: a11db84e... |
| A2 | 复制 Token | ✅ | |
| A3 | Token 遮罩 | ✅ | |
| A4 | Token 显示/复制 | ✅ | |
| B1-B3 | 配置生成/内容/复制 | ✅ | |
| C1-C2 | 程序下载 | ✅ | |

### Phase 1: 部署 203.0.113.31 ✅

| # | 用例 | 状态 | 备注 |
|---|------|------|------|
| D1 | 上传二进制 | ✅ | scp 11MB |
| D2 | 写入 config.json | ✅ | 含 tunnel_agent_id=remote-agent |
| D3 | 启动 | ✅ | 「注册成功」+「WebRTC隧道通道已建立」 |
| D4 | 在线确认 | ✅ | 管理后台 test-tunnel 在线 |

### Phase 2: 基础隧道 ✅

| # | 用例 | 状态 | 备注 |
|---|------|------|------|
| E1 | SSH 连接 :18422 | ✅ | hostname=ppy (192.0.2.39) |
| E2 | 执行命令 | ✅ | hostname/whoami/os-release/uptime |
| E3 | 断开 | ✅ | 日志「桥接结束」 |
| E4 | 重复 5 次 | ✅ | connID=1~8 均成功 |

### Phase 3: 多路复用 ✅

| # | 用例 | 状态 | 备注 |
|---|------|------|------|
| F1 | 并发 3 连接 | ✅ | connID=2,3 同时桥接 |
| F2 | 并发数据 | ✅ | hostname/top + df + ls 独立返回 |
| F3 | 部分断开 | ✅ | kill 中间会话，其余不受影响 |

### Phase 4: 自动重连 ✅（v1.1.8 看门狗）

| # | 用例 | 状态 | 备注 |
|---|------|------|------|
| G1 | 断线 | ✅ | DC 关闭 → 「等待自动重连...」 |
| G2 | 重连 | ✅ | 看门狗 10s 周期重试 → 「WebRTC隧道通道已建立」 |
| G3 | 重连后可用 | ✅ | SSH 成功 |
| G4 | 断线期间 SSH | ✅ | 已建立会话正常断开，wragent 进程不崩溃 |

### Phase 5: 异常 ✅

| # | 用例 | 状态 | 备注 |
|---|------|------|------|
| H1 | Token 错误 | ✅ | 注册失败 → 进入 Tailscale 认证模式，不建隧道 |
| H2 | 目标离线 | ✅ | SSH 快速 Connection reset（0.08s） |

## 关键修复记录（v1.1.1 → v1.1.8）

1. **Message 字段兼容**：`SDP`/`Candidate` 字段加入 ws.Message，兼容服务端 offer/candidate 转发格式
2. **connect_tunnel 数据解析**：服务端从 `data` 嵌套读取 target_agent_id/token
3. **ICE 候选缓冲**：remote description 未设置前缓冲候选，answer 到达后刷新
4. **findTunnelDataChannel 修复**：从 stub 改为返回全局 tunnelDC
5. **自动重连看门狗**：隧道 DC 关闭后 2s 内重连 + 10s 周期健康检查（目标离线时持续重试直到成功）
6. **远端退出 agent 修复**：停掉 192.0.2.50 上旧的 Docker ppy_wragent（双注册冲突），使用 systemd 管理的 /opt/wragent

## 部署清单

- 入口 203.0.113.31：`/usr/local/bin/wragent` + `/root/config.json`（nohup 运行）
- 出口 203.0.113.30：`/opt/wragent/wragent` + systemd `wragent.service`
- 目标 192.0.2.39:22（SSH root/change_me_pass）
