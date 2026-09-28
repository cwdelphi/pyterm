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

async function switchTunnelTab(page: import('@playwright/test').Page) {
  await page.locator('.cfg-box .cfg-tab').filter({ hasText: '隧道管理' }).first().click({ timeout: 15000 })
  await page.waitForTimeout(500)
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
    await switchTunnelTab(page)

    await expect(page.locator('.cfg-box .cfg-tunnel-card').first()).toBeVisible({ timeout: 10000 })
    await expect(page.locator('.cfg-box .cfg-tunnel-name').first()).toBeVisible()
    const ph = await page.locator('.cfg-box .cfg-tunnel-name').first().getAttribute('placeholder')
    expect(ph && ph.length > 0).toBeTruthy()

    const typeErrors = pageErrors.filter((e) => /e is not a function|TypeError/i.test(e))
    expect(typeErrors, `page errors: ${pageErrors.join(' | ')}`).toHaveLength(0)
  })

  test('AC-03: 输入目标端口后弹窗不消失、无 TypeError', async ({ page }) => {
    const pageErrors: string[] = []
    page.on('pageerror', (e) => pageErrors.push(String(e)))
    page.on('console', (msg) => {
      if (msg.type() === 'error' && !/404|favicon/.test(msg.text())) pageErrors.push(msg.text())
    })

    await openAgentTab(page)
    await openAgentConfig(page, 'remote-agent')
    await switchTunnelTab(page)

    // 回归：target_port 由 v-model.number 变成 number 后，joinTargetAddr 调 .trim() 抛错导致弹窗被卸载
    const targetPort = page.locator('.cfg-box .cfg-tunnel-card').first().locator('input[type="number"]').nth(1)
    await expect(targetPort).toBeVisible({ timeout: 10000 })
    await targetPort.click()
    await page.keyboard.type('9090')
    await page.waitForTimeout(800)

    await expect(page.locator('.cfg-box')).toHaveCount(1)
    await expect(page.locator('.cfg-mask')).toHaveCount(1)
    await expect(targetPort).toHaveValue(/9090$/)
    await expect(page.locator('.cfg-dirty')).toBeVisible()

    const typeErrors = pageErrors.filter((e) => /e is not a function|TypeError/i.test(e))
    expect(typeErrors, `page errors: ${pageErrors.join(' | ')}`).toHaveLength(0)
  })

  test('AC-02: local-agent 隧道面板正常渲染（容忍已有隧道）', async ({ page }) => {
    const pageErrors: string[] = []
    page.on('pageerror', (e) => pageErrors.push(String(e)))

    await openAgentTab(page)
    await openAgentConfig(page, 'local-agent')
    await switchTunnelTab(page)

    // 容忍已有隧道：环境可能预置 test-tunnel 等数据，空态与隧道卡片二选一可见即可
    const empty = page.locator('.cfg-box .empty-state-v2').first()
    const card = page.locator('.cfg-box .cfg-tunnel-card').first()
    await expect(empty.or(card)).toBeVisible({ timeout: 10000 })
    expect((await empty.count()) + (await card.count()), 'empty-state 与 tunnel-card 应至少存在一个').toBeGreaterThan(0)

    const typeErrors = pageErrors.filter((e) => /e is not a function|TypeError/i.test(e))
    expect(typeErrors, `page errors: ${pageErrors.join(' | ')}`).toHaveLength(0)
  })
})
