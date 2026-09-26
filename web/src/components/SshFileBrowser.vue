<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount, inject, computed, nextTick, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api, type SshConn } from '../api'

const { t } = useI18n()
const props = defineProps<{ conn: SshConn; tabId: string; webrtcManager?: any }>()
const toast = inject<any>('toast')

interface RemoteFile {
  name: string; size: number; mtime: string; mode: string; mode_num: number
  is_dir: boolean; is_link: boolean; target: string
}

const currentPath = ref('/')
const files = ref<RemoteFile[]>([])
const selected = ref<string | null>(null)
const resolvedPassword = ref('')
const loading = ref(false)
const sortBy = ref<'name' | 'size' | 'mtime'>('name')
const sortAsc = ref(true)

const breadcrumbs = computed(() => {
  const parts = currentPath.value.split('/').filter(Boolean)
  const result: { name: string; path: string }[] = [{ name: '/', path: '/' }]
  let acc = ''
  for (const p of parts) { acc += '/' + p; result.push({ name: p, path: acc }) }
  return result
})

const showPreview = ref(false)
const previewContent = ref('')
const previewPath = ref('')
const previewIsMd = ref(false)
const renderedMd = ref('')

const showNewDialog = ref(false)
const newType = ref<'file' | 'dir'>('file')
const newName = ref('')
const showRenameDialog = ref(false)
const renameOld = ref('')
const renameNew = ref('')
const showDeleteDialog = ref(false)
const deleteTarget = ref('')
const contextMenu = ref<{ show: boolean; x: number; y: number; file: RemoteFile | null }>({ show: false, x: 0, y: 0, file: null })

function connParams() {
  return {
    id: props.conn.id || '', host: props.conn.host, port: props.conn.port,
    username: props.conn.username, auth_type: props.conn.auth_type,
    password: resolvedPassword.value || props.conn.password || '', key_path: props.conn.key_path || '',
  }
}

function formatSize(bytes: number): string {
  if (bytes === 0) return '-'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(1024))
  return (bytes / Math.pow(1024, i)).toFixed(i > 0 ? 1 : 0) + ' ' + units[i]
}

function fileIcon(f: RemoteFile): string {
  if (f.is_link) return '🔗'
  if (f.is_dir) return '📁'
  const ext = f.name.split('.').pop()?.toLowerCase() || ''
  if (['sh', 'bash', 'zsh'].includes(ext)) return '⚙️'
  if (['py', 'js', 'ts', 'vue', 'java', 'go', 'rs', 'c', 'cpp'].includes(ext)) return '📝'
  if (['jpg', 'jpeg', 'png', 'gif', 'svg', 'webp'].includes(ext)) return '🖼️'
  if (['zip', 'tar', 'gz', '7z', 'rar'].includes(ext)) return '📦'
  if (['md'].includes(ext)) return '📑'
  if (['json', 'yaml', 'yml', 'toml', 'xml'].includes(ext)) return '📋'
  return '📄'
}

function isMarkdown(name: string): boolean {
  return name.toLowerCase().endsWith('.md') || name.toLowerCase().endsWith('.markdown')
}

function decodeBase64Utf8(b64: string): string {
  const binary = atob(b64)
  const bytes = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i)
  return new TextDecoder('utf-8').decode(bytes)
}

async function loadDir(path: string) {
  loading.value = true
  try {
    let result: any
    if (props.webrtcManager) {
      console.log('[SFTP] loadDir via WebRTC:', path, 'ready=', props.webrtcManager.isReady?.())
      result = await props.webrtcManager.sendSftpRequest('list', { path })
    } else {
      result = await api.sftpClientList({ ...connParams(), path })
    }
    files.value = result.items
    currentPath.value = result.path
    selected.value = null
    files.value.sort((a, b) => {
      if (a.is_dir !== b.is_dir) return a.is_dir ? -1 : 1
      return a.name.localeCompare(b.name)
    })
  } catch (e: any) { toast?.error?.(e.message || t('sftpBrowser.loadFailed')) }
  finally { loading.value = false }
}

function navigate(path: string) { loadDir(path) }


function onDoubleClick(f: RemoteFile) {
  if (f.is_dir || f.is_link) {
    const base = currentPath.value.replace(/\/$/, '')
    navigate(f.is_link && f.target ? f.target : base + '/' + f.name)
  } else if (isMarkdown(f.name)) {
    openMdPreview(f)
  } else {
    openTextPreview(f)
  }
}

function onSort(col: 'name' | 'size' | 'mtime') {
  if (sortBy.value === col) sortAsc.value = !sortAsc.value
  else { sortBy.value = col; sortAsc.value = true }
  files.value.sort((a, b) => {
    if (a.is_dir !== b.is_dir) return a.is_dir ? -1 : 1
    let cmp = 0
    if (sortBy.value === 'name') cmp = a.name.localeCompare(b.name)
    else if (sortBy.value === 'size') cmp = a.size - b.size
    else cmp = (a.mtime || '').localeCompare(b.mtime || '')
    return sortAsc.value ? cmp : -cmp
  })
}

function onContextMenu(e: MouseEvent, f: RemoteFile) {
  e.preventDefault()
  contextMenu.value = { show: true, x: e.clientX, y: e.clientY, file: f }
  selected.value = f.name
}

function closeContextMenu() { contextMenu.value.show = false }

async function openTextPreview(f: RemoteFile) {
  const path = currentPath.value.replace(/\/$/, '') + '/' + f.name
  if (f.size > 10 * 1024 * 1024) { toast?.warn?.(t('sftpBrowser.fileTooLarge')); return }
  try {
    let result: any
    if (props.webrtcManager) {
      result = await props.webrtcManager.sendSftpRequest('read', { path })
    } else {
      result = await api.sftpClientRead({ ...connParams(), path })
    }
    previewContent.value = decodeBase64Utf8(result.content)
    previewPath.value = f.name
    previewIsMd.value = false
    renderedMd.value = ''
    showPreview.value = true
  } catch (e: any) { toast?.error?.(e.message || t('sftpBrowser.readFailed')) }
}

async function openMdPreview(f: RemoteFile) {
  const path = currentPath.value.replace(/\/$/, '') + '/' + f.name
  if (f.size > 10 * 1024 * 1024) { toast?.warn?.(t('sftpBrowser.fileTooLarge')); return }
  try {
    let result: any
    if (props.webrtcManager) {
      result = await props.webrtcManager.sendSftpRequest('read', { path })
    } else {
      result = await api.sftpClientRead({ ...connParams(), path })
    }
    previewContent.value = decodeBase64Utf8(result.content)
    previewPath.value = f.name
    previewIsMd.value = true
    showPreview.value = true
    await nextTick()
    renderMarkdown()
  } catch (e: any) { toast?.error?.(e.message || t('sftpBrowser.readFailed')) }
}

function renderMarkdown() {
  try {
    const md = (window as any).markdownit?.({ html: true, linkify: true, typographer: true })
    renderedMd.value = md ? md.render(previewContent.value) : '<pre>' + previewContent.value.replace(/</g, '&lt;') + '</pre>'
  } catch { renderedMd.value = '<pre>' + previewContent.value.replace(/</g, '&lt;') + '</pre>' }
}

function toUtf8B64(str: string): string {
  return btoa(Array.from(new TextEncoder().encode(str), b => String.fromCharCode(b)).join(''))
}

function editorHref(f: RemoteFile): string {
  const path = currentPath.value.replace(/\/$/, '') + '/' + f.name
  const params = toUtf8B64(JSON.stringify({ conn: connParams(), path }))
  return '/editor.html#' + params
}



async function downloadFile(f: RemoteFile) {
  const path = currentPath.value.replace(/\/$/, '') + '/' + f.name
  try {
    const token = localStorage.getItem('token')
    const res = await fetch('/api/sftp-client/download', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        ...(token ? { Authorization: `Bearer ${token}` } : {}),
      },
      body: JSON.stringify({ ...connParams(), path }),
    })
    if (!res.ok) throw new Error(t('sftpBrowser.downloadFailed') || 'download failed')
    const blob = await res.blob()
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = f.name
    a.click()
    URL.revokeObjectURL(url)
  } catch (e: any) {
    toast?.error?.(e.message || t('sftpBrowser.downloadFailed'))
  }
}

function openNewDialog(type: 'file' | 'dir') { newType.value = type; newName.value = ''; showNewDialog.value = true }

async function confirmNew() {
  const name = newName.value.trim()
  if (!name) return
  const fullPath = currentPath.value.replace(/\/$/, '') + '/' + name
  try {
    if (props.webrtcManager) {
      await props.webrtcManager.sendSftpRequest(newType.value === 'dir' ? 'mkdir' : 'write', { path: fullPath, content: '' })
    } else {
      if (newType.value === 'dir') await api.sftpClientMkdir({ ...connParams(), path: fullPath })
      else await api.sftpClientTouch({ ...connParams(), path: fullPath })
    }
    showNewDialog.value = false
    toast?.success?.(newType.value === 'dir' ? t('sftpBrowser.dirCreated') : t('sftpBrowser.fileCreated'))
    loadDir(currentPath.value)
  } catch (e: any) { toast?.error?.(e.message || t('sftpBrowser.createFailed')) }
}

function openRenameDialog(f: RemoteFile) {
  renameOld.value = currentPath.value.replace(/\/$/, '') + '/' + f.name
  renameNew.value = f.name; showRenameDialog.value = true; closeContextMenu()
}

async function confirmRename() {
  const n = renameNew.value.trim()
  if (!n) return
  try {
    if (props.webrtcManager) {
      await props.webrtcManager.sendSftpRequest('rename', { old_path: renameOld.value, new_name: n })
    } else {
      await api.sftpClientRename({ ...connParams(), old_path: renameOld.value, new_name: n })
    }
    showRenameDialog.value = false; toast?.success?.(t('sftpBrowser.renameSuccess')); loadDir(currentPath.value)
  } catch (e: any) { toast?.error?.(e.message || t('sftpBrowser.renameFailed')) }
}

function openDeleteDialog(f: RemoteFile) {
  deleteTarget.value = currentPath.value.replace(/\/$/, '') + '/' + f.name
  showDeleteDialog.value = true; closeContextMenu()
}

async function confirmDelete() {
  try {
    if (props.webrtcManager) {
      await props.webrtcManager.sendSftpRequest('delete', { path: deleteTarget.value })
    } else {
      await api.sftpClientDelete({ ...connParams(), path: deleteTarget.value })
    }
    showDeleteDialog.value = false; toast?.success?.(t('sftpBrowser.deleteSuccess')); loadDir(currentPath.value)
  } catch (e: any) { toast?.error?.(e.message || t('sftpBrowser.deleteFailed')) }
}

function handleUpload() {
  const input = document.createElement('input')
  input.type = 'file'; input.multiple = true
  input.onchange = async () => {
    for (const file of Array.from(input.files || [])) {
      try {
        const fd = new FormData()
        fd.append('file', file)
        Object.entries(connParams()).forEach(([k, v]) => fd.append(k, String(v)))
        fd.append('path', currentPath.value)
        const token = localStorage.getItem('token')
        const res = await fetch('/api/sftp-client/upload', {
          method: 'POST',
          body: fd,
          headers: token ? { Authorization: `Bearer ${token}` } : {},
        })
        if (!res.ok) throw new Error(t('sftpBrowser.uploadFailed'))
      }
      catch (e: any) { toast?.error?.(e.message || t('sftpBrowser.uploadFileFailed', { name: file.name })) }
    }
    toast?.success?.(t('sftpBrowser.uploadComplete')); loadDir(currentPath.value)
  }
  input.click()
}

const _closeCtx = () => { contextMenu.value.show = false }
const _cleanupCtx = () => { document.removeEventListener('click', _closeCtx) }
let initialLoaded = false
let loadTimer: ReturnType<typeof setInterval> | null = null
let loadTimeout: ReturnType<typeof setTimeout> | null = null

function tryLoad() {
  if (initialLoaded) return
  if (!props.webrtcManager) {
    console.log('[SFTP] tryLoad: webrtcManager not assigned yet, waiting')
    return
  }
  if (!props.webrtcManager.isSshConnected()) {
    console.log('[SFTP] tryLoad: SSH not connected yet')
    return
  }
  initialLoaded = true
  clearTimers()
  console.log('[SFTP] tryLoad: starting loadDir, ready=', props.webrtcManager?.isReady?.())
  loadDir('/')
}

function clearTimers() {
  if (loadTimer) { clearInterval(loadTimer); loadTimer = null }
  if (loadTimeout) { clearTimeout(loadTimeout); loadTimeout = null }
}

function startWaitDataChannel() {
  if (!props.webrtcManager) return
  console.log('[SFTP] startWaitDataChannel: polling for SSH connected...')
  clearTimers()
  loadTimer = setInterval(() => {
    if (props.webrtcManager?.isSshConnected()) {
      console.log('[SFTP] SSH connected, triggering load')
      tryLoad()
    }
  }, 500)
  // 网关链路首连可能 >10s（含 SFTP 超时误报），放宽到 30s
  loadTimeout = setTimeout(() => {
    console.warn('[SFTP] wait SSH connected timeout (30s)')
    clearTimers()
    toast?.error?.(t('sftpBrowser.connTimeout'))
  }, 30000)
}

watch(() => props.webrtcManager, (wm) => {
  if (wm) {
    console.log('[SFTP] webrtcManager prop changed:', wm.isSshConnected?.() ? 'SSH already connected' : 'SSH not connected yet')
    if (wm.isSshConnected()) {
      tryLoad()
    } else {
      startWaitDataChannel()
    }
  }
}, { immediate: true })

onMounted(async () => {
  document.addEventListener('click', _closeCtx)
  if (props.conn.id) { try { const r = await api.sshGetPassword(props.conn.id); resolvedPassword.value = r.password || '' } catch {} }
  console.log('[SFTP] onMounted: webrtcManager=', !!props.webrtcManager, 'sshConnected=', props.webrtcManager?.isSshConnected?.())
  if (props.webrtcManager?.isSshConnected?.()) {
    tryLoad()
  }
})
onBeforeUnmount(() => { _cleanupCtx(); clearTimers() })
</script>

<template>
  <div class="sfb" @click="closeContextMenu">
    <!-- Breadcrumb -->
    <div class="sfb-breadcrumb">
      <button class="bc-item" :class="{ active: breadcrumbs.length === 1 }" @click="navigate('/')">⌂ /</button>
      <template v-for="(bc, idx) in breadcrumbs.slice(1)" :key="bc.path">
        <button class="bc-item" :class="{ active: idx === breadcrumbs.length - 2 }" @click="navigate(bc.path)">{{ bc.name }}</button>
      </template>
    </div>

    <!-- Toolbar -->
    <div class="sfb-toolbar">
      <button class="sfb-toolbtn" @click="handleUpload">{{ t('sftpBrowser.upload') }}</button>
      <button class="sfb-toolbtn" @click="openNewDialog('file')">{{ t('sftpBrowser.newFile') }}</button>
      <button class="sfb-toolbtn" @click="openNewDialog('dir')">{{ t('sftpBrowser.newDir') }}</button>
      <button class="sfb-toolbtn" @click="loadDir(currentPath)">{{ t('sftpBrowser.refresh') }}</button>
    </div>

    <!-- File table -->
    <div class="sfb-table-wrap">
      <div class="sfb-loading" v-if="loading">{{ t('common.loading') }}</div>
      <table class="sfb-table" v-else>
        <thead>
          <tr>
            <th class="col-icon"></th>
            <th class="col-name" @click="onSort('name')">{{ t('sftpBrowser.colName') }} <span v-if="sortBy==='name'">{{ sortAsc ? '▲' : '▼' }}</span></th>
            <th class="col-size" @click="onSort('size')">{{ t('sftpBrowser.colSize') }} <span v-if="sortBy==='size'">{{ sortAsc ? '▲' : '▼' }}</span></th>
            <th class="col-mtime" @click="onSort('mtime')">{{ t('sftpBrowser.colModified') }} <span v-if="sortBy==='mtime'">{{ sortAsc ? '▲' : '▼' }}</span></th>
            <th class="col-mode">{{ t('sftpBrowser.colPerm') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="f in files" :key="f.name" class="sfb-row" :class="{ selected: selected === f.name }"
            @click="selected = f.name" @dblclick="onDoubleClick(f)" @contextmenu="onContextMenu($event, f)">
            <td class="col-icon">{{ fileIcon(f) }}</td>
            <td class="col-name">
              <span :class="{ 'dir-name': f.is_dir, 'link-name': f.is_link, 'md-name': isMarkdown(f.name) && !f.is_dir }">{{ f.name }}</span>
              <span v-if="f.is_link && f.target" class="link-target"> → {{ f.target }}</span>
            </td>
            <td class="col-size">{{ f.is_dir ? '-' : formatSize(f.size) }}</td>
            <td class="col-mtime">{{ f.mtime || '-' }}</td>
            <td class="col-mode">{{ f.mode }}</td>
          </tr>
          <tr v-if="!files.length && !loading"><td colspan="5" class="sfb-empty">{{ t('sftpBrowser.emptyDir') }}</td></tr>
        </tbody>
      </table>
    </div>

    <!-- Status bar -->
    <div class="sfb-statusbar">
      <span>{{ props.conn.username }}@{{ props.conn.host }}</span>
      <span class="sb-sep">│</span>
      <span>{{ currentPath }}</span>
      <span class="sb-sep">│</span>
      <span>{{ files.length }} {{ t('sftpBrowser.items') }}</span>
    </div>

    <!-- Context menu -->
    <Teleport to="body">
      <div v-if="contextMenu.show" class="sfb-ctxmenu" :style="{ left: contextMenu.x + 'px', top: contextMenu.y + 'px' }">
        <a class="ctx-item" v-if="contextMenu.file && !contextMenu.file!.is_dir" :href="editorHref(contextMenu.file)" target="_blank" @click.stop>{{ t('sftpBrowser.previewEdit') }}</a>
        <div class="ctx-item" v-if="contextMenu.file && contextMenu.file!.is_dir" @click="contextMenu.file && openMdPreview(contextMenu.file)">{{ t('sftpBrowser.preview') }}</div>
        <div class="ctx-item" @click="contextMenu.file && downloadFile(contextMenu.file)">{{ t('sftpBrowser.download') }}</div>
        <div class="ctx-divider"></div>
        <div class="ctx-item" @click="contextMenu.file && openRenameDialog(contextMenu.file)">{{ t('sftpBrowser.rename') }}</div>
        <div class="ctx-item ctx-danger" @click="contextMenu.file && openDeleteDialog(contextMenu.file)">{{ t('sftpBrowser.deleteAction') }}</div>
        <div class="ctx-divider"></div>
        <div class="ctx-item" @click="navigator.clipboard.writeText(currentPath.replace(/\/$/, '') + '/' + contextMenu.file!.name); toast?.success?.(t('sftpBrowser.pathCopied'))">{{ t('sftpBrowser.copyPath') }}</div>
      </div>
    </Teleport>

    <!-- New dialog -->
    <Teleport to="body">
      <div v-if="showNewDialog" class="sfb-modal-mask" @click.self="showNewDialog = false">
        <div class="sfb-modal">
          <div class="sfb-modal-title">{{ newType === 'dir' ? t('sftpBrowser.newDirTitle') : t('sftpBrowser.newFileTitle') }}</div>
          <input class="sfb-modal-input" v-model="newName" :placeholder="newType === 'dir' ? t('sftpBrowser.dirName') : t('sftpBrowser.fileName')" @keydown.enter="confirmNew" autofocus />
          <div class="sfb-modal-actions">
            <button class="sfb-btn-cancel" @click="showNewDialog = false">{{ t('common.cancel') }}</button>
            <button class="sfb-btn-ok" @click="confirmNew">{{ t('common.confirm') }}</button>
          </div>
        </div>
      </div>
    </Teleport>

    <!-- Rename dialog -->
    <Teleport to="body">
      <div v-if="showRenameDialog" class="sfb-modal-mask" @click.self="showRenameDialog = false">
        <div class="sfb-modal">
          <div class="sfb-modal-title">{{ t('sftpBrowser.rename') }}</div>
          <input class="sfb-modal-input" v-model="renameNew" @keydown.enter="confirmRename" autofocus />
          <div class="sfb-modal-actions">
            <button class="sfb-btn-cancel" @click="showRenameDialog = false">{{ t('common.cancel') }}</button>
            <button class="sfb-btn-ok" @click="confirmRename">{{ t('common.confirm') }}</button>
          </div>
        </div>
      </div>
    </Teleport>

    <!-- Delete dialog -->
    <Teleport to="body">
      <div v-if="showDeleteDialog" class="sfb-modal-mask" @click.self="showDeleteDialog = false">
        <div class="sfb-modal">
          <div class="sfb-modal-title">{{ t('sftpBrowser.confirmDelete') }}</div>
          <div class="sfb-modal-text">{{ deleteTarget }}</div>
          <div class="sfb-modal-actions">
            <button class="sfb-btn-cancel" @click="showDeleteDialog = false">{{ t('common.cancel') }}</button>
            <button class="sfb-btn-danger" @click="confirmDelete">{{ t('common.delete') }}</button>
          </div>
        </div>
      </div>
    </Teleport>

    <!-- Preview modal -->
    <Teleport to="body">
      <div v-if="showPreview" class="sfb-modal-mask" @click.self="showPreview = false">
        <div class="sfb-modal sfb-preview-modal">
          <div class="sfb-modal-header">
            <span class="sfb-modal-title">{{ previewPath }}</span>
            <div class="sfb-modal-header-actions">
              <a v-if="previewIsMd" class="sfb-toolbtn" :href="editorHref({ name: previewPath, size: 0, mtime: '', mode: '', mode_num: 0, is_dir: false, is_link: false, target: '' })" target="_blank">{{ t('sftpBrowser.editAction') }}</a>
              <button class="sfb-btn-cancel" @click="showPreview = false">✕</button>
            </div>
          </div>
          <div v-if="previewIsMd" id="md-preview-render" class="sfb-md-preview" v-html="renderedMd"></div>
          <pre v-else class="sfb-preview-content">{{ previewContent }}</pre>
        </div>
      </div>
    </Teleport>


  </div>
</template>

<style scoped>
.sfb { display:flex; flex-direction:column; height:100%; background:var(--bg); color:var(--fg); font-size:13px; }

.sfb-breadcrumb { display:flex; align-items:center; gap:6px; padding:6px 12px; border-bottom:1px solid var(--border); background:var(--panel); flex-wrap:wrap; }
.bc-item { background:var(--bg); border:1px solid var(--border); color:var(--accent); cursor:pointer; padding:4px 10px; border-radius:6px; font-size:12px; font-family:monospace; line-height:1.5; transition:background .12s, border-color .12s; }
.bc-item:hover { background:var(--accent-soft); border-color:var(--accent); }
.bc-item.active { color:var(--fg); font-weight:600; cursor:default; background:var(--accent-soft); border-color:var(--accent); }
.bc-item.active:hover { background:var(--accent-soft); }

.sfb-toolbar { display:flex; gap:6px; padding:6px 12px; border-bottom:1px solid var(--border); background:var(--panel); }
.sfb-toolbtn { background:var(--bg); border:1px solid var(--border); color:var(--fg-2); padding:4px 10px; border-radius:4px; cursor:pointer; font-size:13px; text-decoration:none; display:inline-flex; align-items:center; }
.sfb-toolbtn:hover { background:var(--panel-2); }

.sfb-table-wrap { flex:1; overflow:auto; }
.sfb-table { width:100%; border-collapse:collapse; }
.sfb-table th { position:sticky; top:0; background:var(--panel); padding:6px 12px; text-align:left; font-weight:600; border-bottom:1px solid var(--border); cursor:pointer; user-select:none; white-space:nowrap; z-index:1; }
.sfb-table th:hover { background:var(--panel-2); }
.sfb-table td { padding:5px 12px; border-bottom:1px solid var(--border); }
.sfb-row { cursor:pointer; }
.sfb-row:hover { background:var(--panel-2); }
.sfb-row.selected { background:var(--accent-soft); }
.col-icon { width:40px; text-align:center; }
.col-name { min-width:200px; }
.col-size { width:100px; text-align:right; }
.col-mtime { width:160px; }
.col-mode { width:100px; font-family:monospace; font-size:13px; }
.dir-name { color:var(--accent); font-weight:600; }
.link-name { color:#a78bfa; }
.md-name { color:#34d399; }
.link-target { color:var(--muted); font-size:13px; }
.sfb-loading, .sfb-empty { text-align:center; padding:40px; color:var(--muted); }

.sfb-statusbar { display:flex; align-items:center; gap:8px; padding:4px 12px; border-top:1px solid var(--border); background:var(--panel); font-size:13px; color:var(--muted); }
.sb-sep { opacity:.4; }

.sfb-ctxmenu { position:fixed; z-index:9999; background:var(--panel); border:1px solid var(--border); border-radius:6px; padding:4px 0; min-width:160px; box-shadow:0 4px 12px rgba(0,0,0,.3); }
.ctx-item { padding:6px 14px; cursor:pointer; white-space:nowrap; color:var(--fg-2); text-decoration:none; display:block; }
.ctx-item:hover { background:var(--panel-2); color:var(--fg); }
.ctx-danger { color:#f87171; }
.ctx-danger:hover { background:rgba(248,113,113,.1); }
.ctx-divider { height:1px; background:var(--border); margin:4px 0; }

.sfb-modal-mask { position:fixed; inset:0; z-index:9998; background:rgba(0,0,0,.5); display:flex; align-items:center; justify-content:center; }
.sfb-modal { background:var(--panel); border:1px solid var(--border); border-radius:8px; padding:20px; min-width:340px; max-width:500px; }
.sfb-modal-header { display:flex; justify-content:space-between; align-items:center; margin-bottom:12px; }
.sfb-modal-header-actions { display:flex; gap:6px; }
.sfb-modal-title { font-size:15px; font-weight:600; }
.sfb-modal-text { font-size:13px; color:var(--muted); margin-bottom:12px; word-break:break-all; }
.sfb-modal-input { width:100%; background:var(--bg); border:1px solid var(--border); color:var(--fg); padding:8px 10px; border-radius:4px; font-size:14px; outline:none; margin-bottom:12px; box-sizing:border-box; }
.sfb-modal-input:focus { border-color:var(--accent); }
.sfb-modal-actions { display:flex; justify-content:flex-end; gap:8px; }
.sfb-btn-cancel { background:var(--bg); border:1px solid var(--border); color:var(--fg-2); padding:6px 14px; border-radius:4px; cursor:pointer; }
.sfb-btn-cancel:hover { background:var(--panel-2); }
.sfb-btn-ok { background:var(--accent); border:none; color:#fff; padding:6px 14px; border-radius:4px; cursor:pointer; }
.sfb-btn-ok:hover { opacity:.9; }
.sfb-btn-danger { background:#dc2626; border:none; color:#fff; padding:6px 14px; border-radius:4px; cursor:pointer; }
.sfb-btn { background:var(--bg); border:1px solid var(--border); color:var(--fg-2); padding:4px 8px; border-radius:4px; cursor:pointer; font-size:16px; line-height:1; }
.sfb-btn:hover { background:var(--panel-2); }
.sfb-btn:disabled { opacity:.4; cursor:not-allowed; }

.sfb-preview-modal { max-width:800px; max-height:85vh; display:flex; flex-direction:column; }
.sfb-preview-content { flex:1; overflow:auto; background:var(--bg); padding:12px; border-radius:4px; font-family:monospace; font-size:13px; line-height:1.5; max-height:60vh; white-space:pre-wrap; word-break:break-all; margin:0; color:var(--fg); }

.sfb-md-preview { flex:1; overflow:auto; padding:16px 20px; max-height:60vh; line-height:1.7; color:var(--fg); }
.sfb-md-preview :deep(h1), .sfb-md-preview :deep(h2), .sfb-md-preview :deep(h3) { margin:16px 0 8px; }
.sfb-md-preview :deep(p) { margin:8px 0; }
.sfb-md-preview :deep(code) { background:var(--bg); padding:2px 6px; border-radius:3px; font-size:13px; }
.sfb-md-preview :deep(pre) { background:var(--bg); padding:12px; border-radius:6px; overflow-x:auto; margin:8px 0; }
.sfb-md-preview :deep(pre code) { padding:0; background:none; }
.sfb-md-preview :deep(blockquote) { border-left:3px solid var(--accent); padding-left:12px; color:var(--muted); margin:8px 0; }
.sfb-md-preview :deep(table) { border-collapse:collapse; margin:8px 0; }
.sfb-md-preview :deep(th), .sfb-md-preview :deep(td) { border:1px solid var(--border); padding:6px 10px; }
.sfb-md-preview :deep(a) { color:var(--accent); }

</style>
