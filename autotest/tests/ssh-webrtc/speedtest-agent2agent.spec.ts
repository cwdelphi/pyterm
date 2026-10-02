import { test, expect } from '@playwright/test'
import { goSshView, expandAgentGroup } from '../helpers'

// TC-ST01/ST02: Agent↔Agent P2P 测速(独立 speedtest DC)
const SRC = process.env.SPEEDTEST_SRC || 'local-agent'
const DST = process.env.SPEEDTEST_DST || 'local-agent-2'

function srcCard(page: import('@playwright/test').Page) {
  // 精确匹配: SRC='local-agent' 是 'local-agent-2' 的子串, hasText 子串匹配会命中错误卡片
  return page
    .locator('.agent-card:not(.offline)')
    .filter({ has: page.locator('.agent-card-name', { hasText: new RegExp(`^${SRC}$`) }) })
    .first()
}

// 目标选择器: 测速 UX 重做后不再是 <select>, 而是一排 .st-target 按钮
function targetBtn(page: import('@playwright/test').Page, box: import('@playwright/test').Locator, name: string) {
  return box
    .locator('.st-target')
    .filter({ has: page.locator('.st-target-name', { hasText: new RegExp(`^${name}$`) }) })
    .first()
}

async function openSpeedtestModal(page: import('@playwright/test').Page) {
  await goSshView(page)
  await expandAgentGroup(page)
  const card = srcCard(page)
  await expect(card).toBeVisible({ timeout: 10000 })
  await card.locator('.st-btn').click()
  const box = page.locator('.st-box')
  await expect(box).toBeVisible({ timeout: 5000 })
  return box
}

test.describe('TC-ST01/ST02 Agent↔Agent 测速', () => {
  test('ST01: 全流程跑完(上行+下行+结果+历史)', async ({ page }) => {
    test.setTimeout(120000)
    const box = await openSpeedtestModal(page)

    // 目标选择 + 开始
    const dstBtn = targetBtn(page, box, DST)
    await expect(dstBtn).toBeVisible({ timeout: 5000 })
    await dstBtn.click()
    await expect(dstBtn).toHaveClass(/active/)
    await box.locator('.st-go').click()
    await expect(box.locator('.st-stage.running')).toBeVisible({ timeout: 15000 })

    // 10s 上行 + 10s 下行 + 握手开销
    await expect(box.locator('.st-stage.done')).toBeVisible({ timeout: 70000 })

    const vals = box.locator('.st-res-card .st-res-val')
    const up = parseFloat(await vals.first().innerText())
    const down = parseFloat(await vals.nth(1).innerText())
    expect(up, '上行速率 > 0').toBeGreaterThan(0)
    expect(down, '下行速率 > 0').toBeGreaterThan(0)

    // 历史列表出现新记录
    await expect(box.locator('.st-hist-card').first()).toBeVisible({ timeout: 10000 })

    // 关闭弹层(点遮罩角)
    await page.locator('.modal-mask').last().click({ position: { x: 4, y: 4 } })
    await expect(box).toHaveCount(0, { timeout: 5000 })
  })

  test('ST02: 中止测速 → 回到待命, 可重新发起', async ({ page }) => {
    test.setTimeout(90000)
    const box = await openSpeedtestModal(page)

    const dstBtn = targetBtn(page, box, DST)
    await expect(dstBtn).toBeVisible({ timeout: 5000 })
    await dstBtn.click()
    await expect(dstBtn).toHaveClass(/active/)
    await box.locator('.st-go').click()
    await expect(box.locator('.st-stage.running')).toBeVisible({ timeout: 15000 })

    // 中止 → 待命态
    await box.locator('.st-go').click()
    await expect(box.locator('.st-idle')).toBeVisible({ timeout: 10000 })

    // 锁已释放: 可再次发起(running 再现), 随后再次中止收尾
    await box.locator('.st-go').click()
    await expect(box.locator('.st-stage.running')).toBeVisible({ timeout: 15000 })
    await box.locator('.st-go').click()
    await expect(box.locator('.st-idle')).toBeVisible({ timeout: 10000 })

    await page.locator('.modal-mask').last().click({ position: { x: 4, y: 4 } })
    await expect(box).toHaveCount(0, { timeout: 5000 })
  })

  test('ST-UX: ✕ 按钮关闭 + ESC 关闭弹层', async ({ page }) => {
    test.setTimeout(60000)
    // ✕ 按钮
    let box = await openSpeedtestModal(page)
    await box.locator('.st-close').click()
    await expect(box).toHaveCount(0, { timeout: 5000 })

    // ESC 键
    box = await openSpeedtestModal(page)
    await page.keyboard.press('Escape')
    await expect(box).toHaveCount(0, { timeout: 5000 })
  })
})
