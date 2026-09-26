<script setup lang="ts">
import { ref, computed, onMounted, inject } from 'vue'
import { useI18n } from 'vue-i18n'
import { api, type LinkGroup, type LinkItem } from '../api'

const { t } = useI18n()
const toast = inject<any>('toast')

const data = ref<LinkGroup[]>([])
const activeGroupId = ref('')
const showModal = ref(false)
const editing = ref<LinkItem>({ title: '', url: '', description: '', group_id: 'ungrouped' })
const isNew = ref(false)
const showNewGroup = ref(false)
const newGroupName = ref('')
const showRenameGroup = ref(false)
const renameGroupId = ref('')
const renameGroupName = ref('')
const showDeleteConfirm = ref(false)
const deleteTargetId = ref('')
const deleteTargetName = ref('')
const deleteType = ref<'link' | 'group'>('link')

const allLinks = computed(() => data.value.flatMap((g) => g.links))
const activeGroup = computed(() => data.value.find((g) => g.id === activeGroupId.value))
const displayLinks = computed(() =>
  activeGroupId.value === 'all' ? allLinks.value : activeGroup.value?.links ?? []
)

onMounted(loadData)

async function loadData() {
  try {
    const r = await api.linksList()
    data.value = r.groups || []
    if (!activeGroupId.value && data.value.length) {
      activeGroupId.value = 'all'
    }
  } catch { data.value = [] }
}

function openNew() {
  editing.value = { title: '', url: 'https://', description: '', group_id: activeGroupId.value === 'all' ? (data.value[0]?.id || 'ungrouped') : activeGroupId.value }
  isNew.value = true
  showModal.value = true
}

function openEdit(item: LinkItem) {
  editing.value = { ...item }
  isNew.value = false
  showModal.value = true
}

async function save() {
  if (!editing.value.title.trim() || !editing.value.url.trim()) {
    toast?.error(t('links.titleAndUrlRequired'))
    return
  }
  try {
    if (isNew.value) {
      await api.linksAdd(editing.value)
      toast?.success(t('links.addSuccess'))
    } else {
      await api.linksUpdate(editing.value)
      toast?.success(t('links.updateSuccess'))
    }
    showModal.value = false
    await loadData()
  } catch (e: any) {
    toast?.error(e.message)
  }
}

function confirmRemove(id: string, name: string) {
  deleteTargetId.value = id
  deleteTargetName.value = name
  deleteType.value = 'link'
  showDeleteConfirm.value = true
}

async function doRemove() {
  try {
    if (deleteType.value === 'link') {
      await api.linksDelete(deleteTargetId.value)
      toast?.success(t('links.deleteSuccess'))
    } else {
      await api.linksGroupDelete(deleteTargetId.value)
      if (activeGroupId.value === deleteTargetId.value) activeGroupId.value = 'all'
      toast?.success(t('links.deleteGroupSuccess'))
    }
    showDeleteConfirm.value = false
    await loadData()
  } catch (e: any) {
    toast?.error(e.message)
  }
}

function openNewGroup() {
  newGroupName.value = ''
  showNewGroup.value = true
}

async function doNewGroup() {
  const name = newGroupName.value.trim()
  if (!name) return
  try {
    const r = await api.linksGroupAdd(name)
    showNewGroup.value = false
    await loadData()
    activeGroupId.value = r.id
    toast?.success(t('links.newGroup'))
  } catch (e: any) {
    toast?.error(e.message)
  }
}

function openRenameGroup(g: LinkGroup) {
  renameGroupId.value = g.id
  renameGroupName.value = g.name
  showRenameGroup.value = true
}

async function doRenameGroup() {
  const name = renameGroupName.value.trim()
  if (!name) return
  try {
    await api.linksGroupRename(renameGroupId.value, name)
    showRenameGroup.value = false
    await loadData()
    toast?.success(t('links.renameGroup'))
  } catch (e: any) {
    toast?.error(e.message)
  }
}

function confirmDeleteGroup(id: string, name: string) {
  deleteTargetId.value = id
  deleteTargetName.value = name
  deleteType.value = 'group'
  showDeleteConfirm.value = true
}

function host(url: string) {
  try { return new URL(url).hostname } catch { return url }
}
</script>

<template>
  <div class="lm-wrap">
    <div class="breadcrumb" style="position:absolute;top:0;left:240px;right:0;padding:8px 16px;z-index:1;font-size:13px;">网址管理</div>
    <!-- 左侧分组 -->
    <aside class="lm-sidebar">
      <div class="lm-sidebar-head">
        <span class="sidebar-label">{{ t('links.group') }}</span>
        <button class="icon-btn sm" @click="openNewGroup" :title="t('links.newGroup')">＋</button>
      </div>
      <div class="lm-groups">
        <button class="lm-group-item" :class="{ active: activeGroupId === 'all' }" @click="activeGroupId = 'all'">
          <span class="gm-icon">📋</span>
          <span class="gm-name">{{ t('links.allLinks') }}</span>
          <span class="gm-count">{{ allLinks.length }}</span>
        </button>
        <div v-for="g in data" :key="g.id" class="lm-group-row">
          <button class="lm-group-item" :class="{ active: activeGroupId === g.id }" @click="activeGroupId = g.id">
            <span class="gm-icon">📁</span>
            <span class="gm-name">{{ g.name }}</span>
            <span class="gm-count">{{ g.links.length }}</span>
          </button>
          <div class="gm-actions">
            <button class="gm-act" @click.stop="openRenameGroup(g)" :title="t('links.renameGroup')">✎</button>
            <button class="gm-act danger" @click.stop="confirmDeleteGroup(g.id, g.name)" :title="t('common.delete')">✕</button>
          </div>
        </div>
      </div>
    </aside>

    <!-- 右侧链接 -->
    <div class="lm-main">
      <div class="lm-head">
        <div class="lm-head-left">
          <h3>{{ activeGroup?.name || t('links.allLinks') }}</h3>
          <span class="lm-count">{{ displayLinks.length }} 项</span>
        </div>
        <button class="add-btn" @click="openNew">＋ {{ t('links.addLink') }}</button>
      </div>

      <div class="lm-card-grid">
        <div v-for="item in displayLinks" :key="item.id" class="lm-card" @click="openEdit(item)">
          <div class="lc-head">
            <span class="lc-favicon">🔗</span>
            <span class="lc-host">{{ host(item.url) }}</span>
            <button class="lc-del" @click.stop="confirmRemove(item.id!, item.title)" :title="t('common.delete')">✕</button>
          </div>
          <div class="lc-title">{{ item.title }}</div>
          <div class="lc-desc">{{ item.description || '暂无描述' }}</div>
          <a class="lc-url" :href="item.url" target="_blank" rel="noopener" @click.stop>访问 →</a>
        </div>
        <div v-if="!displayLinks.length" class="lm-empty">
          <div class="empty-icon">🔗</div>
          <div class="empty-text">暂无链接</div>
        </div>
      </div>
    </div>

    <!-- 编辑弹窗 -->
    <Teleport to="body">
      <div v-if="showModal" class="modal-mask" @click.self="showModal = false">
        <div class="modal-box">
          <h3>{{ isNew ? t('links.addLink') : t('links.editLink') }}</h3>
          <div class="form-row">
            <label>{{ t('links.linkTitle') }} *</label>
            <input v-model="editing.title" placeholder="例：Vue3 文档" />
          </div>
          <div class="form-row">
            <label>{{ t('links.url') }} *</label>
            <input v-model="editing.url" placeholder="https://..." />
          </div>
          <div class="form-row">
            <label>{{ t('links.linkDesc') }}</label>
            <textarea v-model="editing.description" rows="2" placeholder="简要说明"></textarea>
          </div>
          <div class="form-row">
            <label>{{ t('links.group') }}</label>
            <select v-model="editing.group_id">
              <option v-for="g in data" :key="g.id" :value="g.id">{{ g.name }}</option>
            </select>
          </div>
          <div class="modal-actions">
            <button class="tool-btn" @click="showModal = false">{{ t('common.cancel') }}</button>
            <button v-if="!isNew" class="tool-btn danger" @click="editing.id && confirmRemove(editing.id, editing.title); showModal = false">{{ t('common.delete') }}</button>
            <button class="tool-btn primary" @click="save">{{ isNew ? t('links.addLink') : t('common.save') }}</button>
          </div>
        </div>
      </div>
    </Teleport>

    <!-- 新建分组弹窗 -->
    <Teleport to="body">
      <div v-if="showNewGroup" class="modal-mask" @click.self="showNewGroup = false">
        <div class="modal-box">
          <h3>{{ t('links.newGroup') }}</h3>
          <div class="form-row">
            <label>{{ t('links.groupName') }}</label>
            <input v-model="newGroupName" @keydown.enter="doNewGroup" autofocus />
          </div>
          <div class="modal-actions">
            <button class="tool-btn" @click="showNewGroup = false">{{ t('common.cancel') }}</button>
            <button class="tool-btn primary" @click="doNewGroup">创建</button>
          </div>
        </div>
      </div>
    </Teleport>

    <!-- 重命名分组弹窗 -->
    <Teleport to="body">
      <div v-if="showRenameGroup" class="modal-mask" @click.self="showRenameGroup = false">
        <div class="modal-box">
          <h3>{{ t('links.renameGroup') }}</h3>
          <div class="form-row">
            <label>新名称</label>
            <input v-model="renameGroupName" @keydown.enter="doRenameGroup" autofocus />
          </div>
          <div class="modal-actions">
            <button class="tool-btn" @click="showRenameGroup = false">{{ t('common.cancel') }}</button>
            <button class="tool-btn primary" @click="doRenameGroup">{{ t('common.confirm') }}</button>
          </div>
        </div>
      </div>
    </Teleport>

    <!-- 删除确认弹窗 -->
    <Teleport to="body">
      <div v-if="showDeleteConfirm" class="modal-mask" @click.self="showDeleteConfirm = false">
        <div class="modal-box modal-danger">
          <h3>⚠️ {{ t('common.confirmDelete') }}</h3>
          <p class="modal-desc">
            确定要删除「<b>{{ deleteTargetName }}</b>」吗？
            <template v-if="deleteType === 'group'"><br/>该分组下的所有链接也将被删除。</template>
          </p>
          <div class="modal-actions">
            <button class="tool-btn" @click="showDeleteConfirm = false">{{ t('common.cancel') }}</button>
            <button class="tool-btn danger" @click="doRemove">{{ t('common.confirmDelete') }}</button>
          </div>
        </div>
      </div>
    </Teleport>
  </div>
</template>

<style scoped>
.lm-wrap { display:flex; height:100%; overflow:hidden; }

/* ── 侧栏 ── */
.lm-sidebar { width:240px; flex-shrink:0; background:var(--panel); border-right:1px solid var(--border); display:flex; flex-direction:column; }
.lm-sidebar-head { display:flex; align-items:center; justify-content:space-between; padding:12px 14px; border-bottom:1px solid var(--border); }
.sidebar-label { font-size:12px; color:var(--muted); text-transform:uppercase; letter-spacing:.05em; font-weight:600; }
.icon-btn.sm { min-width:28px; min-height:28px; font-size:16px; border-radius:6px; }
.icon-btn.sm:hover { background:var(--accent-soft); color:var(--accent); }
.lm-groups { flex:1; overflow-y:auto; padding:8px; }
.lm-group-row { position:relative; display:flex; align-items:center; }
.lm-group-item { flex:1; display:flex; align-items:center; gap:8px; padding:8px 10px; border-radius:8px; cursor:pointer; font-size:13px; color:var(--fg-2); min-height:38px; }
.lm-group-item:hover { background:var(--panel-2); color:var(--fg); }
.lm-group-item.active { background:var(--accent-soft); color:var(--accent); font-weight:600; }
.gm-icon { font-size:15px; flex-shrink:0; }
.gm-name { overflow:hidden; text-overflow:ellipsis; white-space:nowrap; flex:0 1 auto; }
.gm-count { font-size:11px; color:var(--muted); background:var(--panel-2); padding:2px 8px; border-radius:10px; }
.gm-actions { position:absolute; right:8px; top:50%; transform:translateY(-50%); display:flex; gap:2px; opacity:0; transition:opacity .15s; }
.lm-group-row:hover .gm-actions { opacity:1; }
.gm-act { min-width:22px; min-height:22px; font-size:11px; border-radius:4px; color:var(--muted); }
.gm-act:hover { background:var(--panel-2); color:var(--fg); }
.gm-act.danger:hover { color:#dc2626; }

/* ── 主内容区 ── */
.lm-main { flex:1; display:flex; flex-direction:column; min-width:0; padding:24px 32px; overflow-y:auto; }
.lm-head { display:flex; align-items:center; justify-content:space-between; margin-bottom:24px; flex-wrap:wrap; gap:12px; }
.lm-head-left { display:flex; align-items:center; gap:12px; }
.lm-head h3 { margin:0; font-size:20px; }
.lm-count { font-size:13px; color:var(--muted); }
.add-btn { height:36px; padding:0 16px; background:var(--accent); color:var(--accent-fg); border:none; border-radius:8px; font-size:13px; font-weight:600; }
.add-btn:hover { opacity:.9; }

/* ── 卡片网格 ── */
.lm-card-grid { display:grid; grid-template-columns:repeat(3, 1fr); gap:20px; }
.lm-card { background:var(--panel); border:1px solid var(--border); border-radius:12px; padding:20px; cursor:pointer; transition:all .2s; display:flex; flex-direction:column; gap:10px; box-shadow:var(--shadow); }
.lm-card:hover { border-color:var(--accent); box-shadow:var(--shadow-lg); transform:translateY(-2px); }
.lc-head { display:flex; align-items:center; gap:6px; }
.lc-favicon { font-size:16px; }
.lc-host { font-size:12px; color:var(--muted); overflow:hidden; text-overflow:ellipsis; white-space:nowrap; flex:0 1 auto; }
.lc-del { min-width:22px; min-height:22px; font-size:11px; border-radius:4px; color:var(--muted); opacity:0; transition:opacity .15s; }
.lm-card:hover .lc-del { opacity:1; }
.lc-del:hover { color:#dc2626; background:#dc262615; }
.lc-title { font-size:15px; font-weight:600; color:var(--fg); }
.lc-desc { font-size:13px; color:var(--fg-2); line-height:1.5; display:-webkit-box; -webkit-line-clamp:2; -webkit-box-orient:vertical; overflow:hidden; }
.lc-url { font-size:12px; color:var(--accent); text-decoration:none; margin-top:auto; padding-top:4px; }
.lc-url:hover { text-decoration:underline; }
.lm-empty { grid-column:1/-1; text-align:center; padding:60px 20px; }
.lm-empty .empty-icon { font-size:40px; margin-bottom:12px; opacity:.4; }
.lm-empty .empty-text { font-size:14px; color:var(--muted); }

.modal-mask { position:fixed; inset:0; background:rgba(0,0,0,.45); z-index:200; display:flex; align-items:center; justify-content:center; }
.modal-box { background:var(--panel); border-radius:12px; padding:24px; width:420px; max-width:92vw; max-height:85vh; overflow-y:auto; box-shadow:0 20px 60px rgba(0,0,0,.3); }
.modal-box.modal-danger { border:1px solid #fecaca; }
.modal-box h3 { margin:0 0 12px; font-size:17px; }
.modal-desc { font-size:14px; color:var(--fg-2); line-height:1.6; margin:0; }
.modal-desc b { color:var(--fg); }
.form-row { margin-bottom:14px; }
.form-row label { display:block; font-size:13px; color:var(--muted); margin-bottom:4px; }
.form-row input, .form-row textarea, .form-row select { width:100%; padding:8px 10px; border:1px solid var(--border); border-radius:6px; background:var(--panel-2); color:var(--fg); font-size:14px; font-family:inherit; resize:vertical; }
.form-row input:focus, .form-row textarea:focus, .form-row select:focus { outline:none; border-color:var(--accent); box-shadow:0 0 0 2px var(--accent-soft); }
.modal-actions { display:flex; justify-content:flex-end; gap:8px; margin-top:20px; }
.tool-btn { min-height:34px; padding:0 14px; border:1px solid var(--border); border-radius:6px; background:var(--panel); color:var(--fg-2); font-size:13px; }
.tool-btn:hover { border-color:var(--accent); color:var(--accent); }
.tool-btn.primary { background:var(--accent); color:var(--accent-fg); border-color:var(--accent); }
.tool-btn.primary:hover { opacity:.9; }
.tool-btn.danger { border-color:#dc2626; color:#dc2626; }
.tool-btn.danger:hover { background:#dc2626; color:#fff; }

@keyframes fadeIn { from{opacity:0} to{opacity:1} }

@media(max-width:1200px){ .lm-card-grid{grid-template-columns:repeat(2, 1fr)} }
@media(max-width:768px){ .lm-card-grid{grid-template-columns:1fr} .lm-sidebar{width:200px} .lm-main{padding:16px 14px} }
</style>
