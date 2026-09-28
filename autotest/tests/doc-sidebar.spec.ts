import { test, expect } from '@playwright/test'
import { loginAsAdmin } from './helpers'

test.describe('文档管理 · 折叠侧栏重设计', () => {
  test.beforeEach(async ({ page }) => {
    await loginAsAdmin(page)
    await page.click('text=文档管理')
    await page.waitForSelector('.dm-sidebar')
  })

  test('expanded: two-row header, collapse button top-right, 260px', async ({ page }) => {
    const sidebar = page.locator('.dm-sidebar')
    await expect(sidebar).not.toHaveClass(/collapsed/)
    expect(Math.round(await sidebar.evaluate(el => el.getBoundingClientRect().width))).toBe(260)

    await expect(page.locator('.dm-sidebar-top .sidebar-label')).toBeVisible()
    await expect(page.locator('.dm-sidebar-top .collapse-btn')).toBeVisible()
    await expect(page.locator('.dm-tree-filter')).toBeVisible()
    expect(await page.locator('.dm-sidebar-head > *').count()).toBe(2)
    expect(await page.locator('.dm-wrap > .breadcrumb').count()).toBe(0)

    await page.screenshot({ path: 'screenshots/dm-sidebar-expanded.png' })
  })

  test('collapse: 48px icon rail with 3 actions; rail search expands and focuses filter', async ({ page }) => {
    const sidebar = page.locator('.dm-sidebar')
    await page.click('.collapse-btn')
    await expect(sidebar).toHaveClass(/collapsed/)
    await expect.poll(async () => Math.round(await sidebar.evaluate(el => el.getBoundingClientRect().width))).toBe(48)
    expect(await sidebar.locator('.dm-rail-btn').count()).toBe(3)
    await expect(page.locator('.dm-tree')).toHaveCount(0)
    await page.screenshot({ path: 'screenshots/dm-sidebar-collapsed.png' })

    await sidebar.locator('.dm-rail-btn').nth(2).click()
    await expect(sidebar).not.toHaveClass(/collapsed/)
    await expect(page.locator('.dm-tree-filter')).toBeFocused()
  })

  test('collapsed state persists across reload', async ({ page }) => {
    await page.click('.collapse-btn')
    await expect(page.locator('.dm-sidebar')).toHaveClass(/collapsed/)

    await page.reload()
    await page.waitForLoadState('domcontentloaded')
    await page.waitForTimeout(600)
    await page.click('text=文档管理')
    await expect(page.locator('.dm-sidebar')).toHaveClass(/collapsed/)

    await page.click('.collapse-btn')
    await expect(page.locator('.dm-sidebar')).not.toHaveClass(/collapsed/)
  })

  test('filter clear button appears and resets filter', async ({ page }) => {
    const filter = page.locator('.dm-tree-filter')
    await filter.fill('首页')
    await expect(page.locator('.dm-filter-clear')).toBeVisible()
    await page.click('.dm-filter-clear')
    await expect(filter).toHaveValue('')
    await expect(page.locator('.dm-filter-clear')).toHaveCount(0)
  })

  test('drag resize: no transition while dragging, clamp 180-420', async ({ page }) => {
    const sidebar = page.locator('.dm-sidebar')
    const handle = page.locator('.dm-sidebar-drag')
    const box = await handle.boundingBox()
    expect(box).toBeTruthy()

    await page.mouse.move(box!.x + 3, box!.y + 120)
    await page.mouse.down()
    const tr = await sidebar.evaluate(el => getComputedStyle(el).transitionDuration)
    expect(tr === '0s' || tr === '0').toBe(true)
    await page.mouse.move(box!.x + 140, box!.y + 120, { steps: 5 })
    await page.mouse.up()
    const w1 = Math.round(await sidebar.evaluate(el => el.getBoundingClientRect().width))
    expect(w1).toBeGreaterThanOrEqual(180)
    expect(w1).toBeLessThanOrEqual(420)
    expect(w1).toBeGreaterThan(300)

    const box2 = await handle.boundingBox()
    await page.mouse.move(box2!.x + 3, box2!.y + 120)
    await page.mouse.down()
    await page.mouse.move(1270, box2!.y + 120, { steps: 5 })
    await page.mouse.up()
    const w2 = Math.round(await sidebar.evaluate(el => el.getBoundingClientRect().width))
    expect(w2).toBeLessThanOrEqual(420)
    expect(w2).toBeGreaterThanOrEqual(180)
  })
})
