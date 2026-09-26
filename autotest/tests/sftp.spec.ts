import { test, expect } from '@playwright/test'
import { loginAsAdmin } from './helpers'

test.describe('SFTP服务', () => {
  test('页面加载和配置表单', async ({ page }) => {
    await loginAsAdmin(page)
    await page.click('.topmenu:has-text("SFTP服务")')
    await page.waitForTimeout(500)
    await expect(page.locator('.sftp-wrap')).toBeVisible()
    await expect(page.locator('.sftp-card')).toBeVisible()
    await expect(page.locator('.sftp-header')).toBeVisible()
  })

  test('配置表单字段', async ({ page }) => {
    await loginAsAdmin(page)
    await page.click('.topmenu:has-text("SFTP服务")')
    await page.waitForTimeout(500)

    await expect(page.locator('.cfg-form')).toBeVisible()
    await expect(page.locator('label:has-text("共享目录")')).toBeVisible()
    await expect(page.locator('label:has-text("访问模式")')).toBeVisible()
    await expect(page.locator('label:has-text("用户名")')).toBeVisible()
    await expect(page.locator('label:has-text("端口")')).toBeVisible()
  })

  test('状态区域显示', async ({ page }) => {
    await loginAsAdmin(page)
    await page.click('.topmenu:has-text("SFTP服务")')
    await page.waitForTimeout(500)

    await expect(page.locator('.status-card')).toBeVisible()
    await expect(page.locator('.log-box')).toBeVisible()
  })

  test('启动和停止按钮', async ({ page }) => {
    await loginAsAdmin(page)
    await page.click('.topmenu:has-text("SFTP服务")')
    await page.waitForTimeout(500)

    const startBtn = page.locator('.sftp-btn.primary:has-text("启动")')
    const stopBtn = page.locator('.sftp-btn.danger:has-text("停止")')
    const oneVisible = (await startBtn.isVisible()) || (await stopBtn.isVisible())
    expect(oneVisible).toBeTruthy()
  })

  test('SFTP布局铺满右侧', async ({ page }) => {
    await loginAsAdmin(page)
    await page.click('.topmenu:has-text("SFTP服务")')
    await page.waitForTimeout(500)

    const wrapWidth = await page.evaluate(() => {
      const el = document.querySelector('.sftp-wrap')
      return el ? Math.round(el.getBoundingClientRect().width) : 0
    })
    expect(wrapWidth).toBeGreaterThan(800)
  })
})
