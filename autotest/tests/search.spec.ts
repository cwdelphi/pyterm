import { test, expect } from '@playwright/test'
import { loginAsAdmin } from './helpers'

test.describe('Global Search', () => {
  test('opens with Ctrl+K', async ({ page }) => {
    await loginAsAdmin(page)

    await page.keyboard.press('Control+k')
    const searchPanel = page.locator('.gs-panel')
    await expect(searchPanel).toBeVisible()

    await page.keyboard.press('Escape')
    await expect(searchPanel).not.toBeVisible()
  })

  test('searches documents', async ({ page }) => {
    await loginAsAdmin(page)
    await page.keyboard.press('Control+k')

    const input = page.locator('.gs-input')
    await input.fill('首页')

    await page.waitForTimeout(500)

    const results = page.locator('.gs-result')
    const count = await results.count()
    expect(count).toBeGreaterThan(0)
  })

  test('click search button opens search', async ({ page }) => {
    await loginAsAdmin(page)

    await page.click('.search-btn')
    const searchPanel = page.locator('.gs-panel')
    await expect(searchPanel).toBeVisible()
  })
})
