import { test, expect } from '@playwright/test'
import { loginAsAdmin } from './helpers'

test.describe('主题切换', () => {
  test('主题按钮和弹层', async ({ page }) => {
    await loginAsAdmin(page)
    const themeBtn = page.locator('.icon-btn[aria-label="主题切换"]')
    await expect(themeBtn).toBeVisible()
    await themeBtn.click()
    await page.waitForTimeout(300)

    await expect(page.locator('.theme-pop')).toBeVisible()
    await expect(page.locator('.theme-title').first()).toBeVisible()
  })

  test('亮暗模式切换', async ({ page }) => {
    await loginAsAdmin(page)
    const themeBtn = page.locator('.icon-btn[aria-label="主题切换"]')
    await themeBtn.click()
    await page.waitForTimeout(300)

    const darkChip = page.locator('.chip:has-text("暗色")')
    if (await darkChip.isVisible()) {
      await darkChip.click()
      await page.waitForTimeout(300)
      const theme = await page.evaluate(() => document.documentElement.dataset.theme)
      expect(theme).toBe('dark')
    }

    const lightChip = page.locator('.chip:has-text("亮色")')
    if (await lightChip.isVisible()) {
      await lightChip.click()
      await page.waitForTimeout(300)
      const theme = await page.evaluate(() => document.documentElement.dataset.theme)
      expect(theme).toBe('light')
    }
  })

  test('主题色切换', async ({ page }) => {
    await loginAsAdmin(page)
    const themeBtn = page.locator('.icon-btn[aria-label="主题切换"]')
    await themeBtn.click()
    await page.waitForTimeout(300)

    const greenSwatch = page.locator('.swatch[data-a="green"]')
    if (await greenSwatch.isVisible()) {
      await greenSwatch.click()
      await page.waitForTimeout(300)
      const accent = await page.evaluate(() => document.documentElement.dataset.accent)
      expect(accent).toBe('green')
    }
  })

  test('四个功能入口可见', async ({ page }) => {
    await loginAsAdmin(page)
    await expect(page.locator('.topmenu:has-text("文档管理")')).toBeVisible()
    await expect(page.locator('.topmenu:has-text("网址管理")')).toBeVisible()
    await expect(page.locator('.topmenu:has-text("SSH管理")')).toBeVisible()
    await expect(page.locator('.topmenu:has-text("SFTP服务")')).toBeVisible()
  })
})
