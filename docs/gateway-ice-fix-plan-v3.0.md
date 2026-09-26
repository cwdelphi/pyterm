# Gateway ICE Fix Plan v3.0
## 日期: 2026-09-15

### 根因分析
Gateway的handleConnectSuccess方法缺少OnICECandidate回调，且发送offer时用原始offer变量
（不含ICE候选）而非peerConn.LocalDescription()。导致Agent无法获得Gateway的ICE候选，
跨NAT场景下ICE consent check超时→failed。

### 修复清单

| # | 文件 | 修复内容 | 优先级 |
|---|------|----------|--------|
| 1 | wrgateway/server/handler.go | 添加OnICECandidate回调 + Marshal(LocalDescription) | P0 |
| 2 | wragent/webrtc/peer.go | CreateAnswer返回LocalDescription() | P1 |
| 3 | wragent/webrtc/signal.go | 重复Offer关闭旧PeerConnection | P2 |
| 4 | handler.go + peer.go | ICE disconnected不立即关闭 | P2 |
| 5 | web/src/utils/webrtc.ts | _type→type | P3 |
| 6 | wragent/webrtc/signal.go | resize变量名冲突修复 | P3 |
| 7 | wrgateway/server/handler.go | handleBrowserConnect精确匹配 | P3 |

### 验证
- Phase 4: 3/3通过
- Phase 6: 3/3通过
