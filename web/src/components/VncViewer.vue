<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'
import RFB from '@novnc/novnc'

const { t } = useI18n()

const props = defineProps<{
  conn: any
  tabId: string
  webrtcManager?: any
}>()

const emit = defineEmits<{
  (e: 'status', status: string): void
  (e: 'error', msg: string): void
}>()

const canvasContainer = ref<HTMLDivElement>()
const rfbInstance = ref<any>(null)
const status = ref<'connecting' | 'connected' | 'disconnected' | 'error'>('connecting')
const statusText = ref(t('ssh.connecting'))
const qualityLevel = ref(6)
const compressionLevel = ref(2)
let _disconnecting = false
let _reconnectTimer: ReturnType<typeof setTimeout> | null = null
let _reconnectAttempts = 0
const MAX_RECONNECT = 3

class DataChannelTransport {
  private webrtcManager: any
  private conn: any
  onopen: (() => void) | null = null
  onmessage: ((ev: { data: any }) => void) | null = null
  onclose: ((ev: any) => void) | null = null
  onerror: ((ev: any) => void) | null = null
  readyState: number = 0
  binaryType: string = 'arraybuffer'
  protocol: string = 'binary'

  constructor(webrtcManager: any, conn: any) {
    this.webrtcManager = webrtcManager
    this.conn = conn
  }

  connect() {
    this.webrtcManager.on('vnc', (data: ArrayBuffer) => {
      if (this.onmessage) this.onmessage({ data })
    })
    this.webrtcManager.on('error', (err: Error) => {
      if (this.onerror) this.onerror({ message: err.message })
    })
    this.webrtcManager.on('close', () => {
      this.readyState = 3
      if (this.onclose) this.onclose({ code: 1000, reason: 'closed' })
    })
    this.webrtcManager.on('vncConnect', (ok: boolean, detail?: string) => {
      if (ok) {
        this.readyState = 1
        if (this.onopen) this.onopen()
      } else {
        if (this.onerror) this.onerror({ message: detail || t('vnc.connectFailed') })
      }
    })
  }

  send(data: any) {
    let buf: ArrayBuffer
    if (typeof data === 'string') {
      buf = new TextEncoder().encode(data).buffer
    } else if (data instanceof Uint8Array) {
      buf = data.buffer.slice(data.byteOffset, data.byteOffset + data.byteLength)
    } else if (data instanceof ArrayBuffer) {
      buf = data
    } else if (data instanceof Blob) {
      data.arrayBuffer().then((b: ArrayBuffer) => { this.webrtcManager.sendVncInput(b) })
      return
    } else {
      return
    }
    this.webrtcManager.sendVncInput(buf)
  }

  close() {
    this.readyState = 3
    this.onopen = null
    this.onmessage = null
    this.onclose = null
    this.onerror = null
  }
}

function destroyRfb() {
  const rfb = rfbInstance.value
  if (!rfb) return
  try {
    rfb._cursor?.detach?.()
    rfb._gestures?.detach?.()
    rfb._keyboard?.ungrab?.()
    rfb._resizeObserver?.disconnect?.()
    rfb._sock?.close?.()
    rfb._target?.removeChild?.(rfb._screen)
  } catch {}
  rfbInstance.value = null
}

function sendVncConnectCmd() {
  const c = props.conn
  props.webrtcManager?.sendVncConnect({
    host: c.host,
    port: c.vnc_port || 5900,
    password: c.vnc_password || '',
    pixel_format: c.pixel_format || 'RGB888',
    color_depth: c.color_depth || 32,
    read_only: !!c.read_only
  })
}

function scheduleReconnect(reason: string) {
  if (_disconnecting || _reconnectTimer) return
  if (_reconnectAttempts >= MAX_RECONNECT) {
    status.value = 'disconnected'
    statusText.value = t('vnc.abnormalDisconnect')
    emit('status', 'disconnected')
    return
  }
  _reconnectAttempts++
  status.value = 'connecting'
  statusText.value = t('vnc.reconnecting')
  console.log('[VNC] auto-reconnect', _reconnectAttempts, 'reason:', reason)
  try { props.webrtcManager?.sendVncDisconnect() } catch {}
  destroyRfb()
  _reconnectTimer = setTimeout(() => {
    _reconnectTimer = null
    if (_disconnecting) return
    connect()
    sendVncConnectCmd()
  }, 800)
}

function connect() {
  if (!canvasContainer.value) return
  if (!props.webrtcManager) {
    status.value = 'error'
    statusText.value = t('vnc.noChannel')
    emit('error', t('vnc.noChannel'))
    return
  }

  try {
    console.log('[VNC-TAB] connect() starting, mode:', props.conn.mode, 'conn.id:', props.conn.id)
    destroyRfb()
    const transport = new DataChannelTransport(props.webrtcManager, props.conn)
    const origOnOpen = transport.onopen
    transport.onopen = () => {
      console.log('[VNC] transport.onopen fired, readyState:', transport.readyState)
      if (origOnOpen) origOnOpen()
    }
    const origOnMessage = transport.onmessage
    transport.onmessage = (ev: any) => {
      const data = ev.data
      if (data instanceof ArrayBuffer) {
        console.log('[VNC] transport.onmessage binary len:', data.byteLength)
      } else if (data instanceof Array) {
        console.log('[VNC] transport.onmessage array len:', data.length)
      }
      if (origOnMessage) origOnMessage(ev)
    }
    const rfb = new RFB(canvasContainer.value, transport as any, {})
    // Apply options via setters (noVNC 1.7.0 does not auto-apply from constructor)
    rfb.qualityLevel = qualityLevel.value
    rfb.compressionLevel = compressionLevel.value
    // 服务端多不支持客户端改分辨率；resize 失败仅告示，关闭可减少噪音
    rfb.resizeSession = false
    rfb.viewOnly = !!props.conn.read_only
    rfb.scaleViewport = true

    rfb.addEventListener('connect', () => {
      console.log('[VNC] novnc connect event fired')
      _reconnectAttempts = 0
      status.value = 'connected'
      statusText.value = t('vnc.connected')
      emit('status', 'connected')
    })
    rfb.addEventListener('disconnect', (e: any) => {
      console.log('[VNC] novnc disconnect event:', e.detail)
      if (!e.detail?.clean && !_disconnecting) {
        // zlib 解码失败等：停掉 agent 桥接并自动重连，避免黑屏僵死
        scheduleReconnect(e.detail?.reason || 'unclean')
        return
      }
      status.value = 'disconnected'
      statusText.value = e.detail?.clean ? t('vnc.disconnected') : t('vnc.abnormalDisconnect')
      emit('status', 'disconnected')
    })
    rfb.addEventListener('credentialsrequired', () => {
      const pw = props.conn.vnc_password || ''
      console.log('[VNC] credentialsrequired, pw length:', pw.length)
      if (pw) { rfb.sendCredentials({ password: pw }) } else { statusText.value = t('vnc.needPassword') }
    })
    rfb.addEventListener('securityfailure', (e: any) => {
      console.log('[VNC] securityfailure:', e.detail)
      status.value = 'error'
      statusText.value = t('vnc.authFailed') + (e.detail?.reason || t('vnc.unknown'))
      emit('error', statusText.value)
    })
    rfb.addEventListener('desktopname', (e: any) => {
      statusText.value = e.detail?.name || t('vnc.connected')
    })

    // DO NOT override canvas style — let noVNC manage dimensions via _rescale()
    // rfb._canvas is managed by noVNC's Display, overriding breaks coordinate mapping

    rfbInstance.value = rfb
    transport.connect()

    props.webrtcManager.on('vncDisconnect', () => {
      if (_disconnecting) return
      scheduleReconnect('agent-vnc-disconnect')
    })

    status.value = 'connecting'
    statusText.value = t('vnc.waitingChannel')
  } catch (err: any) {
    status.value = 'error'
    statusText.value = t('vnc.initFailed') + err.message
    emit('error', statusText.value)
  }
}

function disconnect() {
  if (_disconnecting) return
  _disconnecting = true
  if (_reconnectTimer) { clearTimeout(_reconnectTimer); _reconnectTimer = null }
  try { props.webrtcManager?.sendVncDisconnect() } catch {}
  setTimeout(() => {
    destroyRfb()
    status.value = 'disconnected'
    statusText.value = t('vnc.alreadyDisconnected')
    emit('status', 'disconnected')
  }, 150)
}

function sendCtrlAltDel() { rfbInstance.value?.sendCtrlAltDel() }

function setQuality(q: number) {
  qualityLevel.value = q
  if (rfbInstance.value) rfbInstance.value.qualityLevel = q
}

function setCompression(c: number) {
  compressionLevel.value = c
  if (rfbInstance.value) rfbInstance.value.compressionLevel = c
}

function takeScreenshot() {
  if (!canvasContainer.value) return
  const canvas = canvasContainer.value.querySelector('canvas')
  if (!canvas) return
  const link = document.createElement('a')
  link.download = 'vnc-' + (props.conn.name || 'screenshot') + '-' + Date.now() + '.png'
  link.href = canvas.toDataURL('image/png')
  link.click()
}

function goFullscreen() { canvasContainer.value?.requestFullscreen?.() }

onMounted(() => { connect() })
onBeforeUnmount(() => { disconnect() })

defineExpose({ disconnect, connect })
</script>

<template>
  <div class="vnc-wrap">
    <div class="vnc-toolbar">
      <span class="vnc-status" :class="status">
        <span class="vnc-dot"></span>
        {{ statusText }}
      </span>
      <div class="vnc-actions">
        <div class="vnc-quality-group">
          <span class="vnc-label">{{ t('vnc.quality') }}</span>
          <button v-for="q in [3,6,9]" :key="q" class="vnc-btn-sm" :class="{ active: qualityLevel === q }" @click="setQuality(q)">
            {{ q <= 3 ? t('vnc.low') : q <= 6 ? t('vnc.medium') : t('vnc.high') }}
          </button>
        </div>
        <div class="vnc-quality-group">
          <span class="vnc-label">{{ t('vnc.compression') }}</span>
          <button v-for="c in [0,2,6]" :key="c" class="vnc-btn-sm" :class="{ active: compressionLevel === c }" @click="setCompression(c)">
            {{ c === 0 ? t('vnc.none') : c <= 2 ? t('vnc.low') : t('vnc.high') }}
          </button>
        </div>
        <button class="vnc-btn" @click="goFullscreen" :title="t('vnc.fullscreen')">⛶</button>
        <button class="vnc-btn" @click="takeScreenshot" :title="t('vnc.screenshot')">📷</button>
        <button class="vnc-btn" @click="sendCtrlAltDel" title="Ctrl+Alt+Del">⌘</button>
        <button class="vnc-btn danger" @click="disconnect" :title="t('vnc.disconnect')">✕</button>
      </div>
    </div>
    <div class="vnc-canvas-wrap" ref="canvasContainer"></div>
  </div>
</template>

<style scoped>
.vnc-wrap { display:flex; flex-direction:column; height:100%; background:#1a1a2e; }
.vnc-toolbar { display:flex; align-items:center; justify-content:space-between; padding:4px 12px; background:var(--panel); border-bottom:1px solid var(--border); min-height:32px; flex-shrink:0; }
.vnc-status { display:flex; align-items:center; gap:6px; font-size:12px; color:var(--muted); }
.vnc-dot { width:8px; height:8px; border-radius:50%; background:var(--muted); transition:background .3s; }
.vnc-status.connected .vnc-dot { background:#34d399; box-shadow:0 0 6px #34d399; }
.vnc-status.connecting .vnc-dot { background:#fbbf24; animation:vnc-pulse 1s infinite; }
.vnc-status.disconnected .vnc-dot { background:#dc2626; }
.vnc-status.error .vnc-dot { background:#ef4444; }
@keyframes vnc-pulse { 0%,100% { opacity:1; } 50% { opacity:.4; } }
.vnc-actions { display:flex; align-items:center; gap:8px; }
.vnc-quality-group { display:flex; align-items:center; gap:2px; }
.vnc-label { font-size:11px; color:var(--muted); margin-right:2px; }
.vnc-btn-sm { min-width:24px; height:22px; font-size:11px; border:1px solid var(--border); border-radius:4px; background:var(--panel); color:var(--fg-2); cursor:pointer; display:flex; align-items:center; justify-content:center; }
.vnc-btn-sm:hover { border-color:var(--accent); color:var(--accent); }
.vnc-btn-sm.active { border-color:var(--accent); color:var(--accent); background:var(--accent-soft); font-weight:600; }
.vnc-btn { min-width:24px; height:24px; font-size:13px; border:1px solid var(--border); border-radius:4px; background:var(--panel); color:var(--fg-2); cursor:pointer; display:flex; align-items:center; justify-content:center; }
.vnc-btn:hover { border-color:var(--accent); color:var(--accent); }
.vnc-btn.danger:hover { border-color:#dc2626; color:#dc2626; }
.vnc-canvas-wrap { flex:1; overflow:hidden; position:relative; display:flex; align-items:center; justify-content:center; }
</style>
