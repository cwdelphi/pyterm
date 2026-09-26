import { test, expect } from '@playwright/test'

const TEST_USER = `reg_test_${Date.now()}`

test.describe('注册流程', () => {
  test('注册页面加载', async ({ page }) => {
    await page.goto('/')
    await page.waitForTimeout(500)
    // 应显示登录或注册页面
    const hasLogin = await page.locator('text=登录').first().isVisible()
    const hasRegister = await page.locator('text=注册').first().isVisible()
    expect(hasLogin || hasRegister).toBeTruthy()
    await page.screenshot({ path: 'screenshots/register-page-loaded.png', fullPage: true })
  })

  test('切换到注册页', async ({ page }) => {
    await page.goto('/')
    await page.waitForTimeout(500)
    const switchBtn = page.locator('text=去注册').first()
    if (await switchBtn.isVisible()) {
      await switchBtn.click()
      await page.waitForTimeout(500)
    }
    // 注册页至少有用户名+密码输入框
    const inputs = page.locator('input')
    const count = await inputs.count()
    expect(count).toBeGreaterThanOrEqual(2)
    await page.screenshot({ path: 'screenshots/register-form.png', fullPage: true })
  })

  test('注册成功', async ({ page }) => {
    await page.goto('/')
    await page.waitForTimeout(500)
    const switchBtn = page.locator('text=去注册').first()
    if (await switchBtn.isVisible()) {
      await switchBtn.click()
      await page.waitForTimeout(500)
    }
    
    await page.locator('input').nth(0).fill(TEST_USER)
    await page.locator('input').nth(1).fill('TestPass123!')
    const emailInput = page.locator('input[placeholder*="邮箱"], input[type="email"]').first()
    if (await emailInput.isVisible()) {
      await emailInput.fill(`${TEST_USER}@test.com`)
    }
    
    await page.locator('button[type="submit"], button:has-text("注册")').first().click()
    await page.waitForTimeout(1000)
    await page.screenshot({ path: 'screenshots/register-success.png', fullPage: true })
  })

  test('重复用户名提示', async ({ page }) => {
    await page.goto('/')
    await page.waitForTimeout(500)
    const switchBtn = page.locator('text=去注册').first()
    if (await switchBtn.isVisible()) {
      await switchBtn.click()
      await page.waitForTimeout(500)
    }
    
    await page.locator('input').nth(0).fill('admin')
    await page.locator('input').nth(1).fill('TestPass123!')
    const emailInput = page.locator('input[placeholder*="邮箱"], input[type="email"]').first()
    if (await emailInput.isVisible()) {
      await emailInput.fill('dup@test.com')
    }
    
    await page.locator('button[type="submit"], button:has-text("注册")').first().click()
    await page.waitForTimeout(1000)
    await page.screenshot({ path: 'screenshots/register-duplicate.png', fullPage: true })
  })

  test('密码过短提示', async ({ page }) => {
    await page.goto('/')
    await page.waitForTimeout(500)
    const switchBtn = page.locator('text=去注册').first()
    if (await switchBtn.isVisible()) {
      await switchBtn.click()
      await page.waitForTimeout(500)
    }
    
    await page.locator('input').nth(0).fill('short_pass_user')
    await page.locator('input').nth(1).fill('12345')
    const emailInput = page.locator('input[placeholder*="邮箱"], input[type="email"]').first()
    if (await emailInput.isVisible()) {
      await emailInput.fill('short@test.com')
    }
    
    await page.locator('button[type="submit"], button:has-text("注册")').first().click()
    await page.waitForTimeout(500)
    await page.screenshot({ path: 'screenshots/register-short-password.png', fullPage: true })
  })
})
