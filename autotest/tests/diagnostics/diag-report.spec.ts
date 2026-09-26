import { test, expect } from '@playwright/test'
import { loginAs, apiLogin, apiReportDiag, apiGetDiagnostics } from '../reliability-helpers'

test.describe('D1-D5 诊断测试', () => {
  const BASE_URL = process.env.BASE_URL || 'https://127.0.0.1:5588'

  test('D1 诊断数据上报', async ({ page }) => {
    await loginAs(page)
    const token = await apiLogin(page)
    expect(token.length).toBeGreaterThan(0)
    const reportData = {
      room_id: 'test-e2e-' + Date.now(),
      conn_type: 'ssh',
      host: '10.0.0.1',
      port: 22,
      username: 'root',
      agent_id: 'local-agent',
      agent_name: 'local-agent',
      path_mode: 'direct',
      signal_mode: 'auto',
      detected_type: 'P2P',
      t_start: 1000,
      t_ws_open: 1200,
      t_signal_ok: 1400,
      t_rtc_connected: 1800,
      t_dc_open: 1900,
      t_first_data: 1950,
      duration_ws: 200,
      duration_signal: 200,
      duration_ice: 400,
      duration_dc: 100,
      duration_data: 50,
      duration_total: 950,
      success: 1,
      browser: 'Chrome',
      agent_connect_ms: 150,
    }
    const result = await apiReportDiag(page, token, reportData)
    expect(result.ok).toBeTruthy()
    const records = await apiGetDiagnostics(page, token, { page: 1, page_size: 5 })
    expect(records.total).toBeGreaterThan(0)
    expect(records.items.length).toBeGreaterThan(0)
    await page.screenshot({ path: 'screenshots/D1-report.png' })
  })

  test('D2 分页正确性', async ({ page }) => {
    await loginAs(page)
    const token = await apiLogin(page)
    expect(token.length).toBeGreaterThan(0)
    const page1 = await apiGetDiagnostics(page, token, { page: 1, page_size: 10 })
    expect(page1.items.length).toBeLessThanOrEqual(10)
    expect(page1.page).toBe(1)
    expect(page1.page_size).toBe(10)
    expect(page1.total).toBeGreaterThan(0)
    if (page1.total > 10) {
      const page2 = await apiGetDiagnostics(page, token, { page: 2, page_size: 10 })
      expect(page2.page).toBe(2)
      expect(page2.items.length).toBeGreaterThan(0)
      expect(page2.items[0].id).not.toBe(page1.items[0].id)
    }
    console.log('Pagination: total=' + page1.total + ' p1=' + page1.items.length)
    await page.screenshot({ path: 'screenshots/D2-pagination.png' })
  })

  test('D3 过滤功能', async ({ page }) => {
    await loginAs(page)
    const token = await apiLogin(page)
    expect(token.length).toBeGreaterThan(0)
    const sshOnly = await apiGetDiagnostics(page, token, { conn_type: 'ssh' })
    for (const item of sshOnly.items) {
      expect(item.conn_type).toBe('ssh')
    }
    const vncOnly = await apiGetDiagnostics(page, token, { conn_type: 'vnc' })
    for (const item of vncOnly.items) {
      expect(item.conn_type).toBe('vnc')
    }
    console.log('Filter SSH:', sshOnly.total, 'VNC:', vncOnly.total)
    await page.screenshot({ path: 'screenshots/D3-filter.png' })
  })

  test('D4 聚合统计', async ({ page }) => {
    await loginAs(page)
    const token = await apiLogin(page)
    expect(token.length).toBeGreaterThan(0)
    const resp = await page.request.post(BASE_URL + '/api/timeline/stats', {
      headers: { Authorization: 'Bearer ' + token },
      data: {},
    })
    const data = await resp.json()
    expect(data.total).toBeGreaterThan(0)
    expect(typeof data.avg_total).toBe('number')
    expect(typeof data.success_rate).toBe('number')
    console.log('Stats: total=' + data.total + ' avg=' + data.avg_total.toFixed(0) + 'ms success=' + data.success_rate + '%')
    await page.screenshot({ path: 'screenshots/D4-aggregation.png' })
  })

  test('D5 诊断面板UI', async ({ page }) => {
    await loginAs(page)
    const adminBtn = page.locator('.topmenu:has-text("系统管理")').first()
    if (await adminBtn.isVisible({ timeout: 3000 }).catch(() => false)) {
      await adminBtn.click()
      await page.waitForTimeout(800)
    }
    const diagBtn = page.locator('.sidebar-item, .side-item, [class*=side]', { hasText: /诊断/ }).first()
    if (await diagBtn.isVisible({ timeout: 3000 }).catch(() => false)) {
      await diagBtn.click()
      await page.waitForTimeout(1000)
    }
    await page.screenshot({ path: 'screenshots/D5-panel.png' })
  })
})
