# 连接诊断系统 v2.0.0

> 版本: 2.0.0 | 日期: 2026-09-17 | 替代: latency-analysis-v1.0.0/v1.1.0

## 变更摘要

将「延迟分析」升级为「连接诊断」系统：每次 SSH/VNC/RDP 连接（含失败）生成完整诊断记录，覆盖全生命周期阶段时间戳+ICE候选+Agent侧数据。管理员在「系统管理→连接诊断」查看全局数据。

## 核心问题

1. `reportLatency()` 在 `latencyDataOk` 赋值前调用，被 early return 拦截
2. 仅 DataChannel 打开成功时上报，失败连接无数据
3. "延迟分析"是顶层菜单，应移入系统管理
4. Agent/Gateway 无任何诊断上报
5. `latency_records` 无 `room_id`，无法按连接分组

---

## 数据库

### 主表 connection_diagnostics

```sql
DROP TABLE IF EXISTS latency_records;
DROP TABLE IF EXISTS latency_daily;
DROP TABLE IF EXISTS latency_monthly;

CREATE TABLE connection_diagnostics (
  id              INT AUTO_INCREMENT PRIMARY KEY,
  user_id         VARCHAR(64)  NOT NULL,
  room_id         VARCHAR(128) NOT NULL,
  conn_config_id  VARCHAR(64)  DEFAULT '',
  conn_name       VARCHAR(255) DEFAULT '',
  conn_type       VARCHAR(32)  NOT NULL,
  host            VARCHAR(255) DEFAULT '',
  port            INT          DEFAULT 0,
  username        VARCHAR(255) DEFAULT '',
  agent_id        VARCHAR(64)  DEFAULT '',
  agent_name      VARCHAR(255) DEFAULT '',
  gateway_id      VARCHAR(64)  DEFAULT '',
  gateway_name    VARCHAR(255) DEFAULT '',
  path_mode       VARCHAR(32)  DEFAULT 'direct',
  signal_mode     VARCHAR(32)  DEFAULT 'auto',
  detected_type   VARCHAR(32)  DEFAULT '',
  t_start         DOUBLE DEFAULT 0,
  t_ws_open       DOUBLE DEFAULT 0,
  t_signal_ok     DOUBLE DEFAULT 0,
  t_rtc_connected DOUBLE DEFAULT 0,
  t_dc_open       DOUBLE DEFAULT 0,
  t_first_data    DOUBLE DEFAULT 0,
  t_error         DOUBLE DEFAULT 0,
  duration_ws     DOUBLE DEFAULT 0,
  duration_signal DOUBLE DEFAULT 0,
  duration_ice    DOUBLE DEFAULT 0,
  duration_dc     DOUBLE DEFAULT 0,
  duration_data   DOUBLE DEFAULT 0,
  duration_total  DOUBLE DEFAULT 0,
  success         TINYINT(1) DEFAULT 1,
  error_stage     VARCHAR(32)  DEFAULT '',
  error_msg       TEXT         DEFAULT '',
  close_reason    VARCHAR(64)  DEFAULT '',
  browser         VARCHAR(32)  DEFAULT '',
  os              VARCHAR(64)  DEFAULT '',
  screen_size     VARCHAR(32)  DEFAULT '',
  ice_local_type  VARCHAR(32)  DEFAULT '',
  ice_remote_type VARCHAR(32)  DEFAULT '',
  ice_local_addr  VARCHAR(128) DEFAULT '',
  ice_remote_addr VARCHAR(128) DEFAULT '',
  connected_at    VARCHAR(64)  DEFAULT '',
  closed_at       VARCHAR(64)  DEFAULT '',
  session_seconds DOUBLE DEFAULT 0,
  agent_connect_ms  DOUBLE DEFAULT 0,
  agent_ssh_host    VARCHAR(255) DEFAULT '',
  agent_ssh_port    INT DEFAULT 0,
  created_at      VARCHAR(64) NOT NULL,
  updated_at      VARCHAR(64) NOT NULL,

  INDEX idx_diag_user_created (user_id, created_at),
  INDEX idx_diag_room (room_id),
  INDEX idx_diag_agent (agent_id, created_at),
  INDEX idx_diag_type (conn_type, created_at),
  INDEX idx_diag_success (success, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

### 聚合表 diag_daily / diag_monthly

```sql
CREATE TABLE diag_daily (
  id INT AUTO_INCREMENT PRIMARY KEY,
  user_id VARCHAR(64) NOT NULL, conn_type VARCHAR(32) NOT NULL,
  detected_type VARCHAR(32) NOT NULL DEFAULT '', date VARCHAR(10) NOT NULL,
  total_count INT DEFAULT 0, success_count INT DEFAULT 0, fail_count INT DEFAULT 0,
  avg_total DOUBLE DEFAULT 0, avg_ws DOUBLE DEFAULT 0, avg_signal DOUBLE DEFAULT 0,
  avg_ice DOUBLE DEFAULT 0, avg_dc DOUBLE DEFAULT 0, avg_data DOUBLE DEFAULT 0,
  p50_total DOUBLE DEFAULT 0, p95_total DOUBLE DEFAULT 0, p99_total DOUBLE DEFAULT 0,
  min_total DOUBLE DEFAULT 0, max_total DOUBLE DEFAULT 0,
  created_at VARCHAR(64) NOT NULL,
  UNIQUE INDEX idx_daily_uq (user_id, conn_type, detected_type, date)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE diag_monthly (
  id INT AUTO_INCREMENT PRIMARY KEY,
  user_id VARCHAR(64) NOT NULL, conn_type VARCHAR(32) NOT NULL,
  detected_type VARCHAR(32) NOT NULL DEFAULT '', month VARCHAR(7) NOT NULL,
  total_count INT DEFAULT 0, success_count INT DEFAULT 0, fail_count INT DEFAULT 0,
  avg_total DOUBLE DEFAULT 0, avg_ws DOUBLE DEFAULT 0, avg_signal DOUBLE DEFAULT 0,
  avg_ice DOUBLE DEFAULT 0, avg_dc DOUBLE DEFAULT 0, avg_data DOUBLE DEFAULT 0,
  p50_total DOUBLE DEFAULT 0, p95_total DOUBLE DEFAULT 0, p99_total DOUBLE DEFAULT 0,
  min_total DOUBLE DEFAULT 0, max_total DOUBLE DEFAULT 0,
  created_at VARCHAR(64) NOT NULL,
  UNIQUE INDEX idx_monthly_uq (user_id, conn_type, detected_type, month)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

---

## 后端 API

| 接口 | 方法 | 说明 | 权限 |
|---|---|---|---|
| `/api/diagnostics/report` | POST | 上报（upsert by room_id） | 登录用户 |
| `/api/diagnostics/records` | POST | 分页查询 | 用户(自己)/管理员(全部) |
| `/api/diagnostics/detail` | POST | 单条详情 | 登录用户 |
| `/api/diagnostics/stats` | POST | 聚合统计 | 登录用户 |
| `/api/diagnostics/stages` | POST | 瀑布图数据 | 登录用户 |
| `/api/diagnostics/trend` | POST | 按天趋势 | 登录用户 |
| `/api/diagnostics/matrix` | POST | 模式对比 | 登录用户 |
| `/api/diagnostics/admin/overview` | POST | 管理员全局概览 | 管理员 |
| `/api/diagnostics/aggregate` | POST | 手动聚合 | 管理员 |

---

## 浏览器端

### 字段替换
删除 latency* 字段，新增 diag 对象（start/wsOpen/signalOk/rtcConnected/dcOpen/firstData/error + 元数据 + ICE信息）

### 上报触发点
| 时机 | success |
|---|---|
| DataChannel onopen | 1 |
| 首条终端数据 | 1 |
| WS连接失败 | 0 |
| 信令错误 | 0 |
| ICE失败 | 0 |
| 连接关闭 | 1（更新session时长） |

### 公开方法
- `setConnMeta({connConfigId, connName, host, port, username, agentName, gatewayName})`
- `markFirstData()` — 首数据到达时触发上报

---

## Agent 端

新增 `MsgDiagnostics = 0xFE`，SSH连接成功后通过DataChannel发送 `agent_connect_ms`/`agent_ssh_host`/`agent_ssh_port`。浏览器端合并上报。

---

## 前端界面

位置：系统管理→侧边栏→连接诊断

5个子Tab：
1. **概览** — 统计卡片+最近记录表格+分页
2. **瀑布图** — 阶段延迟条形图+按路径/类型分组
3. **趋势** — 按天趋势表格
4. **模式对比** — 类型/路径/信号模式分布+交叉表
5. **详情弹窗** — 单条完整诊断信息+瀑布图+ICE候选+Agent报告

---

## 清理

| 文件 | 操作 |
|---|---|
| TopBar.vue | 删除 latency 菜单项 |
| App.vue | 删除 LatencyAnalysis 相关 |
| api.ts | 删除 latency* 方法 |
| database.py | 删除 Latency* 模型 |
| api_latency.py | 重命名为 api_diagnostics.py |
| LatencyAnalysis.vue | 删除 |
| DiagnosisPanel.vue | 新建 |

## 实施顺序

1. 数据库建表
2. ORM模型
3. 后端API
4. 路由注册
5. 前端API方法
6. 浏览器插桩
7. SshManager元数据
8. DiagnosisPanel组件
9. AdminPanel菜单
10. 清理旧代码
11. Agent端插桩
12. 构建+部署+测试
