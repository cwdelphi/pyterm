import { test, expect } from '@playwright/test'
import { loginAsAdmin } from './helpers'

test.describe('全站截图基线', () => {
  test('所有页面截图', async ({ page }) => {
    // 1. 登录页截图
    await page.goto('/')
    await page.waitForTimeout(800)
    await page.screenshot({ path: 'screenshots/baseline-01-login.png', fullPage: true })

    // 2. 登录
    await loginAsAdmin(page)
    await page.screenshot({ path: 'screenshots/baseline-02-home.png', fullPage: true })

    // 3. 文档管理
    const docsBtn = page.locator('.topmenu', { hasText: '文档管理' }).first()
    if (await docsBtn.isVisible()) {
      await docsBtn.click()
      await page.waitForTimeout(800)
      await page.screenshot({ path: 'screenshots/baseline-03-docs.png', fullPage: true })

      const fileItem = page.locator('.dm-tree-item').filter({ hasText: '.md' }).first()
      if (await fileItem.isVisible()) {
        await fileItem.click()
        await page.waitForTimeout(800)
        await page.screenshot({ path: 'screenshots/baseline-04-doc-preview.png', fullPage: true })
      }
    }

    // 4. 网址管理
    const linksBtn = page.locator('.topmenu', { hasText: '网址管理' }).first()
    if (await linksBtn.isVisible()) {
      await linksBtn.click()
      await page.waitForTimeout(800)
      await page.screenshot({ path: 'screenshots/baseline-05-links.png', fullPage: true })
    }

    // 5. SSH管理
    const sshBtn = page.locator('.topmenu', { hasText: 'SSH管理' }).first()
    if (await sshBtn.isVisible()) {
      await sshBtn.click()
      await page.waitForTimeout(800)
      await page.screenshot({ path: 'screenshots/baseline-06-ssh.png', fullPage: true })
    }

    // 6. SFTP服务
    const sftpBtn = page.locator('.topmenu', { hasText: 'SFTP' }).first()
    if (await sftpBtn.isVisible()) {
      await sftpBtn.click()
      await page.waitForTimeout(800)
      await page.screenshot({ path: 'screenshots/baseline-07-sftp.png', fullPage: true })
    }

    // 7. 暗色主题
    const themeBtn = page.locator('.icon-btn[aria-label="主题切换"]')
    if (await themeBtn.isVisible()) {
      await themeBtn.click()
      await page.waitForTimeout(500)
      const darkOption = page.locator('.chip:has-text("暗色")')
      if (await darkOption.isVisible()) {
        await darkOption.click()
        await page.waitForTimeout(500)
      }
    }
    await page.screenshot({ path: 'screenshots/baseline-08-dark-mode.png', fullPage: true })
  })
})
