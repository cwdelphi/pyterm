# 延迟优化方案 v1.0.0

> 版本: 1.0.0 | 日期: 2026-09-17 | 基于: 可靠性测试报告 v1.0.0

## 背景

网关模式延迟比直连模式高 3-5 倍，根本原因：
1. base64 编解码 (+33% 体积)
2. JSON marshal/unmarshal (~100μs/次)
3. WebSocket 文本帧 (UTF-8 校验)
4. 额外网络跳 (1 RTT)
5. 无背压控制 (队列堆积)

## 优化项

### P0: Binary WebSocket帧 (G1+F1)
- 终端数据改用 binary 帧
- 浏览器: signalWs.send(arrayBuffer) 替代 JSON+base64
- 网关: 识别 binary/text 帧，binary 直接转发
- 预期: 网关延迟 -30-40%

### P0: Agent DC BufferedAmount检查 (A3)
- 发送前检查 dc.BufferedAmount()
- 超阈值(64KB)时丢帧或等待
- 预期: 避免队列堆积

### P1: Gateway DC背压控制 (G3)
- 检查 dc.BufferedAmount()
- 超阈值时降速或丢帧

### P1: SSH连接池 (A1)
- 维护 map[string]*gossh.Client
- 复用连接，避免每次重新握手
- 预期: 重连 -200ms

### P2: permessage-deflate (G2)
- 启用 WebSocket 压缩
- 预期: 体积 -40%

### P2: RAF批量渲染 (F2)
- requestAnimationFrame 合并 xterm 写入
- 预期: 渲染 -20%

## 预期效果

| 模式 | 优化前 | 优化后 |
|------|--------|--------|
| 直连 | ~150ms | ~120ms |
| 网关 | ~400-800ms | ~200-300ms |
| 网关/直连 | 3-5x | 1.5-2x |
