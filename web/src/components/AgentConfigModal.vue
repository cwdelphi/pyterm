<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, inject } from "vue"
import { useI18n } from 'vue-i18n'
import { api, type AgentConfig, type AgentIceConfig, type AgentNetInfo } from "../api"

const { t } = useI18n()

const props = defineProps<{ agentId: string; agentName?: string; agentOnline?: boolean; readonly?: boolean }>()
// 共享来的 Agent：仅可查看，保存/编辑/重置 Token 均禁用
const readOnly = computed(() => props.readonly === true)
const emit = defineEmits(["close", "updated", "token"])
const toast = inject<any>("toast")

const agentName = ref(props.agentName || "")
const agentOnline = ref<boolean | undefined>(props.agentOnline)
const allAgents = ref<any[]>([])
const coturnList = ref<any[]>([])

// 方案 §4.1 config_json.ice 缺省值（与后端 IceOptReq 对齐）
function defaultIceConfig(): AgentIceConfig {
  return {
    enabled: true,
    mode: 'auto',
    keep: [],
    drop_prefix: [],
    allow_tailscale: false,
    auto_fallback: true,
    path_cache: true,
    conn_reuse: false,
  }
}

const ICE_MODES = ['auto', 'custom', 'blacklist', 'off'] as const

const config = ref<AgentConfig>({
  ws_reconnect_interval: 5,
  ws_heartbeat_interval: 30,
  ice_cooldown: 2,
  log_level: "info",
  tunnels: [],
  ice: defaultIceConfig(),
})
const loading = ref(true)
const saving = ref(false)
const scanning = ref(false)
const activeTab = ref<'info' | 'basic' | 'tunnels' | 'ice'>('info')
const netInfo = ref<AgentNetInfo | null>(null)
const showCloseConfirm = ref(false)
const showRegenConfirm = ref(false)
const snapshot = ref("")
const editForm = ref({ name: "", coturn_id: "", remark: "" })
const editSnapshot = ref(JSON.stringify({ name: "", coturn_id: "", remark: "" }))

const levels = ['debug', 'info', 'warn', 'error']
const tunnelCount = computed(() => config.value.tunnels.length)

function editPayloadOf() {
  return {
    name: editForm.value.name,
    coturn_id: editForm.value.coturn_id,
    remark: editForm.value.remark,
  }
}

function mappedTunnels() {
  return config.value.tunnels.map((tn: any) => ({
    id: tn.id,
    name: tn.name,
    protocol: tn.protocol,
    local_port: tn.local_port,
    target_addr: tn.protocol === 'socks5' ? "" : joinTargetAddr(tn.target_host || "", tn.target_port || ""),
    target_agent_id: tn.target_agent_id,
    enabled: tn.enabled,
    socks_username: tn.socks_username || "",
    socks_password: tn.socks_password || "",
  }))
}

function payloadOf() {
  const all = mappedTunnels()
  return {
    ws_reconnect_interval: config.value.ws_reconnect_interval,
    ws_heartbeat_interval: config.value.ws_heartbeat_interval,
    ice_cooldown: config.value.ice_cooldown,
    log_level: config.value.log_level,
    // 端到端插件协议: 按协议归桶存储, 不再发送顶层 tunnels(内嵌SSH服务已移除)
    plugins: {
      tunnel: { tunnels: all.filter((t: any) => t.protocol !== 'socks5') },
      socks5: { tunnels: all.filter((t: any) => t.protocol === 'socks5') },
    },
    ice: config.value.ice,
  }
}

// 渲染期求值：任何 payloadOf 异常都不能让弹窗被 Vue 卸载（异常在 save() 内仍会抛出并 toast）
const configDirty = computed(() => {
  if (loading.value) return false
  try {
    return JSON.stringify(payloadOf()) !== snapshot.value
  } catch (e) {
    console.error('[agentConfig] payloadOf failed:', e)
    return false
  }
})
const editDirty = computed(() => !loading.value && JSON.stringify(editPayloadOf()) !== editSnapshot.value)
const dirty = computed(() => configDirty.value || editDirty.value)

async function loadConfig() {
  loading.value = true
  try {
    const agentsData = await api.adminListAgents()
    allAgents.value = agentsData.agents
    const agent = agentsData.agents.find((a: any) => a.id === props.agentId)
    if (agent) {
      if (!agentName.value) agentName.value = agent.name
      if (agentOnline.value === undefined) agentOnline.value = agent.online
      editForm.value = {
        name: agent.name || "",
        coturn_id: agent.coturn_id || "",
        remark: agent.remark || "",
      }
    } else {
      editForm.value = { name: agentName.value, coturn_id: "", remark: "" }
    }
    editSnapshot.value = JSON.stringify(editPayloadOf())

    try {
      const coturnData = await api.adminListCoturn()
      coturnList.value = coturnData.servers || []
    } catch {
      coturnList.value = []
    }

    const data = await api.adminGetAgentConfig(props.agentId)
    if (data.config) {
      const plugins: any = (data.config as any).plugins || {}
      // 新格式按插件桶取; 旧格式回退顶层 tunnels(md 已懒迁移, 双保险)
      const rawList: any[] = (plugins.tunnel?.tunnels || []).concat(plugins.socks5?.tunnels || [])
      const tunnelList = rawList.length ? rawList : (data.config.tunnels || [])
      config.value = {
        ws_reconnect_interval: data.config.ws_reconnect_interval ?? 5,
        ws_heartbeat_interval: data.config.ws_heartbeat_interval ?? 30,
        ice_cooldown: data.config.ice_cooldown ?? 2,
        log_level: data.config.log_level ?? "info",
        tunnels: tunnelList.map((tn: any) => ({
          id: tn.id || crypto.randomUUID().slice(0, 8),
          name: tn.name || "",
          protocol: tn.protocol || "tcp",
          local_port: tn.local_port || 0,
          target_host: splitTargetHost(tn.target_addr || ""),
          target_port: splitTargetPort(tn.target_addr || ""),
          target_agent_id: tn.target_agent_id || "",
          enabled: tn.enabled !== false,
          socks_username: tn.socks_username || "",
          socks_password: tn.socks_password || "",
        })),
        ice: { ...defaultIceConfig(), ...(data.config.ice || {}) },
      }
      netInfo.value = (data.config.net_info as AgentNetInfo) || null
    }
    snapshot.value = JSON.stringify(payloadOf())
  } catch (e: any) {
    toast?.error(e.message)
  }
  loading.value = false
}

// ── ICE 优化页签（P1，方案 §4.1/§4.2）────────────────────────
function splitList(v: string): string[] {
  return String(v ?? '').split(/[,\s]+/).map(s => s.trim()).filter(Boolean)
}

const keepText = computed({
  get: () => (config.value.ice?.keep || []).join(', '),
  set: (v: string) => { if (config.value.ice) config.value.ice.keep = splitList(v) },
})
const dropText = computed({
  get: () => (config.value.ice?.drop_prefix || []).join(', '),
  set: (v: string) => { if (config.value.ice) config.value.ice.drop_prefix = splitList(v) },
})

const isCustom = computed(() => config.value.ice?.mode === 'custom')
const isListMode = computed(() => ['custom', 'blacklist'].includes(config.value.ice?.mode || ''))

async function scanIce() {
  if (readOnly.value) return
  scanning.value = true
  try {
    const r = await api.adminScanAgentIce(props.agentId)
    netInfo.value = r.net_info || null
    toast?.success(t('agentConfig.iceScanDone'))
  } catch (e: any) {
    toast?.error(e.message)
  }
  scanning.value = false
}

// ── P2: 路径缓存清空 + 建连效果看板（方案 §4.5 / §7.2）────────
const clearingCache = ref(false)

async function clearPathCache() {
  if (readOnly.value || clearingCache.value) return
  clearingCache.value = true
  try {
    await api.adminUpdateAgentConfig(props.agentId, { ...payloadOf(), ice_cache_clear: true })
    snapshot.value = JSON.stringify(payloadOf())
    // 乐观刷新：Agent 回推的 network_info 会在下次打开时覆盖
    if (netInfo.value) {
      const n: any = { ...netInfo.value }
      delete n.last_pair
      delete n.ladder
      delete n.path_cache
      netInfo.value = n
    }
    toast?.success(t('agentConfig.iceCacheCleared'))
  } catch (e: any) {
    toast?.error(e.message)
  }
  clearingCache.value = false
}

const iceStats = computed(() => {
  const n = netInfo.value as any
  if (!n) return null
  const ladder = n.ladder || null
  const pair = n.last_pair || null
  const cache = Array.isArray(n.path_cache) ? n.path_cache : []
  if (!ladder && !pair && !cache.length) return null
  return {
    level: ladder?.level ?? 0,
    fallbacks: ladder?.fallbacks ?? 0,
    l3: ladder?.l3 ?? 0,
    cacheHits: ladder?.cache_hits ?? 0,
    iface: pair?.local_iface || '',
    connType: pair?.conn_type || '',
    cacheEntries: cache.length,
  }
})

function addTunnel() {
  if (readOnly.value) return
  config.value.tunnels.push({
    id: crypto.randomUUID().slice(0, 8),
    name: "",
    protocol: "tcp",
    local_port: 0,
    target_host: "",
    target_port: "",
    target_agent_id: "",
    enabled: true,
    socks_username: "",
    socks_password: "",
  })
  activeTab.value = 'tunnels'
}

function removeTunnel(idx: number) {
  if (readOnly.value) return
  config.value.tunnels.splice(idx, 1)
}

function onProtocolChange(tn: any) {
  if (tn.protocol === 'socks5' && !tn.local_port) {
    tn.local_port = 2080
  }
}

function splitTargetHost(addr: string): string {
  if (!addr) return ""
  const m = addr.match(/^\[([^\]]+)\](?::(\d+))?$/)  // IPv6 [::1]:22
  if (m) return m[1]
  const idx = addr.lastIndexOf(":")
  if (idx <= 0) return addr
  return addr.slice(0, idx)
}

function splitTargetPort(addr: string): string {
  if (!addr) return ""
  const m = addr.match(/^\[[^\]]+\](?::(\d+))?$/)  // IPv6 [::1]:22
  if (m) return m[1] || ""
  const idx = addr.lastIndexOf(":")
  if (idx <= 0) return ""
  return addr.slice(idx + 1)
}

function joinTargetAddr(host: unknown, port: unknown): string {
  host = String(host ?? "").trim()
  port = String(port ?? "").trim()
  if (!host) return port ? ":" + port : ""
  if (host.includes(":") && !host.startsWith("[")) host = "[" + host + "]"  // IPv6
  if (port) return host + ":" + port
  return host
}

async function save() {
  if (readOnly.value) return
  saving.value = true
  try {
    if (editDirty.value) {
      await api.adminUpdateAgent(props.agentId, {
        name: editForm.value.name,
        coturn_id: editForm.value.coturn_id,
        remark: editForm.value.remark,
      })
      editSnapshot.value = JSON.stringify(editPayloadOf())
      agentName.value = editForm.value.name
      emit("updated", { name: editForm.value.name })
    }
    if (configDirty.value) {
      await api.adminUpdateAgentConfig(props.agentId, payloadOf())
      snapshot.value = JSON.stringify(payloadOf())
      toast?.success(t('agentConfig.saveSuccess'))
    } else if (editDirty.value) {
      toast?.success(t('admin.updated'))
    }
    emit("close")
  } catch (e: any) {
    toast?.error(e.message)
  }
  saving.value = false
}

async function regenerateToken() {
  if (readOnly.value) return
  try {
    const r = await api.adminRegenerateToken(props.agentId)
    showRegenConfirm.value = false
    toast?.success(t('admin.tokenRegenerated'))
    emit("token", r.token)
  } catch (e: any) {
    toast?.error(e.message)
  }
}

function requestClose() {
  if (saving.value) return
  if (dirty.value) showCloseConfirm.value = true
  else emit("close")
}

function confirmClose() {
  showCloseConfirm.value = false
  emit("close")
}

function onKeydown(e: KeyboardEvent) {
  if (e.key !== "Escape") return
  if (showRegenConfirm.value) { showRegenConfirm.value = false; return }
  if (showCloseConfirm.value) { showCloseConfirm.value = false; return }
  requestClose()
}

onMounted(() => {
  window.addEventListener("keydown", onKeydown)
  loadConfig()
})
onUnmounted(() => window.removeEventListener("keydown", onKeydown))
</script>

<template>
  <Teleport to="body">
    <div class="cfg-mask" @click.self="requestClose">
      <div class="cfg-box">
        <!-- ── Header ── -->
        <div class="cfg-header">
          <div class="cfg-head-left">
            <div class="cfg-icon">⚙️</div>
            <div class="cfg-head-text">
              <div class="cfg-title">{{ t('agentConfig.title') }}</div>
              <div class="cfg-sub">
                <span class="cfg-agent-name">{{ agentName || agentId }}</span>
                <span class="cfg-id">{{ agentId }}</span>
                <span class="cfg-status">
                  <span class="cfg-dot" :class="{ on: agentOnline }"></span>
                  {{ agentOnline ? t('common.online') : t('common.offline') }}
                </span>
              </div>
            </div>
          </div>
          <button class="cfg-close" :title="t('common.close')" @click="requestClose">✕</button>
        </div>

        <!-- ── Tabs ── -->
        <div v-if="readOnly" class="cfg-readonly-banner">🔒 {{ t('admin.sharedReadonly') }}</div>
        <div class="cfg-tabs">
          <button class="cfg-tab" :class="{ active: activeTab === 'info' }" @click="activeTab = 'info'">
            {{ t('agentConfig.tabInfo') }}
          </button>
          <button class="cfg-tab" :class="{ active: activeTab === 'basic' }" @click="activeTab = 'basic'">
            {{ t('agentConfig.tabBasic') }}
          </button>
          <button class="cfg-tab" :class="{ active: activeTab === 'tunnels' }" @click="activeTab = 'tunnels'">
            {{ t('agentConfig.tabTunnels') }}<span class="cfg-tab-count" v-if="tunnelCount">· {{ tunnelCount }}</span>
          </button>
          <button class="cfg-tab" :class="{ active: activeTab === 'ice' }" @click="activeTab = 'ice'">
            {{ t('agentConfig.tabIce') }}
          </button>
        </div>

        <!-- ── Body ── -->
        <div class="cfg-body" :class="{ 'cfg-readonly': readOnly }">
          <div v-if="loading" class="cfg-loading">
            <div class="cfg-spinner"></div>
            <span>{{ t('common.loading') }}</span>
          </div>

          <template v-else>
            <!-- Tab0: 基本信息 -->
            <div v-show="activeTab === 'info'" class="cfg-panel">
              <div class="cfg-section-head">
                <div>
                  <h3>{{ t('agentConfig.tabInfo') }}</h3>
                  <p>{{ t('agentConfig.tabInfoDesc') }}</p>
                </div>
              </div>
              <div class="cfg-form">
                <div class="cfg-form-row">
                  <label>Agent ID</label>
                  <input :value="agentId" readonly />
                </div>
                <div class="cfg-form-row">
                  <label>{{ t('common.name') }}</label>
                  <input v-model="editForm.name" :placeholder="t('admin.displayName')" :disabled="readOnly" />
                </div>
                <div class="cfg-form-row">
                  <label>{{ t('admin.coturnServer') }}</label>
                  <select v-model="editForm.coturn_id" :disabled="readOnly">
                    <option value="">{{ t('admin.noCoturn') }}</option>
                    <option v-for="c in coturnList" :key="c.id" :value="c.id">{{ c.name }}</option>
                  </select>
                </div>
                <div class="cfg-form-row">
                  <label>{{ t('admin.optionalRemark') }}</label>
                  <input v-model="editForm.remark" :placeholder="t('admin.optionalRemark')" :disabled="readOnly" />
                </div>
              </div>
              <div class="cfg-danger-zone">
                <div class="cfg-danger-text">{{ t('admin.regenTokenWarning') }}</div>
                <button class="btn danger" :disabled="readOnly" :title="readOnly ? t('admin.sharedReadonly') : ''" @click="showRegenConfirm = true">🔄 {{ t('admin.regenToken') }}</button>
              </div>
            </div>

            <!-- Tab1: 基础参数 -->
            <div v-show="activeTab === 'basic'" class="cfg-panel">
              <div class="cfg-section-head">
                <div>
                  <h3>{{ t('agentConfig.websocketParams') }}</h3>
                  <p>{{ t('agentConfig.websocketDesc') }}</p>
                </div>
              </div>
              <div class="cfg-card-grid">
                <div class="cfg-card">
                  <div class="cfg-card-head">↻ {{ t('agentConfig.reconnectInterval') }}</div>
                  <div class="cfg-card-body">
                    <div class="cfg-param-row">
                      <input v-model.number="config.ws_reconnect_interval" type="number" min="1" max="60" class="cfg-param-input" />
                      <span class="cfg-param-unit">s</span>
                    </div>
                    <p class="cfg-param-hint">{{ t('agentConfig.reconnectDesc') }}</p>
                  </div>
                </div>
                <div class="cfg-card">
                  <div class="cfg-card-head">♡ {{ t('agentConfig.heartbeatInterval') }}</div>
                  <div class="cfg-card-body">
                    <div class="cfg-param-row">
                      <input v-model.number="config.ws_heartbeat_interval" type="number" min="5" max="120" class="cfg-param-input" />
                      <span class="cfg-param-unit">s</span>
                    </div>
                    <p class="cfg-param-hint">{{ t('agentConfig.heartbeatDesc') }}</p>
                  </div>
                </div>
                <div class="cfg-card">
                  <div class="cfg-card-head">❄ {{ t('agentConfig.iceCooling') }}</div>
                  <div class="cfg-card-body">
                    <div class="cfg-param-row">
                      <input v-model.number="config.ice_cooldown" type="number" min="0" max="30" class="cfg-param-input" />
                      <span class="cfg-param-unit">s</span>
                    </div>
                    <p class="cfg-param-hint">{{ t('agentConfig.iceCoolingDesc') }}</p>
                  </div>
                </div>
                <div class="cfg-card">
                  <div class="cfg-card-head">☰ {{ t('agentConfig.logLevel') }}</div>
                  <div class="cfg-card-body">
                    <div class="cfg-level-pills">
                      <button
                        v-for="lv in levels" :key="lv"
                        class="cfg-level-pill" :class="{ active: config.log_level === lv }"
                        @click="config.log_level = lv"
                      >{{ lv }}</button>
                    </div>
                    <p class="cfg-param-hint">{{ t('agentConfig.hotConfigTip') }}</p>
                  </div>
                </div>
              </div>
            </div>

            <!-- Tab2: 隧道管理 -->
            <div v-show="activeTab === 'tunnels'" class="cfg-panel">
              <div class="cfg-section-head">
                <div>
                  <h3>{{ t('agentConfig.tunnelMgr') }}</h3>
                  <p>{{ t('agentConfig.tunnelDesc') }}</p>
                </div>
                <button class="btn" @click="addTunnel">{{ t('agentConfig.addTunnel') }}</button>
              </div>

              <div v-if="!config.tunnels.length" class="empty-state-v2">
                <span class="empty-icon">🔗</span>
                <div class="empty-title">{{ t('agentConfig.noTunnels') }}</div>
                <div class="empty-desc">{{ t('agentConfig.addTunnelHint') }}</div>
              </div>

              <div v-for="(tn, idx) in config.tunnels" :key="tn.id" class="cfg-tunnel-card">
                <div class="cfg-tunnel-header">
                  <div class="cfg-tunnel-title">
                    <span class="cfg-tunnel-idx">#{{ idx + 1 }}</span>
                    <input v-model="tn.name" class="cfg-tunnel-name" :placeholder="t('agentConfig.tunnelName')" />
                  </div>
                  <div class="cfg-tunnel-actions">
                    <label class="cfg-toggle">
                      <input type="checkbox" v-model="tn.enabled" />
                      <span class="cfg-toggle-slider"></span>
                    </label>
                    <button class="text-btn danger" @click="removeTunnel(idx)">{{ t('common.delete') }}</button>
                  </div>
                </div>
                <div class="cfg-tunnel-body">
                  <div class="cfg-tunnel-row">
                    <div class="cfg-tunnel-field">
                      <label>{{ t('agentConfig.protocol') }}</label>
                      <select v-model="tn.protocol" class="cfg-field-select" @change="onProtocolChange(tn)">
                        <option value="tcp">TCP</option>
                        <option value="udp">UDP</option>
                        <option value="socks5">SOCKS5</option>
                      </select>
                    </div>
                    <div class="cfg-tunnel-field">
                      <label>{{ t('agentConfig.localPort') }}</label>
                      <input v-model.number="tn.local_port" type="number" min="1" max="65535" :placeholder="tn.protocol === 'socks5' ? '2080' : '2222'" />
                    </div>
                    <template v-if="tn.protocol !== 'socks5'">
                      <div class="cfg-tunnel-field grow2">
                        <label>{{ t('agentConfig.targetAddr') }}</label>
                        <input v-model="tn.target_host" :placeholder="t('agentConfig.targetAddrPlaceholder')" />
                      </div>
                      <div class="cfg-tunnel-field narrow">
                        <label>{{ t('agentConfig.targetPort') }}</label>
                        <input v-model.number="tn.target_port" type="number" min="1" max="65535" placeholder="22" />
                      </div>
                    </template>
                  </div>
                  <div class="cfg-tunnel-row" v-if="tn.protocol === 'socks5'">
                    <div class="cfg-tunnel-field">
                      <label>{{ t('agentConfig.socksAuth') }} <span class="cfg-label-muted">{{ t('agentConfig.socksAuthTip') }}</span></label>
                      <input v-model="tn.socks_username" :placeholder="t('agentConfig.socksUser')" />
                    </div>
                    <div class="cfg-tunnel-field">
                      <label>{{ t('agentConfig.socksPass') }}</label>
                      <input v-model="tn.socks_password" type="password" :placeholder="t('agentConfig.socksPass')" />
                    </div>
                  </div>
                  <div class="cfg-tunnel-row">
                    <div class="cfg-tunnel-field">
                      <label>{{ t('agentConfig.targetAgentId') }} <span class="cfg-label-muted">{{ t('agentConfig.targetAgentTip') }}</span></label>
                      <select v-model="tn.target_agent_id" class="cfg-field-select">
                        <option value="">{{ t('agentConfig.localDefault') }}</option>
                        <option v-for="ag in allAgents" :key="ag.id" :value="ag.id">{{ ag.name }} ({{ ag.id }})</option>
                      </select>
                    </div>
                  </div>
                </div>
              </div>
            </div>

            <!-- Tab3: ICE优化（P1，方案 §4.1 配置模型 / §4.2 上下行通道） -->
            <div v-show="activeTab === 'ice'" class="cfg-panel">
              <div class="cfg-section-head">
                <div>
                  <h3>{{ t('agentConfig.iceMgr') }}</h3>
                  <p>{{ t('agentConfig.iceMgrDesc') }}</p>
                </div>
              </div>

              <div class="cfg-card-grid">
                <div class="cfg-card">
                  <div class="cfg-card-head">🛡 {{ t('agentConfig.iceEnabled') }}</div>
                  <div class="cfg-card-body">
                    <div class="cfg-param-row">
                      <label class="cfg-toggle">
                        <input type="checkbox" v-model="config.ice!.enabled" />
                        <span class="cfg-toggle-slider"></span>
                      </label>
                      <span class="cfg-ice-state">{{ config.ice!.enabled ? t('agentConfig.iceOn') : t('agentConfig.iceOff') }}</span>
                    </div>
                    <p class="cfg-param-hint">{{ t('agentConfig.iceEnabledDesc') }}</p>
                  </div>
                </div>

                <div class="cfg-card">
                  <div class="cfg-card-head">🎯 {{ t('agentConfig.iceMode') }}</div>
                  <div class="cfg-card-body">
                    <div class="cfg-level-pills">
                      <button
                        v-for="m in ICE_MODES" :key="m"
                        class="cfg-level-pill" :class="{ active: config.ice!.mode === m }"
                        :disabled="!config.ice!.enabled"
                        @click="config.ice!.mode = m"
                      >{{ t('agentConfig.iceMode_' + m) }}</button>
                    </div>
                    <p class="cfg-param-hint">{{ t('agentConfig.iceModeDesc' + (config.ice!.mode === 'auto' ? 'Auto' : config.ice!.mode === 'custom' ? 'Custom' : config.ice!.mode === 'blacklist' ? 'Blacklist' : 'Off')) }}</p>
                  </div>
                </div>

                <div class="cfg-card">
                  <div class="cfg-card-head">🌐 {{ t('agentConfig.iceTailscale') }}</div>
                  <div class="cfg-card-body">
                    <div class="cfg-param-row">
                      <label class="cfg-toggle">
                        <input type="checkbox" v-model="config.ice!.allow_tailscale" />
                        <span class="cfg-toggle-slider"></span>
                      </label>
                      <span class="cfg-ice-state">{{ config.ice!.allow_tailscale ? t('agentConfig.iceOn') : t('agentConfig.iceOff') }}</span>
                    </div>
                    <p class="cfg-param-hint">{{ t('agentConfig.iceTailscaleDesc') }}</p>
                  </div>
                </div>

                <div class="cfg-card">
                  <div class="cfg-card-head">🪜 {{ t('agentConfig.iceFallback') }}</div>
                  <div class="cfg-card-body">
                    <div class="cfg-param-row">
                      <label class="cfg-toggle">
                        <input type="checkbox" v-model="config.ice!.auto_fallback" />
                        <span class="cfg-toggle-slider"></span>
                      </label>
                      <span class="cfg-ice-state">{{ config.ice!.auto_fallback ? t('agentConfig.iceOn') : t('agentConfig.iceOff') }}</span>
                    </div>
                    <p class="cfg-param-hint">{{ t('agentConfig.iceFallbackDesc') }}</p>
                  </div>
                </div>

                <div class="cfg-card">
                  <div class="cfg-card-head">⚡ {{ t('agentConfig.icePathCache') }}</div>
                  <div class="cfg-card-body">
                    <div class="cfg-param-row">
                      <label class="cfg-toggle">
                        <input type="checkbox" v-model="config.ice!.path_cache" />
                        <span class="cfg-toggle-slider"></span>
                      </label>
                      <span class="cfg-ice-state">{{ config.ice!.path_cache ? t('agentConfig.iceOn') : t('agentConfig.iceOff') }}</span>
                    </div>
                    <div class="cfg-param-row">
                      <button class="btn" :disabled="readOnly || clearingCache" @click="clearPathCache">
                        {{ clearingCache ? t('agentConfig.iceCacheClearing') : t('agentConfig.iceCacheClear') }}
                      </button>
                    </div>
                    <p class="cfg-param-hint">{{ t('agentConfig.icePathCacheDesc') }}</p>
                  </div>
                </div>

                <div class="cfg-card">
                  <div class="cfg-card-head">♻ {{ t('agentConfig.iceConnReuse') }} <span class="cfg-ice-badge">{{ t('agentConfig.iceConnReuseBadge') }}</span></div>
                  <div class="cfg-card-body">
                    <p class="cfg-param-hint">{{ t('agentConfig.iceConnReuseDesc') }}</p>
                  </div>
                </div>
              </div>

              <!-- P2 §7.2: 建连效果看板（Agent 自统计随 network_info 上报） -->
              <div class="cfg-section-head">
                <div>
                  <h3>{{ t('agentConfig.iceEffect') }}</h3>
                  <p>{{ t('agentConfig.iceEffectDesc') }}</p>
                </div>
              </div>
              <div v-if="!iceStats" class="empty-state-v2">
                <span class="empty-icon">📊</span>
                <div class="empty-title">{{ t('agentConfig.iceStatEmpty') }}</div>
                <div class="empty-desc">{{ t('agentConfig.iceStatEmptyDesc') }}</div>
              </div>
              <div v-else class="cfg-ice-meta">
                <span class="cfg-ice-chip"><b>{{ t('agentConfig.iceStatLevel') }}</b> L{{ iceStats.level || 1 }}</span>
                <span class="cfg-ice-chip"><b>{{ t('agentConfig.iceStatFallback') }}</b> {{ iceStats.fallbacks }}</span>
                <span class="cfg-ice-chip"><b>{{ t('agentConfig.iceStatL3') }}</b> {{ iceStats.l3 }}</span>
                <span class="cfg-ice-chip"><b>{{ t('agentConfig.iceStatCacheHit') }}</b> {{ iceStats.cacheHits }}</span>
                <span class="cfg-ice-chip"><b>{{ t('agentConfig.iceStatLastPair') }}</b> {{ iceStats.iface || t('agentConfig.iceStatNoPair') }}<template v-if="iceStats.connType"> · {{ iceStats.connType }}</template></span>
                <span class="cfg-ice-chip"><b>{{ t('agentConfig.iceStatCacheEntries') }}</b> {{ iceStats.cacheEntries }}</span>
              </div>

              <div v-if="isListMode" class="cfg-form">
                <div v-if="isCustom" class="cfg-form-row">
                  <label>{{ t('agentConfig.iceKeep') }}</label>
                  <input v-model="keepText" :placeholder="t('agentConfig.iceKeepPh')" :disabled="readOnly" />
                </div>
                <div class="cfg-form-row">
                  <label>{{ t('agentConfig.iceDropPrefix') }}</label>
                  <input v-model="dropText" :placeholder="t('agentConfig.iceDropPh')" :disabled="readOnly" />
                </div>
              </div>

              <div class="cfg-section-head">
                <div>
                  <h3>{{ t('agentConfig.iceIfList') }}</h3>
                  <p>{{ t('agentConfig.iceIfListDesc') }}</p>
                </div>
                <button class="btn" :disabled="readOnly || scanning" @click="scanIce">
                  {{ scanning ? t('agentConfig.iceScanning') : t('agentConfig.iceScan') }}
                </button>
              </div>

              <div v-if="!netInfo" class="empty-state-v2">
                <span class="empty-icon">📡</span>
                <div class="empty-title">{{ t('agentConfig.iceNoScan') }}</div>
                <div class="empty-desc">{{ t('agentConfig.iceNoScanDesc') }}</div>
              </div>

              <template v-else>
                <div class="cfg-ice-meta">
                  <span class="cfg-ice-chip"><b>{{ t('agentConfig.iceIfHash') }}</b> {{ netInfo.if_hash || '-' }}</span>
                  <span class="cfg-ice-chip"><b>{{ t('agentConfig.iceScannedAt') }}</b> {{ netInfo.scanned_at || '-' }}</span>
                  <span class="cfg-ice-chip"><b>{{ t('agentConfig.icePairs') }}</b> {{ netInfo.est_pairs_before ?? '-' }} → {{ netInfo.est_pairs_after ?? '-' }}</span>
                  <span class="cfg-ice-chip"><b>{{ t('agentConfig.iceCandidates') }}</b> {{ netInfo.addr_before ?? '-' }} → {{ netInfo.addr_after ?? '-' }}</span>
                </div>
                <div v-if="netInfo.valid === false" class="cfg-ice-warn">⚠ {{ netInfo.reason || t('agentConfig.iceScanInvalid') }}</div>
                <div v-if="!(netInfo.interfaces || []).length" class="empty-state-v2">
                  <span class="empty-icon">📡</span>
                  <div class="empty-title">{{ t('agentConfig.iceNoScan') }}</div>
                  <div class="empty-desc">{{ t('agentConfig.iceNoScanDesc') }}</div>
                </div>
                <table v-else class="cfg-ice-table">
                  <thead>
                    <tr>
                      <th>{{ t('agentConfig.iceColName') }}</th>
                      <th>{{ t('agentConfig.iceColState') }}</th>
                      <th>{{ t('agentConfig.iceColAddr') }}</th>
                      <th>{{ t('agentConfig.iceColScore') }}</th>
                      <th>{{ t('agentConfig.iceColKeep') }}</th>
                      <th>{{ t('agentConfig.iceColReason') }}</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr v-for="f in (netInfo.interfaces || [])" :key="f.name" :class="{ 'is-drop': f.keep === false }">
                      <td><span class="cfg-ice-ifname">{{ f.name }}</span><span v-if="f.is_default" class="cfg-ice-badge">default</span></td>
                      <td>{{ f.state }}</td>
                      <td class="cfg-ice-addrs">{{ (f.addrs || []).join(', ') || '-' }}</td>
                      <td>{{ f.score ?? '-' }}</td>
                      <td>
                        <span class="cfg-ice-keep" :class="f.keep ? 'yes' : 'no'">
                          {{ f.keep ? t('agentConfig.iceKeepYes') : t('agentConfig.iceKeepNo') }}
                        </span>
                      </td>
                      <td class="cfg-ice-reason">{{ f.reason || '-' }}</td>
                    </tr>
                  </tbody>
                </table>
              </template>
            </div>
          </template>
        </div>

        <!-- ── Footer ── -->
        <div class="cfg-footer">
          <div class="cfg-dirty" v-if="dirty && !loading">
            <span class="cfg-dirty-dot"></span>
            {{ t('agentConfig.unsaved') }}
          </div>
          <div class="cfg-actions">
            <button class="btn" :disabled="saving" @click="requestClose">{{ t('common.cancel') }}</button>
            <button class="btn primary" :disabled="saving || loading || readOnly" :title="readOnly ? t('admin.sharedReadonly') : ''" @click="save">
              {{ saving ? t('agentConfig.saving') : t('agentConfig.saveAndPush') }}
            </button>
          </div>
        </div>

        <!-- ── 重新生成Token确认 ── -->
        <div v-if="showRegenConfirm" class="cfg-confirm-mask" @click.self="showRegenConfirm = false">
          <div class="cfg-confirm">
            <div class="cfg-confirm-title">⚠️ {{ t('admin.regenToken') }}</div>
            <p class="cfg-confirm-msg">{{ t('admin.regenTokenConfirm') }}<b>{{ agentName || agentId }}</b>{{ t('admin.regenTokenTip') }}{{ t('admin.regenTokenWarning') }}</p>
            <div class="cfg-confirm-actions">
              <button class="btn" @click="showRegenConfirm = false">{{ t('common.cancel') }}</button>
              <button class="btn danger" @click="regenerateToken">{{ t('common.confirm') }}</button>
            </div>
          </div>
        </div>

        <!-- ── 关闭确认 ── -->
        <div v-if="showCloseConfirm" class="cfg-confirm-mask" @click.self="showCloseConfirm = false">
          <div class="cfg-confirm">
            <div class="cfg-confirm-title">{{ t('agentConfig.closeConfirmTitle') }}</div>
            <p class="cfg-confirm-msg">{{ t('agentConfig.closeConfirmMsg') }}</p>
            <div class="cfg-confirm-actions">
              <button class="btn" @click="showCloseConfirm = false">{{ t('agentConfig.keepEditing') }}</button>
              <button class="btn danger" @click="confirmClose">{{ t('agentConfig.discard') }}</button>
            </div>
          </div>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<style scoped>
.cfg-mask {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, .45);
  z-index: 200;
  display: flex;
  align-items: center;
  justify-content: center;
}

.cfg-box {
  position: relative;
  display: flex;
  flex-direction: column;
  width: 1040px;
  max-width: 94vw;
  max-height: 88vh;
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: 16px;
  box-shadow: 0 24px 64px rgba(0, 0, 0, .35);
  overflow: hidden;
}

/* ── Header ── */
.cfg-header {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 18px 24px;
  border-bottom: 1px solid var(--border);
}
.cfg-head-left {
  display: flex;
  align-items: center;
  gap: 14px;
  min-width: 0;
}
.cfg-icon {
  flex-shrink: 0;
  width: 44px;
  height: 44px;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 22px;
  background: var(--panel-2);
  border: 1px solid var(--border);
  border-radius: 12px;
}
.cfg-head-text { min-width: 0; }
.cfg-title {
  font-size: 17px;
  font-weight: 600;
  line-height: 1.3;
}
.cfg-sub {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 4px;
  font-size: 12.5px;
  color: var(--muted);
}
.cfg-agent-name {
  font-weight: 600;
  color: var(--fg);
  max-width: 260px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.cfg-id {
  font-family: monospace;
  font-size: 11.5px;
  padding: 1px 8px;
  background: var(--panel-2);
  border: 1px solid var(--border);
  border-radius: 6px;
}
.cfg-status {
  display: inline-flex;
  align-items: center;
  gap: 5px;
}
.cfg-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: #94a3b8;
}
.cfg-dot.on { background: #22c55e; box-shadow: 0 0 0 3px rgba(34, 197, 94, .18); }
.cfg-close {
  flex-shrink: 0;
  width: 34px;
  height: 34px;
  border: none;
  border-radius: 8px;
  background: transparent;
  color: var(--muted);
  font-size: 15px;
  cursor: pointer;
  transition: .15s;
}
.cfg-close:hover {
  background: var(--panel-2);
  color: var(--fg);
}

/* ── Tabs ── */
.cfg-readonly-banner { margin: 10px 16px 0; padding: 8px 12px; border-radius: 6px; background: var(--panel-2); color: var(--muted); font-size: 12px; }
.cfg-tabs {
  flex-shrink: 0;
  display: flex;
  gap: 8px;
  padding: 14px 24px 0;
}
.cfg-tab {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 8px 20px;
  border: 1px solid var(--border);
  border-radius: 999px;
  background: var(--panel-2);
  color: var(--muted);
  font-size: 13.5px;
  font-weight: 600;
  cursor: pointer;
  transition: .15s;
}
.cfg-tab:hover { color: var(--fg); }
.cfg-tab.active {
  background: var(--btn-primary);
  border-color: var(--btn-primary);
  color: var(--btn-primary-fg);
}
.cfg-tab-count { opacity: .8; font-weight: 700; }

/* ── Body ── */
.cfg-body {
  flex: 1;
  min-height: 260px;
  overflow-y: auto;
  padding: 18px 24px 22px;
}
.cfg-loading {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 10px;
  min-height: 260px;
  color: var(--muted);
}
.cfg-spinner {
  width: 20px;
  height: 20px;
  border: 2px solid var(--border);
  border-top-color: var(--accent);
  border-radius: 50%;
  animation: cfg-spin .8s linear infinite;
}
@keyframes cfg-spin { to { transform: rotate(360deg); } }

.cfg-section-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 14px;
}
.cfg-section-head h3 {
  margin: 0;
  font-size: 15px;
  font-weight: 600;
}
.cfg-section-head p {
  margin: 3px 0 0;
  font-size: 13px;
  color: var(--muted);
}

/* ── 参数卡片 ── */
.cfg-card-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(340px, 1fr));
  gap: 14px;
  align-items: stretch;
}
.cfg-card {
  display: flex;
  flex-direction: column;
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: 12px;
  overflow: hidden;
}
.cfg-card-head {
  padding: 13px 16px 0;
  font-size: 12px;
  font-weight: 600;
  color: var(--muted);
  text-transform: uppercase;
  letter-spacing: .3px;
}
.cfg-card-body {
  flex: 1;
  padding: 10px 16px 15px;
}
.cfg-param-row {
  display: flex;
  align-items: center;
  gap: 6px;
}
.cfg-param-input {
  flex: 1;
  min-width: 0;
  padding: 9px 11px;
  border: 1px solid var(--border);
  border-radius: 8px;
  background: var(--panel-2);
  color: var(--fg);
  font-size: 15px;
  font-weight: 600;
}
.cfg-param-input:focus { outline: none; border-color: var(--accent); }
.cfg-param-unit {
  font-size: 13px;
  color: var(--muted);
  min-width: 20px;
}
.cfg-param-hint {
  margin: 8px 0 0;
  font-size: 12px;
  color: var(--muted);
}

/* ── 日志级别胶囊 ── */
.cfg-level-pills {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}
.cfg-level-pill {
  flex: 1;
  min-width: 62px;
  padding: 9px 8px;
  border: 1px solid var(--border);
  border-radius: 8px;
  background: var(--panel-2);
  color: var(--muted);
  font-size: 12.5px;
  font-weight: 700;
  text-transform: uppercase;
  letter-spacing: .3px;
  cursor: pointer;
  transition: .15s;
}
.cfg-level-pill:hover { color: var(--fg); border-color: var(--accent); }
.cfg-level-pill.active {
  background: var(--btn-primary);
  border-color: var(--btn-primary);
  color: var(--btn-primary-fg);
}

/* ── 隧道卡片 ── */
.cfg-tunnel-card {
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: 12px;
  margin-bottom: 14px;
  overflow: hidden;
}
.cfg-tunnel-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 11px 16px;
  background: var(--panel-2);
  border-bottom: 1px solid var(--border);
}
.cfg-tunnel-title {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}
.cfg-tunnel-idx {
  font-size: 14px;
  font-weight: 700;
  color: var(--accent);
  min-width: 24px;
}
.cfg-tunnel-name {
  border: none;
  background: none;
  font-size: 14px;
  font-weight: 600;
  color: var(--fg);
  padding: 2px 0;
  width: 220px;
  max-width: 40vw;
}
.cfg-tunnel-name:focus { outline: none; border-bottom: 1px solid var(--accent); }
.cfg-tunnel-name::placeholder { color: var(--muted); font-weight: 400; }
.cfg-tunnel-actions {
  display: flex;
  align-items: center;
  gap: 12px;
}

.cfg-toggle {
  position: relative;
  display: inline-block;
  width: 36px;
  height: 20px;
  cursor: pointer;
}
.cfg-toggle input { opacity: 0; width: 0; height: 0; }
.cfg-toggle-slider {
  position: absolute;
  inset: 0;
  background: #4b5563;
  border-radius: 20px;
  transition: .2s;
}
.cfg-toggle-slider::before {
  content: "";
  position: absolute;
  width: 16px;
  height: 16px;
  left: 2px;
  bottom: 2px;
  background: #fff;
  border-radius: 50%;
  transition: .2s;
}
.cfg-toggle input:checked + .cfg-toggle-slider { background: #34d399; }
.cfg-toggle input:checked + .cfg-toggle-slider::before { transform: translateX(16px); }

.cfg-tunnel-body { padding: 14px 16px; }
.cfg-tunnel-row {
  display: flex;
  gap: 12px;
  margin-bottom: 12px;
}
.cfg-tunnel-row:last-child { margin-bottom: 0; }
.cfg-tunnel-field { flex: 1; min-width: 0; }
.cfg-tunnel-field.grow2 { flex: 2; }
.cfg-tunnel-field.narrow { flex: 1; max-width: 140px; }
.cfg-tunnel-field label {
  display: block;
  font-size: 12px;
  color: var(--muted);
  margin-bottom: 4px;
  font-weight: 500;
}
.cfg-label-muted { color: var(--muted); font-weight: 400; }
.cfg-tunnel-field input,
.cfg-field-select {
  width: 100%;
  padding: 8px 10px;
  border: 1px solid var(--border);
  border-radius: 6px;
  background: var(--panel-2);
  color: var(--fg);
  font-size: 13px;
  box-sizing: border-box;
}
.cfg-tunnel-field input:focus,
.cfg-field-select:focus { outline: none; border-color: var(--accent); }
.cfg-field-select { appearance: auto; }

/* ── 基本信息 ── */
.cfg-form {
  display: flex;
  flex-direction: column;
  gap: 14px;
  max-width: 560px;
}
.cfg-form-row label {
  display: block;
  font-size: 12px;
  color: var(--muted);
  font-weight: 500;
  margin-bottom: 4px;
}
.cfg-form-row input,
.cfg-form-row select {
  width: 100%;
  padding: 8px 10px;
  border: 1px solid var(--border);
  border-radius: 6px;
  background: var(--panel-2);
  color: var(--fg);
  font-size: 13px;
  box-sizing: border-box;
  appearance: auto;
}
.cfg-form-row input:focus,
.cfg-form-row select:focus { outline: none; border-color: var(--accent); }
.cfg-form-row input[readonly] {
  color: var(--muted);
  background: var(--panel);
  cursor: not-allowed;
  font-family: monospace;
}
.cfg-danger-zone {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  max-width: 560px;
  margin-top: 22px;
  padding: 12px 14px;
  border: 1px solid var(--border);
  border-radius: 8px;
  background: var(--panel-2);
}
.cfg-danger-text { font-size: 12.5px; color: var(--muted); }

/* ── ICE 优化页签（P1） ── */
.cfg-ice-state { font-size: 12.5px; font-weight: 600; color: var(--fg); }
.cfg-level-pill:disabled { opacity: .4; cursor: not-allowed; }
.cfg-ice-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-bottom: 10px;
}
.cfg-ice-chip {
  font-size: 12px;
  padding: 4px 10px;
  background: var(--panel-2);
  border: 1px solid var(--border);
  border-radius: 999px;
  color: var(--muted);
  font-family: monospace;
}
.cfg-ice-chip b { color: var(--fg); font-weight: 600; margin-right: 4px; }
.cfg-ice-warn {
  margin-bottom: 10px;
  padding: 8px 12px;
  border-radius: 8px;
  background: rgba(245, 158, 11, .12);
  border: 1px solid rgba(245, 158, 11, .4);
  color: #f59e0b;
  font-size: 12.5px;
}
.cfg-ice-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 12.5px;
}
.cfg-ice-table th {
  text-align: left;
  padding: 8px 10px;
  color: var(--muted);
  font-weight: 600;
  border-bottom: 1px solid var(--border);
  white-space: nowrap;
}
.cfg-ice-table td {
  padding: 7px 10px;
  border-bottom: 1px solid var(--border);
  color: var(--fg);
  vertical-align: top;
}
.cfg-ice-table tr.is-drop td { opacity: .55; }
.cfg-ice-ifname { font-family: monospace; font-weight: 600; margin-right: 6px; }
.cfg-ice-badge {
  font-size: 10.5px;
  padding: 1px 6px;
  border-radius: 999px;
  background: rgba(56, 189, 248, .16);
  color: #38bdf8;
}
.cfg-ice-addrs { font-family: monospace; font-size: 11.5px; color: var(--muted); word-break: break-all; }
.cfg-ice-keep {
  font-size: 11.5px;
  padding: 1px 8px;
  border-radius: 999px;
  font-weight: 600;
}
.cfg-ice-keep.yes { background: rgba(34, 197, 94, .16); color: #22c55e; }
.cfg-ice-keep.no { background: rgba(148, 163, 184, .16); color: #94a3b8; }
.cfg-ice-reason { color: var(--muted); font-size: 11.5px; }

/* ── Footer ── */
.cfg-footer {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 14px 24px;
  border-top: 1px solid var(--border);
  background: var(--panel);
}
.cfg-dirty {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  color: #d97706;
}
.cfg-dirty-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: #f59e0b;
  animation: cfg-pulse 1.6s ease-in-out infinite;
}
@keyframes cfg-pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: .35; }
}
.cfg-actions {
  display: flex;
  gap: 8px;
  margin-left: auto;
}

/* ── 关闭确认 ── */
.cfg-confirm-mask {
  position: absolute;
  inset: 0;
  background: rgba(0, 0, 0, .55);
  z-index: 10;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: 16px;
}
.cfg-confirm {
  width: 380px;
  max-width: 90%;
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: 12px;
  padding: 22px;
  box-shadow: 0 20px 50px rgba(0, 0, 0, .35);
}
.cfg-confirm-title {
  font-size: 15px;
  font-weight: 600;
  margin-bottom: 8px;
}
.cfg-confirm-msg {
  margin: 0;
  font-size: 13.5px;
  color: var(--muted);
  line-height: 1.6;
}
.cfg-confirm-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 18px;
}
/* 只读模式（共享来的资源）：可见但不可编辑 */
.cfg-body.cfg-readonly input,
.cfg-body.cfg-readonly select,
.cfg-body.cfg-readonly textarea,
.cfg-body.cfg-readonly button:not(.cfg-tab):not(.cfg-close) {
  pointer-events: none;
  opacity: 0.6;
}
.cfg-body.cfg-readonly .cfg-level-pills button {
  pointer-events: none;
  opacity: 0.6;
}
</style>
