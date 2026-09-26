# 移除 local-001 假 Agent 与测试遗留 Agent

> 版本: v1.0 | 日期: 2026-09-24 | 状态: 已实施

## 背景

`app/database.py` 的 `init_db()` 曾在每次启动时若不存在则自动创建 `local-001`（「本地连接」），并将 `agent_id` 为空的 `ssh_connections` 绑到该 id。该 Agent 永远不会有 WebSocket 注册，在 Agent 列表中恒为 offline，造成「每次测试/重启又冒出 local-001」的噪音。

另有测试遗留 `wragent-2026092001`（无连接引用）长期占列表。

## 变更

### 1. `app/database.py` `init_db()` 迁移块

**删除：**

- 自动 `INSERT` `id="local-001"` 的 Agent 创建逻辑
- `UPDATE ssh_connections SET agent_id='local-001' WHERE agent_id='' OR agent_id IS NULL`

**保留：**

- `connection_mode='direct' → 'agent'` 的模式迁移（与 local-001 无关）
- `local-gateway` 自动创建（独立逻辑）

**新增幂等清理（防再出现）：**

- 若库中仍有 `local-001`：先把引用它的 `ssh_connections.agent_id` 置空，再 `DELETE` 该 Agent
- 若库中仍有 `wragent-2026092001` 且无任何连接引用：`DELETE` 该 Agent

### 2. 当前库

- 执行时已确认 `local-001` 不在 `agents` 表、无连接引用
- 已手工删除 `wragent-2026092001`

## 影响

- 启动后 Agent 列表不再出现「本地连接 / local-001」
- 测试夹具使用的是 `local-agent`（`autotest/tests/fixtures/test-data.ts`），不依赖 `local-001`
- 历史 `architecture_optimization_v1.0.md` 中记载的 local-001 迁移设计已废弃，见本文

## 验证

```bash
python3 -m py_compile app/database.py
docker restart pyterm_md
# 确认 agents 列表无 local-001 / wragent-2026092001
```
