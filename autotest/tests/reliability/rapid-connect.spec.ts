import { test, expect } from '@playwright/test'
import { goSshView, openFirstConnection, waitForFirstData, closeAllSshTabs } from '../reliability-helpers'

test.describe('R9 快速连接/断开循环', () => {
  test('R9 5次快速connect/disconnect', async ({ page }) => {
    await goSshView(page)
    let crashDetected = false
    page.on('pageerror', () => { crashDetected = true })
    for (let i = 0; i < 5; i++) {
      try {
        await openFirstConnection(page)
        const sshBtn = page.locator('.ssh-sub-btn.type-ssh').first()
        await sshBtn.click()
        await page.waitForTimeout(1500)
        await closeAllSshTabs(page)
        await page.waitForTimeout(500)
      } catch {}
    }
    expect(crashDetected).toBeFalsy()
    const alive = await page.locator('.topmenu').first().isVisible()
    expect(alive).toBeTruthy()
    await page.screenshot({ path: 'screenshots/R9-rapid.png' })
  })
})
