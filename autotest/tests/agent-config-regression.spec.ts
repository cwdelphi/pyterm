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
  // Agent 管理无搜索框，翻页查找目标（pageSize=10；库里现有 7 个 Agent）
  let found = false
  for (let i = 0; i < 5 && !found; i++) {
    found = (await page.locator('tr').filter({ hasText: agentId }).count()) > 0
    if (!found) {
      const next = page.locator('.page-btn').filter({ hasText: '下一页' }).first()
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
  test('AC-01: 打开远端 agent 配置无 TypeError，隧道卡片可见', async ({ page }) => {
    const pageErrors: string[] = []
    page.on('pageerror', (e) => pageErrors.push(String(e)))
    page.on('console', (msg) => {
      if (msg.type() === 'error' && !/404|favicon/.test(msg.text())) pageErrors.push(msg.text())
    })

    await openAgentTab(page)
    await openAgentConfig(page, 'node-297b94443a6621e402df35ea')
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
    await openAgentConfig(page, 'node-297b94443a6621e402df35ea')
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

// ═══════════════════════════════════════════════════════════
//  T7: ICE 优化页签（P1，方案 §4.1 配置模型 / §4.2 上下行通道）
// ═══════════════════════════════════════════════════════════

async function switchIceTab(page: import('@playwright/test').Page) {
  await page.locator('.cfg-box .cfg-tab').filter({ hasText: 'ICE优化' }).first().click({ timeout: 15000 })
  await page.waitForTimeout(500)
}

function iceCard(page: import('@playwright/test').Page, title: string) {
  return page.locator('.cfg-box .cfg-card').filter({ hasText: title }).first()
}

async function saveModal(page: import('@playwright/test').Page) {
  await page.locator('.cfg-box button').filter({ hasText: '保存并推送到Agent' }).first().click({ timeout: 15000 })
  await page.waitForTimeout(2000)
}

test.describe('AgentConfig ICE 页签回归 (P1/T7)', () => {
  test('AC-04: ICE 页签渲染完整（开关/模式/接口清单），无 TypeError', async ({ page }) => {
    const pageErrors: string[] = []
    page.on('pageerror', (e) => pageErrors.push(String(e)))
    page.on('console', (msg) => {
      if (msg.type() === 'error' && !/404|favicon/.test(msg.text())) pageErrors.push(msg.text())
    })

    await openAgentTab(page)
    await openAgentConfig(page, 'local-agent')
    await switchIceTab(page)

    await expect(page.locator('.cfg-box .cfg-section-head h3').filter({ hasText: 'ICE 优化规则' }).first())
      .toBeVisible({ timeout: 10000 })
    await expect(iceCard(page, '接口过滤总开关')).toBeVisible()
    await expect(iceCard(page, '过滤模式')).toBeVisible()
    // 模式 4 个 pill（自动/白名单/黑名单/不过滤），仅 ICE 面板可见者
    await expect(page.locator('.cfg-box .cfg-level-pill:visible')).toHaveCount(4)
    // 接口清单：有 net_info → 表格有行；未上报 → 空态，二者必居其一
    // （空态按文案收敛：ICE 页还有「建连效果」空态，二者并存会触发 strict mode violation）
    const rows = page.locator('.cfg-box .cfg-ice-table tbody tr:visible')
    const empty = page.locator('.cfg-box .empty-state-v2:visible').filter({ hasText: '尚无扫描数据' })
    await expect(rows.first().or(empty.first())).toBeVisible({ timeout: 10000 })
    // 扫描按钮
    await expect(page.locator('.cfg-box button').filter({ hasText: '立即扫描' }).first()).toBeVisible()

    const typeErrors = pageErrors.filter((e) => /e is not a function|TypeError/i.test(e))
    expect(typeErrors, `page errors: ${pageErrors.join(' | ')}`).toHaveLength(0)
  })

  test('AC-05: 总开关关闭 → 模式 pill 禁用 + dirty 标记（不保存）', async ({ page }) => {
    const pageErrors: string[] = []
    page.on('pageerror', (e) => pageErrors.push(String(e)))

    await openAgentTab(page)
    await openAgentConfig(page, 'local-agent')
    await switchIceTab(page)

    const card = iceCard(page, '接口过滤总开关')
    await expect(card).toBeVisible({ timeout: 10000 })
    const toggle = card.locator('input[type="checkbox"]').first()
    const slider = card.locator('.cfg-toggle-slider').first()
    await expect(slider).toBeVisible({ timeout: 10000 })
    const original = await toggle.isChecked()
    const pills = page.locator('.cfg-box .cfg-level-pill:visible')
    const dirty = page.locator('.cfg-box .cfg-dirty')

    // 关闭总开关 → 模式 pill 全部 disabled（T9 1 键熔断的 UI 侧）
    if (original) {
      await slider.click()
      await page.waitForTimeout(300)
      await expect(pills.first()).toBeDisabled()
      await expect(dirty).toBeVisible()
      // 还原为原值（等价于放弃修改）
      await slider.click()
      await page.waitForTimeout(300)
      await expect(dirty).toHaveCount(0)
    } else {
      // 原本就是关闭态：pill 应为禁用，且不应产生 dirty
      await expect(pills.first()).toBeDisabled()
      await expect(dirty).toHaveCount(0)
    }

    const typeErrors = pageErrors.filter((e) => /e is not a function|TypeError/i.test(e))
    expect(typeErrors, `page errors: ${pageErrors.join(' | ')}`).toHaveLength(0)
  })

  test('AC-06: ICE 规则保存 → 重开回读 → 写后自动还原', async ({ page }) => {
    await openAgentTab(page)
    await openAgentConfig(page, 'local-agent')
    await switchIceTab(page)

    const card = iceCard(page, '允许 Tailscale')
    await expect(card).toBeVisible({ timeout: 10000 })
    const toggle = card.locator('input[type="checkbox"]').first()
    const slider = card.locator('.cfg-toggle-slider').first()
    await expect(slider).toBeVisible({ timeout: 10000 })
    const original = await toggle.isChecked()

    try {
      await slider.click()
      await expect(page.locator('.cfg-box .cfg-dirty')).toBeVisible({ timeout: 5000 })
      await saveModal(page)
      await expect(page.locator('.cfg-box')).toHaveCount(0, { timeout: 15000 })

      // 重开回读：新值已持久化到 config_json.ice 并推送 Agent
      await openAgentConfig(page, 'local-agent')
      await switchIceTab(page)
      const reopenedCard = iceCard(page, '允许 Tailscale')
      await expect(reopenedCard).toBeVisible({ timeout: 10000 })
      const reopened = reopenedCard.locator('input[type="checkbox"]').first()
      await expect(reopenedCard.locator('.cfg-toggle-slider').first()).toBeVisible({ timeout: 10000 })
      await expect(reopened).toBeChecked({ timeout: 5000 })
      expect(await reopened.isChecked()).toBe(!original)
    } finally {
      // 写后还原：避免用例改动宿主 ICE 规则
      try {
        const c2 = iceCard(page, '允许 Tailscale')
        const t2 = c2.locator('input[type="checkbox"]').first()
        const s2 = c2.locator('.cfg-toggle-slider').first()
        if (await s2.isVisible().catch(() => false)) {
          if ((await t2.isChecked()) !== original) await s2.click()
          await page.waitForTimeout(300)
          if (await page.locator('.cfg-box .cfg-dirty').isVisible().catch(() => false)) {
            await saveModal(page)
          }
        }
      } catch { /* 还原失败不影响用例结论 */ }
    }
  })
})

// ═══════════════════════════════════════════════════════════
//  P2: 路径缓存 / 回退阶梯开关 / 效果看板（方案 §4.5 / §7.2）
// ═══════════════════════════════════════════════════════════

test.describe('AgentConfig ICE 页签 P2 回归', () => {
  test('AC-07: 阶梯/路径缓存/连接复用卡片与清空按钮齐全，无 TypeError', async ({ page }) => {
    const pageErrors: string[] = []
    page.on('pageerror', (e) => pageErrors.push(String(e)))
    page.on('console', (msg) => {
      if (msg.type() === 'error' && !/404|favicon/.test(msg.text())) pageErrors.push(msg.text())
    })

    await openAgentTab(page)
    await openAgentConfig(page, 'local-agent')
    await switchIceTab(page)

    // #4 失败自动回退阶梯（开关可交互）
    const fb = iceCard(page, '失败自动回退阶梯')
    await expect(fb).toBeVisible({ timeout: 10000 })
    await expect(fb.locator('input[type="checkbox"]').first()).toBeEnabled()

    // #5 路径缓存（开关 + 清空按钮）
    const pc = iceCard(page, '路径缓存')
    await expect(pc).toBeVisible()
    await expect(pc.locator('input[type="checkbox"]').first()).toBeEnabled()
    await expect(pc.locator('button').filter({ hasText: '清空路径缓存' }).first()).toBeVisible()

    // #6 连接复用：标「二期」，不给可切换开关（禁假开关）
    const cr = iceCard(page, '连接复用')
    await expect(cr).toBeVisible()
    await expect(cr.locator('.cfg-ice-badge').first()).toBeVisible()
    await expect(cr.locator('input[type="checkbox"]')).toHaveCount(0)

    const typeErrors = pageErrors.filter((e) => /e is not a function|TypeError/i.test(e))
    expect(typeErrors, `page errors: ${pageErrors.join(' | ')}`).toHaveLength(0)
  })

  test('AC-08: 清空路径缓存 → PUT 携带 ice_cache_clear=true 且不产生 dirty', async ({ page }) => {
    await openAgentTab(page)
    await openAgentConfig(page, 'local-agent')
    await switchIceTab(page)

    const pc = iceCard(page, '路径缓存')
    await expect(pc).toBeVisible({ timeout: 10000 })
    const btn = pc.locator('button').filter({ hasText: '清空路径缓存' }).first()
    await expect(btn).toBeEnabled()

    const reqP = page.waitForRequest(
      (r) => r.method() === 'PUT' && /\/api\/admin\/agents\/[^/]+\/config$/.test(r.url()),
      { timeout: 20000 },
    )
    await btn.click()
    const req = await reqP
    const body = req.postDataJSON()
    expect(body?.ice_cache_clear).toBe(true)
    expect(body?.ice).toBeTruthy()

    await page.waitForTimeout(500)
    // 清空只是下发指令，不算配置改动
    await expect(page.locator('.cfg-box .cfg-dirty')).toHaveCount(0)
  })

  test('AC-09: 建连效果看板渲染（有数据显 chip，无数据显空态）', async ({ page }) => {
    const pageErrors: string[] = []
    page.on('pageerror', (e) => pageErrors.push(String(e)))

    await openAgentTab(page)
    await openAgentConfig(page, 'local-agent')
    await switchIceTab(page)

    const head = page.locator('.cfg-box .cfg-section-head h3').filter({ hasText: '建连效果' }).first()
    await expect(head).toBeVisible({ timeout: 10000 })

    const chips = page.locator('.cfg-box .cfg-ice-chip').filter({ hasText: '当前等级' })
    const empty = page.locator('.cfg-box .empty-state-v2').filter({ hasText: '尚无建连统计' })
    await expect(chips.first().or(empty.first())).toBeVisible({ timeout: 10000 })

    if (await chips.first().isVisible().catch(() => false)) {
      await expect(page.locator('.cfg-box .cfg-ice-chip').filter({ hasText: '回退次数' }).first()).toBeVisible()
      await expect(page.locator('.cfg-box .cfg-ice-chip').filter({ hasText: '胜出接口' }).first()).toBeVisible()
    }

    const typeErrors = pageErrors.filter((e) => /e is not a function|TypeError/i.test(e))
    expect(typeErrors, `page errors: ${pageErrors.join(' | ')}`).toHaveLength(0)
  })
})
