import { test, expect } from '@playwright/test'
import { loginAsAdmin } from './helpers'

async function openAgentTab(page: import('@playwright/test').Page) {
  await loginAsAdmin(page)
  await page.waitForTimeout(800)
  await page.locator('.topmenu').filter({ hasText: '系统管理' }).first().click({ timeout: 15000 })
  await page.waitForTimeout(800)
  await page.locator('.menu-item').filter({ hasText: 'Agent管理' }).first().click({ timeout: 15000 })
  await page.waitForTimeout(800)
}

async function openAgentConfig(page: import('@playwright/test').Page, agentId: string) {
  // Agent 管理无搜索框，翻页查找目标（pageSize=10，共 13 个 Agent）
  let found = false
  for (let i = 0; i < 5 && !found; i++) {
    found = (await page.locator('tr').filter({ hasText: agentId }).count()) > 0
    if (!found) {
      const next = page.locator('.page-btn').filter({ hasText: '>' }).first()
      if (await next.count() && (await next.isEnabled())) {
        await next.click()
        await page.waitForTimeout(500)
      } else break
    }
  }
  expect(found, `agent ${agentId} not found in pages`).toBeTruthy()
  const row = page.locator('tr').filter({ hasText: agentId }).first()
  await row.locator('button').filter({ hasText: '配置' }).first().click({ timeout: 15000 })
  await page.waitForTimeout(2000)
}

test.describe('AgentConfig 隧道页回归', () => {
  test('AC-01: 打开 remote-agent 配置无 TypeError，隧道卡片可见', async ({ page }) => {
    const pageErrors: string[] = []
    page.on('pageerror', (e) => pageErrors.push(String(e)))
    page.on('console', (msg) => {
      if (msg.type() === 'error' && !/404|favicon/.test(msg.text())) pageErrors.push(msg.text())
    })

    await openAgentTab(page)
    await openAgentConfig(page, 'remote-agent')

    await expect(page.locator('.tunnel-card').first()).toBeVisible({ timeout: 10000 })
    await expect(page.locator('.tunnel-name-input').first()).toBeVisible()
    const ph = await page.locator('.tunnel-name-input').first().getAttribute('placeholder')
    expect(ph && ph.length > 0).toBeTruthy()

    const typeErrors = pageErrors.filter((e) => /e is not a function|TypeError/i.test(e))
    expect(typeErrors, `page errors: ${pageErrors.join(' | ')}`).toHaveLength(0)
  })

  test('AC-02: local-agent 空隧道状态正常', async ({ page }) => {
    const pageErrors: string[] = []
    page.on('pageerror', (e) => pageErrors.push(String(e)))

    await openAgentTab(page)
    await openAgentConfig(page, 'local-agent')

    await expect(page.locator('.empty-state-v2').first()).toBeVisible({ timeout: 10000 })
    const typeErrors = pageErrors.filter((e) => /e is not a function|TypeError/i.test(e))
    expect(typeErrors).toHaveLength(0)
  })
})
