<script setup lang="ts">
import { ref, computed, watch, onBeforeUnmount, nextTick, inject } from 'vue'
import { useI18n } from 'vue-i18n'
import { EditorView, keymap, lineNumbers, highlightActiveLine, highlightActiveLineGutter } from '@codemirror/view'
import { EditorState } from '@codemirror/state'
import { markdown } from '@codemirror/lang-markdown'
import { defaultKeymap, history, historyKeymap } from '@codemirror/commands'
import { syntaxHighlighting, defaultHighlightStyle, bracketMatching } from '@codemirror/language'
import { searchKeymap, search } from '@codemirror/search'
import { renderDoc, renderMermaid } from '../markdown'
import { isDark } from '../theme'
import { api } from '../api'

interface ConnParams {
  id: string; host: string; port: number; username: string
  auth_type: string; password: string; key_path: string
}

const props = defineProps<{ show: boolean; connParams: ConnParams; path: string; webrtcManager?: any }>()
const emit = defineEmits<{ (e: 'update:show', v: boolean): void; (e: 'saved'): void }>()
const { t } = useI18n()
const toast = inject<any>('toast')

const editorEl = ref<HTMLDivElement>()
const previewEl = ref<HTMLDivElement>()
const content = ref('')
const saved = ref(true)
const saving = ref(false)
const statusKind = ref<'saved' | 'unsaved' | 'saving' | 'error'>('saved')
const statusExtra = ref('')
const loading = ref(false)
const loadError = ref('')
const viewMode = ref<'edit' | 'split' | 'preview'>('split')

let cm: EditorView | null = null
let previewTimer: ReturnType<typeof setTimeout> | null = null

const fileName = computed(() => props.path.split('/').pop() || props.path)

const statusText = computed(() => {
  switch (statusKind.value) {
    case 'saving': return t('fileEditor.saving')
    case 'unsaved': return t('fileEditor.unsaved')
    case 'error': return statusExtra.value
    default: return t('fileEditor.saved')
  }
})

/* ── Base64 helpers ── */
function decodeBase64Utf8(b64: string): string {
  const binary = atob(b64)
  const bytes = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i)
  return new TextDecoder('utf-8').decode(bytes)
}

/* ── Markdown insert helpers ── */
function insertMd(view: EditorView, before: string, wrap = false) {
  const { from, to } = view.state.selection.main
  const sel = view.state.sliceDoc(from, to)
  const after = wrap ? before : ''
  const insert = sel ? before + sel + after : before
  view.dispatch({ changes: { from, to, insert }, selection: { anchor: from + insert.length } })
  view.focus()
}

function insertTable(view: EditorView) {
  const { from, to } = view.state.selection.main
  const table = '\n| 列1 | 列2 | 列3 |\n|------|------|------|\n| 内容 | 内容 | 内容 |\n'
  view.dispatch({ changes: { from, to, insert: table }, selection: { anchor: from + table.length } })
  view.focus()
}

function handleFmtAction(action: string) {
  if (!cm) return
  const v = cm
  switch (action) {
    case 'bold': insertMd(v, '**', true); break
    case 'italic': insertMd(v, '*', true); break
    case 'strike': insertMd(v, '~~', true); break
    case 'h2': insertMd(v, '## '); break
    case 'h3': insertMd(v, '### '); break
    case 'link': insertMd(v, '[链接](url)'); break
    case 'image': insertMd(v, '![图片](url)'); break
    case 'table': insertTable(v); break
    case 'quote': insertMd(v, '> '); break
    case 'code': insertMd(v, '```\n代码\n```'); break
    case 'list': insertMd(v, '- '); break
  }
}

function onFormatClick(e: MouseEvent) {
  const btn = (e.target as HTMLElement).closest('[data-fmt]') as HTMLElement | null
  if (btn) handleFmtAction(btn.dataset.fmt || '')
}

const formatKeymap = [
  { key: 'Mod-b', run: (v: EditorView) => { insertMd(v, '**', true); return true } },
  { key: 'Mod-i', run: (v: EditorView) => { insertMd(v, '*', true); return true } },
  { key: 'Mod-k', run: (v: EditorView) => { insertMd(v, '[链接](url)'); return true } },
  { key: 'Mod-Shift-k', run: (v: EditorView) => { insertMd(v, '![图片](url)'); return true } },
  { key: 'Mod-Shift-h', run: (v: EditorView) => { insertMd(v, '## '); return true } },
  { key: 'Mod-q', run: (v: EditorView) => { insertMd(v, '> '); return true } },
  { key: 'Mod-Shift-c', run: (v: EditorView) => { insertMd(v, '```\n代码\n```'); return true } },
  { key: 'Mod-Shift-t', run: (v: EditorView) => { insertTable(v); return true } },
  { key: 'Mod-Shift-s', run: (v: EditorView) => { insertMd(v, '~~', true); return true } },
]

/* ── Themes ── */
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

/* ── Editor lifecycle ── */
function createEditor(dark: boolean) {
  if (cm) { cm.destroy(); cm = null }
  if (!editorEl.value) return
  cm = new EditorView({
    state: EditorState.create({
      doc: content.value,
      extensions: [
        lineNumbers(), highlightActiveLineGutter(), history(),
        bracketMatching(), highlightActiveLine(),
        syntaxHighlighting(defaultHighlightStyle), markdown(), search(),
        keymap.of([...defaultKeymap, ...historyKeymap, ...searchKeymap, ...formatKeymap]),
        dark ? darkTheme : lightTheme,
        EditorView.updateListener.of((u) => {
          if (u.docChanged) {
            content.value = u.state.doc.toString()
            saved.value = false
            statusKind.value = 'unsaved'
            debouncedPreview()
          }
        }),
        EditorView.lineWrapping
      ]
    }),
    parent: editorEl.value
  })
}

function destroyEditor() {
  if (previewTimer) { clearTimeout(previewTimer); previewTimer = null }
  if (cm) { cm.destroy(); cm = null }
}

function debouncedPreview() {
  if (previewTimer) clearTimeout(previewTimer)
  previewTimer = setTimeout(doPreview, 200)
}

async function doPreview() {
  if (viewMode.value === 'edit') return
  const target = previewEl.value
  if (!target) return
  const { html } = renderDoc(content.value)
  target.innerHTML = html
  await nextTick()
  await renderMermaid(target, isDark.value)
}

function switchView(mode: 'edit' | 'split' | 'preview') {
  viewMode.value = mode
  nextTick(() => {
    cm?.requestMeasure()
    if (mode !== 'edit') doPreview()
  })
}

/* ── Load / Save ── */
async function loadFile() {
  loading.value = true
  loadError.value = ''
  destroyEditor()
  try {
    let result: any
    if (props.webrtcManager) {
      result = await props.webrtcManager.sendSftpRequest('read', { path: props.path })
    } else {
      result = await api.sftpClientRead({ ...props.connParams, path: props.path })
    }
    // T1.3: webrtc 路径 read 返回 raw bytes, 直连 TextDecoder; HTTP 兜底仍是 base64 串
    content.value = props.webrtcManager ? new TextDecoder().decode(result.content) : decodeBase64Utf8(result.content)
    saved.value = true
    statusKind.value = 'saved'
    statusExtra.value = ''
  } catch (e: any) {
    loadError.value = `${t('fileEditor.loadFailed')}: ${e?.message || e}`
    content.value = ''
  } finally {
    loading.value = false
  }
  await nextTick()
  if (!loadError.value) {
    createEditor(isDark.value)
    if (viewMode.value !== 'edit') doPreview()
  }
}

async function saveFile() {
  if (!cm || saved.value || saving.value) return
  saving.value = true
  statusKind.value = 'saving'
  try {
    if (props.webrtcManager) {
      // T1.3: 二进制协议下 content 传 raw UTF-8 字节, 免 base64
      await props.webrtcManager.sendSftpRequest('write', { path: props.path, content: new TextEncoder().encode(content.value) })
    } else {
      await api.sftpClientWrite({ ...props.connParams, path: props.path, content: content.value })
    }
    saved.value = true
    statusKind.value = 'saved'
    statusExtra.value = ''
    emit('saved')
  } catch (e: any) {
    statusKind.value = 'error'
    statusExtra.value = `${t('fileEditor.saveFailed')}: ${e?.message || e}`
    toast?.error?.(statusExtra.value)
  } finally {
    saving.value = false
  }
}

function requestClose() {
  if (!saved.value && !confirm(t('fileEditor.closeConfirm'))) return
  emit('update:show', false)
}

function onKeyDown(e: KeyboardEvent) {
  if ((e.ctrlKey || e.metaKey) && e.key === 's') { e.preventDefault(); saveFile() }
}

/* ── Open / close ── */
watch(() => props.show, (v) => {
  if (v) {
    content.value = ''
    saved.value = true
    saving.value = false
    statusKind.value = 'saved'
    statusExtra.value = ''
    viewMode.value = 'split'
    window.addEventListener('keydown', onKeyDown)
    nextTick(() => loadFile())
  } else {
    window.removeEventListener('keydown', onKeyDown)
    destroyEditor()
  }
})

watch(isDark, (dark) => {
  if (props.show && cm) {
    content.value = cm.state.doc.toString()
    createEditor(dark)
    if (viewMode.value !== 'edit') doPreview()
  }
})

onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKeyDown)
  destroyEditor()
})
</script>

<template>
  <Teleport to="body">
    <div v-if="show" class="fem-mask">
      <div class="fem">
        <!-- Toolbar -->
        <div class="fem-toolbar">
          <span class="fem-name">{{ fileName }}</span>
          <span class="fem-path">{{ path }}</span>
          <span class="fem-status" :class="'is-' + statusKind">{{ statusText }}</span>
          <span class="fem-spacer"></span>
          <button class="fem-btn fem-primary" :disabled="saved || saving" @click="saveFile">💾 {{ t('fileEditor.save') }}</button>
          <button class="fem-btn" @click="requestClose">✕</button>
        </div>

        <!-- Format + view bar -->
        <div class="fem-format" @click="onFormatClick">
          <button class="fmt-btn" data-fmt="bold" :title="t('fileEditor.bold')"><b>B</b></button>
          <button class="fmt-btn" data-fmt="italic" :title="t('fileEditor.italic')"><i>I</i></button>
          <button class="fmt-btn" data-fmt="strike" :title="t('fileEditor.strike')"><s>S</s></button>
          <span class="fmt-sep"></span>
          <button class="fmt-btn" data-fmt="h2" :title="t('fileEditor.h2')">H2</button>
          <button class="fmt-btn" data-fmt="h3" :title="t('fileEditor.h3')">H3</button>
          <span class="fmt-sep"></span>
          <button class="fmt-btn" data-fmt="link" :title="t('fileEditor.link')">🔗</button>
          <button class="fmt-btn" data-fmt="image" :title="t('fileEditor.image')">📷</button>
          <button class="fmt-btn" data-fmt="table" :title="t('fileEditor.table')">▦</button>
          <span class="fmt-sep"></span>
          <button class="fmt-btn" data-fmt="quote" :title="t('fileEditor.quote')">❝</button>
          <button class="fmt-btn" data-fmt="code" :title="t('fileEditor.code')">&lt;/&gt;</button>
          <button class="fmt-btn" data-fmt="list" :title="t('fileEditor.list')">≡</button>
          <span class="fem-spacer"></span>
          <button class="view-btn" :class="{ active: viewMode === 'edit' }" @click="switchView('edit')">{{ t('fileEditor.viewEdit') }}</button>
          <button class="view-btn" :class="{ active: viewMode === 'split' }" @click="switchView('split')">{{ t('fileEditor.viewSplit') }}</button>
          <button class="view-btn" :class="{ active: viewMode === 'preview' }" @click="switchView('preview')">{{ t('fileEditor.viewPreview') }}</button>
        </div>

        <!-- Body -->
        <div class="fem-body">
          <div v-if="loading" class="fem-state">{{ t('fileEditor.loading') }}</div>
          <div v-else-if="loadError" class="fem-state fem-error">{{ loadError }}</div>
          <div v-else class="fem-split">
            <div class="fem-editor" :class="{ hidden: viewMode === 'preview' }" ref="editorEl"></div>
            <div class="fem-preview markdown-body" :class="{ hidden: viewMode === 'edit' }" ref="previewEl"></div>
          </div>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<style scoped>
.fem-mask { position:fixed; inset:0; z-index:9999; background:rgba(0,0,0,.55); display:flex; align-items:center; justify-content:center; padding:24px; }
.fem { display:flex; flex-direction:column; width:min(1200px, 96vw); height:min(860px, 92vh); background:var(--panel); border:1px solid var(--border); border-radius:10px; overflow:hidden; box-shadow:0 20px 60px rgba(0,0,0,.4); }

.fem-toolbar { display:flex; align-items:center; gap:12px; padding:8px 14px; border-bottom:1px solid var(--border); background:var(--panel-2); flex-shrink:0; min-height:40px; }
.fem-name { font-size:14px; font-weight:600; white-space:nowrap; color:var(--fg); }
.fem-path { font-size:12px; color:var(--muted); flex:1; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
.fem-status { font-size:12px; padding:2px 8px; border-radius:4px; white-space:nowrap; }
.fem-status.is-saved { color:#34d399; }
.fem-status.is-unsaved, .fem-status.is-saving { color:#fbbf24; }
.fem-status.is-error { color:#f87171; }
.fem-spacer { flex:1; }

.fem-btn { padding:5px 12px; border-radius:4px; border:1px solid var(--border); cursor:pointer; font-size:12px; font-weight:500; background:var(--bg); color:var(--fg-2); }
.fem-btn:hover { background:var(--panel-2); }
.fem-btn:disabled { opacity:.45; cursor:not-allowed; }
.fem-primary { background:var(--accent); border:none; color:var(--accent-fg); }
.fem-primary:hover { opacity:.9; background:var(--accent); }

.fem-format { display:flex; align-items:center; gap:4px; padding:4px 14px; border-bottom:1px solid var(--border); background:var(--panel-2); flex-shrink:0; flex-wrap:wrap; }
.fmt-btn { background:var(--bg); border:1px solid var(--border); color:var(--muted); padding:4px 9px; border-radius:4px; cursor:pointer; font-size:13px; line-height:1; font-family:inherit; }
.fmt-btn:hover { background:var(--panel-2); color:var(--fg); border-color:var(--accent); }
.fmt-sep { width:1px; height:18px; background:var(--border); margin:0 3px; }
.view-btn { background:var(--bg); border:1px solid var(--border); color:var(--muted); padding:4px 12px; border-radius:4px; cursor:pointer; font-size:12px; }
.view-btn:hover { background:var(--panel-2); color:var(--fg); }
.view-btn.active { background:var(--accent); color:var(--accent-fg); border-color:var(--accent); }

.fem-body { flex:1; min-height:0; display:flex; flex-direction:column; background:var(--bg); }
.fem-state { flex:1; display:flex; align-items:center; justify-content:center; color:var(--muted); font-size:15px; padding:24px; text-align:center; }
.fem-error { color:#f87171; }

.fem-split { flex:1; display:flex; overflow:hidden; min-height:0; }
.fem-editor { flex:1; min-width:0; overflow:hidden; display:flex; flex-direction:column; min-height:0; }
.fem-editor :deep(.cm-editor) { flex:1; min-height:0; height:100%; }
.fem-editor :deep(.cm-scroller) { min-height:0; }
.fem-preview { flex:1; overflow-y:auto; padding:20px 28px; min-width:0; min-height:0; color:var(--fg); }
.fem-editor.hidden, .fem-preview.hidden { display:none; }
</style>
