<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount, nextTick, inject, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api, type SshConn, getCurrentUser } from '../api'

const { t } = useI18n()
import SshFileBrowser from './SshFileBrowser.vue'
import VncViewer from './VncViewer.vue'
import { WebRTCManager } from '../utils/webrtc'

const toast = inject<any>('toast')
const props = defineProps<{ view?: string }>()
watch(() => props.view, (v) => { if (v === 'ssh') refitAll() })

/* ── 数据 ── */
const conns = ref<SshConn[]>([])
const tabs = ref<{ id: string; conn: SshConn; label: string; status: 'connected' | 'disconnected' | 'connecting'; type?: 'terminal' | 'filemanager' | 'vnc'; connMode?: 'agent' }[]>([])
const activeTab = ref('')
const expandedIds = ref<Set<string>>(new Set())
const terminals = ref<Record<string, any>>({})
const webrtcRefs = ref<Record<string, WebRTCManager | null>>({})
const vncRefs = ref<Record<string, any>>({})
const termContainers = ref<Record<string, HTMLDivElement | null>>({})
const fitAddons = ref<Record<string, any>>({})
const resizeObservers = ref<Record<string, ResizeObserver | null>>({})

/* ── 弹窗状态 ── */
const showForm = ref(false)
const isEdit = ref(false)
const editingId = ref('')
const form = ref<SshConn>({ name: '', host: '', port: 22, username: 'root', auth_type: 'password', password: '', key_path: '', connection_mode: 'agent', agent_id: '', gateway_id: '', remark: '', connection_type: 'ssh', vnc_port: 5900, vnc_password: '', pixel_format: 'tight', color_depth: 'full', read_only: false, rdp_port: 3389, rdp_password: '', rdp_domain: '', rdp_resolution: '1920x1080' })
const testing = ref(false)
const showDeleteConfirm = ref(false)
const deleteTargetId = ref('')
const deleteTargetName = ref('')
const showPasswords = ref<Record<string, boolean>>({ password: false, vnc_password: false, rdp_password: false })
function togglePassword(field: string) { showPasswords.value[field] = !showPasswords.value[field] }
const agents = ref<{id: string; name: string; remark: string; is_active: number}[]>([])
const gateways = ref<{id: string; name: string; url: string; remark: string; is_active: number}[]>([])

/* ── 分组折叠状态 ── */
const collapsedGroups = ref<Set<string>>(new Set(['ssh', 'vnc', 'rdp']))

/* ── 过滤后的配置列表 ── */
const filteredConns = computed(() => conns.value)

const groupedConns = computed(() => {
  const groups: Record<string, SshConn[]> = { ssh: [], vnc: [], rdp: [] }
  for (const c of filteredConns.value) {
    const t = c.connection_type || 'ssh'
    if (groups[t]) groups[t].push(c)
    else groups.ssh.push(c)
  }
  for (const k of Object.keys(groups)) {
    groups[k].sort((a, b) => (a.sort_order ?? 0) - (b.sort_order ?? 0))
  }
  return groups
})

/* ── 排序: 每个分组一个可写 ref,渲染与上移/下移都基于它 ── */
const sshGroup = ref<SshConn[]>([])
const vncGroup = ref<SshConn[]>([])
const rdpGroup = ref<SshConn[]>([])
let syncTimer: ReturnType<typeof setTimeout> | null = null

function syncDragGroups() {
  if (syncTimer) clearTimeout(syncTimer)
  syncTimer = setTimeout(() => {
    sshGroup.value = groupedConns.value.ssh.map(c => ({ ...c }))
    vncGroup.value = groupedConns.value.vnc.map(c => ({ ...c }))
    rdpGroup.value = groupedConns.value.rdp.map(c => ({ ...c }))
  }, 50)
}
watch(groupedConns, syncDragGroups, { immediate: true })
watch(conns, () => nextTick(syncDragGroups))

const groupRefs = { ssh: sshGroup, vnc: vncGroup, rdp: rdpGroup }

function moveCard(groupKey: keyof typeof groupRefs, idx: number, dir: -1 | 1) {
  const group = groupRefs[groupKey]
  const list = group.value
  const j = idx + dir
  if (j < 0 || j >= list.length) return
  const next = list.slice()
  const tmp = next[idx]
  next[idx] = next[j]
  next[j] = tmp
  group.value = next
  const items = next.map((c, i) => ({ id: c.id, sort_order: i }))
  const idMap = new Map(items.map(it => [it.id!, it.sort_order]))
  conns.value = conns.value.map(c => (idMap.has(c.id!) ? { ...c, sort_order: idMap.get(c.id!)! } : c))
  if (items.length) api.sshReorder(items).catch(() => {})
}


/* ── 初始化 ── */
let termLib: any = null
let FitAddon: any = null

onMounted(async () => {
  conns.value = await api.sshList()
  loadAgents()
  loadGateways()
  window.addEventListener('open-ssh', ((e: CustomEvent) => {
    const c = conns.value.find((c: any) => c.name === e.detail)
    if (c) openSshTab(c)
  }) as EventListener)
})
onBeforeUnmount(() => { tabs.value.forEach((t) => closeTerm(t.id)) })

async function loadAgents() {
  try { const data = await api.adminListAgents(); agents.value = data.agents } catch {}
}
async function loadGateways() {
  try { const data = await api.adminListGateways(); gateways.value = data.gateways } catch {}
}
async function loadXterm() {
  if (termLib) return
  const xmod = await import('@xterm/xterm')
  const fitmod = await import('@xterm/addon-fit')
  await import('@xterm/xterm/css/xterm.css')
  termLib = xmod.Terminal
  FitAddon = fitmod.FitAddon
}

/* ── 分组操作 ── */
function toggleGroup(group: string) {
  const s = new Set(collapsedGroups.value)
  if (s.has(group)) s.delete(group); else s.add(group)
  collapsedGroups.value = s
}

/* ── 侧栏操作 ── */
function toggleExpand(id: string, group: string) {
  const s = new Set(expandedIds.value)
  if (s.has(id)) {
    s.delete(id)
  } else {
    for (const c of groupedConns.value[group] || []) {
      if (c.id) s.delete(c.id)
    }
    s.add(id)
  }
  expandedIds.value = s
}

function resolveGatewayUrl(conn: SshConn): string {
  if (!conn.gateway_id) return ''
  const gw = gateways.value.find(g => g.id === conn.gateway_id)
  return gw?.url || ''
}
function resolveGatewayId(conn: SshConn): string { return conn.gateway_id || '' }

/** Ensure gateways list is loaded before resolving URL (avoids silent direct fallback). */
async function ensureGatewaysLoaded(conn: SshConn): Promise<void> {
  if (!conn.gateway_id) return
  if (gateways.value.some(g => g.id === conn.gateway_id)) return
  await loadGateways()
  if (!gateways.value.some(g => g.id === conn.gateway_id)) {
    console.warn('[SSH] gateway list missing id after load:', conn.gateway_id, 'known=', gateways.value.map(g => g.id))
  }
}
function resolveGatewayName(conn: SshConn): string {
  if (!conn.gateway_id) return ''
  const gw = gateways.value.find(g => g.id === conn.gateway_id)
  return gw?.name || ''
}
function resolveGatewayIp(conn: SshConn): string {
  if (!conn.gateway_id) return ''
  const gw = gateways.value.find(g => g.id === conn.gateway_id)
  if (!gw?.url) return ''
  try { return new URL(gw.url).hostname } catch { return '' }
}

/* ── 连接 ── */
function openSshTab(conn: SshConn) { connectSshWithMode(conn) }
function openFileTab(conn: SshConn) { connectFileWithMode(conn) }

function connectSshWithMode(conn: SshConn) {
  loadXterm().then(() => {
    const same = tabs.value.filter(t => t.conn.id === conn.id && t.type !== 'filemanager')
    const label = same.length === 0 ? conn.name : `${conn.name}-${same.length}`
    const tabId = `${conn.id || conn.host}-${Date.now()}`
    tabs.value.push({ id: tabId, conn, label, status: 'connecting', type: 'terminal', connMode: 'agent' })
    activeTab.value = tabId
    nextTick(() => createTerm(tabId, conn))
  })
}

async function connectFileWithMode(conn: SshConn) {
  const existing = tabs.value.find(t => t.conn.id === conn.id && t.type === 'filemanager')
  if (existing) { activeTab.value = existing.id; return }
  const tabId = `file-${conn.id || conn.host}-${Date.now()}`
  let webrtc: WebRTCManager | null = null
  const token = localStorage.getItem('token')
  if (!token) { toast?.error(t('ssh.notLoggedIn')); return }
  await ensureGatewaysLoaded(conn)
  webrtc = new WebRTCManager(token, tabId)
    webrtc.setConnType("ssh")
    webrtc.setMode("auto")
  webrtc.setConnMeta({ connConfigId: conn.id || "", connName: conn.name || "", host: conn.host, port: conn.port, username: conn.username, agentName: agents.value.find(a => a.id === conn.agent_id)?.name || "", gatewayName: gateways.value.find(g => g.id === conn.gateway_id)?.name || "", gateway_ip: resolveGatewayIp(conn) })
  webrtcRefs.value[tabId] = webrtc
  webrtc.on('open', async () => {
    let pw = conn.password || ''
    if (!pw && conn.id) { try { const r = await api.sshGetPassword(conn.id); pw = r.password || '' } catch {} }
    webrtc!.sendSshConnect({ host: conn.host, port: conn.port, username: conn.username, auth_type: conn.auth_type, password: pw }, 80, 24)
    const tab = tabs.value.find(t => t.id === tabId)
    if (tab) tab.status = 'connected'
  })
  // FILE 不注册 terminal（无 xterm），但首帧/0xFE 仍会 markFirstData → 触发 SshFileBrowser 轮询
  webrtc.on('sshConnected', () => {
    const tab = tabs.value.find(t => t.id === tabId)
    if (tab && tab.status !== 'connected') tab.status = 'connected'
  })
  webrtc.on('close', () => { const tab = tabs.value.find(t => t.id === tabId); if (tab) tab.status = 'disconnected' })
  webrtc.on('error', (error: Error) => { toast?.error(error.message); const tab = tabs.value.find(t => t.id === tabId); if (tab) tab.status = 'disconnected' })
  webrtc.connect(conn.agent_id || '', resolveGatewayUrl(conn), resolveGatewayId(conn))
  tabs.value.push({ id: tabId, conn, label: `${conn.name} ${t('ssh.fileBrowser')}`, status: 'connecting', type: 'filemanager', connMode: 'agent' })
  activeTab.value = tabId
}

/* ── 终端 ── */
function createTerm(tabId: string, conn: SshConn) {
  const el = termContainers.value[tabId]
  if (!el || terminals.value[tabId]) return
  const isDark = document.documentElement.dataset.theme === 'dark'
  const theme: Record<string, string> = {
    background: isDark ? '#0f172a' : '#1e1e1e', foreground: isDark ? '#e2e8f0' : '#d4d4d4',
    cursor: '#2563eb', cursorAccent: '#ffffff', selectionBackground: '#264f78',
    black: '#0f172a', red: '#f87171', green: '#34d399', yellow: '#fbbf24',
    blue: '#60a5fa', magenta: '#a78bfa', cyan: '#22d3ee', white: '#d4d4d4',
    brightBlack: '#6b7280', brightRed: '#fca5a5', brightGreen: '#6ee7b7',
    brightYellow: '#fde68a', brightBlue: '#93c5fd', brightMagenta: '#c4b5fd',
    brightCyan: '#67e8f9', brightWhite: '#f3f4f6'
  }
  const term = new termLib({ fontFamily: '"SFMono-Regular", Consolas, "Liberation Mono", Menlo, monospace', fontSize: 16, lineHeight: 1.3, cursorBlink: true, theme })
  const fit = new FitAddon()
  term.loadAddon(fit)
  terminals.value[tabId] = term
  fitAddons.value[tabId] = fit
  term.open(el)
  nextTick(() => {
    fit.fit()
    connectWebRTC(tabId, conn, term)
    const ro = new ResizeObserver(() => { try { fit.fit() } catch {} })
    ro.observe(el)
    resizeObservers.value[tabId] = ro
  })
}

function connectWebRTC(tabId: string, conn: SshConn, term: any) {
  const token = localStorage.getItem('token')
  if (!token) { term.write('\r\n\x1b[31m' + t('ssh.notLoggedIn') + '\x1b[0m\r\n'); return }
  void (async () => {
    await ensureGatewaysLoaded(conn)
    const webrtc = new WebRTCManager(token, tabId)
    webrtc.setConnType("ssh")
    webrtc.setMode("auto")
    webrtc.setConnMeta({ connConfigId: conn.id || "", connName: conn.name || "", host: conn.host, port: conn.port, username: conn.username, agentName: agents.value.find(a => a.id === conn.agent_id)?.name || "", gatewayName: gateways.value.find(g => g.id === conn.gateway_id)?.name || "", gateway_ip: resolveGatewayIp(conn) })
    webrtcRefs.value[tabId] = webrtc
    webrtc.on('open', async () => {
      const tab = tabs.value.find(t => t.id === tabId)
      if (tab && tab.status === 'connecting') tab.status = 'connected'
      let pw = conn.password || ''
      if (!pw && conn.id) { try { const r = await api.sshGetPassword(conn.id); pw = r.password || '' } catch {} }
      webrtc.sendSshConnect({ host: conn.host, port: conn.port, username: conn.username, auth_type: conn.auth_type, password: pw }, term.cols, term.rows)
    })
    let _termBuf: Uint8Array[] = []; let _termRaf = 0; let _termBufBytes = 0; let _termDisposed = false
    const _flushTerm = () => {
      _termRaf = 0
      if (_termBuf.length === 0) return
      if (_termDisposed || !terminals.value[tabId]) { _termBuf = []; _termBufBytes = 0; return }
      const merged = new Uint8Array(_termBuf.reduce((s, a) => s + a.length, 0)); let off = 0
      for (const chunk of _termBuf) { merged.set(chunk, off); off += chunk.length }
      _termBuf = []; _termBufBytes = 0
      try { term.write(merged) } catch {}
    }
    webrtc.on('terminal', (data: ArrayBuffer) => {
      webrtc.markFirstData()
      const chunk = new Uint8Array(data)
      _termBuf.push(chunk); _termBufBytes += chunk.length
      // P2: 缓冲上限 2MB，超限立即 flush 防止内存无限增长
      if (_termBufBytes >= 2 * 1024 * 1024) { _flushTerm() }
      else if (!_termRaf) _termRaf = requestAnimationFrame(_flushTerm)
      const tab = tabs.value.find(t => t.id === tabId); if (tab && tab.status === 'connecting') tab.status = 'connected'
    })
    webrtc.on('close', () => {
      _termDisposed = true
      if (_termRaf) { cancelAnimationFrame(_termRaf); _termRaf = 0 }
      term.write('\r\n\x1b[33mWebRTC连接已断开\x1b[0m\r\n')
      const tab = tabs.value.find(t => t.id === tabId); if (tab) tab.status = 'disconnected'
    })
    webrtc.on('error', (error: Error) => { term.write('\r\n\x1b[31m' + error.message + '\x1b[0m\r\n'); const tab = tabs.value.find(t => t.id === tabId); if (tab) tab.status = 'disconnected' })
    webrtc.connect(conn.agent_id || '', resolveGatewayUrl(conn), resolveGatewayId(conn))
    term.onData((data: string) => { if (webrtcRefs.value[tabId]) webrtcRefs.value[tabId]!.sendTerminal(data) })
    term.onResize(({ cols, rows }: { cols: number; rows: number }) => { if (webrtcRefs.value[tabId]) webrtcRefs.value[tabId]!.sendResize(cols, rows) })
  })()
}

/* ── VNC 连接 ── */
async function openVncTab(conn: SshConn) {
  const existing = tabs.value.find(t => t.conn.id === conn.id && t.type === 'vnc')
  if (existing) { activeTab.value = existing.id; return }
  const tabId = `vnc-${conn.id || conn.host}-${Date.now()}`
  let webrtc: WebRTCManager | null = null
  const token = localStorage.getItem('token')
  if (!token) { toast?.error(t('ssh.notLoggedIn')); return }
  await ensureGatewaysLoaded(conn)
  let vncPw = conn.vnc_password || ''
  console.log('[VNC-TAB] initial vnc_password length:', vncPw.length, 'conn.id:', conn.id)
  if (!vncPw && conn.id) {
    try {
      const r = await api.sshGetPassword(conn.id)
      vncPw = (r as any).vnc_password || ''
      console.log('[VNC-TAB] fetched vnc_password length:', vncPw.length)
    } catch (e) {
      console.error('[VNC-TAB] fetch password failed:', e)
    }
  }
  conn.vnc_password = vncPw
  console.log('[VNC-TAB] final vnc_password length:', conn.vnc_password.length)
  webrtc = new WebRTCManager(token, tabId)
    webrtc.setConnType("vnc")
    webrtc.setMode("auto")
  webrtc.setConnMeta({ connConfigId: conn.id || "", connName: conn.name || "", host: conn.host, port: conn.vnc_port || 5900, username: conn.username, agentName: agents.value.find(a => a.id === conn.agent_id)?.name || "", gatewayName: gateways.value.find(g => g.id === conn.gateway_id)?.name || "", gateway_ip: resolveGatewayIp(conn) })
  webrtcRefs.value[tabId] = webrtc
  webrtc.on('open', async () => {
    webrtc!.sendVncConnect({ host: conn.host, port: conn.vnc_port || 5900, password: vncPw, pixel_format: conn.pixel_format || 'RGB888', color_depth: conn.color_depth || 32, read_only: !!conn.read_only })
    const tab = tabs.value.find(t => t.id === tabId)
    if (tab) tab.status = 'connected'
  })
  webrtc.on('close', () => { const tab = tabs.value.find(t => t.id === tabId); if (tab) tab.status = 'disconnected' })
  webrtc.on('error', (error: Error) => { toast?.error(error.message); const tab = tabs.value.find(t => t.id === tabId); if (tab) tab.status = 'disconnected' })
  tabs.value.push({ id: tabId, conn, label: `VNC - ${conn.name}`, status: 'connecting', type: 'vnc', connMode: 'agent' })
  activeTab.value = tabId
  await nextTick()
  webrtc.connect(conn.agent_id || '', resolveGatewayUrl(conn), resolveGatewayId(conn))
}

function reconnectTab(tabId: string) {
  const tab = tabs.value.find(t => t.id === tabId)
  if (!tab) return
  closeTerm(tabId)
  if (tab.type === 'vnc') {
    // R13: 修复参数错误 — openVncTab 只接收 conn
    tabs.value = tabs.value.filter(t => t.id !== tabId)
    nextTick(() => openVncTab(tab.conn))
  } else {
    terminals.value[tabId] = null
    webrtcRefs.value[tabId] = null
    nextTick(() => createTerm(tabId, tab.conn))
  }
}

function closeTab(tabId: string) {
  closeTerm(tabId)
  tabs.value = tabs.value.filter(t => t.id !== tabId)
  if (activeTab.value === tabId) activeTab.value = tabs.value.length ? tabs.value[tabs.value.length - 1].id : ''
}

async function closeTerm(tabId: string) {
  const t = terminals.value[tabId]; if (t) { t.dispose(); delete terminals.value[tabId] }
  const webrtc = webrtcRefs.value[tabId]; if (webrtc) { await webrtc.close(); delete webrtcRefs.value[tabId] }
  const vnc = vncRefs.value[tabId]; if (vnc) { try { vnc.disconnect() } catch {}; delete vncRefs.value[tabId] }
  const ro = resizeObservers.value[tabId]; if (ro) { try { ro.disconnect() } catch {}; delete resizeObservers.value[tabId] }
  delete fitAddons.value[tabId]
}

function refitAll() {
  nextTick(() => {
    for (const tabId of Object.keys(fitAddons.value)) {
      try { fitAddons.value[tabId]?.fit() } catch {}
    }
  })
}

function onTabClick(tabId: string) { activeTab.value = tabId; nextTick(() => { const t = terminals.value[tabId]; if (t) t.focus() }) }
const activeConn = () => tabs.value.find(t => t.id === activeTab.value)

/* ── 配置管理 ── */
function openNew() {
  form.value = { name: '', host: '', port: 22, username: 'root', auth_type: 'password', password: '', key_path: '', connection_mode: 'agent', agent_id: '', gateway_id: '', remark: '', connection_type: 'ssh', vnc_port: 5900, vnc_password: '', pixel_format: 'tight', color_depth: 'full', read_only: false, rdp_port: 3389, rdp_password: '', rdp_domain: '', rdp_resolution: '1920x1080' }
  isEdit.value = false; editingId.value = ''; showForm.value = true
}

async function openEditConn(conn: SshConn) {
  form.value = { ...conn, password: '', key_path: conn.key_path || '', connection_mode: conn.connection_mode || 'agent', agent_id: conn.agent_id || '', gateway_id: conn.gateway_id || '', remark: conn.remark || '', connection_type: conn.connection_type || 'ssh', vnc_port: conn.vnc_port || 5900, vnc_password: '', pixel_format: conn.pixel_format || 'tight', color_depth: conn.color_depth || 'full', read_only: conn.read_only || false, rdp_port: conn.rdp_port || 3389, rdp_password: '', rdp_domain: conn.rdp_domain || '', rdp_resolution: conn.rdp_resolution || '1920x1080' }
  editingId.value = conn.id || ''; isEdit.value = true; showForm.value = true
  if (conn.id) {
    try {
      const pw = await api.sshGetPassword(conn.id)
      form.value.password = pw.password || ''
      form.value.vnc_password = pw.vnc_password || ''
      form.value.rdp_password = pw.rdp_password || ''
    } catch {}
  }
}

async function saveConn() {
  if (!form.value.name.trim() || !form.value.host.trim()) { toast?.error(t('ssh.nameRequired')); return }
  if (form.value.connection_type === 'ssh' && !form.value.username.trim()) { toast?.error(t('ssh.sshUsernameRequired')); return }
  try {
    if (isEdit.value) { await api.sshUpdate({ ...form.value, id: editingId.value }) }
    else { await api.sshAdd(form.value) }
    showForm.value = false; conns.value = await api.sshList()
    toast?.success(isEdit.value ? t('ssh.updateSuccess') : t('ssh.addSuccess'))
  } catch (e: any) { toast?.error(e.message) }
}

function confirmDelete(id: string, name: string) { deleteTargetId.value = id; deleteTargetName.value = name; showDeleteConfirm.value = true }

async function doDelete() {
  try { await api.sshDelete(deleteTargetId.value); showDeleteConfirm.value = false; conns.value = await api.sshList(); toast?.success(t('ssh.deleteSuccess')) } catch (e: any) { toast?.error(e.message) }
}

async function testConn() {
  testing.value = true
  try { const r = await api.sshTest(form.value); if (r.ok) toast?.success(t('ssh.testSuccess')); else toast?.error(r.detail || t('ssh.testFailed')) }
  catch (e: any) { toast?.error(t('ssh.testFailed') + e.message) }
  finally { testing.value = false }
}

function typeLabel(t: string) {
  return t === 'vnc' ? 'VNC' : t === 'rdp' ? 'RDP' : 'SSH'
}
</script>

<template>
  <div class="ssh-wrap">
    <div class="breadcrumb" style="position:absolute;top:0;left:260px;right:0;padding:8px 16px;z-index:1;font-size:13px;">{{ t('ssh.title') }}</div>
    <!-- 左侧连接列表 -->
    <aside class="ssh-sidebar">
      <div class="ssh-sidebar-head">
        <div class="sidebar-title">
          <span class="sidebar-icon">💻</span>
          <span>{{ t('ssh.title') }}</span>
        </div>
        <div class="sidebar-actions">
          <button class="icon-btn primary sm" @click="openNew" :title="t('ssh.newConn')">＋</button>
        </div>
      </div>
      <div class="ssh-list">
        <!-- SSH/FILE 分组 -->
        <div v-if="groupedConns.ssh.length || !collapsedGroups.has('ssh')" class="conn-group">
          <div class="group-header" @click="toggleGroup('ssh')">
            <span class="group-arrow" :class="{ expanded: !collapsedGroups.has('ssh') }">▸</span>
            <span class="group-label">SSH/FILE ({{ groupedConns.ssh.length }})</span>
          </div>
          <div v-if="!collapsedGroups.has('ssh')" class="group-items">
            <div>
              <template v-for="(c, idx) in sshGroup" :key="c.id">
                <div :data-id="c.id" class="ssh-card type-ssh" :class="{ expanded: expandedIds.has(c.id!) }">
                  <div class="ssh-card-main" @click="toggleExpand(c.id!, 'ssh')">
                    <div class="ssh-card-left">
                      <span class="type-bar"></span>
                      <div class="ssh-card-info">
                        <span class="ssh-card-name">{{ c.name }}</span>
                        <span class="ssh-card-host">{{ c.host }}:{{ c.port }}</span>
                      </div>
                    </div>
                    <div class="ssh-card-right">
                      <span v-if="c.gateway_id" class="gw-badge" :class="gateways.find(g => g.id === c.gateway_id)?.online ? 'online' : 'offline'">🌐</span>
                      <span class="move-btns">
                        <button class="move-btn" :disabled="idx === 0" title="上移" @click.stop="moveCard('ssh', idx, -1)">↑</button>
                        <button class="move-btn" :disabled="idx === sshGroup.length - 1" title="下移" @click.stop="moveCard('ssh', idx, 1)">↓</button>
                      </span>
                      <span class="si-dot" :class="{ online: tabs.some(t => t.conn.id === c.id && t.status === 'connected') }"></span>
                      <span class="si-arrow" :class="{ expanded: expandedIds.has(c.id!) }">▸</span>
                    </div>
                  </div>
                  <div v-if="expandedIds.has(c.id!)" class="ssh-card-expanded">
                    <div class="ssh-card-row">
                      <div class="ssh-card-actions">
                        <button class="ssh-sub-btn type-ssh" @click.stop="openSshTab(c)">SSH</button>
                        <button class="ssh-sub-btn type-file" @click.stop="openFileTab(c)">FILE</button>
                      </div>
                      <div class="ssh-card-manage">
                        
                        <button class="ssh-card-btn edit" @click.stop="openEditConn(c)">{{ t('common.edit') }}</button>
                        <button class="ssh-card-btn danger" @click.stop="confirmDelete(c.id!, c.name)">{{ t('common.delete') }}</button>
                      </div>
                    </div>
                  </div>
                </div>
              </template>
            </div>
          </div>
        </div>
        <!-- VNC 分组 -->
        <div v-if="groupedConns.vnc.length || !collapsedGroups.has('vnc')" class="conn-group">
          <div class="group-header" @click="toggleGroup('vnc')">
            <span class="group-arrow" :class="{ expanded: !collapsedGroups.has('vnc') }">▸</span>
            <span class="group-label">VNC ({{ groupedConns.vnc.length }})</span>
          </div>
          <div v-if="!collapsedGroups.has('vnc')" class="group-items">
            <div>
              <template v-for="(c, idx) in vncGroup" :key="c.id">
                <div :data-id="c.id" class="ssh-card type-vnc" :class="{ expanded: expandedIds.has(c.id!) }">
                  <div class="ssh-card-main" @click="toggleExpand(c.id!, 'vnc')">
                    <div class="ssh-card-left">
                      <span class="type-bar"></span>
                      <div class="ssh-card-info">
                        <span class="ssh-card-name">{{ c.name }}</span>
                        <span class="ssh-card-host">{{ c.host }}:{{ c.vnc_port || 5900 }}</span>
                      </div>
                    </div>
                    <div class="ssh-card-right">
                      <span v-if="c.gateway_id" class="gw-badge" :class="gateways.find(g => g.id === c.gateway_id)?.online ? 'online' : 'offline'">🌐</span>
                      <span class="move-btns">
                        <button class="move-btn" :disabled="idx === 0" title="上移" @click.stop="moveCard('vnc', idx, -1)">↑</button>
                        <button class="move-btn" :disabled="idx === vncGroup.length - 1" title="下移" @click.stop="moveCard('vnc', idx, 1)">↓</button>
                      </span>
                      <span class="si-dot" :class="{ online: tabs.some(t => t.conn.id === c.id && t.status === 'connected') }"></span>
                      <span class="si-arrow" :class="{ expanded: expandedIds.has(c.id!) }">▸</span>
                    </div>
                  </div>
                  <div v-if="expandedIds.has(c.id!)" class="ssh-card-expanded">
                    <div class="ssh-card-row">
                      <div class="ssh-card-actions">
                        <button class="ssh-sub-btn type-vnc" @click.stop="openVncTab(c)">VNC</button>
                      </div>
                      <div class="ssh-card-manage">
                        
                        <button class="ssh-card-btn edit" @click.stop="openEditConn(c)">{{ t('common.edit') }}</button>
                        <button class="ssh-card-btn danger" @click.stop="confirmDelete(c.id!, c.name)">{{ t('common.delete') }}</button>
                      </div>
                    </div>
                  </div>
                </div>
              </template>
            </div>
          </div>
        </div>
        <!-- RDP 分组 -->
        <div v-if="groupedConns.rdp.length || !collapsedGroups.has('rdp')" class="conn-group">
          <div class="group-header" @click="toggleGroup('rdp')">
            <span class="group-arrow" :class="{ expanded: !collapsedGroups.has('rdp') }">▸</span>
            <span class="group-label">RDP ({{ groupedConns.rdp.length }})</span>
          </div>
          <div v-if="!collapsedGroups.has('rdp')" class="group-items">
            <div>
              <template v-for="(c, idx) in rdpGroup" :key="c.id">
                <div :data-id="c.id" class="ssh-card type-rdp" :class="{ expanded: expandedIds.has(c.id!) }">
                  <div class="ssh-card-main" @click="toggleExpand(c.id!, 'rdp')">
                    <div class="ssh-card-left">
                      <span class="type-bar"></span>
                      <div class="ssh-card-info">
                        <span class="ssh-card-name">{{ c.name }}</span>
                        <span class="ssh-card-host">{{ c.host }}:{{ c.rdp_port || 3389 }}</span>
                      </div>
                    </div>
                    <div class="ssh-card-right">
                      <span v-if="c.gateway_id" class="gw-badge" :class="gateways.find(g => g.id === c.gateway_id)?.online ? 'online' : 'offline'">🌐</span>
                      <span class="move-btns">
                        <button class="move-btn" :disabled="idx === 0" title="上移" @click.stop="moveCard('rdp', idx, -1)">↑</button>
                        <button class="move-btn" :disabled="idx === rdpGroup.length - 1" title="下移" @click.stop="moveCard('rdp', idx, 1)">↓</button>
                      </span>
                      <span class="si-dot" :class="{ online: tabs.some(t => t.conn.id === c.id && t.status === 'connected') }"></span>
                      <span class="si-arrow" :class="{ expanded: expandedIds.has(c.id!) }">▸</span>
                    </div>
                  </div>
                  <div v-if="expandedIds.has(c.id!)" class="ssh-card-expanded">
                    <div class="ssh-card-row">
                      <div class="ssh-card-actions">
                        <button class="ssh-sub-btn type-rdp disabled" disabled :title="t('ssh.rdpUnavailable')">RDP</button>
                      </div>
                      <div class="ssh-card-manage">
                        <button class="ssh-card-btn edit" @click.stop="openEditConn(c)">{{ t('common.edit') }}</button>
                        <button class="ssh-card-btn danger" @click.stop="confirmDelete(c.id!, c.name)">{{ t('common.delete') }}</button>
                      </div>
                    </div>
                  </div>
                </div>
              </template>
            </div>
          </div>
        </div>
        <div v-if="!conns.length" class="ssh-empty">
          <div class="empty-icon">💻</div>
          <div class="empty-text">{{ t('ssh.noConns') }}</div>
          <div class="empty-hint">{{ t('ssh.addHint') }}</div>
        </div>
      </div>
    </aside>

    <!-- 右侧终端 -->
    <div class="ssh-main">
      <div v-if="tabs.length" class="ssh-tabs">
        <div v-for="t in tabs" :key="t.id" class="ssh-tab" :class="{ active: activeTab === t.id }" @click="onTabClick(t.id)">
          <span class="tab-dot" :class="{ connected: t.status === 'connected', connecting: t.status === 'connecting', disconnected: t.status === 'disconnected' }"></span>
          <span class="tab-label">{{ t.label }}</span>
          <span v-if="t.connMode === 'agent'" class="tab-webrtc" title="Agent模式">🌐</span>
          <button v-if="t.status === 'disconnected'" class="tab-reconnect" @click.stop="reconnectTab(t.id)" title="重新连接">↻</button>
          <button class="tab-close" @click.stop="closeTab(t.id)">✕</button>
        </div>
      </div>
      <div class="ssh-terms">
        <template v-for="t in tabs" :key="t.id">
          <div v-show="activeTab === t.id" class="ssh-term-wrap">
            <div v-if="t.type === 'vnc'" :ref="(el) => { termContainers[t.id] = el as HTMLDivElement }" class="vnc-container">
              <VncViewer :conn="t.conn" :tab-id="t.id" :webrtc-manager="webrtcRefs[t.id]" @status="(s: string) => { const tab = tabs.find(x => x.id === t.id); if (tab) tab.status = s as any }" @error="(msg: string) => {}" />
            </div>
            <div v-else-if="t.type !== 'filemanager'" :ref="(el) => { termContainers[t.id] = el as HTMLDivElement }" class="ssh-term"></div>
            <SshFileBrowser v-else :conn="t.conn" :tab-id="t.id" :webrtc-manager="webrtcRefs[t.id]" style="height:100%" />
          </div>
        </template>
        <div v-if="!tabs.length" class="ssh-placeholder">
          <div class="ph-title">{{ t('ssh.sshConn') }}</div>
          <div class="ph-desc">{{ t('ssh.doubleClickHint') }}</div>
        </div>
      </div>
      <div v-if="activeTab && activeConn()" class="ssh-statusbar">
        <span class="sb-item">{{ activeConn()!.name }}</span>
        <span class="sb-sep">│</span>
        <span class="sb-item sb-status" :class="activeConn()!.status">
          <span class="sb-dot"></span>
          {{ activeConn()!.status === 'connected' ? t('ssh.connected') : activeConn()!.status === 'connecting' ? t('ssh.connecting') : t('ssh.disconnected') }}
        </span>
        <span class="sb-sep">│</span>
        <span class="sb-item">{{ activeConn()!.host }}:{{ activeConn()!.port }}</span>
        <span class="sb-sep">│</span>
        <span class="sb-item">{{ activeConn()!.connMode === 'agent' ? 'Agent' : t('diagnosis.directConn') }}</span>
        <span class="sb-sep">│</span>
        <span class="sb-item">UTF-8</span>
      </div>
    </div>


    <!-- 连接表单弹窗 -->
    <Teleport to="body">
      <div v-if="showForm" class="modal-mask" @click.self="showForm = false">
        <div class="modal-box form-wide">
          <h3>{{ isEdit ? t('ssh.editConn') : t('ssh.newConnTitle') }}</h3>
          <!-- 类型选择器 -->
          <div v-if="!isEdit" class="type-selector">
            <div class="type-card" :class="{ active: form.connection_type === 'ssh' }" @click="form.connection_type = 'ssh'">
              <div class="type-card-icon" style="background:#dbeafe;color:#2563eb">🔵</div>
              <div class="type-card-label">SSH/FILE</div>
              <div class="type-card-desc">终端+文件管理</div>
              <div class="type-card-status available">✓ 可用</div>
            </div>
            <div class="type-card" :class="{ active: form.connection_type === 'vnc' }" @click="form.connection_type = 'vnc'">
              <div class="type-card-icon" style="background:#fed7aa;color:#ea580c">🟠</div>
              <div class="type-card-label">VNC</div>
              <div class="type-card-desc">桌面远程</div>
              <div class="type-card-status available">✓ 可用</div>
            </div>
            <div class="type-card disabled" @click="toast?.info(t('ssh.rdpUnavailable'))">
              <div class="type-card-icon" style="background:#ede9fe;color:#7c3aed">🟣</div>
              <div class="type-card-label">RDP</div>
              <div class="type-card-desc">远程桌面</div>
              <div class="type-card-status reserved">🔒 预留</div>
            </div>
          </div>
          <div v-else class="form-type-display">
            <span class="type-tag" :class="form.connection_type">{{ typeLabel(form.connection_type || 'ssh') }}</span>
          </div>
          <div class="form-cards">
            <!-- 基本信息 -->
            <div class="form-card">
              <div class="form-card-title"><span class="form-card-dot" :class="'dot-' + (form.connection_type || 'ssh')"></span>{{ t('ssh.basicInfo') }}</div>
              <div class="form-card-body">
                <div class="form-field"><label>名称 <span class="required">*</span></label><input v-model="form.name" placeholder="例：生产服务器" /></div>
                <div class="form-field"><label>{{ t('ssh.host') }} <span class="required">*</span></label><input v-model="form.host" placeholder="192.168.1.100" /></div>
                <template v-if="form.connection_type === 'ssh'">
                  <div class="form-field"><label>{{ t('ssh.sshPort') }}</label><input v-model.number="form.port" type="number" placeholder="22" /></div>
                </template>
                <template v-else-if="form.connection_type === 'vnc'">
                  <div class="form-field"><label>{{ t('ssh.vncPort') }}</label><input v-model.number="form.vnc_port" type="number" placeholder="5900" /></div>
                </template>
                <template v-else>
                  <div class="form-field"><label>{{ t('ssh.rdpPort') }}</label><input v-model.number="form.rdp_port" type="number" placeholder="3389" /></div>
                  <div class="form-field"><label>{{ t('ssh.domain') }}</label><input v-model="form.rdp_domain" :placeholder="t('ssh.domainOptional')" /></div>
                </template>
              </div>
            </div>
            <!-- 认证信息 -->
            <div class="form-card">
              <div class="form-card-title"><span class="form-card-dot" :class="'dot-' + (form.connection_type || 'ssh')"></span>{{ t('ssh.authInfo') }}</div>
              <div class="form-card-body">
                <template v-if="form.connection_type === 'ssh'">
                  <div class="form-field"><label>用户名 <span class="required">*</span></label><input v-model="form.username" placeholder="root" /></div>
                  <div class="form-field">
                    <label>{{ t('ssh.authMethod') }}</label>
                    <div class="auth-switch">
                      <button class="auth-btn" :class="{ active: form.auth_type === 'password' }" @click="form.auth_type = 'password'">{{ t('ssh.passwordAuth') }}</button>
                      <button class="auth-btn" :class="{ active: form.auth_type === 'key' }" @click="form.auth_type = 'key'">{{ t('ssh.keyAuth') }}</button>
                    </div>
                  </div>
                  <div v-if="form.auth_type === 'password'" class="form-field">
                    <label>{{ t('ssh.passwordAuth') }} {{ isEdit ? t('ssh.keepBlank') : '' }}</label>
                    <div class="pwd-wrap"><input v-model="form.password" :type="showPasswords.password ? 'text' : 'password'" :placeholder="isEdit && (form as any).has_password ? t('ssh.keepBlank') : t('ssh.sshPassword')" /><button type="button" class="pwd-toggle" @click="togglePassword('password')">{{ showPasswords.password ? '🙈' : '👁' }}</button></div>
                  </div>
                  <div v-else class="form-field"><label>{{ t('ssh.keyPath') }}</label><input v-model="form.key_path" placeholder="~/.ssh/id_rsa" /></div>
                </template>
                <template v-else-if="form.connection_type === 'vnc'">
                  <div class="form-field">
                    <label>{{ t('ssh.vncPassword') }} {{ isEdit ? t('ssh.keepBlank') : '' }}</label>
                    <div class="pwd-wrap"><input v-model="form.vnc_password" :type="showPasswords.vnc_password ? 'text' : 'password'" :placeholder="isEdit && (form as any).has_password ? t('ssh.keepBlank') : t('ssh.vncPassword')" /><button type="button" class="pwd-toggle" @click="togglePassword('vnc_password')">{{ showPasswords.vnc_password ? '🙈' : '👁' }}</button></div>
                  </div>
                  <div class="form-field">
                    <label>{{ t('ssh.pixelFormat') }}</label>
                    <select v-model="form.pixel_format" class="form-select">
                      <option value="tight">Tight (推荐)</option><option value="raw">Raw</option><option value="hextile">Hextile</option><option value="ZRLE">ZRLE</option>
                    </select>
                  </div>
                  <div class="form-field">
                    <label>{{ t('ssh.colorDepth') }}</label>
                    <select v-model="form.color_depth" class="form-select">
                      <option value="full">Full (32bpp)</option><option value="high">High (16bpp)</option><option value="low">Low (8bpp)</option>
                    </select>
                  </div>
                  <div class="form-field">
                    <label>{{ t('ssh.readOnlyMode') }}</label>
                    <div class="auth-switch">
                      <button class="auth-btn" :class="{ active: !form.read_only }" @click="form.read_only = false">{{ t('ssh.operable') }}</button>
                      <button class="auth-btn" :class="{ active: form.read_only }" @click="form.read_only = true">{{ t('ssh.viewOnly') }}</button>
                    </div>
                  </div>
                </template>
                <template v-else>
                  <div class="form-field"><label>用户名</label><input v-model="form.username" placeholder="admin" /></div>
                  <div class="form-field">
                    <label>{{ t('ssh.rdpPassword') }} {{ isEdit ? t('ssh.keepBlank') : '' }}</label>
                    <div class="pwd-wrap"><input v-model="form.rdp_password" :type="showPasswords.rdp_password ? 'text' : 'password'" :placeholder="isEdit ? t('ssh.keepBlank') : t('ssh.rdpPassword')" /><button type="button" class="pwd-toggle" @click="togglePassword('rdp_password')">{{ showPasswords.rdp_password ? '🙈' : '👁' }}</button></div>
                  </div>
                  <div class="form-field">
                    <label>{{ t('ssh.resolution') }}</label>
                    <select v-model="form.rdp_resolution" class="form-select">
                      <option value="1920x1080">1920×1080</option><option value="1366x768">1366×768</option><option value="1280x720">1280×720</option>
                    </select>
                  </div>
                </template>
              </div>
            </div>
          </div>
          <!-- 连接方式 -->
          <div class="form-card form-card-full">
            <div class="form-card-title"><span class="form-card-dot" :class="'dot-' + (form.connection_type || 'ssh')"></span>{{ t('ssh.connMode') }}</div>
            <div class="form-card-body form-card-row">
              <div class="form-field form-field-agent"><label>Agent</label>
                <select v-model="form.agent_id" class="form-select">
                  <option value="">{{ t('ssh.selectAgent') }}</option>
                  <option v-for="a in agents" :key="a.id" :value="a.id">{{ a.name }}{{ a.remark ? ' (' + a.remark + ')' : '' }}</option>
                </select>
              </div>
              <div class="form-field form-field-agent"><label>{{ t('ssh.gatewayOptional') }}</label>
                <select v-model="form.gateway_id" class="form-select">
                  <option value="">{{ t('ssh.noGateway') }}</option>
                  <option v-for="g in gateways" :key="g.id" :value="g.id">{{ g.name }}{{ g.remark ? ' (' + g.remark + ')' : '' }}</option>
                </select>
              </div>
            </div>
          </div>
          <div class="form-remark"><label>{{ t('common.remark') }}</label><input v-model="form.remark" :placeholder="t('admin.optionalRemark')" /></div>
          <div class="form-actions">
            <button v-if="form.connection_type === 'ssh'" class="btn info" @click="testConn" :disabled="testing">{{ testing ? t('ssh.testing') : t('ssh.testConn') }}</button>
            <div v-else></div>
            <div class="form-actions-right">
              <button class="btn" @click="showForm = false">{{ t('common.cancel') }}</button>
              <button class="btn primary" @click="saveConn">{{ isEdit ? t('common.save') : t('common.add') }}</button>
            </div>
          </div>
        </div>
      </div>
    </Teleport>

    <!-- 删除确认弹窗 -->
    <Teleport to="body">
      <div v-if="showDeleteConfirm" class="modal-mask" @click.self="showDeleteConfirm = false">
        <div class="modal-box modal-danger">
          <h3>{{ t('common.confirmDelete') }}</h3>
          <p class="modal-desc">{{ t('ssh.deleteConfirm') }}「<b>{{ deleteTargetName }}</b>」？</p>
          <div class="modal-actions">
            <button class="btn" @click="showDeleteConfirm = false">{{ t('common.cancel') }}</button>
            <button class="btn danger" @click="doDelete">{{ t('common.confirmDelete') }}</button>
          </div>
        </div>
      </div>
    </Teleport>
  </div>
</template>

<style scoped>
.ssh-wrap { display:flex; height:100%; overflow:hidden; }
.ssh-sidebar { width:260px; flex-shrink:0; background:var(--panel); border-right:1px solid var(--border); display:flex; flex-direction:column; }
.ssh-sidebar-head { display:flex; align-items:center; justify-content:space-between; padding:14px 16px; border-bottom:1px solid var(--border); }
.sidebar-title { display:flex; align-items:center; gap:8px; font-size:14px; font-weight:600; }
.sidebar-icon { font-size:16px; }
.sidebar-actions { display:flex; gap:4px; }
.icon-btn.sm { min-width:28px; min-height:28px; font-size:16px; border-radius:6px; border:1px solid var(--border); background:var(--panel); color:var(--fg-2); cursor:pointer; display:inline-flex; align-items:center; justify-content:center; }
.icon-btn.sm:hover { background:var(--accent-soft); color:var(--accent); border-color:var(--accent); }
.icon-btn.primary { background:var(--accent); color:var(--accent-fg); border-color:var(--accent); }
.icon-btn.primary:hover { opacity:.9; }
.ssh-list { flex:1; overflow-y:auto; padding:8px; display:flex; flex-direction:column; gap:2px; }

/* 分组 */
.conn-group { margin-bottom:4px; }
.group-header { display:flex; align-items:center; gap:6px; padding:6px 8px; cursor:pointer; border-radius:6px; user-select:none; }
.group-header:hover { background:var(--panel-2); }
.group-arrow { font-size:11px; color:var(--muted); transition:transform .15s; display:inline-block; width:12px; }
.group-arrow.expanded { transform:rotate(90deg); }
.group-label { font-size:12px; font-weight:600; color:var(--muted); text-transform:uppercase; letter-spacing:.03em; }
.group-items { display:flex; flex-direction:column; gap:4px; padding:0 0 4px; }
.ssh-card-main { display:flex; align-items:center; justify-content:space-between; padding:8px 10px; cursor:pointer; gap:6px; min-height:48px; }
.ssh-card-left { display:flex; align-items:center; gap:8px; min-width:0; flex:1; }
.type-bar { width:3px; height:28px; border-radius:2px; flex-shrink:0; }
.type-ssh .type-bar { background:#2563eb; }
.type-vnc .type-bar { background:#ea580c; }
.type-rdp .type-bar { background:#7c3aed; }
.ssh-card-info { display:flex; flex-direction:column; min-width:0; }
.ssh-card-name { font-size:13px; font-weight:600; color:var(--fg); overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
.ssh-card-host { font-size:11px; color:var(--muted); overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
.ssh-card-right { display:flex; align-items:center; gap:6px; flex-shrink:0; }
.si-dot { width:8px; height:8px; border-radius:50%; background:var(--muted); flex-shrink:0; transition:all .3s; }
.si-dot.sm { width:6px; height:6px; }
.si-dot.online { background:#34d399; box-shadow:0 0 6px #34d399; }
.si-arrow { font-size:11px; color:var(--muted); transition:transform .15s; display:inline-block; }
.si-arrow.expanded { transform:rotate(90deg); }
.gw-badge { font-size:11px; padding:1px 4px; border-radius:4px; line-height:1; }
.gw-badge.online { background:#d1fae5; color:#16a34a; }
.gw-badge.offline { background:#f3f4f6; color:#6b7280; }
.ssh-card-expanded { padding:6px 10px 8px; border-top:1px solid var(--border); }
.ssh-card-row { display:flex; align-items:center; justify-content:space-between; }
.ssh-card-actions { display:flex; gap:4px; }
.ssh-card-manage { display:flex; gap:6px; align-items:center; }
.ssh-card-btn { min-height:24px; padding:0 10px; font-size:12px; border:1px solid var(--border); border-radius:5px; background:var(--panel); color:var(--fg-2); cursor:pointer; display:inline-flex; align-items:center; justify-content:center; }
.ssh-card-btn:hover { border-color:var(--accent); color:var(--accent); }
.ssh-card-btn.edit { background:#dbeafe; color:#2563eb; border-color:#93c5fd; }
.ssh-card-btn.danger { color:#dc2626; border-color:#fecaca; }
.ssh-card-btn.danger:hover { background:#dc2626; color:white; }
.gw-label { font-size:11px; color:var(--muted); margin-left:auto; }
.ssh-sub-btn { display:flex; align-items:center; justify-content:center; min-height:26px; padding:0 10px; border:1px solid var(--border); border-radius:5px; background:var(--panel); color:var(--fg-2); font-size:12px; cursor:pointer; transition:all .15s; font-weight:500; }
.ssh-sub-btn:hover { border-color:var(--accent); color:var(--accent); }
.ssh-sub-btn.disabled { opacity:.4; cursor:not-allowed; background:var(--panel-2); color:var(--muted); border-color:var(--border); }
.ssh-sub-btn.type-ssh { background:#dbeafe; color:#2563eb; border-color:#93c5fd; font-weight:600; }
.ssh-sub-btn.type-ssh:hover { background:#bfdbfe; border-color:#2563eb; }
.ssh-sub-btn.type-file { background:#dcfce7; color:#16a34a; border-color:#86efac; font-weight:600; }
.ssh-sub-btn.type-file:hover { background:#bbf7d0; border-color:#16a34a; }
.ssh-sub-btn.type-vnc { background:#fed7aa; color:#ea580c; border-color:#fdba74; font-weight:600; }
.ssh-sub-btn.type-vnc:hover { background:#fde68a; border-color:#ea580c; }
.ssh-sub-btn.type-rdp { background:#ede9fe; color:#7c3aed; border-color:#c4b5fd; font-weight:600; }
.ssh-empty { text-align:center; padding:32px 12px; }
.ssh-empty .empty-icon { font-size:32px; margin-bottom:8px; opacity:.3; }
.ssh-empty .empty-text { font-size:13px; color:var(--muted); margin-bottom:4px; }
.ssh-empty .empty-hint { font-size:11px; color:var(--muted); opacity:.6; }
.ssh-main { flex:1; display:flex; flex-direction:column; min-width:0; overflow:hidden; }
.ssh-tabs { display:flex; gap:0; background:var(--panel); border-bottom:1px solid var(--border); overflow-x:auto; scrollbar-width:none; }
.ssh-tabs::-webkit-scrollbar { display:none; }
.ssh-tab { display:flex; align-items:center; gap:6px; padding:0 16px; min-height:38px; font-size:13px; color:var(--muted); cursor:pointer; border-right:1px solid var(--border); white-space:nowrap; position:relative; transition:color .15s, background .15s; }
.ssh-tab:hover { background:var(--panel-2); color:var(--fg-2); }
.ssh-tab.active { color:var(--fg); background:var(--bg); }
.ssh-tab.active::after { content:''; position:absolute; left:0; right:0; bottom:0; height:2px; background:var(--accent); }
.tab-dot { width:6px; height:6px; border-radius:50%; background:var(--muted); flex-shrink:0; transition:background .3s; }
.tab-dot.connected { background:#34d399; }
.tab-dot.connecting { background:#fbbf24; animation:pulse 1s infinite; }
.tab-dot.disconnected { background:#dc2626; }
.tab-label { overflow:hidden; text-overflow:ellipsis; max-width:120px; }
.tab-webrtc { font-size:13px; }
.tab-reconnect { min-width:18px; min-height:18px; font-size:13px; border-radius:4px; color:#f59e0b; font-weight:700; border:none; background:none; cursor:pointer; }
.tab-reconnect:hover { background:#f59e0b20; }
.tab-close { min-width:18px; min-height:18px; font-size:13px; border-radius:4px; color:var(--muted); border:none; background:none; cursor:pointer; }
.tab-close:hover { background:var(--panel-2); color:var(--fg); }
@keyframes pulse { 0%,100% { opacity:1; } 50% { opacity:.4; } }
.ssh-terms { flex:1; position:relative; overflow:hidden; background:#1e1e1e; }
.ssh-term-wrap { position:absolute; inset:0; }
.ssh-term { position:absolute; inset:0; }
.ssh-term :deep(.xterm) { padding:8px 12px; }
.ssh-placeholder { position:absolute; inset:0; display:flex; flex-direction:column; align-items:center; justify-content:center; gap:12px; color:#6b7280; }
.ph-title { font-size:18px; font-weight:600; color:#9ca3af; }
.ph-desc { font-size:14px; color:#6b7280; }
.ssh-statusbar { display:flex; align-items:center; gap:0; padding:0 12px; min-height:26px; background:var(--panel); border-top:1px solid var(--border); font-size:13px; color:var(--muted); flex-shrink:0; }
.sb-item { padding:0 8px; }
.sb-sep { color:var(--border); }
.sb-status { display:flex; align-items:center; gap:4px; }
.sb-dot { width:6px; height:6px; border-radius:50%; }
.sb-status.connected .sb-dot { background:#34d399; }
.sb-status.connecting .sb-dot { background:#fbbf24; }
.sb-status.disconnected .sb-dot { background:#dc2626; }
.vnc-container { position:absolute; inset:0; }

/* 弹窗 */
.modal-mask { position:fixed; inset:0; background:rgba(0,0,0,.45); z-index:200; display:flex; align-items:center; justify-content:center; }
.modal-box { background:var(--panel); border-radius:12px; padding:24px; width:420px; max-width:92vw; max-height:85vh; overflow-y:auto; box-shadow:0 20px 60px rgba(0,0,0,.3); }
.modal-box.modal-danger { border:1px solid #fecaca; }
.modal-box h3 { margin:0 0 12px; font-size:17px; }
.modal-desc { font-size:14px; color:var(--fg-2); line-height:1.6; margin:0; }
.modal-desc b { color:var(--fg); }
.modal-actions { display:flex; justify-content:flex-end; gap:8px; margin-top:20px; }
/* 按钮 - 使用全局 .btn / .text-btn */
.modal-wide { width:800px; max-width:95vw; }

/* 新建表单 */
.form-wide { width:640px; max-width:95vw; }
.form-wide h3 { margin:0 0 16px; font-size:17px; font-weight:600; }
.type-selector { display:flex; gap:10px; margin-bottom:16px; }
.type-card { flex:1; padding:12px; border:2px solid var(--border); border-radius:10px; text-align:center; cursor:pointer; transition:all .15s; }
.type-card:hover { border-color:var(--accent); }
.type-card.active { border-color:var(--accent); background:var(--accent-soft); }
.type-card.disabled { opacity:.5; cursor:not-allowed; }
.type-card-icon { font-size:20px; margin-bottom:4px; width:36px; height:36px; border-radius:8px; display:inline-flex; align-items:center; justify-content:center; }
.type-card-label { font-size:13px; font-weight:600; margin-bottom:2px; }
.type-card-desc { font-size:11px; color:var(--muted); }
.type-card-status { font-size:11px; margin-top:4px; }
.type-card-status.available { color:#16a34a; }
.type-card-status.reserved { color:#7c3aed; }
.form-type-display { margin-bottom:16px; }
.type-tag { display:inline-flex; align-items:center; padding:4px 12px; border-radius:6px; font-size:13px; font-weight:600; }
.type-tag.ssh { background:#dbeafe; color:#2563eb; }
.type-tag.vnc { background:#fed7aa; color:#ea580c; }
.type-tag.rdp { background:#ede9fe; color:#7c3aed; }
.form-cards { display:grid; grid-template-columns:1fr 1fr; gap:12px; margin-bottom:12px; }
.form-card { background:var(--panel-2); border:1px solid var(--border); border-radius:10px; padding:14px 16px; }
.form-card-full { grid-column:1/-1; }
.form-card-title { display:flex; align-items:center; gap:6px; font-size:13px; font-weight:600; color:var(--muted); text-transform:uppercase; letter-spacing:.04em; margin-bottom:12px; }
.form-card-dot { width:3px; height:12px; border-radius:2px; flex-shrink:0; }
.form-card-dot.dot-ssh { background:#2563eb; }
.form-card-dot.dot-vnc { background:#ea580c; }
.form-card-dot.dot-rdp { background:#7c3aed; }
.form-card-body { display:flex; flex-direction:column; gap:10px; }
.form-card-row { flex-direction:row; align-items:flex-end; gap:16px; }
.form-field { display:flex; flex-direction:column; gap:4px; flex:1; min-width:0; }
.form-field label { font-size:13px; color:var(--muted); font-weight:500; }
.form-field input, .form-field select { width:100%; padding:7px 10px; border:1px solid var(--border); border-radius:6px; background:var(--panel); color:var(--fg); font-size:13px; transition:border-color .15s, box-shadow .15s; }
.form-field input:focus, .form-field select:focus { outline:none; border-color:var(--accent); box-shadow:0 0 0 2px var(--accent-soft); }
.form-field input::placeholder { color:var(--muted); opacity:.6; }
.form-field-agent { min-width:180px; max-width:240px; }
.required { color:#ef4444; }
.auth-switch { display:flex; gap:4px; }
.auth-btn { flex:1; height:34px; border:1px solid var(--border); border-radius:6px; font-size:13px; color:var(--fg-2); background:var(--panel); cursor:pointer; }
.auth-btn.active { border-color:var(--accent); color:var(--accent); background:var(--accent-soft); font-weight:600; }
.form-remark { margin-bottom:12px; }
.form-remark label { display:block; font-size:13px; color:var(--muted); font-weight:500; margin-bottom:4px; }
.form-remark input { width:100%; padding:7px 10px; border:1px solid var(--border); border-radius:6px; background:var(--panel-2); color:var(--fg); font-size:13px; }
.form-actions { display:flex; align-items:center; justify-content:space-between; margin-top:16px; padding-top:14px; border-top:1px solid var(--border); }
.form-actions-right { display:flex; gap:8px; }
.test-btn { color:var(--accent); border-color:var(--accent-soft); }
.test-btn:hover { background:var(--accent-soft); }

/* 拖拽手柄 */
.move-btns { display:inline-flex; gap:2px; flex-shrink:0; }
.move-btn { width:20px; height:20px; padding:0; border:1px solid var(--border); border-radius:4px; background:var(--bg); color:var(--muted); font-size:12px; line-height:1; cursor:pointer; display:inline-flex; align-items:center; justify-content:center; }
.move-btn:hover:not(:disabled) { color:var(--accent); border-color:var(--accent); background:var(--accent-soft); }
.move-btn:disabled { opacity:.3; cursor:default; }

/* 密码小眼睛 */
.pwd-wrap { position:relative; display:flex; align-items:center; }
.pwd-wrap input { flex:1; padding-right:32px; }
.pwd-toggle { position:absolute; right:4px; top:50%; transform:translateY(-50%); background:none; border:none; cursor:pointer; font-size:14px; padding:4px 6px; color:var(--muted); line-height:1; }
.pwd-toggle:hover { color:var(--accent); }
</style>
