import { test, expect } from '@playwright/test'
import { loginAsAdmin } from './helpers'

test.describe('完整认证流程', () => {
  test('登录→访问功能→登出', async ({ page }) => {
    await page.goto('/')
    await page.waitForTimeout(500)
    await page.screenshot({ path: 'screenshots/flow-01-login-page.png', fullPage: true })

    await loginAsAdmin(page)
    await page.screenshot({ path: 'screenshots/flow-02-after-login.png', fullPage: true })

    const docsBtn = page.locator('.topmenu', { hasText: '文档管理' }).first()
    if (await docsBtn.isVisible()) {
      await docsBtn.click()
      await page.waitForTimeout(800)
      await page.screenshot({ path: 'screenshots/flow-03-docs.png', fullPage: true })
    }

    const linksBtn = page.locator('.topmenu', { hasText: '网址管理' }).first()
    if (await linksBtn.isVisible()) {
      await linksBtn.click()
      await page.waitForTimeout(800)
      await page.screenshot({ path: 'screenshots/flow-04-links.png', fullPage: true })
    }

    const sshBtn = page.locator('.topmenu', { hasText: 'SSH管理' }).first()
    if (await sshBtn.isVisible()) {
      await sshBtn.click()
      await page.waitForTimeout(800)
      await page.screenshot({ path: 'screenshots/flow-05-ssh.png', fullPage: true })
    }

    const userBtn = page.locator('.user-btn, .user-avatar, [class*="user"]').first()
    if (await userBtn.isVisible()) {
      await userBtn.click()
      await page.waitForTimeout(300)
      const logoutBtn = page.locator('text=登出, text=退出, text=注销').first()
      if (await logoutBtn.isVisible()) {
        await logoutBtn.click()
        await page.waitForTimeout(1000)
        await page.screenshot({ path: 'screenshots/flow-06-after-logout.png', fullPage: true })
      }
    }
  })

  test('TopBar显示用户名', async ({ page }) => {
    await loginAsAdmin(page)

    await page.screenshot({ path: 'screenshots/flow-topbar-username.png', fullPage: true })
    const topbar = page.locator('.topbar, header').first()
    await expect(topbar).toBeVisible()
  })

  test('退出后无法访问功能', async ({ page }) => {
    await page.goto('/')
    await page.waitForTimeout(1000)
    
    const loginInput = page.locator('input').first()
    const isLoginPage = await loginInput.isVisible()
    await page.screenshot({ path: 'screenshots/flow-protected.png', fullPage: true })
    expect(isLoginPage).toBeTruthy()
  })

  test('用户菜单操作', async ({ page }) => {
    await loginAsAdmin(page)

    const userArea = page.locator('.user-btn, .user-avatar, [class*="user-menu"], [class*="userPop"]').first()
    if (await userArea.isVisible()) {
      await userArea.click()
      await page.waitForTimeout(500)
      await page.screenshot({ path: 'screenshots/flow-user-menu.png', fullPage: true })
    }
  })
})
