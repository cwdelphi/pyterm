# 连接诊断时序图 - 实施方案 v1.0

## 一、网络元素

| 网元 | 名称 | 角色 | IP来源 |
|------|------|------|--------|
| 发起端 | Browser/APP | 用户发起连接 | 后端WebSocket握手获取 |
| 管理平台 | Backend (portal.example.com) | 信令转发 | 固定IP |
| 远端Agent | wrAgent | 桥接SSH/VNC | Agent注册时上报 |
| 中继网关 | wrgateway | NAT穿透中继 | Gateway注册时上报 |
| 目标服务器 | SSH/VNC | 被连接的目标 | 连接配置中 |

## 二、连接路径

- 直连: 浏览器 <-WebRTC DC-> wrAgent <-TCP-> 目标服务器
- 网关: 浏览器 <-WSS-> wrgateway <-WebRTC DC-> wrAgent <-TCP-> 目标服务器
- Agent隧道: wrAgent_1 <-WebRTC DC-> wrAgent_2 <-TCP-> 目标服务器

## 三、数据库设计(全新)

### connection_timeline: 连接主记录
- room_id, conn_type, host, port, path_mode
- client_ip, agent_1_ip, agent_2_ip, gateway_ip
- duration_total, success, error_stage, error_msg, failed_step, completed_steps, total_steps

### timeline_steps: 交互步骤
- room_id, step_index, from_node, to_node, protocol, action, description
- src_ip, dst_ip, latency_ms, latency_pct
- status(ok/failed/skipped), error_msg

## 四、时序图步骤(直连SSH)

| # | 源->目标 | 协议 | 动作 | 耗时 | 失败时 |
|---|---------|------|------|------|--------|
| 1 | 浏览器->管理平台 | WebSocket | connect_agent | duration_ws | WS连接失败 |
| 2 | 管理平台->Agent | WebSocket | 转发通知 | duration_signal*0.3 | Agent离线 |
| 3 | 管理平台<-Agent | WebSocket | 信令应答 | duration_signal*0.7 | 信令超时 |
| 4 | 浏览器<-Agent | WebRTC ICE | ICE+DTLS | duration_ice | NAT穿透失败 |
| 5 | 浏览器<-Agent | WebRTC DC | 通道打开 | duration_dc | SCTP失败 |
| 6 | Agent->目标 | TCP | TCP连接 | agent_tcp_ms | 目标不可达 |
| 7 | Agent->目标 | SSH | SSH握手 | agent_ssh_ms | 认证失败 |
| 8 | Agent<-目标 | SSH | 首字节 | agent_shell_ms | Shell启动失败 |

## 五、可视化: Mermaid + CSS增强

- Mermaid渲染SVG序列图
- CSS transform缩放(50%-200%)
- rect按阶段分组着色
- -x语法标记失败箭头
- Note over添加失败分析

## 六、性能影响

| 项目 | 开销 | 评估 |
|------|------|------|
| Agent time.Now() x4 | ~400ns | 零感知 |
| JSON +50字节 | ~1us | 零感知 |
| HTTP POST +100字节 | ~2ms | fire-and-forget |
| 年存储 | ~136MB | 可忽略 |
| 前端mermaid | 0 | 已有依赖 |

## 七、实施顺序

1. 删除旧表+旧API+旧模型 (10min)
2. 新建数据库模型 (10min)
3. Agent 0xFE增强+重编译部署 (30min)
4. 新建api_timeline.py (1h)
5. 修改main.py路由 (5min)
6. 修改webrtc.ts采集+上报 (30min)
7. 修改api.ts API绑定 (10min)
8. 重写DiagnosisPanel.vue (2.5h)
9. 测试验证 (30min)
合计: ~5.5小时
