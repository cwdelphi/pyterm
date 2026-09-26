import { test, expect } from '@playwright/test'
import { goSshView, openFirstConnection, waitForFirstData, closeAllSshTabs, apiLogin, apiGetDiagnostics } from '../reliability-helpers'

test.describe('L1-L8 延迟测试', () => {
  test('L8 多次连接采集延迟基线', async ({ page }) => {
    await goSshView(page)
    const token = await apiLogin(page)
    const before = await apiGetDiagnostics(page, token, { page: 1, page_size: 1 })
    const beforeCount = before.total || 0
    for (let i = 0; i < 3; i++) {
      await goSshView(page)
      await openFirstConnection(page)
      const sshBtn = page.locator('.ssh-sub-btn.type-ssh').first()
      await sshBtn.click()
      await page.waitForTimeout(3000)
      await waitForFirstData(page, 15000)
      await closeAllSshTabs(page)
      await page.waitForTimeout(1000)
    }
    const after = await apiGetDiagnostics(page, token, { page: 1, page_size: 20 })
    expect(after.total).toBeGreaterThanOrEqual(beforeCount)
    console.log('=== Latency Baseline ===')
    console.log('New records:', after.total - beforeCount)
    for (const r of after.items.slice(0, 3)) {
      console.log('  ' + r.conn_type + ' ' + r.host + ' Total=' + (r.duration_total || 0).toFixed(0) + 'ms WS=' + (r.duration_ws || 0).toFixed(0) + ' ICE=' + (r.duration_ice || 0).toFixed(0))
    }
    await page.screenshot({ path: 'screenshots/L8-baseline.png' })
  })

  test('L6 端到端总延迟', async ({ page }) => {
    await goSshView(page)
    const token = await apiLogin(page)
    await openFirstConnection(page)
    const sshBtn = page.locator('.ssh-sub-btn.type-ssh').first()
    await sshBtn.click()
    await page.waitForTimeout(3000)
    const ok = await waitForFirstData(page, 15000)
    expect(ok).toBeTruthy()
    await page.waitForTimeout(1000)
    const recs = await apiGetDiagnostics(page, token, { page: 1, page_size: 1 })
    const latest = recs.items[0]
    console.log('Total connect latency:', latest.duration_total?.toFixed(0) + 'ms')
    expect(latest.duration_total).toBeLessThan(10000)
    await page.screenshot({ path: 'screenshots/L6-total.png' })
    await closeAllSshTabs(page)
  })

  test('L1 WS握手延迟', async ({ page }) => {
    await goSshView(page)
    const token = await apiLogin(page)
    await openFirstConnection(page)
    const sshBtn = page.locator('.ssh-sub-btn.type-ssh').first()
    await sshBtn.click()
    await page.waitForTimeout(3000)
    await waitForFirstData(page, 15000)
    await page.waitForTimeout(1000)
    const recs = await apiGetDiagnostics(page, token, { page: 1, page_size: 1 })
    const latest = recs.items[0]
    console.log('WS handshake:', latest.duration_ws?.toFixed(0) + 'ms')
    expect(latest.duration_ws).toBeLessThan(5000)
    await closeAllSshTabs(page)
  })

  test('L2 信令协商延迟', async ({ page }) => {
    await goSshView(page)
    const token = await apiLogin(page)
    await openFirstConnection(page)
    const sshBtn = page.locator('.ssh-sub-btn.type-ssh').first()
    await sshBtn.click()
    await page.waitForTimeout(3000)
    await waitForFirstData(page, 15000)
    await page.waitForTimeout(1000)
    const recs = await apiGetDiagnostics(page, token, { page: 1, page_size: 1 })
    const latest = recs.items[0]
    console.log('Signal latency:', latest.duration_signal?.toFixed(0) + 'ms')
    await closeAllSshTabs(page)
  })

  test('L3 ICE+DTLS延迟', async ({ page }) => {
    await goSshView(page)
    const token = await apiLogin(page)
    await openFirstConnection(page)
    const sshBtn = page.locator('.ssh-sub-btn.type-ssh').first()
    await sshBtn.click()
    await page.waitForTimeout(3000)
    await waitForFirstData(page, 15000)
    await page.waitForTimeout(1000)
    const recs = await apiGetDiagnostics(page, token, { page: 1, page_size: 1 })
    const latest = recs.items[0]
    console.log('ICE latency:', latest.duration_ice?.toFixed(0) + 'ms')
    expect(latest.duration_ice).toBeLessThan(5000)
    await closeAllSshTabs(page)
  })

  test('L4 DataChannel延迟', async ({ page }) => {
    await goSshView(page)
    const token = await apiLogin(page)
    await openFirstConnection(page)
    const sshBtn = page.locator('.ssh-sub-btn.type-ssh').first()
    await sshBtn.click()
    await page.waitForTimeout(3000)
    await waitForFirstData(page, 15000)
    await page.waitForTimeout(1000)
    const recs = await apiGetDiagnostics(page, token, { page: 1, page_size: 1 })
    const latest = recs.items[0]
    console.log('DC latency:', latest.duration_dc?.toFixed(0) + 'ms')
    expect(latest.duration_dc).toBeLessThan(2000)
    await closeAllSshTabs(page)
  })

  test('L5 首数据延迟', async ({ page }) => {
    await goSshView(page)
    const token = await apiLogin(page)
    await openFirstConnection(page)
    const sshBtn = page.locator('.ssh-sub-btn.type-ssh').first()
    await sshBtn.click()
    await page.waitForTimeout(3000)
    await waitForFirstData(page, 15000)
    await page.waitForTimeout(1000)
    const recs = await apiGetDiagnostics(page, token, { page: 1, page_size: 1 })
    const latest = recs.items[0]
    console.log('First data:', latest.duration_data?.toFixed(0) + 'ms')
    await closeAllSshTabs(page)
  })
})
