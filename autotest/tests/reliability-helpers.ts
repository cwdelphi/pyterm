import { Page, expect } from '@playwright/test'

const BASE_URL = process.env.BASE_URL || 'https://127.0.0.1:5588'

export interface PhaseTimings {
  wsOpen: number; signalOk: number; rtcConnected: number;
  dcOpen: number; firstData: number; start: number;
  duration_ws: number; duration_signal: number; duration_ice: number;
  duration_dc: number; duration_data: number; duration_total: number;
  agent_connect_ms: number; success: number; error_stage: string;
}

export async function loginAs(page: Page, user = 'admin', pass = 'change_me_pass') {
  await page.goto('/')
  await page.waitForLoadState('domcontentloaded')
  await page.waitForTimeout(300)
  const password = page.locator('input[type="password"]').first()
  const needLogin = await password.isVisible({ timeout: 2000 }).catch(() => false)
  if (needLogin) {
    const userIn = page.locator('input:not([type="password"]):not([type="checkbox"]):not([type="radio"])').first()
    await userIn.fill(user)
    await password.fill(pass)
    await page.locator('button[type="submit"], button:has-text("登录")').first().click()
    await page.waitForTimeout(1500)
  } else {
    await page.waitForTimeout(300)
  }
}

export async function goSshView(page: Page) {
  await loginAs(page)
  const btn = page.locator('.topmenu:has-text("远程管理")').first()
  if (await btn.isVisible({ timeout: 3000 }).catch(() => false)) {
    await btn.click()
    await page.waitForTimeout(500)
  }
}

export async function openFirstConnection(page: Page) {
  const card = page.locator('.ssh-card-main').first()
  if (!(await card.isVisible({ timeout: 3000 }).catch(() => false))) {
    const group = page.locator('.group-header').first()
    if (await group.isVisible({ timeout: 2000 }).catch(() => false)) {
      await group.click()
      await page.waitForTimeout(300)
    }
  }
  const main = page.locator('.ssh-card-main').first()
  await expect(main).toBeVisible({ timeout: 5000 })
  await main.click()
  await page.waitForTimeout(300)
}

export async function openSshTerminalByName(page: Page, name: string) {
  const card = page.locator('.ssh-card', { hasText: name }).first()
  if (!(await card.isVisible({ timeout: 3000 }).catch(() => false))) {
    for (const groupType of ['SSH', 'VNC', 'RDP']) {
      const hdr = page.locator('.group-header', { hasText: groupType }).first()
      if (await hdr.isVisible({ timeout: 1000 }).catch(() => false)) {
        await hdr.click()
        await page.waitForTimeout(300)
      }
    }
  }
  const c = page.locator('.ssh-card', { hasText: name }).first()
  await expect(c).toBeVisible({ timeout: 5000 })
  await c.locator('.ssh-card-main').click()
  await page.waitForTimeout(300)
  const sshBtn = page.locator('.ssh-sub-btn.type-ssh').first()
  await sshBtn.click()
  await page.waitForTimeout(3000)
}

export async function measurePhaseTimings(page: Page): Promise<PhaseTimings | null> {
  try {
    const result = await page.evaluate(() => {
      const w = window as any
      const wm = w.__webrtcManager || w._webrtcManager
      if (wm && wm.diag && wm.diag.start) {
        const d = wm.diag
        return {
          start: d.start || 0, wsOpen: d.wsOpen || 0, signalOk: d.signalOk || 0,
          rtcConnected: d.rtcConnected || 0, dcOpen: d.dcOpen || 0, firstData: d.firstData || 0,
          duration_ws: d.wsOpen ? d.wsOpen - d.start : 0,
          duration_signal: d.signalOk && d.wsOpen ? d.signalOk - d.wsOpen : 0,
          duration_ice: d.rtcConnected && d.signalOk ? d.rtcConnected - d.signalOk : 0,
          duration_dc: d.dcOpen && d.rtcConnected ? d.dcOpen - d.rtcConnected : 0,
          duration_data: d.firstData && d.dcOpen ? d.firstData - d.dcOpen : 0,
          duration_total: (d.firstData || d.error || performance.now()) - d.start,
          agent_connect_ms: d.agent_connect_ms || 0,
          success: d.errorMsg ? 0 : 1,
          error_stage: d.errorStage || '',
        }
      }
      return null
    })
    if (result) return result
  } catch {}
  return null
}

export async function waitForDcOpen(page: Page, timeoutMs = 15000): Promise<boolean> {
  const start = Date.now()
  while (Date.now() - start < timeoutMs) {
    const t = await measurePhaseTimings(page)
    if (t && t.dcOpen > 0) return true
    await page.waitForTimeout(200)
  }
  return false
}

export async function waitForFirstData(page: Page, timeoutMs = 15000): Promise<boolean> {
  const start = Date.now()
  while (Date.now() - start < timeoutMs) {
    try {
      const t = await measurePhaseTimings(page)
      if (t && t.firstData > 0) return true
      if (t && t.dcOpen > 0) return true
    } catch {}
    const termVisible = await page.locator('.xterm-rows').first().isVisible().catch(() => false)
    if (termVisible) {
      const text = await page.locator('.xterm-rows').first().textContent().catch(() => '')
      if (text && text.length > 10) return true
    }
    await page.waitForTimeout(300)
  }
  return false
}

export async function termTypeAndRead(page: Page, cmd: string, expected: string, timeout = 5000): Promise<boolean> {
  const textarea = page.locator('textarea.xterm-helper-textarea').first()
  if (!(await textarea.isVisible({ timeout: 3000 }).catch(() => false))) return false
  await textarea.fill(cmd + '\n')
  await page.waitForTimeout(2000)
  const text = await page.locator('.xterm-rows').first().textContent() || ''
  return text.includes(expected)
}

export async function apiLogin(page: Page): Promise<string> {
  const resp = await page.request.post(BASE_URL + '/api/auth/login', {
    data: { username: 'admin', password: 'change_me_pass' },
  })
  const data = await resp.json()
  return data.token || ''
}

export async function apiGetDiagnostics(page: Page, token: string, params: any = {}): Promise<any> {
  const resp = await page.request.post(BASE_URL + '/api/timeline/records', {
    headers: { Authorization: 'Bearer ' + token },
    data: { page: params.page || 1, page_size: params.page_size || 10, ...params },
  })
  return await resp.json()
}

export async function apiGetDiagStats(page: Page, token: string): Promise<any> {
  const resp = await page.request.post(BASE_URL + '/api/timeline/stats', {
    headers: { Authorization: 'Bearer ' + token },
    data: {},
  })
  return await resp.json()
}

export async function apiGetDiagDetail(page: Page, token: string, roomId: string): Promise<any> {
  const resp = await page.request.post(BASE_URL + '/api/timeline/detail', {
    headers: { Authorization: 'Bearer ' + token },
    data: { room_id: roomId },
  })
  return await resp.json()
}

export async function apiReportDiag(page: Page, token: string, data: any): Promise<any> {
  const resp = await page.request.post(BASE_URL + '/api/timeline/report', {
    headers: { Authorization: 'Bearer ' + token },
    data,
  })
  return await resp.json()
}

export interface TimelineCapture {
  reports: any[]
  stop: () => void
}

export function startTimelineCapture(page: Page): TimelineCapture {
  const reports: any[] = []
  const onRequest = (req: any) => {
    try {
      if (req.method() === 'POST' && req.url().includes('/api/timeline/report')) {
        const pd = req.postData()
        if (pd) reports.push(JSON.parse(pd))
      }
    } catch {}
  }
  page.on('request', onRequest)
  return {
    reports,
    stop: () => {
      try { page.off('request', onRequest) } catch {}
    },
  }
}

export interface LinkDiag {
  clientReport: any | null
  serverRecord: any | null
  serverDetail: any | null
  phase: PhaseTimings | null
  roomId: string
}

export async function collectLinkDiag(
  page: Page,
  token: string,
  capture?: TimelineCapture,
  waitMs = 1500,
): Promise<LinkDiag> {
  await page.waitForTimeout(waitMs)
  const phase = await measurePhaseTimings(page)
  const clientReport = capture && capture.reports.length
    ? capture.reports[capture.reports.length - 1]
    : null
  const records = await apiGetDiagnostics(page, token, { page: 1, page_size: 5 })
  const items = records?.items || []
  let serverRecord = items[0] || null
  if (clientReport?.room_id) {
    serverRecord = items.find((i: any) => i.room_id === clientReport.room_id) || serverRecord
  }
  const roomId = clientReport?.room_id || serverRecord?.room_id || ''
  let serverDetail: any = null
  if (roomId) {
    serverDetail = await apiGetDiagDetail(page, token, roomId)
  }
  return { clientReport, serverRecord, serverDetail, phase, roomId }
}

export function tabCount(page: Page): Promise<number> {
  return page.evaluate(() => document.querySelectorAll('.ssh-tab').length)
}

export function getMemoryUsage(page: Page): Promise<number> {
  return page.evaluate(() => {
    const m = (performance as any).memory
    return m ? m.usedJSHeapSize : 0
  })
}

export async function closeAllSshTabs(page: Page) {
  const tabs = page.locator('.ssh-tab .tab-close')
  const count = await tabs.count()
  for (let i = count - 1; i >= 0; i--) {
    await tabs.nth(i).click().catch(() => {})
    await page.waitForTimeout(300)
  }
}
