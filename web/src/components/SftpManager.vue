<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'
import { api, type SftpConfig } from '../api'

const { t } = useI18n()

const config = ref<SftpConfig>({
  share_dir: '',
  read_only: true,
  username: 'ppy',
  password: 'cw',
  port: 2222,
  bind: '0.0.0.0',
})
const running = ref(false)
const status = ref({ share_dir: '', port: 2222, bind: '0.0.0.0', username: '' })
const logs = ref<string[]>([])
const msg = ref('')
const saving = ref(false)
const starting = ref(false)
const stopping = ref(false)

let logTimer: ReturnType<typeof setInterval> | null = null

onMounted(async () => {
  await loadConfig()
  await loadStatus()
  await loadLog()
  logTimer = setInterval(loadLog, 3000)
})

onBeforeUnmount(() => {
  if (logTimer) clearInterval(logTimer)
})

async function loadConfig() {
  try {
    config.value = await api.sftpConfig()
  } catch {}
}

async function loadStatus() {
  try {
    const s = await api.sftpStatus()
    running.value = s.running
    status.value = s
  } catch {}
}

async function loadLog() {
  try {
    const r = await api.sftpLog()
    logs.value = r.log
  } catch {}
}

async function saveConfig() {
  saving.value = true
  try {
    await api.sftpSaveConfig(config.value)
    showMsg(t('sftp.configSaved'))
  } catch (e: any) {
    showMsg(t('sftp.saveFailed') + e.message, true)
  } finally {
    saving.value = false
  }
}

async function startServer() {
  starting.value = true
  try {
    const r = await api.sftpStart()
    if (r.ok) {
      showMsg(t('sftp.serviceStarted'))
      running.value = true
      await loadStatus()
      await loadLog()
    } else {
      showMsg(r.detail || t('sftp.startFailed'), true)
      await loadLog()
    }
  } catch (e: any) {
    showMsg(t('sftp.startFailedDetail') + e.message, true)
  } finally {
    starting.value = false
  }
}

async function stopServer() {
  stopping.value = true
  try {
    const r = await api.sftpStop()
    if (r.ok) {
      showMsg(t('sftp.serviceStopped'))
      running.value = false
      await loadStatus()
      await loadLog()
    } else {
      showMsg(r.detail || t('sftp.stopFailed'), true)
    }
  } catch (e: any) {
    showMsg(t('sftp.stopFailedDetail') + e.message, true)
  } finally {
    stopping.value = false
  }
}

function showMsg(text: string, isError = false) {
  msg.value = (isError ? '✗ ' : '✓ ') + text
  setTimeout(() => (msg.value = ''), 3000)
}

const connectCmd = () => `sftp -P ${config.value.port} ${config.value.username}@${location.hostname}`
</script>

<template>
  <div class="sftp-wrap">
    <div class="sftp-card">
      <!-- 标题栏 -->
      <div class="sftp-header">
        <div class="sftp-title">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/></svg>
          <span>SFTP Server</span>
        </div>
        <span class="sftp-badge" :class="{ running }">
          <span class="badge-dot"></span>
          {{ running ? t('sftp.running') : t('sftp.stopped') }}
        </span>
      </div>

      <!-- 主体：左右两栏 -->
      <div class="sftp-body">
        <!-- 左：配置 -->
        <div class="sftp-config">
          <div class="section-title">{{ t('sftp.config') }}</div>
          <div class="cfg-form">
            <div class="cfg-row">
              <label>{{ t('sftp.shareDir') }}</label>
              <input v-model="config.share_dir" placeholder="/path/to/shared" />
            </div>
            <div class="cfg-row">
              <label>{{ t('sftp.accessMode') }}</label>
              <div class="radio-group">
                <label class="radio-item">
                  <input type="radio" :value="true" v-model="config.read_only" /> {{ t('sftp.readOnly') }}
                </label>
                <label class="radio-item">
                  <input type="radio" :value="false" v-model="config.read_only" /> {{ t('sftp.readWrite') }}
                </label>
              </div>
            </div>
            <div class="cfg-row">
              <label>{{ t('auth.username') }}</label>
              <input v-model="config.username" placeholder="ppy" />
            </div>
            <div class="cfg-row">
              <label>{{ t('auth.password') }}</label>
              <input v-model="config.password" type="password" placeholder="密码" />
            </div>
            <div class="cfg-row">
              <label>{{ t('sftp.port') }}</label>
              <input v-model.number="config.port" type="number" placeholder="2222" />
            </div>
            <div class="cfg-row">
              <label>{{ t('sftp.bind') }}</label>
              <input v-model="config.bind" placeholder="0.0.0.0" />
            </div>
            <div class="cfg-actions">
              <button class="sftp-btn" @click="saveConfig" :disabled="saving">{{ saving ? t('sftp.savingConfig') : t('sftp.saveConfig') }}</button>
              <button v-if="!running" class="sftp-btn primary" @click="startServer" :disabled="starting">{{ starting ? t('sftp.starting') : t('sftp.start') }}</button>
              <button v-else class="sftp-btn danger" @click="stopServer" :disabled="stopping">{{ stopping ? t('sftp.stopping') : t('sftp.stop') }}</button>
            </div>
          </div>
        </div>

        <!-- 右：状态 -->
        <div class="sftp-right">
          <!-- 运行状态 -->
          <div class="section-title">{{ t('sftp.runStatus') }}</div>
          <div class="status-card">
            <div class="status-row">
              <span class="status-label">{{ t('sftp.port') }}</span>
              <span class="status-value">{{ config.port }}</span>
            </div>
            <div class="status-row">
              <span class="status-label">{{ t('sftp.bind') }}</span>
              <span class="status-value">{{ config.bind }}</span>
            </div>
            <div class="status-cmd">
              <code>{{ connectCmd() }}</code>
            </div>
          </div>

          <!-- 防火墙 -->
          <div class="section-title" style="margin-top:16px">{{ t('sftp.firewall') }}</div>
          <div class="fw-row">
            <span class="fw-port">{{ config.port }}<span class="fw-ok">✓</span></span>
            <span class="fw-port">22<span class="fw-ok">✓</span></span>
          </div>

          <!-- 服务日志 -->
          <div class="section-title" style="margin-top:16px">{{ t('sftp.serviceLog') }}</div>
          <div class="log-box">
            <div v-for="(line, i) in logs" :key="i" class="log-line">{{ line }}</div>
            <div v-if="!logs.length" class="log-empty">{{ t('sftp.noLogs') }}</div>
          </div>
        </div>
      </div>

      <!-- 消息条 -->
      <div v-if="msg" class="sftp-msg" :class="{ err: msg.startsWith('✗') }">{{ msg }}</div>
    </div>
  </div>
</template>

<style scoped>
.sftp-wrap {
  flex: 1;
  display: flex;
  align-items: flex-start;
  justify-content: center;
  padding: 32px 24px;
  overflow-y: auto;
}

.sftp-card {
  width: 100%;
  max-width: 900px;
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: 12px;
  overflow: hidden;
}

/* 标题栏 */
.sftp-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 16px 24px;
  border-bottom: 1px solid var(--border);
}

.sftp-title {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 16px;
  font-weight: 600;
}

.sftp-badge {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  color: var(--muted);
  padding: 4px 12px;
  border-radius: 20px;
  background: var(--panel-2);
}

.sftp-badge.running {
  color: #16a34a;
  background: #dcfce7;
}

.badge-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--muted);
}

.sftp-badge.running .badge-dot {
  background: #16a34a;
}

/* 主体 */
.sftp-body {
  display: flex;
  gap: 0;
}

.sftp-config {
  width: 320px;
  flex-shrink: 0;
  padding: 20px 24px;
  border-right: 1px solid var(--border);
}

.sftp-right {
  flex: 1;
  padding: 20px 24px;
  min-width: 0;
}

.section-title {
  font-size: 13px;
  font-weight: 600;
  color: var(--muted);
  margin-bottom: 12px;
  text-transform: uppercase;
  letter-spacing: .03em;
}

/* 表单 */
.cfg-form {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.cfg-row label {
  display: block;
  font-size: 13px;
  color: var(--muted);
  margin-bottom: 4px;
}

.cfg-row input[type='text'],
.cfg-row input[type='number'],
.cfg-row input[type='password'],
.cfg-row input:not([type]) {
  width: 100%;
  padding: 8px 10px;
  border: 1px solid var(--border);
  border-radius: 6px;
  background: var(--bg);
  color: var(--fg);
  font-size: 13px;
}

.cfg-row input:focus {
  outline: none;
  border-color: var(--accent);
  box-shadow: 0 0 0 2px var(--accent-soft);
}

.radio-group {
  display: flex;
  gap: 16px;
}

.radio-item {
  display: flex;
  align-items: center;
  gap: 5px;
  font-size: 13px;
  color: var(--fg-2);
  cursor: pointer;
}

.radio-item input[type='radio'] {
  accent-color: var(--accent);
}

.cfg-actions {
  display: flex;
  gap: 8px;
  margin-top: 8px;
}

/* 按钮 */
.sftp-btn {
  min-height: 34px;
  padding: 0 16px;
  font-size: 13px;
  border: 1px solid var(--border);
  border-radius: 6px;
  background: var(--panel);
  color: var(--fg-2);
}

.sftp-btn:hover:not(:disabled) {
  border-color: var(--accent);
  color: var(--accent);
}

.sftp-btn:disabled {
  opacity: .4;
  cursor: default;
}

.sftp-btn.primary {
  background: var(--accent);
  color: var(--accent-fg);
  border-color: var(--accent);
}

.sftp-btn.primary:hover:not(:disabled) {
  opacity: .9;
}

.sftp-btn.danger {
  border-color: #dc2626;
  color: #dc2626;
}

.sftp-btn.danger:hover:not(:disabled) {
  background: #dc2626;
  color: #fff;
}

/* 状态卡 */
.status-card {
  padding: 14px 16px;
  border: 1px solid var(--border);
  border-radius: 8px;
  background: var(--bg);
}

.status-row {
  display: flex;
  justify-content: space-between;
  font-size: 13px;
  padding: 4px 0;
}

.status-label {
  color: var(--muted);
}

.status-value {
  color: var(--fg);
  font-weight: 500;
}

.status-cmd {
  margin-top: 10px;
  padding: 8px 12px;
  background: var(--panel-2);
  border-radius: 6px;
  font-size: 12px;
  font-family: 'SFMono-Regular', Consolas, monospace;
  color: var(--fg-2);
  overflow-x: auto;
  white-space: nowrap;
}

/* 防火墙 */
.fw-row {
  display: flex;
  gap: 12px;
}

.fw-port {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 6px 14px;
  border: 1px solid var(--border);
  border-radius: 6px;
  font-size: 13px;
  font-family: monospace;
  color: var(--fg-2);
}

.fw-ok {
  color: #16a34a;
  font-weight: 700;
}

/* 日志 */
.log-box {
  min-height: 200px;
  max-height: 400px;
  overflow-y: auto;
  padding: 12px 14px;
  background: #0f172a;
  border-radius: 8px;
  font-family: 'SFMono-Regular', Consolas, monospace;
  font-size: 12px;
  line-height: 1.6;
  color: #94a3b8;
}

.log-line {
  white-space: pre-wrap;
  word-break: break-all;
}

.log-empty {
  color: #475569;
  font-style: italic;
}

/* 消息 */
.sftp-msg {
  padding: 8px 24px;
  font-size: 13px;
  color: var(--accent);
  border-top: 1px solid var(--border);
  animation: fadeIn .2s;
}

.sftp-msg.err {
  color: #dc2626;
}

@keyframes fadeIn {
  from { opacity: 0; }
  to { opacity: 1; }
}

@media (max-width: 768px) {
  .sftp-body { flex-direction: column; }
  .sftp-config { width: 100%; border-right: none; border-bottom: 1px solid var(--border); }
}
</style>
