import { test, expect } from '@playwright/test'
import { loginAsAdmin, goSshView, apiLogin, goAdminView } from '../helpers'

test.describe('阶段0: 基础设施验证', () => {
  test('P0-01: 登录页面加载', async ({ page }) => {
    await page.goto('/')
    await page.waitForLoadState('networkidle')
    await expect(page.locator('input').first()).toBeVisible({ timeout: 10000 })
  })

  test('P0-02: 登录成功', async ({ page }) => {
    await loginAsAdmin(page)
    await expect(page.locator('.topmenu').first()).toBeVisible({ timeout: 10000 })
  })

  test('P0-03: SSH管理页面加载', async ({ page }) => {
    await goSshView(page)
    await expect(page.locator('.ssh-wrap')).toBeVisible({ timeout: 10000 })
  })

  test('P0-04: 管理后台页面加载', async ({ page }) => {
    await goAdminView(page)
    await expect(page.locator('.admin-wrap')).toBeVisible({ timeout: 10000 })
  })

  test('P0-05: API登录获取Token', async ({ page }) => {
    const token = await apiLogin(page)
    expect(token).toBeTruthy()
    expect(token.length).toBeGreaterThan(20)
  })
})
