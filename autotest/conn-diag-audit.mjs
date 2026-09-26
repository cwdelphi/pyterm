// 阶段1: 9 配置连接模拟 + 拦截 /api/timeline/report 原始载荷
import { chromium } from 'playwright'
import fs from 'fs'

const BASE = 'https://127.0.0.1:5588'
const OUT = '/tmp/opencode/audit'
fs.mkdirSync(OUT, { recursive: true })

const SSH_CONNS = [
  { name: 'mtk', marker: 'AUD_SSH_MTK' },
  { name: '本机Agent', marker: 'AUD_SSH_LOCAL' },
  { name: '远程Agent', marker: 'AUD_SSH_REMOTE' },
  { name: '本地网关+本地Agent', marker: 'AUD_SSH_GWLOCAL' },
  { name: '本地网关+远程Agent', marker: 'AUD_SSH_GWREMOTE' },
]
const VNC_CONNS = [
  { name: '本地Agent', marker: '' },
  { name: '远程Agent', marker: '' },
  { name: 'VNC-本地网关+本地Agent', marker: '' },
  { name: 'VNC-本地网关+远程Agent', marker: '' },
]

const reports = []
const results = []

const browser = await chromium.launch({ headless: true, executablePath: '/usr/bin/google-chrome' })
const ctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1440, height: 900 } })
const page = await ctx.newPage()

page.on('request', (req) => {
  if (req.url().includes('/api/timeline/report') && req.method() === 'POST') {
    try {
      const body = JSON.parse(req.postData() || '{}')
      reports.push({ ts: Date.now(), body })
      console.log(`  [REPORT INTERCEPTED] room=${body.room_id} conn=${body.conn_name} success=${body.success} total=${body.duration_total}`)
    } catch (e) { console.log('  [REPORT PARSE ERR]', e.message) }
  }
})
const consoleLogs = []
page.on('console', (m) => { consoleLogs.push(`[${m.type()}] ${m.text()}`) })
page.on('pageerror', (e) => consoleLogs.push(`[PAGE_ERROR] ${e.message}`))

async function login() {
  await page.goto(BASE + '/', { waitUntil: 'domcontentloaded' })
  await page.waitForTimeout(800)
  const pw = page.locator('input[type="password"]')
  if (await pw.count()) {
    await page.locator('input:not([type="password"]):not([type="checkbox"])').first().fill('admin')
    await pw.first().fill('change_me_pass')
    await page.locator('button').filter({ hasText: /登\s*录/ }).first().click()
    await page.waitForTimeout(2000)
  }
}

async function goSshView() {
  await page.locator('.topmenu').filter({ hasText: '远程管理' }).first().click()
  await page.waitForTimeout(800)
}

async function expandGroup(label) {
  const header = page.locator('.group-header', { hasText: label }).first()
  const arrow = header.locator('.group-arrow').first()
  const expanded = await arrow.evaluate(el => el.classList.contains('expanded')).catch(() => false)
  if (!expanded) { await header.click(); await page.waitForTimeout(500) }
}

async function clickConn(name, group) {
  if (group === 'vnc') await expandGroup('VNC'); else await expandGroup('SSH/FILE')
  const typeCls = group === 'vnc' ? 'type-vnc' : 'type-ssh'
  // 按分组类型类限定，再在同类型内用可见性 + 名称精确匹配（避免 远程Agent vs 本地网关+远程Agent 子串误命中）
  let target = null
  const cards = page.locator(`.ssh-card.${typeCls}`)
  const n = await cards.count()
  for (let i = 0; i < n; i++) {
    const c = cards.nth(i)
    if (!(await c.isVisible().catch(() => false))) continue
    const nm = (await c.locator('.ssh-card-name').textContent().catch(() => '')) || ''
    if (nm.trim() === name) { target = c; break }
  }
  if (!target) {
    await expandGroup(group === 'vnc' ? 'SSH/FILE' : 'VNC')
    const n2 = await cards.count()
    for (let i = 0; i < n2; i++) {
      const c = cards.nth(i)
      if (!(await c.isVisible().catch(() => false))) continue
      const nm = (await c.locator('.ssh-card-name').textContent().catch(() => '')) || ''
      if (nm.trim() === name) { target = c; break }
    }
    await expandGroup(group === 'vnc' ? 'SSH/FILE' : 'VNC')
  }
  if (!target) throw new Error(`visible card not found: ${name} (${group})`)
  await target.locator('.ssh-card-main').click()
  await page.waitForTimeout(500)
}

async function runSsh(c) {
  const t0 = Date.now()
  const r = { kind: 'ssh', name: c.name, ok: false, err: '', ms: 0 }
  try {
    await clickConn(c.name, 'ssh')
    await page.locator('.ssh-sub-btn.type-ssh').first().click()
    await page.waitForTimeout(3000)
    const ta = page.locator('textarea.xterm-helper-textarea').first()
    await ta.waitFor({ state: 'visible', timeout: 20000 })
    await ta.click()
    await ta.fill(`echo ${c.marker}\n`)
    const deadline = Date.now() + 20000
    let text = ''
    while (Date.now() < deadline) {
      await page.waitForTimeout(500)
      text = (await page.locator('.xterm-rows').first().textContent().catch(() => '')) || ''
      if (text.includes(c.marker)) break
    }
    r.ok = text.includes(c.marker)
    if (!r.ok) r.err = `echo missing: ${text.slice(-200)}`
    await page.screenshot({ path: `${OUT}/ssh-${c.marker}.png` })
    // 关闭当前 tab（若有关闭按钮）
    const closeBtn = page.locator('.ssh-tab.active .tab-close').first()
    if (await closeBtn.isVisible({ timeout: 1000 }).catch(() => false)) await closeBtn.click().catch(() => {})
    await page.waitForTimeout(1000)
  } catch (e) { r.err = String(e.message || e); await page.screenshot({ path: `${OUT}/ssh-FAIL-${c.marker}.png` }).catch(() => {}) }
  r.ms = Date.now() - t0
  results.push(r)
  console.log(`  => ${r.ok ? 'OK' : 'FAIL'} ${c.name} (${r.ms}ms) ${r.err}`)
}

async function runVnc(c) {
  const t0 = Date.now()
  const r = { kind: 'vnc', name: c.name, ok: false, err: '', ms: 0 }
  try {
    await clickConn(c.name, 'vnc')
    await page.locator('.ssh-sub-btn.type-vnc').first().click()
    const canvas = page.locator('.vnc-canvas-wrap canvas').first()
    await canvas.waitFor({ state: 'visible', timeout: 25000 })
    await page.waitForTimeout(3000)
    const status = await page.locator('.vnc-status').first().getAttribute('class').catch(() => '')
    r.ok = /connected/.test(status || '')
    if (!r.ok) r.err = `vnc status class: ${status}`
    const slug = c.name.replace(/[^a-zA-Z0-9]+/g, '_')
    await page.screenshot({ path: `${OUT}/vnc-${slug}.png` })
    const closeBtn = page.locator('.ssh-tab.active .tab-close').first()
    if (await closeBtn.isVisible({ timeout: 1000 }).catch(() => false)) await closeBtn.click().catch(() => {})
    await page.waitForTimeout(1000)
  } catch (e) { r.err = String(e.message || e); const slug = c.name.replace(/[^a-zA-Z0-9]+/g, '_'); await page.screenshot({ path: `${OUT}/vnc-FAIL-${slug}.png` }).catch(() => {}) }
  r.ms = Date.now() - t0
  results.push(r)
  console.log(`  => ${r.ok ? 'OK' : 'FAIL'} VNC ${c.name} (${r.ms}ms) ${r.err}`)
}

console.log('== login ==')
await login()
await goSshView()

console.log('== SSH connections (5) ==')
for (const c of SSH_CONNS) { await runSsh(c); await page.waitForTimeout(1500) }

console.log('== VNC connections (4) ==')
for (const c of VNC_CONNS) { await runVnc(c); await page.waitForTimeout(1500) }

// 等诊断上报落库
await page.waitForTimeout(5000)

fs.writeFileSync(`${OUT}/results.json`, JSON.stringify(results, null, 2))
fs.writeFileSync(`${OUT}/reports.json`, JSON.stringify(reports, null, 2))
fs.writeFileSync(`${OUT}/console.log`, consoleLogs.join('\n'))

console.log('\n== SUMMARY ==')
for (const r of results) console.log(`${r.ok ? 'OK  ' : 'FAIL'} ${r.kind} ${r.name} ${r.ms}ms ${r.err}`)
console.log(`reports intercepted: ${reports.length}`)
console.log(`screenshots: ${fs.readdirSync(OUT).filter(f => f.endsWith('.png')).length}`)

await browser.close()
