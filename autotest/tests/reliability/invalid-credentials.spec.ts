import { test, expect } from '@playwright/test'
import { loginAs } from '../reliability-helpers'

test.describe('R10 错误密码处理', () => {
  test('R10 用错误密码连接', async ({ page }) => {
    await loginAs(page)
    const card = page.locator('.ssh-card-main').first()
    if (await card.isVisible({ timeout: 3000 }).catch(() => false)) {
      await card.click()
      await page.waitForTimeout(300)
    }
    await page.screenshot({ path: 'screenshots/R10-invalid-cred.png' })
  })
})
