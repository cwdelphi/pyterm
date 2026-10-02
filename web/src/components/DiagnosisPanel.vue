<script setup lang="ts">
import { ref, onMounted, nextTick, computed, inject } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '../api'
import mermaid from 'mermaid'

const { t } = useI18n()

const toast = inject<any>('toast')
const activeTab = ref<'records' | 'stats'>('records')
const records = ref<any[]>([])
const totalRecords = ref(0)
const currentPage = ref(1)
const pageSize = 10
const loading = ref(false)
const filterType = ref('')
const pathFilter = ref('')        // DC 链路筛选：P2P / relay / BUG / none(未检测)
const searchQuery = ref('')
const selectedRecord = ref<any>(null)
const timelineDetail = ref<any>(null)
const timelineSteps = ref<any[]>([])
const timelineActors = ref<any[]>([])
const failureAnalysis = ref<any>(null)
const zoomLevel = ref(1)
const stats = ref<any>(null)
const mermaidContainer = ref<HTMLElement>()
let mermaidId = 0

const totalPages = computed(() => Math.max(1, Math.ceil(totalRecords.value / pageSize)))
const pageNumbers = computed(() => {
  const pages: number[] = []
  const start = Math.max(1, currentPage.value - 2)
  const end = Math.min(totalPages.value, currentPage.value + 2)
  for (let i = start; i <= end; i++) pages.push(i)
  return pages
})

async function loadRecords() {
  loading.value = true
  try {
    const res = await api.timelineRecords({
      page: currentPage.value, page_size: pageSize,
      conn_type: filterType.value || undefined,
      webrtc_path: pathFilter.value || undefined,
      search: searchQuery.value || undefined,
    })
    records.value = res.items || []
    totalRecords.value = res.total || 0
  } catch (e: any) { toast?.error(t('diagnosis.loadFailed') + e.message) }
  finally { loading.value = false }
}

function goToPage(p: number) {
  if (p < 1 || p > totalPages.value) return
  currentPage.value = p
  loadRecords()
}

async function loadStats() {
  try { stats.value = await api.timelineStats({}) } catch {}
}

async function viewTimeline(record: any) {
  selectedRecord.value = record
  loading.value = true
  try {
    const res = await api.timelineDetail({ room_id: record.room_id })
    if (res?.error) throw new Error(res.error)
    timelineDetail.value = res.connection
    timelineSteps.value = res.steps || []
    timelineActors.value = res.actors || []
    failureAnalysis.value = res.failure_analysis
    await nextTick()
    renderMermaid()
  } catch (e: any) { toast?.error(t('diagnosis.loadFailed') + e.message) }
  finally { loading.value = false }
}

function backToList() {
  selectedRecord.value = null
  timelineDetail.value = null
  timelineSteps.value = []
  timelineActors.value = []
  failureAnalysis.value = null
}

function mermaidSafe(text: string): string {
  // Mermaid text is not HTML — do not entity-escape & < > (shows as &amp;).
  // Only neutralize sequenceDiagram syntax breakers: ";", "#", quotes, newlines.
  return String(text ?? '')
    .replace(/[\r\n\t]+/g, ' ')
    .replace(/;/g, '；')
    .replace(/#/g, '＃')
    .replace(/"/g, "'")
    .replace(/[`]/g, "'")
    .trim()
}

function buildMermaidCode(): string {
  // 全量绘制（含 skipped）：编号 #index 必须与表格 step_index、失败卡 Step N 对齐
  const drawable = timelineSteps.value.filter(s => s.from_node && s.to_node && s.from_node !== s.to_node)
  if (!timelineActors.value.length || !drawable.length) return ''
  const lines = ['sequenceDiagram']
  for (const a of timelineActors.value) {
    const ip = a.ip ? ' (' + a.ip + ')' : ''
    lines.push('    participant ' + a.id + ' as "' + mermaidSafe(a.name + ip) + '"')
  }
  lines.push('')
  for (const s of drawable) {
    // 编号前缀对齐表格 step_index；禁用 '#'（mermaid 视为实体码起始会吞掉整行文本）
    const label = s.index + '. ' + mermaidSafe(s.action) + ' [' + fmtMs(s.latency_ms) + ']'
    // Always draw from_node -> to_node to match the detail table.
    if (s.status === 'failed') lines.push('    ' + s.from_node + '-x ' + s.to_node + ': ' + label)
    else if (s.status === 'partial') lines.push('    ' + s.from_node + '-->> ' + s.to_node + ': ' + label + ' (partial)')
    else if (s.status === 'skipped') lines.push('    ' + s.from_node + '-->> ' + s.to_node + ': ' + label + ' (skipped)')
    else lines.push('    ' + s.from_node + '->> ' + s.to_node + ': ' + label)
  }
  if (!timelineDetail.value?.success) {
    const first = timelineActors.value[0]?.id || 'browser'
    const last = timelineActors.value[timelineActors.value.length - 1]?.id || 'target'
    const safeErr = mermaidSafe(timelineDetail.value?.error_msg || failureAnalysis.value?.root_cause || t('diagnosis.connFailed'))
    lines.push('    Note over ' + first + ',' + last + ': ' + safeErr)
  }
  return lines.join('\n')
}

async function renderMermaid() {
  if (!mermaidContainer.value) return
  const code = buildMermaidCode()
  if (!code) { mermaidContainer.value.innerHTML = `<p style="color:var(--muted);text-align:center;padding:40px">${t('diagnosis.noTimelineData')}</p>`; return }
  mermaidContainer.value.innerHTML = ''
  const div = document.createElement('div')
  div.className = 'mermaid'
  div.id = 'mermaid-' + (++mermaidId)
  div.textContent = code
  mermaidContainer.value.appendChild(div)
  try {
    mermaid.initialize({ startOnLoad: false, theme: document.documentElement.classList.contains('dark') ? 'dark' : 'default', sequence: { mirrorActors: false } })
    await mermaid.run({ nodes: [div] })
  } catch (e) {
    console.error('mermaid error:', e)
    div.outerHTML = ''
    const fallback = document.createElement('pre')
    fallback.className = 'dp-mermaid-fallback'
    fallback.textContent = timelineSteps.value
      .filter(s => s.from_node && s.to_node && s.from_node !== s.to_node)
      .map(s => `${s.index}. ${s.from_node} → ${s.to_node} : ${mermaidSafe(s.action)} [${fmtMs(s.latency_ms)}]${s.status === 'skipped' ? ' (skipped)' : s.status === 'failed' ? ' (failed)' : ''}`)
      .join('\n')
    mermaidContainer.value.appendChild(fallback)
  }
}

function zoomIn() { zoomLevel.value = Math.min(2, zoomLevel.value + 0.1) }
function zoomOut() { zoomLevel.value = Math.max(0.5, zoomLevel.value - 0.1) }
function resetZoom() { zoomLevel.value = 1 }
function fmtMs(ms: number | null | undefined) {
  if (ms === null || ms === undefined || Number.isNaN(Number(ms))) return '-'
  const v = Number(ms)
  if (v <= 0) return '0ms'
  return v < 1 ? '<1ms' : v.toFixed(0) + 'ms'
}
function browserLabel(b: string) { return b || t('diagnosis.unknown') }
function pathModeLabel(m: string) {
  if (m === 'gateway') return t('diagnosis.gatewayConn')
  if (m === 'direct') return t('diagnosis.directConn')
  return m || '-'
}
// DC 链路标注：P2P(打洞直连) / relay(coturn 中继) / BUG(检测失败) / 空(未检测)
function wpLabel(p?: string) {
  if (p === 'P2P') return 'P2P'
  if (p === 'relay') return 'Relay'
  if (p === 'BUG') return 'BUG'
  return '-'
}
function wpBadgeClass(p?: string) {
  if (p === 'P2P') return 'dp-p2p'
  if (p === 'relay') return 'dp-relay'
  if (p === 'BUG') return 'dp-bug'
  return 'dp-wp-none'
}
// DC 链路语义按 path_mode 区分：直连=浏览器↔Agent；网关=网关↔Agent（浏览器经 WSS 连网关）
function wpHint(pathMode?: string) {
  return pathMode === 'gateway' ? t('diagnosis.wpHintGateway') : t('diagnosis.wpHintDirect')
}

onMounted(() => { loadRecords(); loadStats() })
</script>

<template>
<div class="dp">
  <div class="dp-tabs">
    <button :class="{ active: activeTab === 'records' }" @click="activeTab = 'records'; selectedRecord = null">{{ t('diagnosis.connectionRecords') }}</button>
    <button :class="{ active: activeTab === 'stats' }" @click="activeTab = 'stats'; selectedRecord = null">{{ t('diagnosis.statistics') }}</button>
  </div>

  <div v-if="activeTab === 'records' && !selectedRecord" class="dp-content">
    <div class="dp-filters">
      <select v-model="filterType" @change="currentPage = 1; loadRecords()">
        <option value="">{{ t('diagnosis.allTypes') }}</option>
        <option value="ssh">SSH</option>
        <option value="vnc">VNC</option>
        <option value="rdp">RDP</option>
      </select>
      <select v-model="pathFilter" @change="currentPage = 1; loadRecords()">
        <option value="">{{ t('diagnosis.allPaths') }}</option>
        <option value="P2P">{{ t('diagnosis.pathFilterP2P') }}</option>
        <option value="relay">{{ t('diagnosis.pathFilterRelay') }}</option>
        <option value="BUG">{{ t('diagnosis.pathFilterBug') }}</option>
        <option value="none">{{ t('diagnosis.pathFilterUnknown') }}</option>
      </select>
      <input v-model="searchQuery" :placeholder="t('diagnosis.searchPlaceholder')" @keyup.enter="currentPage = 1; loadRecords()" />
      <button @click="currentPage = 1; loadRecords()">{{ t('diagnosis.searchPlaceholder') }}</button>
    </div>
    <div class="dp-card">
      <table class="dp-table">
        <thead><tr>
          <th>#</th><th>{{ t('diagnosis.colName') }}</th><th>{{ t('diagnosis.colType') }}</th><th>{{ t('diagnosis.colInitType') }}</th><th>{{ t('diagnosis.colTarget') }}</th><th>{{ t('diagnosis.colGateway') }}</th><th>{{ t('diagnosis.colWebrtcPath') }}</th><th>{{ t('diagnosis.colStatus') }}</th><th>{{ t('diagnosis.colDuration') }}</th><th>{{ t('diagnosis.colDate') }}</th><th>{{ t('diagnosis.colAction') }}</th>
        </tr></thead>
        <tbody>
          <tr v-for="r in records" :key="r.id">
            <td><small>{{ r.id }}</small></td>
            <td>{{ r.conn_name || '-' }}</td>
            <td><span class="dp-badge">{{ r.conn_type }}</span></td>
            <td>{{ browserLabel(r.browser) }}</td>
            <td>{{ r.host }}:{{ r.port }}</td>
            <td><small>{{ pathModeLabel(r.path_mode) }}{{ r.gateway_ip ? ' · ' + r.gateway_ip : '' }}</small></td>
            <td><span class="dp-badge" :class="wpBadgeClass(r.webrtc_path)" :title="wpHint(r.path_mode)">{{ wpLabel(r.webrtc_path) }}</span></td>
            <td :class="r.success ? 'dp-ok' : 'dp-fail'">{{ r.success ? t('common.success') : t('common.failed') }}</td>
            <td>{{ fmtMs(r.duration_total) }}</td>
            <td><small>{{ r.created_at }}</small></td>
            <td><button class="dp-link" @click="viewTimeline(r)">{{ t('diagnosis.viewTimeline') }}</button></td>
          </tr>
          <tr v-if="!records.length"><td colspan="11" class="dp-empty">{{ t('diagnosis.noRecords') }}</td></tr>
        </tbody>
      </table>
      <div class="dp-pagination">
        <button :disabled="currentPage <= 1" @click="goToPage(currentPage - 1)">&#8249; {{ t('common.prev') }}</button>
        <button v-for="p in pageNumbers" :key="p" :class="{ active: p === currentPage }" @click="goToPage(p)">{{ p }}</button>
        <button :disabled="currentPage >= totalPages" @click="goToPage(currentPage + 1)">{{ t('common.next') }} &#8250;</button>
        <span class="dp-page-info">{{ t('diagnosis.pageTotal', { n: totalRecords, p: totalPages }) }}</span>
      </div>
    </div>
  </div>

  <div v-if="activeTab === 'records' && selectedRecord" class="dp-content dp-detail-scroll">
    <div class="dp-card dp-info">
      <button class="dp-back" @click="backToList()">&#8592; {{ t('diagnosis.backToList') }}</button>
      <div class="dp-task-id">#{{ timelineDetail?.id }}</div>
      <h3>{{ timelineDetail?.conn_name || timelineDetail?.host }}</h3>
      <div class="dp-meta">
        <span class="dp-badge">{{ timelineDetail?.conn_type }}</span>
        <span>{{ timelineDetail?.host }}:{{ timelineDetail?.port }}</span>
        <span>{{ pathModeLabel(timelineDetail?.path_mode) }}</span>
        <span class="dp-badge" :class="wpBadgeClass(timelineDetail?.webrtc_path)" :title="wpHint(timelineDetail?.path_mode)">{{ wpLabel(timelineDetail?.webrtc_path) }}</span>
        <span :class="timelineDetail?.success ? 'dp-ok' : 'dp-fail'">{{ timelineDetail?.success ? t('common.success') : t('common.failed') }}</span>
        <span v-if="timelineDetail?.duration_total">{{ t('diagnosis.totalDuration') }}: {{ fmtMs(timelineDetail.duration_total) }}</span>
      </div>
      <div class="dp-nodes">
        <span v-for="a in timelineActors" :key="a.id" class="dp-node">
          <span class="dp-node-dot" :style="{ background: a.color }"></span>
          {{ a.name }} <small>{{ a.ip }}</small>
        </span>
      </div>
    </div>
    <div v-if="failureAnalysis" class="dp-card dp-fail-card">
      <div class="dp-fail-icon">&#10005;</div>
      <div>
        <strong>{{ t('diagnosis.connFailed') }}</strong><template v-if="failureAnalysis.failed_step"> — Step {{ failureAnalysis.failed_step }}</template><template v-if="failureAnalysis.failed_node"> · {{ failureAnalysis.failed_node }}</template>
        <p>{{ timelineDetail?.error_msg || failureAnalysis.root_cause }}</p>
        <small>{{ t('diagnosis.completed') }} {{ failureAnalysis.completed_steps }}/{{ failureAnalysis.total_steps }} {{ t('diagnosis.steps') }}</small>
      </div>
    </div>
    <div class="dp-zoom">
      <button @click="zoomOut">&#8722;</button>
      <span>{{ (zoomLevel * 100).toFixed(0) }}%</span>
      <button @click="zoomIn">+</button>
      <button @click="resetZoom">{{ t('common.reset') }}</button>
    </div>
    <div class="dp-card dp-mermaid-wrap">
      <div ref="mermaidContainer" class="dp-mermaid" :style="{ transform: 'scale(' + zoomLevel + ')', transformOrigin: 'top left' }"></div>
    </div>
    <div class="dp-card">
      <h4>{{ t('diagnosis.interactionDetail') }}</h4>
      <table class="dp-table">
        <thead><tr><th>#</th><th>{{ t('diagnosis.protocol') }}</th><th>{{ t('diagnosis.sourceTarget') }}</th><th>{{ t('diagnosis.actionLabel') }}</th><th>{{ t('diagnosis.sourceIp') }}</th><th>{{ t('diagnosis.targetIp') }}</th><th>{{ t('diagnosis.colDuration') }}</th><th>{{ t('diagnosis.ratio') }}</th><th>{{ t('diagnosis.colStatus') }}</th></tr></thead>
        <tbody>
          <tr v-for="s in timelineSteps" :key="s.index" :class="s.status === 'failed' ? 'row-fail' : ''">
            <td>{{ s.index }}</td>
            <td>{{ s.protocol }}</td>
            <td>{{ s.from_node }} &#8594; {{ s.to_node }}</td>
            <td>{{ s.action }}</td>
            <td><small>{{ s.src_ip }}</small></td>
            <td><small>{{ s.dst_ip }}</small></td>
            <td>{{ fmtMs(s.latency_ms) }}</td>
            <td>{{ s.latency_pct }}%</td>
            <td :class="s.status === 'ok' ? 'dp-ok' : s.status === 'failed' ? 'dp-fail' : s.status === 'partial' ? 'dp-partial' : 'dp-skip'">
              {{ s.status === 'ok' ? '✓' : s.status === 'failed' ? '✕' : s.status === 'partial' ? '⚠' : '-' }}
              <small v-if="s.error_msg">{{ s.error_msg }}</small>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>

  <div v-if="activeTab === 'stats'" class="dp-content">
    <div class="dp-stats" v-if="stats">
      <div class="dp-stat-card"><div class="dp-stat-val">{{ stats.total }}</div><div class="dp-stat-label">{{ t('diagnosis.totalConn') }}</div></div>
      <div class="dp-stat-card"><div class="dp-stat-val">{{ stats.success_rate }}%</div><div class="dp-stat-label">{{ t('diagnosis.successRate') }}</div></div>
      <div class="dp-stat-card"><div class="dp-stat-val">{{ stats.avg_total }}ms</div><div class="dp-stat-label">{{ t('diagnosis.avgDuration') }}</div></div>
      <div class="dp-stat-card"><div class="dp-stat-val">{{ stats.fail_count }}</div><div class="dp-stat-label">{{ t('diagnosis.failCount') }}</div></div>
      <div class="dp-stat-card">
        <div class="dp-stat-val">{{ stats.p2p_rate ?? 0 }}%</div>
        <div class="dp-stat-label">{{ t('diagnosis.p2pRate') }}</div>
        <div class="dp-stat-sub">P2P {{ stats.p2p_count ?? 0 }} · Relay {{ stats.relay_count ?? 0 }}</div>
      </div>
    </div>
    <div v-else class="dp-card dp-empty">{{ t('common.loading') }}</div>
  </div>
</div>
</template>

<style scoped>
.dp { padding: 0; }
.dp-tabs { display: flex; gap: 4px; margin-bottom: 16px; border-bottom: 1px solid var(--border); padding-bottom: 0; }
.dp-tabs button { padding: 8px 16px; border: none; background: none; color: var(--muted); cursor: pointer; border-bottom: 2px solid transparent; font-size: 14px; }
.dp-tabs button.active { color: var(--accent); border-bottom-color: var(--accent); font-weight: 600; }
.dp-content { display: flex; flex-direction: column; gap: 16px; }
.dp-detail-scroll { max-height: calc(100vh - 180px); overflow-y: auto; padding-right: 4px; }
.dp-card { background: var(--panel); border: 1px solid var(--border); border-radius: 12px; padding: 16px; }
.dp-back { border: none; background: none; color: var(--accent); cursor: pointer; font-size: 14px; margin-bottom: 8px; padding: 0; }
.dp-task-id { font-size: 20px; font-weight: 700; color: var(--fg); margin-bottom: 4px; }
.dp-info h3 { margin: 4px 0 8px; }
.dp-meta { display: flex; gap: 12px; align-items: center; flex-wrap: wrap; font-size: 14px; color: var(--muted); }
.dp-nodes { display: flex; gap: 16px; margin-top: 12px; flex-wrap: wrap; }
.dp-node { display: flex; align-items: center; gap: 6px; font-size: 13px; }
.dp-node-dot { width: 10px; height: 10px; border-radius: 50%; display: inline-block; }
.dp-fail-card { display: flex; gap: 12px; align-items: flex-start; background: #fef2f2; border-color: #fecaca; }
.dp-fail-icon { font-size: 24px; color: #dc2626; font-weight: bold; }
.dp-fail-card p { color: #dc2626; margin: 4px 0; }
.dp-zoom { display: flex; align-items: center; gap: 8px; font-size: 13px; color: var(--muted); }
.dp-zoom button { padding: 4px 10px; border: 1px solid var(--border); border-radius: 6px; background: var(--panel); cursor: pointer; }
.dp-mermaid-wrap { overflow: auto; min-height: 350px; }
.dp-mermaid { min-width: 100%; min-height: 300px; }
.dp-mermaid-fallback { background: var(--panel-2); border: 1px solid var(--border); border-radius: 8px; padding: 14px 16px; font-size: 13px; line-height: 1.8; color: var(--fg-2); white-space: pre-wrap; font-family: monospace; }
.dp-table { width: 100%; border-collapse: collapse; font-size: 13px; }
.dp-table th { text-align: left; padding: 8px; border-bottom: 2px solid var(--border); color: var(--muted); font-weight: 600; text-transform: uppercase; font-size: 11px; white-space: nowrap; }
.dp-table td { padding: 8px; border-bottom: 1px solid var(--border); }
.dp-table .row-fail { background: #fef2f2; }
.dp-link { border: none; background: none; color: var(--accent); cursor: pointer; text-decoration: underline; }
.dp-empty { text-align: center; color: var(--muted); padding: 20px; }
.dp-filters { display: flex; gap: 8px; align-items: center; }
.dp-filters select, .dp-filters input { padding: 6px 10px; border: 1px solid var(--border); border-radius: 6px; background: var(--panel); color: var(--fg); font-size: 13px; }
.dp-filters button { padding: 6px 14px; border: 1px solid var(--border); border-radius: 6px; background: var(--panel); cursor: pointer; }
.dp-pagination { display: flex; align-items: center; gap: 4px; justify-content: center; margin-top: 12px; }
.dp-pagination button { padding: 4px 10px; border: 1px solid var(--border); border-radius: 6px; background: var(--panel); color: var(--fg); cursor: pointer; font-size: 13px; }
.dp-pagination button.active { background: var(--accent); color: #fff; border-color: var(--accent); }
.dp-pagination button:disabled { opacity: 0.4; cursor: not-allowed; }
.dp-page-info { margin-left: 12px; color: var(--muted); font-size: 13px; }
.dp-stats { display: grid; grid-template-columns: repeat(auto-fill, minmax(180px, 1fr)); gap: 16px; }
.dp-stat-card { background: var(--panel); border: 1px solid var(--border); border-radius: 12px; padding: 20px; text-align: center; }
.dp-stat-val { font-size: 28px; font-weight: 700; color: var(--accent); }
.dp-stat-label { font-size: 13px; color: var(--muted); margin-top: 4px; }
.dp-badge { display: inline-block; padding: 2px 8px; border-radius: 4px; font-size: 12px; background: var(--accent-soft, #e8f0fe); color: var(--accent); font-weight: 600; text-transform: uppercase; }
.dp-badge.dp-p2p { background: #dcfce7; color: #16a34a; }
.dp-badge.dp-relay { background: #ffedd5; color: #ea580c; }
.dp-badge.dp-bug { background: #fee2e2; color: #dc2626; }
.dp-badge.dp-wp-none { background: var(--panel-2, #f1f5f9); color: var(--muted); }
.dp-stat-sub { font-size: 11px; color: var(--muted); margin-top: 6px; }
.dp-ok { color: #16a34a; font-weight: 600; }
.dp-fail { color: #dc2626; font-weight: 600; }
.dp-partial { color: #d97706; font-weight: 600; }
.dp-skip { color: var(--muted); }
</style>
