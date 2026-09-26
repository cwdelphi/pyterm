import { test, expect } from '@playwright/test'
import { loginAsAdmin, apiAddConnection, apiDeleteByPrefix, apiWaitForConnection, goSshView, apiLogin, openFileBrowser } from '../helpers'

const PREFIX = 'sftp_editor_'
const SSH_HOST = process.env.SSH_HOST || '127.0.0.1'
const SSH_PORT = Number(process.env.SSH_PORT || 2222)
const SSH_USER = process.env.SSH_USER || 'sshuser'
const SSH_PASS = process.env.SSH_PASS || 'change_me_sshpass'

test.describe('文件预览/编辑覆盖层', () => {
  test.beforeAll(async ({ browser }) => {
    const page = await browser.newPage()
    await loginAsAdmin(page)
    const token = await apiLogin(page)
    await apiDeleteByPrefix(page, token, PREFIX)
    await apiAddConnection(page, token, {
      name: `${PREFIX}editor`,
      host: SSH_HOST,
      port: SSH_PORT,
      username: SSH_USER,
      password: SSH_PASS,
      agent_id: 'local-agent',
    })
    await apiWaitForConnection(page, token, `${PREFIX}editor`)
    await page.close()
  })

  test.afterAll(async ({ browser }) => {
    const page = await browser.newPage()
    await loginAsAdmin(page)
    const token = await apiLogin(page)
    await apiDeleteByPrefix(page, token, PREFIX)
    await page.close()
  })

  test('右键预览/编辑打开应用内编辑器并保存', async ({ page }) => {
    await goSshView(page)
    await openFileBrowser(page, `${PREFIX}editor`)
    await expect(page.locator('.sfb').first()).toBeVisible({ timeout: 15000 })

    const fileName = `editor_e2e_${Date.now()}.md`
    await page.locator('.sfb-row', { hasText: 'home' }).first().dblclick()
    await page.waitForTimeout(1500)
    await page.locator('.sfb-row', { hasText: 'sshuser' }).first().dblclick()
    await page.waitForTimeout(1500)
    await page.locator('button:has-text("新建文件")').first().click()
    await page.waitForTimeout(500)
    await page.locator('.sfb-modal-input').fill(fileName)
    await page.locator('.sfb-modal-actions .sfb-btn-ok').click({ force: true })
    await page.waitForTimeout(2500)
    await page.locator('.sfb-toolbtn:has-text("刷新")').first().click({ force: true })
    await page.waitForTimeout(1500)

    const row = page.locator('.sfb-row', { hasText: fileName }).first()
    await expect(row).toBeVisible({ timeout: 15000 })

    const pagesBefore = page.context().pages().length
    await row.click({ button: 'right' })
    await expect(page.locator('.sfb-ctxmenu')).toBeVisible({ timeout: 5000 })
    await page.locator('.sfb-ctxmenu .ctx-item', { hasText: '预览/编辑' }).first().click()

    await expect(page.locator('.fem-mask')).toBeVisible({ timeout: 15000 })
    await expect(page.locator('.fem-name')).toHaveText(fileName)
    await expect(page.locator('.fem .cm-content')).toBeVisible({ timeout: 20000 })
    expect(page.context().pages().length).toBe(pagesBefore)

    await page.locator('.fem .cm-content').click()
    await page.keyboard.type('editor-e2e-content')
    await expect(page.locator('.fem-status')).toContainText('未保存', { timeout: 5000 })
    await page.locator('.fem-toolbar button:has-text("保存")').click()
    await expect(page.locator('.fem-status')).toContainText('已保存', { timeout: 20000 })

    await page.locator('.fem-toolbar button').last().click()
    await expect(page.locator('.fem-mask')).toBeHidden({ timeout: 5000 })

    const row2 = page.locator('.sfb-row', { hasText: fileName }).first()
    await row2.click({ button: 'right' })
    await expect(page.locator('.sfb-ctxmenu')).toBeVisible({ timeout: 5000 })
    await page.locator('.sfb-ctxmenu .ctx-item', { hasText: '预览/编辑' }).first().click()
    await expect(page.locator('.fem .cm-content')).toBeVisible({ timeout: 20000 })
    await expect(page.locator('.fem .cm-content')).toContainText('editor-e2e-content', { timeout: 10000 })
    await page.screenshot({ path: 'screenshots/file-editor-overlay.png' })
    await page.locator('.fem-toolbar button').last().click()
    await expect(page.locator('.fem-mask')).toBeHidden({ timeout: 5000 })

    const row3 = page.locator('.sfb-row', { hasText: fileName }).first()
    await row3.click({ button: 'right' })
    await page.locator('.sfb-ctxmenu .ctx-item', { hasText: '删除' }).first().click()
    await page.waitForTimeout(500)
    await page.locator('.sfb-modal-actions .sfb-btn-danger').click({ force: true })
    await page.waitForTimeout(1500)
  })
})
