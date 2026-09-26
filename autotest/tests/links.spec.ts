import { test, expect } from '@playwright/test'
import { loginAsAdmin } from './helpers'

test.describe('网址管理', () => {
  test('页面加载和分组列表', async ({ page }) => {
    await loginAsAdmin(page)
    await page.click('.topmenu:has-text("网址管理")')
    await page.waitForTimeout(500)
    await expect(page.locator('.lm-wrap')).toBeVisible()
    await expect(page.locator('.lm-sidebar')).toBeVisible()
  })

  test('右侧内容区域', async ({ page }) => {
    await loginAsAdmin(page)
    await page.click('.topmenu:has-text("网址管理")')
    await page.waitForTimeout(500)
    await expect(page.locator('.lm-main')).toBeVisible()
  })

  test('新建分组', async ({ page }) => {
    await loginAsAdmin(page)
    await page.click('.topmenu:has-text("网址管理")')
    await page.waitForTimeout(500)

    const addGroupBtn = page.locator('.lm-sidebar-head .icon-btn')
    if (await addGroupBtn.isVisible()) {
      await addGroupBtn.click()
      await page.waitForTimeout(300)
      const nameInput = page.locator('.modal-box input').first()
      if (await nameInput.isVisible()) {
        await nameInput.fill('测试分组')
        await page.click('.modal-box .tool-btn.primary')
        await page.waitForTimeout(500)
      }
    }
  })
})
