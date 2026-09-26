import { test, expect } from '@playwright/test'
import { loginAsAdmin, apiAddConnection, apiDeleteByPrefix, apiWaitForConnection, goSshView, apiLogin, openFileBrowser } from '../helpers'

const PREFIX = 'sftp_ops_'
const SSH_HOST = process.env.SSH_HOST || '127.0.0.1'
const SSH_PORT = Number(process.env.SSH_PORT || 2222)
const SSH_USER = process.env.SSH_USER || 'sshuser'
const SSH_PASS = process.env.SSH_PASS || 'change_me_sshpass'

test.describe('SFTP文件管理', () => {
  test.beforeAll(async ({ browser }) => {
    const page = await browser.newPage()
    await loginAsAdmin(page)
    const token = await apiLogin(page)
    await apiDeleteByPrefix(page, token, PREFIX)
    await apiAddConnection(page, token, {
      name: `${PREFIX}ops`,
      host: SSH_HOST,
      port: SSH_PORT,
      username: SSH_USER,
      password: SSH_PASS,
      agent_id: 'local-agent',
    })
    await apiWaitForConnection(page, token, `${PREFIX}ops`)
    await page.close()
  })

  test.afterAll(async ({ browser }) => {
    const page = await browser.newPage()
    await loginAsAdmin(page)
    const token = await apiLogin(page)
    await apiDeleteByPrefix(page, token, PREFIX)
    await page.close()
  })

  test('文件浏览器打开并列出目录', async ({ page }) => {
    await goSshView(page)
    await openFileBrowser(page, `${PREFIX}ops`)
    await expect(page.locator('.sfb').first()).toBeVisible({ timeout: 15000 })
    await expect(page.locator('.sfb-row').first()).toBeVisible({ timeout: 15000 })
    const rows = page.locator('.sfb-row')
    expect(await rows.count()).toBeGreaterThan(0)
    await expect(page.locator('.sfb-breadcrumb').first()).toBeVisible()
    await page.screenshot({ path: 'screenshots/sftp-file-browser.png' })
  })

  test('新建目录', async ({ page }) => {
    await goSshView(page)
    await openFileBrowser(page, `${PREFIX}ops`)
    await expect(page.locator('.sfb').first()).toBeVisible({ timeout: 15000 })
    const dirName = `ops_dir_${Date.now()}`
    await page.locator('button:has-text("新建目录")').first().click()
    await page.waitForTimeout(500)
    await page.locator('.sfb-modal-input').fill(dirName)
    await page.locator('.sfb-modal-actions .sfb-btn-ok').click({ force: true })
    await page.waitForTimeout(3000)
    const maskVisible = await page.locator('.sfb-modal-mask').isVisible().catch(() => false)
    if (maskVisible) {
      await page.locator('.sfb-modal-mask').click({ position: { x: 5, y: 5 } })
      await page.waitForTimeout(500)
    }
    await page.locator('.sfb-toolbtn:has-text("刷新")').first().click({ force: true })
    await page.waitForTimeout(2000)
    await expect(page.locator('.sfb').first()).toBeVisible()
    await page.screenshot({ path: 'screenshots/sftp-new-dir.png' })
  })

  test('新建文件', async ({ page }) => {
    await goSshView(page)
    await openFileBrowser(page, `${PREFIX}ops`)
    await expect(page.locator('.sfb').first()).toBeVisible({ timeout: 15000 })
    const fileName = `ops_file_${Date.now()}.txt`
    await page.locator('button:has-text("新建文件")').first().click()
    await page.waitForTimeout(500)
    await page.locator('.sfb-modal-input').fill(fileName)
    await page.locator('.sfb-modal-actions .sfb-btn-ok').click({ force: true })
    await page.waitForTimeout(3000)
    const maskVisible = await page.locator('.sfb-modal-mask').isVisible().catch(() => false)
    if (maskVisible) {
      await page.locator('.sfb-modal-mask').click({ position: { x: 5, y: 5 } })
      await page.waitForTimeout(500)
    }
    await page.locator('.sfb-toolbtn:has-text("刷新")').first().click({ force: true })
    await page.waitForTimeout(2000)
    await expect(page.locator('.sfb').first()).toBeVisible()
    await page.screenshot({ path: 'screenshots/sftp-new-file.png' })
  })

  test('面包屑导航', async ({ page }) => {
    await goSshView(page)
    await openFileBrowser(page, `${PREFIX}ops`)
    await expect(page.locator('.sfb-breadcrumb').first()).toBeVisible({ timeout: 15000 })
  })

  test('文件排序', async ({ page }) => {
    await goSshView(page)
    await openFileBrowser(page, `${PREFIX}ops`)
    await expect(page.locator('.sfb').first()).toBeVisible({ timeout: 15000 })
    const sortHeader = page.locator('th.col-name').first()
    await sortHeader.click()
    await page.waitForTimeout(500)
    await sortHeader.click()
    await page.waitForTimeout(500)
    await expect(page.locator('.sfb-row').first()).toBeVisible()
  })

  test('文件上传按钮存在', async ({ page }) => {
    await goSshView(page)
    await openFileBrowser(page, `${PREFIX}ops`)
    await expect(page.locator('.sfb').first()).toBeVisible({ timeout: 15000 })
    await expect(page.locator('button:has-text("上传")').first()).toBeVisible()
    await page.screenshot({ path: 'screenshots/sftp-upload.png' })
  })
})
