import { test, expect } from '@playwright/test'
import { goSshView, assertTerminalVisible, termTypeAndCheck } from '../helpers'

// webterm 需要 ≥2.2.5 的 agent 二进制(本地 local-agent), 避免点到旧版远端 agent
const AGENT_NAME = process.env.WEBTERM_AGENT || 'local-agent'

function agentCard(page: import('@playwright/test').Page) {
  return page
    .locator('.agent-card:not(.offline)')
    .filter({ has: page.locator('.agent-card-name', { hasText: AGENT_NAME }) })
    .first()
}

test.describe('webterm Agent本地控制台', () => {
  test('Agent分组渲染、默认展开且在线卡可见', async ({ page }) => {
    await goSshView(page)
    await expect(page.locator('.agent-group')).toBeVisible({ timeout: 10000 })
    const arrow = page.locator('.agent-group .group-arrow').first()
    await expect(arrow).toHaveClass(/expanded/, { timeout: 5000 })
    await expect(page.locator('.agent-card').first()).toBeVisible({ timeout: 10000 })
  })

  test('单击在线Agent卡直开webterm并执行命令', async ({ page }) => {
    let auditBody: string | null = null
    await page.route('**/api/webrtc/webterm-open', async route => {
      auditBody = route.request().postData()
      await route.continue()
    })
    await goSshView(page)
    const card = agentCard(page)
    await expect(card).toBeVisible({ timeout: 10000 })
    await card.click()

    // 直开终端页签
    await expect(page.locator('.ssh-tab')).toHaveCount(1, { timeout: 10000 })

    // shell 可交互 (TC-W01): 本地 PTY 回显
    await assertTerminalVisible(page)
    await termTypeAndCheck(page, 'echo webterm_e2e_ok', 'webterm_e2e_ok')

    // 打开留痕 (TC-W06)
    await expect
      .poll(() => (auditBody ? (JSON.parse(auditBody) as { agent_id: string }).agent_id : ''), {
        timeout: 10000,
      })
      .toBeTruthy()

    // 重复点击同一Agent → 聚焦既有页签, 不新开
    await card.click()
    await page.waitForTimeout(500)
    await expect(page.locator('.ssh-tab')).toHaveCount(1)
  })

  test('webterm标签页断开后可重连', async ({ page }) => {
    await goSshView(page)
    const card = agentCard(page)
    await expect(card).toBeVisible({ timeout: 10000 })
    await card.click()
    await expect(page.locator('.ssh-tab')).toHaveCount(1, { timeout: 10000 })
    await assertTerminalVisible(page)
    await termTypeAndCheck(page, 'echo webterm_reconnect_ok', 'webterm_reconnect_ok')

    // 关闭页签 → 重新打开仍是新 shell
    await page.locator('.ssh-tab .tab-close').first().click()
    await page.waitForTimeout(400)
    await expect(page.locator('.ssh-tab')).toHaveCount(0)
    await card.click()
    await expect(page.locator('.ssh-tab')).toHaveCount(1, { timeout: 10000 })
    await assertTerminalVisible(page)
    await termTypeAndCheck(page, 'echo webterm_reopen_ok', 'webterm_reopen_ok')
  })
})
