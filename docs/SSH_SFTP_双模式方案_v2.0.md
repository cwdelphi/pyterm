# pyterm SSH/SFTP 双模式方案 v2.0

> 日期: 2026-09-10
> 状态: 执行中

## 核心设计决策

| 决策点 | 结论 |
|--------|------|
| wragent持有目标SSH信息？ | **否** — 浏览器通过DataChannel传入 |
| WebRTC是独立连接项？ | **否** — SSH和文件两个子项，WebRTC只是连接方式 |
| Agent选择时机 | 新建连接时选择 |
| Agent管理 | 管理员后台CRUD + token生成 |
| wragent并发 | 50个连接/实例 |
| wragent认证 | WebSocket连接后首条消息传token |
| SFTP通道 | 复用ssh-terminal DataChannel |

## 架构

```
浏览器 → direct模式 → WS /ws/ssh → asyncssh → 目标SSH
浏览器 → agent模式  → WS /ws/webrtc → 信令 → WebRTC P2P → wragent → 目标SSH
```

## DataChannel协议

| 前缀 | 类型 | 方向 |
|------|------|------|
| 0x00 | 终端数据 | 双向 |
| 0x01 | SSH连接指令 | 浏览器→wragent |
| 0x02 | resize | 浏览器→wragent |
| 0x10 | SFTP请求 | 浏览器→wragent |
| 0x11 | SFTP响应 | wragent→浏览器 |
| 0xFF | 错误 | wragent→浏览器 |

## 实现步骤

| # | 任务 | 文件 | 复杂度 |
|---|------|------|--------|
| 1 | SshConn模型添加connection_mode/agent_id | models.py, api_isolated.py, api.ts | 低 |
| 2 | Agent CRUD admin API + token生成 | auth_api.py | 中 |
| 3 | wragent token认证改为消息体传token | websocket/client.go, api_isolated.py | 低 |
| 4 | wragent删除target_ssh_*，接收ssh_connect | config.go, signal.go, main.go | 高 |
| 5 | wragent添加SFTP handler (pkg/sftp) | signal.go, go.mod | 高 |
| 6 | WebRTCManager支持消息前缀+SFTP | utils/webrtc.ts | 中 |
| 7 | SshManager新建表单Agent选择器+列表修正 | SshManager.vue | 高 |
| 8 | SshFileBrowser Agent模式SFTP | SshFileBrowser.vue | 高 |
| 9 | AgentManager管理后台 | AgentManager.vue | 高 |
| 10 | 测试直连模式 | autotest/ | 中 |
| 11 | 测试Agent模式 | autotest/ | 高 |
