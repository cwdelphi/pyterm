<script setup lang="ts">
import { ref, onMounted, onUnmounted, inject, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api, type User, type AuditLog } from '../api'
import { probeConnType } from '../utils/webrtc'
import GatewayManager from './GatewayManager.vue'
import DiagnosisPanel from './DiagnosisPanel.vue'
import EmptyState from './EmptyState.vue'
import AgentConfigModal from './AgentConfigModal.vue'

const { t } = useI18n()
const toast = inject<any>('toast')

/* ── 左侧菜单 ── */
const menuItems = computed(() => [
  { key: 'agents', label: t('admin.agents'), icon: '🤖' },
  { key: 'gateways', label: t('admin.gateways'), icon: '🌉' },
  { key: 'coturn', label: t('admin.coturn'), icon: '🌐' },
  { key: 'users', label: t('admin.users'), icon: '👥' },
  { key: 'roles', label: t('admin.roles'), icon: '🔐' },
  { key: 'audit', label: t('admin.audit'), icon: '📋' },
  { key: 'settings', label: t('admin.settings'), icon: '⚙️' },
  { key: 'diagnostics', label: t('admin.diagnostics'), icon: '🔍' },
  { key: 'downloads', label: t('admin.downloads'), icon: '📥' }
])
const activeTab = ref<'users' | 'agents' | 'gateways' | 'coturn' | 'roles' | 'audit' | 'settings' | 'diagnostics' | 'downloads'>('users')

/* ── 分页通用 ── */
const pageSize = 10
function usePagination<T>(data: Ref<T[]>) {
  const page = ref(1)
  const paginated = computed(() => {
    const start = (page.value - 1) * pageSize
    return data.value.slice(start, start + pageSize)
  })
  const totalPages = computed(() => Math.max(1, Math.ceil(data.value.length / pageSize)))
  function resetPage() { page.value = 1 }
  return { page, paginated, totalPages, resetPage }
}

/* ── 用户管理 ── */
const users = ref<User[]>([])
const userSearch = ref('')
const showUserForm = ref(false)
const isEditUser = ref(false)
const editingUserId = ref('')
const userForm = ref({ username: '', password: '', email: '', role: 'user', phone: '' })
const showResetPwd = ref(false)
const resetPwdUserId = ref('')
const resetPwdNew = ref('')
const showDeleteUser = ref(false)
const deleteUserId = ref('')
const deleteUserName = ref('')

/* ── Agent配置弹窗 ── */
const configAgentId = ref('')
const configAgentName = ref('')
const configAgentOnline = ref<boolean | undefined>(undefined)
function openAgentConfig(a: any) {
  configAgentId.value = a.id
  configAgentName.value = a.name || ''
  configAgentOnline.value = a.online
}
function closeAgentConfig() {
  configAgentId.value = ''
}
function onAgentInfoUpdated(payload: { name?: string } | undefined) {
  if (payload?.name && configAgentId.value) configAgentName.value = payload.name
  loadAgents()
}
function onAgentToken(token: string) {
  agentTokenValue.value = token
  deployAgentId.value = configAgentId.value
  deployMethod.value = 'docker'
  agentGuideStep.value = 1
  showAgentToken.value = true
}

const filteredUsers = computed(() => {
  if (!userSearch.value) return users.value
  const q = userSearch.value.toLowerCase()
  return users.value.filter(u => u.username.toLowerCase().includes(q) || (u.email || '').toLowerCase().includes(q))
})
const userPagination = usePagination(filteredUsers)

async function loadUsers() {
  try { const data = await api.adminListUsers(); users.value = data.users } catch (e: any) { toast?.error(e.message) }
}

function openNewUser() {
  userForm.value = { username: '', password: '', email: '', role: 'user', phone: '' }
  isEditUser.value = false; editingUserId.value = ''; showUserForm.value = true
}

function openEditUser(u: User) {
  userForm.value = { username: u.username, password: '', email: u.email || '', role: u.role, phone: u.phone || '' }
  isEditUser.value = true; editingUserId.value = u.id; showUserForm.value = true
}

async function saveUser() {
  try {
    if (isEditUser.value) {
      const data: any = { username: userForm.value.username, email: userForm.value.email, role: userForm.value.role, phone: userForm.value.phone }
      await api.adminUpdateUser(editingUserId.value, data)
    } else {
      await api.adminCreateUser(userForm.value)
    }
    showUserForm.value = false; await loadUsers(); toast?.success(isEditUser.value ? t('admin.updated') : t('admin.created'))
  } catch (e: any) { toast?.error(e.message) }
}

function openResetPwd(userId: string) { resetPwdUserId.value = userId; resetPwdNew.value = ''; showResetPwd.value = true }

async function doResetPwd() {
  try { await api.adminResetPassword(resetPwdUserId.value, resetPwdNew.value); showResetPwd.value = false; toast?.success(t('admin.passwordReset')) } catch (e: any) { toast?.error(e.message) }
}

async function toggleUserStatus(userId: string) {
  try { await api.adminToggleUserStatus(userId); await loadUsers(); toast?.success(t('admin.statusToggled')) } catch (e: any) { toast?.error(e.message) }
}

function openDeleteUser(u: User) { deleteUserId.value = u.id; deleteUserName.value = u.username; showDeleteUser.value = true }

async function doDeleteUser() {
  try { await api.adminDeleteUser(deleteUserId.value); showDeleteUser.value = false; await loadUsers(); toast?.success(t('admin.deleted')) } catch (e: any) { toast?.error(e.message) }
}

const selectedUserIds = ref<Set<string>>(new Set())

function toggleUserSelect(userId: string) {
  const u = users.value.find(x => x.id === userId)
  if (u?.role === 'admin') return
  const s = new Set(selectedUserIds.value)
  if (s.has(userId)) s.delete(userId); else s.add(userId)
  selectedUserIds.value = s
}
function toggleAllUsers() {
  const selectable = filteredUsers.value.filter(u => u.role !== 'admin')
  if (selectable.length && selectable.every(u => selectedUserIds.value.has(u.id))) {
    selectedUserIds.value = new Set()
  } else {
    selectedUserIds.value = new Set(selectable.map(u => u.id))
  }
}
async function batchDeleteUsers() {
  if (!confirm(t('common.confirmDelete') + ` ${selectedUserIds.value.size} ${t('admin.userCount')}？`)) return
  try {
    const result = await api.adminBatchDeleteUsers(Array.from(selectedUserIds.value))
    selectedUserIds.value = new Set()
    await loadUsers()
    toast?.success(t('admin.deleted') + ` ${result.deleted.length} ${t('admin.userCount')}` + (result.skipped.length ? `, ${t('common.next')} ${result.skipped.length}` : ''))
  } catch (e: any) { toast?.error(e.message) }
}

/* ── 角色权限 ── */
const roles = ref<any[]>([])
const allPermissions = ref<Record<string, string>>({})

async function loadRoles() {
  try {
    const data = await api.adminGetRoles()
    roles.value = data.roles
    allPermissions.value = data.all_permissions
  } catch (e: any) { toast?.error(e.message) }
}

/* ── Agent管理 ── */
const agents = ref<any[]>([])
const agentSearch = ref('')
const showAgentForm = ref(false)
const agentForm = ref({ id: '', name: '', coturn_id: '', remark: '' })
const showAgentToken = ref(false)
const agentTokenValue = ref('')
const agentGuideStep = ref(1)
const showDeployScript = ref(false)
const deployAgentId = ref('')
const deployMethod = ref('docker')
const deployCopied = ref(false)
const deploySignedPath = ref('')          // 模式二签名 URL（相对路径）
const deployLoading = ref(false)          // 正在请求签名 URL
const pendingAgents = ref<any[]>([])      // 待注册 Agent

function getDeployCommandFull(): string {
  const cmd = deploySignedPath.value
    ? `curl -fsSL "${window.location.origin}${deploySignedPath.value}" | bash`
    : ''
  return cmd
}

async function refreshDeployUrl() {
  if (!deployAgentId.value) return
  deployLoading.value = true
  deploySignedPath.value = ''
  deployCopied.value = false
  try {
    const r = await api.deployAgentSignedUrl(deployMethod.value, deployAgentId.value)
    deploySignedPath.value = r.url
  } catch (e: any) {
    toast?.error(e.message || '生成部署链接失败')
  }
  deployLoading.value = false
}

function copyDeployScript() {
  const full = getDeployCommandFull()
  if (!full) { toast?.error('部署链接未就绪，请稍候或重新生成'); return }
  navigator.clipboard.writeText(full)
  deployCopied.value = true
  toast?.success(t('admin.copied'))
  setTimeout(() => { deployCopied.value = false }, 2000)
}

async function loadPendingAgents() {
  try {
    const data = await api.adminPendingAgents()
    pendingAgents.value = data.pending || []
  } catch {}
}

async function approvePending(sid: string) {
  try {
    const r = await api.approvePendingAgent(sid)
    toast?.success(t('admin.agentBound') + ' ' + r.agent_id)
    await loadPendingAgents()
    await loadAgents()
  } catch (e: any) {
    toast?.error(e.message)
  }
}
const showDeleteAgent = ref(false)
const deleteAgentId = ref('')
const deleteAgentName = ref('')

const filteredAgents = computed(() => {
  if (!agentSearch.value) return agents.value
  const q = agentSearch.value.toLowerCase()
  return agents.value.filter(a => a.id.toLowerCase().includes(q) || a.name.toLowerCase().includes(q))
})
const agentPagination = usePagination(filteredAgents)

async function loadAgents() {
  try { const data = await api.adminListAgents(); agents.value = data.agents } catch (e: any) { toast?.error(e.message) }
}

/* ── Agent模式探测（单行） ── */
const detectingAgents = ref<Set<string>>(new Set())
const detectBusy = ref(false)

async function detectOneAgent(a: any) {
  const token = localStorage.getItem('token')
  if (!token) { toast?.error(t('admin.notLoggedIn')); return }
  if (!a.online || detectingAgents.value.has(a.id)) return
  detectingAgents.value.add(a.id)
  detectBusy.value = true
  try {
    const ct = await probeConnType(a.id, token, 8000)
    if (ct && ct !== 'BUG') {
      toast?.success(`${a.name}: ${ct}`)
      const ag = agents.value.find(x => x.id === a.id)
      if (ag) ag.conn_type = ct
    } else {
      toast?.error(`${a.name}: ${t('admin.detectFailed')}`)
    }
  } catch (e) {
    console.error(`[detect] ${a.id} 探测异常:`, e)
    toast?.error(`${a.name}: ${t('admin.detectFailed')}`)
  } finally {
    detectingAgents.value.delete(a.id)
    detectBusy.value = detectingAgents.value.size > 0
  }
}

async function fetchNextAgentId() {
  try { const data = await api.adminNextAgentId(); agentForm.value.id = data.id } catch (e: any) { toast?.error(e.message) }
}

function openNewAgent() {
  agentForm.value = { id: '', name: '', coturn_id: '', remark: '' }
  showAgentForm.value = true
  fetchNextAgentId()
}

async function saveAgent() {
  try {
    const result = await api.adminAddAgent({ id: agentForm.value.id, name: agentForm.value.name, coturn_id: agentForm.value.coturn_id, remark: agentForm.value.remark })
    agentTokenValue.value = result.token
    deployAgentId.value = agentForm.value.id
    deployMethod.value = 'docker'
    agentGuideStep.value = 1
    showAgentToken.value = true
    showAgentForm.value = false; await loadAgents(); toast?.success(t('admin.created'))
  } catch (e: any) { toast?.error(e.message) }
}

async function toggleAgentStatus(agentId: string) {
  try { await api.adminToggleAgentStatus(agentId); await loadAgents(); toast?.success(t('admin.statusToggled')) } catch (e: any) { toast?.error(e.message) }
}

function openDeployScript(agentId: string) { deployAgentId.value = agentId; deployMethod.value = 'docker'; showDeployScript.value = true; refreshDeployUrl() }

function openDeleteAgent(a: any) { deleteAgentId.value = a.id; deleteAgentName.value = a.name; showDeleteAgent.value = true }

/* ── Agent Token 显示/复制 ── */

async function copyAgentToken(token: string) {
  await navigator.clipboard.writeText(token)
  toast?.success(t('admin.copiedToClipboard'))
}

async function doDeleteAgent() {
  try { await api.adminDeleteAgent(deleteAgentId.value); showDeleteAgent.value = false; await loadAgents(); toast?.success(t('admin.deleted')) } catch (e: any) { toast?.error(e.message) }
}

const upgradingAgents = ref<Set<string>>(new Set())
async function upgradeAgent(agentId: string) {
  if (!confirm(t('admin.upgradeConfirm'))) return
  if (upgradingAgents.value.has(agentId)) return
  upgradingAgents.value.add(agentId)
  try {
    const resp = await fetch(`/api/admin/agents/${agentId}/upgrade`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${localStorage.getItem('token')}` },
      body: JSON.stringify({})
    })
    const r = await resp.json().catch(() => ({}))
    if (resp.ok && r.ok !== false) toast?.success(t('common.upgradeSuccess'))
    else toast?.error((r.detail || r.message) || t('common.upgradeFailed'))
  } catch (e: any) { toast?.error(e.message || t('common.upgradeFailed')) }
  finally { upgradingAgents.value.delete(agentId) }
}

/* ── coturn管理 ── */
const coturnServers = ref<any[]>([])
const showCoturnForm = ref(false)
const isEditCoturn = ref(false)
const editingCoturnId = ref('')
const coturnForm = ref({ name: '', host: '', port: 3478, tls_port: 5349, secret: '', realm: 'pyterm.local', relay_range: '49160-49259', total_quota: 100, remark: '' })
const showDeleteCoturn = ref(false)
const deleteCoturnId = ref('')
const deleteCoturnName = ref('')

const filteredCoturn = computed(() => {
  if (!agentSearch.value || activeTab.value !== 'coturn') return coturnServers.value
  const q = agentSearch.value.toLowerCase()
  return coturnServers.value.filter(c => c.name.toLowerCase().includes(q) || c.host.toLowerCase().includes(q))
})
const coturnPagination = usePagination(filteredCoturn)

/* ── 网关数据 ── */
const gateways = ref<any[]>([])
async function loadGateways() {
  try { const data = await api.adminListGateways(); gateways.value = data.gateways } catch {}
}

async function loadCoturn() {
  try { const data = await api.adminListCoturn(); coturnServers.value = data.servers } catch (e: any) { toast?.error(e.message) }
}

function openNewCoturn() {
  coturnForm.value = { name: '', host: '', port: 3478, tls_port: 5349, secret: '', realm: 'pyterm.local', relay_range: '49160-49259', total_quota: 100, remark: '' }
  isEditCoturn.value = false; editingCoturnId.value = ''; showCoturnForm.value = true
}

function openEditCoturn(c: any) {
  coturnForm.value = { name: c.name, host: c.host, port: c.port, tls_port: c.tls_port, secret: c.secret, realm: c.realm, relay_range: c.relay_range, total_quota: c.total_quota, remark: c.remark || '' }
  isEditCoturn.value = true; editingCoturnId.value = c.id; showCoturnForm.value = true
}

async function saveCoturn() {
  try {
    if (isEditCoturn.value) { await api.adminUpdateCoturn(editingCoturnId.value, coturnForm.value) }
    else { await api.adminAddCoturn(coturnForm.value) }
    showCoturnForm.value = false; await loadCoturn(); toast?.success(isEditCoturn.value ? t('admin.updated') : t('admin.created'))
  } catch (e: any) { toast?.error(e.message) }
}

function openDeleteCoturn(c: any) { deleteCoturnId.value = c.id; deleteCoturnName.value = c.name; showDeleteCoturn.value = true }

async function doDeleteCoturn() {
  try { await api.adminDeleteCoturn(deleteCoturnId.value); showDeleteCoturn.value = false; await loadCoturn(); toast?.success(t('admin.deleted')) } catch (e: any) { toast?.error(e.message) }
}

/* ── STUN/TURN 测试 ── */
const showCoturnTest = ref(false)
const coturnTestResult = ref<any>(null)
const coturnTestBusy = ref(false)
const coturnTestCreds = ref<any>(null)

/* ── 共享管理 ── */
const showShareModal = ref(false)
const shareTarget = ref<any>(null)
const shareType = ref<'agent' | 'coturn' | 'gateway'>('agent')
const shareMode = ref('private')
const shareUsers = ref<any[]>([])
const allUsers = ref<any[]>([])
const shareSelectedUsers = ref<string[]>([])

async function openShareModal(item: any, type: 'agent' | 'coturn' | 'gateway') {
  shareTarget.value = item
  shareType.value = type
  shareMode.value = item.shared_with === 'all' ? 'all' : item.shared_with === 'private' ? 'private' : 'select'
  shareSelectedUsers.value = []
  // 获取用户列表
  try {
    const data = await api.adminListUsers()
    allUsers.value = data.users.filter((u: any) => u.id !== item.owner_id)
  } catch {}
  // 如果是选择用户模式，解析已有共享
  if (shareMode.value === 'select' && item.shared_with && item.shared_with !== 'private' && item.shared_with !== 'all') {
    try {
      const parsed = JSON.parse(item.shared_with)
      shareSelectedUsers.value = parsed
    } catch {}
  }
  showShareModal.value = true
}

async function saveShare() {
  if (!shareTarget.value) return
  let sharedWith: string | string[] = shareMode.value === 'select' ? shareSelectedUsers.value : shareMode.value
  try {
    if (shareType.value === 'agent') {
      await api.adminShareAgent(shareTarget.value.id, sharedWith)
    } else if (shareType.value === 'gateway') {
      await api.adminShareGateway(shareTarget.value.id, sharedWith)
    } else {
      await api.adminShareCoturn(shareTarget.value.id, sharedWith)
    }
    showShareModal.value = false
    toast?.success(t('admin.shareSaved'))
    if (shareType.value === 'agent') await loadAgents()
    else if (shareType.value === 'gateway') await loadGateways()
    else await loadCoturn()
  } catch (e: any) { toast?.error(e.message) }
}

function iceErrDetail(e: any): string {
  const code = e?.errorCode ? ` [${e.errorCode}]` : ''
  const text = e?.errorText || e?.errorDetail || t('common.testIceError')
  return `${text}${code}`
}

async function testCoturn(c: any) {
  showCoturnTest.value = true
  coturnTestBusy.value = true
  coturnTestResult.value = null
  coturnTestCreds.value = null
  const results: any[] = []

  console.group('[coturn-test] ====== 开始测试 ======')
  console.log('[coturn-test] 服务器配置:', JSON.stringify({ host: c.host, port: c.port, tls_port: c.tls_port, name: c.name }))

  try {
    // 获取HMAC凭证
    const creds = await api.adminGetTestCredentials(c.id)
    coturnTestCreds.value = creds
    console.log('[coturn-test] HMAC凭证获取成功, username:', creds?.username)

    // 测试 STUN（用coturn自己的地址）
    try {
      const stunUrl = `stun:${c.host}:${c.port}`
      console.group(`[coturn-test] STUN测试: ${stunUrl}`)
      const pc = new RTCPeerConnection({ iceServers: [{ urls: stunUrl }] })
      const start = Date.now()
      await new Promise<void>((resolve) => {
        pc.onicegatheringstatechange = () => {
          console.log(`[coturn-test] STUN iceGatheringState: ${pc.iceGatheringState}`)
        }
        pc.oniceconnectionstatechange = () => {
          console.log(`[coturn-test] STUN iceConnectionState: ${pc.iceConnectionState}`)
        }
        pc.onicecandidate = (e) => {
          if (e.candidate) {
            console.log(`[coturn-test] STUN candidate: type=${e.candidate.type} proto=${e.candidate.protocol} addr=${e.candidate.address}:${e.candidate.port} cand=${e.candidate.candidate}`)
          } else {
            console.log('[coturn-test] STUN candidate gathering complete')
          }
          if (e.candidate && e.candidate.type === 'srflx') {
            clearTimeout(timer)
            results.push({ server: `STUN (${c.name}) ${c.port}/UDP`, status: t('common.testSuccess'), time: `${Date.now() - start}ms`, detail: `srflx: ${e.candidate.address}:${e.candidate.port}` })
            pc.close(); resolve()
          }
        }
        const timer = setTimeout(() => {
          console.warn('[coturn-test] STUN 超时5s, iceGatheringState:', pc.iceGatheringState, 'iceConnectionState:', pc.iceConnectionState)
          results.push({ server: `STUN (${c.name}) ${c.port}/UDP`, status: t('common.testTimeout'), time: '5000ms', detail: t('common.testTimeoutDetail') })
          pc.close(); resolve()
        }, 5000)
        pc.onicecandidateerror = (e: any) => {
          clearTimeout(timer)
          console.error('[coturn-test] STUN error:', JSON.stringify({
            errorText: e.errorText, errorCode: e.errorCode, errorDetail: e.errorDetail,
            url: e.url, address: e.address, port: e.port, peer: e.peer
          }))
          results.push({ server: `STUN (${c.name}) ${c.port}/UDP`, status: t('common.testFailed'), time: `${Date.now() - start}ms`, detail: iceErrDetail(e) })
          pc.close(); resolve()
        }
        pc.createDataChannel('test')
        pc.createOffer().then(o => {
          console.log('[coturn-test] STUN offer SDP:', o.sdp?.substring(0, 200))
          pc.setLocalDescription(o)
        })
      })
      console.groupEnd()
    } catch (e: any) {
      console.error('[coturn-test] STUN异常:', e)
      results.push({ server: `STUN (${c.name})`, status: t('common.testFailed'), time: '-', detail: e.message })
    }

    // 测试 TURN UDP
    if (creds) {
      try {
        const turnUrl = `turn:${c.host}:${c.port}?transport=udp`
        console.group(`[coturn-test] TURN UDP测试: ${turnUrl}`)
        console.log('[coturn-test] TURN creds:', { username: creds.username, credential_len: creds.credential?.length })
        const pc = new RTCPeerConnection({ iceServers: [
          { urls: turnUrl, username: creds.username, credential: creds.credential },
        ] })
        const start = Date.now()
        await new Promise<void>((resolve) => {
          pc.onicegatheringstatechange = () => {
            console.log(`[coturn-test] TURN-UDP iceGatheringState: ${pc.iceGatheringState}`)
          }
          pc.oniceconnectionstatechange = () => {
            console.log(`[coturn-test] TURN-UDP iceConnectionState: ${pc.iceConnectionState}`)
          }
          pc.onicecandidate = (e) => {
            if (e.candidate) {
              console.log(`[coturn-test] TURN-UDP candidate: type=${e.candidate.type} proto=${e.candidate.protocol} addr=${e.candidate.address}:${e.candidate.port} cand=${e.candidate.candidate}`)
            } else {
              console.log('[coturn-test] TURN-UDP candidate gathering complete')
            }
            if (e.candidate && e.candidate.type === 'relay') {
              clearTimeout(timer)
              results.push({ server: `TURN (${c.name}) ${c.port}/UDP`, status: t('common.testSuccess'), time: `${Date.now() - start}ms`, detail: `relay: ${e.candidate.address}:${e.candidate.port}` })
              pc.close(); resolve()
            }
          }
          const timer = setTimeout(() => {
            console.warn('[coturn-test] TURN-UDP 超时5s, iceGatheringState:', pc.iceGatheringState, 'iceConnectionState:', pc.iceConnectionState)
            results.push({ server: `TURN (${c.name}) ${c.port}/UDP`, status: t('common.testTimeout'), time: '5000ms', detail: t('common.testTimeoutDetail') })
            pc.close(); resolve()
          }, 5000)
          pc.onicecandidateerror = (e: any) => {
            clearTimeout(timer)
            console.error('[coturn-test] TURN-UDP error:', JSON.stringify({
              errorText: e.errorText, errorCode: e.errorCode, errorDetail: e.errorDetail,
              url: e.url, address: e.address, port: e.port, peer: e.peer
            }))
            results.push({ server: `TURN (${c.name}) ${c.port}/UDP`, status: t('common.testFailed'), time: `${Date.now() - start}ms`, detail: iceErrDetail(e) })
            pc.close(); resolve()
          }
          pc.createDataChannel('test')
          pc.createOffer().then(o => {
            console.log('[coturn-test] TURN-UDP offer SDP:', o.sdp?.substring(0, 200))
            pc.setLocalDescription(o)
          })
        })
        console.groupEnd()
      } catch (e: any) {
        console.error('[coturn-test] TURN-UDP异常:', e)
        results.push({ server: `TURN (${c.name}) UDP`, status: t('common.testFailed'), time: '-', detail: e.message })
      }

       // 测试 TURN TCP (无需TLS证书)
       try {
         const turnsUrl = `turn:${c.host}:${c.port}?transport=tcp`
         console.group(`[coturn-test] TURN TCP测试: ${turnsUrl}`)
        const pc = new RTCPeerConnection({ iceServers: [
          { urls: turnsUrl, username: creds.username, credential: creds.credential },
        ] })
        const start = Date.now()
        await new Promise<void>((resolve) => {
          pc.onicegatheringstatechange = () => {
            console.log(`[coturn-test] TURN-TCP iceGatheringState: ${pc.iceGatheringState}`)
          }
          pc.oniceconnectionstatechange = () => {
            console.log(`[coturn-test] TURN-TCP iceConnectionState: ${pc.iceConnectionState}`)
          }
          pc.onicecandidate = (e) => {
            if (e.candidate) {
              console.log(`[coturn-test] TURN-TCP candidate: type=${e.candidate.type} proto=${e.candidate.protocol} addr=${e.candidate.address}:${e.candidate.port} cand=${e.candidate.candidate}`)
            } else {
              console.log('[coturn-test] TURN-TCP candidate gathering complete')
            }
            if (e.candidate && e.candidate.type === 'relay') {
              clearTimeout(timer)
              results.push({ server: `TURN (${c.name}) ${c.port}/TCP`, status: t('common.testSuccess'), time: `${Date.now() - start}ms`, detail: `relay: ${e.candidate.address}:${e.candidate.port}` })
              pc.close(); resolve()
            }
          }
          const timer = setTimeout(() => {
            console.warn('[coturn-test] TURN-TCP 超时5s, iceGatheringState:', pc.iceGatheringState, 'iceConnectionState:', pc.iceConnectionState)
            results.push({ server: `TURN (${c.name}) ${c.port}/TCP`, status: t('common.testTimeout'), time: '5000ms', detail: t('common.testTimeoutDetail') })
            pc.close(); resolve()
          }, 5000)
          pc.onicecandidateerror = (e: any) => {
            clearTimeout(timer)
            console.error('[coturn-test] TURN-TCP error:', JSON.stringify({
              errorText: e.errorText, errorCode: e.errorCode, errorDetail: e.errorDetail,
              url: e.url, address: e.address, port: e.port, peer: e.peer
            }))
            results.push({ server: `TURN (${c.name}) ${c.port}/TCP`, status: t('common.testFailed'), time: `${Date.now() - start}ms`, detail: iceErrDetail(e) })
            pc.close(); resolve()
          }
          pc.createDataChannel('test')
          pc.createOffer().then(o => {
            console.log('[coturn-test] TURNS offer SDP:', o.sdp?.substring(0, 200))
            pc.setLocalDescription(o)
          })
        })
        console.groupEnd()
      } catch (e: any) {
        console.error('[coturn-test] TURNS异常:', e)
        results.push({ server: `TURNS (${c.name}) TLS`, status: t('common.testFailed'), time: '-', detail: e.message })
      }
    }

    console.log('[coturn-test] ====== 测试结果 ======', JSON.stringify(results, null, 2))
    console.groupEnd()
    coturnTestResult.value = { server: c?.name || '-', host: c?.host || '-', results }
  } catch (e: any) {
    console.error('[coturn-test] 凭证获取失败:', e)
    coturnTestResult.value = { server: c?.name || '-', host: c?.host || '-', results: [{ server: t('common.testGetCred'), status: t('common.testFailed'), time: '-', detail: e.message }] }
  }
  coturnTestBusy.value = false
}

/* ── 审计日志 ── */
const auditLogs = ref<AuditLog[]>([])
const auditSearch = ref('')

const filteredLogs = computed(() => {
  if (!auditSearch.value) return auditLogs.value
  const q = auditSearch.value.toLowerCase()
  return auditLogs.value.filter(l => l.username.toLowerCase().includes(q) || l.action.toLowerCase().includes(q) || (l.detail || '').toLowerCase().includes(q))
})
const auditPagination = usePagination(filteredLogs)

async function loadAudit() {
  try { const data = await api.adminAuditLog(); auditLogs.value = data.logs } catch (e: any) { toast?.error(e.message) }
}

/* ── 密码管理 ── */
const pwdForm = ref({ old_password: '', new_password: '', confirm_password: '' })
async function changeMyPassword() {
  if (!pwdForm.value.old_password || !pwdForm.value.new_password) { toast?.error(t('admin.pwdFillRequired')); return }
  if (pwdForm.value.new_password !== pwdForm.value.confirm_password) { toast?.error(t('admin.pwdMismatch')); return }
  if (pwdForm.value.new_password?.length < 6) { toast?.error(t('admin.pwdMinLength')); return }
  try {
    await fetch('/api/auth/password', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${localStorage.getItem('token')}` },
      body: JSON.stringify({ old_password: pwdForm.value.old_password, new_password: pwdForm.value.new_password })
    }).then(r => r.json())
    pwdForm.value = { old_password: '', new_password: '', confirm_password: '' }
    toast?.success(t('admin.pwdUpdated'))
  } catch (e: any) { toast?.error(e.message) }
}

import { getLogLevel, setLogLevel, LogLevel } from '../utils/logger'
const consoleLogLevel = ref(getLogLevel())
function onLogLevelChange() {
  setLogLevel(consoleLogLevel.value)
}

/* ── 初始化 ── */
let refreshTimer: ReturnType<typeof setInterval> | null = null

onMounted(async () => {
  await Promise.all([loadUsers(), loadAgents(), loadGateways(), loadCoturn(), loadAudit(), loadRoles(), loadPendingAgents()])
  refreshTimer = setInterval(() => {
    if (activeTab.value === 'agents' && !detectBusy.value) {
      loadAgents()
      loadPendingAgents()
    }
  }, 5000)
})

onUnmounted(() => {
  if (refreshTimer) clearInterval(refreshTimer)
})

function switchTab(key: string) {
  activeTab.value = key as any
  agentSearch.value = ''
}

watch(deployMethod, () => { if (deployAgentId.value) refreshDeployUrl() })
watch(agentGuideStep, (v) => { if (v === 3) refreshDeployUrl() })

function getRoleLabel(role: string): string {
  const map: Record<string, string> = { admin: t('admin.adminRole'), user: t('admin.normalUser') }
  return map[role] || role
}

function getRoleColor(role: string): string {
  const map: Record<string, string> = { admin: '#2563eb', user: '#6b7280' }
  return map[role] || '#6b7280'
}

function fmtAuditTime(ts: string): string {
  if (!ts) return '-'
  try {
    const d = new Date(ts)
    const mm = String(d.getMonth() + 1).padStart(2, '0')
    const dd = String(d.getDate()).padStart(2, '0')
    const hh = String(d.getHours()).padStart(2, '0')
    const mi = String(d.getMinutes()).padStart(2, '0')
    return mm + '-' + dd + ' ' + hh + ':' + mi
  } catch { return ts?.slice(0, 16) || '-' }
}
</script>

<template>
  <div class="admin-wrap">
    <!-- 左侧菜单 -->
    <aside class="admin-sidebar">
      <div class="sidebar-head">
        <span class="sidebar-head-icon">⚙️</span>
        <span>{{ t('admin.title') }}</span>
      </div>
      <div class="sidebar-nav">
        <button
          v-for="item in menuItems" :key="item.key"
          class="menu-item" :class="{ active: activeTab === item.key }"
          @click="switchTab(item.key)"
        >
          <span class="menu-icon">{{ item.icon }}</span>
          {{ item.label }}
        </button>
      </div>
    </aside>

    <!-- 右侧内容 -->
    <main class="admin-main">
      <div class="admin-header">
        <div class="breadcrumb">
          <span style="cursor:pointer;color:var(--accent)" @click="activeTab = 'users'">{{ t('admin.title') }}</span>
          <span class="sep">/</span>
          <span>{{ menuItems.find(i => i.key === activeTab)?.label }}</span>
        </div>
        <h2>{{ menuItems.find(i => i.key === activeTab)?.label }}</h2>
      </div>

      <!-- ═══ 用户管理 ═══ -->
      <div v-if="activeTab === 'users'" class="admin-content">
        <div class="table-card">
          <div class="table-header">
            <h3>{{ t('admin.usersMgmt') }}</h3>
            <div style="display:flex;gap:8px;align-items:center">
              <span class="table-count">{{ t('common.total') }} {{ filteredUsers.length }} {{ t('admin.userCount') }}</span>
              <button class="btn danger" :disabled="selectedUserIds.size === 0" @click="batchDeleteUsers">{{ t('admin.batchDelete') }} ({{ selectedUserIds.size }})</button>
            </div>
          </div>
          <table>
            <thead>
              <tr>
                <th><input type="checkbox" :checked="filteredUsers.filter(u => u.role !== 'admin').length > 0 && filteredUsers.filter(u => u.role !== 'admin').every(u => selectedUserIds.has(u.id))" @change="toggleAllUsers()" /></th>
                <th>{{ t('admin.username') }}</th><th>{{ t('admin.email') }}</th><th>{{ t('admin.role') }}</th><th>{{ t('common.status') }}</th><th>{{ t('admin.lastLogin') }}</th><th>{{ t('admin.createdAt') }}</th><th>{{ t('common.action') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="u in userPagination.paginated.value" :key="u.id">
                <td><input type="checkbox" :checked="selectedUserIds.has(u.id)" :disabled="u.role === 'admin'" @change="toggleUserSelect(u.id)" /></td>
                <td><span class="user-cell">{{ u.username }}</span></td>
                <td>{{ u.email || '-' }}</td>
                <td><span class="role-badge" :style="{ background: getRoleColor(u.role) }">{{ getRoleLabel(u.role) }}</span></td>
                <td><span class="status-dot" :class="{ active: u.is_active }"></span> {{ u.is_active ? t('common.enabled') : t('common.disabled') }}</td>
                <td>{{ u.last_login || '-' }}</td>
                <td>{{ u.created_at?.slice(0, 10) || '-' }}</td>
                <td class="action-cell">
                  <div class="action-inner">
                    <button class="text-btn" @click="openEditUser(u)">{{ t('common.edit') }}</button>
                    <button class="text-btn" :class="u.is_active ? 'warn' : 'success'" @click="toggleUserStatus(u.id)">{{ u.is_active ? t('common.disabled') : t('common.enabled') }}</button>
                    <button class="text-btn" @click="openResetPwd(u.id)">{{ t('admin.resetPassword') }}</button>
                    <button v-if="u.role !== 'admin'" class="text-btn danger" @click="openDeleteUser(u)">{{ t('common.delete') }}</button>
                  </div>
                </td>
              </tr>
              <tr v-if="!userPagination.paginated.value.length"><td colspan="8" class="empty-row">{{ t('common.noData') }}</td></tr>
            </tbody>
          </table>
        </div>
        <div class="table-pagination" v-if="userPagination.totalPages.value > 1">
          <button class="page-btn" :disabled="userPagination.page.value <= 1" @click="userPagination.page.value--">&lt;</button>
          <span class="page-info">{{ userPagination.page.value }} / {{ userPagination.totalPages.value }}</span>
          <button class="page-btn" :disabled="userPagination.page.value >= userPagination.totalPages.value" @click="userPagination.page.value++">&gt;</button>
        </div>
      </div>

      <!-- ═══ Agent管理 ═══ -->
      <div v-if="activeTab === 'agents'" class="admin-content">
        <div class="admin-toolbar">
          <button class="btn primary" @click="openNewAgent">{{ t('admin.addAgent') }}</button>
        </div>

        <!-- 待注册 Agent（模式一：公开安装后在此审批绑定到当前账户） -->
        <div class="table-card" v-if="pendingAgents.length">
          <div class="table-header">
            <h3>{{ t('admin.pendingAgents') }} <span class="pending-badge">{{ pendingAgents.length }}</span></h3>
            <span class="table-count">{{ t('admin.pendingHint') }}</span>
          </div>
          <table>
            <thead>
              <tr><th>Agent ID</th><th>{{ t('common.name') }}</th><th>{{ t('admin.auditTime') }}</th><th>{{ t('common.action') }}</th></tr>
            </thead>
            <tbody>
              <tr v-for="p in pendingAgents" :key="p.sid">
                <td class="mono">{{ p.agent_id }}</td>
                <td>{{ p.agent_name }}</td>
                <td class="nowrap">{{ new Date(p.created_at * 1000).toLocaleString() }}</td>
                <td class="action-cell">
                  <button class="text-btn success" @click="approvePending(p.sid)" style="color:#16a34a">{{ t('admin.approve') }}</button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>

        <div class="table-card">
          <div class="table-header">
            <h3>{{ t('admin.agents') }}</h3>
            <span class="table-count">{{ t('common.total') }} {{ agents.length }} Agent</span>
          </div>
          <table>
            <thead>
              <tr><th>ID</th><th>{{ t('common.name') }}</th><th>{{ t('common.ip') }}</th><th>{{ t('admin.versionTag') }}</th><th>{{ t('common.token') }}</th><th>{{ t('common.status') }}</th><th>{{ t('common.mode') }}</th><th>{{ t('common.share') }}</th><th>{{ t('common.action') }}</th></tr>
            </thead>
            <tbody>
              <tr v-for="a in agentPagination.paginated.value" :key="a.id">
                <td class="mono">{{ a.id }}</td>
                <td>{{ a.name }}<span v-if="!a.is_owner && a.owner_name" class="owner-tag">{{ t('admin.fromOwner') }} {{ a.owner_name }}</span></td>
                <td class="mono">{{ a.ip || '-' }}</td>
                <td>
                  <span v-if="a.version" class="ver-badge" :class="{ old: a.needs_upgrade, muted: a.version === 'dev' }">{{ a.version === 'dev' ? 'dev' : 'v' + a.version }}</span>
                  <span v-else class="ver-badge muted">-</span>
                  <span v-if="a.needs_upgrade" class="upgrade-tip" :title="'最新版本 v' + (a.latest_version || '')">⚠️ {{ t('admin.canUpgrade') }}</span>
                </td>
                <td class="token-cell">
                  <button class="token-btn" :title="t('common.copy')" @click="copyAgentToken(a.token)">📋</button>
                </td>
                <td>
                  <span class="status-dot" :class="{ active: a.online }"></span>
                  {{ a.online ? t('common.online') : t('common.offline') }}
                </td>
                <td>
                  <span v-if="!a.online" class="conn-tag offline">{{ t('common.offline') }}</span>
                  <span v-else-if="detectingAgents.has(a.id)" class="conn-tag detecting">{{ t('common.detecting') }}</span>
                  <span v-else-if="a.conn_type === 'P2P'" class="conn-tag p2p">P2P</span>
                  <span v-else-if="a.conn_type === 'relay'" class="conn-tag relay">{{ t('common.relay') }}</span>
                  <span v-else class="conn-tag pending">{{ t('common.pendingDetect') }}</span>
                </td>
                <td>
                  <button class="share-tag" :class="a.shared_with === 'all' ? 'share-all' : a.shared_with === 'private' ? 'share-private' : 'share-select'" @click="openShareModal(a, 'agent')">
                    {{ a.shared_with === 'all' ? '🌐' + t('common.allUsers') : a.shared_with === 'private' ? '🔒' + t('common.private') : '👥' + t('common.specified') }}
                  </button>
                </td>
                <td class="action-cell">
                  <div class="action-inner">
                    <button v-if="a.needs_upgrade" class="text-btn success" @click="upgradeAgent(a.id)" style="color:#16a34a" :disabled="upgradingAgents.has(a.id)">{{ upgradingAgents.has(a.id) ? t('common.upgrading') : (t('admin.upgradeVersion') + a.latest_version) }}</button>
                    <button class="text-btn info" :disabled="!a.online || detectingAgents.has(a.id)" @click="detectOneAgent(a)">{{ detectingAgents.has(a.id) ? t('common.detecting') : t('admin.detectOne') }}</button>
                    <button class="text-btn" @click="openAgentConfig(a)">{{ t('sftp.config') }}</button>
                    <button class="text-btn" :class="a.is_active ? 'warn' : 'success'" @click="toggleAgentStatus(a.id)">{{ a.is_active ? t('common.disabled') : t('common.enabled') }}</button>
                    <button class="text-btn" @click="openDeployScript(a.id)">{{ t('ssh.deployScript') }}</button>
                    <button class="text-btn danger" @click="openDeleteAgent(a)">{{ t('common.delete') }}</button>
                  </div>
                </td>
              </tr>
              <tr v-if="!agentPagination.paginated.value.length"><td colspan="9" class="empty-row">{{ t('common.noData') }}</td></tr>
            </tbody>
          </table>
        </div>
        <div class="table-pagination" v-if="agentPagination.totalPages.value > 1">
          <button class="page-btn" :disabled="agentPagination.page.value <= 1" @click="agentPagination.page.value--">&lt;</button>
          <span class="page-info">{{ agentPagination.page.value }} / {{ agentPagination.totalPages.value }}</span>
          <button class="page-btn" :disabled="agentPagination.page.value >= agentPagination.totalPages.value" @click="agentPagination.page.value++">&gt;</button>
        </div>
      </div>

      <!-- ═══ 网关管理 ═══ -->
      <div v-if="activeTab === 'gateways'" class="admin-content">
        <GatewayManager @share="(gw: any) => openShareModal(gw, 'gateway')" />
      </div>

      <!-- ═══ coturn管理 ═══ -->
      <div v-if="activeTab === 'coturn'" class="admin-content">
        <div class="admin-toolbar">
          <button class="btn primary" @click="openNewCoturn">{{ t('admin.addCoturn') }}</button>
        </div>
        <div class="table-card">
          <div class="table-header">
            <h3>{{ t('admin.coturn') }}</h3>
            <span class="table-count">{{ t('common.total') }} {{ coturnServers.length }} {{ t('admin.coturnCount') }}</span>
          </div>
          <table>
            <thead>
              <tr><th>ID</th><th>{{ t('common.name') }}</th><th>{{ t('common.ip') }}</th><th>STUN</th><th>TURN</th><th>{{ t('common.status') }}</th><th>{{ t('common.share') }}</th><th>{{ t('common.action') }}</th></tr>
            </thead>
            <tbody>
              <tr v-for="c in coturnPagination.paginated.value" :key="c.id">
                <td class="mono">{{ c.id }}</td>
                <td>{{ c.name }}<span v-if="!c.is_owner && c.owner_name" class="owner-tag">{{ t('admin.fromOwner') }} {{ c.owner_name }}</span></td>
                <td class="mono">{{ c.host }}</td>
                <td class="mono">{{ c.port }}/UDP</td>
                <td class="mono">{{ c.tls_port }}/TCP</td>
                <td>
                  <span class="status-dot" :class="{ active: c.is_active }"></span>
                  {{ c.is_active ? t('common.enabled') : t('common.disabled') }}
                </td>
                <td>
                  <button class="share-tag" :class="c.shared_with === 'all' ? 'share-all' : c.shared_with === 'private' ? 'share-private' : 'share-select'" @click="openShareModal(c, 'coturn')">
                    {{ c.shared_with === 'all' ? '🌐' + t('common.allUsers') : c.shared_with === 'private' ? '🔒' + t('common.private') : '👥' + t('common.specified') }}
                  </button>
                </td>
                <td class="action-cell">
                  <div class="action-inner">
                    <button class="text-btn" @click="openEditCoturn(c)">{{ t('common.edit') }}</button>
                    <button class="text-btn info" @click="testCoturn(c)">测试</button>
                    <button class="text-btn danger" @click="openDeleteCoturn(c)">{{ t('common.delete') }}</button>
                  </div>
                </td>
              </tr>
              <tr v-if="!coturnPagination.paginated.value.length"><td colspan="8" class="empty-row">{{ t('common.noData') }}</td></tr>
            </tbody>
          </table>
        </div>
        <div class="table-pagination" v-if="coturnPagination.totalPages.value > 1">
          <button class="page-btn" :disabled="coturnPagination.page.value <= 1" @click="coturnPagination.page.value--">&lt;</button>
          <span class="page-info">{{ coturnPagination.page.value }} / {{ coturnPagination.totalPages.value }}</span>
          <button class="page-btn" :disabled="coturnPagination.page.value >= coturnPagination.totalPages.value" @click="coturnPagination.page.value++">&gt;</button>
        </div>
      </div>

      <!-- ═══ 角色管理 ═══ -->
      <div v-if="activeTab === 'roles'" class="admin-content">
        <div class="admin-toolbar">
          <span class="toolbar-title">{{ t('admin.rolesAndPerms') }}</span>
        </div>
        <div class="roles-grid">
          <div v-for="r in roles" :key="r.key" class="role-card">
            <div class="role-card-header">
              <span class="role-badge-lg" :style="{ background: getRoleColor(r.key) }">{{ r.label }}</span>
              <span class="role-key">{{ r.key }}</span>
            </div>
            <div class="role-card-body">
              <div class="perm-section-title">{{ t('admin.ownedPerms') }}</div>
              <div v-for="perm in r.permissions" :key="perm" class="perm-item">
                <span class="perm-yes">✓</span>
                <span>{{ allPermissions[perm] || perm }}</span>
                <span class="perm-key">{{ perm }}</span>
              </div>
              <div v-if="!r.permissions.length" class="perm-empty">{{ t('admin.noPerms') }}</div>
            </div>
          </div>
        </div>
      </div>

      <!-- ═══ 审计日志 ═══ -->
      <div v-if="activeTab === 'audit'" class="admin-content">
        <div class="admin-toolbar">
          <input v-model="auditSearch" :placeholder="t('admin.searchUserAction')" class="admin-search" @input="auditPagination.resetPage()" />
        </div>
        <div class="table-card">
          <div class="table-header">
            <h3>{{ t('admin.audit') }}</h3>
            <span class="table-count">{{ t('common.total') }} {{ filteredLogs.length }} {{ t('admin.auditLogCount') }}</span>
          </div>
          <table>
            <thead>
              <tr><th>{{ t('admin.auditTime') }}</th><th>{{ t('admin.auditUser') }}</th><th>{{ t('admin.auditAction') }}</th><th>{{ t('admin.auditTarget') }}</th><th>{{ t('admin.auditDetail') }}</th><th>IP</th></tr>
            </thead>
            <tbody>
              <tr v-for="l in auditPagination.paginated.value" :key="l.id">
                <td class="nowrap">{{ fmtAuditTime(l.created_at) || '-' }}</td>
                <td>{{ l.username }}</td>
                <td><span class="action-badge">{{ l.action }}</span></td>
                <td>{{ l.target_type }}{{ l.target_id ? ':' + l.target_id : '' }}</td>
                <td class="detail-cell">{{ l.detail || '-' }}</td>
                <td class="mono">{{ l.ip || '-' }}</td>
              </tr>
              <tr v-if="!auditPagination.paginated.value.length"><td colspan="6" class="empty-row">{{ t('admin.noLogs') }}</td></tr>
            </tbody>
          </table>
        </div>
        <div class="table-pagination" v-if="auditPagination.totalPages.value > 1">
          <button class="page-btn" :disabled="auditPagination.page.value <= 1" @click="auditPagination.page.value--">&lt;</button>
          <span class="page-info">{{ auditPagination.page.value }} / {{ auditPagination.totalPages.value }}</span>
          <button class="page-btn" :disabled="auditPagination.page.value >= auditPagination.totalPages.value" @click="auditPagination.page.value++">&gt;</button>
        </div>
      </div>

      <!-- ═══ 系统配置 ═══ -->
      <div v-if="activeTab === 'settings'" class="admin-content">
        <div class="settings-grid">
          <!-- 密码修改卡片 -->
          <div class="settings-card">
            <div class="settings-card-header">🔑 {{ t('admin.passwordChange') }}</div>
            <div class="settings-card-body">
              <p style="margin:0 0 16px;font-size:13px;color:var(--muted)">{{ t('admin.updatePwdHint') }}</p>
              <div class="form-row"><label>{{ t('admin.currentPassword') }}</label><input v-model="pwdForm.old_password" type="password" placeholder="请输入当前密码" /></div>
              <div class="form-row"><label>{{ t('admin.newPassword') }}</label><input v-model="pwdForm.new_password" type="password" placeholder="请输入新密码" /></div>
              <div class="form-row"><label>{{ t('admin.confirmNewPassword') }}</label><input v-model="pwdForm.confirm_password" type="password" placeholder="请再次输入新密码" /></div>
              <div class="form-row" style="margin-top:8px"><button class="btn primary" style="width:100%;height:40px" @click="changeMyPassword">{{ t('admin.saveChange') }}</button></div>
            </div>
          </div>
          <!-- 浏览器控制台日志等级卡片 -->
          <div class="settings-card">
            <div class="settings-card-header">🖥️ {{ t('admin.consoleLogLevel') }}</div>
            <div class="settings-card-body">
              <p style="margin:0 0 16px;font-size:13px;color:var(--muted)">{{ t('admin.consoleLogLevelDesc') }}</p>
              <div class="form-row">
                <label>{{ t('admin.logLevel') }}</label>
                <select v-model.number="consoleLogLevel" class="form-select" @change="onLogLevelChange">
                  <option :value="0">DEBUG - 全部输出（ICE/DC/帧数据）</option>
                  <option :value="1">INFO - 关键事件（连接状态/结果）</option>
                  <option :value="2">WARN - 仅警告</option>
                  <option :value="3">ERROR - 仅错误</option>
                  <option :value="4">OFF - 禁用所有日志</option>
                </select>
              </div>
              <p style="margin:12px 0 0;font-size:12px;color:var(--muted)">{{ t('admin.currentLevel') }}: <strong>{{ ['DEBUG','INFO','WARN','ERROR','OFF'][consoleLogLevel] }}</strong></p>
            </div>
          </div>
        </div>
      </div>
      <div v-if="activeTab === 'diagnostics'" class="admin-content">
        <DiagnosisPanel />
      </div>

      <!-- ═══ 程序下载 ═══ -->
      <div v-if="activeTab === 'downloads'" class="admin-content">
        <div class="downloads-grid">
          <div class="download-card">
            <div class="download-card-header">
              <span class="download-icon">🤖</span>
              <h3>wragent</h3>
              <span class="download-tag">{{ t('admin.remoteTerminalAgent') }}</span>
            </div>
            <p class="download-desc">{{ t('admin.downloadDesc1') }}</p>
            <div class="download-list">
              <a class="download-item" :href="`/api/deploy/wragent/linux-amd64`" download>
                <span class="download-platform">🐧 Linux (x86_64)</span>
                <span class="download-btn">{{ t('admin.downloadBtn') }}</span>
              </a>
              <a class="download-item" :href="`/api/deploy/wragent/windows-amd64`" download>
                <span class="download-platform">🪟 Windows (x86_64)</span>
                <span class="download-btn">{{ t('admin.downloadBtn') }}</span>
              </a>
            </div>
          </div>

          <div class="download-card">
            <div class="download-card-header">
              <span class="download-icon">🌉</span>
              <h3>wrgateway</h3>
              <span class="download-tag">{{ t('admin.webrtcGateway') }}</span>
            </div>
            <p class="download-desc">{{ t('admin.downloadDesc2') }}</p>
            <div class="download-list">
              <a class="download-item" :href="`/api/deploy/wrgateway/linux-amd64`" download>
                <span class="download-platform">🐧 Linux (x86_64)</span>
                <span class="download-btn">{{ t('admin.downloadBtn') }}</span>
              </a>
              <a class="download-item" :href="`/api/deploy/wrgateway/windows-amd64`" download>
                <span class="download-platform">🪟 Windows (x86_64)</span>
                <span class="download-btn">{{ t('admin.downloadBtn') }}</span>
              </a>
            </div>
          </div>
        </div>
      </div>
    </main>

    <!-- ═══ 用户表单弹窗 ═══ -->
    <Teleport to="body">
      <div v-if="showUserForm" class="modal-mask" @click.self="showUserForm = false">
        <div class="modal-box" style="width:440px">
          <h3>{{ isEditUser ? t('admin.editUser') : t('admin.newUser') }}</h3>
          <div class="form-row"><label>{{ t('admin.username') }}</label><input v-model="userForm.username" :disabled="isEditUser" /></div>
          <div v-if="!isEditUser" class="form-row"><label>密码</label><input v-model="userForm.password" type="password" /></div>
          <div class="form-row"><label>{{ t('admin.email') }}</label><input v-model="userForm.email" /></div>
          <div class="form-row"><label>{{ t('admin.phone') }}</label><input v-model="userForm.phone" /></div>
          <div class="form-row"><label>{{ t('admin.role') }}</label>
            <select v-model="userForm.role" class="form-select">
              <option value="user">{{ t('admin.normalUser') }}</option>
              <option value="admin">{{ t('admin.adminRole') }}</option>
            </select>
          </div>
          <div class="modal-actions">
            <button class="btn" @click="showUserForm = false">{{ t('common.cancel') }}</button>
            <button class="btn primary" @click="saveUser">{{ t('common.confirm') }}</button>
          </div>
        </div>
      </div>
    </Teleport>

    <!-- ═══ 重置密码弹窗 ═══ -->
    <Teleport to="body">
      <div v-if="showResetPwd" class="modal-mask" @click.self="showResetPwd = false">
        <div class="modal-box" style="width:380px">
          <h3>{{ t('admin.resetPassword') }}</h3>
          <div class="form-row"><label>{{ t('admin.newPasswordLabel') }}</label><input v-model="resetPwdNew" type="password" :placeholder="t('admin.newPwdMin6')" /></div>
          <div class="modal-actions">
            <button class="btn" @click="showResetPwd = false">{{ t('common.cancel') }}</button>
            <button class="btn primary" @click="doResetPwd" :disabled="resetPwdNew.length < 6">{{ t('common.confirm') }}</button>
          </div>
        </div>
      </div>
    </Teleport>

    <!-- ═══ 删除用户确认 ═══ -->
    <Teleport to="body">
      <div v-if="showDeleteUser" class="modal-mask" @click.self="showDeleteUser = false">
        <div class="modal-box modal-danger" style="width:380px">
          <h3>{{ t('common.confirmDelete') }}</h3>
          <p>{{ t('admin.confirmDeleteUser') }}「<b>{{ deleteUserName }}</b>」？{{ t('common.confirmDeleteDesc') }}</p>
          <div class="modal-actions">
            <button class="btn" @click="showDeleteUser = false">{{ t('common.cancel') }}</button>
            <button class="btn danger" @click="doDeleteUser">{{ t('common.confirmDelete') }}</button>
          </div>
        </div>
      </div>
    </Teleport>

    <!-- ═══ Agent表单弹窗 ═══ -->
    <Teleport to="body">
      <div v-if="showAgentForm" class="modal-mask" @click.self="showAgentForm = false">
        <div class="modal-box" style="width:440px">
          <h3>{{ t('admin.newIdTitle') }}</h3>
          <div class="form-row">
            <label>Agent ID</label>
            <div class="input-with-btn">
              <input v-model="agentForm.id" readonly />
              <button class="icon-btn" @click="fetchNextAgentId" title="刷新ID">🔄</button>
            </div>
          </div>
          <div class="form-row"><label>{{ t('common.name') }}</label><input v-model="agentForm.name" :placeholder="t('admin.displayName')" /></div>
          <div class="form-row">
            <label>{{ t('admin.coturnServer') }}</label>
            <select v-model="agentForm.coturn_id" class="form-select">
              <option value="">{{ t('admin.noCoturn') }}</option>
              <option v-for="c in coturnServers" :key="c.id" :value="c.id">{{ c.name }}</option>
            </select>
          </div>
          <div class="form-row"><label>{{ t('admin.optionalRemark') }}</label><input v-model="agentForm.remark" :placeholder="t('admin.optionalRemark')" /></div>
          <div class="modal-actions">
            <button class="btn" @click="showAgentForm = false">{{ t('common.cancel') }}</button>
            <button class="btn primary" @click="saveAgent">{{ t('common.confirm') }}</button>
          </div>
        </div>
      </div>
    </Teleport>

    <!-- ═══ Agent Token显示 ═══ -->
    <Teleport to="body">
      <div v-if="showAgentToken" class="modal-mask" @click.self="showAgentToken = false">
        <div class="modal-box" style="width:520px">
          <h3>{{ agentGuideStep === 1 ? 'Agent Token' : t('ssh.deployScript') + ' Agent' }}</h3>

          <!-- Step 1: Token -->
          <template v-if="agentGuideStep === 1">
            <p class="modal-desc">{{ t('admin.tokenSaveHint') }}</p>
            <div class="token-box"><code>{{ agentTokenValue }}</code></div>
            <div class="modal-actions">
              <button class="btn primary" @click="navigator.clipboard.writeText(agentTokenValue); toast?.success(t('admin.copiedToClipboard'))">复制Token</button>
              <button class="btn primary" @click="agentGuideStep = 2">{{ t('admin.nextStepDeploy') }}</button>
            </div>
            <div class="agent-guide-progress"><span class="active">1 Token</span><span class="line"></span><span>2 {{ t('admin.selectMethod') }}</span><span class="line"></span><span>3 {{ t('admin.execCommand') }}</span></div>
          </template>

          <!-- Step 2: 选择部署方式 -->
          <template v-else-if="agentGuideStep === 2">
            <p class="modal-desc">Agent：<b>{{ deployAgentId }}</b>，{{ t('admin.selectDeployMethod') }}</p>
            <div class="deploy-tabs">
              <button :class="{ active: deployMethod === 'docker' }" @click="deployMethod = 'docker'">🐳 Docker</button>
              <button :class="{ active: deployMethod === 'systemd' }" @click="deployMethod = 'systemd'">🔧 systemd</button>
            </div>
            <p style="font-size:13px;color:var(--muted)">{{ deployMethod === 'docker' ? 'Docker 容器方式，需目标机已装 Docker' : 'systemd 系统服务（非 root 自动 sudo）' }}</p>
            <div class="modal-actions">
              <button class="btn" @click="agentGuideStep = 1">{{ t('admin.prevStep') }}</button>
              <button class="btn primary" @click="agentGuideStep = 3">{{ t('admin.nextGetCommand') }}</button>
            </div>
            <div class="agent-guide-progress"><span>1 Token</span><span class="line done"></span><span class="active">2 {{ t('admin.selectMethod') }}</span><span class="line done"></span><span>3 {{ t('admin.execCommand') }}</span></div>
          </template>

          <!-- Step 3: 部署命令 -->
          <template v-else>
            <p class="modal-desc">{{ t('admin.runDeployCommand') }}</p>
            <div class="deploy-hint">
              <div style="display:flex;gap:8px;align-items:center">
                <code style="flex:1">{{ deployLoading ? t('admin.generating') : getDeployCommandFull() }}</code>
                <button class="btn sm" :disabled="deployLoading" @click="refreshDeployUrl">🔄 {{ t('admin.regenerate') }}</button>
                <button class="btn sm" :class="{ copied: deployCopied }" @click="copyDeployScript">
                  {{ deployCopied ? t('admin.copied') + ' ✓' : t('admin.copyCommand') }}
                </button>
              </div>
            </div>
            <div style="font-size:13px;color:var(--muted);line-height:1.7;margin-top:10px">
              {{ t('admin.deployStep1') }}<br/>
              {{ t('admin.deployStep2') }}<br/>
              {{ t('admin.deployStep3') }}<br/>
              {{ t('admin.deployStep4') }}
            </div>
            <div class="modal-actions">
              <button class="btn" @click="agentGuideStep = 2">{{ t('admin.prevStep') }}</button>
              <button class="btn primary" @click="showAgentToken = false">{{ t('admin.done') }}</button>
            </div>
            <div class="agent-guide-progress"><span>1 Token</span><span class="line done"></span><span>2 {{ t('admin.selectMethod') }}</span><span class="line done"></span><span class="active">3 {{ t('admin.execCommand') }}</span></div>
          </template>
        </div>
      </div>
    </Teleport>

    <!-- ═══ 部署脚本弹窗 ═══ -->
    <Teleport to="body">
      <div v-if="showDeployScript" class="modal-mask" @click.self="showDeployScript = false">
        <div class="modal-box modal-wide">
          <div class="deploy-head">
            <div class="deploy-head-info">
              <span class="deploy-head-icon">🚀</span>
              <div>
                <h3>{{ t('ssh.deployScript') }} Agent</h3>
                <p class="deploy-head-id">{{ deployAgentId }}</p>
              </div>
            </div>
            <button class="deploy-close" @click="showDeployScript = false">✕</button>
          </div>

          <div class="deploy-methods">
            <button class="deploy-method" :class="{ active: deployMethod === 'docker' }" @click="deployMethod = 'docker'">
              <span class="deploy-method-icon">🐳</span>
              <span class="deploy-method-name">Docker</span>
              <span class="deploy-method-tag">{{ t('admin.default') }}</span>
            </button>
            <button class="deploy-method" :class="{ active: deployMethod === 'systemd' }" @click="deployMethod = 'systemd'">
              <span class="deploy-method-icon">🔧</span>
              <span class="deploy-method-name">systemd</span>
            </button>
          </div>

          <div class="deploy-desc">
            <template v-if="deployMethod === 'docker'">Docker 容器方式部署，需要目标服务器安装 Docker</template>
            <template v-else>{{ t('admin.systemdDesc') }}</template>
            <div class="deploy-note">🔑 {{ t('admin.mode2Note') }}</div>
          </div>

          <div class="deploy-script-box">
            <div class="deploy-script-header">
              <span>{{ t('admin.executeOnTarget') }}</span>
              <span style="display:flex;gap:8px;align-items:center">
                <button class="btn sm" :disabled="deployLoading" @click="refreshDeployUrl">🔄 {{ t('admin.regenerate') }}</button>
                <button class="btn sm" :class="{ copied: deployCopied }" @click="copyDeployScript">
                  {{ deployCopied ? t('admin.copied') + ' ✓' : t('admin.copyCommand') }}
                </button>
              </span>
            </div>
            <pre class="deploy-code"><code>{{ deployLoading ? t('admin.generating') : getDeployCommandFull() }}</code></pre>
          </div>

          <div class="deploy-steps">
            <div class="deploy-step"><span class="step-num">1</span>{{ t('admin.deployStep1') }}</div>
            <div class="deploy-step"><span class="step-num">2</span>{{ t('admin.deployStep2') }}</div>
            <div class="deploy-step"><span class="step-num">3</span>{{ t('admin.deployStep3') }}</div>
            <div class="deploy-step"><span class="step-num">4</span>{{ t('admin.deployStep4') }}</div>
          </div>

          <div class="deploy-foot">
            <span class="deploy-foot-note">{{ t('admin.deployRequirement') }}</span>
            <button class="btn primary" @click="showDeployScript = false">{{ t('common.cancel') }}</button>
          </div>
        </div>
      </div>
    </Teleport>

<!-- ═══ 删除Agent确认 ═══ -->
    <Teleport to="body">
      <div v-if="showDeleteAgent" class="modal-mask" @click.self="showDeleteAgent = false">
        <div class="modal-box modal-danger" style="width:380px">
          <h3>{{ t('common.confirmDelete') }}</h3>
          <p>{{ t('admin.confirmDeleteGateway') }}「<b>{{ deleteAgentName }}</b>」吗？</p>
          <div class="modal-actions">
            <button class="btn" @click="showDeleteAgent = false">{{ t('common.cancel') }}</button>
            <button class="btn danger" @click="doDeleteAgent">{{ t('common.confirmDelete') }}</button>
          </div>
        </div>
      </div>
    </Teleport>

    <!-- ═══ coturn表单弹窗 ═══ -->
    <Teleport to="body">
      <div v-if="showCoturnForm" class="modal-mask" @click.self="showCoturnForm = false">
        <div class="modal-box" style="width:500px">
          <h3>{{ isEditCoturn ? t('admin.editCoturn') : t('admin.addCoturn') }}</h3>
          <div class="form-row"><label>{{ t('common.name') }}</label><input v-model="coturnForm.name" /></div>
          <div class="form-row"><label>{{ t('ssh.host') }}</label><input v-model="coturnForm.host" placeholder="IP或域名" /></div>
          <div class="form-row-2col">
            <div class="form-row"><label>{{ t('admin.port') }}</label><input v-model.number="coturnForm.port" type="number" /></div>
            <div class="form-row"><label>{{ t('admin.tlsPort') }}</label><input v-model.number="coturnForm.tls_port" type="number" /></div>
          </div>
          <div class="form-row"><label>{{ t('admin.hmacKey') }}</label><input v-model="coturnForm.secret" /></div>
          <div class="form-row"><label>Realm</label><input v-model="coturnForm.realm" /></div>
          <div class="form-row"><label>{{ t('admin.relayPortRange') }}</label><input v-model="coturnForm.relay_range" placeholder="49160-49259" /></div>
          <div class="form-row"><label>{{ t('admin.totalQuota') }}</label><input v-model.number="coturnForm.total_quota" type="number" /></div>
          <div class="form-row"><label>{{ t('admin.optionalRemark') }}</label><input v-model="coturnForm.remark" /></div>
          <div class="modal-actions">
            <button class="btn" @click="showCoturnForm = false">{{ t('common.cancel') }}</button>
            <button class="btn primary" @click="saveCoturn">{{ t('common.confirm') }}</button>
          </div>
        </div>
      </div>
    </Teleport>

    <!-- ═══ 删除coturn确认 ═══ -->
    <Teleport to="body">
      <div v-if="showDeleteCoturn" class="modal-mask" @click.self="showDeleteCoturn = false">
        <div class="modal-box modal-danger" style="width:380px">
          <h3>{{ t('common.confirmDelete') }}</h3>
          <p>{{ t('admin.confirmDeleteGateway') }}「<b>{{ deleteCoturnName }}</b>」吗？</p>
          <div class="modal-actions">
            <button class="btn" @click="showDeleteCoturn = false">{{ t('common.cancel') }}</button>
            <button class="btn danger" @click="doDeleteCoturn">{{ t('common.confirmDelete') }}</button>
          </div>
        </div>
      </div>

      <!-- STUN/TURN 测试弹窗 -->
      <div v-if="showCoturnTest" class="modal-mask" @click.self="showCoturnTest = false">
        <div class="modal-box modal-wide" style="width:640px">
          <h3>{{ t('admin.stunTurnTest') }}</h3>
          <div v-if="coturnTestCreds" class="test-creds-info">
            <span class="test-label">{{ t('admin.creds') }}：</span><b class="mono">{{ coturnTestCreds.username }}</b>
            <span style="margin:0 4px">/</span><b class="mono">{{ coturnTestCreds.credential }}</b>
          </div>
          <div v-if="coturnTestBusy" class="test-loading">
            <div class="test-spinner"></div>
            <span>{{ t('admin.testingConnectivity') }}</span>
          </div>
          <div v-if="coturnTestResult">
            <div class="test-server-info">
              <span class="test-label">{{ t('admin.server') }}：</span><b>{{ coturnTestResult.server }}</b>
              <span class="test-label" style="margin-left:16px">{{ t('ssh.host') }}：</span><b class="mono">{{ coturnTestResult.host }}</b>
            </div>
            <div class="test-results-wrap">
              <table class="test-results-table">
                <thead>
                  <tr><th>{{ t('admin.testItem') }}</th><th>{{ t('common.status') }}</th><th>{{ t('admin.duration') }}</th><th>{{ t('admin.auditDetail') }}</th></tr>
                </thead>
                <tbody>
                  <tr v-for="(r, i) in coturnTestResult.results" :key="i">
                    <td>{{ r.server }}</td>
                    <td>
                      <span v-if="r.status === '成功'" class="test-status ok">{{ t('common.success') }}</span>
                      <span v-else-if="r.status === '回退STUN'" class="test-status warn">{{ t('admin.fallbackStun') }}</span>
                      <span v-else-if="r.status === '超时'" class="test-status timeout">{{ t('common.timeout') }}</span>
                      <span v-else-if="r.status === '跳过'" class="test-status skip">{{ t('common.skip') }}</span>
                      <span v-else class="test-status fail">{{ r.status }}</span>
                    </td>
                    <td class="mono">{{ r.time }}</td>
                    <td class="mono" style="font-size:12px">{{ r.detail }}</td>
                  </tr>
                </tbody>
              </table>
            </div>
          </div>
          <div class="modal-actions">
            <button class="btn primary" @click="showCoturnTest = false">{{ t('common.cancel') }}</button>
          </div>
        </div>
      </div>
    </Teleport>

    <!-- ═══ 共享管理弹窗 ═══ -->
    <Teleport to="body">
      <div v-if="showShareModal" class="modal-mask" @click.self="showShareModal = false">
        <div class="modal-box" style="width:480px">
          <h3>{{ t('admin.shareSettings') }} - {{ shareTarget?.name }}</h3>
          <div class="share-options">
            <label class="share-radio" :class="{ selected: shareMode === 'private' }">
              <input type="radio" v-model="shareMode" value="private" />
              <span class="share-icon">🔒</span>
              <div>
                <div class="share-title">{{ t('common.private') }}</div>
                <div class="share-desc">{{ t('admin.sharePrivateDesc') }}</div>
              </div>
            </label>
            <label class="share-radio" :class="{ selected: shareMode === 'all' }">
              <input type="radio" v-model="shareMode" value="all" />
              <span class="share-icon">🌐</span>
              <div>
                <div class="share-title">{{ t('common.allUsers') }}</div>
                <div class="share-desc">{{ t('admin.shareAllDesc') }}</div>
              </div>
            </label>
            <label class="share-radio" :class="{ selected: shareMode === 'select' }">
              <input type="radio" v-model="shareMode" value="select" />
              <span class="share-icon">👥</span>
              <div>
                <div class="share-title">{{ t('common.specified') }}</div>
                <div class="share-desc">{{ t('admin.shareSelectDesc') }}</div>
              </div>
            </label>
          </div>
          <div v-if="shareMode === 'select'" class="share-user-list">
            <div v-for="u in allUsers" :key="u.id" class="share-user-item">
              <label>
                <input type="checkbox" :value="u.id" v-model="shareSelectedUsers" />
                <span class="share-username">{{ u.username }}</span>
              </label>
            </div>
            <div v-if="!allUsers.length" class="empty-row">{{ t('admin.noOtherUsers') }}</div>
          </div>
          <div class="modal-actions">
            <button class="btn" @click="showShareModal = false">{{ t('common.cancel') }}</button>
            <button class="btn primary" @click="saveShare">{{ t('admin.save') }}</button>
          </div>
        </div>
      </div>
    </Teleport>

    <!-- ═══ Agent配置弹窗 ═══ -->
    <AgentConfigModal
      v-if="configAgentId"
      :agent-id="configAgentId"
      :agent-name="configAgentName"
      :agent-online="configAgentOnline"
      @close="closeAgentConfig"
      @updated="onAgentInfoUpdated"
      @token="onAgentToken"
    />
  </div>
</template>

<style scoped>
.admin-wrap { height: 100%; display: flex; overflow: hidden; }

/* 左侧菜单 */
.admin-sidebar { width: 200px; border-right: 1px solid var(--border); background: var(--panel); padding: 16px 0; flex-shrink: 0; overflow-y: auto; }
.sidebar-head { display:flex; align-items:center; gap:8px; padding:8px 20px 16px; font-size:14px; font-weight:600; border-bottom:1px solid var(--border); margin-bottom:8px; }
.sidebar-head-icon { font-size:16px; }
.sidebar-nav { display:flex; flex-direction:column; gap:2px; padding:0 8px; }
.menu-item { display: flex; align-items: center; gap: 10px; width: 100%; padding: 10px 14px; border: none; border-radius: 8px; background: none; color: var(--fg-2); font-size: 13px; cursor: pointer; transition: all .15s; text-align: left; }
.menu-item:hover { background: var(--panel-2); color: var(--fg); }
.menu-item.active { background: var(--accent-soft); color: var(--accent); font-weight: 600; }
.menu-icon { font-size: 15px; width: 20px; text-align: center; }

/* 右侧内容 */
.admin-main { flex: 1; display: flex; flex-direction: column; overflow: hidden; min-width: 0; }
.admin-header { padding: 16px 24px 0; }
.admin-header h2 { margin: 0; font-size: 20px; font-weight: 600; }

.admin-content { flex: 1; display: flex; flex-direction: column; overflow: hidden; padding: 12px 24px 24px; }
.admin-toolbar { display: flex; gap: 8px; margin-bottom: 12px; align-items: center; }
.admin-search { flex: 1; max-width: 320px; padding: 8px 12px; border: 1px solid var(--border); border-radius: 6px; background: var(--panel); color: var(--fg); font-size: 13px; }
.admin-search:focus { outline: none; border-color: var(--accent); }

.admin-table-wrap { /* deprecated: use .table-card */ }
.admin-table { /* deprecated: use global table styles */ }
.admin-table th { position: sticky; top: 0; z-index: 1; }
.admin-table tbody tr:hover { background: var(--panel-2); }
.empty-row { text-align: center; color: var(--muted); padding: 32px !important; }
.mono { font-size: 13px; }
.user-cell { font-weight: 600; }
.token-cell { max-width: 160px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.token-btn { background: none; border: none; cursor: pointer; font-size: 14px; padding: 2px 4px; }
.nowrap { white-space: nowrap; }
.detail-cell { max-width: 200px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

/* 分页 */
.pagination { display: flex; align-items: center; justify-content: center; gap: 12px; padding: 12px 0 0; }
.page-btn { min-width: 32px; height: 32px; border: 1px solid var(--border); border-radius: 6px; background: var(--panel); color: var(--fg); font-size: 13px; cursor: pointer; display: flex; align-items: center; justify-content: center; }
.page-btn:hover:not(:disabled) { border-color: var(--accent); color: var(--accent); }
.page-btn:disabled { opacity: .3; cursor: default; }
.page-info { font-size: 13px; color: var(--muted); min-width: 60px; text-align: center; }

.role-badge { display: inline-block; padding: 2px 8px; border-radius: 4px; color: white; font-size: 13px; font-weight: 600; }
.status-dot { display: inline-block; width: 8px; height: 8px; border-radius: 50%; background: #dc2626; margin-right: 4px; vertical-align: middle; }
.status-dot.active { background: #34d399; }
.default-badge { display: inline-block; padding: 2px 8px; border-radius: 4px; background: var(--accent); color: white; font-size: 13px; }
/* 文字链接按钮 - 使用全局 .text-btn */
.action-badge { display: inline-block; padding: 2px 6px; border-radius: 3px; background: var(--panel-2); font-size: 13px; }


.conn-tag { display:inline-block; padding:2px 8px; border-radius:4px; font-size:13px; font-weight:600; }
.conn-tag.p2p { background:#d1fae5; color:#065f46; }
.conn-tag.relay { background:#fef3c7; color:#92400e; }
.conn-tag.detecting { background:#dbeafe; color:#1e40af; animation:pulse 1.5s infinite; }
.conn-tag.pending { background:#f3f4f6; color:#6b7280; }
.conn-tag.offline { background:#f3f4f6; color:#6b7280; }
.conn-tag.error { background:#fee2e2; color:#991b1b; }
@keyframes pulse { 0%,100% { opacity:1; } 50% { opacity:.5; } }

.action-cell { white-space: nowrap; }
.action-inner { display: flex; gap: 4px; align-items: center; }
.icon-btn { min-width: 28px; min-height: 28px; font-size: 14px; border: 1px solid var(--border); border-radius: 6px; background: var(--panel); color: var(--fg-2); cursor: pointer; display: inline-flex; align-items: center; justify-content: center; }
.icon-btn:hover { border-color: var(--accent); color: var(--accent); }
.icon-btn.sm { min-width: 26px; min-height: 26px; font-size: 13px; }
.icon-btn.danger { color: #dc2626; border-color: #fecaca; }
.icon-btn.danger:hover { background: #dc2626; color: white; }
.icon-btn:disabled { opacity: .3; cursor: default; }

.perm-table th, .perm-table td { text-align: center; }
.perm-table th:first-child, .perm-table td:first-child { text-align: left; }
.role-col { min-width: 80px; }
.perm-label { font-weight: 500; }
.perm-key { color: var(--muted); font-size: 11px; margin-left: 4px; }
.perm-yes { color: #16a34a; font-weight: 700; font-size: 14px; }
.toolbar-title { font-weight: 600; font-size: 14px; }

.roles-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(360px, 1fr)); gap: 20px; padding: 8px 0; }
.role-card { background: var(--panel); border: 1px solid var(--border); border-radius: 10px; overflow: hidden; }
.role-card-header { display: flex; align-items: center; gap: 10px; padding: 16px 20px; border-bottom: 1px solid var(--border); background: var(--panel-2); }
.role-badge-lg { display: inline-block; padding: 4px 14px; border-radius: 6px; color: white; font-size: 14px; font-weight: 600; }
.role-key { color: var(--muted); font-size: 12px; }
.role-card-body { padding: 16px 20px; }
.perm-section-title { font-size: 12px; color: var(--muted); margin-bottom: 8px; font-weight: 600; }
.perm-item { display: flex; align-items: center; gap: 6px; padding: 4px 0; font-size: 13px; }
.perm-empty { color: var(--muted); font-size: 13px; padding: 8px 0; }

/* 按钮 - 使用全局 .btn */

.modal-mask { position: fixed; inset: 0; background: rgba(0,0,0,.45); z-index: 200; display: flex; align-items: center; justify-content: center; }
.modal-box { background: var(--panel); border-radius: 12px; padding: 24px; max-width: 92vw; max-height: 85vh; overflow-y: auto; box-shadow: 0 20px 60px rgba(0,0,0,.3); }
.modal-box.modal-danger { border: 1px solid #fecaca; }
.modal-box.modal-wide { width: 600px; }
.modal-box h3 { margin: 0 0 12px; font-size: 17px; }
.modal-box p { font-size: 14px; color: var(--fg-2); line-height: 1.6; }
.modal-box p b { color: var(--fg); }
.modal-desc { font-size: 13px; color: var(--muted); margin-bottom: 12px; }
.modal-actions { display: flex; justify-content: flex-end; gap: 8px; margin-top: 16px; }
.form-row { margin-bottom: 12px; }
.form-row label { display: block; font-size: 13px; color: var(--muted); margin-bottom: 4px; font-weight: 500; }
.form-row input, .form-select { width: 100%; padding: 8px 10px; border: 1px solid var(--border); border-radius: 6px; background: var(--panel-2); color: var(--fg); font-size: 13px; box-sizing: border-box; }
.form-row input:focus, .form-select:focus { outline: none; border-color: var(--accent); }
.form-row-2col { display: flex; gap: 12px; }
.form-row-2col .form-row { flex: 1; }
.form-select { appearance: auto; }



.test-loading { display:flex; align-items:center; gap:10px; padding:24px 0; color:var(--muted); font-size:14px; }
.spinner, .test-spinner { width:20px; height:20px; border:2px solid var(--border); border-top-color:var(--accent); border-radius:50%; animation:spin .8s linear infinite; display:inline-block; vertical-align:middle; margin-right:4px; }
@keyframes spin { to { transform:rotate(360deg); } }
.test-server-info { display:flex; align-items:center; gap:4px; padding:12px 16px; background:var(--panel-2); border-radius:8px; margin-bottom:16px; font-size:14px; }
.test-label { color:var(--muted); }
.test-results-wrap { border:1px solid var(--border); border-radius:8px; overflow:hidden; }
.test-results-table { width:100%; border-collapse:collapse; font-size:13px; }
.test-results-table th { background:var(--panel-2); padding:10px 14px; text-align:left; font-weight:600; color:var(--muted); border-bottom:1px solid var(--border); }
.test-results-table td { padding:10px 14px; border-bottom:1px solid var(--border); }
.test-results-table tr:last-child td { border-bottom:none; }
.test-status { display:inline-block; padding:2px 10px; border-radius:4px; font-size:12px; font-weight:600; }
.test-status.ok { background:#d1fae5; color:#065f46; }
.test-status.warn { background:#fef3c7; color:#92400e; }
.test-status.fail { background:#fee2e2; color:#991b1b; }
.test-status.timeout { background:#f3f4f6; color:#6b7280; }
.test-status.skip { background:#e0e7ff; color:#3730a3; }

.input-with-btn { display: flex; gap: 6px; align-items: center; }
.input-with-btn input { flex: 1; }
.input-with-btn .icon-btn { flex-shrink: 0; }

.token-box { background: var(--bg); border: 1px solid var(--border); border-radius: 6px; padding: 12px; margin: 8px 0; overflow-x: auto; }
.token-box code { font-size: 13px; word-break: break-all; font-family: monospace; }

.deploy-head { display: flex; align-items: flex-start; justify-content: space-between; margin-bottom: 16px; }
.deploy-head-info { display: flex; align-items: center; gap: 10px; }
.deploy-head-icon { font-size: 28px; }
.deploy-head-info h3 { margin: 0; font-size: 17px; }
.deploy-head-id { margin: 2px 0 0; font-size: 13px; color: var(--muted); }
.deploy-close { background: none; border: none; font-size: 18px; color: var(--muted); cursor: pointer; padding: 4px 8px; border-radius: 4px; }
.deploy-close:hover { background: var(--panel-2); color: var(--fg); }

.deploy-methods { display: flex; gap: 10px; margin-bottom: 12px; }
.deploy-method { flex: 1; display: flex; flex-direction: column; align-items: center; gap: 6px; padding: 14px 10px; border: 2px solid var(--border); border-radius: 10px; background: var(--panel); cursor: pointer; transition: all .15s; position: relative; }
.deploy-method:hover { border-color: var(--accent); }
.deploy-method.active { border-color: var(--accent); background: var(--accent-soft); }
.deploy-method-icon { font-size: 22px; }
.deploy-method-name { font-size: 13px; font-weight: 600; }
.deploy-method-tag { position: absolute; top: 6px; right: 8px; font-size: 11px; padding: 1px 6px; border-radius: 4px; background: var(--accent); color: #fff; }

.deploy-desc { font-size: 13px; color: var(--muted); margin-bottom: 12px; }
.deploy-note { margin-top: 8px; font-size: 12px; color: var(--accent); background: var(--accent-soft); border-radius: 6px; padding: 6px 10px; }
.pending-badge { display: inline-block; padding: 1px 8px; border-radius: 10px; background: #f59e0b; color: #fff; font-size: 12px; vertical-align: middle; }

.deploy-script-box { background: var(--bg); border: 1px solid var(--border); border-radius: 8px; overflow: hidden; margin-bottom: 16px; }
.deploy-script-header { display: flex; justify-content: space-between; align-items: center; padding: 10px 14px; border-bottom: 1px solid var(--border); font-size: 13px; color: var(--muted); }
.deploy-code { margin: 0; padding: 14px; font-size: 13px; font-family: monospace; line-height: 1.6; overflow-x: auto; white-space: pre-wrap; word-break: break-all; color: var(--fg); }

.deploy-steps { display: flex; flex-direction: column; gap: 8px; margin-bottom: 16px; }
.deploy-step { display: flex; align-items: center; gap: 8px; font-size: 13px; color: var(--fg-2); }
.step-num { display: inline-flex; align-items: center; justify-content: center; width: 20px; height: 20px; border-radius: 50%; background: var(--accent-soft); color: var(--accent); font-size: 12px; font-weight: 700; flex-shrink: 0; }

.deploy-foot { display: flex; align-items: center; justify-content: space-between; padding-top: 12px; border-top: 1px solid var(--border); }
.deploy-foot-note { font-size: 12px; color: var(--muted); }

.btn.copied { background: #34d399; color: #fff; border-color: #34d399; }
.agent-guide-progress { display: flex; align-items: center; gap: 4px; margin-top: 14px; font-size: 12px; color: var(--muted); justify-content: center; }
.agent-guide-progress .line { flex: 1; max-width: 40px; height: 2px; background: var(--border); }
.agent-guide-progress .line.done { background: var(--accent); }
.agent-guide-progress .active { color: var(--accent); font-weight: 600; }

/* 共享标签 */
.share-tag { display:inline-block; padding:2px 8px; border-radius:4px; font-size:12px; cursor:pointer; border:1px solid transparent; transition:all .15s; }
.share-private { background:#f3f4f6; color:#6b7280; border-color:#d1d5db; }
.share-all { background:#d1fae5; color:#065f46; border-color:#a7f3d0; }
.share-select { background:#dbeafe; color:#1e40af; border-color:#93c5fd; }
.share-tag:hover { opacity:.8; }

/* 来源标签 */
.owner-tag { display:inline-block; margin-left:6px; padding:1px 6px; border-radius:3px; font-size:11px; background:#f3f4f6; color:#6b7280; }

/* 共享弹窗 */
.share-options { display:flex; flex-direction:column; gap:8px; margin:12px 0; }
.share-radio { display:flex; align-items:flex-start; gap:10px; padding:10px 12px; border:1px solid var(--border); border-radius:8px; cursor:pointer; transition:all .15s; }
.share-radio:hover { border-color:var(--accent); }
.share-radio.selected { border-color:var(--accent); background:var(--accent-soft); }
.share-radio input[type="radio"] { margin-top:4px; }
.share-icon { font-size:20px; }
.share-title { font-size:13px; font-weight:600; }
.share-desc { font-size:12px; color:var(--muted); }
.share-user-list { max-height:200px; overflow-y:auto; border:1px solid var(--border); border-radius:6px; padding:8px; margin-top:8px; }
.share-user-item { padding:6px 4px; }
.share-user-item label { display:flex; align-items:center; gap:8px; cursor:pointer; }
.share-username { font-size:13px; }

/* 测试凭证 */
.test-creds-info { font-size:13px; color:var(--fg-2); margin-bottom:8px; padding:6px 10px; background:var(--bg); border-radius:6px; border:1px solid var(--border); }

/* 密码管理 */
.pwd-card {
  width: 100%;
  max-width: 400px;
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: 12px;
  padding: 32px;
}
.pwd-form { max-width:400px; }
.pwd-form .form-row { margin-bottom:14px; }
.pwd-form label { display:block; font-size:13px; color:var(--muted); margin-bottom:4px; }
.pwd-form input { width:100%; padding:8px 10px; border:1px solid var(--border); border-radius:6px; background:var(--panel-2); color:var(--fg); font-size:14px; }

/* 程序下载 */
.downloads-grid { display:grid; grid-template-columns:repeat(auto-fit, minmax(320px, 1fr)); gap:20px; padding:8px 0; }
.download-card { background:var(--panel); border:1px solid var(--border); border-radius:10px; overflow:hidden; }
.download-card-header { display:flex; align-items:center; gap:10px; padding:16px 20px; border-bottom:1px solid var(--border); background:var(--panel-2); }
.download-icon { font-size:24px; }
.download-card-header h3 { margin:0; font-size:16px; font-weight:600; }
.download-desc { font-size:13px; color:var(--fg-2); line-height:1.6; padding:16px 20px 12px; margin:0; }
.download-list { padding:0 20px 16px; display:flex; flex-direction:column; gap:8px; }
.download-item { display:flex; align-items:center; justify-content:space-between; padding:10px 14px; border:1px solid var(--border); border-radius:8px; text-decoration:none; color:var(--fg); transition:all .15s; }
.download-item:hover { border-color:var(--accent); background:var(--accent-soft); }
.download-platform { font-size:13px; font-weight:500; }
.download-btn { font-size:12px; color:var(--accent); font-weight:600; }
.download-tag { margin-left:auto; font-size:11px; padding:2px 10px; border-radius:999px; background:var(--accent-soft); color:var(--accent); font-weight:600; white-space:nowrap; }

.ver-badge { display:inline-block; padding:2px 8px; border-radius:4px; font-size:12px; font-weight:600; background:#e0e7ff; color:#4f46e5; white-space:nowrap; }
.ver-badge.old { background:#fef3c7; color:#d97706; }
.ver-badge.muted { color:var(--fg-muted); background:transparent; }
.upgrade-tip { margin-left:4px; font-size:12px; color:#d97706; cursor:help; }
.settings-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(380px, 1fr)); gap: 16px; padding: 20px; width: 100%; }
.settings-card { background: var(--panel); border: 1px solid var(--border); border-radius: 10px; overflow: hidden; }
.settings-card-header { display: flex; align-items: center; gap: 10px; padding: 16px 20px; border-bottom: 1px solid var(--border); background: var(--panel-2); font-weight: 600; font-size: 15px; }
.settings-card-body { padding: 20px; }
</style>
