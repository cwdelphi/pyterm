<script setup lang="ts">
import { ref, defineEmits } from 'vue'
import { useI18n } from 'vue-i18n'
import { setLocale, getLocale } from '../i18n'

const { t } = useI18n()
const emit = defineEmits<{
  (e: 'login', token: string, user: any): void
  (e: 'switch-to-register'): void
}>()

const username = ref('')
const password = ref('')
const loading = ref(false)
const error = ref('')
const currentLang = ref(getLocale())
const installCopied = ref(false)
const installCommand = `curl -fsSL "${window.location.origin}/install-agent" | bash`

async function copyInstallCmd() {
  try { await navigator.clipboard.writeText(installCommand) } catch {}
  installCopied.value = true
  setTimeout(() => { installCopied.value = false }, 2000)
}

function toggleLang() {
  const next = currentLang.value === 'zh-CN' ? 'en' : 'zh-CN'
  currentLang.value = next
  setLocale(next)
}

async function handleLogin() {
  if (!username.value || !password.value) {
    error.value = t('auth.usernameRequired')
    return
  }

  loading.value = true
  error.value = ''

  try {
    const response = await fetch('/api/auth/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        username: username.value,
        password: password.value
      })
    })

    const data = await response.json()

    if (!response.ok) {
      throw new Error(data.detail || t('auth.loginFailed'))
    }

    // 存储token
    localStorage.setItem('token', data.token)
    localStorage.setItem('user', JSON.stringify(data.user))

    emit('login', data.token, data.user)
  } catch (e: any) {
    error.value = e.message || t('auth.loginFailed')
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="login-container">
    <!-- 左侧：登录表单 -->
    <div class="login-left">
      <button
        class="lang-toggle"
        type="button"
        :title="currentLang === 'zh-CN' ? 'Switch to English' : '切换到中文'"
        @click="toggleLang"
      >
        {{ t('topbar.langSwitch') }}
      </button>
      <div class="login-stack">
      <div class="login-card">
        <div class="login-header">
          <h1>{{ t('site.name') }}</h1>
          <p>{{ t('auth.loginTitle') }}</p>
        </div>

        <form @submit.prevent="handleLogin" class="login-form">
          <div class="form-group">
            <label for="username">{{ t('auth.username') }}</label>
            <input
              id="username"
              v-model="username"
              type="text"
              :placeholder="t('auth.usernamePlaceholder')"
              :disabled="loading"
              autocomplete="username"
            />
          </div>

          <div class="form-group">
            <label for="password">{{ t('auth.password') }}</label>
            <input
              id="password"
              v-model="password"
              type="password"
              :placeholder="t('auth.passwordPlaceholder')"
              :disabled="loading"
              autocomplete="current-password"
            />
          </div>

          <div v-if="error" class="error-message">
            {{ error }}
          </div>

          <button type="submit" class="login-button" :disabled="loading">
            {{ loading ? t('auth.loggingIn') : t('auth.submit') }}
          </button>
        </form>

        <div class="login-footer">
          <p>{{ t('auth.noAccount') }} <a href="#" @click.prevent="emit('switch-to-register')">{{ t('auth.registerNow') }}</a></p>
        </div>
      </div>

      <!-- 公开安装（模式一） -->
      <div class="install-card">
        <div class="install-head">
          <span class="install-icon">🌐</span>
          <div class="install-head-text">
            <strong>{{ t('login.installTitle') }}</strong>
            <small v-if="t('login.installSubtitle')">{{ t('login.installSubtitle') }}</small>
          </div>
          <button class="install-copy" type="button" :class="{ copied: installCopied }" @click="copyInstallCmd">
            {{ installCopied ? t('admin.copied') + ' ✓' : t('admin.copyCommand') }}
          </button>
        </div>
        <pre class="install-code"><code>{{ installCommand }}</code></pre>
        <div class="install-note">💡 {{ t('login.installApproveNote') }}</div>
      </div>
      </div>
    </div>

    <!-- 右侧：产品场景展示 -->
    <div class="login-right">
      <div class="sc-list">
        <div class="sc-title">{{ t('login.scenariosTitle') }}</div>

        <!-- ═══ 场景 ① 浏览器直连内网 Agent ═══ -->
        <div class="sc-item">
          <div class="sc-item-head">
            <span class="sc-badge">{{ t('login.scenarioBadge', { n: '①' }) }}</span>
            <span class="sc-name">{{ t('login.scenario1Title') }}</span>
          </div>
          <div class="sc-diagram">
            <div class="sc-zone">
              <div class="sc-zone-tag">{{ t('login.zonePublic') }}</div>
              <div class="sc-flow">
                <div class="sc-node sc-node-core">
                  <span class="sc-node-icon">⚙️</span>
                  <div class="sc-node-text"><strong>{{ t('login.nodePlatform') }}</strong><small>{{ t('login.nodeSignal') }}</small></div>
                </div>
                <div class="sc-node sc-node-infra">
                  <span class="sc-node-icon">☁️</span>
                  <div class="sc-node-text"><strong>Coturn</strong><small>STUN / TURN</small></div>
                </div>
              </div>
            </div>
            <div class="sc-arrow sc-arrow-ws sc-arrow-down">WSS</div>
            <div class="sc-zone sc-zone-agent">
              <div class="sc-zone-tag">{{ t('login.zoneLanA') }}</div>
              <div class="sc-flow">
                <div class="sc-node sc-node-user">
                  <span class="sc-node-icon">🖥️</span>
                  <div class="sc-node-text"><strong>{{ t('login.nodeBrowser') }}</strong><small>{{ t('login.nodeUserTerminal') }}</small></div>
                </div>
              </div>
            </div>
            <div class="sc-arrow sc-arrow-web sc-arrow-down">{{ t('login.arrowRelay') }}</div>
            <div class="sc-zone sc-zone-agent">
              <div class="sc-zone-tag">{{ t('login.zoneLanB') }}</div>
              <div class="sc-flow">
                <div class="sc-node sc-node-agent">
                  <span class="sc-node-icon">🤖</span>
                  <div class="sc-node-text"><strong>wragent</strong><small>{{ t('login.nodeAgentOnTarget') }}</small></div>
                </div>
                <div class="sc-arrow sc-arrow-local">{{ t('login.arrowAccess') }}</div>
                <div class="sc-node sc-node-target">
                  <span class="sc-node-icon">🗄️</span>
                  <div class="sc-node-text"><strong>{{ t('login.nodeTarget') }}</strong><small>SSH / SFTP / VNC / RDP</small></div>
                </div>
              </div>
            </div>
          </div>
          <div class="sc-foot">{{ t('login.footScenario1') }}</div>
        </div>

        <!-- ═══ 场景 ② 浏览器经 wrgateway 代理访问内网 ═══ -->
        <div class="sc-item">
          <div class="sc-item-head">
            <span class="sc-badge">{{ t('login.scenarioBadge', { n: '②' }) }}</span>
            <span class="sc-name">{{ t('login.scenario2Title') }}</span>
          </div>
          <div class="sc-diagram">
            <div class="sc-zone">
              <div class="sc-zone-tag">{{ t('login.zoneLanA') }}</div>
              <div class="sc-flow">
                <div class="sc-node sc-node-user">
                  <span class="sc-node-icon">🖥️</span>
                  <div class="sc-node-text"><strong>{{ t('login.nodeBrowser') }}</strong><small>{{ t('login.nodeHttpProxyEnv') }}</small></div>
                </div>
                <div class="sc-arrow" style="color:#fb923c">HTTP</div>
                <div class="sc-node sc-node-gw">
                  <span class="sc-node-icon">🌉</span>
                  <div class="sc-node-text"><strong>wrgateway</strong><small>{{ t('login.nodeProxyForward') }}</small></div>
                </div>
              </div>
            </div>
            <div class="sc-arrow sc-arrow-web sc-arrow-down">{{ t('login.arrowRelay') }}</div>
            <div class="sc-zone sc-zone-agent">
              <div class="sc-zone-tag">{{ t('login.zoneLanB') }}</div>
              <div class="sc-flow">
                <div class="sc-node sc-node-agent">
                  <span class="sc-node-icon">🤖</span>
                  <div class="sc-node-text"><strong>wragent</strong><small>{{ t('login.nodeIntranetDevice') }}</small></div>
                </div>
                <div class="sc-arrow sc-arrow-local">{{ t('login.arrowNetworkAccess') }}</div>
                <div class="sc-node sc-node-target">
                  <span class="sc-node-icon">🗄️</span>
                  <div class="sc-node-text"><strong>{{ t('login.nodeTarget') }}</strong><small>SSH / SFTP / VNC / RDP</small></div>
                </div>
              </div>
            </div>
            <div class="sc-zone">
              <div class="sc-zone-tag">{{ t('login.zonePublic') }}</div>
              <div class="sc-flow">
                <div class="sc-node sc-node-core">
                  <span class="sc-node-icon">⚙️</span>
                  <div class="sc-node-text"><strong>{{ t('login.nodePlatform') }}</strong><small>{{ t('login.nodeSignal') }}</small></div>
                </div>
                <div class="sc-node sc-node-infra">
                  <span class="sc-node-icon">☁️</span>
                  <div class="sc-node-text"><strong>Coturn</strong><small>STUN / TURN</small></div>
                </div>
              </div>
            </div>
          </div>
          <div class="sc-foot">{{ t('login.footScenario2') }}</div>
        </div>

        <!-- ═══ 场景 ③ APP 通过多 Agent 隧道访问内网 TCP/UDP ═══ -->
        <div class="sc-item">
          <div class="sc-item-head">
            <span class="sc-badge">{{ t('login.scenarioBadge', { n: '③' }) }}</span>
            <span class="sc-name">{{ t('login.scenario3Title') }}</span>
          </div>
          <div class="sc-diagram">
            <div class="sc-zone">
              <div class="sc-zone-tag">{{ t('login.zonePublic') }}</div>
              <div class="sc-flow">
                <div class="sc-node sc-node-core">
                  <span class="sc-node-icon">⚙️</span>
                  <div class="sc-node-text"><strong>{{ t('login.nodePlatform') }}</strong><small>{{ t('login.nodeSignal') }}</small></div>
                </div>
                <div class="sc-node sc-node-infra">
                  <span class="sc-node-icon">☁️</span>
                  <div class="sc-node-text"><strong>Coturn</strong><small>STUN / TURN</small></div>
                </div>
              </div>
            </div>
            <div class="sc-arrow sc-arrow-ws sc-arrow-down">WSS</div>
            <div class="sc-zone sc-zone-agent">
              <div class="sc-zone-tag">{{ t('login.zoneLanA') }}</div>
              <div class="sc-flow">
                <div class="sc-node sc-node-user">
                  <span class="sc-node-icon">📱</span>
                  <div class="sc-node-text"><strong>{{ t('login.nodeApp') }}</strong><small>{{ t('login.nodeIntranetTerminal') }}</small></div>
                </div>
              </div>
            </div>
            <div class="sc-arrow sc-arrow-web sc-arrow-down">{{ t('login.arrowRelay') }}</div>
            <div class="sc-diagram-branches">
              <div class="sc-branch-zone">
                <div class="sc-zone-tag">{{ t('login.zoneLanB') }}</div>
                <div class="sc-node sc-node-agent">
                  <span class="sc-node-icon">🤖</span>
                  <div class="sc-node-text"><strong>wragent (A)</strong><small>{{ t('login.nodeIntranetServerA') }}</small></div>
                </div>
                <div class="sc-arrow sc-arrow-local sc-arrow-down">{{ t('login.arrowNetworkAccess') }}</div>
                <div class="sc-node sc-node-target">
                  <span class="sc-node-icon">🗄️</span>
                  <div class="sc-node-text"><strong>{{ t('login.nodeTargetA') }}</strong><small>TCP / UDP</small></div>
                </div>
              </div>
              <div class="sc-branch-zone">
                <div class="sc-zone-tag">{{ t('login.zoneLanC') }}</div>
                <div class="sc-node sc-node-agent">
                  <span class="sc-node-icon">🤖</span>
                  <div class="sc-node-text"><strong>wragent (B)</strong><small>{{ t('login.nodeIntranetServerB') }}</small></div>
                </div>
                <div class="sc-arrow sc-arrow-local sc-arrow-down">{{ t('login.arrowNetworkAccess') }}</div>
                <div class="sc-node sc-node-target">
                  <span class="sc-node-icon">🗄️</span>
                  <div class="sc-node-text"><strong>{{ t('login.nodeTargetB') }}</strong><small>TCP / UDP</small></div>
                </div>
              </div>
            </div>
          </div>
          <div class="sc-foot">{{ t('login.footScenario3') }}</div>
        </div>

        <!-- ═══ 统一管理链路 ═══ -->
        <div class="sc-mgmt">
          <span class="sc-mgmt-icon">🔗</span>
          <span class="sc-mgmt-text"><strong>{{ t('login.mgmtTitle') }}</strong> — {{ t('login.mgmtDesc', { wss: 'WSS' }) }}</span>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.login-container {
  display: flex;
  align-items: stretch;
  min-height: 100vh;
  background: var(--bg);
}

/* 左侧表单 */
.login-left {
  position: relative;
  width: 40%;
  min-width: 360px;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 40px 24px;
}

.lang-toggle {
  position: absolute;
  top: 16px;
  right: 16px;
  min-width: 36px;
  height: 32px;
  padding: 0 10px;
  border: 1px solid var(--border);
  border-radius: 6px;
  background: var(--panel-2);
  color: var(--fg-2);
  font-size: 12px;
  font-weight: 600;
  cursor: pointer;
  transition: all .15s;
  z-index: 2;
}

.lang-toggle:hover {
  border-color: var(--accent);
  color: var(--accent);
  background: var(--accent-soft);
}

.login-card {
  width: 100%;
  max-width: 400px;
  padding: 40px;
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: 12px;
  box-shadow: var(--shadow-lg);
}

.login-stack {
  display: flex;
  flex-direction: column;
  gap: 16px;
  width: 100%;
  max-width: 400px;
}

/* ── 公开安装（模式一）卡片 ── */
.install-card {
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: 12px;
  box-shadow: var(--shadow-lg);
  padding: 18px 20px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.install-head {
  display: flex;
  align-items: center;
  gap: 10px;
}

.install-icon { font-size: 20px; line-height: 1; flex: none; }

.install-head-text {
  display: flex;
  flex-direction: column;
  min-width: 0;
  flex: 1;
}

.install-head-text strong { font-size: 14px; color: var(--fg); font-weight: 600; }
.install-head-text small { font-size: 12px; color: var(--muted); line-height: 1.4; }

.install-copy {
  flex: none;
  padding: 6px 12px;
  border: 1px solid var(--accent);
  border-radius: 6px;
  background: var(--accent-soft);
  color: var(--accent);
  font-size: 12px;
  font-weight: 600;
  cursor: pointer;
  transition: all .15s;
}

.install-copy:hover { background: var(--accent); color: #fff; }
.install-copy.copied { border-color: #16a34a; background: rgba(22,163,74,.12); color: #16a34a; }

.install-code {
  margin: 0;
  padding: 10px 12px;
  background: var(--bg);
  border: 1px solid var(--border);
  border-radius: 8px;
  font-size: 12.5px;
  font-family: monospace;
  line-height: 1.6;
  overflow-x: auto;
  white-space: pre-wrap;
  word-break: break-all;
  color: var(--fg);
}

.install-note { font-size: 12px; color: var(--muted); line-height: 1.5; }

.login-header {
  text-align: center;
  margin-bottom: 32px;
}

.login-header h1 {
  font-size: 28px;
  font-weight: 600;
  color: var(--fg);
  margin: 0 0 8px 0;
}

.login-header p {
  font-size: 14px;
  color: var(--muted);
  margin: 0;
}

.login-form {
  display: flex;
  flex-direction: column;
  gap: 20px;
}

.form-group {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.form-group label {
  font-size: 14px;
  font-weight: 500;
  color: var(--fg);
}

.form-group input {
  padding: 12px 16px;
  border: 1px solid var(--border);
  border-radius: 8px;
  font-size: 14px;
  background: var(--panel-2);
  color: var(--fg);
  transition: border-color 0.2s, box-shadow 0.2s;
}

.form-group input::placeholder {
  color: var(--muted);
}

.form-group input:focus {
  outline: none;
  border-color: var(--accent);
  box-shadow: 0 0 0 3px var(--accent-soft);
}

.form-group input:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

.error-message {
  padding: 12px;
  background: rgba(239, 68, 68, 0.12);
  color: #dc2626;
  border-radius: 8px;
  font-size: 14px;
  text-align: center;
}

.login-button {
  padding: 12px 24px;
  background: var(--accent);
  color: white;
  border: none;
  border-radius: 8px;
  font-size: 16px;
  font-weight: 500;
  cursor: pointer;
  transition: background-color 0.2s, opacity 0.2s;
}

.login-button:hover:not(:disabled) {
  opacity: 0.88;
}

.login-button:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

.login-footer {
  margin-top: 24px;
  text-align: center;
}

.login-footer p {
  font-size: 14px;
  color: var(--muted);
  margin: 0;
}

.login-footer a {
  color: var(--accent);
  text-decoration: none;
  font-weight: 500;
}

.login-footer a:hover {
  text-decoration: underline;
}

/* 右侧场景区 */
.login-right {
  flex: 1;
  min-width: 0;
  overflow-y: auto;
  padding: 40px 32px;
  border-left: 1px solid var(--border);
  background: var(--panel-2);
}

/* ── 使用场景（PPT 风格示意） ── */
.sc-list {
  max-width: 760px;
  margin: 0 auto;
}
.sc-title { font-size: 18px; font-weight: 700; color: var(--fg); margin: 0 0 16px; }
.sc-item { border: 1px solid var(--border); border-radius: 10px; padding: 12px; margin-bottom: 14px; background: var(--panel); }
.sc-item-head { display: flex; align-items: center; gap: 8px; margin-bottom: 10px; flex-wrap: wrap; }
.sc-badge { flex: none; font-size: 11px; font-weight: 700; padding: 2px 10px; border-radius: 999px; background: var(--accent); color: #fff; }
.sc-name { font-size: 13px; font-weight: 600; color: var(--fg); }
.sc-diagram { display: flex; flex-direction: column; align-items: stretch; gap: 4px; padding: 12px; border-radius: 8px; background: var(--panel-2); border: 1px dashed var(--border); }
.sc-diagram-tree { flex-direction: column; }
.sc-flow { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; justify-content: center; }
.sc-zone { border: 1px solid var(--border); border-radius: 8px; padding: 10px; background: var(--panel); display: flex; flex-direction: column; gap: 8px; width: 100%; box-sizing: border-box; align-items: center; }
.sc-zone-tag { font-size: 11px; color: var(--muted); text-align: center; font-weight: 600; letter-spacing: .3px; }
.sc-zone-agent { border-color: var(--accent); background: var(--accent-soft); }
.sc-zone-edge { border-color: #a78bfa; background: rgba(167,139,250,.1); }
.sc-node { display: flex; align-items: center; gap: 8px; padding: 8px 12px; border-radius: 8px; background: var(--panel); border: 1px solid var(--border); box-shadow: var(--shadow); min-width: 120px; }
.sc-node-icon { font-size: 22px; line-height: 1; }
.sc-node-text { display: flex; flex-direction: column; }
.sc-node-text strong { font-size: 12.5px; color: var(--fg); line-height: 1.3; }
.sc-node-text small { font-size: 11px; color: var(--muted); line-height: 1.3; }
.sc-node-user { border-color: #60a5fa; background: linear-gradient(135deg, rgba(37,99,235,.12), rgba(37,99,235,.04)); }
.sc-node-core { border-color: #34d399; background: linear-gradient(135deg, rgba(5,150,105,.14), rgba(5,150,105,.04)); }
.sc-node-agent { border-color: #f59e0b; background: linear-gradient(135deg, rgba(245,158,11,.14), rgba(245,158,11,.04)); }
.sc-node-gw { border-color: #a78bfa; background: linear-gradient(135deg, rgba(167,139,250,.16), rgba(167,139,250,.05)); }
.sc-node-target { border-color: #94a3b8; background: linear-gradient(135deg, rgba(148,163,184,.16), rgba(148,163,184,.05)); }
.sc-arrow { flex: none; display: flex; flex-direction: column; align-items: center; gap: 2px; font-size: 11px; color: var(--muted); white-space: nowrap; position: relative; padding: 2px 4px; }
.sc-arrow::after { content: ''; display: block; width: 0; height: 0; border-left: 5px solid var(--muted); border-top: 4px solid transparent; border-bottom: 4px solid transparent; }
.sc-arrow-both::before { content: ''; display: block; width: 0; height: 0; border-right: 5px solid var(--muted); border-top: 4px solid transparent; border-bottom: 4px solid transparent; }
.sc-arrow-down { flex-direction: column; align-self: center; }
.sc-arrow-down::after { border: none; border-left: 4px solid transparent; border-right: 4px solid transparent; border-top: 6px solid var(--muted); }
.sc-arrow-down.sc-arrow-both::before { display: none; }
.sc-arrow-ws { color: #60a5fa; }
.sc-arrow-ws::after { border-left-color: #60a5fa; }
.sc-arrow-ws.sc-arrow-down::after { border-top-color: #60a5fa; border-left-color: transparent; }
.sc-arrow-local { color: #f59e0b; }
.sc-arrow-local::after { border-left-color: #f59e0b; }
.sc-arrow-local.sc-arrow-down::after { border-top-color: #f59e0b; border-left-color: transparent; }
.sc-arrow-web { color: #a78bfa; }
.sc-arrow-web::after { border-left-color: #a78bfa; }
.sc-arrow-web.sc-arrow-both::before { border-right-color: #a78bfa; }
.sc-arrow-web.sc-arrow-down::after { border-top-color: #a78bfa; border-left-color: transparent; }
.sc-foot { font-size: 12px; color: var(--muted); line-height: 1.5; margin-top: 8px; padding: 0 2px; }
.sc-tree-branches { display: flex; align-items: flex-start; justify-content: center; gap: 10px; flex-wrap: wrap; width: 100%; }
.sc-tree-branches .sc-node { flex-direction: column; text-align: center; }
.sc-tree-branches .sc-node-text { align-items: center; }
.sc-branch { display: flex; flex-direction: column; align-items: center; }
.sc-zone .sc-flow + .sc-flow { margin-top: 4px; }
.sc-zone-agent .sc-zone-tag { color: var(--accent); }
.sc-zone-edge .sc-zone-tag { color: #a78bfa; }

/* 响应式：窄屏上下堆叠 */
@media (max-width: 900px) {
  .login-container { flex-direction: column; }
  .login-left { width: 100%; min-width: 0; padding: 32px 16px; }
  .login-right { padding: 24px 16px; border-left: none; border-top: 1px solid var(--border); }
}

/* ── 场景三分支布局 ── */
.sc-diagram-branches { display: flex; gap: 16px; justify-content: center; flex-wrap: wrap; }
.sc-branch-zone { display: flex; flex-direction: column; align-items: center; gap: 6px; border: 1px solid var(--border); border-radius: 8px; padding: 10px 14px; background: var(--panel); min-width: 160px; }
.sc-branch-zone .sc-zone-tag { font-size: 11px; color: var(--muted); font-weight: 600; }

/* ── Coturn 基础设施节点 ── */
.sc-node-infra { border-color: #94a3b8; background: linear-gradient(135deg, rgba(148,163,184,.16), rgba(148,163,184,.05)); }

/* ── 管理链路说明 ── */
.sc-mgmt { display: flex; align-items: flex-start; gap: 8px; margin-top: 16px; padding: 10px 14px; border: 1px solid var(--border); border-radius: 8px; background: var(--panel); font-size: 12px; color: var(--muted); line-height: 1.6; }
.sc-mgmt-icon { font-size: 16px; flex-shrink: 0; margin-top: 1px; }
.sc-mgmt-text strong { color: var(--fg); }
</style>
