export interface TreeNode {
  name: string
  path: string
  type: 'dir' | 'file'
  children?: TreeNode[]
}

export interface Config {
  site_name: string
  home: string
}

export interface LinkItem {
  id?: string
  title: string
  url: string
  description: string
  group_id: string
}

export interface LinkGroup {
  id: string
  name: string
  links: LinkItem[]
}

export interface LinkData {
  groups: LinkGroup[]
}

export interface SshConn {
  id?: string
  name: string
  host: string
  port: number
  username: string
  auth_type: string
  password?: string
  key_path?: string
  connection_mode?: 'agent'
  agent_id?: string
  gateway_id?: string
  remark?: string
  // VNC fields
  connection_type?: 'ssh' | 'vnc'
  vnc_port?: number
  vnc_password?: string
  pixel_format?: string
  color_depth?: string
  read_only?: boolean
  // RDP fields (reserved)
  rdp_port?: number
  rdp_password?: string
  rdp_domain?: string
  rdp_resolution?: string
  has_password?: boolean
  sort_order?: number
}

export interface SftpConfig {
  share_dir: string
  read_only: boolean
  username: string
  password: string
  port: number
  bind: string
}

export interface User {
  id: string
  username: string
  email: string
  role: string
  avatar?: string
  phone?: string
  created_at?: string
  last_login?: string
  last_active?: string
  login_count?: number
  is_active: boolean
}
export interface TunnelConfig {
  id: string
  name: string
  protocol: 'tcp' | 'udp'
  local_port: number
  target_addr?: string
  target_host?: string
  target_port?: string
  target_agent_id: string
  enabled: boolean
}

export interface AgentConfig {
  ws_reconnect_interval: number
  ws_heartbeat_interval: number
  ice_cooldown: number
  log_level: string
  tunnels: TunnelConfig[]
}

export interface AuditLog {
  id: number
  user_id: string
  username: string
  action: string
  target_type: string
  target_id: string
  detail: string
  ip: string
  created_at: string
}

// 获取认证token
function getAuthToken(): string | null {
  return localStorage.getItem('token')
}

// 检查是否已认证
export function isAuthenticated(): boolean {
  return !!getAuthToken()
}

// 获取当前用户
export function getCurrentUser(): User | null {
  const userStr = localStorage.getItem('user')
  if (!userStr) return null
  try {
    return JSON.parse(userStr)
  } catch {
    return null
  }
}

// 登出
export function logout(): void {
  localStorage.removeItem('token')
  localStorage.removeItem('user')
  window.location.reload()
}

async function getJSON<T>(url: string, requireAuth = true): Promise<T> {
  const headers: Record<string, string> = {}
  
  if (requireAuth) {
    const token = getAuthToken()
    if (token) {
      headers['Authorization'] = `Bearer ${token}`
    }
  }
  
  const res = await fetch(url, { headers })
  if (res.status === 401 && requireAuth) {
    // Token过期或无效，清除本地存储并刷新页面
    logout()
    throw new Error('认证已过期，请重新登录')
  }
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  return res.json() as Promise<T>
}

async function postJSON<T = any>(url: string, body: any, requireAuth = true, method = 'POST'): Promise<T> {
  const headers: Record<string, string> = {
    'Content-Type': 'application/json'
  }
  
  if (requireAuth) {
    const token = getAuthToken()
    if (token) {
      headers['Authorization'] = `Bearer ${token}`
    }
  }
  
  const res = await fetch(url, {
    method,
    headers,
    body: JSON.stringify(body)
  })
  
  if (res.status === 401 && requireAuth) {
    logout()
    throw new Error('认证已过期，请重新登录')
  }
  
  if (!res.ok) {
    const e = await res.json().catch(() => ({}))
    throw new Error(e.detail || `HTTP ${res.status}`)
  }
  return res.json()
}

export interface RemoteFileItem {
  name: string
  size: number
  mtime: string
  mode: string
  mode_num: number
  is_dir: boolean
  is_link: boolean
  target: string
}

export const api = {
  // 认证API（不需要token）
  authLogin: (username: string, password: string) => 
    postJSON<{token: string; user: User}>('/api/auth/login', { username, password }, false),
  
  authRegister: (username: string, password: string, email: string) => 
    postJSON<User>('/api/auth/register', { username, password, email }, false),
  
  authMe: (): Promise<User> => getJSON('/api/auth/me'),
  
  // 配置API（不需要token）
  config: (): Promise<Config> => getJSON('/api/config', false),
  
  tree: (): Promise<TreeNode[]> => getJSON('/api/tree', false),
  
  async doc(path: string): Promise<string> {
    const headers: Record<string, string> = {}
    const token = getAuthToken()
    if (token) {
      headers['Authorization'] = `Bearer ${token}`
    }
    
    const res = await fetch(`/api/doc?path=${encodeURIComponent(path)}`, { headers })
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    return res.text()
  },

  /* ── 文件管理 ── */
  docsTree: (): Promise<TreeNode[]> => getJSON('/api/docs/tree'),
  
  async docsContent(path: string): Promise<string> {
    const j = await getJSON<{ content: string }>(`/api/docs/content?path=${encodeURIComponent(path)}`)
    return j.content
  },
  
  docsWrite: (path: string, content: string) => postJSON('/api/docs/write', { path, content }),
  
  docsMkdir: (path: string) => postJSON('/api/docs/mkdir', { path }),
  
  docsRename: (oldPath: string, newName: string) =>
    postJSON('/api/docs/rename', { old_path: oldPath, new_name: newName }),
  
  docsDelete: (path: string) => postJSON('/api/docs/delete', { path }),
  
  docsMove: (src: string, dstDir: string) => postJSON('/api/docs/move', { src, dst_dir: dstDir }),

  /* ── 文件管理(旧API兼容) ── */
  filesTree: (): Promise<TreeNode[]> => getJSON('/api/docs/tree'),
  
  async filesRead(path: string): Promise<string> {
    const j = await getJSON<{ content: string }>(`/api/docs/content?path=${encodeURIComponent(path)}`)
    return j.content
  },
  
  filesWrite: (path: string, content: string) => postJSON('/api/docs/write', { path, content }),
  
  filesNew: (parent: string, name: string, isDir = false) =>
    isDir ? postJSON('/api/docs/mkdir', { path: parent ? `${parent}/${name}` : name })
          : postJSON('/api/docs/write', { path: parent ? `${parent}/${name}` : name, content: '' }),
  
  filesRename: (oldPath: string, newName: string) =>
    postJSON('/api/docs/rename', { old_path: oldPath, new_name: newName }),
  
  filesDelete: (path: string) => postJSON('/api/docs/delete', { path }),

  filesMove: (src: string, dstDir: string) => postJSON('/api/docs/move', { src, dst_dir: dstDir }),

  /* ── 链接管理(分组) ── */
  linksList: (): Promise<LinkData> => getJSON('/api/links'),
  linksAdd: (item: Omit<LinkItem, 'id'>) => postJSON('/api/links/add', item),
  linksUpdate: (item: LinkItem) => postJSON('/api/links/update', item),
  linksDelete: (id: string) => postJSON('/api/links/delete', { id }),
  linksGroupAdd: (name: string) => postJSON('/api/links/group/add', { name }),
  linksGroupRename: (id: string, name: string) => postJSON('/api/links/group/rename', { id, name }),
  linksGroupDelete: (id: string) => postJSON('/api/links/group/delete', { id }),

  /* ── SSH 管理 ── */
  sshList: (): Promise<SshConn[]> => getJSON('/api/ssh'),
  sshGetPassword: (connId: string): Promise<{password: string; vnc_password: string; rdp_password: string}> => getJSON(`/api/ssh/${connId}/password`),
  sshAdd: (conn: SshConn) => postJSON('/api/ssh/add', conn),
  sshUpdate: (conn: SshConn) => postJSON('/api/ssh/update', conn),
  sshDelete: (id: string) => postJSON('/api/ssh/delete', { id }),
  sshTest: (conn: Omit<SshConn, 'id' | 'name' | 'remark'>) => postJSON('/api/ssh/test', conn),
  sshReorder: (items: { id: string | undefined; sort_order: number }[]) => postJSON('/api/ssh/reorder', items),

  /* ── SFTP 服务 ── */
  sftpConfig: (): Promise<SftpConfig> => getJSON('/api/sftp/config'),
  sftpSaveConfig: (cfg: SftpConfig) => postJSON('/api/sftp/config', cfg),
  sftpStart: () => postJSON('/api/sftp/start', {}),
  sftpStop: () => postJSON('/api/sftp/stop', {}),
  sftpStatus: (): Promise<{ running: boolean; share_dir: string; read_only: boolean; port: number; bind: string; username: string }> => getJSON('/api/sftp/status'),
  sftpLog: (): Promise<{ log: string[] }> => getJSON('/api/sftp/log'),

  /* ── Agent配置管理 ── */
  adminGetAgentConfig: (agentId: string): Promise<{config: AgentConfig | null}> =>
    getJSON('/api/admin/agents/' + agentId + '/config'),
  adminUpdateAgentConfig: (agentId: string, config: AgentConfig): Promise<{ok: boolean}> =>
    postJSON('/api/admin/agents/' + agentId + '/config', config, true, 'PUT'),
  
  /* ── Admin Agent CRUD ── */
  adminNextAgentId: (): Promise<{ id: string }> =>
    getJSON('/api/admin/agent-id/next'),
  adminListAgents: (): Promise<{ agents: Array<{id: string; name: string; token: string; coturn_id: string; remark: string; is_active: number; online?: boolean; ip?: string; conn_type?: string; owner_id?: string; owner_name?: string; is_owner?: boolean; shared_with?: string; version?: string; needs_upgrade?: boolean; latest_version?: string}> }> =>
    getJSON('/api/admin/agents'),
  adminAddAgent: (data: {id: string; name: string; coturn_id?: string; remark?: string}): Promise<{id: string; token: string; name: string}> =>
    postJSON('/api/admin/agents', data),
  adminUpdateAgent: (agentId: string, data: {name?: string; coturn_id?: string; remark?: string}): Promise<{ok: boolean}> =>
    postJSON('/api/admin/agents/' + agentId, data, true, 'PUT'),
  adminDeleteAgent: (agentId: string): Promise<{ok: boolean}> =>
    postJSON('/api/admin/agents/' + agentId, {}, true, 'DELETE'),
  adminRegenerateToken: (agentId: string): Promise<{token: string}> =>
    postJSON('/api/admin/agents/' + agentId + '/token', {}),
  adminToggleAgentStatus: (agentId: string): Promise<{ok: boolean; is_active: number}> =>
    postJSON('/api/admin/agents/' + agentId + '/status', {}),
  adminShareAgent: (agentId: string, sharedWith: string | string[]): Promise<{ok: boolean; shared_with: string}> =>
    postJSON('/api/admin/agents/' + agentId + '/share', { shared_with: sharedWith }),
  adminGetAgentShares: (agentId: string): Promise<{shared_with: string; users: Array<{id: string; username: string}>}> =>
    getJSON('/api/admin/agents/' + agentId + '/shares'),
  adminGetTestCredentials: (coturnId: string): Promise<{username: string; credential: string; host: string; port: number; tls_port: number}> =>
    getJSON('/api/admin/coturn/' + coturnId + '/test-credentials'),
  deployAgentScript: (method: string, agentId: string): string =>
    `/api/deploy/agent?method=${method}&id=${agentId}`,
  deployAgentSignedUrl: (method: string, agentId: string): Promise<{url: string; expires_in: number}> =>
    postJSON('/api/deploy/agent/signed-url', { method, id: agentId }),
  adminPendingAgents: (): Promise<{ pending: Array<{sid: string; agent_id: string; agent_name: string; created_at: number}> }> =>
    getJSON('/api/admin/agents/pending'),
  approvePendingAgent: (sid: string): Promise<{ok: boolean; agent_id: string}> =>
    postJSON('/webrtc/agents/setup', { sid }),

  /* ── Admin Gateway CRUD ── */
  adminNextGatewayId: (): Promise<{ id: string }> =>
    getJSON('/api/admin/gateway-id/next'),
  adminListGateways: (): Promise<{ gateways: Array<{id: string; name: string; token: string; url: string; remark: string; is_active: number; online?: boolean; owner_id?: string; owner_name?: string; is_owner?: boolean; shared_with?: string; version?: string; needs_upgrade?: boolean; latest_version?: string}> }> =>
    getJSON('/api/admin/gateways'),
  adminAddGateway: (data: {id: string; name: string; url: string; remark?: string}): Promise<{id: string; token: string; name: string}> =>
    postJSON('/api/admin/gateways', data),
  adminUpdateGateway: (gatewayId: string, data: {name?: string; url?: string; remark?: string}): Promise<{ok: boolean}> =>
    postJSON('/api/admin/gateways/' + gatewayId, data, true, 'PUT'),
  adminDeleteGateway: (gatewayId: string): Promise<{ok: boolean}> =>
    postJSON('/api/admin/gateways/' + gatewayId, {}, true, 'DELETE'),
  adminRegenerateGatewayToken: (gatewayId: string): Promise<{token: string}> =>
    postJSON('/api/admin/gateways/' + gatewayId + '/token', {}),
  adminToggleGatewayStatus: (gatewayId: string): Promise<{ok: boolean; is_active: boolean}> =>
    postJSON('/api/admin/gateways/' + gatewayId + '/status', {}),
  adminShareGateway: (gatewayId: string, sharedWith: string | string[]): Promise<{ok: boolean; shared_with: string}> =>
    postJSON('/api/admin/gateways/' + gatewayId + '/share', { shared_with: sharedWith }),
  adminGetGatewayShares: (gatewayId: string): Promise<{shared_with: string; users: Array<{id: string; username: string}>}> =>
    getJSON('/api/admin/gateways/' + gatewayId + '/shares'),

  /* ── Admin 用户管理 ── */
  adminListUsers: (): Promise<{ users: User[] }> =>
    getJSON('/api/admin/users'),
  adminCreateUser: (data: {username: string; password: string; email?: string; role?: string; phone?: string}): Promise<{ok: boolean; id: string}> =>
    postJSON('/api/admin/users', data),
  adminUpdateUser: (userId: string, data: {username?: string; email?: string; role?: string; phone?: string}): Promise<{ok: boolean}> =>
    postJSON('/api/admin/users/' + userId, data, true, 'PUT'),
  adminDeleteUser: (userId: string): Promise<{ok: boolean}> =>
    postJSON('/api/admin/users/' + userId, {}, true, 'DELETE'),
  adminResetPassword: (userId: string, newPassword: string): Promise<{ok: boolean}> =>
    postJSON('/api/admin/users/' + userId + '/password', { new_password: newPassword }),
  adminToggleUserStatus: (userId: string): Promise<{ok: boolean; is_active: boolean}> =>
    postJSON('/api/admin/users/' + userId + '/status', {}),
  adminBatchDeleteUsers: (ids: string[]): Promise<{ok: boolean; deleted: string[]; skipped: any[]}> =>
    postJSON('/api/admin/users/batch-delete', { ids }),

  /* ── Admin 角色权限 ── */
  adminGetRoles: (): Promise<{ roles: any[]; all_permissions: Record<string, string> }> =>
    getJSON('/api/admin/roles'),
  adminMyPermissions: (): Promise<{ role: string; permissions: string[]; all_permissions: Record<string, string> }> =>
    getJSON('/api/admin/me/permissions'),

  /* ── Admin 审计日志 ── */
  adminAuditLog: (): Promise<{ logs: AuditLog[] }> =>
    getJSON('/api/admin/audit'),

  /* ── Admin coturn管理 ── */
  adminListCoturn: (): Promise<{ servers: any[] }> =>
    getJSON('/api/admin/coturn'),
  adminAddCoturn: (data: any): Promise<{ok: boolean; id: string}> =>
    postJSON('/api/admin/coturn', data),
  adminUpdateCoturn: (coturnId: string, data: any): Promise<{ok: boolean}> =>
    postJSON('/api/admin/coturn/' + coturnId, data, true, 'PUT'),
  adminDeleteCoturn: (coturnId: string): Promise<{ok: boolean}> =>
    postJSON('/api/admin/coturn/' + coturnId, {}, true, 'DELETE'),
  adminSetDefaultCoturn: (coturnId: string): Promise<{ok: boolean}> =>
    postJSON('/api/admin/coturn/' + coturnId + '/default', {}),
  adminShareCoturn: (coturnId: string, sharedWith: string | string[]): Promise<{ok: boolean; shared_with: string}> =>
    postJSON('/api/admin/coturn/' + coturnId + '/share', { shared_with: sharedWith }),
  adminGetCoturnShares: (coturnId: string): Promise<{shared_with: string; users: Array<{id: string; username: string}>}> =>
    getJSON('/api/admin/coturn/' + coturnId + '/shares'),

  /* ── WebRTC Agent ── */
  webrtcAgents: (): Promise<{ agents: Array<{id: string; name: string; version: string; status: string; ip?: string; conn_type?: string; last_seen: number}> }> =>
    getJSON('/api/webrtc/agents'),
  webrtcRooms: (): Promise<{ rooms: Array<{room_id: string; agent_id: string; user_id: string}> }> =>
    getJSON('/api/webrtc/rooms'),
  webrtcSignalUrl: () => {
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
    return `${proto}//${location.host}/api/ws/webrtc`
  },

  /* ── 日志 ── */
  frontendLog: (level: string, message: string, url?: string) => postJSON('/api/log', { level, message, url: url || location.pathname }, false),
  getLogs: (): Promise<{ backend: string[]; frontend: string[] }> => getJSON('/api/logs', false),
  
  /* ── SFTP 客户端 (远程文件管理) ── */
  sftpClientList: (params: {host: string; port: number; username: string; auth_type: string; password: string; key_path: string; path: string}) =>
    postJSON<{items: RemoteFileItem[]; path: string}>('/api/sftp-client/list', params),
  sftpClientCwd: (params: {host: string; port: number; username: string; auth_type: string; password: string; key_path: string}) =>
    postJSON<{cwd: string}>('/api/sftp-client/cwd', params),
  sftpClientStat: (params: {host: string; port: number; username: string; auth_type: string; password: string; key_path: string; path: string}) =>
    postJSON<{size: number; mtime: number; mode: string; is_dir: boolean}>('/api/sftp-client/stat', params),
  sftpClientRead: (params: {host: string; port: number; username: string; auth_type: string; password: string; key_path: string; path: string}) =>
    postJSON<{content: string; encoding: string}>('/api/sftp-client/read', params),
  sftpClientWrite: (params: {host: string; port: number; username: string; auth_type: string; password: string; key_path: string; path: string; content: string}) =>
    postJSON<{ok: boolean}>('/api/sftp-client/write', params),
  sftpClientMkdir: (params: {host: string; port: number; username: string; auth_type: string; password: string; key_path: string; path: string}) =>
    postJSON<{ok: boolean}>('/api/sftp-client/mkdir', params),
  sftpClientTouch: (params: {host: string; port: number; username: string; auth_type: string; password: string; key_path: string; path: string}) =>
    postJSON<{ok: boolean}>('/api/sftp-client/touch', params),
  sftpClientRename: (params: {host: string; port: number; username: string; auth_type: string; password: string; key_path: string; old_path: string; new_name: string}) =>
    postJSON<{ok: boolean}>('/api/sftp-client/rename', params),
  sftpClientDelete: (params: {host: string; port: number; username: string; auth_type: string; password: string; key_path: string; path: string}) =>
    postJSON<{ok: boolean}>('/api/sftp-client/delete', params),
  sftpClientRmdir: (params: {host: string; port: number; username: string; auth_type: string; password: string; key_path: string; path: string}) =>
    postJSON<{ok: boolean}>('/api/sftp-client/rmdir', params),
  sftpClientChmod: (params: {host: string; port: number; username: string; auth_type: string; password: string; key_path: string; path: string; mode: number}) =>
    postJSON<{ok: boolean}>('/api/sftp-client/chmod', params),

  /* ── 连接时序图 ── */
  timelineReport: (data: any) =>
    postJSON<{ok: boolean; room_id: string; steps: number}>('/api/timeline/report', data),

  timelineRecords: (params: any) =>
    postJSON<{items: any[]; total: number; page: number; page_size: number}>('/api/timeline/records', params),

  timelineDetail: (params: {room_id?: string; record_id?: number}) =>
    postJSON<any>('/api/timeline/detail', params),

  timelineStats: (params: any) =>
    postJSON<any>('/api/timeline/stats', params),
}

export function collectFiles(nodes: TreeNode[], out: TreeNode[] = []): TreeNode[] {
  for (const n of nodes) {
    if (n.type === 'file') out.push(n)
    else collectFiles(n.children ?? [], out)
  }
  return out
}

export function findNode(nodes: TreeNode[], path: string): TreeNode | null {
  for (const n of nodes) {
    if (n.path === path) return n
    if (n.type === 'dir') {
      const hit = findNode(n.children ?? [], path)
      if (hit) return hit
    }
  }
  return null
}
