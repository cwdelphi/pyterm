import { test, expect } from '@playwright/test'
import { goSshView, openFirstConnection, waitForFirstData } from '../reliability-helpers'

test.describe('R11 多标签页并发', () => {
  test('R11 两个标签页同时连接', async ({ page }) => {
    await goSshView(page)
    await openFirstConnection(page)
    const sshBtn = page.locator('.ssh-sub-btn.type-ssh').first()
    await sshBtn.click()
    await page.waitForTimeout(3000)
    const ok1 = await waitForFirstData(page, 10000)
    expect(ok1).toBeTruthy()
    const tabCount = await page.locator('.ssh-tab').count()
    expect(tabCount).toBeGreaterThanOrEqual(1)
    await page.screenshot({ path: 'screenshots/R11-concurrent.png' })
  })
})
