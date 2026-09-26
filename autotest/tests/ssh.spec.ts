import { test, expect } from '@playwright/test'
import { loginAsAdmin } from './helpers'

test.describe('SSH管理', () => {
  test('页面加载和连接列表', async ({ page }) => {
    await loginAsAdmin(page)
    await page.click('.topmenu:has-text("SSH管理")')
    await page.waitForTimeout(500)
    await expect(page.locator('.ssh-wrap')).toBeVisible()
    await expect(page.locator('.ssh-sidebar')).toBeVisible()
  })

  test('终端区域铺满右侧', async ({ page }) => {
    await loginAsAdmin(page)
    await page.click('.topmenu:has-text("SSH管理")')
    await page.waitForTimeout(500)

    const mainWidth = await page.evaluate(() => {
      const el = document.querySelector('.ssh-main')
      return el ? Math.round(el.getBoundingClientRect().width) : 0
    })
    expect(mainWidth).toBeGreaterThan(500)

    const wrapWidth = await page.evaluate(() => {
      const el = document.querySelector('.ssh-wrap')
      return el ? Math.round(el.getBoundingClientRect().width) : 0
    })
    expect(wrapWidth).toBeGreaterThan(800)
  })

  test('连接表单弹窗', async ({ page }) => {
    await loginAsAdmin(page)
    await page.click('.topmenu:has-text("SSH管理")')
    await page.waitForTimeout(500)

    const addBtn = page.locator('.ssh-sidebar-head .icon-btn')
    await expect(addBtn).toBeVisible()
    await addBtn.click()
    await page.waitForTimeout(300)
    await expect(page.locator('.modal-box')).toBeVisible()
    await page.locator('.modal-box .tool-btn').filter({ hasText: '取消' }).click()
  })

  test('SSH配置保存（拦截验证）', async ({ page }) => {
    await loginAsAdmin(page)
    await page.click('.topmenu:has-text("SSH管理")')
    await page.waitForTimeout(500)

    let capturedBody: string | null = null
    await page.route('**/api/ssh/add', async (route) => {
      capturedBody = route.request().postData()
      await route.abort()
    })

    await page.locator('.ssh-sidebar-head .icon-btn').click()
    await page.waitForTimeout(300)

    await page.locator('.modal-box input').nth(0).fill('测试机')
    await page.locator('.modal-box input').nth(1).fill('127.0.0.1')
    await page.locator('.modal-box input').nth(3).fill('root')
    await page.locator('.modal-box input').nth(4).fill('secret')

    await page.locator('.modal-box .tool-btn.primary').click()
    await page.waitForTimeout(500)

    expect(capturedBody).toBeTruthy()
    const parsed = JSON.parse(capturedBody!)
    expect(parsed.name).toBe('测试机')
    expect(parsed.host).toBe('127.0.0.1')
    expect(parsed.username).toBe('root')
  })
})
