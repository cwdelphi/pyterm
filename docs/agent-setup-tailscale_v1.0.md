# Agent Tailscale 模式注册 + 远程 SSH 调试

**版本:** v1.0
**日期:** 2026-09-12

## 需求 1: Tailscale 模式 Agent 注册

### 最终效果

```
$ ./wragent
To authenticate, visit:
  https://portal.example.com/agent/s/abc123def456
```

### 流程

```
wragent 启动 (无 token)
  → 连接 server WS
  → 发送 {"type": "setup_start", "agent_id": "xxx"}
  → 服务器生成 sid, 存储会话
  → 回复 {"type": "setup_url", "url": "https://portal.example.com/agent/s/xxx"}
  → wragent 打印 URL
  → 用户浏览器打开 URL
  → 用户登录 (JWT)
  → 点击"注册此 Agent"
  → POST /api/webrtc/agents/setup {sid, agent_name, jwt}
  → 服务器创建 agent, 生成 auth_token
  → 通过 WS 推送 {"type": "setup_complete", "token": "xxx", "agent_id": "xxx"}
  → wragent 收到 token → 保存 config.json
  → wragent 发送 register → 连接成功
```

### 服务器端改动 (`app/api_isolated.py`)

| 改动 | 说明 |
|------|------|
| `_setup_sessions = {}` | 内存字典, `{sid: {ws, agent_id, created_at}}` |
| WS 消息 `setup_start` | wragent 发送 → 生成 sid → 回复 `setup_url` |
| `GET /agent/s/{sid}` | 返回 HTML 认证页面 |
| `POST /api/webrtc/agents/setup` | 验证 JWT + sid → 创建 agent → WS 推送 token |
| 清理定时器 | 30 分钟过期自动删除 |

### wragent 改动

| 文件 | 改动 |
|------|------|
| `main.go` | 检测 `AuthToken == ""` → setup 模式 |
| `websocket/client.go` | 发送 `setup_start`, 处理 `setup_url` / `setup_complete` |

### 前端改动

| 文件 | 说明 |
|------|------|
| `web/public/agent-setup.html` | 新建 ~80 行 HTML 认证页面 |
| `nginx/nginx.conf` | 添加 `/agent/s/` 路由 |

## 需求 2: 远程 Agent SSH 调试

### 浏览器端 `web/src/utils/webrtc.ts` 增加调试日志

| 位置 | 新增日志 |
|------|----------|
| `sendSshConnect()` | 打印 host/port/username |
| MSG_ERROR 处理 | 打印错误详情 |
| `onconnectionstatechange` | 打印 ICE 状态变化 |
| `detectConnType()` | 打印 candidate 详情 |
