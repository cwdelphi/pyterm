# TCP 隧道测试方案 v1.4

> 日期: 2026-09-18
> 状态: 待执行

## 拓扑

```
用户笔记本
  │ SSH root/cw
  ▼
203.0.113.31 ──wragent入口(:18422)──WebRTC──▶ 192.0.2.50 remote-agent ──TCP──▶ 192.0.2.39:22
```

| 节点 | 地址 | 角色 | SSH |
|------|------|------|-----|
| 入口 | 203.0.113.31 | wragent test-tunnel, 监听 :18422 | root / cw |
| 出口 | 192.0.2.50 | remote-agent（已部署） | root / cw |
| 目标 | 192.0.2.39 | SSH 服务 (22端口) | root / change_me_pass |

## 测试用例

### Phase 0: 管理后台

| # | 用例 | 操作 | 预期 | 状态 |
|---|------|------|------|------|
| A1 | 新建 Agent | Agent管理 → ＋新建 → ID=`test-tunnel`, 名称=`隧道入口测试` | 创建成功，弹出 Token | |
| A2 | 复制 Token | 弹窗点击「复制」 | 剪贴板有 Token | |
| A3 | Token 遮罩 | 表格中 Token 列 | 显示 `••••••••` | |
| A4 | Token 显示/复制 | 点击 👁 和 📋 | 切换可见 + 复制成功 | |
| B1 | 配置生成 | 点击 `test-tunnel` 行「配置」按钮 | 弹窗显示 JSON | |
| B2 | 配置内容 | 检查 JSON 字段 | server_url/agent_id/auth_token 正确 | |
| B3 | 复制配置 | 点击「📋 复制配置」 | 剪贴板有完整 JSON | |
| C1 | 程序下载 | 点击「📥 程序下载」 | 显示 wragent + wrgateway 卡片 | |
| C2 | 下载 wragent Linux | 点击下载 | 文件 ~11M | |

### Phase 1: 部署 203.0.113.31

| # | 用例 | 操作 | 预期 | 状态 |
|---|------|------|------|------|
| D1 | 上传二进制 | scp wragent-linux-amd64 root@203.0.113.31:/usr/local/bin/wragent | 成功 | |
| D2 | 写入 config.json | 用 B3 复制的 JSON + 添加隧道字段 | 文件正确 | |
| D3 | 启动 | ./wragent -config config.json | 日志「注册成功」+「隧道模式」+「WebRTC隧道通道已建立」 | |
| D4 | 在线确认 | 管理后台 test-tunnel | 显示「在线」 | |

config.json:
```json
{
  "server_url": "wss://domain:5588/api/ws/webrtc",
  "agent_id": "test-tunnel",
  "auth_token": "<从管理后台复制>",
  "ws_reconnect_interval": 5,
  "ws_heartbeat_interval": 30,
  "ice_cooldown": 10,
  "tunnel_local_port": 18422,
  "tunnel_target_addr": "192.0.2.39:22",
  "tunnel_agent_id": "remote-agent"
}
```

### Phase 2: 基础隧道

| # | 用例 | 操作 | 预期 | 状态 |
|---|------|------|------|------|
| E1 | SSH 连接 | ssh -o StrictHostKeyChecking=no -p 18422 root@localhost（密码 change_me_pass） | 连接成功 | |
| E2 | 执行命令 | hostname && whoami && ip addr show | 返回 192.0.2.39 信息 | |
| E3 | 断开 | exit | 日志「桥接结束」 | |
| E4 | 重复 5 次 | 连接/断开 ×5 | 每次成功 | |

### Phase 3: 多路复用

| # | 用例 | 操作 | 预期 | 状态 |
|---|------|------|------|------|
| F1 | 并发 3 连接 | 3 个终端 SSH localhost:18422 | 均成功 | |
| F2 | 并发数据 | 分别执行 top / df -h / ls -la | 数据独立 | |
| F3 | 部分断开 | 关闭其中一个 | 其余不受影响 | |

### Phase 4: 自动重连

| # | 用例 | 操作 | 预期 | 状态 |
|---|------|------|------|------|
| G1 | 断线 | docker stop pyterm_wragent | 日志「等待自动重连...」 | |
| G2 | 重连 | docker start pyterm_wragent | 日志「WebRTC隧道通道已建立」 | |
| G3 | 重连后可用 | SSH localhost:18422 | 成功 | |
| G4 | 断线期间 | 断线时 SSH | 阻塞不崩溃 | |

### Phase 5: 异常

| # | 用例 | 操作 | 预期 | 状态 |
|---|------|------|------|------|
| H1 | Token 错误 | 用错误 token 启动 | 注册失败 | |
| H2 | 目标离线 | remote-agent 停掉后 SSH | 连接超时/拒绝 | |

## 执行顺序

Phase 0 → Phase 1 → Phase 2 → Phase 3 → Phase 4 → Phase 5
