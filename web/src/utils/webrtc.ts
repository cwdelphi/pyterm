/**
 * WebRTC工具模块 - 支持DataChannel消息前缀协议
 */

import { log, LogLevel } from './logger'

export const MSG_TERMINAL = 0x00
export const MSG_SSH_CONNECT = 0x01
export const MSG_RESIZE = 0x02
export const MSG_ACK = 0x03
export const MSG_SFTP_REQUEST = 0x10
export const MSG_SFTP_RESPONSE = 0x11
// SFTP 分片传输：DataChannel 单条消息上限 65536 字节（pion 与浏览器 SCTP 一致），
// 超限会被 pion 拒绝（outbound packet larger than maximum message size），
// 因此读写请求/响应超过阈值时分片发送，按 req_id 重组。
export const SFTP_SINGLE_LIMIT = 32 * 1024
export const SFTP_CHUNK_SIZE = 24 * 1024
export const MSG_VNC_CONNECT = 0x20
export const MSG_VNC_DATA = 0x21
export const MSG_VNC_DISCONNECT = 0x22
export const MSG_VNC_INPUT = 0x23
export const MSG_VNC_RESIZE = 0x24
export const MSG_VNC_CLIPBOARD = 0x25
export const MSG_VNC_ERROR = 0x2F
export const MSG_ERROR = 0xFF

// Uint8Array -> base64（分片传输用，分块避免超大字符串一次性转换）
function bytesToBase64(bytes: Uint8Array): string {
  let bin = ''
  const CHUNK = 0x8000
  for (let i = 0; i < bytes.length; i += CHUNK) {
    bin += String.fromCharCode.apply(null, Array.from(bytes.subarray(i, i + CHUNK)) as unknown as number[])
  }
  return btoa(bin)
}

export interface AgentInfo {
  id: string
  name: string
  remark?: string
  status: string
  ip?: string
  conn_type?: string
  version?: string
  last_seen: number
}

export async function fetchOnlineAgents(token: string): Promise<AgentInfo[]> {
  try {
    const res = await fetch("/api/webrtc/agents", {
      headers: { Authorization: `Bearer ${token}` },
    })
    if (!res.ok) return []
    const data = await res.json()
    return data.agents || []
  } catch {
    return []
  }
}

export interface GatewayInfo {
  id: string
  name: string
  status: string
  ip?: string
  version?: string
  last_seen: number
}

export async function fetchOnlineGateways(token: string): Promise<GatewayInfo[]> {
  if (!token) return []
  try {
    const res = await fetch("/api/webrtc/gateways", {
      headers: { Authorization: `Bearer ${token}` },
    })
    if (res.status === 401) {
      localStorage.removeItem("token")
      window.location.href = "/login"
      return []
    }
    if (!res.ok) return []
    const data = await res.json()
    return data.gateways || []
  } catch {
    return []
  }
}

const defaultIceServers: RTCIceServer[] = [
  { urls: "stun:stun.l.google.com:19302" },
]

let _cachedIceServers: RTCIceServer[] = []
let _iceServersExpiry = 0

async function getIceServers(): Promise<RTCIceServer[]> {
  const now = Date.now()
  if (_cachedIceServers.length && now < _iceServersExpiry) return _cachedIceServers
  try {
    const token = localStorage.getItem("token")
    if (!token) return defaultIceServers
    const res = await fetch("/api/webrtc/ice-servers", {
      headers: { Authorization: `Bearer ${token}` },
    })
    if (res.ok) {
      const data = await res.json()
      if (data.ice_servers && data.ice_servers.length) {
        _cachedIceServers = data.ice_servers
        _iceServersExpiry = now + 240000 // 缓存 4 分钟
        return _cachedIceServers
      }
    }
  } catch {}
  return defaultIceServers
}

// ── P4: 直连信令 WS 复用（同页多标签共享一条 /api/ws/webrtc）──
// Gateway 模式每会话独立（token/Room 在网关侧 1:1），仍各开一条。

type SharedDirectMember = {
  roomId: string
  agentId: string
  token: string
  noConnect?: boolean
  onText: (msg: any) => void
  onOpen: () => void
  onClose: () => void
  onError: (err: Error) => void
}

class SharedDirectSignal {
  private ws: WebSocket | null = null
  private members = new Map<string, SharedDirectMember>() // roomId -> member
  private connecting = false
  private heartbeatTimer: ReturnType<typeof setInterval> | null = null
  private lastAckAt = 0
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null
  private reconnectAttempt = 0
  private closedByUs = false

  private url(): string {
    const proto = location.protocol === "https:" ? "wss:" : "ws:"
    return `${proto}//${location.host}/api/ws/webrtc`
  }

  join(m: SharedDirectMember): void {
    const already = this.members.has(m.roomId)
    this.members.set(m.roomId, m)
    this.closedByUs = false
    this.ensureConnected()
    // 已有连接且非重复 join：立即通知本成员 open 并补发 connect
    if (!already && this.ws && this.ws.readyState === WebSocket.OPEN) {
      try { m.onOpen() } catch {}
      this.sendConnect(m)
    }
  }

  leave(roomId: string): void {
    this.members.delete(roomId)
    if (this.members.size === 0) {
      this.teardown()
    }
  }

  send(roomId: string, payload: any): boolean {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) return false
    try {
      this.ws.send(JSON.stringify(payload))
      return true
    } catch {
      return false
    }
  }

  sendBinary(data: ArrayBuffer | Uint8Array): boolean {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) return false
    try {
      this.ws.send(data)
      return true
    } catch {
      return false
    }
  }

  isOpen(): boolean {
    return !!this.ws && this.ws.readyState === WebSocket.OPEN
  }

  private ensureConnected(): void {
    if (this.ws && (this.ws.readyState === WebSocket.OPEN || this.ws.readyState === WebSocket.CONNECTING)) {
      // 已连接/连接中：不要对全体成员重发 connect_agent。
      // 新成员由 join() 尾部补发；全量重发会触发已建连房间二次 offer，agent 关旧 Peer 杀掉 SSH。
      // 断线重连走 ws.onopen 全量补发（房间需重建）。
      return
    }
    if (this.connecting) return
    this.connecting = true
    this.closedByUs = false
    const ws = new WebSocket(this.url())
    ws.binaryType = "arraybuffer"
    this.ws = ws

    ws.onopen = () => {
      this.connecting = false
      this.reconnectAttempt = 0
      this.lastAckAt = Date.now()
      // 仅新建/重连的 WS：对全部成员补发（旧连接已随 onclose 清理）
      for (const m of this.members.values()) {
        try { m.onOpen() } catch {}
        this.sendConnect(m)
      }
      this.startHeartbeat()
    }

    ws.onmessage = (ev) => {
      if (ev.data instanceof ArrayBuffer) {
        // 直连模式服务端一般不发 binary；若有则广播给仍在线成员
        for (const m of this.members.values()) {
          try { m.onText({ __binary: ev.data }) } catch {}
        }
        return
      }
      let msg: any
      try { msg = JSON.parse(ev.data as string) } catch { return }
      if (msg.type === "heartbeat_ack") {
        this.lastAckAt = Date.now()
        return
      }
      const rid = msg.room_id as string | undefined
      if (rid && this.members.has(rid)) {
        try { this.members.get(rid)!.onText(msg) } catch {}
        return
      }
      // 无 room 或 room 未知：广播（error 等）
      for (const m of this.members.values()) {
        try { m.onText(msg) } catch {}
      }
    }

    ws.onclose = () => {
      this.connecting = false
      this.stopHeartbeat()
      const wasUs = this.closedByUs
      for (const m of this.members.values()) {
        try { m.onClose() } catch {}
      }
      this.ws = null
      if (!wasUs && this.members.size > 0) this.scheduleReconnect()
    }

    ws.onerror = () => {
      const err = new Error("信令WebSocket连接失败")
      for (const m of this.members.values()) {
        try { m.onError(err) } catch {}
      }
    }
  }

  private sendConnect(m: SharedDirectMember): void {
    if (m.noConnect) return
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) return
    try {
      this.ws.send(JSON.stringify({
        type: "connect_agent",
        agent_id: m.agentId,
        room_id: m.roomId,
        token: m.token,
      }))
    } catch {}
  }

  private startHeartbeat(): void {
    this.stopHeartbeat()
    this.heartbeatTimer = setInterval(() => {
      if (!this.ws || this.ws.readyState !== WebSocket.OPEN) return
      try { this.ws.send(JSON.stringify({ type: "heartbeat" })) } catch {}
      // R4: ack 超时 45s（3 个周期）→ 强制重连
      if (this.lastAckAt && Date.now() - this.lastAckAt > 45000) {
        log(LogLevel.WARN, "SIG-HUB", "heartbeat_ack timeout, force reconnect")
        try { this.ws.close() } catch {}
      }
    }, 15000)
  }

  private stopHeartbeat(): void {
    if (this.heartbeatTimer) {
      clearInterval(this.heartbeatTimer)
      this.heartbeatTimer = null
    }
  }

  private scheduleReconnect(): void {
    if (this.reconnectTimer) return
    const attempt = this.reconnectAttempt++
    const delay = Math.min(1000 * Math.pow(2, attempt), 30000) + Math.random() * 500
    log(LogLevel.INFO, "SIG-HUB", `reconnect in ${Math.round(delay)}ms (attempt ${attempt + 1})`)
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null
      if (this.members.size > 0 && !this.closedByUs) this.ensureConnected()
    }, delay)
  }

  private teardown(): void {
    this.closedByUs = true
    this.stopHeartbeat()
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer)
      this.reconnectTimer = null
    }
    if (this.ws) {
      try { this.ws.close() } catch {}
      this.ws = null
    }
    this.members.clear()
    this.reconnectAttempt = 0
  }
}

const _sharedDirect = new SharedDirectSignal()

/** 测速虚拟成员: 不发 connect_agent, 仅收广播消息(speedtest_* 房间未知 → onmessage 广播) */
export function speedtestSubscribe(onMsg: (msg: any) => void): () => void {
  _sharedDirect.join({
    roomId: "__speedtest__", agentId: "", token: "", noConnect: true,
    onText: onMsg, onOpen: () => {}, onClose: () => {}, onError: () => {},
  })
  return () => _sharedDirect.leave("__speedtest__")
}

export function speedtestSend(payload: Record<string, unknown>): boolean {
  return _sharedDirect.send("__speedtest__", payload)
}

export class WebRTCManager {
  private peerConnection: RTCPeerConnection | null = null
  private token: string
  private roomId: string
  private agentId: string = ""
  private gatewayUrl: string = ""
  private gatewayId: string = ""
  private onTerminalData: ((data: ArrayBuffer) => void) | null = null
  private onOpen: (() => void) | null = null
  private onClose: (() => void) | null = null
  private onError: ((error: Error) => void) | null = null
  private onSftpResponse: ((data: any) => void) | null = null
  private onVncData: ((data: ArrayBuffer) => void) | null = null
  private onVncConnectResult: ((ok: boolean, detail?: string) => void) | null = null
  private onVncDisconnect: (() => void) | null = null
  private dataChannel: RTCDataChannel | null = null
  private signalWs: WebSocket | null = null
  private useSharedDirect = false
  private intentionalClose = false
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null
  private reconnectAttempt = 0
  private heartbeatTimer: ReturnType<typeof setInterval> | null = null
  private lastAckAt = 0
  private dcReadyTimeout: ReturnType<typeof setTimeout> | null = null
  private onOpenFired: boolean = false
  private diagReported: boolean = false
  private _connType: string = ""
  private diag = {
    start: 0, wsOpen: 0, signalOk: 0, rtcConnected: 0, dcOpen: 0, firstData: 0, error: 0,
    errorStage: "", errorMsg: "", connConfigId: "", connName: "", host: "", port: 0,
    username: "", agentName: "", gatewayName: "", pathMode: "direct", signalMode: "auto",
    iceLocalType: "", iceRemoteType: "", iceLocalAddr: "", iceRemoteAddr: "",
    connTypeDetected: "", screenSize: "", agent_connect_ms: 0, agent_tcp_ms: 0, agent_ssh_ms: 0, agent_shell_ms: 0,
    agent_ssh_host: "", agent_ssh_port: 0, agent_ok: 0,
    client_ip: "", gateway_ip: "", agent_1_ip: "",
  }
  private sshConnected: boolean = false
  private onSshConnected: (() => void) | null = null
  private sftpCallbacks: Map<string, {
    resolve: (v: any) => void
    reject: (e: Error) => void
    timer?: ReturnType<typeof setTimeout>
    startedAt?: number
    timeoutMs?: number
    arm?: () => void
  }> = new Map()
  // 分片响应重组缓冲：req_id -> 各分片
  private sftpChunkBuf: Map<string, { parts: (Uint8Array | null)[]; n: number; got: number }> = new Map()
  // P3: 终端输出 seq 追踪 + 背压丢帧空洞检测 + 节流 ack
  private lastTermSeq = -1
  private termGapTotal = 0
  private pendingAckSeq = -1
  private ackTimer: ReturnType<typeof setTimeout> | null = null
  private lastAckSent = -1
  // vnc_connect 竞态兜底：1.5s 无 0x20 回包则重发，最多 3 次
  private vncConnectRetryTimer: ReturnType<typeof setTimeout> | null = null
  private vncConnectAttempts = 0
  private lastVncConnect: {
    host: string
    port: number
    password?: string
    pixel_format?: string
    color_depth?: number
    read_only?: boolean
  } | null = null

  constructor(token: string, roomId: string) {
    this.token = token
    this.roomId = roomId
  }

  on(event: "terminal", handler: (data: ArrayBuffer) => void): void
  on(event: "open", handler: () => void): void
  on(event: "close", handler: () => void): void
  on(event: "error", handler: (error: Error) => void): void
  on(event: "sftp", handler: (data: any) => void): void
  on(event: "vnc", handler: (data: ArrayBuffer) => void): void
  on(event: "vncConnect", handler: (ok: boolean, detail?: string) => void): void
  on(event: "sshConnected", handler: () => void): void
  on(event: string, handler: any): void {
    switch (event) {
      case "terminal": this.onTerminalData = handler; break
      case "open": this.onOpen = handler; break
      case "close": this.onClose = handler; break
      case "error": this.onError = handler; break
      case "sftp": this.onSftpResponse = handler; break
      case "vnc": this.onVncData = handler; break
      case "vncConnect": this.onVncConnectResult = handler; break
      case "vncDisconnect": this.onVncDisconnect = handler; break
      case "sshConnected": this.onSshConnected = handler; break
    }
  }

  async connect(agentId: string, gatewayUrl?: string, gatewayId?: string): Promise<void> {
    this.agentId = agentId
    this.gatewayUrl = gatewayUrl || ""
    this.gatewayId = gatewayId || ""
    // Set path_mode from gateway_id (config truth) immediately — not only on
    // DataChannel open — so early timeline reports aren't stuck as "direct".
    this.diag.pathMode = this.gatewayId || this.gatewayUrl ? "gateway" : "direct"
    this.intentionalClose = false
    this.reconnectAttempt = 0
    this.diag.start = performance.now()
    this.diag.wsOpen = 0
    this.diag.signalOk = 0
    this.diag.rtcConnected = 0
    this.diag.dcOpen = 0
    this.diag.firstData = 0
    this.diag.error = 0
    this.diag.errorStage = ""
    this.diag.errorMsg = ""
    this.diag.agent_connect_ms = 0
    this.diag.agent_tcp_ms = 0
    this.diag.agent_ssh_ms = 0
    this.diag.agent_shell_ms = 0
    this.diag.agent_ssh_host = ""
    this.diag.agent_ssh_port = 0
    this.diag.agent_ok = 0
    this.diagReported = false
    this.lastTermSeq = -1
    this.termGapTotal = 0
    this.pendingAckSeq = -1
    this.lastAckSent = -1
    if (this.ackTimer) { clearTimeout(this.ackTimer); this.ackTimer = null }
    this.diag.screenSize = screen.width + "x" + screen.height
    try {
      this.connectSignal()
    } catch (error) {
      this.diag.error = performance.now(); this.diag.errorStage = "ws"; this.diag.errorMsg = (error as Error).message
      if (this.onError) this.onError(error as Error)
    }
  }

  private connectSignal(): void {
    // P4: 直连模式复用共享信令 WS；gateway 仍独立
    if (!this.gatewayUrl) {
      this.useSharedDirect = true
      this.diag.wsOpen = performance.now()
      _sharedDirect.join({
        roomId: this.roomId,
        agentId: this.agentId,
        token: this.token,
        onOpen: () => {
          log(LogLevel.INFO, "BS-WS", `shared open, room=${this.roomId}`)
          this.diag.wsOpen = this.diag.wsOpen || performance.now()
          this.startHeartbeatShared()
        },
        onText: (msg) => { void this.handleSignal(msg) },
        onClose: () => {
          log(LogLevel.INFO, "BS-WS", "shared closed")
          this.stopHeartbeat()
          // 信令断开 ≠ DC 断开；仅 DC 也断时清会话标志，允许 hub 重连后重新 open
          if (!this.dataChannel || this.dataChannel.readyState !== "open") {
            this.sshConnected = false
            this.onOpenFired = false
          }
          if (this.onClose) this.onClose()
        },
        onError: (err) => {
          this.diag.error = performance.now(); this.diag.errorStage = "ws"; this.diag.errorMsg = err.message
          this.reportDiagnostic()
          if (this.onError) this.onError(err)
        },
      })
      return
    }

    let wsUrl = this.gatewayUrl
    // S7: JWT 走 Sec-WebSocket-Protocol（浏览器 WS 无法设 Authorization 头），不再拼进 URL/query
    // 回退：无 token 时仍可连（服务端会拒），旧 query 兼容由服务端保留
    if (this.token) {
      this.signalWs = new WebSocket(wsUrl, ["bearer", this.token])
    } else {
      this.signalWs = new WebSocket(wsUrl)
    }
    this.signalWs.binaryType = "arraybuffer"

    this.signalWs.onopen = () => {
      const msgType = "connect_gateway"
      this.diag.wsOpen = performance.now()
      log(LogLevel.INFO, "BG-WS", `connected, sending ${msgType} for ${this.gatewayId}`)
      const payload: any = {
        type: msgType,
        agent_id: this.agentId,
        room_id: this.roomId,
        token: this.token,
      }
      if (this.agentId) {
        payload.gateway_id = this.gatewayId
      }
      this.signalWs!.send(JSON.stringify(payload))
      this.startHeartbeat()
    }

    this.signalWs.onmessage = (ev) => {
      try {
        // Binary frame from gateway: [prefix][payload]
        if (ev.data instanceof ArrayBuffer) {
          const bin = new Uint8Array(ev.data)
          if (bin.length >= 1) {
            const prefix = bin[0]
            const payload = bin.slice(1)
            log(LogLevel.DEBUG, "BG-WS", `binary frame: prefix=0x${prefix.toString(16).padStart(2,'0')} len=${bin.length} hasVncCb=${!!this.onVncData} hasTermCb=${!!this.onTerminalData}`)
            if (prefix === 0x00) {
              if (this.onTerminalData) {
                this.markFirstData()
                this.handleTerminalFrame(payload.buffer)
              } else {
                // 回调尚未注册时静默丢弃，避免误导性 DROPPED 告警
                log(LogLevel.DEBUG, "BG-WS", `terminal frame dropped (no handler yet): len=${bin.length}`)
              }
            } else if (prefix === 0x21) {
              if (this.onVncData) {
                this.markFirstData()
                this.onVncData(payload.buffer)
              } else {
                log(LogLevel.DEBUG, "BG-WS", `vnc frame dropped (no handler yet): len=${bin.length}`)
              }
            } else if (prefix === 0xFE) {
              try {
                const text = new TextDecoder().decode(payload)
                const msg = JSON.parse(text)
                log(LogLevel.DEBUG, "BG-WS", "Agent diagnostics (binary):", msg)
                if (msg.agent_connect_ms !== undefined) this.diag.agent_connect_ms = msg.agent_connect_ms
                if (msg.agent_tcp_ms !== undefined) this.diag.agent_tcp_ms = msg.agent_tcp_ms
                if (msg.agent_ssh_ms !== undefined) this.diag.agent_ssh_ms = msg.agent_ssh_ms
                if (msg.agent_shell_ms !== undefined) this.diag.agent_shell_ms = msg.agent_shell_ms
                if (msg.agent_ssh_host) this.diag.agent_ssh_host = msg.agent_ssh_host
                if (msg.agent_ssh_port) this.diag.agent_ssh_port = msg.agent_ssh_port
                if (msg.error_stage) { this.diag.errorStage = msg.error_stage; this.diag.errorMsg = msg.error_msg || "" }
                this.markAgentDiagOk(msg)
                if (this.diag.firstData > 0) {
                  this.diagReported = false
                  this.reportDiagnostic()
                } else if (this.diag.agent_connect_ms > 0) {
                  log(LogLevel.DEBUG, "BG-WS", "0xFE arrived before firstData, deferring report")
                } else {
                  this.diagReported = false
                  this.reportDiagnostic()
                }
              } catch {}
            } else {
              log(LogLevel.WARN, "BG-WS", `binary frame DROPPED: prefix=0x${prefix.toString(16).padStart(2,'0')}`)
            }
          }
          return
        }
        log(LogLevel.DEBUG, "BG-WS", `text frame: ${(ev.data as string).substring(0, 120)}`)
        const msg = JSON.parse(ev.data)
        void this.handleSignal(msg)
      } catch (e) { log(LogLevel.WARN, "BG-WS", `onmessage error:`, e) }
    }

    this.signalWs.onclose = () => {
      log(LogLevel.INFO, "BG-WS", "closed")
      this.stopHeartbeat()
      this.sshConnected = false
      if (this.intentionalClose) {
        if (this.onClose) this.onClose()
        return
      }
      // R4: gateway 信令断线自动重连
      this.scheduleSignalReconnect()
      if (this.onClose) this.onClose()
    }

    this.signalWs.onerror = (ev) => {
      log(LogLevel.ERROR, "BG-WS", "error:", ev)
      this.diag.error = performance.now(); this.diag.errorStage = "ws"; this.diag.errorMsg = "信令WebSocket连接失败"; this.reportDiagnostic()
      if (this.onError) this.onError(new Error("信令WebSocket连接失败"))
    }
  }

  // R4: 信令自动重连（指数退避，上限 30s）
  private scheduleSignalReconnect(): void {
    if (this.intentionalClose || this.reconnectTimer) return
    if (this.reconnectAttempt >= 8) {
      log(LogLevel.ERROR, this.gatewayUrl ? "BG-WS" : "BS-WS", "reconnect gave up after 8 attempts")
      return
    }
    const attempt = this.reconnectAttempt++
    const delay = Math.min(1000 * Math.pow(2, attempt), 30000) + Math.random() * 500
    log(LogLevel.INFO, this.gatewayUrl ? "BG-WS" : "BS-WS", `reconnect in ${Math.round(delay)}ms (attempt ${attempt + 1})`)
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null
      if (this.intentionalClose) return
      // 清掉旧 PC/DC，重新走 connect
      try { if (this.dataChannel) this.dataChannel.close() } catch {}
      try { if (this.peerConnection) this.peerConnection.close() } catch {}
      this.dataChannel = null
      this.peerConnection = null
      this.onOpenFired = false
      this.sshConnected = false
      this.connectSignal()
    }, delay)
  }

  private startHeartbeatShared(): void {
    this.stopHeartbeat()
    this.lastAckAt = Date.now()
    // 共享 hub 已统一发 heartbeat 并检测 ack 超时；成员侧仅跟踪
    this.heartbeatTimer = setInterval(() => {
      this.lastAckAt = Date.now()
    }, 15000)
  }

  private async handleSignal(msg: any): Promise<void> {
    log(LogLevel.DEBUG, this.gatewayUrl ? "BG-WS" : "BS-WS", "recv:", msg.type, msg.detail || "")
    switch (msg.type) {
      case "connect_success":
        log(LogLevel.INFO, this.gatewayUrl ? "BG-WS" : "BS-WS", "connect_success, room:", msg.room_id)
        if (msg.client_ip) this.diag.client_ip = msg.client_ip
        if (msg.agent_ip) this.diag.agent_1_ip = msg.agent_ip
        if (msg.gateway_ip) this.diag.gateway_ip = msg.gateway_ip
        if (this.gatewayUrl) {
        this.diag.signalOk = performance.now()
          log(LogLevel.INFO, "BG-WS", "Gateway mode: waiting for DataChannel ready signal")
          this.dcReadyTimeout = setTimeout(() => {
            if (!this.onOpenFired) {
              this.onOpenFired = true
              log(LogLevel.WARN, "BG-WS", "DC ready timeout fallback, firing onOpen")
              this.diag.dcOpen = this.diag.dcOpen || performance.now()
              this.diag.pathMode = this.diag.pathMode === "direct" ? "gateway" : this.diag.pathMode
              this.reportDiagnostic()
              if (this.onOpen) this.onOpen()
            }
          }, 10000)
        } else {
          this.diag.signalOk = performance.now()
          // 幂等：已有活跃 PC+DC 时忽略重复 connect_success（hub 补发/服务端重入）
          // 否则会二次 createPeer → agent 同 room 关旧 Peer → SSH 会话被杀
          const dcOpen = !!this.dataChannel && this.dataChannel.readyState === "open"
          const pcAlive = !!this.peerConnection
            && this.peerConnection.connectionState !== "closed"
            && this.peerConnection.connectionState !== "failed"
          if (dcOpen && pcAlive) {
            log(LogLevel.INFO, "BS-WS", "connect_success ignored (peer already active), room:", msg.room_id)
            break
          }
          // 需要重建：先关旧 PC/DC 并复位 open 标志，新 DC 打开后重发 ssh_connect
          this.closePeerState()
          await this.createPeer(msg.room_id)
        }
        break
      case "datachannel_ready":
        if (this.gatewayUrl) {
          log(LogLevel.INFO, "BG-WS", "DataChannel ready")
          if (this.dcReadyTimeout) { clearTimeout(this.dcReadyTimeout); this.dcReadyTimeout = null }
          this.diag.dcOpen = performance.now()
          this.diag.rtcConnected = this.diag.rtcConnected || this.diag.dcOpen
          this.diag.pathMode = "gateway"
          if (!this.diag.signalOk) this.diag.signalOk = performance.now()
          // 不在此处调用 reportDiagnostic()，等待 markFirstData() 或 0xFE 到达时再上报
          if (!this.onOpenFired) {
            this.onOpenFired = true
            if (this.onOpen) this.onOpen()
          }
        }
        break
      case "error":
        log(LogLevel.ERROR, this.gatewayUrl ? "BG-WS" : "BS-WS", "error:", msg.detail)
        this.diag.error = performance.now(); this.diag.errorStage = "signal"; this.diag.errorMsg = msg.detail || "连接失败"; this.reportDiagnostic()
        if (this.onError) this.onError(new Error(msg.detail || "连接失败"))
        break
      case "agent_diagnostics": {
        log(LogLevel.DEBUG, "BG-WS", "Agent diagnostics (relay):", msg)
        const d = (msg.data && typeof msg.data === 'object') ? { ...msg, ...msg.data } : msg
        if (d.agent_connect_ms !== undefined) this.diag.agent_connect_ms = d.agent_connect_ms
        if (d.agent_tcp_ms !== undefined) this.diag.agent_tcp_ms = d.agent_tcp_ms
        if (d.agent_ssh_ms !== undefined) this.diag.agent_ssh_ms = d.agent_ssh_ms
        if (d.agent_shell_ms !== undefined) this.diag.agent_shell_ms = d.agent_shell_ms
        if (d.agent_ssh_host) this.diag.agent_ssh_host = d.agent_ssh_host
        if (d.agent_ssh_port) this.diag.agent_ssh_port = d.agent_ssh_port
        if (d.error_stage) { this.diag.errorStage = d.error_stage; this.diag.errorMsg = d.error_msg || "" }
        this.markAgentDiagOk(d)
        if (this.diag.firstData > 0) {
          this.diagReported = false
          this.reportDiagnostic()
        } else if (this.diag.agent_connect_ms > 0) {
          log(LogLevel.DEBUG, "BG-WS", "agent_diagnostics arrived before firstData, deferring report")
        } else {
          this.diagReported = false
          this.reportDiagnostic()
        }
        break
      }
      case "terminal_data":
        if (this.gatewayUrl && this.onTerminalData) {
          const bin = Uint8Array.from(atob(msg.data), c => c.charCodeAt(0))
          // 兼容 JSON 路径（无 seq）：直接透传
          this.onTerminalData(bin.buffer)
        }
        break
      case "ssh_connect_result":
        if (this.gatewayUrl) {
          log(LogLevel.INFO, "BG-WS", "ssh_connect_result:", msg.ok, msg.detail)
          // R7: 失败回滚 sshConnected
          this.sshConnected = !!msg.ok
          if (msg.ok && this.onSshConnected) this.onSshConnected()
          if (!msg.ok && this.onError) this.onError(new Error(msg.detail || "SSH连接失败"))
        }
        break
      case "sftp_response": {
        if (this.gatewayUrl) {
          this.handleSftpResponse(msg)
        }
        break
      }
      case "vnc_data": {
        if (this.gatewayUrl && this.onVncData) {
          this.markFirstData()
          const bin = Uint8Array.from(atob(msg.data), c => c.charCodeAt(0))
          this.onVncData(bin.buffer)
        }
        break
      }
      case "vnc_connect_result": {
          log(LogLevel.INFO, "BG-WS", "VNC connect result:", msg.ok, msg.detail)
        this.onVncConnectAck(msg.ok, msg.detail)
        if (!msg.ok && this.onError) this.onError(new Error(msg.detail || "VNC连接失败"))
        break
      }
      case "vnc_disconnect": {
        log(LogLevel.INFO, "BG-WS", "VNC disconnect from agent")
        if (this.onVncDisconnect) this.onVncDisconnect()
        break
      }
      case "offer":
        await this.handleOffer(msg)
        break
      case "answer":
        await this.handleAnswer(msg)
        break
      case "candidate":
        await this.handleCandidate(msg)
        break
      case "heartbeat_ack":
        this.lastAckAt = Date.now()
        break
    }
  }

  // 关闭旧 Peer/DC 并复位会话标志（重建前调用；不触发 onClose 回调避免 UI 误标断开）
  private closePeerState(): void {
    const oldDc = this.dataChannel
    const oldPc = this.peerConnection
    this.dataChannel = null
    this.peerConnection = null
    this.onOpenFired = false
    this.sshConnected = false
    if (oldDc) {
      oldDc.onopen = null
      oldDc.onclose = null
      oldDc.onmessage = null
      oldDc.onerror = null
      try { oldDc.close() } catch {}
    }
    if (oldPc) {
      oldPc.onconnectionstatechange = null
      oldPc.onicecandidate = null
      oldPc.ondatachannel = null
      try { oldPc.close() } catch {}
    }
  }

  private async createPeer(roomId: string): Promise<void> {
    // 重建前清掉旧连接，避免孤儿 PC failed 误报 + answer 装错 PC
    this.closePeerState()
    const iceServers = await getIceServers()
    const config: RTCConfiguration = { iceServers, iceCandidatePoolSize: 2 }
    this.peerConnection = new RTCPeerConnection(config)

    this.peerConnection.onicecandidate = (event) => {
      if (event.candidate) {
          log(LogLevel.DEBUG, "BA-DC", "ICE candidate:", event.candidate.candidate?.substring(0, 60))
        this.sendSignal({
          type: "candidate",
          room_id: roomId,
          candidate: event.candidate.toJSON(),
          from: "browser",
        })
      }
    }

    this.peerConnection.onconnectionstatechange = () => {
      const state = this.peerConnection?.connectionState
        log(LogLevel.INFO, "BA-DC", "Connection state:", state)
      if (state === "connected") {
        this.detectConnType()
        this.diag.rtcConnected = performance.now()
      } else if (state === "failed" || state === "disconnected") {
        log(LogLevel.ERROR, "BA-DC", "Connection failed/disconnected:", state)
        this.diag.error = performance.now(); this.diag.errorStage = "ice"; this.diag.errorMsg = "WebRTC连接" + state
        this.reportDiagnostic()
        if (this.onError) this.onError(new Error("WebRTC连接" + state))
      }
    }

    this.peerConnection.ondatachannel = (event) => {
        log(LogLevel.INFO, "BA-DC", "Remote DataChannel received:", event.channel.label)
      this.setupDataChannel(event.channel)
    }

    this.dataChannel = this.peerConnection.createDataChannel("ssh-terminal", { ordered: true })
    log(LogLevel.INFO, "BA-DC", "Created DataChannel: ssh-terminal")
    this.setupDataChannel(this.dataChannel)

    const offer = await this.peerConnection.createOffer()
    await this.peerConnection.setLocalDescription(offer)
    log(LogLevel.INFO, "BA-DC", "Offer created and sent")

    this.sendSignal({
      type: "offer",
      room_id: roomId,
      sdp: this.peerConnection.localDescription,
    })
  }

  private async handleOffer(msg: any): Promise<void> {
    if (!this.peerConnection) {
      const iceServers = await getIceServers()
      const config: RTCConfiguration = { iceServers, iceCandidatePoolSize: 2 }
      this.peerConnection = new RTCPeerConnection(config)
      this.peerConnection.onicecandidate = (event) => {
        if (event.candidate) {
          const c = event.candidate
          log(LogLevel.DEBUG, "BA-DC", "ICE candidate:", c.candidate?.split(" ").slice(0, 8).join(" "), "type:", c.candidate?.includes("typ relay") ? "relay" : "other")
          this.sendSignal({
            type: "candidate",
            room_id: msg.room_id,
            candidate: event.candidate.toJSON(),
            from: "browser",
          })
        }
      }
      this.peerConnection.onconnectionstatechange = () => {
        const state = this.peerConnection?.connectionState
        log(LogLevel.INFO, "BA-DC", "Connection state:", state)
        if (state === "connected") {
          this.diag.rtcConnected = performance.now()
          this.detectConnType()
        } else if (state === "failed" || state === "disconnected") {
          log(LogLevel.ERROR, "BA-DC", "Connection FAILED/DISCONNECTED:", state)
          this.diag.error = performance.now(); this.diag.errorStage = "ice"; this.diag.errorMsg = "WebRTC连接" + state
        if (this.onError) this.onError(new Error("WebRTC连接" + state))
        }
      }
      this.peerConnection.oniceconnectionstatechange = () => {
        log(LogLevel.DEBUG, "BA-DC", "ICE state:", this.peerConnection?.iceConnectionState)
      }
      this.peerConnection.ondatachannel = (event) => {
        log(LogLevel.INFO, "BA-DC", "Remote DataChannel:", event.channel.label)
        this.setupDataChannel(event.channel)
      }
    }

    log(LogLevel.INFO, "BA-DC", "Setting remote description (offer)")
    await this.peerConnection.setRemoteDescription(new RTCSessionDescription(msg.sdp))
    const answer = await this.peerConnection.createAnswer()
    await this.peerConnection.setLocalDescription(answer)
    log(LogLevel.INFO, "BA-DC", "Answer created and sent")

    this.sendSignal({
      type: "answer",
      room_id: msg.room_id,
      sdp: this.peerConnection.localDescription,
    })
  }

  private async handleAnswer(msg: any): Promise<void> {
    if (!this.peerConnection) return
    log(LogLevel.INFO, "BA-DC", "Setting remote description (answer)")
    await this.peerConnection.setRemoteDescription(new RTCSessionDescription(msg.sdp))
  }

  private async handleCandidate(msg: any): Promise<void> {
    if (!this.peerConnection) return
    try {
      await this.peerConnection.addIceCandidate(new RTCIceCandidate(msg.candidate))
    } catch {}
  }

  private setupDataChannel(channel: RTCDataChannel): void {
    this.dataChannel = channel
    channel.binaryType = "arraybuffer"
    channel.onopen = () => {
      log(LogLevel.INFO, "BA-DC", "DataChannel OPEN:", channel.label)
      this.diag.dcOpen = performance.now()
      this.diag.pathMode = this.gatewayId || this.gatewayUrl ? "gateway" : "direct"
      // 不在此处调用 reportDiagnostic()，等待 markFirstData() 或 0xFE 到达时再上报
      // 避免首次上报 agent 数据全为0 导致误判失败
      if (!this.onOpenFired) {
        this.onOpenFired = true
        if (this.onOpen) this.onOpen()
      }
    }
    channel.onclose = () => {
      log(LogLevel.INFO, "BA-DC", "DataChannel CLOSED:", channel.label)
      this.sshConnected = false
      if (this.onClose) this.onClose()
    }
    channel.onmessage = (event) => {
      this.handleDataChannelMessage(event.data)
    }
    channel.onerror = (ev) => {
      log(LogLevel.ERROR, "BA-DC", "DataChannel ERROR:", channel.label, ev)
      this.diag.error = performance.now(); this.diag.errorStage = "dc"; this.diag.errorMsg = "数据通道错误"; this.reportDiagnostic()
      if (this.onError) this.onError(new Error("数据通道错误"))
    }
  }

  private handleDataChannelMessage(data: ArrayBuffer): void {
    if (data.byteLength < 1) return
    const view = new Uint8Array(data)
    const prefix = view[0]
    const payload = data.slice(1)
    const prefixNames: Record<number, string> = { 0x00: "TERMINAL", 0x01: "SSH_CONNECT", 0x02: "RESIZE", 0x10: "SFTP_REQ", 0x11: "SFTP_RESP", 0x20: "VNC_CONNECT", 0x21: "VNC_DATA", 0x22: "VNC_INPUT", 0x23: "VNC_RESIZE", 0x24: "VNC_CLIPBOARD", 0x2F: "VNC_ERROR", 0xFF: "ERROR" }
    log(LogLevel.DEBUG, "BA-DC", "DC recv prefix:", "0x" + prefix.toString(16).padStart(2, "0"), prefixNames[prefix] || "UNKNOWN", "len:", payload.byteLength)

    switch (prefix) {
      case MSG_TERMINAL:
        this.handleTerminalFrame(payload)
        break
      case MSG_SFTP_RESPONSE: {
        try {
          const text = new TextDecoder().decode(payload)
          const msg = JSON.parse(text)
          this.handleSftpResponse(msg)
        } catch {}
        break
      }
      case MSG_ERROR: {
        try {
          const text = new TextDecoder().decode(payload)
          const msg = JSON.parse(text)
          log(LogLevel.ERROR, "BA-DC", "Agent ERROR:", msg.detail)
          this.sshConnected = false
          this.diag.error = performance.now(); this.diag.errorStage = "app"; this.diag.errorMsg = msg.detail || "未知错误"
          if (this.onError) this.onError(new Error(msg.detail || "未知错误"))
        } catch (e) {
          log(LogLevel.ERROR, "BA-DC", "Failed to parse error message:", e)
        }
        break
      }
      case MSG_VNC_DATA: {
        this.markFirstData()
        if (this.onVncData) this.onVncData(payload)
        break
      }
      case 0xFE: {
        try {
          const text = new TextDecoder().decode(payload)
          const msg = JSON.parse(text)
          log(LogLevel.DEBUG, "BA-DC", "Agent diagnostics:", msg)
          if (msg.agent_connect_ms !== undefined) this.diag.agent_connect_ms = msg.agent_connect_ms
          if (msg.agent_tcp_ms !== undefined) this.diag.agent_tcp_ms = msg.agent_tcp_ms
          if (msg.agent_ssh_ms !== undefined) this.diag.agent_ssh_ms = msg.agent_ssh_ms
          if (msg.agent_shell_ms !== undefined) this.diag.agent_shell_ms = msg.agent_shell_ms
          if (msg.agent_ssh_host) this.diag.agent_ssh_host = msg.agent_ssh_host
          if (msg.agent_ssh_port) this.diag.agent_ssh_port = msg.agent_ssh_port
          if (msg.error_stage) { this.diag.errorStage = msg.error_stage; this.diag.errorMsg = msg.error_msg || "" }
          this.markAgentDiagOk(msg)
          // 直连 FILE：无 terminal 回调时，0xFE 且 shell 已就绪也置位 sshConnected
          if (msg.agent_connect_ms > 0 && !msg.error_stage && !this.sshConnected && this._connType !== "vnc") {
            this.markFirstData()
          }
          if (this.diag.firstData > 0) {
            // 首字节已到达，立即上报完整数据
            this.diagReported = false
            this.reportDiagnostic()
          } else if (this.diag.agent_connect_ms > 0) {
            // SSH已连接但首字节未到，仅存储数据，等markFirstData()触发上报
            log(LogLevel.DEBUG, "BA-DC", "0xFE arrived before firstData, deferring report")
          } else {
            // 连接失败场景，上报错误
            this.diagReported = false
            this.reportDiagnostic()
          }
        } catch {}
        break
      }
      case MSG_VNC_CONNECT: {
        try {
          const text = new TextDecoder().decode(payload)
          const msg = JSON.parse(text)
          log(LogLevel.INFO, "BA-DC", "VNC connect result:", msg.ok, msg.detail)
          this.onVncConnectAck(msg.ok, msg.detail)
          if (!msg.ok && this.onError) this.onError(new Error(msg.detail || "VNC连接失败"))
        } catch {}
        break
      }
    }
  }

  // --- 终端数据 ---
  // P3: agent→browser 终端帧格式 [0x00][4B BE seq][payload]；检测空洞并节流回 ack
  private handleTerminalFrame(payload: ArrayBuffer): void {
    // 任意终端帧 = SSH 会话已可用（FILE 页签不注册 terminal 回调也须置位）
    this.markFirstData()
    let data = payload
    let seq = -1
    if (payload.byteLength >= 4) {
      const view = new DataView(payload)
      seq = view.getUint32(0, false)
      // 兼容：若首4字节看起来像 seq 且后续仍有数据，剥离
      // agent 保证至少发 1 字节终端数据时帧长 >= 5；纯 4 字节可能是旧格式短输出
      // 以 lastTermSeq 连续性启发：新连接首帧且长度恰为 4 时无法区分 — agent 侧 seq 从 0 起且始终附带
      data = payload.slice(4)
      if (this.lastTermSeq >= 0 && seq > this.lastTermSeq + 1) {
        const lost = seq - this.lastTermSeq - 1
        this.termGapTotal += lost
        log(LogLevel.WARN, "BA-DC", `terminal seq gap: lost≈${lost} frames (last=${this.lastTermSeq} got=${seq} totalGaps=${this.termGapTotal})`)
      }
      this.lastTermSeq = seq
      this.scheduleTerminalAck(seq)
    }
    if (this.onTerminalData) this.onTerminalData(data)
  }

  private scheduleTerminalAck(seq: number): void {
    if (seq === this.lastAckSent) return
    this.pendingAckSeq = seq
    if (this.ackTimer) return
    this.ackTimer = setTimeout(() => {
      this.ackTimer = null
      const s = this.pendingAckSeq
      if (s < 0 || s === this.lastAckSent) return
      this.sendTerminalAck(s)
      this.lastAckSent = s
    }, 200)
  }

  private sendTerminalAck(seq: number): void {
    const buf = new Uint8Array(5)
    buf[0] = MSG_ACK
    buf[1] = (seq >>> 24) & 0xff
    buf[2] = (seq >>> 16) & 0xff
    buf[3] = (seq >>> 8) & 0xff
    buf[4] = seq & 0xff
    if (this.gatewayUrl) {
      if (this.signalWs && this.signalWs.readyState === WebSocket.OPEN) {
        this.signalWs.send(buf.buffer)
      }
      return
    }
    if (this.dataChannel && this.dataChannel.readyState === "open") {
      this.dataChannel.send(buf.buffer)
    }
  }

  sendTerminal(data: string | ArrayBuffer): void {
    if (this.gatewayUrl) {
      if (!this.signalWs || this.signalWs.readyState !== WebSocket.OPEN) return
      const bin = typeof data === "string" ? new TextEncoder().encode(data) : new Uint8Array(data)
      // Binary WebSocket frame: [prefix][payload] — zero overhead, no base64/JSON
      const framed = new Uint8Array(1 + bin.length)
      framed[0] = MSG_TERMINAL
      framed.set(bin, 1)
      this.signalWs.send(framed.buffer)
      return
    }
    if (!this.dataChannel || this.dataChannel.readyState !== "open") return
    const encoded = typeof data === "string" ? new TextEncoder().encode(data) : new Uint8Array(data)
    const prefixed = new Uint8Array(1 + encoded.length)
    prefixed[0] = MSG_TERMINAL
    prefixed.set(encoded, 1)
    this.dataChannel.send(prefixed)
  }

  // --- SSH连接指令 ---
  sendSshConnect(conn: {
    host: string
    port: number
    username: string
    auth_type: string
    password?: string
    key_path?: string
    mode?: string   // "local"=webterm 本地shell(免SSH服务, 网关整体重打包透传)
  }, cols: number, rows: number): void {
    if (this.gatewayUrl) {
      if (!this.signalWs || this.signalWs.readyState !== WebSocket.OPEN) {
        log(LogLevel.ERROR, "BG-WS", "sendSshConnect: signal WS not open")
        return
      }
      log(LogLevel.INFO, "BG-WS", "sendSshConnect:", (conn.mode === "local" ? "[webterm] " : "") + conn.username + "@" + conn.host + ":" + conn.port)
      this.signalWs.send(JSON.stringify({
        type: "ssh_connect", room_id: this.roomId,
        host: conn.host, port: conn.port, username: conn.username,
        auth_type: conn.auth_type, password: conn.password || "", cols, rows,
        ...(conn.mode ? { mode: conn.mode } : {}),
      }))
      // R7: 不在发送时乐观置位，等 ssh_connect_result/首帧数据确认
      this.sshConnected = false
      return
    }
    if (!this.dataChannel || this.dataChannel.readyState !== "open") {
      log(LogLevel.ERROR, "BA-DC", "sendSshConnect: DataChannel not open, state:", this.dataChannel?.readyState)
      return
    }
    log(LogLevel.INFO, "BA-DC", "sendSshConnect:", (conn.mode === "local" ? "[webterm] " : "") + conn.username + "@" + conn.host + ":" + conn.port, cols + "x" + rows)
    const msg = {
      type: "ssh_connect",
      host: conn.host,
      port: conn.port,
      username: conn.username,
      auth_type: conn.auth_type,
      password: conn.password || "",
      cols,
      rows,
      ...(conn.mode ? { mode: conn.mode } : {}),
    }
    const encoded = new TextEncoder().encode(JSON.stringify(msg))
    const prefixed = new Uint8Array(1 + encoded.length)
    prefixed[0] = MSG_SSH_CONNECT
    prefixed.set(encoded, 1)
    this.dataChannel.send(prefixed)
    // R7: 发送时不置位；首帧终端数据或明确成功后由 markFirstData/结果处理
    this.sshConnected = false
  }

  // --- resize ---
  sendResize(cols: number, rows: number): void {
    if (this.gatewayUrl) {
      if (!this.signalWs || this.signalWs.readyState !== WebSocket.OPEN) return
      this.signalWs.send(JSON.stringify({ type: "resize", room_id: this.roomId, cols, rows }))
      return
    }
    if (!this.dataChannel || this.dataChannel.readyState !== "open") return
    const msg = { cols, rows }
    const encoded = new TextEncoder().encode(JSON.stringify(msg))
    const prefixed = new Uint8Array(1 + encoded.length)
    prefixed[0] = MSG_RESIZE
    prefixed.set(encoded, 1)
    this.dataChannel.send(prefixed)
  }

  // --- SFTP请求 ---
  // 注册回调并启动超时；分片到达会调用 arm() 续期（单次间隔仍为 timeoutMs，总时长封顶 4 倍）
  private trackSftp(reqId: string, resolve: (v: any) => void, reject: (e: Error) => void, timeoutMs: number): void {
    const entry: { resolve: (v: any) => void; reject: (e: Error) => void; timer?: ReturnType<typeof setTimeout>; startedAt: number; timeoutMs: number; arm?: () => void } =
      { resolve, reject, startedAt: Date.now(), timeoutMs }
    const arm = () => {
      if (entry.timer) clearTimeout(entry.timer)
      const left = timeoutMs * 4 - (Date.now() - entry.startedAt)
      const wait = left <= 0 ? 1 : Math.min(timeoutMs, left)
      entry.timer = setTimeout(() => {
        if (this.sftpCallbacks.has(reqId)) {
          this.sftpCallbacks.delete(reqId)
          this.sftpChunkBuf.delete(reqId)
          reject(new Error("SFTP请求超时"))
        }
      }, wait)
    }
    entry.arm = arm
    arm()
    this.sftpCallbacks.set(reqId, entry)
  }

  // 统一处理 SFTP 响应：分片先重组，完整消息再触发回调
  private handleSftpResponse(msg: any): void {
    if (msg && msg.chunk) {
      const full = this.collectSftpChunk(msg)
      if (!full) return
      msg = full
    }
    const cb = this.sftpCallbacks.get(msg.req_id)
    if (cb) {
      this.sftpCallbacks.delete(msg.req_id)
      this.sftpChunkBuf.delete(msg.req_id)
      if (cb.timer) clearTimeout(cb.timer)
      if (msg.ok) cb.resolve(msg)
      else cb.reject(new Error(msg.detail || "SFTP操作失败"))
    }
    if (this.onSftpResponse) this.onSftpResponse(msg)
  }

  // 收集分片；收齐后拼装并解析出完整响应（未收齐返回 null）
  private collectSftpChunk(msg: any): any | null {
    const reqId = msg.req_id
    const c = msg.chunk
    if (!reqId || !c || typeof c.i !== 'number' || typeof c.n !== 'number') return null
    let buf = this.sftpChunkBuf.get(reqId)
    if (!buf) {
      buf = { parts: new Array(c.n).fill(null), n: c.n, got: 0 }
      this.sftpChunkBuf.set(reqId, buf)
    }
    if (c.i >= 0 && c.i < buf.n && buf.parts[c.i] == null) {
      try {
        const bin = atob(c.data)
        const bytes = new Uint8Array(bin.length)
        for (let k = 0; k < bin.length; k++) bytes[k] = bin.charCodeAt(k)
        buf.parts[c.i] = bytes
        buf.got++
      } catch {
        return null
      }
    }
    const cb = this.sftpCallbacks.get(reqId)
    if (cb && cb.arm) cb.arm()
    if (buf.got < buf.n) return null
    this.sftpChunkBuf.delete(reqId)
    try {
      let total = 0
      for (const p of buf.parts) total += p ? p.length : 0
      const all = new Uint8Array(total)
      let off = 0
      for (const p of buf.parts) {
        if (p) { all.set(p, off); off += p.length }
      }
      return JSON.parse(new TextDecoder().decode(all))
    } catch (e) {
      log(LogLevel.WARN, "BA-DC", "SFTP chunk assemble failed:", e)
      return null
    }
  }

  sendSftpRequest(op: string, params: Record<string, any> = {}): Promise<any> {
    return new Promise((resolve, reject) => {
      // 动态超时: list/stat/delete/rename 5s, read/write 30s（分片到达续期）
      const timeoutMs = (op === 'list' || op === 'stat' || op === 'delete' || op === 'rename') ? 5000 : 30000
      const isGateway = !!this.gatewayUrl
      if (isGateway) {
        if (!this.signalWs || this.signalWs.readyState !== WebSocket.OPEN) {
          reject(new Error("信令通道未连接"))
          return
        }
        log(LogLevel.INFO, "BG-WS", "sendSftpRequest:", op, params.path || params.old_path || "")
      } else {
        if (!this.dataChannel || this.dataChannel.readyState !== "open") {
          log(LogLevel.WARN, "BA-DC", "sendSftpRequest rejected: channel state =", this.dataChannel?.readyState ?? "null")
          reject(new Error("数据通道未连接"))
          return
        }
        if (!this.sshConnected) {
          log(LogLevel.WARN, "BA-DC", "sendSftpRequest rejected: ssh not connected yet")
          reject(new Error("SSH连接未建立"))
          return
        }
        log(LogLevel.INFO, "BA-DC", "sendSftpRequest:", op, params.path || params.old_path || "")
      }

      const reqId = crypto.randomUUID()
      this.trackSftp(reqId, resolve, reject, timeoutMs)

      const sendJson = (obj: Record<string, any>) => {
        if (isGateway) {
          this.signalWs!.send(JSON.stringify(obj))
          return
        }
        const encoded = new TextEncoder().encode(JSON.stringify(obj))
        const prefixed = new Uint8Array(1 + encoded.length)
        prefixed[0] = MSG_SFTP_REQUEST
        prefixed.set(encoded, 1)
        this.dataChannel!.send(prefixed)
      }

      const msg: Record<string, any> = isGateway
        ? { type: "sftp_request", room_id: this.roomId, op, req_id: reqId, ...params }
        : { type: "sftp", op, req_id: reqId, ...params }
      const encoded = new TextEncoder().encode(JSON.stringify(msg))
      if (encoded.length <= SFTP_SINGLE_LIMIT) {
        sendJson(msg)
        return
      }
      // 超过单条 DC 上限：分片发送，末条为不含 content 的装配指令（chunks=n）
      const n = Math.ceil(encoded.length / SFTP_CHUNK_SIZE)
      for (let i = 0; i < n; i++) {
        const s = i * SFTP_CHUNK_SIZE
        const bytes = encoded.subarray(s, Math.min(s + SFTP_CHUNK_SIZE, encoded.length))
        sendJson({ type: msg.type, room_id: msg.room_id, op, req_id: reqId, chunk: { i, n, data: bytesToBase64(bytes) } })
      }
      const finalMsg: Record<string, any> = { ...msg }
      delete finalMsg.content
      finalMsg.chunks = n
      sendJson(finalMsg)
      log(LogLevel.INFO, isGateway ? "BG-WS" : "BA-DC", "sendSftpRequest chunked:", n, "chunks, op =", op, "bytes =", encoded.length)
    })
  }

  // --- VNC连接指令 ---
  sendVncConnect(conn: {
    host: string
    port: number
    password?: string
    pixel_format?: string
    color_depth?: number
    read_only?: boolean
  }): void {
    this.lastVncConnect = { ...conn }
    this.vncConnectAttempts = 0
    this.doSendVncConnect(conn)
    this.armVncConnectRetry()
  }

  private doSendVncConnect(conn: {
    host: string
    port: number
    password?: string
    pixel_format?: string
    color_depth?: number
    read_only?: boolean
  }): void {
    if (this.gatewayUrl) {
      if (!this.signalWs || this.signalWs.readyState !== WebSocket.OPEN) {
        log(LogLevel.ERROR, "BG-WS", "sendVncConnect: signal WS not open")
        return
      }
      log(LogLevel.INFO, "BG-WS", "sendVncConnect:", conn.host + ":" + conn.port)
      this.signalWs.send(JSON.stringify({
        type: "vnc_connect", room_id: this.roomId,
        host: conn.host, port: conn.port,
        password: conn.password || "",
        pixel_format: conn.pixel_format || "RGB888",
        color_depth: conn.color_depth || 32,
        read_only: conn.read_only || false,
      }))
      return
    }
    if (!this.dataChannel || this.dataChannel.readyState !== "open") {
      log(LogLevel.ERROR, "BA-DC", "sendVncConnect: DataChannel not open")
      return
    }
    log(LogLevel.INFO, "BA-DC", "sendVncConnect:", conn.host + ":" + conn.port)
    const msg = {
      type: "vnc_connect",
      host: conn.host,
      port: conn.port,
      password: conn.password || "",
      pixel_format: conn.pixel_format || "RGB888",
      color_depth: conn.color_depth || 32,
      read_only: conn.read_only || false,
    }
    const encoded = new TextEncoder().encode(JSON.stringify(msg))
    const prefixed = new Uint8Array(1 + encoded.length)
    prefixed[0] = MSG_VNC_CONNECT
    prefixed.set(encoded, 1)
    this.dataChannel.send(prefixed)
  }

  // agent 未回 0x20/vnc_connect_result 时重发（OnMessage 注册竞态兜底）
  private armVncConnectRetry(): void {
    this.clearVncConnectRetry()
    this.vncConnectRetryTimer = setTimeout(() => {
      this.vncConnectRetryTimer = null
      if (!this.lastVncConnect) return
      if (this.vncConnectAttempts >= 3) {
        log(LogLevel.WARN, "BA-DC", "vnc_connect no ack after 3 retries, giving up")
        if (this.onError) this.onError(new Error("VNC连接超时"))
        return
      }
      this.vncConnectAttempts++
      log(LogLevel.WARN, "BA-DC", `vnc_connect no ack, retry #${this.vncConnectAttempts}`)
      this.doSendVncConnect(this.lastVncConnect)
      this.armVncConnectRetry()
    }, 1500)
  }

  private clearVncConnectRetry(): void {
    if (this.vncConnectRetryTimer) {
      clearTimeout(this.vncConnectRetryTimer)
      this.vncConnectRetryTimer = null
    }
  }

  private onVncConnectAck(ok: boolean, detail?: string): void {
    this.clearVncConnectRetry()
    this.vncConnectAttempts = 0
    if (this.onVncConnectResult) this.onVncConnectResult(ok, detail)
  }

  // --- VNC输入事件 ---
  sendVncInput(data: ArrayBuffer): void {
    if (this.gatewayUrl) {
      if (!this.signalWs || this.signalWs.readyState !== WebSocket.OPEN) {
        log(LogLevel.WARN, "BG-WS", `sendVncInput BLOCKED: ws=${!!this.signalWs} readyState=${this.signalWs?.readyState}`)
        return
      }
      // Gateway expects JSON text: { type: "vnc_input", data: "<base64>" }
      const bin = new Uint8Array(data)
      let binary = ''
      for (let i = 0; i < bin.length; i++) binary += String.fromCharCode(bin[i])
      const b64 = btoa(binary)
      const msg = { type: 'vnc_input', data: b64, room_id: this.roomId }
      log(LogLevel.DEBUG, "BG-WS", `sendVncInput: ${bin.length} bytes (JSON)`)
      this.signalWs.send(JSON.stringify(msg))
      return
    }
    if (!this.dataChannel || this.dataChannel.readyState !== "open") return
    const encoded = new Uint8Array(data)
    const prefixed = new Uint8Array(1 + encoded.length)
    prefixed[0] = MSG_VNC_INPUT
    prefixed.set(encoded, 1)
    this.dataChannel.send(prefixed)
  }

  // --- VNC断开连接 ---
  sendVncDisconnect(): void {
    if (this.gatewayUrl) {
      if (!this.signalWs || this.signalWs.readyState !== WebSocket.OPEN) return
      this.signalWs.send(JSON.stringify({ type: "vnc_disconnect", room_id: this.roomId }))
      return
    }
    if (!this.dataChannel || this.dataChannel.readyState !== "open") return
    const prefixed = new Uint8Array([MSG_VNC_DISCONNECT, 0x00])
    this.dataChannel.send(prefixed)
  }

  // --- VNC窗口调整 ---
  sendVncResize(width: number, height: number): void {
    if (this.gatewayUrl) {
      if (!this.signalWs || this.signalWs.readyState !== WebSocket.OPEN) return
      this.signalWs.send(JSON.stringify({ type: "vnc_resize", room_id: this.roomId, width, height }))
      return
    }
    if (!this.dataChannel || this.dataChannel.readyState !== "open") return
    const msg = { width, height }
    const encoded = new TextEncoder().encode(JSON.stringify(msg))
    const prefixed = new Uint8Array(1 + encoded.length)
    prefixed[0] = MSG_VNC_RESIZE
    prefixed.set(encoded, 1)
    this.dataChannel.send(prefixed)
  }

  // --- VNC剪贴板 ---
  sendVncClipboard(text: string): void {
    if (this.gatewayUrl) {
      if (!this.signalWs || this.signalWs.readyState !== WebSocket.OPEN) return
      this.signalWs.send(JSON.stringify({ type: "vnc_clipboard", room_id: this.roomId, text }))
      return
    }
    if (!this.dataChannel || this.dataChannel.readyState !== "open") return
    const msg = { text }
    const encoded = new TextEncoder().encode(JSON.stringify(msg))
    const prefixed = new Uint8Array(1 + encoded.length)
    prefixed[0] = MSG_VNC_CLIPBOARD
    prefixed.set(encoded, 1)
    this.dataChannel.send(prefixed)
  }

  // --- 原始发送 (兼容旧代码) ---
  isSshConnected(): boolean { return this.sshConnected }

  isReady(): boolean {
    if (this.gatewayUrl) return this.onOpenFired
    return !!this.dataChannel && this.dataChannel.readyState === "open"
  }

  send(data: ArrayBuffer | string): void {
    if (!this.dataChannel || this.dataChannel.readyState !== "open") return
    if (typeof data === "string") {
      this.sendTerminal(data)
    } else {
      this.sendTerminal(data)
    }
  }

  private sendSignal(message: any): void {
    if (this.useSharedDirect) {
      _sharedDirect.send(this.roomId, message)
      return
    }
    if (this.signalWs && this.signalWs.readyState === WebSocket.OPEN) {
      this.signalWs.send(JSON.stringify(message))
    }
  }

  private parseConnType(stats: RTCStatsReport): string {
    type PairInfo = { bytes: number; nominated: boolean; lt: string; rt: string; lp: any; rp: any }
    const pairs: PairInfo[] = []
    stats.forEach((report: any) => {
      if (report.type !== "candidate-pair") return
      const local = stats.get(report.localCandidateId)
      if (!local || (local as any).type !== "local-candidate") return
      const remote = stats.get(report.remoteCandidateId)
      pairs.push({
        bytes: (report.bytesReceived || 0) + (report.bytesSent || 0),
        nominated: report.nominated === true || report.selected === true,
        lt: (local as any).candidateType,
        rt: remote ? (remote as any).candidateType : "unknown",
        lp: (local as any).address,
        rp: remote ? (remote as any).address : undefined,
      })
    })
    if (!pairs.length) return "unknown"
    // 判定链路：优先取真正承载数据的 pair（bytes 最大），其次取 nominated/selected。
    // 不再把任意 state==="succeeded" 的探测 pair 当作选中链路——中继场景下
    // 多条 host/prflx pair 也会 succeeded，导致中转被误报成 P2P。
    let pick: PairInfo | undefined
    const carrying = pairs.filter((p) => p.bytes > 0)
    if (carrying.length) {
      pick = carrying.reduce((a, b) => (b.bytes > a.bytes ? b : a))
    } else {
      const nominated = pairs.filter((p) => p.nominated)
      pick = nominated[nominated.length - 1]
    }
    if (!pick) return "unknown"
    log(LogLevel.DEBUG, "BA-DC", "Selected pair: local=" + pick.lp + " (" + pick.lt + ") remote=" + pick.rp + " (" + pick.rt + ") bytes=" + pick.bytes)
    if (pick.lt === "relay" || pick.rt === "relay") {
      return "relay"
    }
    if (pick.lt === "host" || pick.lt === "srflx" || pick.lt === "prflx") {
      return "P2P"
    }
    return "unknown"
  }

  private async detectConnType(): Promise<void> {
    const report = (t: string) => {
      try {
        this.sendSignal({
          type: "connection_type",
          agent_id: this.agentId,
          conn_type: t,
        })
      } catch {}
    }
    try {
      let pc = this.peerConnection
      if (!pc) return
      log(LogLevel.DEBUG, "BA-DC", "detectConnType: waiting 1.5s for ICE to settle...")
      await new Promise((r) => setTimeout(r, 1500))
      pc = this.peerConnection
      if (!pc) return
      if (pc.connectionState !== "connected") {
        log(LogLevel.WARN, "BA-DC", "detectConnType: connection not connected, state:", pc.connectionState)
        return
      }
      let connType = "unknown"
      for (let i = 0; i < 3; i++) {
        pc = this.peerConnection
        if (!pc) return
        const stats = await pc.getStats()
        connType = this.parseConnType(stats)
        log(LogLevel.DEBUG, "BA-DC", "detectConnType attempt", i + 1, ":", connType)
        if (connType !== "unknown") break
        await new Promise((r) => setTimeout(r, 1000))
      }
      if (connType === "unknown") connType = "BUG"
      this.diag.connTypeDetected = connType
      log(LogLevel.INFO, "BA-DC", "Final connection type:", connType)
      report(connType)
    } catch (e) {
      log(LogLevel.WARN, "BA-DC", "detectConnType failed:", e)
      report("BUG")
    }
  }

  setConnType(t: string) { this._connType = t }
  setMode(m: string) { this.diag.signalMode = m }

  setConnMeta(opts: any) { Object.assign(this.diag, opts) }

  markFirstData() {
    // R7: 首帧数据到达视为 SSH/VNC 会话已可用
    if (!this.sshConnected && this._connType !== "vnc") {
      this.sshConnected = true
      if (this.onSshConnected) this.onSshConnected()
    }
    if (!this.diag.firstData && this.diag.start) {
      this.diag.firstData = performance.now()
      this.diag.dcOpen = this.diag.dcOpen || this.diag.firstData
      this.diagReported = false
      this.reportDiagnostic()
    }
  }

  // agent 成功诊断到达即置位：VNC 局域网内 TCP <1ms 被截断为 0，
  // 后端仅凭 agent_*_ms>0 会误判失败（假失败）。空字段 wrapper 无 agent 证据 → 不置位。
  private markAgentDiagOk(msg: any) {
    if (msg?.error_stage) return
    if (msg?.agent_ssh_host || msg?.agent_connect_ms !== undefined || msg?.agent_tcp_ms !== undefined
      || msg?.agent_ssh_ms !== undefined || msg?.agent_shell_ms !== undefined) {
      this.diag.agent_ok = 1
    }
  }

  private reportDiagnostic() {
    if (this.diagReported) return
    if (!this.diag.start) return
    this.diagReported = true
    try {
      const d = this.diag
      const now = new Date().toISOString()
      const ua = navigator.userAgent
      let browser = ""
      if (ua.includes("Chrome/")) browser = "Chrome"
      else if (ua.includes("Firefox/")) browser = "Firefox"
      else if (ua.includes("Safari/")) browser = "Safari"
      else if (ua.includes("Edge/")) browser = "Edge"
      const body: any = {
        room_id: this.roomId,
        conn_name: d.connName,
        conn_type: this._connType || "ssh",
        host: d.host,
        port: d.port,
        username: d.username,
        agent_id: this.agentId,
        path_mode: d.pathMode,
        client_ip: d.client_ip || "",
        agent_2_ip: d.agent_1_ip || "",
        agent_2_name: d.agentName || "",
        gateway_ip: d.gateway_ip || "",
        duration_total: Math.max(0, (d.firstData || d.error || performance.now()) - d.start),
        success: d.errorMsg ? 0 : 1,
        error_stage: d.errorStage,
        error_msg: d.errorMsg,
        browser: browser,
        os_info: navigator.platform || "",
        connected_at: d.dcOpen ? now : "",
        agent_tcp_ms: d.agent_tcp_ms || 0,
        agent_ssh_ms: d.agent_ssh_ms || 0,
        agent_shell_ms: d.agent_shell_ms || 0,
        agent_connect_ms: d.agent_connect_ms || 0,
        agent_ssh_host: d.agent_ssh_host || "",
        agent_ssh_port: d.agent_ssh_port || 0,
        agent_ok: d.agent_ok ? 1 : 0,
        t_start: d.start,
        t_ws_open: d.wsOpen,
        t_signal_ok: d.signalOk,
        t_rtc_connected: d.rtcConnected,
        t_dc_open: d.dcOpen,
        t_first_data: d.firstData,
        t_error: d.error || 0,
        duration_ws: d.wsOpen ? Math.max(0, d.wsOpen - d.start) : 0,
        duration_signal: d.signalOk && d.wsOpen ? Math.max(0, d.signalOk - d.wsOpen) : 0,
        duration_ice: d.rtcConnected && d.signalOk ? Math.max(0, d.rtcConnected - d.signalOk) : 0,
        duration_dc: d.dcOpen && d.rtcConnected ? Math.max(0, d.dcOpen - d.rtcConnected) : 0,
        duration_data: d.firstData && d.dcOpen ? Math.max(0, d.firstData - d.dcOpen) : 0,
      }
      try {
        const token = localStorage.getItem("token")
        fetch("/api/timeline/report", {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
            ...(token ? { Authorization: "Bearer " + token } : {}),
          },
          body: JSON.stringify(body),
        })
          .then(r => { if (!r.ok) console.error("[DIAG] report failed:", r.status, r.statusText) })
          .catch(e => console.error("[DIAG] report error:", e))
      } catch {}
    } catch {}
  }

  private startHeartbeat(): void {
    this.stopHeartbeat()
    this.lastAckAt = Date.now()
    this.heartbeatTimer = setInterval(() => {
      this.sendSignal({ type: "heartbeat" })
      // R4: 45s 无 ack 视为假活，断开触发重连
      if (this.lastAckAt && Date.now() - this.lastAckAt > 45000) {
        log(LogLevel.WARN, this.gatewayUrl ? "BG-WS" : "BS-WS", "heartbeat_ack timeout")
        this.lastAckAt = Date.now()
        if (this.signalWs) {
          try { this.signalWs.close() } catch {}
        }
      }
    }, 15000)
  }

  private stopHeartbeat(): void {
    if (this.heartbeatTimer) {
      clearInterval(this.heartbeatTimer)
      this.heartbeatTimer = null
    }
  }

  async close(): Promise<void> {
    log(LogLevel.DEBUG, "BA-DC", "close()")
    this.intentionalClose = true
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer)
      this.reconnectTimer = null
    }
    // P8: 缩短诊断等待 — 最多 800ms（原 3000+500）
    if (!this.sshConnected && this.diag.agent_connect_ms === 0) {
      const canWait = (this.dataChannel && this.dataChannel.readyState === "open")
        || (this.gatewayUrl && this.diag.firstData === 0)
      if (canWait) await new Promise(r => setTimeout(r, 800))
    }
    if (this.diag.agent_connect_ms > 0 && !this.diag.firstData) {
      const canWaitFirst = (this.dataChannel && this.dataChannel.readyState === "open")
        || !!this.gatewayUrl
      if (canWaitFirst) {
        log(LogLevel.DEBUG, "BA-DC", "close() waiting for firstData...")
        await new Promise(r => setTimeout(r, 200))
      }
    }
    this.reportDiagnostic()
    this.stopHeartbeat()
    if (this.dcReadyTimeout) { clearTimeout(this.dcReadyTimeout); this.dcReadyTimeout = null }
    if (this.ackTimer) { clearTimeout(this.ackTimer); this.ackTimer = null }
    this.clearVncConnectRetry()
    this.lastVncConnect = null
    this.pendingAckSeq = -1
    this.onOpenFired = false
    this.diagReported = false
    this.sftpCallbacks.clear()
    this.sftpChunkBuf.clear()
    this.onTerminalData = null
    this.onSftpResponse = null
    this.onVncData = null
    this.onVncConnectResult = null
    this.onClose = null
    this.onError = null
    if (this.dataChannel) { this.dataChannel.close(); this.dataChannel = null }
    if (this.peerConnection) { this.peerConnection.close(); this.peerConnection = null }
    if (this.useSharedDirect) {
      _sharedDirect.leave(this.roomId)
      this.useSharedDirect = false
      this.signalWs = null
      return
    }
    if (this.signalWs) { this.signalWs.close(); this.signalWs = null }
  }
}

/**
 * 主动探测浏览器 <-> Agent 之间的连接类型 (P2P / relay / BUG)
 * 一次性建立 WebRTC 连接，分析 nominated candidate 的 local type，上报并返回结果。
 */
export async function probeConnType(agentId: string, token: string, timeoutMs = 10000): Promise<string> {
  const proto = location.protocol === "https:" ? "wss:" : "ws:"
  const signalWs = new WebSocket(`${proto}//${location.host}/api/ws/webrtc`)
  const roomId = `probe_${agentId}_${Date.now()}_${Math.random().toString(36).slice(2, 8)}`
  let peer: RTCPeerConnection | null = null
  let dc: RTCDataChannel | null = null

  const parseConnType = (stats: RTCStatsReport): string => {
    let ct = "unknown"
    stats.forEach((report: any) => {
      if (report.type !== "candidate-pair") return
      const selected = report.nominated || report.state === "succeeded" || report.selected
      if (!selected) return
      const local = stats.get(report.localCandidateId)
      const remote = stats.get(report.remoteCandidateId)
      if (!local || local.type !== "local-candidate") return
      const lt = (local as any).candidateType
      const rt = remote ? (remote as any).candidateType : "unknown"
      if (lt === "relay" || rt === "relay") ct = "relay"
      else if (lt === "host" || lt === "srflx" || lt === "prflx") ct = "P2P"
    })
    return ct
  }

  const report = (connType: string) => {
    try {
      if (signalWs.readyState === WebSocket.OPEN) {
        signalWs.send(JSON.stringify({ type: "connection_type", agent_id: agentId, conn_type: connType }))
      }
    } catch {}
  }

  const cleanup = () => {
    try { if (dc) dc.close() } catch {}
    try { if (peer) peer.close() } catch {}
    try { signalWs.close() } catch {}
  }

  return new Promise((resolve) => {
    let settled = false
    const finish = (result: string) => {
      if (settled) return
      settled = true
      report(result)
      cleanup()
      resolve(result)
    }

    const timer = setTimeout(() => finish("BUG"), timeoutMs)

    signalWs.onerror = () => { clearTimeout(timer); finish("BUG") }
    signalWs.onclose = () => { clearTimeout(timer); if (!settled) finish("BUG") }

    signalWs.onopen = () => {
      signalWs.send(JSON.stringify({
        type: "connect_agent",
        agent_id: agentId,
        room_id: roomId,
        token,
      }))
    }

    signalWs.onmessage = async (ev) => {
      let msg: any
      try { msg = JSON.parse(ev.data) } catch { return }
      switch (msg.type) {
        case "connect_success": {
          peer = new RTCPeerConnection({
            iceServers: await getIceServers(),
            iceCandidatePoolSize: 2,
          })
          peer.onicecandidate = (e) => {
            if (e.candidate) {
              signalWs.send(JSON.stringify({
                type: "candidate", room_id: roomId, candidate: e.candidate.toJSON(), from: "browser",
              }))
            }
          }
          peer.onconnectionstatechange = async () => {
            if (peer?.connectionState === "connected") {
              await new Promise((r) => setTimeout(r, 1500))
              if (!peer) { finish("BUG"); return }
              try {
                let ct = "unknown"
                for (let i = 0; i < 3; i++) {
                  const stats = await peer.getStats()
                  ct = parseConnType(stats)
                  if (ct !== "unknown") break
                  await new Promise((r) => setTimeout(r, 1000))
                }
                clearTimeout(timer)
                finish(ct === "unknown" ? "BUG" : ct)
              } catch {
                clearTimeout(timer)
                finish("BUG")
              }
            } else if (peer?.connectionState === "failed" || peer?.connectionState === "disconnected") {
              clearTimeout(timer)
              finish("BUG")
            }
          }
          dc = peer.createDataChannel("probe", { ordered: true })
          const offer = await peer.createOffer()
          await peer.setLocalDescription(offer)
          signalWs.send(JSON.stringify({ type: "offer", room_id: roomId, sdp: peer.localDescription }))
          break
        }
        case "answer": {
          try { await peer?.setRemoteDescription(new RTCSessionDescription(msg.sdp)) } catch {}
          break
        }
        case "candidate": {
          try { await peer?.addIceCandidate(new RTCIceCandidate(msg.candidate)) } catch {}
          break
        }
        case "error": {
          clearTimeout(timer)
          finish("BUG")
          break
        }
      }
    }
  })
}

export default WebRTCManager
