import { test, expect, Page } from '@playwright/test'
import * as fs from 'fs'
import * as path from 'path'
import {
  loginAsAdmin, apiLogin, goSshView, assertTerminalVisible,
  termTypeAndCheck, setupConsoleCapture,
} from '../helpers'
import {
  startTimelineCapture, collectLinkDiag, waitForFirstData, closeAllSshTabs,
} from '../reliability-helpers'

const BASE_URL = process.env.BASE_URL || 'https://127.0.0.1:5588'
// Keep case/meta JSON outside Playwright outputDir (test-results is wiped each run).
// JSONL append-only avoids lost rows if a mid-run rewrite races.
const ARTIFACT_DIR = path.resolve(__dirname, '../../artifacts')
const RESULTS_FILE = path.join(ARTIFACT_DIR, 'remote-cases.json')
const RESULTS_LOG = path.join(ARTIFACT_DIR, 'remote-cases.jsonl')
const META_FILE = path.join(ARTIFACT_DIR, 'remote-timeline-meta.json')
const SCREEN_DIR = path.resolve(__dirname, '../../screenshots')

interface RemoteConn {
  id: string
  name: string
  connection_type: 'ssh' | 'vnc'
  host: string
  port: number
  vnc_port?: number
  agent_id: string
  gateway_id: string
}

interface CaseResult {
  caseId: string
  connId: string
  name: string
  connType: string
  agentId: string
  gatewayId: string
  expectedPathMode: string
  status: 'passed' | 'failed' | 'skipped'
  error?: string
  roomId?: string
  pathMode?: string
  durationTotal?: number
  durationWs?: number
  durationSignal?: number
  durationIce?: number
  durationDc?: number
  durationData?: number
  agentConnectMs?: number
  success?: number
  errorStage?: string
  errorMsg?: string
  screenshot?: string
  steps?: number
  totalSteps?: number
}

function ensureResults() {
  fs.mkdirSync(ARTIFACT_DIR, { recursive: true })
  fs.mkdirSync(SCREEN_DIR, { recursive: true })
  if (!fs.existsSync(RESULTS_FILE)) fs.writeFileSync(RESULTS_FILE, '[]')
  if (!fs.existsSync(RESULTS_LOG)) fs.writeFileSync(RESULTS_LOG, '')
}

function readResults(): CaseResult[] {
  ensureResults()
  const map = new Map<string, CaseResult>()
  // JSONL is authoritative (append-only); merge JSON only for keys missing from jsonl
  try {
    const lines = fs.readFileSync(RESULTS_LOG, 'utf8').split('\n').filter(Boolean)
    for (const line of lines) {
      try {
        const r = JSON.parse(line) as CaseResult
        if (r && r.caseId) map.set(r.caseId, r)
      } catch {}
    }
  } catch {}
  try {
    const fromJson = JSON.parse(fs.readFileSync(RESULTS_FILE, 'utf8') || '[]')
    if (Array.isArray(fromJson)) {
      for (const r of fromJson as CaseResult[]) {
        if (r && r.caseId && !map.has(r.caseId)) map.set(r.caseId, r)
      }
    }
  } catch {}
  return Array.from(map.values())
}

function appendResult(r: CaseResult) {
  ensureResults()
  try {
    fs.appendFileSync(RESULTS_LOG, JSON.stringify(r) + '\n')
  } catch (e) {
    console.log(`[appendResult] JSONL FAIL ${r.caseId}`, e)
  }
  const list = readResults()
  try {
    fs.writeFileSync(RESULTS_FILE, JSON.stringify(list, null, 2))
    console.log(`[appendResult] ${r.caseId}=${r.status} total=${list.length}`)
  } catch (e) {
    console.log(`[appendResult] WRITE FAIL ${r.caseId}`, e)
  }
}

function expectedPathMode(c: RemoteConn): string {
  return c.gateway_id ? 'gateway' : 'direct'
}

async function apiGetConns(page: Page, token: string): Promise<RemoteConn[]> {
  const resp = await page.request.get(BASE_URL + '/api/ssh', {
    headers: { Authorization: 'Bearer ' + token },
    timeout: 15000,
  })
  const data = await resp.json()
  return Array.isArray(data) ? data : (data.connections || data.items || [])
}

async function apiAgentOnline(
  agentCache: Map<string, boolean>,
  page: Page,
  token: string,
  agentId: string,
): Promise<boolean> {
  if (!agentId) return true
  if (agentCache.has(agentId)) return agentCache.get(agentId)!
  try {
    const resp = await page.request.get(BASE_URL + '/api/admin/agents', {
      headers: { Authorization: 'Bearer ' + token },
      timeout: 10000,
    })
    const data = await resp.json()
    const list = data.agents || data
    const a = (list || []).find((x: any) => x.id === agentId)
    const online = !!a && !!a.online
    for (const x of list || []) agentCache.set(x.id, !!x.online)
    if (!agentCache.has(agentId)) agentCache.set(agentId, online)
    return agentCache.get(agentId)!
  } catch {
    return true
  }
}

async function ensureGroupExpanded(page: Page, group: 'ssh' | 'vnc') {
  const label = group === 'ssh' ? 'SSH/FILE' : 'VNC'
  const header = page.locator('.group-header', { hasText: label }).first()
  await expect(header).toBeVisible({ timeout: 8000 })
  const expanded = await page.locator('.group-header', { hasText: label })
    .locator('.group-arrow.expanded').count()
  if (!expanded) {
    await header.click()
    await page.waitForTimeout(400)
  }
}

async function openConnCard(page: Page, conn: RemoteConn) {
  await goSshView(page)
  await ensureGroupExpanded(page, conn.connection_type === 'vnc' ? 'vnc' : 'ssh')
  const card = page.locator(`.ssh-card[data-id="${conn.id}"]`)
  await expect(card).toBeVisible({ timeout: 10000 })
  if (!(await card.locator('.ssh-card-expanded').isVisible().catch(() => false))) {
    await card.locator('.ssh-card-main').click()
    await page.waitForTimeout(300)
  }
  return card
}

async function collectAndRecord(
  page: Page,
  token: string,
  capture: ReturnType<typeof startTimelineCapture>,
  base: Omit<CaseResult, 'roomId' | 'pathMode' | 'durationTotal'>,
  screenshotName: string,
): Promise<CaseResult> {
  const diag = await collectLinkDiag(page, token, capture, 1800)
  const shot = path.join(SCREEN_DIR, screenshotName)
  await page.screenshot({ path: shot }).catch(() => {})
  const client = diag.clientReport || {}
  const conn = diag.serverDetail?.connection || {}
  const record = diag.serverRecord || {}
  const result: CaseResult = {
    ...base,
    roomId: diag.roomId || client.room_id || record.room_id || '',
    pathMode: conn.path_mode || client.path_mode || '',
    durationTotal: conn.duration_total ?? client.duration_total ?? record.duration_total,
    durationWs: client.duration_ws,
    durationSignal: client.duration_signal,
    durationIce: client.duration_ice,
    durationDc: client.duration_dc,
    durationData: client.duration_data,
    agentConnectMs: client.agent_connect_ms ?? conn.agent_connect_ms,
    success: conn.success ?? record.success,
    errorStage: conn.error_stage || client.error_stage || '',
    errorMsg: conn.error_msg || client.error_msg || '',
    screenshot: `screenshots/${screenshotName}`,
    steps: conn.completed_steps,
    totalSteps: conn.total_steps,
  }
  appendResult(result)
  // Do NOT stop capture here — RC-S/RC-V loops reuse the same listener across conns
  return result
}

function buildCaseId(conn: RemoteConn, seq: number): string {
  const prefix = conn.connection_type === 'vnc' ? 'RC-V' : 'RC-S'
  return `${prefix}${seq}`
}

test.describe('远程管理全量连接 RC', () => {
  let token = ''
  let conns: RemoteConn[] = []
  let sshSeq = 0
  let vncSeq = 0
  const agentCache = new Map<string, boolean>()

  test.beforeAll(async ({ browser }) => {
    ensureResults()
    // Do NOT clear results here: Playwright re-runs beforeAll after worker
    // restarts (when a mid-suite test fails), which used to wipe prior cases.
    const page = await browser.newPage()
    await loginAsAdmin(page)
    token = await apiLogin(page)
    conns = await apiGetConns(page, token)
    expect(conns.length).toBeGreaterThan(0)
    await page.close()
  })

  test('RC-ENV 连接清单与 Agent/网关在线', async ({ page }) => {
    // First test of the suite: safe place for a one-shot clear of prior-run artifacts
    if (!globalThis.__rcEnvCleared) {
      globalThis.__rcEnvCleared = true
      fs.writeFileSync(RESULTS_FILE, '[]')
      fs.writeFileSync(RESULTS_LOG, '')
      fs.writeFileSync(path.join(ARTIFACT_DIR, 'run-stamp.txt'), String(Date.now()) + '\n')
      console.log(`[RC-ENV] cleared prior-run results`)
    }
    await loginAsAdmin(page)
    const agentsOk: string[] = []
    const agentsBad: string[] = []
    for (const c of conns) {
      if (!c.agent_id) continue
      if (agentsOk.includes(c.agent_id) || agentsBad.includes(c.agent_id)) continue
      const online = await apiAgentOnline(agentCache, page, token, c.agent_id)
      ;(online ? agentsOk : agentsBad).push(c.agent_id)
    }
    const gwResp = await page.request.get(BASE_URL + '/api/admin/gateways', {
      headers: { Authorization: 'Bearer ' + token },
      timeout: 10000,
    })
    const gwData = await gwResp.json()
    const gws = gwData.gateways || []
    console.log(`[RC-ENV] conns=${conns.length} agents_online=${agentsOk.join(',')} offline=${agentsBad.join(',') || '-'} gateways=${gws.map((g: any) => g.id + ':' + (g.online ? 'up' : 'down')).join(',')}`)
    expect(conns.length).toBeGreaterThanOrEqual(9)
    await page.screenshot({ path: path.join(SCREEN_DIR, 'RC-ENV.png') })
    appendResult({
      caseId: 'RC-ENV', connId: '-', name: '连接清单', connType: 'env',
      agentId: agentsOk.join(',') || '-', gatewayId: gws.map((g: any) => g.id).join(',') || '-',
      expectedPathMode: '-',
      status: agentsBad.length ? 'failed' : 'passed',
      error: agentsBad.length ? `offline agents: ${agentsBad.join(',')}` : undefined,
      screenshot: 'screenshots/RC-ENV.png',
    })
  })

  test('RC-S 全量 SSH 连接', async ({ page }) => {
    test.setTimeout(600000)
    const token2 = token || (await apiLogin(page))
    const list = conns.filter(c => (c.connection_type || 'ssh') === 'ssh')
    expect(list.length).toBeGreaterThanOrEqual(4)
    const capture = startTimelineCapture(page)
    const consoleCap = setupConsoleCapture(page, 'RC-S')
    const failures: string[] = []
    for (const conn of list) {
      sshSeq += 1
      const caseId = buildCaseId(conn, sshSeq)
      const base = {
        caseId,
        connId: conn.id,
        name: conn.name,
        connType: 'ssh',
        agentId: conn.agent_id || '',
        gatewayId: conn.gateway_id || '',
        expectedPathMode: expectedPathMode(conn),
        status: 'failed' as const,
      }
      try {
        const agentUp = await apiAgentOnline(agentCache, page, token2, conn.agent_id)
        if (!agentUp) {
          appendResult({ ...base, status: 'skipped', error: `agent offline: ${conn.agent_id}` })
          continue
        }
        const card = await openConnCard(page, conn)
        capture.reports.length = 0
        await card.locator('.ssh-sub-btn.type-ssh').click()
        let dataOk = false
        let lastErr = ''
        for (let attempt = 1; attempt <= 2 && !dataOk; attempt++) {
          if (attempt > 1) {
            console.log(`[RC-S] ${caseId} attempt1 failed (${lastErr}) — retry`)
            await closeAllSshTabs(page).catch(() => {})
            await page.waitForTimeout(800)
            const card2 = await openConnCard(page, conn)
            capture.reports.length = 0
            await card2.locator('.ssh-sub-btn.type-ssh').click()
          }
          await page.waitForTimeout(2500)
          dataOk = await waitForFirstData(page, 30000)
          if (!dataOk) lastErr = 'first data timeout'
        }
        if (!dataOk) throw new Error(lastErr || 'first data timeout')
        await assertTerminalVisible(page)
        await termTypeAndCheck(page, `echo "RC_OK_${conn.id}"`, `RC_OK_${conn.id}`)
        const result = await collectAndRecord(page, token2, capture, { ...base, status: 'passed' }, `${caseId}.png`)
        const expectMode = expectedPathMode(conn)
        if (result.pathMode && result.pathMode !== expectMode) {
          const msg = `path_mode=${result.pathMode} expected=${expectMode}`
          appendResult({ ...result, status: 'failed', error: msg })
          failures.push(`${caseId}:${conn.name}:${msg}`)
        }
        await closeAllSshTabs(page)
        await page.waitForTimeout(500)
      } catch (e: any) {
        const msg = (e?.message || String(e)).split('\n')[0].slice(0, 300)
        appendResult({
          caseId, connId: conn.id, name: conn.name, connType: 'ssh',
          agentId: conn.agent_id || '', gatewayId: conn.gateway_id || '',
          expectedPathMode: expectedPathMode(conn),
          status: 'failed', error: msg,
          screenshot: `screenshots/${caseId}.png`,
        })
        await page.screenshot({ path: path.join(SCREEN_DIR, `${caseId}.png`) }).catch(() => {})
        await closeAllSshTabs(page).catch(() => {})
        failures.push(`${caseId}:${conn.name}:${msg}`)
      }
    }
    consoleCap.dumpLogs()
    capture.stop()
    expect(failures, `SSH failures: ${failures.join(' | ')}`).toEqual([])
  })

  test('RC-V 全量 VNC 连接', async ({ page }) => {
    test.setTimeout(600000)
    const token2 = token || (await apiLogin(page))
    const list = conns.filter(c => c.connection_type === 'vnc')
    expect(list.length).toBeGreaterThanOrEqual(4)
    const capture = startTimelineCapture(page)
    const failures: string[] = []
    for (const conn of list) {
      vncSeq += 1
      const caseId = buildCaseId(conn, vncSeq)
      const base = {
        caseId,
        connId: conn.id,
        name: conn.name,
        connType: 'vnc',
        agentId: conn.agent_id || '',
        gatewayId: conn.gateway_id || '',
        expectedPathMode: expectedPathMode(conn),
        status: 'failed' as const,
      }
      try {
        const agentUp = await apiAgentOnline(agentCache, page, token2, conn.agent_id)
        if (!agentUp) {
          appendResult({ ...base, status: 'skipped', error: `agent offline: ${conn.agent_id}` })
          continue
        }
        const card = await openConnCard(page, conn)
        capture.reports.length = 0
        await card.locator('.ssh-sub-btn.type-vnc').click()
        let lastStatus = ''
        let ready = false
        for (let attempt = 1; attempt <= 2 && !ready; attempt++) {
          const vncDeadline = Date.now() + (attempt === 1 ? 40000 : 30000)
          while (Date.now() < vncDeadline) {
            lastStatus = (await page.locator('.vnc-status').first().textContent().catch(() => '')) || ''
            const connected = await page.locator('.vnc-status.connected').isVisible().catch(() => false)
            const canvas = await page.locator('.vnc-canvas-wrap canvas').isVisible().catch(() => false)
            if (connected && canvas) { ready = true; break }
            if (/error|失败|断开/i.test(lastStatus) && attempt === 1) break
            await page.waitForTimeout(500)
          }
          if (!ready && attempt === 1) {
            console.log(`[RC-V] ${caseId} attempt1 fail status="${lastStatus.trim()}" — retry`)
            await closeAllSshTabs(page).catch(() => {})
            await page.waitForTimeout(800)
            const card2 = await openConnCard(page, conn)
            capture.reports.length = 0
            await card2.locator('.ssh-sub-btn.type-vnc').click()
          }
        }
        if (!ready) throw new Error(`VNC not ready status="${lastStatus.trim()}" after 2 attempts`.slice(0, 300))
        const result = await collectAndRecord(page, token2, capture, { ...base, status: 'passed' }, `${caseId}.png`)
        const expectMode = expectedPathMode(conn)
        if (result.pathMode && result.pathMode !== expectMode) {
          const msg = `path_mode=${result.pathMode} expected=${expectMode}`
          appendResult({ ...result, status: 'failed', error: msg })
          failures.push(`${caseId}:${conn.name}:${msg}`)
        }
        await closeAllSshTabs(page)
        await page.waitForTimeout(500)
      } catch (e: any) {
        const msg = (e?.message || String(e)).split('\n')[0].slice(0, 300)
        appendResult({
          caseId, connId: conn.id, name: conn.name, connType: 'vnc',
          agentId: conn.agent_id || '', gatewayId: conn.gateway_id || '',
          expectedPathMode: expectedPathMode(conn),
          status: 'failed', error: msg,
          screenshot: `screenshots/${caseId}.png`,
        })
        await page.screenshot({ path: path.join(SCREEN_DIR, `${caseId}.png`) }).catch(() => {})
        await closeAllSshTabs(page).catch(() => {})
        failures.push(`${caseId}:${conn.name}:${msg}`)
      }
    }
    capture.stop()
    expect(failures, `VNC failures: ${failures.join(' | ')}`).toEqual([])
  })

  test('RC-F SFTP 文件浏览（优先本地 Agent SSH）', async ({ page }) => {
    test.setTimeout(300000)
    const token2 = token || (await apiLogin(page))
    const preferred = conns.filter(c =>
      (c.connection_type || 'ssh') === 'ssh' &&
      (c.agent_id === 'local-agent' || c.agent_id === 'test-tunnel'),
    )
    const targets = preferred.length ? preferred.slice(0, 2) : conns.filter(c => (c.connection_type || 'ssh') === 'ssh').slice(0, 2)
    expect(targets.length).toBeGreaterThan(0)
    const failures: string[] = []
    let fSeq = 0
    for (const conn of targets) {
      fSeq += 1
      const caseId = `RC-F${fSeq}`
      try {
        const agentUp = await apiAgentOnline(agentCache, page, token2, conn.agent_id)
        if (!agentUp) {
          appendResult({
            caseId, connId: conn.id, name: conn.name, connType: 'sftp',
            agentId: conn.agent_id || '', gatewayId: conn.gateway_id || '',
            expectedPathMode: expectedPathMode(conn),
            status: 'skipped', error: `agent offline: ${conn.agent_id}`,
          })
          continue
        }
        const card = await openConnCard(page, conn)
        await card.locator('.ssh-sub-btn.type-file').click()
        await expect(page.locator('.sfb-table, .sfb-row, .sfb-breadcrumb').first()).toBeVisible({ timeout: 25000 })
        await page.waitForTimeout(1500)
        await page.screenshot({ path: path.join(SCREEN_DIR, `${caseId}.png`) })
        appendResult({
          caseId, connId: conn.id, name: conn.name, connType: 'sftp',
          agentId: conn.agent_id || '', gatewayId: conn.gateway_id || '',
          expectedPathMode: expectedPathMode(conn),
          status: 'passed', screenshot: `screenshots/${caseId}.png`,
        })
        await closeAllSshTabs(page)
        await page.waitForTimeout(400)
      } catch (e: any) {
        const msg = (e?.message || String(e)).split('\n')[0].slice(0, 300)
        appendResult({
          caseId, connId: conn.id, name: conn.name, connType: 'sftp',
          agentId: conn.agent_id || '', gatewayId: conn.gateway_id || '',
          expectedPathMode: expectedPathMode(conn),
          status: 'failed', error: msg,
          screenshot: `screenshots/${caseId}.png`,
        })
        await page.screenshot({ path: path.join(SCREEN_DIR, `${caseId}.png`) }).catch(() => {})
        await closeAllSshTabs(page).catch(() => {})
        failures.push(`${caseId}:${msg}`)
      }
    }
    expect(failures, `SFTP failures: ${failures.join(' | ')}`).toEqual([])
  })

  test('RC-ADM 管理后台连接诊断面板', async ({ page }) => {
    const caseBase = {
      caseId: 'RC-ADM', connId: '-', name: '连接诊断面板', connType: 'admin',
      agentId: '', gatewayId: '', expectedPathMode: '-',
      status: 'failed' as const,
      screenshot: 'screenshots/RC-ADM.png',
    }
    try {
      await loginAsAdmin(page)
      const adminBtn = page.locator('.topmenu:has-text("系统管理")').first()
      await expect(adminBtn).toBeVisible({ timeout: 8000 })
      await adminBtn.click()
      await page.waitForTimeout(800)
      const diagItem = page.locator('.sidebar-nav .menu-item', { hasText: '连接诊断' }).first()
      await expect(diagItem).toBeVisible({ timeout: 8000 })
      await diagItem.click()
      await expect(page.locator('.dp-tabs')).toBeVisible({ timeout: 8000 })
      await expect(page.locator('.dp-table')).toBeVisible({ timeout: 8000 })
      const rowCount = await page.locator('.dp-table tbody tr').count()
      expect(rowCount).toBeGreaterThan(0)
      const firstTimeline = page.locator('.dp-link').first()
      if (await firstTimeline.isVisible().catch(() => false)) {
        await firstTimeline.click()
        await expect(page.locator('.dp-mermaid-wrap, .dp-info').first()).toBeVisible({ timeout: 10000 })
        await page.screenshot({ path: path.join(SCREEN_DIR, 'RC-ADM-timeline.png') })
      }
      const statsTab = page.locator('.dp-tabs button', { hasText: /统计|Statistics|stats/i }).first()
      if (await statsTab.isVisible().catch(() => false)) {
        await statsTab.click()
        await expect(page.locator('.dp-stats, .dp-stat-card').first()).toBeVisible({ timeout: 8000 })
      }
      await page.screenshot({ path: path.join(SCREEN_DIR, 'RC-ADM.png') })
      appendResult({ ...caseBase, status: 'passed' })
    } catch (e: any) {
      const msg = (e?.message || String(e)).split('\n')[0].slice(0, 300)
      await page.screenshot({ path: path.join(SCREEN_DIR, 'RC-ADM.png') }).catch(() => {})
      appendResult({ ...caseBase, status: 'failed', error: msg })
      throw e
    }
  })

  test('RC-TL Timeline API 汇总（供报告）', async ({ page }) => {
    await loginAsAdmin(page)
    const t = token || (await apiLogin(page))
    const statsResp = await page.request.post(BASE_URL + '/api/timeline/stats', {
      headers: { Authorization: 'Bearer ' + t },
      data: {},
    })
    const stats = await statsResp.json()
    expect(stats.total).toBeGreaterThan(0)
    const recResp = await page.request.post(BASE_URL + '/api/timeline/records', {
      headers: { Authorization: 'Bearer ' + t },
      data: { page: 1, page_size: 50 },
    })
    const recs = await recResp.json()
    const failures = (recs.items || []).filter((r: any) => !r.success)
    console.log(`[RC-TL] total=${stats.total} success_rate=${stats.success_rate}% avg=${stats.avg_total}ms recent_fail=${failures.length}`)
    ensureResults()
    const metaFile = META_FILE
    fs.writeFileSync(metaFile, JSON.stringify({ stats, recent: recs.items || [], failures }, null, 2))
    await page.screenshot({ path: path.join(SCREEN_DIR, 'RC-TL.png') })
    appendResult({
      caseId: 'RC-TL', connId: '-', name: 'Timeline API', connType: 'api',
      agentId: '', gatewayId: '', expectedPathMode: '-',
      status: 'passed', screenshot: 'screenshots/RC-TL.png',
    })
  })
})
