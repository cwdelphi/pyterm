import { EditorView, keymap, lineNumbers, highlightActiveLine, highlightActiveLineGutter } from '@codemirror/view'
import { EditorState, Compartment } from '@codemirror/state'
import { markdown } from '@codemirror/lang-markdown'
import { defaultKeymap, history, historyKeymap } from '@codemirror/commands'
import { syntaxHighlighting, defaultHighlightStyle, bracketMatching } from '@codemirror/language'
import { searchKeymap, search } from '@codemirror/search'
import { renderDoc, renderMermaid } from './markdown'

interface ConnParams {
  id: string; host: string; port: number; username: string
  auth_type: string; password: string; key_path: string
}

interface EditorParams {
  conn: ConnParams; path: string
}

let cm: EditorView | null = null
let saved = true
let currentContent = ''
let viewMode: 'edit' | 'split' | 'preview' = 'split'
let wrapCompartment = new Compartment()

// === Themes ===

const darkTheme = EditorView.theme({
  '&': { backgroundColor: '#0f172a', color: '#e2e8f0' },
  '.cm-content': {
    fontFamily: '"SFMono-Regular", Consolas, "Liberation Mono", Menlo, monospace',
    fontSize: '14px', lineHeight: '1.6', padding: '12px 0'
  },
  '.cm-gutters': {
    backgroundColor: '#1e293b', color: '#64748b',
    border: 'none', borderRight: '1px solid #334155'
  },
  '.cm-activeLineGutter': { backgroundColor: '#16202f' },
  '.cm-activeLine': { backgroundColor: '#16202f44' },
  '.cm-selectionBackground': { backgroundColor: '#1e40af55 !important' },
  '.cm-cursor': { borderLeftColor: '#60a5fa' },
  '&.cm-focused .cm-selectionBackground': { backgroundColor: '#1d4ed855 !important' },
  '.cm-matchingBracket': { backgroundColor: '#065f46', outline: 'none' }
})

const lightTheme = EditorView.theme({
  '&': { backgroundColor: '#ffffff', color: '#1e293b' },
  '.cm-content': {
    fontFamily: '"SFMono-Regular", Consolas, "Liberation Mono", Menlo, monospace',
    fontSize: '14px', lineHeight: '1.6', padding: '12px 0'
  },
  '.cm-gutters': {
    backgroundColor: '#f8fafc', color: '#94a3b8',
    border: 'none', borderRight: '1px solid #e2e8f0'
  },
  '.cm-activeLineGutter': { backgroundColor: '#f1f5f9' },
  '.cm-activeLine': { backgroundColor: '#f1f5f922' },
  '.cm-selectionBackground': { backgroundColor: '#bfdbfe !important' },
  '.cm-cursor': { borderLeftColor: '#2563eb' },
  '&.cm-focused .cm-selectionBackground': { backgroundColor: '#93c5fd !important' },
  '.cm-matchingBracket': { backgroundColor: '#bbf7d0', outline: 'none' }
})

// === Helpers ===

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

// === Hash decode ===

function fromUtf8B64(b64: string): string {
  const binary = atob(b64)
  const bytes = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i)
  return new TextDecoder('utf-8').decode(bytes)
}

function parseHash(): EditorParams | null {
  try {
    const hash = location.hash.slice(1)
    if (!hash) return null
    return JSON.parse(fromUtf8B64(hash))
  } catch { return null }
}

function decodeBase64Utf8(b64: string): string {
  const binary = atob(b64)
  const bytes = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i)
  return new TextDecoder('utf-8').decode(bytes)
}

// === UI helpers ===

function showError(msg: string) {
  document.getElementById('loading')!.style.display = 'none'
  const el = document.getElementById('error')!
  el.style.display = ''
  document.getElementById('error-msg')!.textContent = msg
}

function setStatus(text: string, cls: string) {
  const el = document.getElementById('status')!
  el.textContent = text
  el.className = 'status ' + cls
}

// === Preview ===

const previewPane = () => document.getElementById('preview-pane')!

let previewTimer: ReturnType<typeof setTimeout> | null = null
function debouncedPreview() {
  if (previewTimer) clearTimeout(previewTimer)
  previewTimer = setTimeout(doPreview, 200)
}

async function doPreview() {
  if (viewMode === 'edit') return
  const target = previewPane()
  if (!target) return
  const { html } = renderDoc(currentContent)
  target.innerHTML = html
  await renderMermaid(target, true)
}

// === View mode ===

function updateView() {
  const editorPane = document.getElementById('editor-pane')!
  const previewPaneEl = document.getElementById('preview-pane')!
  const formatBar = document.getElementById('format-bar')!
  const btnEdit = document.getElementById('btn-view-edit')!
  const btnSplit = document.getElementById('btn-view-split')!
  const btnPreview = document.getElementById('btn-view-preview')!

  formatBar.style.display = ''

  editorPane.classList.toggle('hidden', viewMode === 'preview')
  previewPaneEl.classList.toggle('hidden', viewMode === 'edit')

  btnEdit.classList.toggle('active', viewMode === 'edit')
  btnSplit.classList.toggle('active', viewMode === 'split')
  btnPreview.classList.toggle('active', viewMode === 'preview')

  if (cm) {
    cm.requestMeasure()
  }

  if (viewMode !== 'edit') doPreview()
}

function switchView(mode: 'edit' | 'split' | 'preview') {
  viewMode = mode
  updateView()
}

// === Format bar handlers ===

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

// === Editor creation ===

function createEditor(dark: boolean) {
  if (cm) { cm.destroy(); cm = null }
  const wrap = document.getElementById('editor-pane')!
  if (!wrap) return

  cm = new EditorView({
    state: EditorState.create({
      doc: currentContent,
      extensions: [
        lineNumbers(), highlightActiveLineGutter(), history(),
        bracketMatching(), highlightActiveLine(),
        syntaxHighlighting(defaultHighlightStyle), markdown(), search(),
        keymap.of([...defaultKeymap, ...historyKeymap, ...searchKeymap, ...formatKeymap]),
        dark ? darkTheme : lightTheme,
        EditorView.updateListener.of((u) => {
          if (u.docChanged) {
            currentContent = u.state.doc.toString()
            saved = false
            setStatus('未保存', 'status-unsaved')
            document.getElementById('btn-save')!.removeAttribute('disabled')
            debouncedPreview()
          }
        }),
        wrapCompartment.of(EditorView.lineWrapping)
      ]
    }),
    parent: wrap
  })
}

// === Save ===

async function saveFile() {
  const params = parseHash()
  if (!params || !cm) return

  const btn = document.getElementById('btn-save')!
  btn.setAttribute('disabled', 'true')
  setStatus('保存中...', 'status-unsaved')

  try {
    const token = localStorage.getItem('token')
    const res = await fetch('/api/sftp-client/write', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        ...(token ? { Authorization: `Bearer ${token}` } : {}),
      },
      body: JSON.stringify({ ...params.conn, path: params.path, content: currentContent })
    })
    if (!res.ok) {
      const e = await res.json().catch(() => ({}))
      throw new Error(e.detail || 'HTTP ' + res.status)
    }
    saved = true
    setStatus('已保存', 'status-saved')
    btn.removeAttribute('disabled')
  } catch (e: any) {
    setStatus('保存失败: ' + e.message, 'status-error')
    btn.removeAttribute('disabled')
  }
}

// === Init ===

async function loadAndEdit() {
  const params = parseHash()
  if (!params) { showError('无效的编辑器参数'); return }

  const fileName = params.path.split('/').pop() || params.path
  document.title = fileName + ' - Markdown 编辑器'
  document.getElementById('file-name')!.textContent = fileName
  document.getElementById('file-path')!.textContent = params.path

  let content = ''
  try {
    const token = localStorage.getItem('token')
    const res = await fetch('/api/sftp-client/read', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        ...(token ? { Authorization: `Bearer ${token}` } : {}),
      },
      body: JSON.stringify({ ...params.conn, path: params.path })
    })
    if (!res.ok) {
      const e = await res.json().catch(() => ({}))
      throw new Error(e.detail || 'HTTP ' + res.status)
    }
    const data = await res.json()
    content = decodeBase64Utf8(data.content)
  } catch (e: any) {
    showError('加载文件失败: ' + e.message); return
  }

  currentContent = content
  document.getElementById('loading')!.style.display = 'none'
  document.getElementById('editor-container')!.style.display = ''

  const isDark = window.matchMedia('(prefers-color-scheme: dark)').matches
  createEditor(isDark)
  updateView()

  // Format bar click
  document.getElementById('format-bar')!.addEventListener('click', (e) => {
    const btn = (e.target as HTMLElement).closest('.fmt-btn') as HTMLElement | null
    if (btn) handleFmtAction(btn.dataset.fmt || '')
  })

  // View mode buttons
  document.getElementById('btn-view-edit')!.addEventListener('click', () => switchView('edit'))
  document.getElementById('btn-view-split')!.addEventListener('click', () => switchView('split'))
  document.getElementById('btn-view-preview')!.addEventListener('click', () => switchView('preview'))

  // Save button
  document.getElementById('btn-save')!.addEventListener('click', saveFile)

  // Ctrl+S
  window.addEventListener('keydown', (e) => {
    if ((e.ctrlKey || e.metaKey) && e.key === 's') { e.preventDefault(); saveFile() }
  })

  // Unsaved warning
  window.addEventListener('beforeunload', (e) => {
    if (!saved) { e.preventDefault(); e.returnValue = '' }
  })
}

loadAndEdit()
