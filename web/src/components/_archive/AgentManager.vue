<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount, inject } from "vue"
import { api } from "../api"
import { fetchOnlineAgents, probeConnType, type AgentInfo } from "../utils/webrtc"

const toast = inject<any>("toast")

interface Agent {
  id: string
  name: string
  token: string
  remark: string
  is_active: number
  online?: boolean
  version?: string
  needs_upgrade?: boolean
  latest_version?: string
}

const agents = ref<Agent[]>([])
const onlineIds = ref<Set<string>>(new Set())
const connTypes = ref<Record<string, string>>({})
const agentInfoMap = ref<Record<string, AgentInfo>>({})
const probing = ref<Record<string, boolean>>({})
const loading = ref(false)
const showAddForm = ref(false)
const showEditForm = ref(false)
const showTokenDialog = ref(false)
const showDeleteDialog = ref(false)
const showRegenConfirm = ref(false)
const regenTarget = ref("")
const newAgent = ref({ id: "", name: "", remark: "" })
const editAgent = ref({ id: "", name: "", remark: "", shared_with: "private" })
const newToken = ref("")
const deleteTarget = ref("")
let pollTimer: ReturnType<typeof setInterval> | null = null

// --- AgentID 自动生成 ---
function generateAgentId() {
  const hex = Math.random().toString(16).substring(2, 8)
  return `agent-${hex}`
}

// --- 部署弹窗 ---
const showDeployDialog = ref(false)
const deployAgentId = ref("")
const deployMethod = ref<"docker" | "pm2" | "systemd">("docker")
const deployCopied = ref(false)

function openDeploy(agentId: string) {
  deployAgentId.value = agentId
  deployMethod.value = "docker"
  deployCopied.value = false
  showDeployDialog.value = true
}

function getDeployScript(): string {
  const host = window.location.origin
  return `curl -fsSL ${host}/api/deploy/agent?method=${deployMethod.value}&id=${deployAgentId.value} | bash`
}

function copyDeployScript() {
  navigator.clipboard.writeText(getDeployScript())
  deployCopied.value = true
  toast?.success?.("命令已复制")
  setTimeout(() => { deployCopied.value = false }, 2000)
}

onMounted(async () => {
  await refreshAgents()
  await checkOnline()
  probeAllOnline()
  pollTimer = setInterval(checkOnline, 10000)
})

onBeforeUnmount(() => {
  if (pollTimer) clearInterval(pollTimer)
})

async function refreshAgents() {
  loading.value = true
  try {
    const data = await api.adminListAgents()
    agents.value = data.agents
  } catch (e: any) {
    toast?.error?.(e.message || "加载失败")
  }
  loading.value = false
}

async function checkOnline() {
  const token = localStorage.getItem("token")
  if (!token) return
  try {
    const list = await fetchOnlineAgents(token)
    const infoMap: Record<string, AgentInfo> = {}
    for (const a of list) infoMap[a.id] = a
    agentInfoMap.value = infoMap
    onlineIds.value = new Set(list.filter((a) => a.status === "online").map((a) => a.id))
    const ct: Record<string, string> = {}
    for (const a of list) {
      if (a.conn_type) ct[a.id] = a.conn_type
    }
    connTypes.value = ct
  } catch {}
}

async function probeAgent(agentId: string) {
  const token = localStorage.getItem("token")
  if (!token || probing.value[agentId]) return
  probing.value = { ...probing.value, [agentId]: true }
  try {
    await probeConnType(agentId, token)
  } catch {}
  probing.value = { ...probing.value, [agentId]: false }
  await checkOnline()
}

async function probeAllOnline() {
  for (const id of onlineIds.value) {
    probeAgent(id)
  }
}

function openAdd() {
  newAgent.value = { id: generateAgentId(), name: "", remark: "" }
  showAddForm.value = true
}

async function doAdd() {
  if (!newAgent.value.id.trim() || !newAgent.value.name.trim()) {
    toast?.error?.("ID和名称不能为空")
    return
  }
  try {
    const result = await api.adminAddAgent(newAgent.value)
    newToken.value = result.token
    showAddForm.value = false
    showTokenDialog.value = true
    await refreshAgents()
    toast?.success?.("Agent已添加")
  } catch (e: any) {
    toast?.error?.(e.message || "添加失败")
  }
}

function openEdit(agent: Agent) {
  editAgent.value = { id: agent.id, name: agent.name, remark: agent.remark, shared_with: (agent as any).shared_with || 'private' } as any
  showEditForm.value = true
}

async function doEdit() {
  try {
    await api.adminUpdateAgent(editAgent.value.id, {
      name: editAgent.value.name,
      remark: editAgent.value.remark,
      shared_with: (editAgent.value as any).shared_with,
    })
    showEditForm.value = false
    await refreshAgents()
    toast?.success?.("已更新")
  } catch (e: any) {
    toast?.error?.(e.message || "更新失败")
  }
}

async function regenerateToken(agentId: string) {
  regenTarget.value = agentId
  showRegenConfirm.value = true
}

async function confirmRegenToken() {
  showRegenConfirm.value = false
  try {
    const result = await api.adminRegenerateToken(regenTarget.value)
    newToken.value = result.token
    showTokenDialog.value = true
    await refreshAgents()
  } catch (e: any) {
    toast?.error?.(e.message || "重新生成失败")
  }
}

function openDelete(agent: Agent) {
  deleteTarget.value = agent.id
  showDeleteDialog.value = true
}

async function doDelete() {
  try {
    await api.adminDeleteAgent(deleteTarget.value)
    showDeleteDialog.value = false
    await refreshAgents()
    toast?.success?.("已删除")
  } catch (e: any) {
    toast?.error?.(e.message || "删除失败")
  }
}

async function toggleStatus(agentId: string) {
  try {
    await api.adminToggleAgentStatus(agentId)
    await refreshAgents()
  } catch (e: any) {
    toast?.error?.(e.message || "操作失败")
  }
}

function copyToken() {
  navigator.clipboard.writeText(newToken.value)
  toast?.success?.("Token已复制")
}

function copyEditToken() {
  const agent = agents.value.find(a => a.id === editAgent.id)
  if (agent?.token) {
    navigator.clipboard.writeText(agent.token)
    toast?.success?.('Token已复制')
  }
}
</script>

<template>
  <div class="agent-mgr">
    <div class="breadcrumb" style="padding:8px 0 0;font-size:13px;">Agent管理</div>
    <div class="am-header">
      <h2>Agent管理</h2>
      <div class="am-header-actions">
        <button class="btn sm" @click="refreshAgents" :disabled="loading">
          {{ loading ? "刷新中..." : "刷新" }}
        </button>
        <button class="btn sm" @click="probeAllOnline" :disabled="!onlineIds.size">
          重新检测
        </button>
        <button class="btn primary sm" @click="openAdd">＋ 添加Agent</button>
      </div>
    </div>

    <div class="am-table-wrap">
      <table class="am-table">
        <thead>
          <tr>
            <th>状态</th>
            <th>连接模式</th>
            <th>版本</th>
            <th>ID</th>
            <th>名称</th>
            <th>IP</th>
            <th>备注</th>
            <th>操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="agent in agents" :key="agent.id">
            <td>
              <span class="status-dot" :class="{ online: onlineIds.has(agent.id) }"></span>
              {{ onlineIds.has(agent.id) ? "在线" : "离线" }}
            </td>
            <td>
              <span v-if="onlineIds.has(agent.id) && connTypes[agent.id]" class="conn-badge" :class="connTypes[agent.id]">
                {{ connTypes[agent.id] === 'P2P' ? 'P2P' : connTypes[agent.id] === 'relay' ? '中转' : 'BUG' }}
              </span>
              <span v-else-if="onlineIds.has(agent.id) && probing[agent.id]" class="conn-badge pending">检测中</span>
              <span v-else class="conn-badge offline">-</span>
            </td>
            <td>
              <span v-if="agent.version" class="ver-badge" :class="{ old: agent.needs_upgrade, muted: agent.version === 'dev' }">
                {{ agent.version === 'dev' ? 'dev' : 'v' + agent.version }}
              </span>
              <span v-else class="ver-badge muted">-</span>
              <span v-if="agent.needs_upgrade" class="upgrade-tip" :title="'最新版本 v' + (agent.latest_version || '')">⚠️ 可升级</span>
            </td>
            <td class="mono">{{ agent.id }}</td>
            <td>{{ agent.name }}</td>
            <td class="mono">{{ agentInfoMap[agent.id]?.ip || "-" }}</td>
            <td>{{ agent.remark || "-" }}</td>
            <td class="actions-cell">
              <div class="actions-inner">
                <button class="text-btn" @click="openEdit(agent)">编辑</button>
                <button class="text-btn deploy" @click="openDeploy(agent.id)">部署</button>
                <button class="text-btn danger" @click="openDelete(agent)">删除</button>
                <button class="text-btn" @click="regenerateToken(agent.id)">刷新Token</button>
                <button class="text-btn" :class="agent.is_active ? 'active' : 'inactive'" @click="toggleStatus(agent.id)">
                  {{ agent.is_active ? "启用中" : "已禁用" }}
                </button>
              </div>
            </td>
          </tr>
          <tr v-if="!agents.length">
            <td colspan="8" class="empty-row">暂无Agent，点击上方"添加Agent"开始</td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- 添加Agent弹窗 -->
    <Teleport to="body">
      <div v-if="showAddForm" class="modal-mask" @click.self="showAddForm = false">
        <div class="am-modal">
          <div class="am-modal-head">
            <div class="am-modal-title">
              <span class="am-modal-icon">🤖</span>
              <div>
                <h3>添加 Agent</h3>
                <p class="am-modal-sub">注册一个新的 Agent 节点，用于远程中转连接</p>
              </div>
            </div>
            <button class="am-modal-close" @click="showAddForm = false">✕</button>
          </div>

          <div class="am-form-grid">
            <div class="am-card">
              <div class="am-card-title"><span class="am-card-dot"></span>基本信息</div>
              <div class="am-card-body">
                <div class="am-field">
                  <label>Agent ID <span class="required">*</span></label>
                  <input v-model="newAgent.id" placeholder="例：prod-agent-001" />
                  <span class="am-hint">唯一标识，仅允许字母数字和中划线</span>
                </div>
                <div class="am-field">
                  <label>显示名称 <span class="required">*</span></label>
                  <input v-model="newAgent.name" placeholder="例：生产服务器Agent" />
                  <span class="am-hint">在 SSH 连接和列表中显示的名称</span>
                </div>
              </div>
            </div>

            <div class="am-card">
              <div class="am-card-title"><span class="am-card-dot"></span>补充信息</div>
              <div class="am-card-body">
                <div class="am-field">
                  <label>备注</label>
                  <textarea v-model="newAgent.remark" rows="3" placeholder="可选，描述该 Agent 的位置、用途等"></textarea>
                </div>
              </div>
            </div>
          </div>

          <div class="am-modal-foot">
            <span class="am-foot-note">添加后将自动生成 64 位认证 Token</span>
            <div class="am-modal-actions">
              <button class="btn" @click="showAddForm = false">取消</button>
              <button class="btn primary" @click="doAdd">添加 Agent</button>
            </div>
          </div>
        </div>
      </div>
    </Teleport>

    <!-- 编辑Agent弹窗 -->
    <Teleport to="body">
      <div v-if="showEditForm" class="modal-mask" @click.self="showEditForm = false">
        <div class="am-modal">
          <div class="am-modal-head">
            <div class="am-modal-title">
              <span class="am-modal-icon">✎</span>
              <div>
                <h3>编辑 Agent</h3>
                <p class="am-modal-sub">修改 Agent 的名称与备注信息</p>
              </div>
            </div>
            <button class="am-modal-close" @click="showEditForm = false">✕</button>
          </div>

          <div class="am-form-grid">
            <div class="am-card">
              <div class="am-card-title"><span class="am-card-dot"></span>基本信息</div>
              <div class="am-card-body">
                <div class="am-field">
                  <label>Agent ID</label>
                  <input :value="editAgent.id" disabled />
                  <span class="am-hint">ID 不可修改，作为连接标识</span>
                </div>
                <div class="am-field">
                  <label>显示名称 <span class="required">*</span></label>
                  <input v-model="editAgent.name" placeholder="例：生产服务器Agent" />
                </div>
              </div>
            </div>

            <div class="am-card">
              <div class="am-card-title"><span class="am-card-dot"></span>补充信息</div>
              <div class="am-card-body">
                <div class="am-field">
                  <label>备注</label>
                  <textarea v-model="editAgent.remark" rows="3" placeholder="可选，描述该 Agent 的位置、用途等"></textarea>
                </div>
              </div>
            </div>

            <div class="am-card">
              <div class="am-card-title"><span class="am-card-dot" style="background:#d97706"></span>认证 Token</div>
              <div class="am-card-body">
                <div class="am-field">
                  <label>当前 Token</label>
                  <div style="display:flex;gap:8px;align-items:center">
                    <code style="flex:1;padding:8px 10px;background:var(--bg);border:1px solid var(--border);border-radius:6px;word-break:break-all;font-size:12px;font-family:monospace">{{ agents.find(a => a.id === editAgent.id)?.token || '加载中...' }}</code>
                    <button class="btn sm" @click="copyEditToken" style="flex-shrink:0">复制</button>
                  </div>
                </div>
                <div class="am-field">
                  <button class="btn sm" style="color:#d97706;border-color:#fcd34d" @click="regenerateToken(editAgent.id)">🔄 重新生成Token</button>
                </div>
              </div>
            </div>

            <div class="am-card">
              <div class="am-card-title"><span class="am-card-dot" style="background:#7c3aed"></span>共享设置</div>
              <div class="am-card-body">
                <div class="am-field">
                  <div class="auth-switch">
                    <button class="auth-btn" :class="{ active: (editAgent as any).shared_with === 'private' || !(editAgent as any).shared_with }" @click="(editAgent as any).shared_with = 'private'">🔒 仅自己</button>
                    <button class="auth-btn" :class="{ active: (editAgent as any).shared_with === 'all' }" @click="(editAgent as any).shared_with = 'all'">🌐 所有人</button>
                  </div>
                </div>
              </div>
            </div>
          </div>

          <div class="am-modal-foot">
            <span class="am-foot-note">如需更换认证凭据，请在列表操作中重新生成 Token</span>
            <div class="am-modal-actions">
              <button class="btn" @click="showEditForm = false">取消</button>
              <button class="btn primary" @click="doEdit">保存修改</button>
            </div>
          </div>
        </div>
      </div>
    </Teleport>

    <!-- Token展示弹窗 -->
    <Teleport to="body">
      <div v-if="showTokenDialog" class="modal-mask" @click.self="showTokenDialog = false">
        <div class="am-modal am-modal-token">
          <div class="am-modal-head">
            <div class="am-modal-title">
              <span class="am-modal-icon">🔑</span>
              <div>
                <h3>Agent Token</h3>
                <p class="am-modal-sub">请将此 Token 配置到 wragent 的 config.json 中</p>
              </div>
            </div>
            <button class="am-modal-close" @click="showTokenDialog = false">✕</button>
          </div>

          <div class="am-token-box">
            <code>{{ newToken }}</code>
            <button class="btn sm" @click="copyToken">复制</button>
          </div>

          <div class="am-modal-foot">
            <span class="am-foot-note">Token 仅此一次显示，请妥善保存</span>
            <div class="am-modal-actions">
              <button class="btn primary" @click="showTokenDialog = false">我已保存，关闭</button>
            </div>
          </div>
        </div>
      </div>
    </Teleport>

    <!-- 删除确认弹窗 -->
    <Teleport to="body">
      <div v-if="showDeleteDialog" class="modal-mask" @click.self="showDeleteDialog = false">
        <div class="am-modal am-modal-confirm am-modal-danger">
          <div class="am-modal-head">
            <div class="am-modal-title">
              <span class="am-modal-icon">⚠️</span>
              <div>
                <h3>确认删除</h3>
                <p class="am-modal-sub">此操作不可撤销</p>
              </div>
            </div>
            <button class="am-modal-close" @click="showDeleteDialog = false">✕</button>
          </div>
          <div class="am-confirm-body">
            <p>确定要删除 Agent「<b>{{ deleteTarget }}</b>」吗？</p>
            <p class="am-hint">删除后该 Agent 将无法连接，已有 SSH 引用该 Agent 的配置将失效。</p>
          </div>
          <div class="am-modal-foot">
            <span class="am-foot-note"></span>
            <div class="am-modal-actions">
              <button class="btn" @click="showDeleteDialog = false">取消</button>
              <button class="btn danger" @click="doDelete">确认删除</button>
            </div>
          </div>
        </div>
      </div>
    </Teleport>

    <!-- Token重新生成确认弹窗 -->
    <Teleport to="body">
      <div v-if="showRegenConfirm" class="modal-mask" @click.self="showRegenConfirm = false">
        <div class="am-modal am-modal-confirm">
          <div class="am-modal-head">
            <div class="am-modal-title">
              <span class="am-modal-icon">🔑</span>
              <div>
                <h3>重新生成 Token</h3>
                <p class="am-modal-sub">更换当前 Agent 的认证凭据</p>
              </div>
            </div>
            <button class="am-modal-close" @click="showRegenConfirm = false">✕</button>
          </div>
          <div class="am-confirm-body">
            <p>重新生成后，旧 Token 将立即失效，对应 Agent 将无法连接。确定继续？</p>
          </div>
          <div class="am-modal-foot">
            <span class="am-foot-note"></span>
            <div class="am-modal-actions">
              <button class="btn" @click="showRegenConfirm = false">取消</button>
              <button class="btn primary" @click="confirmRegenToken">确认重新生成</button>
            </div>
          </div>
        </div>
      </div>
    </Teleport>

    <!-- 部署Agent弹窗 -->
    <Teleport to="body">
      <div v-if="showDeployDialog" class="modal-mask" @click.self="showDeployDialog = false">
        <div class="am-modal am-modal-deploy">
          <div class="am-modal-head">
            <div class="am-modal-title">
              <span class="am-modal-icon">🚀</span>
              <div>
                <h3>部署 Agent</h3>
                <p class="am-modal-sub">{{ deployAgentId }}</p>
              </div>
            </div>
            <button class="am-modal-close" @click="showDeployDialog = false">✕</button>
          </div>

          <div class="am-deploy-body">
            <div class="am-deploy-methods">
              <button class="am-deploy-method" :class="{ active: deployMethod === 'docker' }" @click="deployMethod = 'docker'">
                <span class="am-deploy-icon">🐳</span>
                <span>Docker</span>
                <span class="am-deploy-tag">缺省</span>
              </button>
              <button class="am-deploy-method" :class="{ active: deployMethod === 'pm2' }" @click="deployMethod = 'pm2'">
                <span class="am-deploy-icon">⚡</span>
                <span>PM2</span>
              </button>
              <button class="am-deploy-method" :class="{ active: deployMethod === 'systemd' }" @click="deployMethod = 'systemd'">
                <span class="am-deploy-icon">🔧</span>
                <span>systemd</span>
              </button>
            </div>

            <div class="am-deploy-desc">
              <template v-if="deployMethod === 'docker'">Docker 容器方式部署，需要目标服务器安装 Docker</template>
              <template v-else-if="deployMethod === 'pm2'">PM2 进程管理方式，需要目标服务器安装 Node.js</template>
              <template v-else>系统服务方式，需要 root 权限</template>
            </div>

            <div class="am-deploy-script">
              <div class="am-deploy-script-header">
                <span>在目标 Linux 服务器上执行：</span>
                <button class="btn sm" :class="{ copied: deployCopied }" @click="copyDeployScript">
                  {{ deployCopied ? '已复制 ✓' : '复制命令' }}
                </button>
              </div>
              <pre class="am-deploy-code"><code>{{ getDeployScript() }}</code></pre>
            </div>

            <div class="am-deploy-steps">
              <div class="am-deploy-step"><span class="step-num">1</span>在目标服务器执行上述命令</div>
              <div class="am-deploy-step"><span class="step-num">2</span>服务启动后会打印认证链接</div>
              <div class="am-deploy-step"><span class="step-num">3</span>在浏览器打开链接完成认证</div>
              <div class="am-deploy-step"><span class="step-num">4</span>认证完成后 Agent 自动上线</div>
            </div>
          </div>

          <div class="am-modal-foot">
            <span class="am-foot-note">要求：Linux x86_64，需要 curl 和 bash</span>
            <div class="am-modal-actions">
              <button class="btn primary" @click="showDeployDialog = false">关闭</button>
            </div>
          </div>
        </div>
      </div>
    </Teleport>
  </div>
</template>

<style scoped>
.agent-mgr { padding: 20px; height: 100%; overflow-y: auto; }
.am-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 20px; }
.am-header h2 { margin: 0; font-size: 18px; }
.am-header-actions { display: flex; gap: 8px; }
.am-table-wrap { overflow-x: auto; }
.am-table { width: 100%; border-collapse: collapse; font-size: 13px; }
.am-table th { text-align: left; padding: 10px 14px; border-bottom: 2px solid var(--border); font-weight: 600; color: var(--muted); white-space: nowrap; }
.am-table td { padding: 8px 14px; border-bottom: 1px solid var(--border); }
.am-table tr:hover { background: var(--panel-2); }
.mono { font-size: 13px; }
.token-cell { max-width: 200px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.status-dot { display: inline-block; width: 8px; height: 8px; border-radius: 50%; background: #6b7280; margin-right: 4px; }
.status-dot.online { background: #34d399; box-shadow: 0 0 6px #34d399; }
.actions-cell { white-space: nowrap; }
.actions-inner { display: flex; gap: 6px; flex-wrap: nowrap; align-items: center; }
/* 按钮 - 使用全局 .btn / .text-btn */
.empty-row { text-align: center; color: var(--muted); padding: 40px 12px; }
.token-display { display: flex; align-items: center; gap: 8px; background: var(--bg); padding: 10px; border-radius: 6px; margin-bottom: 12px; }
.token-display code { flex: 1; word-break: break-all; font-size: 12px; }
.modal-desc { font-size: 13px; color: var(--muted); margin-bottom: 12px; }
.modal-mask { position: fixed; inset: 0; background: rgba(0, 0, 0, .45); z-index: 200; display: flex; align-items: center; justify-content: center; }
.modal-box { background: var(--panel); border-radius: 12px; padding: 24px; width: 420px; max-width: 92vw; max-height: 85vh; overflow-y: auto; box-shadow: 0 20px 60px rgba(0, 0, 0, .3); }
.modal-box.modal-danger { border: 1px solid #fecaca; }
.modal-box h3 { margin: 0 0 12px; font-size: 17px; }
.modal-actions { display: flex; justify-content: flex-end; gap: 8px; margin-top: 20px; }
.conn-badge { display: inline-block; padding: 2px 8px; border-radius: 4px; font-size: 13px; font-weight: 600; }
.conn-badge.P2P { background: #dcfce7; color: #16a34a; }
.conn-badge.relay { background: #fef9c3; color: #ca8a04; }
.conn-badge.BUG { background: #fee2e2; color: #dc2626; }
.conn-badge.pending { background: var(--panel-2); color: var(--muted); }
.conn-badge.offline { color: var(--muted); }
.ver-badge { display: inline-block; padding: 2px 8px; border-radius: 4px; font-size: 13px; font-weight: 600; background: var(--accent-soft); color: var(--accent); }
.ver-badge.old { background: #fef3c7; color: #d97706; }
.ver-badge.muted { color: var(--muted); background: transparent; }
.upgrade-tip { margin-left: 4px; font-size: 13px; color: #d97706; cursor: help; }

/* ── 卡片式弹窗 ── */
.am-modal { background: var(--panel); border-radius: 16px; width: 560px; max-width: 94vw; max-height: 88vh; overflow-y: auto; box-shadow: 0 24px 70px rgba(0, 0, 0, .35); animation: am-pop .18s ease-out; }
.am-modal-confirm { width: 440px; }
.am-modal-token { width: 640px; }
.am-modal-danger { border: 1px solid #fecaca; }
@keyframes am-pop { from { opacity: 0; transform: translateY(12px) scale(.97); } to { opacity: 1; transform: none; } }

.am-modal-head { display: flex; align-items: flex-start; justify-content: space-between; gap: 12px; padding: 20px 24px 16px; border-bottom: 1px solid var(--border); }
.am-modal-title { display: flex; align-items: flex-start; gap: 12px; }
.am-modal-icon { width: 40px; height: 40px; flex-shrink: 0; display: flex; align-items: center; justify-content: center; font-size: 20px; background: var(--accent-soft); border-radius: 10px; }
.am-modal-danger .am-modal-icon { background: #fee2e2; }
.am-modal-title h3 { margin: 0; font-size: 17px; font-weight: 600; line-height: 1.3; }
.am-modal-sub { margin: 2px 0 0; font-size: 13px; color: var(--muted); }
.am-modal-close { width: 30px; height: 30px; flex-shrink: 0; display: flex; align-items: center; justify-content: center; border-radius: 8px; font-size: 14px; color: var(--muted); transition: background .15s, color .15s; }
.am-modal-close:hover { background: var(--panel-2); color: var(--fg); }

.am-form-grid { display: flex; flex-direction: column; gap: 12px; padding: 16px 24px; }
.am-card { background: var(--panel); border: 1px solid var(--border); border-radius: 12px; padding: 16px 18px; }
.am-card-title { display: flex; align-items: center; gap: 7px; font-size: 13px; font-weight: 600; color: var(--muted); text-transform: uppercase; letter-spacing: .05em; margin-bottom: 14px; }
.am-card-dot { width: 3px; height: 13px; border-radius: 2px; background: var(--accent); }
.am-card-body { display: flex; flex-direction: column; gap: 14px; }
.am-field { display: flex; flex-direction: column; gap: 5px; }
.am-field label { font-size: 13px; color: var(--fg-2); font-weight: 500; }
.am-field input, .am-field textarea, .am-field select { width: 100%; padding: 9px 12px; border: 1px solid var(--border); border-radius: 8px; background: var(--panel); color: var(--fg); font-size: 13px; font-family: inherit; transition: border-color .15s, box-shadow .15s; }
.am-field textarea { resize: vertical; min-height: 64px; line-height: 1.5; }
.am-field input:focus, .am-field textarea:focus, .am-field select:focus { outline: none; border-color: var(--accent); box-shadow: 0 0 0 2px var(--accent-soft); }
.am-field input:disabled { opacity: .55; cursor: not-allowed; }
.am-field input::placeholder, .am-field textarea::placeholder { color: var(--muted); opacity: .7; }
.am-hint { font-size: 13px; color: var(--muted); }
.required { color: #ef4444; margin-left: 1px; }

.am-modal-foot { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 14px 24px 20px; border-top: 1px solid var(--border); }
.am-foot-note { font-size: 13px; color: var(--muted); }
.am-modal-actions { display: flex; gap: 8px; margin-left: auto; }

.am-confirm-body { padding: 20px 24px; font-size: 14px; color: var(--fg-2); line-height: 1.7; }
.am-confirm-body b { color: var(--fg); }
.am-confirm-body .am-hint { margin-top: 6px; display: block; }

.am-token-box { margin: 16px 24px; display: flex; align-items: center; gap: 10px; background: var(--bg); border: 1px dashed var(--border); border-radius: 10px; padding: 12px 14px; }
.am-token-box code { flex: 1; word-break: break-all; font-size: 12.5px; font-family: monospace; line-height: 1.6; }

/* 部署按钮 - 使用全局 .text-btn */

/* ── 部署弹窗 ── */
.am-modal-deploy { width: 620px; }
.am-deploy-body { padding: 16px 24px; display: flex; flex-direction: column; gap: 16px; }
.am-deploy-methods { display: flex; gap: 10px; }
.am-deploy-method { flex: 1; display: flex; flex-direction: column; align-items: center; gap: 6px; padding: 14px 10px; border: 2px solid var(--border); border-radius: 10px; background: var(--panel); cursor: pointer; transition: all .15s; position: relative; }
.am-deploy-method:hover { border-color: var(--accent); }
.am-deploy-method.active { border-color: var(--accent); background: var(--accent-soft); }
.am-deploy-icon { font-size: 22px; }
.am-deploy-method span:nth-child(2) { font-size: 13px; font-weight: 600; }
.am-deploy-tag { position: absolute; top: 6px; right: 8px; font-size: 13px; padding: 1px 6px; border-radius: 4px; background: var(--accent); color: var(--accent-fg); }
.am-deploy-desc { font-size: 13px; color: var(--muted); }
.am-deploy-script { background: var(--bg); border: 1px solid var(--border); border-radius: 8px; overflow: hidden; }
.am-deploy-script-header { display: flex; justify-content: space-between; align-items: center; padding: 10px 14px; border-bottom: 1px solid var(--border); font-size: 13px; color: var(--muted); }
.btn.copied { background: #34d399; color: #fff; border-color: #34d399; }
.am-deploy-code { margin: 0; padding: 14px; font-size: 13px; font-family: monospace; line-height: 1.6; overflow-x: auto; white-space: pre-wrap; word-break: break-all; color: var(--fg); }
.am-deploy-steps { display: flex; flex-direction: column; gap: 8px; }
.am-deploy-step { display: flex; align-items: center; gap: 8px; font-size: 13px; color: var(--fg-2); }
.auth-switch { display:flex; gap:4px; }
.auth-btn { flex:1; height:34px; border:1px solid var(--border); border-radius:6px; font-size:13px; color:var(--fg-2); background:var(--panel); cursor:pointer; }
.auth-btn.active { border-color:var(--accent); color:var(--accent); background:var(--accent-soft); font-weight:600; }
.step-num { display: inline-flex; align-items: center; justify-content: center; width: 20px; height: 20px; border-radius: 50%; background: var(--accent-soft); color: var(--accent); font-size: 13px; font-weight: 700; flex-shrink: 0; }
</style>
