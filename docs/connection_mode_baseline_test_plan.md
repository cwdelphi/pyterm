# SSH/SFTP 三种连接模式基线测试计划

> 版本：V1.0 | 日期：2026-09-12 | 状态：已实施

---

## 一、测试目标

验证 SSH 和 SFTP 文件管理在三种连接模式下的功能完整性和数据一致性：

1. **直连模式 (Direct)**：后端直接通过 asyncssh 连接远程 SSH/SFTP 服务器
2. **本地Agent模式 (Local Agent)**：通过 WebRTC DataChannel 连接到本机 wragent，再由 wragent 连接本地 SSH/SFTP
3. **远程Agent模式 (Remote Agent)**：通过 WebRTC DataChannel 连接到远程主机上的 wragent

---

## 二、架构概览

```
┌─────────────┐     WebSocket      ┌──────────┐     asyncssh     ┌──────────┐
│   浏览器     │ ──────────────────→│  后端API  │ ──────────────→│ 远程SSH  │
│  (前端UI)    │                    │ (FastAPI) │                │ 服务器   │
└──────┬──────┘                    └──────────┘                └──────────┘
       │                                                  直连模式链路
       │
       │     WebRTC DataChannel     ┌──────────┐     本地SSH      ┌──────────┐
       └───────────────────────────→│  wragent  │ ──────────────→│ 本地SSH  │
                                    │  (Go)     │                │ 服务器   │
                                    └──────────┘                └──────────┘
                                         ↑                        Agent模式链路
                                    (同机或远程)
```

---

## 三、测试层级

### L1: 单元测试 (pytest)

| 测试文件 | 用例数 | 覆盖范围 |
|---------|--------|---------|
| `test_connection_mode_direct.py` | 8 | 直连模式CRUD、字段验证、旧数据兼容、用户隔离 |
| `test_connection_mode_agent.py` | 8 | Agent模式CRUD、agent_id字段、Agent列表、隔离 |
| `test_connection_mode_sftp.py` | 8 | SFTP操作（list/read/write/mkdir/rename/delete/chmod） |
| `test_connection_mode_lifecycle.py` | 6 | 全生命周期：创建→切换→验证→删除、多次切换 |
| **合计** | **30** | |

### L2: E2E测试 (Playwright)

| 测试文件 | 用例数 | 覆盖范围 |
|---------|--------|---------|
| `connection-mode-baseline.spec.ts` | 15 | 直连模式UI、Agent模式UI、模式切换、兼容性、隔离 |
| `admin-panel.spec.ts` | 8 | 管理员面板：菜单可见性、用户CRUD、Agent CRUD、审计日志 |
| **合计** | **23** | |

### L3: 集成测试（已有）

| 测试文件 | 用例数 | 覆盖范围 |
|---------|--------|---------|
| `test_integration_real_ssh.py` | 6 | 真实SSH连接、SFTP操作 |
| `test_integration_sftp_client.py` | 6 | 真实SFTP API端到端 |
| `test_integration_connection_mode.py` | 2 | 模式集成验证 |

---

## 四、测试用例矩阵

### 4.1 直连模式 (Direct)

| 用例ID | 类型 | 描述 | 前置条件 | 预期结果 |
|--------|------|------|----------|----------|
| D-01 | 单元 | 添加connection_mode=direct连接 | 已登录 | 连接保存成功，connection_mode="direct" |
| D-02 | 单元 | 列表包含connection_mode字段 | 已有连接 | 每个连接都有connection_mode字段 |
| D-03 | 单元 | 默认connection_mode=direct | 不传mode | 默认值为"direct" |
| D-04 | 单元 | 更新connection_mode | 已有direct连接 | 可切换为agent |
| D-05 | 单元 | 更新时password空保留字段 | 已有连接 | 密码和mode都不丢失 |
| D-06 | 单元 | 旧数据兼容 | 旧格式连接 | 默认connection_mode="direct" |
| D-07 | 单元 | 用户隔离 | 两用户 | A的连接不在B列表 |
| D-08 | 单元 | SFTP API不受mode影响 | 任意连接 | SFTP操作正常 |
| CM-D01 | E2E | 新建直连SSH | 管理员登录 | 连接出现在侧栏 |
| CM-D02 | E2E | 编辑时mode持久化 | 已有直连 | 编辑表单显示"直连"active |
| CM-D07 | E2E | 状态栏显示"直连" | 打开终端 | 状态栏含"直连" |
| CM-D08 | E2E | 配置面板显示模式列 | 打开配置 | "模式"列可见 |

### 4.2 Agent模式

| 用例ID | 类型 | 描述 | 前置条件 | 预期结果 |
|--------|------|------|----------|----------|
| A-01 | 单元 | 添加connection_mode=agent+agent_id | 已登录 | 保存成功，字段正确 |
| A-02 | 单元 | 列表包含agent_id字段 | 有agent连接 | agent_id非空 |
| A-03 | 单元 | agent模式空agent_id | 无agent | 保存成功但agent_id="" |
| A-04 | 单元 | Admin Agent列表 | 管理员 | 返回agents数组 |
| A-05 | 单元 | Agent连接参数完整 | 有agent连接 | 含host/port/username/agent_id |
| A-06 | 单元 | 更新agent_id | 已有agent连接 | agent_id可更新 |
| A-07 | 单元 | agent→direct切换 | 已有agent连接 | agent_id清除 |
| A-08 | 单元 | Agent模式用户隔离 | 两用户 | 隔离有效 |
| CM-A01 | E2E | 新建Agent SSH | 管理员 | 连接出现在侧栏 |
| CM-A02 | E2E | Agent下拉框显示 | 打开表单 | 切Agent时下拉框出现 |
| CM-A06 | E2E | 状态栏显示"Agent" | 打开终端 | 状态栏含"Agent" |

### 4.3 模式切换

| 用例ID | 类型 | 描述 | 前置条件 | 预期结果 |
|--------|------|------|----------|----------|
| L-01 | 单元 | direct→agent→direct全周期 | 已登录 | 每步字段正确 |
| L-02 | 单元 | agent→direct生命周期 | 已登录 | agent_id正确清除 |
| L-03 | 单元 | 完整CRUD+模式切换 | 已登录 | 所有操作成功 |
| L-04 | 单元 | 所有连接含mode字段 | 多连接 | 100%包含 |
| L-05 | 单元 | 多次切换数据不丢失 | 已登录 | 密码保留、字段正确 |
| L-06 | 单元 | 非法mode值也被保存 | 已登录 | 向后兼容 |
| CM-S01 | E2E | 编辑时direct→agent | 有直连 | Agent下拉可见 |
| CM-S03 | E2E | 切换后持久化 | 有agent连接 | 刷新后mode正确 |
| CM-S04 | E2E | 不弹出模式选择弹窗 | 有连接 | 无.modal-mode |

### 4.4 向后兼容

| 用例ID | 类型 | 描述 | 前置条件 | 预期结果 |
|--------|------|------|----------|----------|
| CM-B01 | E2E | 旧连接默认direct | API创建无mode | connection_mode="direct" |

### 4.5 用户隔离

| 用例ID | 类型 | 描述 | 前置条件 | 预期结果 |
|--------|------|------|----------|----------|
| CM-I01 | E2E | 用户A连接对B不可见 | 两用户 | B看不到A的连接 |

### 4.6 管理员面板

| 用例ID | 类型 | 描述 | 前置条件 | 预期结果 |
|--------|------|------|----------|----------|
| AM-01 | E2E | 管理员看到系统管理 | 管理员登录 | 菜单可见 |
| AM-02 | E2E | 4个子Tab | 进入管理 | 用户/Agent/coturn/审计 |
| AM-03 | E2E | 用户列表加载 | 进入用户管理 | 表格有数据 |
| AM-04 | E2E | 创建用户 | 点击新建 | 用户出现在列表 |
| AM-05 | E2E | 编辑用户角色 | 有用户 | 角色可修改 |
| AM-08 | E2E | 不能禁用自己 | 管理员 | 操作被拒绝 |
| AM-09 | E2E | 删除用户 | 有测试用户 | 用户从列表消失 |
| AM-10 | E2E | Agent CRUD | Agent管理Tab | 创建→验证→删除 |
| AM-12 | E2E | 审计日志可查看 | 审计日志Tab | 日志表格可见 |

---

## 五、运行方式

### 后端单元测试

```bash
# 在Docker容器内运行
docker exec pyterm_md python -m pytest \
  app/tests/test_connection_mode_direct.py \
  app/tests/test_connection_mode_agent.py \
  app/tests/test_connection_mode_sftp.py \
  app/tests/test_connection_mode_lifecycle.py \
  -v
```

### E2E测试

```bash
cd autotest
npx playwright test tests/connection-mode-baseline.spec.ts
npx playwright test tests/admin-panel.spec.ts
```

---

## 六、通过标准

| 指标 | 目标 |
|------|------|
| 后端单元测试通过率 | 100% (30/30) |
| E2E测试通过率 | ≥90% (Agent模式可优雅跳过) |
| 代码覆盖率（connection_mode相关） | ≥80% |

---

## 七、已知限制

1. **Agent模式E2E**：需要运行中的wragent，无Agent时跳过
2. **WebRTC信令测试**：依赖coturn/STUN服务器
3. **SFTP测试**：mock环境无法完全模拟真实SFTP错误码
