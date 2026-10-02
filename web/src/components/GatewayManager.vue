<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount, inject } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '../api'
import { fetchOnlineGateways } from '../utils/webrtc'

const { t } = useI18n()
const toast = inject<any>('toast')
const emit = defineEmits<{ (e: 'share', gw: Gateway): void }>()

interface Gateway {
  id: string; name: string; token: string; url: string;
  remark: string; is_active: number; online?: boolean;
  owner_id?: string; owner_name?: string; is_owner?: boolean; shared_with?: string;
  version?: string; needs_upgrade?: boolean; latest_version?: string;
}

const gateways = ref<Gateway[]>([])

/* ── 分页(与 AdminPanel usePagination 同款, 常驻分页栏) ── */
const pageSize = 10
const page = ref(1)
const totalPages = computed(() => Math.max(1, Math.ceil(gateways.value.length / pageSize)))
const total = computed(() => gateways.value.length)
const pageNumbers = computed(() => {
  const pages: number[] = []
  const start = Math.max(1, page.value - 2)
  const end = Math.min(totalPages.value, page.value + 2)
  for (let i = start; i <= end; i++) pages.push(i)
  return pages
})
const paginated = computed(() => {
  const start = (page.value - 1) * pageSize
  return gateways.value.slice(start, start + pageSize)
})
function goPage(p: number) { if (p >= 1 && p <= totalPages.value) page.value = p }
const onlineIds = ref<Set<string>>(new Set())
const showAddForm = ref(false)
const showEditForm = ref(false)
const showTokenDialog = ref(false)
const showDeleteDialog = ref(false)
const showRegenConfirm = ref(false)
const newGateway = ref<{ id: string; name: string; url: string; remark: string }>({ id: '', name: '', url: '', remark: '' })
const editGateway = ref<Gateway>({ id: '', name: '', token: '', url: '', remark: '', is_active: 1 })
const newToken = ref('')
const deleteTarget = ref<Gateway | null>(null)
const regenTarget = ref<Gateway | null>(null)
const adding = ref(false)

async function refreshGateways(silent = false) {
  try {
    const data = await api.adminListGateways()
    gateways.value = data.gateways
  } catch (e: any) { if (!silent) toast?.error(e.message) }
}

async function generateGatewayId() {
  try {
    const data = await api.adminNextGatewayId()
    newGateway.value.id = data.id
  } catch {}
}

function openAdd() {
  newGateway.value = { id: '', name: '', url: 'wss://', remark: '' }
  generateGatewayId()
  showAddForm.value = true
}

async function doAdd() {
  if (!newGateway.value.name || !newGateway.value.url) { toast?.error(t('links.titleAndUrlRequired')); return }
  adding.value = true
  try {
    const data = await api.adminAddGateway(newGateway.value)
    newToken.value = data.token
    showAddForm.value = false
    showTokenDialog.value = true
    await refreshGateways()
  } catch (e: any) { toast?.error(e.message) }
  finally { adding.value = false }
}

function openEdit(gw: Gateway) {
  editGateway.value = { ...gw }
  showEditForm.value = true
}

async function doEdit() {
  try {
    await api.adminUpdateGateway(editGateway.value.id, { name: editGateway.value.name, url: editGateway.value.url, remark: editGateway.value.remark })
    showEditForm.value = false
    await refreshGateways()
    toast?.success(t('admin.updated'))
  } catch (e: any) { toast?.error(e.message) }
}

function openDelete(gw: Gateway) { deleteTarget.value = gw; showDeleteDialog.value = true }

async function doDelete() {
  if (!deleteTarget.value) return
  try {
    await api.adminDeleteGateway(deleteTarget.value.id)
    showDeleteDialog.value = false
    await refreshGateways()
    toast?.success(t('admin.deleted'))
  } catch (e: any) { toast?.error(e.message) }
}

function openRegen(gw: Gateway) { regenTarget.value = gw; showRegenConfirm.value = true }

async function confirmRegenToken() {
  if (!regenTarget.value) return
  try {
    const data = await api.adminRegenerateGatewayToken(regenTarget.value.id)
    newToken.value = data.token
    editGateway.value.token = data.token
    showRegenConfirm.value = false
    showTokenDialog.value = true
    await refreshGateways()
  } catch (e: any) { toast?.error(e.message) }
}

async function toggleStatus(gw: Gateway) {
  try { await api.adminToggleGatewayStatus(gw.id); await refreshGateways() } catch {}
}

async function upgradeGateway(gw: Gateway) {
  if (!confirm(t('admin.upgradeConfirmGateway'))) return
  try {
    const resp = await fetch(`/api/admin/gateways/${gw.id}/upgrade`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${localStorage.getItem('token')}` },
      body: JSON.stringify({}),
    })
    const data = await resp.json().catch(() => ({}))
    if (!resp.ok) throw new Error(typeof data.detail === 'string' ? data.detail : t('common.upgradeFailed'))
    toast?.success(t('common.upgradeSuccess'))
    await refreshGateways(true)
  } catch (e: any) { toast?.error(e.message || t('common.upgradeFailed')) }
}

function copyToken() { navigator.clipboard.writeText(newToken.value); toast?.success(t('common.copySuccess')) }
function copyEditToken() { navigator.clipboard.writeText(editGateway.value.token); toast?.success(t('common.copySuccess')) }
function copyGatewayToken(token: string) { navigator.clipboard.writeText(token); toast?.success(t('common.copySuccess')) }

let pollTimer: ReturnType<typeof setInterval> | null = null

async function checkOnline() {
  try {
    const { getCurrentUser } = await import('../api')
    const user = getCurrentUser()
    if (!user) return
    const online = await fetchOnlineGateways(user.token || '')
    onlineIds.value = new Set(online.map(g => g.id))
    await refreshGateways(true)
  } catch {}
}

onMounted(() => {
  checkOnline()
  pollTimer = setInterval(checkOnline, 10000)
})

onBeforeUnmount(() => {
  if (pollTimer) clearInterval(pollTimer)
})
</script>

<template>
  <div class="gw-mgr">
    <div class="gw-header">
      <div class="gw-actions">
        <button class="btn primary" @click="openAdd">{{ t('admin.addGateway') }}</button>
      </div>
    </div>

    <div class="gw-table-wrap">
      <div class="table-card"><table class="gw-table">
        <thead><tr>
          <th>ID</th><th>{{ t('common.name') }}</th><th>{{ t('common.ip') }}</th><th>{{ t('admin.versionTag') }}</th><th>{{ t('common.token') }}</th><th>{{ t('common.status') }}</th><th>{{ t('common.share') }}</th><th>{{ t('common.action') }}</th>
        </tr></thead>
        <tbody>
          <tr v-for="gw in paginated" :key="gw.id">
            <td class="mono">{{ gw.id }}</td>
            <td>{{ gw.name }}<span v-if="gw.owner_name" class="owner-badge">{{ gw.owner_name }}</span></td>
            <td class="mono" :title="gw.url">{{ gw.url || '-' }}</td>
            <td>
              <span v-if="gw.version" class="ver-badge" :class="{ old: gw.needs_upgrade, muted: gw.version === 'dev' }">{{ gw.version === 'dev' ? 'dev' : 'v' + gw.version }}</span>
              <span v-else class="ver-badge muted">-</span>
              <span v-if="gw.needs_upgrade" class="upgrade-tip" :title="'Latest v' + (gw.latest_version || '')">⚠️ {{ t('common.upgrade') }}</span>
            </td>
            <td class="token-cell">
              <button v-if="gw.token" class="token-btn" :title="t('common.copy')" @click="copyGatewayToken(gw.token)">📋</button>
              <span v-else :title="t('admin.sharedReadonly')">-</span>
            </td>
            <td>
              <span class="status-dot" :class="gw.is_active ? (onlineIds.has(gw.id) ? 'online' : 'offline') : 'disabled'"></span>
              {{ !gw.is_active ? t('common.disabled') : (onlineIds.has(gw.id) ? t('common.online') : t('common.offline')) }}
            </td>
            <td><button v-if="gw.is_owner" class="share-tag" :class="gw.shared_with === 'all' ? 'share-all' : gw.shared_with === 'private' ? 'share-private' : 'share-select'" @click="emit('share', gw)">
              {{ gw.shared_with === 'all' ? t('admin.shareAll') : gw.shared_with === 'private' ? t('admin.sharePrivate') : t('admin.shareSelect') }}
            </button><span v-else class="share-tag" :title="t('admin.sharedReadonly')">{{ t('admin.sharedFromTag') }}</span></td>
            <td class="actions-cell">
              <div class="actions-inner">
                <button v-if="gw.needs_upgrade" class="text-btn success" :disabled="!gw.is_owner" :title="gw.is_owner ? '' : t('admin.sharedReadonly')" @click="upgradeGateway(gw)" style="color:#16a34a">{{ t('admin.upgradeVersion') }}{{ gw.latest_version }}</button>
                <button class="text-btn sm" :disabled="!gw.is_owner" :title="gw.is_owner ? '' : t('admin.sharedReadonly')" @click="openEdit(gw)">{{ t('common.edit') }}</button>
                <button class="text-btn sm danger" :disabled="!gw.is_owner" :title="gw.is_owner ? '' : t('admin.sharedReadonly')" @click="openDelete(gw)">{{ t('common.delete') }}</button>
                <button class="text-btn sm" :class="gw.is_active ? 'warn' : 'success'" :disabled="!gw.is_owner" :title="gw.is_owner ? '' : t('admin.sharedReadonly')" @click="toggleStatus(gw)">{{ gw.is_active ? t('common.disabled') : t('common.enabled') }}</button>
              </div>
            </td>
          </tr>
          <tr v-if="!paginated.length"><td colspan="8" class="empty-row">{{ t('admin.gateways') }}</td></tr>
        </tbody>
</table>
          <div class="table-pagination">
          <button class="page-btn" :disabled="page <= 1" @click="goPage(page - 1)">‹ {{ t('common.prev') }}</button>
          <button v-for="p in pageNumbers" :key="p" class="page-btn page-num" :class="{ active: p === page }" @click="goPage(p)">{{ p }}</button>
          <button class="page-btn" :disabled="page >= totalPages" @click="goPage(page + 1)">{{ t('common.next') }} ›</button>
          <span class="page-info">{{ t('common.pageTotal', { n: total, p: totalPages }) }}</span>
        </div>
        </div>
    </div>

    <!-- 添加网关弹窗 -->
    <Teleport to="body">
      <div v-if="showAddForm" class="modal-mask" @click.self="showAddForm = false">
        <div class="gw-modal">
          <div class="gw-modal-head">{{ t('admin.addGateway') }}</div>
          <div class="gw-modal-body">
            <div class="gw-form-grid">
              <div class="gw-field"><label>ID</label><input v-model="newGateway.id" disabled /></div>
              <div class="gw-field"><label>{{ t('common.name') }} *</label><input v-model="newGateway.name" placeholder="DMZ Gateway" /></div>
              <div class="gw-field gw-full"><label>{{ t('ssh.host') }} *</label><input v-model="newGateway.url" placeholder="wss://gateway.example.com:443" /></div>
              <div class="gw-field gw-full"><label>{{ t('common.remark') }}</label><textarea v-model="newGateway.remark" rows="2"></textarea></div>
            </div>
          </div>
          <div class="gw-modal-foot">
            <button class="text-btn" @click="showAddForm = false">{{ t('common.cancel') }}</button>
            <button class="btn primary" :disabled="adding" @click="doAdd">{{ adding ? t('common.loading') : t('admin.addGateway') }}</button>
          </div>
        </div>
      </div>
    </Teleport>

    <!-- 编辑网关弹窗 -->
    <Teleport to="body">
      <div v-if="showEditForm" class="modal-mask" @click.self="showEditForm = false">
        <div class="gw-modal" style="max-width:560px">
          <div class="gw-modal-head">{{ t('common.edit') }}</div>
          <div class="gw-modal-body">
            <div class="gw-form-grid">
              <div class="gw-field"><label>ID</label><input :value="editGateway.id" disabled /></div>
              <div class="gw-field"><label>{{ t('common.name') }}</label><input v-model="editGateway.name" /></div>
              <div class="gw-field gw-full"><label>{{ t('ssh.host') }}</label><input v-model="editGateway.url" /></div>
              <div class="gw-field gw-full"><label>{{ t('common.remark') }}</label><textarea v-model="editGateway.remark" rows="2"></textarea></div>
            </div>
            <div class="gw-section-divider"></div>
            <div class="gw-section-title">Token</div>
            <div class="gw-token-row">
              <code class="gw-token-display">{{ editGateway.token || '••••••••' }}</code>
              <button class="text-btn sm" @click="copyEditToken">{{ t('common.copy') }}</button>
              <button class="text-btn sm" @click="openRegen(editGateway)">{{ t('admin.regenToken') }}</button>
            </div>
          </div>
          <div class="gw-modal-foot">
            <button class="text-btn" @click="showEditForm = false">{{ t('common.cancel') }}</button>
            <button class="btn primary" @click="doEdit">{{ t('common.save') }}</button>
          </div>
        </div>
      </div>
    </Teleport>

    <!-- Token 显示弹窗 -->
    <Teleport to="body">
      <div v-if="showTokenDialog" class="modal-mask" @click.self="showTokenDialog = false">
        <div class="gw-modal" style="max-width:520px">
          <div class="gw-modal-head">Token</div>
          <div class="gw-modal-body">
            <p style="margin:0 0 8px;color:var(--fg-muted)">{{ t('admin.copiedToClipboard') }}:</p>
            <div style="display:flex;gap:8px;align-items:center">
              <code style="flex:1;padding:10px;background:var(--bg);border:1px solid var(--border);border-radius:6px;word-break:break-all;font-size:13px">{{ newToken }}</code>
              <button class="text-btn sm" @click="copyToken">{{ t('common.copy') }}</button>
            </div>
          </div>
          <div class="gw-modal-foot">
            <button class="btn primary" @click="showTokenDialog = false">{{ t('common.cancel') }}</button>
          </div>
        </div>
      </div>
    </Teleport>

    <!-- 删除确认弹窗 -->
    <Teleport to="body">
      <div v-if="showDeleteDialog" class="modal-mask" @click.self="showDeleteDialog = false">
        <div class="gw-modal" style="max-width:420px">
          <div class="gw-modal-head">{{ t('common.confirmDelete') }}</div>
          <div class="gw-modal-body">
            <p>{{ t('admin.confirmDeleteGateway') }} <strong>{{ deleteTarget?.id }}</strong> 吗？</p>
            <p style="color:var(--danger);font-size:13px">{{ t('common.confirmDeleteDesc') }}</p>
          </div>
          <div class="gw-modal-foot">
            <button class="text-btn" @click="showDeleteDialog = false">{{ t('common.cancel') }}</button>
            <button class="text-btn danger" @click="doDelete">{{ t('common.delete') }}</button>
          </div>
        </div>
      </div>
    </Teleport>

    <!-- Token 重新生成确认弹窗 -->
    <Teleport to="body">
      <div v-if="showRegenConfirm" class="modal-mask" @click.self="showRegenConfirm = false">
        <div class="gw-modal" style="max-width:420px">
          <div class="gw-modal-head">{{ t('admin.regenToken') }}</div>
          <div class="gw-modal-body">
            <p>{{ t('admin.regenTokenConfirm') }} <strong>{{ regenTarget?.id }}</strong> {{ t('admin.regenTokenTip') }}</p>
            <p style="color:var(--danger);font-size:13px">{{ t('admin.regenTokenWarning') }}</p>
          </div>
          <div class="gw-modal-foot">
            <button class="text-btn" @click="showRegenConfirm = false">{{ t('common.cancel') }}</button>
            <button class="btn primary" @click="confirmRegenToken">{{ t('common.confirm') }}</button>
          </div>
        </div>
      </div>
    </Teleport>
  </div>
</template>

<style scoped>
.gw-mgr { padding:20px 28px; }
.gw-header { display:flex; justify-content:space-between; align-items:center; margin-bottom:16px; }
.gw-header h2 { margin:0; font-size:20px; }
.gw-actions { display:flex; gap:8px; }
.gw-table-wrap { overflow-x:auto; }
.gw-table { width:100%; border-collapse:collapse; }
.actions-cell { white-space:nowrap; }
.actions-inner { display:flex; gap:4px; align-items: center; }
.token-btn { background:none; border:none; cursor:pointer; font-size:14px; padding:2px 4px; }
.empty-row { text-align:center; color:var(--fg-muted); padding:32px; }
.owner-badge { font-size:11px; color:var(--fg-muted); margin-left:6px; }
.status-dot { display:inline-block; width:8px; height:8px; border-radius:50%; margin-right:4px; vertical-align:middle; }
.status-dot.online { background:#34d399; }
.status-dot.offline { background:#dc2626; }
.status-dot.disabled { background:#94a3b8; }
/* 按钮 - 使用全局 .btn / .text-btn */
.modal-mask { position:fixed; inset:0; background:rgba(0,0,0,.4); display:flex; align-items:center; justify-content:center; z-index:1000; }
.gw-modal { background:var(--panel); border:1px solid var(--border); border-radius:12px; width:480px; max-width:90vw; animation:pop .15s ease; }
@keyframes pop { from { transform:scale(.95); opacity:0; } }
.gw-modal-head { padding:16px 20px; font-weight:600; border-bottom:1px solid var(--border); }
.gw-modal-body { padding:16px 20px; }
.gw-modal-foot { padding:12px 20px; display:flex; justify-content:flex-end; gap:8px; border-top:1px solid var(--border); }
.gw-form-grid { display:grid; grid-template-columns:1fr 1fr; gap:12px; }
.gw-field { display:flex; flex-direction:column; gap:4px; }
.gw-field.gw-full { grid-column:1/-1; }
.gw-field label { font-size:12px; font-weight:600; color:var(--fg-muted); }
.gw-field input,.gw-field textarea { padding:8px 10px; border:1px solid var(--border); border-radius:6px; background:var(--bg); color:var(--fg); font-size:14px; }
.shared-badge { display:inline-block; padding:2px 8px; border-radius:4px; font-size:12px; font-weight:600; white-space:nowrap; }
.share-tag { display:inline-block; padding:2px 8px; border-radius:4px; font-size:12px; cursor:pointer; border:1px solid transparent; transition:all .15s; }
.share-private { background:#f3f4f6; color:#6b7280; border-color:#d1d5db; }
.share-all { background:#d1fae5; color:#065f46; border-color:#a7f3d0; }
.share-select { background:#dbeafe; color:#1e40af; border-color:#93c5fd; }
.share-tag:hover { opacity:.8; }
.ver-badge { display:inline-block; padding:2px 8px; border-radius:4px; font-size:12px; font-weight:600; background:#e0e7ff; color:#4f46e5; white-space:nowrap; }
.ver-badge.old { background:#fef3c7; color:#d97706; }
.ver-badge.muted { color:var(--fg-muted); background:transparent; }
.upgrade-tip { margin-left:4px; font-size:12px; color:#d97706; cursor:help; }
.gw-section-divider { height:1px; background:var(--border); margin:16px 0; }
.gw-section-title { font-size:12px; font-weight:600; color:var(--fg-muted); text-transform:uppercase; margin-bottom:8px; }
.gw-token-row { display:flex; align-items:center; gap:8px; }
.gw-token-display { flex:1; padding:8px 10px; background:var(--bg); border:1px solid var(--border); border-radius:6px; font-size:12px; word-break:break-all; font-family:monospace; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; max-width:260px; }
</style>
