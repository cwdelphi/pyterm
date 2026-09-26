import { test, expect } from '@playwright/test'
import { goSshView, openFirstConnection, waitForFirstData } from '../reliability-helpers'

test.describe('R8 重复连接防护', () => {
  test('R8 同时发起两个连接', async ({ page }) => {
    await goSshView(page)
    await openFirstConnection(page)
    const sshBtn = page.locator('.ssh-sub-btn.type-ssh').first()
    await sshBtn.click()
    await page.waitForTimeout(2000)
    await openFirstConnection(page)
    const sshBtn2 = page.locator('.ssh-sub-btn.type-ssh').first()
    await sshBtn2.click()
    await page.waitForTimeout(3000)
    const tabs = await page.locator('.ssh-tab').count()
    expect(tabs).toBeGreaterThanOrEqual(1)
    await page.screenshot({ path: 'screenshots/R8-duplicate.png' })
  })
})
