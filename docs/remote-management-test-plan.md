# 远程管理配置测试方案 v1.0

## 1. 测试目标

在不改变现有远程管理配置（Agent/Gateway/Coturn/Nginx等）的前提下，对有网关的两个配置进行端到端测试，并确保测试后配置可复原。

## 2. 测试范围

### 2.1 两个网关配置

| 连接ID | 名称 | Agent | Gateway | 测试端口 |
|--------|------|-------|---------|----------|
| e46ff0d3 | 本地网关+本地Agent | local-agent | local-gateway | 2222 |
| c2c127cd | 本地网关+远程Agent | remote-agent | local-gateway | 2222 |

### 2.2 测试阶段

| Phase | 测试文件 | 用例数 | 说明 |
|-------|----------|--------|------|
| 0 | infra.spec.ts | 5 | 基础设施验证（登录/API/页面） |
| 1 | gateway-online.spec.ts | 5 | 网关生命周期（在线状态/配置/Token/列表/页面） |
| 4 | gw-local-agent-ssh.spec.ts | 3 | 本地网关+本地Agent SSH |
| 5 | gw-local-agent-sftp.spec.ts | 2+ | 本地网关+本地Agent SFTP |
| 6 | gw-remote-agent-ssh.spec.ts | 3 | 本地网关+远程Agent SSH |
| 7 | gw-remote-agent-sftp.spec.ts | 2 | 本地网关+远程Agent SFTP |

## 3. 配置文件清单

### 3.1 远程管理核心配置

| 文件 | 用途 | 关键配置项 |
|------|------|-----------|
| `.env` | 全局环境变量 | SERVER_PUBLIC_IP, TURN_SECRET, JWT_SECRET |
| `wragent/config.json` | local-agent连接配置 | server_url, agent_id, auth_token |
| `wragent/config/remote-agent-config.json` | remote-agent配置模板 | server_url, agent_id, auth_token |
| `wrgateway/config.json` | 本地网关配置 | listen, server_url, gateway_id, gateway_token |
| `config/turnserver.conf` | coturn配置 | listening-port, tls-listening-port, external-ip, static-auth-secret |
| `nginx/nginx.conf` | 反向代理 | SSL终止, WebSocket代理(仅端口5588) |

### 3.2 数据库状态

- **agents表**: local-agent, remote-agent
- **gateways表**: local-gateway
- **ssh_connections表**: 4条基线连接（含2条网关连接）

### 3.3 服务拓扑

```
浏览器 --(WSS/SSL:5588)--> nginx --> md(FastAPI:5588)
                                       |
                    WebSocket信令(/api/ws/webrtc)
                       |                |
                  wragent(Host)    wrgateway(:5599)
                       |                |
                  WebRTC(DC)       WebRTC(DC)
                       |                |
                  test-ssh-server(:2222)
                  
              coturn(:19302/:5349) <-- ICE中继
```

## 4. 测试流程

### 4.1 执行流程

```bash
# 1. 部署初始化（建立基线）
bash scripts/deploy-init.sh

# 2. 测试初始化（恢复基线+准备数据）
bash scripts/test-init.sh

# 3. 执行测试
cd autotest
npx playwright test \
  --project=phase-0-infra \
  --project=phase-1-gw-lifecycle \
  --project=phase-4-gw-local-ssh \
  --project=phase-5-gw-local-sftp \
  --project=phase-6-gw-remote-ssh \
  --project=phase-7-gw-remote-sftp

# 4. 测试复原
bash scripts/test-restore.sh
```

### 4.2 测试用例表格

| # | 编号 | 阶段 | 用例 | 前置条件 | 操作 | 预期 |
|---|------|------|------|----------|------|------|
| 1 | P0-01 | 0 | 登录页面加载 | nginx运行 | GET / | 登录表单可见 |
| 2 | P0-02 | 0 | 登录成功 | admin用户存在 | POST /api/auth/login | 返回token |
| 3 | P0-03 | 0 | SSH管理页面 | 登录成功 | 点击远程管理 | SSH视图可见 |
| 4 | P0-04 | 0 | 管理后台页面 | 登录成功 | 点击系统管理 | 管理视图可见 |
| 5 | P0-05 | 0 | API登录获取Token | admin用户 | POST /api/auth/login | 返回JWT |
| 6 | GW-01 | 1 | 网关在线状态 | wrgateway运行 | GET /api/admin/gateways | local-gateway.online=true |
| 7 | GW-02 | 1 | 网关配置信息 | GW-01通过 | 同上 | id=name=url正确 |
| 8 | GW-03 | 1 | 网关Token验证 | GW-01通过 | 同上 | token长度>10 |
| 9 | GW-04 | 1 | 网关在线列表 | GW-01通过 | GET /api/webrtc/gateways | status=online |
| 10 | GW-05 | 1 | 管理后台页面 | GW-01通过 | 管理后台→网关tab | 表格可见 |
| 11 | C-S-01 | 4 | 本地网关SSH侧栏 | local-agent+gateway在线 | 远程管理页面 | 卡片可见 |
| 12 | C-S-02 | 4 | 通过网关打开终端 | C-S-01通过 | 点击SSH按钮 | xterm可见 |
| 13 | C-S-03 | 4 | 通过网关终端交互 | C-S-02通过 | echo测试 | 输出匹配 |
| 14 | - | 5 | 本地网关SFTP打开 | local-agent+gateway在线 | 点击文件按钮 | SFB可见 |
| 15 | - | 5 | 本地网关SFTP文件列表 | 上一步通过 | 等待加载 | 列表有内容 |
| 16 | D-S-01 | 6 | 远程网关SSH侧栏 | remote-agent+gateway在线 | 远程管理页面 | 卡片可见 |
| 17 | D-S-02 | 6 | 通过远程网关终端 | D-S-01通过 | 点击SSH按钮 | xterm可见 |
| 18 | D-S-03 | 6 | 通过远程网关交互 | D-S-02通过 | echo测试 | 输出匹配 |
| 19 | D-F-01 | 7 | 远程网关SFTP打开 | remote-agent+gateway在线 | 点击文件按钮 | SFB可见 |
| 20 | D-F-02 | 7 | 远程网关SFTP列表 | D-F-01通过 | 等待加载 | 列表有内容 |

## 5. 脚本说明

| 脚本 | 功能 | 时机 |
|------|------|------|
| deploy-init.sh | 建立基线快照 | 首次部署/配置变更后 |
| test-init.sh | 测试前初始化 | 每次测试前 |
| test-restore.sh | 测试后复原 | 每次测试后 |
| config-snapshot.json | 基线数据 | deploy-init生成 |

## 6. 风险与缓解

| 风险 | 影响 | 缓解 |
|------|------|------|
| remote-agent离线 | Phase 6/7无法执行 | test-init前检查，离线则跳过 |
| 测试修改Agent/Gateway | 复原失败 | 复原脚本备份+恢复token |
| wrgateway重启延迟 | 测试超时 | 重启后等待15秒 |
| DB与config token不一致 | 认证失败 | deploy-init校验一致性 |

## 7. 复原验证标准

测试复原后需满足：
1. `ssh_connections` 表仅包含4条基线连接
2. `agents` 表中token与config文件一致
3. `gateways` 表中token与config文件一致
4. wragent/wrgateway容器已重启并在线
5. 无测试残留数据（以c_s_/d_s_/d_f_等前缀的连接）
