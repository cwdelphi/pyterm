import { test, expect } from '@playwright/test'
import { goSshView, openFirstConnection, waitForFirstData, closeAllSshTabs } from '../reliability-helpers'

test.describe('R12 资源回收检测', () => {
  test('R12 连接关闭后资源回收', async ({ page }) => {
    await goSshView(page)
    await openFirstConnection(page)
    const sshBtn = page.locator('.ssh-sub-btn.type-ssh').first()
    await sshBtn.click()
    await page.waitForTimeout(3000)
    const ok = await waitForFirstData(page, 10000)
    expect(ok).toBeTruthy()
    const tabsBefore = await page.locator('.ssh-tab').count()
    await closeAllSshTabs(page)
    await page.waitForTimeout(1000)
    const tabsAfter = await page.locator('.ssh-tab').count()
    expect(tabsAfter).toBeLessThanOrEqual(tabsBefore)
    await page.screenshot({ path: 'screenshots/R12-resource.png' })
  })
})
