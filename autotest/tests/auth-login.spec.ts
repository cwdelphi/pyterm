import { test, expect } from '@playwright/test'

test.describe('登录流程', () => {
  test('登录页面加载', async ({ page }) => {
    await page.goto('/')
    await page.waitForTimeout(500)
    await page.screenshot({ path: 'screenshots/login-page-loaded.png', fullPage: true })
    // 页面应包含登录相关的输入框
    const inputs = page.locator('input')
    const count = await inputs.count()
    expect(count).toBeGreaterThanOrEqual(2)
  })

  test('登录成功', async ({ page }) => {
    await page.goto('/')
    await page.waitForTimeout(500)
    
    // 填写已存在的测试用户
    await page.locator('input').nth(0).fill('admin')
    await page.locator('input').nth(1).fill('123456')
    
    await page.locator('button[type="submit"], button:has-text("登录")').first().click()
    await page.waitForTimeout(1500)
    await page.screenshot({ path: 'screenshots/login-success.png', fullPage: true })
  })

  test('密码错误提示', async ({ page }) => {
    await page.goto('/')
    await page.waitForTimeout(500)
    
    await page.locator('input').nth(0).fill('admin')
    await page.locator('input').nth(1).fill('wrongpassword')
    
    await page.locator('button[type="submit"], button:has-text("登录")').first().click()
    await page.waitForTimeout(1000)
    await page.screenshot({ path: 'screenshots/login-wrong-password.png', fullPage: true })
  })

  test('用户名为空提示', async ({ page }) => {
    await page.goto('/')
    await page.waitForTimeout(500)
    
    await page.locator('input').nth(1).fill('somepassword')
    
    await page.locator('button[type="submit"], button:has-text("登录")').first().click()
    await page.waitForTimeout(500)
    await page.screenshot({ path: 'screenshots/login-empty-username.png', fullPage: true })
  })

  test('切换到注册页', async ({ page }) => {
    await page.goto('/')
    await page.waitForTimeout(500)
    
    const switchBtn = page.locator('text=去注册').first()
    if (await switchBtn.isVisible()) {
      await switchBtn.click()
      await page.waitForTimeout(500)
    }
    await page.screenshot({ path: 'screenshots/login-switch-to-register.png', fullPage: true })
  })
})
