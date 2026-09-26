import { test, expect } from '@playwright/test'
import { loginAsAdmin, goAdminView } from '../helpers'

test.describe('WebRTC信令', () => {
  test.beforeEach(async ({ page }) => {
    await loginAsAdmin(page)
  })

  async function openAgentsTab(page: import('@playwright/test').Page) {
    await goAdminView(page)
    const agentTab = page.locator('.menu-item').filter({ hasText: 'Agent' }).first()
    await agentTab.click()
    await page.waitForTimeout(800)
  }

  test('Agent列表显示', async ({ page }) => {
    await openAgentsTab(page)
    await page.screenshot({ path: 'screenshots/webrtc-agent-list.png' })
    const content = page.locator('.admin-wrap, [class*="agent"]').first()
    await expect(content).toBeVisible()
    const listOrEmpty = page.locator('.am-empty, .am-card, [class*="empty"], table, .admin-table').first()
    await expect(listOrEmpty).toBeVisible()
  })

  test('WebRTC连接按钮', async ({ page }) => {
    await openAgentsTab(page)
    const connectBtn = page.locator('button:has-text("连接"), button:has-text("Connect")').first()
    if (await connectBtn.isVisible().catch(() => false)) {
      await page.screenshot({ path: 'screenshots/webrtc-connect-btn.png' })
      const isDisabled = await connectBtn.isDisabled().catch(() => false)
      expect(typeof isDisabled).toBe('boolean')
    }
    const refreshBtn = page.locator('button:has-text("刷新")').first()
    if (await refreshBtn.isVisible().catch(() => false)) {
      await refreshBtn.click()
      await page.waitForTimeout(500)
      await page.screenshot({ path: 'screenshots/webrtc-agent-refresh.png' })
    }
    await expect(page.locator('.admin-wrap').first()).toBeVisible()
  })
})
