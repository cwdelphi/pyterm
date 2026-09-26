# Gateway/Agent 修复 + VNC Phase 1 实施方案

> 日期: 2026-09-15
> 状态: 实施中

---

## 一、Gateway 架构问题

### 问题 1 (P0): Gateway 时序竞态
- 现象: Gateway 模式下 SSH/SFTP 报首条消息必须是SSH连接指令
- 根因: connect_success 过早触发, DataChannel 未就绪时消息被丢弃

### 问题 2 (P1): /api/webrtc/gateways 401
- 根因: Token 过期或未正确传递

### 问题 3 (P1): detectConnType null 引用
- 根因: close() 后异步访问 peerConnection

---

## 二、修复方案

1. Gateway 消息缓冲队列 (handler.go)
2. 前端 datachannel_ready 等待 (webrtc.ts)
3. 401 Token 修复 (webrtc.ts)
4. detectConnType null 检查 (webrtc.ts)

---

## 三、VNC Phase 1

- docker-compose 暴露 5900 端口
- wragent/vnc/bridge.go (RFB 客户端桥接)
- VncViewer.vue (noVNC Canvas 渲染)
- DataChannel 协议: 0x20-0x2F

---

## 四、实施顺序

| Phase | 任务 | 估时 |
|-------|------|------|
| A | Gateway 时序修复 | 2d |
| B | 401 + detectConnType | 0.5d |
| C | VNC Phase 1 | 6d |
| D | 集成测试 | 1.5d |
