import { test, expect } from '@playwright/test'
import { goSshView, openFirstConnection, waitForFirstData, closeAllSshTabs } from '../reliability-helpers'

test.describe('T2 心跳保活', () => {
  test('T2 连接保持30s无断开', async ({ page }) => {
    await goSshView(page)
    await openFirstConnection(page)
    const sshBtn = page.locator('.ssh-sub-btn.type-ssh').first()
    await sshBtn.click()
    await page.waitForTimeout(3000)
    const ok = await waitForFirstData(page, 10000)
    expect(ok).toBeTruthy()
    await page.waitForTimeout(30000)
    const termVisible = await page.locator('.xterm-rows').first().isVisible().catch(() => false)
    expect(termVisible).toBeTruthy()
    const textarea = page.locator('textarea.xterm-helper-textarea').first()
    await textarea.fill('echo HEARTBEAT_OK\n')
    await page.waitForTimeout(1000)
    const text = await page.locator('.xterm-rows').first().textContent() || ''
    expect(text).toContain('HEARTBEAT_OK')
    await page.screenshot({ path: 'screenshots/T2-heartbeat.png' })
    await closeAllSshTabs(page)
  })
})
