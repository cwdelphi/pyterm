<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, inject } from "vue"
import { useI18n } from 'vue-i18n'
import { api, type AgentConfig } from "../api"

const { t } = useI18n()

const props = defineProps<{ agentId: string; agentName?: string; agentOnline?: boolean }>()
const emit = defineEmits(["close"])
const toast = inject<any>("toast")

const agentName = ref(props.agentName || "")
const agentOnline = ref<boolean | undefined>(props.agentOnline)
const allAgents = ref<any[]>([])
const config = ref<AgentConfig>({
  ws_reconnect_interval: 5,
  ws_heartbeat_interval: 30,
  ice_cooldown: 2,
  log_level: "info",
  tunnels: [],
})
const loading = ref(true)
const saving = ref(false)
const activeTab = ref<'basic' | 'tunnels'>('basic')
const showCloseConfirm = ref(false)
const snapshot = ref("")

const levels = ['debug', 'info', 'warn', 'error']
const tunnelCount = computed(() => config.value.tunnels.length)

function payloadOf() {
  return {
    ws_reconnect_interval: config.value.ws_reconnect_interval,
    ws_heartbeat_interval: config.value.ws_heartbeat_interval,
    ice_cooldown: config.value.ice_cooldown,
    log_level: config.value.log_level,
    tunnels: config.value.tunnels.map((tn: any) => ({
      id: tn.id,
      name: tn.name,
      protocol: tn.protocol,
      local_port: tn.local_port,
      target_addr: joinTargetAddr(tn.target_host || "", tn.target_port || ""),
      target_agent_id: tn.target_agent_id,
      enabled: tn.enabled,
    })),
  }
}

const dirty = computed(() => !loading.value && JSON.stringify(payloadOf()) !== snapshot.value)

async function loadConfig() {
  loading.value = true
  try {
    const agentsData = await api.adminListAgents()
    allAgents.value = agentsData.agents
    const agent = agentsData.agents.find((a: any) => a.id === props.agentId)
    if (agent) {
      if (!agentName.value) agentName.value = agent.name
      if (agentOnline.value === undefined) agentOnline.value = agent.online
    }

    const data = await api.adminGetAgentConfig(props.agentId)
    if (data.config) {
      config.value = {
        ws_reconnect_interval: data.config.ws_reconnect_interval ?? 5,
        ws_heartbeat_interval: data.config.ws_heartbeat_interval ?? 30,
        ice_cooldown: data.config.ice_cooldown ?? 2,
        log_level: data.config.log_level ?? "info",
        tunnels: (data.config.tunnels || []).map((tn: any) => ({
          id: tn.id || crypto.randomUUID().slice(0, 8),
          name: tn.name || "",
          protocol: tn.protocol || "tcp",
          local_port: tn.local_port || 0,
          target_host: splitTargetHost(tn.target_addr || ""),
          target_port: splitTargetPort(tn.target_addr || ""),
          target_agent_id: tn.target_agent_id || "",
          enabled: tn.enabled !== false,
        })),
      }
    }
    snapshot.value = JSON.stringify(payloadOf())
  } catch (e: any) {
    toast?.error(e.message)
  }
  loading.value = false
}

function addTunnel() {
  config.value.tunnels.push({
    id: crypto.randomUUID().slice(0, 8),
    name: "",
    protocol: "tcp",
    local_port: 0,
    target_host: "",
    target_port: "",
    target_agent_id: "",
    enabled: true,
  })
  activeTab.value = 'tunnels'
}

function removeTunnel(idx: number) {
  config.value.tunnels.splice(idx, 1)
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

function joinTargetAddr(host: string, port: string): string {
  host = (host || "").trim()
  port = (port || "").trim()
  if (!host) return port ? ":" + port : ""
  if (host.includes(":") && !host.startsWith("[")) host = "[" + host + "]"  // IPv6
  if (port) return host + ":" + port
  return host
}

async function save() {
  saving.value = true
  try {
    await api.adminUpdateAgentConfig(props.agentId, payloadOf())
    snapshot.value = JSON.stringify(payloadOf())
    toast?.success(t('agentConfig.saveSuccess'))
    emit("close")
  } catch (e: any) {
    toast?.error(e.message)
  }
  saving.value = false
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
        <div class="cfg-tabs">
          <button class="cfg-tab" :class="{ active: activeTab === 'basic' }" @click="activeTab = 'basic'">
            {{ t('agentConfig.tabBasic') }}
          </button>
          <button class="cfg-tab" :class="{ active: activeTab === 'tunnels' }" @click="activeTab = 'tunnels'">
            {{ t('agentConfig.tabTunnels') }}<span class="cfg-tab-count" v-if="tunnelCount">· {{ tunnelCount }}</span>
          </button>
        </div>

        <!-- ── Body ── -->
        <div class="cfg-body">
          <div v-if="loading" class="cfg-loading">
            <div class="cfg-spinner"></div>
            <span>{{ t('common.loading') }}</span>
          </div>

          <template v-else>
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
                      <select v-model="tn.protocol" class="cfg-field-select">
                        <option value="tcp">TCP</option>
                        <option value="udp">UDP</option>
                      </select>
                    </div>
                    <div class="cfg-tunnel-field">
                      <label>{{ t('agentConfig.localPort') }}</label>
                      <input v-model.number="tn.local_port" type="number" min="1" max="65535" placeholder="2222" />
                    </div>
                    <div class="cfg-tunnel-field grow2">
                      <label>{{ t('agentConfig.targetAddr') }}</label>
                      <input v-model="tn.target_host" :placeholder="t('agentConfig.targetAddrPlaceholder')" />
                    </div>
                    <div class="cfg-tunnel-field narrow">
                      <label>{{ t('agentConfig.targetPort') }}</label>
                      <input v-model.number="tn.target_port" type="number" min="1" max="65535" placeholder="22" />
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
            <button class="btn primary" :disabled="saving || loading" @click="save">
              {{ saving ? t('agentConfig.saving') : t('agentConfig.saveAndPush') }}
            </button>
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
</style>
