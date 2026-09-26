import { test, expect } from '@playwright/test'
import { goSshView, openFirstConnection, waitForFirstData, closeAllSshTabs } from '../reliability-helpers'

test.describe('T7 信令重试', () => {
  test('T7 信令连接后正常工作', async ({ page }) => {
    await goSshView(page)
    await openFirstConnection(page)
    const sshBtn = page.locator('.ssh-sub-btn.type-ssh').first()
    await sshBtn.click()
    await page.waitForTimeout(2000)
    const ok = await waitForFirstData(page, 15000)
    expect(ok).toBeTruthy()
    await page.screenshot({ path: 'screenshots/T7-signal-retry.png' })
    await closeAllSshTabs(page)
  })
})
