import { describe, it, expect, vi, beforeEach } from 'vitest'
import { WebRTCManager } from '../utils/webrtc'

// 网关模式 vnc_connect 竞态回归 (Fix B):
// dcReady=false (10s 兜底提前触发 onOpen / datachannel_ready 未到) 时,
// sendVncConnect 必须缓冲不发送, datachannel_ready 到达后补发。
// 此前直接发送 → 网关 DC 未 Open → pion ensureOpen 报错被忽略 → 3 次重试全丢 → VNC连接超时。

function makeManager(): { w: WebRTCManager; ws: { readyState: number; send: ReturnType<typeof vi.fn> } } {
  const w = new WebRTCManager('tok', 'room-test')
  const ws = { readyState: 1, send: vi.fn() }
  ;(w as any).gatewayUrl = 'ws://gw.example/ws'
  ;(w as any).signalWs = ws
  return { w, ws }
}

describe('WebRTCManager.sendVncConnect 网关模式 DC 就绪竞态', () => {
  beforeEach(() => {
    vi.stubGlobal('WebSocket', { OPEN: 1 })
  })

  it('dcReady=false: 缓冲不发送, datachannel_ready 后补发且只发一次', async () => {
    const { w, ws } = makeManager()
    ;(w as any).dcReady = false

    w.sendVncConnect({ host: '127.0.0.1', port: 5900, password: 'pw' })
    expect(ws.send).not.toHaveBeenCalled()
    expect((w as any).pendingVncConnect).not.toBeNull()
    expect((w as any).pendingVncConnect.host).toBe('127.0.0.1')

    await (w as any).handleSignal({ type: 'datachannel_ready', room_id: 'room-test' })
    expect(ws.send).toHaveBeenCalledTimes(1)
    const sent = JSON.parse(ws.send.mock.calls[0][0])
    expect(sent.type).toBe('vnc_connect')
    expect(sent.host).toBe('127.0.0.1')
    expect(sent.port).toBe(5900)
    expect((w as any).pendingVncConnect).toBeNull()
    expect((w as any).dcReady).toBe(true)

    // 清理重试/看门狗定时器, 避免测试悬挂
    ;(w as any).clearVncConnectRetry()
    ;(w as any).clearVncDefer()
  })

  it('dcReady=true: 直接发送且不进缓冲', () => {
    const { w, ws } = makeManager()
    ;(w as any).dcReady = true

    w.sendVncConnect({ host: '10.0.0.2', port: 5901 })
    expect(ws.send).toHaveBeenCalledTimes(1)
    expect((w as any).pendingVncConnect).toBeNull()
    const sent = JSON.parse(ws.send.mock.calls[0][0])
    expect(sent.type).toBe('vnc_connect')
    expect(sent.host).toBe('10.0.0.2')
    ;(w as any).clearVncConnectRetry()
  })

  it('datachannel_ready 到达但无缓冲时不重复发送', async () => {
    const { w, ws } = makeManager()
    ;(w as any).dcReady = false
    await (w as any).handleSignal({ type: 'datachannel_ready', room_id: 'room-test' })
    expect(ws.send).not.toHaveBeenCalled()
    expect((w as any).dcReady).toBe(true)
  })
})
