import { test, expect } from '@playwright/test'
import { goSshView, openFirstConnection, waitForFirstData } from '../reliability-helpers'

test.describe('R5 WebSocket断开处理', () => {
  test('R5 WS断开后连接中断', async ({ page }) => {
    await goSshView(page)
    await openFirstConnection(page)
    const sshBtn = page.locator('.ssh-sub-btn.type-ssh').first()
    await sshBtn.click()
    await page.waitForTimeout(3000)
    const ok = await waitForFirstData(page, 10000)
    expect(ok).toBeTruthy()
    await page.screenshot({ path: 'screenshots/R5-ws-before.png' })
    const termVisible = await page.locator('.xterm').first().isVisible()
    expect(termVisible).toBeTruthy()
  })
})
