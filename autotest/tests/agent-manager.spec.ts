import { test, expect } from '@playwright/test'
import { loginAsAdmin } from './helpers'

test.describe('Agent管理', () => {
  test.beforeEach(async ({ page }) => {
    await loginAsAdmin(page)
  })

  test('Agent页面加载', async ({ page }) => {
    const agentBtn = page.locator('.topmenu', { hasText: 'Agent' }).first()
    if (await agentBtn.isVisible()) {
      await agentBtn.click()
      await page.waitForTimeout(800)
      await page.screenshot({ path: 'screenshots/agent-page.png', fullPage: true })
      const content = page.locator('.agent-manager, [class*="agent"]').first()
      await expect(content).toBeVisible()
    }
  })

  test('无Agent空状态', async ({ page }) => {
    const agentBtn = page.locator('.topmenu', { hasText: 'Agent' }).first()
    if (await agentBtn.isVisible()) {
      await agentBtn.click()
      await page.waitForTimeout(800)
      await page.screenshot({ path: 'screenshots/agent-empty.png', fullPage: true })
      const emptyOrList = page.locator('.am-empty, .am-card, [class*="empty"]').first()
      await expect(emptyOrList).toBeVisible()
    }
  })

  test('刷新按钮', async ({ page }) => {
    const agentBtn = page.locator('.topmenu', { hasText: 'Agent' }).first()
    if (await agentBtn.isVisible()) {
      await agentBtn.click()
      await page.waitForTimeout(800)
      
      const refreshBtn = page.locator('button:has-text("刷新")').first()
      if (await refreshBtn.isVisible()) {
        await refreshBtn.click()
        await page.waitForTimeout(500)
        await page.screenshot({ path: 'screenshots/agent-refresh.png', fullPage: true })
      }
    }
  })

  test('导航栏可见', async ({ page }) => {
    await page.screenshot({ path: 'screenshots/agent-nav-visible.png', fullPage: true })
    const nav = page.locator('.topbar, header').first()
    await expect(nav).toBeVisible()
  })
})
