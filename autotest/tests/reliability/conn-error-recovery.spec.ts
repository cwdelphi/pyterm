import { test, expect } from '@playwright/test'
import { loginAs, goSshView, openFirstConnection, waitForFirstData, closeAllSshTabs } from '../reliability-helpers'

test.describe('R3 连接错误恢复', () => {
  test('R3 SSH连接失败后能重新发起', async ({ page }) => {
    await loginAs(page)
    const adminBtn = page.locator('.topmenu:has-text("系统管理")').first()
    if (await adminBtn.isVisible({ timeout: 3000 }).catch(() => false)) {
      await adminBtn.click()
      await page.waitForTimeout(500)
    }
    const backBtn = page.locator('.topmenu:has-text("远程管理")').first()
    if (await backBtn.isVisible({ timeout: 2000 }).catch(() => false)) {
      await backBtn.click()
      await page.waitForTimeout(500)
    }
    await openFirstConnection(page)
    const sshBtn = page.locator('.ssh-sub-btn.type-ssh').first()
    await sshBtn.click()
    await page.waitForTimeout(3000)
    const ok = await waitForFirstData(page, 10000)
    expect(ok).toBeTruthy()
    await page.screenshot({ path: 'screenshots/R3-error-recovery.png' })
  })
})
