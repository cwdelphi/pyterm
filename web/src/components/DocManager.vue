<script setup lang="ts">
import { ref, watch, onMounted, onBeforeUnmount, nextTick, inject, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { api, findNode, collectFiles, type TreeNode } from '../api'

const { t } = useI18n()
import { renderDoc, renderMermaid, type TocItem } from '../markdown'
import { EditorView, keymap, lineNumbers, highlightActiveLine, highlightActiveLineGutter } from '@codemirror/view'
import { EditorState } from '@codemirror/state'
import { markdown } from '@codemirror/lang-markdown'
import { defaultKeymap, history, historyKeymap } from '@codemirror/commands'
import { syntaxHighlighting, defaultHighlightStyle, bracketMatching } from '@codemirror/language'
import { searchKeymap, search } from '@codemirror/search'
import FileTreeNode from './FileTreeNode.vue'
import DirSelectNode from './DirSelectNode.vue'

const props = defineProps<{ dark: boolean }>()
const toast = inject<any>('toast')

const fileTree = ref<TreeNode[]>([])
const expanded = ref<Record<string, boolean>>({})
interface DocTab {
  id: string
  path: string
  content: string
  saved: boolean
  viewMode: 'browse' | 'edit' | 'preview' | 'split'
  loading: boolean
  errMsg: string
}

const tabs = ref<DocTab[]>([])
const activeTabId = ref('')

const currentFile = computed(() => activeTab.value?.path || '')
const viewMode = computed(() => activeTab.value?.viewMode || 'browse')
const content = computed(() => activeTab.value?.content || '')
const saved = computed(() => activeTab.value?.saved ?? true)
const loading = computed(() => activeTab.value?.loading ?? false)
const errMsg = computed(() => activeTab.value?.errMsg || '')

const saving = ref(false)
const activeTab = computed(() => tabs.value.find(t => t.id === activeTabId.value))

function setActiveTab(id: string) {
  activeTabId.value = id
}

function updateActiveTab(patch: Partial<DocTab>) {
  const tab = tabs.value.find(t => t.id === activeTabId.value)
  if (tab) Object.assign(tab, patch)
}

watch(activeTabId, async (newId, oldId) => {
  if (!newId || newId === oldId) return
  const tab = tabs.value.find(t => t.id === newId)
  if (!tab) return
  if (cm) { cm.destroy(); cm = null }
  await nextTick()
  if (tab.viewMode === 'edit' || tab.viewMode === 'split') {
    createEditor(props.dark)
  }
  await doBrowseRender()
})

const previewRef = ref<HTMLElement>()
const previewPaneRef = ref<HTMLElement>()
const editorRef = ref<HTMLDivElement>()
let cm: EditorView | null = null

// Sidebar collapse
const sidebarCollapsed = ref(false)
const sidebarWidth = ref(260)
const dragging = ref(false)

function onSidebarDragStart(e: MouseEvent) {
  dragging.value = true
  const startX = e.clientX
  const startW = sidebarWidth.value
  const onMove = (ev: MouseEvent) => {
    sidebarWidth.value = Math.max(180, Math.min(420, startW + ev.clientX - startX))
  }
  const onUp = () => {
    dragging.value = false
    document.removeEventListener('mousemove', onMove)
    document.removeEventListener('mouseup', onUp)
    localStorage.setItem('sidebarWidth', String(sidebarWidth.value))
  }
  document.addEventListener('mousemove', onMove)
  document.addEventListener('mouseup', onUp)
}

// File tree search
const treeFilter = ref('')
const filteredTree = computed(() => {
  if (!treeFilter.value) return fileTree.value
  return filterTree(fileTree.value, treeFilter.value.toLowerCase())
})

function filterTree(nodes: TreeNode[], q: string): TreeNode[] {
  const out: TreeNode[] = []
  for (const n of nodes) {
    if (n.type === 'dir') {
      const children = filterTree(n.children ?? [], q)
      if (children.length || n.name.toLowerCase().includes(q)) {
        out.push({ ...n, children })
      }
    } else if (n.name.toLowerCase().includes(q)) {
      out.push(n)
    }
  }
  return out
}

watch(treeFilter, (q) => {
  if (q) {
    fileTree.value.forEach(n => { if (n.type === 'dir') expanded.value[n.path] = true })
  }
})

// TOC sidebar
const tocItems = ref<TocItem[]>([])
const showToc = ref(false)
const activeTocId = ref('')

// Recent access
const recentFiles = ref<string[]>([])
const MAX_RECENT = 5

function loadRecent() {
  try { recentFiles.value = JSON.parse(localStorage.getItem('recentDocs') || '[]') } catch { recentFiles.value = [] }
}
function saveRecent(path: string) {
  recentFiles.value = [path, ...recentFiles.value.filter(p => p !== path)].slice(0, MAX_RECENT)
  localStorage.setItem('recentDocs', JSON.stringify(recentFiles.value))
}

const lightTheme = EditorView.theme({
  '&': { backgroundColor: '#ffffff', color: '#1e293b' },
  '.cm-content': { fontFamily: '"SFMono-Regular", Consolas, "Liberation Mono", Menlo, monospace', fontSize: '14px', lineHeight: '1.6', padding: '12px 0' },
  '.cm-gutters': { backgroundColor: '#f8fafc', color: '#94a3b8', border: 'none', borderRight: '1px solid #e2e8f0' },
  '.cm-activeLineGutter': { backgroundColor: '#f1f5f9' },
  '.cm-activeLine': { backgroundColor: '#f1f5f922' },
  '.cm-selectionBackground': { backgroundColor: '#bfdbfe !important' },
  '.cm-cursor': { borderLeftColor: '#2563eb' },
  '&.cm-focused .cm-selectionBackground': { backgroundColor: '#93c5fd !important' },
  '.cm-matchingBracket': { backgroundColor: '#bbf7d0', outline: 'none' }
})

const darkTheme = EditorView.theme({
  '&': { backgroundColor: '#0f172a', color: '#e2e8f0' },
  '.cm-content': { fontFamily: '"SFMono-Regular", Consolas, "Liberation Mono", Menlo, monospace', fontSize: '14px', lineHeight: '1.6', padding: '12px 0' },
  '.cm-gutters': { backgroundColor: '#1e293b', color: '#64748b', border: 'none', borderRight: '1px solid #334155' },
  '.cm-activeLineGutter': { backgroundColor: '#16202f' },
  '.cm-activeLine': { backgroundColor: '#16202f44' },
  '.cm-selectionBackground': { backgroundColor: '#1e40af55 !important' },
  '.cm-cursor': { borderLeftColor: '#60a5fa' },
  '&.cm-focused .cm-selectionBackground': { backgroundColor: '#1d4ed855 !important' },
  '.cm-matchingBracket': { backgroundColor: '#065f46', outline: 'none' }
})

function createEditor(dark: boolean) {
  if (cm) { cm.destroy(); cm = null }
  if (!editorRef.value) return
  cm = new EditorView({
    state: EditorState.create({
      doc: activeTab.value?.content || '',
      extensions: [
        lineNumbers(), highlightActiveLineGutter(), history(),
        bracketMatching(), highlightActiveLine(),
        syntaxHighlighting(defaultHighlightStyle), markdown(),
        search(),
        keymap.of([...defaultKeymap, ...historyKeymap, ...searchKeymap, ...formatKeymap]),
        dark ? darkTheme : lightTheme,
        EditorView.updateListener.of((u) => {
          if (u.docChanged) {
            updateActiveTab({ content: u.state.doc.toString(), saved: false })
            debouncedPreview()
          }
        }),
        EditorView.lineWrapping
      ]
    }),
    parent: editorRef.value
  })
}

watch(() => props.dark, (d) => { if (activeTab.value && (activeTab.value.viewMode === 'edit' || activeTab.value.viewMode === 'split')) createEditor(d) })

let previewTimer: ReturnType<typeof setTimeout> | null = null
function debouncedPreview() {
  if (previewTimer) clearTimeout(previewTimer)
  previewTimer = setTimeout(doPreview, 200)
}

async function doPreview() {
  const target = previewPaneRef.value || previewRef.value
  if (!target) return
  if (!activeTab.value || (activeTab.value.viewMode !== 'edit' && activeTab.value.viewMode !== 'split' && activeTab.value.viewMode !== 'preview')) return
  const { html, toc } = renderDoc(activeTab.value?.content || '')
  tocItems.value = toc
  target.innerHTML = html
  await nextTick()
  await renderMermaid(target, props.dark)
}

async function doBrowseRender() {
  if (!previewRef.value || !activeTab.value || activeTab.value.viewMode !== 'browse') return
  const { html, toc } = renderDoc(activeTab.value?.content || '')
  tocItems.value = toc
  previewRef.value.innerHTML = html
  await nextTick()
  await renderMermaid(previewRef.value, props.dark)
}

onMounted(async () => {
  loadRecent()
  fileTree.value = await api.filesTree()
  try { sidebarWidth.value = Number(localStorage.getItem('sidebarWidth')) || 260 } catch {}
  window.addEventListener('open-doc', ((e: CustomEvent) => { openFile(e.detail) }) as EventListener)
})

onBeforeUnmount(() => { if (cm) { cm.destroy(); cm = null } })

function toggleDir(path: string) {
  expanded.value[path] = !expanded.value[path]
}

async function clickNode(node: TreeNode) {
  if (node.type === 'dir') {
    toggleDir(node.path)
  } else {
    await openFile(node.path)
  }
}

function getTabId(path: string): string {
  return path
}

async function openFile(path: string) {
  // If tab already exists, just switch to it
  const existing = tabs.value.find(t => t.path === path)
  if (existing) {
    activeTabId.value = existing.id
    return
  }

  // Create new tab
  const id = getTabId(path)
  const newTab: DocTab = {
    id,
    path,
    content: '',
    saved: true,
    viewMode: 'browse',
    loading: true,
    errMsg: '',
  }
  tabs.value.push(newTab)
  activeTabId.value = id

  try {
    const fileContent = await api.filesRead(path)
    updateActiveTab({ content: fileContent, saved: true, loading: false })
    saveRecent(path)
    await nextTick()
    await doBrowseRender()
  } catch (e: any) {
    updateActiveTab({ loading: false, errMsg: `${t('docs.loadFailed')}: ${e}` })
  }
}

async function switchToEdit() {
  if (!activeTab.value || activeTab.value.viewMode === 'edit') return
  updateActiveTab({ viewMode: 'edit' })
  await nextTick()
  createEditor(props.dark)
  await doPreview()
}

async function switchToPreview() {
  if (!activeTab.value || activeTab.value.viewMode === 'preview') return
  if (cm) { cm.destroy(); cm = null }
  updateActiveTab({ viewMode: 'preview' })
  await nextTick()
  await doPreview()
}

async function switchToSplit() {
  if (!activeTab.value || activeTab.value.viewMode === 'split') return
  updateActiveTab({ viewMode: 'split' })
  await nextTick()
  createEditor(props.dark)
  await doPreview()
}

async function switchToBrowse() {
  if (!activeTab.value || activeTab.value.viewMode === 'browse') return
  if (cm) { cm.destroy(); cm = null }
  updateActiveTab({ viewMode: 'browse' })
  await nextTick()
  await doBrowseRender()
}

async function saveFile() {
  if (!activeTab.value || saving.value) return
  saving.value = true
  try {
    await api.filesWrite(activeTab.value.path, activeTab.value.content)
    updateActiveTab({ saved: true })
    toast?.success(t('docs.saveSuccess'))
  } catch (e: any) {
    toast?.error(t('docs.saveFailed') + e.message)
  } finally {
    saving.value = false
  }
}

function closeTab(id: string) {
  const idx = tabs.value.findIndex(t => t.id === id)
  if (idx < 0) return
  const tab = tabs.value[idx]
  if (!tab.saved) {
    if (!confirm(`"${tab.path.split('/').pop()}" ${t('docs.unsavedConfirm')}`)) return
  }
  // Destroy editor if closing active tab
  if (id === activeTabId.value && cm) {
    cm.destroy()
    cm = null
  }
  tabs.value.splice(idx, 1)
  if (activeTabId.value === id) {
    activeTabId.value = tabs.value[Math.min(idx, tabs.value.length - 1)]?.id || ''
  }
}

function closeOtherTabs(id: string) {
  const keep = tabs.value.find(t => t.id === id)
  if (!keep) return
  if (cm) { cm.destroy(); cm = null }
  tabs.value = [keep]
  activeTabId.value = id
}

const showNew = ref(false)
const newName = ref('')
const newIsDir = ref(false)
const newParent = ref('')
const showRename = ref(false)
const renamePath = ref('')
const renameNew = ref('')

function openNew(parent: string) {
  newParent.value = parent
  newName.value = ''
  newIsDir.value = false
  showNew.value = true
}

async function doNew() {
  const name = newName.value.trim()
  if (!name) return
  try {
    const r = await api.filesNew(newParent.value, name, newIsDir.value)
    showNew.value = false
    await refreshTree()
    if (r.path && !newIsDir.value) openFile(r.path)
    toast?.success(t('docs.createSuccess'))
  } catch (e: any) {
    toast?.error(e.message)
  }
}

function openRename(path: string) {
  renamePath.value = path
  renameNew.value = path.split('/').pop() || ''
  showRename.value = true
}

async function doRename() {
  const name = renameNew.value.trim()
  if (!name || !renamePath.value) return
  try {
    const r = await api.filesRename(renamePath.value, name)
    showRename.value = false
    if (activeTab.value && activeTab.value.path === renamePath.value) updateActiveTab({ path: r.path })
    await refreshTree()
    toast?.success(t('docs.renameSuccess'))
  } catch (e: any) {
    toast?.error(e.message)
  }
}

const showDeleteConfirm = ref(false)
const deleteTarget = ref('')
const deleteTargetName = ref('')

function confirmDelete(path: string, name: string) {
  deleteTarget.value = path
  deleteTargetName.value = name
  showDeleteConfirm.value = true
}

async function doDelete() {
  try {
    await api.filesDelete(deleteTarget.value)
    showDeleteConfirm.value = false
    if (currentFile.value === deleteTarget.value) {
      if (cm) { cm.destroy(); cm = null }
    }
    await refreshTree()
    toast?.success(t('docs.deleteSuccess'))
  } catch (e: any) {
    toast?.error(e.message)
  }
}

const showMove = ref(false)
const moveTarget = ref('')
const moveTargetName = ref('')
const moveDstDir = ref('')
const moveCurrentDir = ref('')
const ROOT_SENTINEL = '__root__'

function openMove(path: string, name: string) {
  moveTarget.value = path
  moveTargetName.value = name
  moveDstDir.value = ''
  const parts = path.split('/')
  moveCurrentDir.value = parts.length > 1 ? parts.slice(0, -1).join('/') : ''
  console.log('[move] openMove', { path, name, currentDir: moveCurrentDir.value })
  console.log('[move] fileTree:', JSON.stringify(fileTree.value))
  showMove.value = true
}

function selectMoveDir(p: string) {
  moveDstDir.value = p
  console.log('[move] selectMoveDir ->', p === ROOT_SENTINEL ? '(根目录)' : p)
}

function moveDstReal(): string {
  return moveDstDir.value === ROOT_SENTINEL ? '' : moveDstDir.value
}

function canMove(): boolean {
  return moveDstDir.value !== ''
}

async function doMove() {
  const dst = moveDstReal()
  console.log('[move] doMove', { target: moveTarget.value, name: moveTargetName.value, dst: dst || '(根目录)', raw: moveDstDir.value })
  try {
    const r = await api.filesMove(moveTarget.value, dst)
    console.log('[move] api.filesMove ok', r)
    showMove.value = false
    if (activeTab.value && activeTab.value.path === moveTarget.value && r.path) {
      updateActiveTab({ path: r.path })
    }
    await refreshTree()
    toast?.success(t('docs.moveSuccess'))
  } catch (e: any) {
    console.error('[move] api.filesMove error', e)
    toast?.error(e.message || String(e))
  }
}

function onlyDirs(nodes: TreeNode[], excludePath?: string): TreeNode[] {
  return nodes.filter(n => {
    if (n.type !== 'dir') return false
    if (excludePath && n.path === excludePath) return false
    return true
  }).map(n => ({
    ...n,
    children: n.children ? onlyDirs(n.children, excludePath) : []
  }))
}

async function refreshTree() {
  fileTree.value = await api.filesTree()
}

function handleKeydown(e: KeyboardEvent) {
  if ((e.ctrlKey || e.metaKey) && e.key === 's') { e.preventDefault(); saveFile() }
}

function insertMarkdown(syntax: string, wrap = false) {
  if (!cm) return
  const { from, to } = cm.state.selection.main
  const selected = cm.state.sliceDoc(from, to)
  let insert = ''
  let cursorOffset = 0
  if (wrap && selected) {
    insert = syntax + selected + syntax
    cursorOffset = syntax.length + selected.length + syntax.length
  } else if (syntax.endsWith(' ')) {
    insert = syntax
    cursorOffset = syntax.length
  } else if (syntax.includes('()')) {
    insert = syntax.replace('()', selected || '文本')
    cursorOffset = insert.indexOf(')')
  } else {
    insert = syntax + (selected || '文本')
    cursorOffset = insert.length
  }
  cm.dispatch({ changes: { from, to, insert }, selection: { anchor: from + cursorOffset } })
  cm.focus()
}

function insertTable() {
  if (!cm) return
  const { from, to } = cm.state.selection.main
  const table = '\n| 列1 | 列2 | 列3 |\n|------|------|------|\n| 内容 | 内容 | 内容 |\n'
  cm.dispatch({ changes: { from, to, insert: table } })
  cm.focus()
}

const formatKeymap = [
  { key: 'Mod-b', run: () => { insertMarkdown('**', true); return true } },
  { key: 'Mod-i', run: () => { insertMarkdown('*', true); return true } },
  { key: 'Mod-k', run: () => { insertMarkdown('[链接](url)'); return true } },
  { key: 'Mod-Shift-k', run: () => { insertMarkdown('![图片](url)'); return true } },
  { key: 'Mod-Shift-h', run: () => { insertMarkdown('## '); return true } },
  { key: 'Mod-q', run: () => { insertMarkdown('> '); return true } },
  { key: 'Mod-Shift-c', run: () => { insertMarkdown('```\n代码\n```'); return true } },
  { key: 'Mod-Shift-t', run: () => { insertTable(); return true } },
  { key: 'Mod-Shift-s', run: () => { insertMarkdown('~~', true); return true } },
]

const fileShortName = () => activeTab.value ? activeTab.value.path.split('/').pop() || '' : ''
</script>

<template>
  <div class="dm-wrap" @keydown="handleKeydown">
    <div class="breadcrumb" style="position:absolute;top:0;left:260px;right:0;padding:8px 16px;z-index:1;font-size:13px;">{{ t('docs.title') }}</div>
    <!-- 左侧文件树 -->
    <aside
      class="dm-sidebar"
      :class="{ collapsed: sidebarCollapsed }"
      :style="sidebarCollapsed ? {} : { width: sidebarWidth + 'px' }"
    >
      <div class="dm-sidebar-head">
        <template v-if="!sidebarCollapsed">
          <div class="dm-sidebar-top">
            <span class="sidebar-label">{{ t('docs.sidebar') }}</span>
            <button class="icon-btn sm" @click="openNew('')" :title="t('docs.newFile')">＋</button>
          </div>
          <input
            v-model="treeFilter"
            class="dm-tree-filter"
            :placeholder="t('docs.searchPlaceholder')"
          />
        </template>
        <button class="icon-btn sm collapse-btn" @click="sidebarCollapsed = !sidebarCollapsed" :title="sidebarCollapsed ? t('docs.expandSidebar') : t('docs.collapseSidebar')">
          <svg v-if="sidebarCollapsed" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M9 18l6-6-6-6"/></svg>
          <svg v-else width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M15 18l-6-6 6-6"/></svg>
        </button>
      </div>

      <div v-if="!sidebarCollapsed" class="dm-tree">
        <!-- Recent files -->
        <div v-if="recentFiles.length && !treeFilter" class="dm-recent">
          <div class="dm-recent-label">{{ t('docs.recentAccess') }}</div>
          <div
            v-for="path in recentFiles" :key="path"
            class="dm-recent-item"
            @click="openFile(path)"
          >
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="var(--muted)" stroke-width="2"><circle cx="12" cy="12" r="10"/><polyline points="12 6 12 12 16 14"/></svg>
            <span class="dm-recent-name">{{ path.split('/').pop() }}</span>
          </div>
        </div>

        <FileTreeNode
          v-for="node in filteredTree"
          :key="node.path"
          :node="node"
          :depth="0"
          :active-path="currentFile"
          :expanded="expanded"
          @click="clickNode"
          @toggle="toggleDir"
          @new="openNew"
          @rename="openRename"
          @delete="confirmDelete"
          @move="openMove"
        />

        <div v-if="!filteredTree.length" class="dm-empty">
          {{ treeFilter ? t('docs.noMatch') : t('docs.noFiles') }}
        </div>
      </div>
    </aside>

    <!-- Sidebar drag handle -->
    <div v-if="!sidebarCollapsed" class="dm-sidebar-drag" @mousedown="onSidebarDragStart"></div>

    <!-- 右侧内容 -->
    <div class="dm-main">
      <div class="dm-breadcrumb" v-if="activeTab">
        <span class="bc-path">{{ activeTab.path }}</span>
      </div>

      <!-- Tab bar -->
      <div class="dm-tabs" v-if="tabs.length">
        <div
          v-for="tab in tabs" :key="tab.id"
          class="dm-tab"
          :class="{ active: tab.id === activeTabId }"
          @click="setActiveTab(tab.id)"
          @dblclick="() => {}"
        >
          <span class="dm-tab-name">{{ tab.path.split('/').pop() }}</span>
          <span v-if="!tab.saved" class="dm-tab-unsaved">●</span>
          <button class="dm-tab-close" @click.stop="closeTab(tab.id)" :title="t('common.close')">&times;</button>
        </div>
      </div>

      <div class="dm-toolbar">
        <div class="dm-toolbar-left">
          <span v-if="viewMode === 'edit' || viewMode === 'split'" class="dm-unsaved" :class="{ visible: !saved }">● {{ t('docs.unsaved') }}</span>
          <div v-if="viewMode === 'edit' || viewMode === 'split'" class="dm-format-bar">
            <button class="fmt-btn" @click="insertMarkdown('**', true)" title="加粗 Ctrl+B"><b>B</b></button>
            <button class="fmt-btn" @click="insertMarkdown('*', true)" title="斜体 Ctrl+I"><i>I</i></button>
            <button class="fmt-btn" @click="insertMarkdown('~~', true)" title="删除线"><s>S</s></button>
            <span class="fmt-sep"></span>
            <button class="fmt-btn" @click="insertMarkdown('## ')" title="标题">H2</button>
            <button class="fmt-btn" @click="insertMarkdown('### ')" title="小标题">H3</button>
            <span class="fmt-sep"></span>
            <button class="fmt-btn" @click="insertMarkdown('[链接](url)')" title="链接">🔗</button>
            <button class="fmt-btn" @click="insertMarkdown('![图片](url)')" title="图片">📷</button>
            <button class="fmt-btn" @click="insertTable()" title="表格">▦</button>
            <span class="fmt-sep"></span>
            <button class="fmt-btn" @click="insertMarkdown('> ')" title="引用">❝</button>
            <button class="fmt-btn" @click="insertMarkdown('```\n代码\n```')" title="代码块">/&gt;</button>
            <button class="fmt-btn" @click="insertMarkdown('- ')" title="列表">≡</button>
          </div>
        </div>
        <div class="dm-toolbar-right">
          <template v-if="currentFile">
            <button class="tool-btn" :class="{ active: viewMode === 'browse' }" @click="switchToBrowse">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/></svg>
              {{ t('docs.browse') }}
            </button>
            <button class="tool-btn" :class="{ active: viewMode === 'edit' }" @click="switchToEdit">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M11 4H4a2 2 0 00-2 2v14a2 2 0 002 2h14a2 2 0 002-2v-7"/><path d="M18.5 2.5a2.121 2.121 0 013 3L12 15l-4 1 1-4 9.5-9.5z"/></svg>
              {{ t('docs.edit') }}
            </button>
            <button class="tool-btn" :class="{ active: viewMode === 'split' }" @click="switchToSplit">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="3" y="3" width="18" height="18" rx="2"/><line x1="12" y1="3" x2="12" y2="21"/></svg>
              {{ t('docs.split') }}
            </button>
            <span class="toolbar-sep"></span>
            <button class="tool-btn" @click="openRename(currentFile)">{{ t('docs.rename') }}</button>
            <button class="tool-btn" @click="openMove(currentFile, fileShortName())">{{ t('fileTree.move') }}</button>
            <button class="tool-btn danger" @click="confirmDelete(currentFile, fileShortName())">{{ t('common.delete') }}</button>
            <button class="tool-btn primary" @click="saveFile" :disabled="saved || saving">{{ t('common.save') }}</button>
          </template>
        </div>
      </div>

      <div class="dm-content-row">
        <div v-if="viewMode === 'browse'" class="dm-browse" :class="{ 'with-toc': showToc && tocItems.length }">
          <div v-if="loading" class="doc-loading"><div class="spinner"></div><div>{{ t('docs.loadingDoc') }}</div></div>
          <div v-else-if="errMsg" class="doc-error"><b>{{ t('docs.errorTitle') }}</b><p>{{ errMsg }}</p></div>
          <div v-else-if="currentFile && content" ref="previewRef" class="markdown-body"></div>
          <div v-else class="doc-empty">
            <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="var(--muted)" stroke-width="1.5" style="opacity:.4"><path d="M14 2H6a2 2 0 00-2 2v16a2 2 0 002 2h12a2 2 0 002-2V8z"/><polyline points="14 2 14 8 20 8"/></svg>
            <div class="empty-title">{{ t('docs.selectFileHint') }}</div>
            <div class="empty-desc">{{ t('docs.selectFileDesc') }}</div>
          </div>
          <aside v-if="showToc && tocItems.length && viewMode === 'browse' && currentFile" class="dm-toc">
            <div class="dm-toc-title">{{ t('docs.toc') }}</div>
            <div class="dm-toc-list">
              <a
                v-for="t in tocItems" :key="t.id"
                :href="'#' + t.id"
                class="dm-toc-item"
                :class="{ active: activeTocId === t.id }"
                :style="{ paddingLeft: (t.level - 1) * 12 + 'px' }"
                @click.prevent="document.getElementById(t.id)?.scrollIntoView({ behavior: 'smooth' })"
              >{{ t.text }}</a>
            </div>
          </aside>
        </div>

        <div v-if="viewMode === 'edit'" class="dm-edit-only">
          <div class="dm-editor-full" ref="editorRef"></div>
        </div>
        <div v-if="viewMode === 'preview'" class="dm-preview-only">
          <div v-if="currentFile && content" ref="previewRef" class="markdown-body dm-preview-content"></div>
        </div>
        <div v-if="viewMode === 'split'" class="dm-edit-split">
          <div class="dm-editor-pane" ref="editorRef"></div>
          <div class="dm-divider"></div>
          <div class="dm-preview-pane markdown-body" ref="previewPaneRef"></div>
        </div>
      </div>
    </div>

    <button
      v-if="tocItems.length && viewMode === 'browse' && currentFile"
      class="dm-toc-toggle"
      :class="{ active: showToc }"
      @click="showToc = !showToc"
      :title="t('docs.toc')"
    >
      <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="8" y1="6" x2="21" y2="6"/><line x1="8" y1="12" x2="21" y2="12"/><line x1="8" y1="18" x2="21" y2="18"/><line x1="3" y1="6" x2="3.01" y2="6"/><line x1="3" y1="12" x2="3.01" y2="12"/><line x1="3" y1="18" x2="3.01" y2="18"/></svg>
    </button>

    <!-- Modals -->
    <Teleport to="body">
      <div v-if="showNew" class="modal-mask" @click.self="showNew = false">
        <div class="modal-box">
          <h3>{{ newIsDir ? t('docs.newDir') : t('docs.newFile') }}</h3>
          <div class="form-row">
            <label>{{ t('common.name') }}</label>
            <input v-model="newName" :placeholder="newIsDir ? t('docs.newDirName') : t('docs.newFileName')" @keydown.enter="doNew" autofocus />
          </div>
          <div class="form-row check-row">
            <label><input type="checkbox" v-model="newIsDir" /> {{ t('docs.createAsDir') }}</label>
          </div>
          <div class="modal-actions">
            <button class="tool-btn" @click="showNew = false">{{ t('common.cancel') }}</button>
            <button class="tool-btn primary" @click="doNew">{{ t('common.create') }}</button>
          </div>
        </div>
      </div>
    </Teleport>

    <Teleport to="body">
      <div v-if="showRename" class="modal-mask" @click.self="showRename = false">
        <div class="modal-box">
          <h3>{{ t('docs.rename') }}</h3>
          <div class="form-row">
            <label>{{ t('docs.newName') }}</label>
            <input v-model="renameNew" @keydown.enter="doRename" autofocus />
          </div>
          <div class="modal-actions">
            <button class="tool-btn" @click="showRename = false">{{ t('common.cancel') }}</button>
            <button class="tool-btn primary" @click="doRename">{{ t('common.confirm') }}</button>
          </div>
        </div>
      </div>
    </Teleport>

    <Teleport to="body">
      <div v-if="showDeleteConfirm" class="modal-mask" @click.self="showDeleteConfirm = false">
        <div class="modal-box modal-danger">
          <h3>{{ t('common.confirmDelete') }}</h3>
          <p class="modal-desc">{{ t('docs.deleteConfirm') }}</p>
          <div class="modal-actions">
            <button class="tool-btn" @click="showDeleteConfirm = false">{{ t('common.cancel') }}</button>
            <button class="tool-btn danger" @click="doDelete">{{ t('common.confirmDelete') }}</button>
          </div>
        </div>
      </div>
    </Teleport>

    <Teleport to="body">
      <div v-if="showMove" class="modal-mask" @click.self="showMove = false">
        <div class="modal-box">
          <h3>{{ t('docs.moveTo') }}</h3>
          <p class="modal-desc" style="margin-bottom:12px">将「<b>{{ moveTargetName }}</b>」移动到：</p>
          <div class="move-dir-tree">
            <div
              class="move-dir-item"
              :class="{ active: moveDstDir === ROOT_SENTINEL }"
              @click="selectMoveDir(ROOT_SENTINEL)"
            >
              <span class="move-dir-icon">📁</span>
              <span>{{ t('docs.rootDir') }}</span>
            </div>
            <template v-for="node in onlyDirs(fileTree, moveCurrentDir)" :key="node.path">
              <DirSelectNode
                :node="node"
                :depth="0"
                :selected="moveDstDir"
                @select="selectMoveDir"
              />
            </template>
          </div>
          <div class="modal-actions">
            <button class="tool-btn" @click="showMove = false">{{ t('common.cancel') }}</button>
            <button class="tool-btn primary" @click="doMove" :disabled="!canMove()">{{ t('fileTree.move') }}</button>
          </div>
        </div>
      </div>
    </Teleport>
  </div>
</template>

<style scoped>
.dm-wrap { display:flex; height:100%; overflow:hidden; }

/* Sidebar */
.dm-sidebar {
  width:260px; flex-shrink:0; background:var(--panel); border-right:1px solid var(--border);
  display:flex; flex-direction:column; transition: width .2s ease;
}
.dm-sidebar.collapsed { width:42px !important; }
.dm-sidebar-head {
  display:flex; flex-direction:column; gap:8px;
  padding:10px; border-bottom:1px solid var(--border);
}
.dm-sidebar.collapsed .dm-sidebar-head { padding:10px 6px; align-items:center; }
.dm-sidebar-top { display:flex; align-items:center; justify-content:space-between; }
.sidebar-label { font-size:12px; color:var(--muted); text-transform:uppercase; letter-spacing:.05em; font-weight:600; white-space:nowrap; }
.icon-btn.sm { min-width:28px; min-height:28px; font-size:16px; border-radius:6px; display:flex; align-items:center; justify-content:center; }
.icon-btn.sm:hover { background:var(--accent-soft); color:var(--accent); }
.collapse-btn { color:var(--muted); }
.collapse-btn:hover { color:var(--fg); }

.dm-tree-filter {
  width:100%; height:30px; padding:0 10px; font-size:12px;
  border:1px solid var(--border); border-radius:6px; background:var(--panel-2);
  color:var(--fg); outline:none; box-sizing:border-box;
}
.dm-tree-filter:focus { border-color:var(--accent); }

/* Tree */
.dm-tree { flex:1; overflow-y:auto; padding:6px 4px; }
.dm-tree::-webkit-scrollbar { width:5px; }
.dm-tree::-webkit-scrollbar-track { background:transparent; }
.dm-tree::-webkit-scrollbar-thumb { background:var(--border); border-radius:3px; }
.dm-tree::-webkit-scrollbar-thumb:hover { background:var(--muted); }

/* Recent */
.dm-recent { padding:0 4px 8px; border-bottom:1px solid var(--border); margin-bottom:6px; }
.dm-recent-label { font-size:11px; color:var(--muted); text-transform:uppercase; margin-bottom:4px; padding:0 4px; font-weight:600; }
.dm-recent-item {
  display:flex; align-items:center; gap:6px; padding:5px 8px; border-radius:6px;
  cursor:pointer; font-size:12px; color:var(--fg-2); min-height:30px;
}
.dm-recent-item:hover { background:var(--panel-2); color:var(--accent); }
.dm-recent-name { overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }

.dm-empty { font-size:12px; color:var(--muted); text-align:center; padding:24px; }

/* Drag handle */
.dm-sidebar-drag {
  width:6px; cursor:col-resize; background:transparent; flex-shrink:0;
  transition: background .15s; position:relative;
}
.dm-sidebar-drag::after {
  content:''; position:absolute; left:2px; top:0; bottom:0; width:2px;
  background:transparent; transition: background .15s;
}
.dm-sidebar-drag:hover::after { background:var(--accent); }

/* Main */
.dm-main { flex:1; display:flex; flex-direction:column; min-width:0; overflow:hidden; }
.dm-breadcrumb { padding:10px 24px 0; font-size:13px; color:var(--muted); }
.bc-path { font-family:'SFMono-Regular',Consolas,'Liberation Mono',Menlo,monospace; }

.dm-toolbar { display:flex; align-items:center; justify-content:space-between; padding:6px 24px; border-bottom:1px solid var(--border); background:var(--panel); min-height:42px; flex-shrink:0; gap:12px; }
.dm-toolbar-left { display:flex; align-items:center; gap:10px; min-width:0; flex:1; }
.dm-unsaved { font-size:12px; color:#f59e0b; opacity:0; transition:opacity .2s; }
.dm-unsaved.visible { opacity:1; }
.dm-toolbar-right { display:flex; gap:4px; flex-shrink:0; align-items:center; }
.toolbar-sep { width:1px; height:20px; background:var(--border); margin:0 4px; }

.dm-format-bar { display:flex; align-items:center; gap:1px; margin-left:8px; }
.fmt-btn { min-width:28px; min-height:28px; font-size:13px; border-radius:4px; color:var(--fg-2); display:inline-flex; align-items:center; justify-content:center; }
.fmt-btn:hover { background:var(--accent-soft); color:var(--accent); }
.fmt-sep { width:1px; height:18px; background:var(--border); margin:0 3px; }
.tool-btn { display:inline-flex; align-items:center; gap:4px; min-height:32px; padding:0 12px; font-size:13px; border:1px solid var(--border); border-radius:6px; color:var(--fg-2); background:var(--panel); }
.tool-btn:hover:not(:disabled) { border-color:var(--accent); color:var(--accent); }
.tool-btn:disabled { opacity:.35; cursor:default; }
.tool-btn.active { border-color:var(--accent); color:var(--accent); background:var(--accent-soft); font-weight:600; }
.tool-btn.primary { background:var(--accent); color:var(--accent-fg); border-color:var(--accent); }
.tool-btn.primary:hover:not(:disabled) { opacity:.9; }
.tool-btn.danger:hover:not(:disabled) { border-color:#dc2626; color:#dc2626; }

/* Content row */
.dm-content-row { flex:1; display:flex; min-height:0; overflow:hidden; }
.dm-browse { flex:1; overflow-y:auto; padding:28px 36px 80px; max-width:960px; margin:0 auto; width:100%; }
.dm-browse.with-toc { max-width:none; margin:0; padding-right:20px; }
.dm-edit-only { flex:1; display:flex; min-height:0; }
.dm-editor-full { flex:1; min-width:0; display:flex; flex-direction:column; min-height:0; }
.dm-editor-full :deep(.cm-editor) { flex:1; min-height:0; }
.dm-preview-only { flex:1; overflow-y:auto; padding:28px 36px 80px; max-width:960px; margin:0 auto; width:100%; }
.dm-preview-content { width:100%; }
.dm-edit-split { flex:1; display:flex; min-height:0; overflow:hidden; }
.dm-editor-pane { flex:0 0 50%; min-width:0; display:flex; flex-direction:column; min-height:0; }
.dm-editor-pane :deep(.cm-editor) { flex:1; min-height:0; }
.dm-divider { width:1px; background:var(--border); flex-shrink:0; }
.dm-preview-pane { flex:0 0 50%; min-width:0; overflow-y:auto; padding:20px 28px; }

/* TOC */
.dm-toc {
  width:220px; flex-shrink:0; border-left:1px solid var(--border);
  padding:16px 12px; overflow-y:auto; background:var(--panel);
}
.dm-toc-title { font-size:12px; color:var(--muted); text-transform:uppercase; font-weight:600; margin-bottom:10px; letter-spacing:.03em; }
.dm-toc-item {
  display:block; font-size:13px; color:var(--fg-2); padding:4px 8px;
  border-radius:5px; text-decoration:none; line-height:1.4;
  border-left:2px solid transparent; transition:all .15s;
}
.dm-toc-item:hover { color:var(--accent); background:var(--accent-soft); }
.dm-toc-item.active { color:var(--accent); border-left-color:var(--accent); font-weight:500; }

.dm-toc-toggle {
  position:fixed; right:16px; bottom:16px; z-index:50;
  width:40px; height:40px; border-radius:50%;
  background:var(--panel); border:1px solid var(--border);
  box-shadow:var(--shadow-lg); display:flex; align-items:center; justify-content:center;
  color:var(--muted); transition:all .15s;
}
.dm-toc-toggle:hover, .dm-toc-toggle.active { color:var(--accent); border-color:var(--accent); }

/* Empty state */
.doc-empty { display:flex; flex-direction:column; align-items:center; justify-content:center; padding:80px 20px; text-align:center; gap:12px; }
.empty-title { font-size:18px; font-weight:600; color:var(--fg-2); }
.empty-desc { font-size:14px; color:var(--muted); }

/* Modal */
.modal-mask { position:fixed; inset:0; background:rgba(0,0,0,.45); z-index:200; display:flex; align-items:center; justify-content:center; }
.modal-box { background:var(--panel); border-radius:12px; padding:24px; width:380px; max-width:90vw; box-shadow:0 20px 60px rgba(0,0,0,.3); }
.modal-box.modal-danger { border:1px solid #fecaca; }
.modal-box h3 { margin:0 0 12px; font-size:17px; }
.modal-desc { font-size:14px; color:var(--fg-2); line-height:1.6; margin:0; }
.modal-desc b { color:var(--fg); }
.form-row { margin-bottom:14px; }
.form-row label { display:block; font-size:13px; color:var(--muted); margin-bottom:4px; }
.form-row input[type='text'], .form-row input:not([type]) { width:100%; padding:8px 10px; border:1px solid var(--border); border-radius:6px; background:var(--panel-2); color:var(--fg); font-size:14px; }
.form-row input:focus { outline:none; border-color:var(--accent); box-shadow:0 0 0 2px var(--accent-soft); }
.check-row label { display:flex; align-items:center; gap:6px; cursor:pointer; color:var(--fg-2); font-size:14px; }
.check-row input[type='checkbox'] { accent-color:var(--accent); }
.modal-actions { display:flex; justify-content:flex-end; gap:8px; margin-top:20px; }

.move-dir-tree { max-height:300px; overflow-y:auto; border:1px solid var(--border); border-radius:8px; padding:6px; margin-bottom:12px; }
.move-dir-item {
  display:flex; align-items:center; gap:6px; padding:7px 10px; border-radius:6px;
  cursor:pointer; font-size:13px; color:var(--fg-2); transition:background .1s;
}
.move-dir-item:hover { background:var(--panel-2); color:var(--fg); }
.move-dir-item.active { background:var(--accent-soft); color:var(--accent); font-weight:600; }
.move-dir-icon { font-size:14px; }

@media(max-width:768px){
  .dm-sidebar{display:none}
  .dm-edit-split{flex-direction:column}
  .dm-divider{width:100%;height:1px}
  .dm-preview-pane{max-height:45vh}
  .dm-browse{padding:16px 14px 60px}
  .dm-toolbar{padding:6px 14px}
  .dm-format-bar{display:none}
  .dm-toc{display:none}
}


/* ═══ Tab bar ═══ */
.dm-tabs {
  display: flex;
  gap: 2px;
  padding: 0 12px;
  background: var(--panel-2);
  border-bottom: 1px solid var(--border);
  overflow-x: auto;
  scrollbar-width: none;
  flex-shrink: 0;
}
.dm-tabs::-webkit-scrollbar { display: none; }

.dm-tab {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 8px 14px;
  font-size: 13px;
  color: var(--muted);
  cursor: pointer;
  border-bottom: 2px solid transparent;
  white-space: nowrap;
  transition: all .15s;
  border-radius: 6px 6px 0 0;
  max-width: 180px;
}
.dm-tab:hover {
  color: var(--fg);
  background: var(--panel);
}
.dm-tab.active {
  color: var(--accent);
  border-bottom-color: var(--accent);
  background: var(--panel);
  font-weight: 600;
}

.dm-tab-name {
  overflow: hidden;
  text-overflow: ellipsis;
}

.dm-tab-unsaved {
  color: var(--btn-warning);
  font-size: 10px;
  flex-shrink: 0;
}

.dm-tab-close {
  width: 18px;
  height: 18px;
  display: flex;
  align-items: center;
  justify-content: center;
  border: none;
  background: none;
  color: var(--muted);
  font-size: 14px;
  border-radius: 4px;
  cursor: pointer;
  opacity: 0;
  transition: all .15s;
  flex-shrink: 0;
}
.dm-tab:hover .dm-tab-close { opacity: 1; }
.dm-tab-close:hover { background: rgba(239, 68, 68, 0.15); color: #ef4444; }
</style>
