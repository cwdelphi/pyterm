import { test, expect } from '@playwright/test'
import { loginAsAdmin, goAdminView } from '../helpers'

test.describe('Agent管理', () => {
  test.beforeEach(async ({ page }) => {
    await loginAsAdmin(page)
  })

  async function openAgentsTab(page: import('@playwright/test').Page) {
    await goAdminView(page)
    const agentTab = page.locator('.menu-item').filter({ hasText: 'Agent' }).first()
    await agentTab.click()
    await page.waitForTimeout(800)
  }

  test('Agent页面加载', async ({ page }) => {
    await openAgentsTab(page)
    await page.screenshot({ path: 'screenshots/agent-page-load.png' })
    const content = page.locator('.admin-wrap, [class*="agent"]').first()
    await expect(content).toBeVisible()
  })

  test('空状态显示', async ({ page }) => {
    await openAgentsTab(page)
    await page.screenshot({ path: 'screenshots/agent-empty-state.png' })
    const emptyOrList = page.locator('.am-empty, .am-card, [class*="empty"], table, .admin-table').first()
    await expect(emptyOrList).toBeVisible()
  })

  test('刷新按钮', async ({ page }) => {
    await openAgentsTab(page)
    const refreshBtn = page.locator('button:has-text("刷新")').first()
    if (await refreshBtn.isVisible().catch(() => false)) {
      await refreshBtn.click()
      await page.waitForTimeout(500)
      await page.screenshot({ path: 'screenshots/agent-refresh.png' })
    }
    await expect(page.locator('.admin-wrap').first()).toBeVisible()
  })
})
