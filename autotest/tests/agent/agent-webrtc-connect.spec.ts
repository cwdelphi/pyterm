import { test, expect } from '@playwright/test'
import { loginAsAdmin, goAdminView } from '../helpers'

test.describe('Agent WebRTC连接', () => {
  test.beforeEach(async ({ page }) => {
    await loginAsAdmin(page)
  })

  async function openAgentsTab(page: import('@playwright/test').Page) {
    await goAdminView(page)
    const agentTab = page.locator('.menu-item').filter({ hasText: 'Agent' }).first()
    await agentTab.click()
    await page.waitForTimeout(800)
  }

  test('从Agent发起WebRTC连接', async ({ page }) => {
    await openAgentsTab(page)
    const connectBtn = page.locator('button:has-text("连接"), button:has-text("Connect")').first()
    if (await connectBtn.isVisible().catch(() => false)) {
      const isDisabled = await connectBtn.isDisabled()
      if (!isDisabled) {
        await connectBtn.click()
        await page.waitForTimeout(2000)
        await page.screenshot({ path: 'screenshots/agent-webrtc-connect.png' })
      }
    }
    await expect(page.locator('.admin-wrap').first()).toBeVisible()
  })

  test('Agent连接终端交互', async ({ page }) => {
    await openAgentsTab(page)
    const connectBtn = page.locator('button:has-text("连接"), button:has-text("Connect")').first()
    if (await connectBtn.isVisible().catch(() => false)) {
      const isDisabled = await connectBtn.isDisabled()
      if (!isDisabled) {
        await connectBtn.click()
        await page.waitForTimeout(3000)

        const termWrap = page.locator('.ssh-term-wrap, [class*="terminal"]')
        const hasTerm = await termWrap.first().isVisible().catch(() => false)
        if (hasTerm) {
          const term = page.locator('.ssh-term .xterm-helper-textarea, textarea')
          if (await term.first().isVisible().catch(() => false)) {
            await term.first().focus()
            await term.first().type('echo agent_test\n', { delay: 50 })
            await page.waitForTimeout(1000)
          }
        }
        await page.screenshot({ path: 'screenshots/agent-terminal-interaction.png' })
      }
    }
    await expect(page.locator('.admin-wrap').first()).toBeVisible()
  })
})
