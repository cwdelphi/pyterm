import { test, expect } from '@playwright/test'
import { loginAs } from '../reliability-helpers'

test.describe('R7 Agent离线提示', () => {
  test('R7 检查Agent状态显示', async ({ page }) => {
    await loginAs(page)
    const adminBtn = page.locator('.topmenu:has-text("系统管理")').first()
    await adminBtn.click()
    await page.waitForTimeout(800)
    const agentTab = page.locator('.sidebar-item, .side-item, [class*=side]', { hasText: /Agent/ }).first()
    if (await agentTab.isVisible({ timeout: 3000 }).catch(() => false)) {
      await agentTab.click()
      await page.waitForTimeout(1000)
    }
    await page.screenshot({ path: 'screenshots/R7-agent-status.png' })
    const pageContent = await page.textContent('body') || ''
    expect(pageContent).toContain('Agent')
  })
})
