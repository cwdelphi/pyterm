import { Page, expect } from '@playwright/test'

const BASE_URL = process.env.BASE_URL || 'https://127.0.0.1:5588'
const ADMIN_USER = 'admin'
const ADMIN_PASS = 'change_me_pass'
const TIMEOUT = 25000

// ═══════════════════════════════════════════
//  认证
// ═══════════════════════════════════════════

export async function loginAsAdmin(page: Page) {
  await page.goto('/')
  await page.waitForLoadState('domcontentloaded')
  await page.waitForTimeout(400)
  const password = page.locator('input[type="password"]').first()
  const needLogin = await password.isVisible({ timeout: 2000 }).catch(() => false)
  if (needLogin) {
    const userIn = page.locator('input:not([type="password"]):not([type="checkbox"]):not([type="radio"])').first()
    await userIn.fill(ADMIN_USER)
    await password.fill(ADMIN_PASS)
    await page.locator('button[type="submit"], button').filter({ hasText: /登\s*录/ }).first().click()
    await page.waitForTimeout(1500)
  } else {
    await page.waitForTimeout(300)
  }
}

export async function apiLogin(page: Page): Promise<string> {
  let lastErr: unknown
  for (let i = 0; i < 5; i++) {
    try {
      const resp = await page.request.post(`${BASE_URL}/api/auth/login`, {
        data: { username: ADMIN_USER, password: ADMIN_PASS },
        timeout: 10000,
      })
      if (!resp.ok()) {
        lastErr = new Error(`login HTTP ${resp.status()}`)
        await page.waitForTimeout(500)
        continue
      }
      const text = await resp.text()
      if (!text.trimStart().startsWith('{')) {
        lastErr = new Error(`login non-JSON: ${text.slice(0, 80)}`)
        await page.waitForTimeout(500)
        continue
      }
      const data = JSON.parse(text)
      if (data.token) return data.token
      lastErr = new Error(`login missing token: ${text.slice(0, 80)}`)
      await page.waitForTimeout(500)
    } catch (e) {
      lastErr = e
      await page.waitForTimeout(500)
    }
  }
  throw lastErr instanceof Error ? lastErr : new Error(String(lastErr))
}

// ═══════════════════════════════════════════
//  导航
// ═══════════════════════════════════════════

export async function goSshView(page: Page) {
  await loginAsAdmin(page)
  const sshBtn = page.locator('.topmenu:has-text("远程管理")').first()
  if (await sshBtn.isVisible({ timeout: 3000 }).catch(() => false)) {
    await sshBtn.click()
    await page.waitForTimeout(800)
  }
}

export async function goAdminView(page: Page) {
  await loginAsAdmin(page)
  const adminBtn = page.locator('.topmenu:has-text("系统管理")').first()
  if (await adminBtn.isVisible({ timeout: 3000 }).catch(() => false)) {
    await adminBtn.click()
    await page.waitForTimeout(800)
  }
}

export async function waitForPageReady(page: Page) {
  await page.waitForLoadState('networkidle')
  await page.waitForTimeout(1000)
}

// ═══════════════════════════════════════════
//  API: 连接管理
// ═══════════════════════════════════════════

export async function apiAddConnection(page: Page, token: string, conn: {
  name: string; host: string; port?: number; username: string;
  password: string; agent_id?: string; gateway_id?: string;
  connection_type?: string;
  vnc_port?: number; vnc_password?: string;
}): Promise<string> {
  const resp = await page.request.post(`${BASE_URL}/api/ssh/add`, {
    headers: { Authorization: `Bearer ${token}` },
    data: {
      name: conn.name,
      host: conn.host,
      port: conn.port || 22,
      username: conn.username,
      auth_type: 'password',
      password: conn.password,
      connection_mode: 'agent',
      agent_id: conn.agent_id || 'local-agent',
      gateway_id: conn.gateway_id || '',
      connection_type: conn.connection_type || 'ssh',
      vnc_port: conn.vnc_port ?? 5900,
      vnc_password: conn.vnc_password ?? '',
    },
  })
  const data = await resp.json()
  return data.id || ''
}

export async function apiDeleteByPrefix(page: Page, token: string, prefix: string) {
  const resp = await page.request.get(`${BASE_URL}/api/ssh`, {
    headers: { Authorization: `Bearer ${token}` },
  })
  const data = await resp.json()
  const list = Array.isArray(data) ? data : (data.connections || [])
  for (const conn of list) {
    if (conn.name && conn.name.startsWith(prefix)) {
      await page.request.post(`${BASE_URL}/api/ssh/delete`, {
        headers: { Authorization: `Bearer ${token}` },
        data: { id: conn.id },
      })
    }
  }
}

export async function apiWaitForConnection(page: Page, token: string, name: string, timeoutMs = 10000) {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    const resp = await page.request.get(`${BASE_URL}/api/ssh`, {
      headers: { Authorization: `Bearer ${token}` },
    })
    const data = await resp.json()
    const list = Array.isArray(data) ? data : (data.connections || [])
    for (const conn of list) {
      if (conn.name === name) return conn
    }
    await page.waitForTimeout(300)
  }
  throw new Error(`Connection "${name}" not found within ${timeoutMs}ms`)
}

// ═══════════════════════════════════════════
//  UI: 连接操作
// ═══════════════════════════════════════════

export async function openAddModal(page: Page) {
  const btn = page.locator('.sidebar-actions .icon-btn.primary, button[title="新建连接"]').first()
  await btn.click()
  await expect(page.locator('.modal-box')).toBeVisible({ timeout: 5000 })
}

export async function fillAndSave(page: Page, opts: {
  name: string; host: string; port?: number; username: string;
  password: string; agentId?: string; gatewayId?: string;
}) {
  const modal = page.locator('.modal-box')
  const fields = modal.locator('.form-field')
  // 名称, 主机, 用户名, 端口
  await fields.nth(0).locator('input').fill(opts.name)
  await fields.nth(1).locator('input').fill(opts.host)
  await fields.nth(3).locator('input').fill(String(opts.port || 22))
  await fields.nth(2).locator('input').fill(opts.username)

  // 密码
  await modal.locator('input[type="password"]').fill(opts.password)

  // Agent 选择
  if (opts.agentId) {
    const agentSelect = modal.locator('select').filter({ hasText: /请选择|Agent/ }).first()
    if (await agentSelect.isVisible()) {
      await agentSelect.selectOption({ value: opts.agentId }).catch(() => {})
    }
  }

  // 网关选择
  if (opts.gatewayId) {
    const gwSelect = modal.locator('select').filter({ hasText: /网关|不使用/ }).first()
    if (await gwSelect.isVisible()) {
      await gwSelect.selectOption({ value: opts.gatewayId }).catch(() => {})
    }
  }

  await modal.locator('button:has-text("添加")').first().click()
  await page.waitForTimeout(1500)
}

export async function sidebarHas(page: Page, name: string) {
  return await page.locator(`.ssh-card-name:has-text("${name}")`).count() > 0
}

// ═══════════════════════════════════════════
//  UI: 终端
// ═══════════════════════════════════════════

export async function expandGroup(page: Page, groupType: 'ssh' | 'vnc' | 'rdp') {
  const label = groupType === 'ssh' ? 'SSH/FILE' : groupType === 'vnc' ? 'VNC' : 'RDP'
  const header = page.locator('.group-header', { hasText: label }).first()
  const arrow = header.locator('.group-arrow').first()
  const expanded = await arrow.evaluate(el => el.classList.contains('expanded')).catch(() => false)
  if (!expanded) {
    await header.click()
    await page.waitForTimeout(500)
  }
}

// 展开「Agent 控制台」分组（缺省折叠；用类名选择器避开 i18n 文案）
export async function expandAgentGroup(page: Page) {
  const header = page.locator('.agent-group .group-header').first()
  const arrow = header.locator('.group-arrow').first()
  const expanded = await arrow.evaluate(el => el.classList.contains('expanded')).catch(() => false)
  if (!expanded) {
    await header.click()
    await page.waitForTimeout(500)
  }
}

export async function clickConn(page: Page, name: string) {
  // 尝试直接查找卡片
  let card = page.locator(`.ssh-card:has-text("${name}")`).first()
  if (!(await card.isVisible({ timeout: 2000 }).catch(() => false))) {
    // 卡片不可见，尝试展开 SSH 分组
    await expandGroup(page, 'ssh')
    card = page.locator(`.ssh-card:has-text("${name}")`).first()
  }
  if (!(await card.isVisible({ timeout: 2000 }).catch(() => false))) {
    // 仍不可见，尝试展开 VNC 分组
    await expandGroup(page, 'vnc')
    card = page.locator(`.ssh-card:has-text("${name}")`).first()
  }
  await expect(card).toBeVisible({ timeout: TIMEOUT })
  await card.locator('.ssh-card-main').click()
  await page.waitForTimeout(500)
}

export async function openSshTerminal(page: Page, name: string) {
  await clickConn(page, name)
  const sshBtn = page.locator('.ssh-sub-btn.type-ssh').first()
  await sshBtn.click()
  await page.waitForTimeout(3000)
}

export async function openFileBrowser(page: Page, name: string) {
  await clickConn(page, name)
  const fileBtn = page.locator('.ssh-sub-btn.type-file').first()
  await fileBtn.click()
  await expect(page.locator('.sfb').first()).toBeVisible({ timeout: 15000 })
  // 目录列表可能晚于面板骨架渲染，等待至少一行或明确空目录
  await page.waitForTimeout(500)
  const hasRow = await page.locator('.sfb-row').first().isVisible().catch(() => false)
  if (!hasRow) {
    await expect(
      page.locator('.sfb-row').first().or(page.locator('.sfb-empty')),
    ).toBeVisible({ timeout: 15000 })
  }
}

export async function assertTerminalVisible(page: Page) {
  const term = page.locator('.xterm').first()
  await expect(term).toBeVisible({ timeout: TIMEOUT })
}

export async function termTypeAndCheck(page: Page, command: string, expected: string) {
  const textarea = page.locator('textarea.xterm-helper-textarea').first()
  await expect(textarea).toBeVisible({ timeout: 15000 })
  await textarea.click()
  // fill() 的 "\n" 不会触发 xterm 回车(只会作为文本插入), 必须显式 press Enter
  await textarea.fill(command.replace(/\n+$/, ''))
  await textarea.press('Enter')
  const deadline = Date.now() + 15000
  let text = ''
  while (Date.now() < deadline) {
    await page.waitForTimeout(500)
    text = (await page.locator('.xterm-rows').first().textContent().catch(() => '')) || ''
    if (text.includes(expected)) return
  }
  throw new Error(`terminal echo missing "${expected}" in: ${text.slice(-400)}`)
}

// ═══════════════════════════════════════════
//  控制台日志捕获
// ═══════════════════════════════════════════

export function setupConsoleCapture(page: Page, testName: string) {
  const logs: string[] = []
  page.on('console', msg => {
    const line = `[${msg.type()}] ${msg.text()}`
    logs.push(line)
    if (msg.type() === 'error' || msg.type() === 'warn') {
      console.log(`  [CONSOLE ${msg.type().toUpperCase()}] ${msg.text()}`)
    }
  })
  page.on('pageerror', err => {
    logs.push(`[PAGE_ERROR] ${err.message}`)
    console.log(`  [PAGE_ERROR] ${err.message}`)
  })
  return {
    getLogs: () => logs,
    dumpLogs: () => {
      console.log(`\n=== Console Logs: ${testName} ===`)
      logs.forEach(l => console.log(`  ${l}`))
      console.log(`=== End Console Logs ===\n`)
    }
  }
}

// ═══════════════════════════════════════════
//  VNC 操作
// ═══════════════════════════════════════════

export async function openVncTab(page: Page, name: string) {
  const card = page.locator(`.ssh-card:has-text("${name}")`).first()
  await expect(card).toBeVisible({ timeout: TIMEOUT })
  await card.locator('.ssh-card-main').click()
  await page.waitForTimeout(300)
  const vncBtn = page.locator('.ssh-sub-btn.type-vnc').first()
  await vncBtn.click()
  await page.waitForTimeout(5000)
}

export async function assertVncConnected(page: Page) {
  const canvas = page.locator('.vnc-canvas-wrap canvas').first()
  await expect(canvas).toBeVisible({ timeout: 10000 })
  const status = page.locator('.vnc-status').first()
  await expect(status).toHaveClass(/connected/)
}
