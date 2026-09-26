# wragent 配置管理方案 v1.0

> 版本: v1.0  
> 日期: 2026-09-18  
> 状态: 执行中

## 目标

将 wragent 的 `config.json` 精简为仅保留服务器访问地址和 token，其他参数移到服务端进行管理。

## 当前配置结构

```json
{
  "server_url": "wss://203.0.113.10:5588/api/ws/webrtc",
  "agent_id": "local-agent",
  "auth_token": "xxxxxxxxxxxx",
  "ws_reconnect_interval": 5,
  "ws_heartbeat_interval": 30,
  "ice_cooldown": 10,
  "tunnel_local_port": 0,
  "tunnel_target_addr": "",
  "tunnel_agent_id": ""
}
```

## 精简后本地 config.json

```json
{
  "server_url": "wss://203.0.113.10:5588/api/ws/webrtc",
  "agent_id": "local-agent",
  "auth_token": "xxxxxxxxxxxx"
}
```

## 配置字段分配

| 字段 | 类型 | 默认值 | 管理位置 |
|------|------|--------|----------|
| `server_url` | string | `ws://localhost:5588/api/ws/webrtc` | 本地 |
| `agent_id` | string | `agent-001` | 本地 |
| `auth_token` | string | `""` | 本地 |
| `ws_reconnect_interval` | int | `5` | 服务端 |
| `ws_heartbeat_interval` | int | `30` | 服务端 |
| `ice_cooldown` | int | `10` | 服务端 |
| `tunnel_local_port` | int | `0` | 本地(可选) |
| `tunnel_target_addr` | string | `""` | 本地(可选) |
| `tunnel_agent_id` | string | `""` | 本地(可选) |

## 实施步骤

### Step 1: 数据库扩展

在 `agents` 表新增 `config_json` 字段:

```sql
ALTER TABLE agents ADD COLUMN config_json TEXT DEFAULT '{}';
```

### Step 2: API 接口

| 接口 | 方法 | 说明 |
|------|------|------|
| `/api/agents/{id}/config` | GET | 获取 Agent 配置 |
| `/api/agents/{id}/config` | PUT | 更新 Agent 配置 |

### Step 3: wragent 启动流程

```
启动 → 加载本地 config.json → 连接 WebSocket → 注册成功
→ 服务端在 register_success 中附带配置 → 合并覆盖默认值
```

### Step 4: 配置合并优先级

1. 本地 config.json 显式设置的值 (最高优先级)
2. 服务端下发的配置值
3. 代码默认值

## UX 界面设计

### Agent 配置管理页面

```
┌─────────────────────────────────────────────────────┐
│  Agent 管理 > local-agent                    [编辑]  │
├─────────────────────────────────────────────────────┤
│  [基本信息]  [连接配置]  [隧道配置]  [日志]          │
│  ─────────  ─────────                             │
│                                                     │
│  ┌─ 连接配置 ─────────────────────────────────────┐ │
│  │                                                 │ │
│  │  WebSocket 重连间隔                             │ │
│  │  [====5 秒========================]            │ │
│  │  范围: 1-60 秒，默认 5 秒                        │ │
│  │                                                 │ │
│  │  心跳间隔                                       │ │
│  │  [====30 秒=======================]            │ │
│  │  范围: 5-120 秒，默认 30 秒                      │ │
│  │                                                 │ │
│  │  ICE 收集超时                                    │ │
│  │  [====10 秒=======================]            │ │
│  │  范围: 3-30 秒，默认 10 秒                       │ │
│  │                                                 │ │
│  │           [恢复默认]              [保存配置]     │ │
│  └─────────────────────────────────────────────────┘ │
│                                                     │
│  ⚠ 修改后将在 Agent 下次重连时生效                    │
└─────────────────────────────────────────────────────┘
```

## 向后兼容

- 本地 config.json 中已有值的字段优先级高于服务端
- 服务端返回的配置仅覆盖未在本地显式设置的字段
- 如果本地 config.json 不存在某字段，使用服务端值；若服务端也无，使用代码默认值

## 变更记录

| 版本 | 日期 | 变更内容 |
|------|------|----------|
| v1.0 | 2026-09-18 | 初始方案 |
