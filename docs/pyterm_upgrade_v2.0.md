# PyTerm 全面升级方案 v2.0

## 需求总览 (16项)

| # | 需求 | 类型 | 优先级 |
|---|------|------|--------|
| 1 | 审计日志修复 — 有模型无写入代码 | Bug | 高 |
| 2 | VNC 修复 — WebRTC通但noVNC空白 | Bug | 高 |
| 3 | 网关状态修复 — 总是灰色 | Bug | 高 |
| 4 | 登录首页面改为远程管理 | Bug | 高 |
| 5 | SshConnection 表加 RDP 字段 | 数据模型 | 中 |
| 6 | 后端 API 支持 RDP 字段 | 数据模型 | 中 |
| 7 | 用户批量删除 API | 数据模型 | 中 |
| 8 | 权限调整: SSH→远程管理, user+agent:manage | 数据模型 | 低 |
| 9 | 左侧卡片: 单列列表, 类型色块, 可折叠分组, 网关图标右上角 | 前端 | 高 |
| 10 | 新建配置: 三选一, 动态字段 | 前端 | 高 |
| 11 | 配置管理: 卡片网格, 按类型分组可折叠 | 前端 | 中 |
| 12 | Agent/网关编辑弹窗: Token+复制+共享 | 前端 | 中 |
| 13 | 网关: 共享列, 操作列精简 | 前端 | 中 |
| 14 | 系统管理: 密码管理页面 | 前端 | 低 |
| 15 | 用户管理: 批量删除 | 前端 | 中 |
| 16 | 操作按钮色块统一 + 角色权限显示名称更新 | 前端 | 低 |

## 类型体系

| 类型 | connection_type | 功能入口 |
|------|----------------|----------|
| SSH/FILE | ssh | SSH终端 + SFTP文件管理 (一配置两入口) |
| VNC | vnc | 桌面远程 |
| RDP | rdp | Windows远程桌面 (预留不实现) |

## 左侧远程管理列表

- 单列卡片列表, 可折叠分组, 默认全部折叠
- 卡片高度48px紧凑, 间距6px
- 左边框4px类型色块: SSH蓝 #2563eb, VNC橙 #ea580c, RDP紫 #7c3aed
- 第一行: 名称(左) + 网关图标(可选, 右) + 状态(右)
- 点击卡片展开操作按钮
- 网关图标: 有网关且在线=绿底🌐, 有网关离线=灰底🌐

## 配置管理面板

- 卡片网格, 按类型分组可折叠
- SSH/FILE展开有 [连接▾] 下拉: SSH终端/SFTP文件
- VNC直接连接, RDP灰色禁用

## 新建配置: 三选一

- 选择类型后只显示该类型字段
- SSH/FILE: 名称/主机/SSH端口 + 认证方式/密码/密钥
- VNC: 名称/主机/VNC端口 + VNC密码/像素格式/色彩深度/只读
- RDP: 名称/主机/RDP端口/域名 + RDP密码/分辨率

## Agent/网关编辑弹窗

- 显示Token + 复制按钮 + 重新生成
- 共享设置: 仅自己/所有人/指定用户

## 网关管理

- 新增共享列, 移除Token/共享操作按钮
- 操作列: [编辑] [启用/禁用] [删除]

## 系统管理

- 新增密码管理页面, 调用 PUT /api/auth/password

## 用户管理

- 新增批量删除, 调用 POST /api/admin/users/batch-delete

## 权限

- SSH连接→远程管理, 普通用户增加agent:manage

## 实施步骤

### Phase 1: Bug修复
1.1 审计日志: log_audit() + 21个端点 (auth.py, auth_api.py)
1.2 VNC修复: noVNC导入 + DataChannelTransport (VncViewer.vue, webrtc.ts)
1.3 网关状态修复 (GatewayManager.vue)
1.4 登录首页=远程管理 (App.vue)

### Phase 2: 数据模型
2.1 SshConnection +RDP字段 (database.py, models.py)
2.2 后端API支持RDP (api_isolated.py)
2.3 前端api.ts +RDP (api.ts)
2.4 批量删除API (auth_api.py)
2.5 权限调整 (auth.py)

### Phase 3: 前端重构
3.1-3.10 各组件UI重构

### Phase 4: 测试部署
4.1 npm run build
4.2 docker restart
4.3 回归测试

总工时: ~25h
