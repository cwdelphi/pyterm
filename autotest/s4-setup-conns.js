#!/usr/bin/env node
/* S4 并发建连：串行预建/清理 perf 连接（避免并发写 ssh_connections.json 竞态）
 * 用法: node s4-setup-conns.js create <labelBase> <N>   # 如 create s4_n5_r1 5
 *       node s4-setup-conns.js delete <labelBase> <N>
 * 连接名: perf_<labelBase>_w<i> (SSH) / perfvnc_<labelBase>_w<i> (VNC)
 */
const BASE = process.env.BASE_URL || 'https://127.0.0.1:5588'
process.env.NODE_TLS_REJECT_UNAUTHORIZED = '0' // 自签证书 IP 不匹配，测试工具内网自用
const mode = process.argv[2]
const labelBase = process.argv[3]
const N = Number(process.argv[4] || 1)
if (!['create', 'delete'].includes(mode) || !labelBase) {
  console.error('usage: node s4-setup-conns.js create|delete <labelBase> <N>')
  process.exit(2)
}

async function main() {
  const lr = await fetch(`${BASE}/api/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username: 'admin', password: 'change_me_pass' }),
  })
  const token = (await lr.json()).token
  const H = { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' }
  const list = await (await fetch(`${BASE}/api/ssh`, { headers: H })).json()
  const conns = Array.isArray(list) ? list : list.connections || list.data || []
  if (mode === 'create') {
    for (let i = 1; i <= N; i++) {
      const wi = `w${String(i).padStart(2, '0')}`
      const defs = [
        [`perf_${labelBase}_${wi}`, {}],
        [`perfvnc_${labelBase}_${wi}`, { connection_type: 'vnc' }],
      ]
      for (const [name, extra] of defs) {
        if (conns.find((c) => c.name === name)) { console.log(`exists ${name}`); continue }
        const r = await fetch(`${BASE}/api/ssh/add`, {
          method: 'POST',
          headers: H,
          body: JSON.stringify({
            name, host: '127.0.0.1', port: 2222, username: 'sshuser',
            auth_type: 'password', password: 'change_me_sshpass',
            connection_mode: 'agent', agent_id: 'local-agent', gateway_id: '',
            connection_type: 'ssh', vnc_port: 5900, vnc_password: 'vncPass123',
            ...extra,
          }),
        })
        console.log(name, r.status)
        if (r.status >= 400) process.exitCode = 1
      }
    }
  } else {
    for (const c of conns) {
      if (c.name && (c.name.startsWith(`perf_${labelBase}`) || c.name.startsWith(`perfvnc_${labelBase}`))) {
        const r = await fetch(`${BASE}/api/ssh/delete`, { method: 'POST', headers: H, body: JSON.stringify({ id: c.id }) })
        console.log('del', c.name, r.status)
      }
    }
  }
}
main().catch((e) => { console.error(e); process.exit(1) })
