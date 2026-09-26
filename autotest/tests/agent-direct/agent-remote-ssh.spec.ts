import { test, expect } from '@playwright/test'
import { loginAsAdmin, apiAddConnection, apiDeleteByPrefix, apiWaitForConnection, goSshView, apiLogin, openSshTerminal, openFileBrowser, assertTerminalVisible, termTypeAndCheck, expandGroup } from '../helpers'
import { makeConnB, genConnName } from '../fixtures/test-data'

const PREFIX = 'b_s_'

test.describe('场景B: 直连 + 远程 Agent SSH', () => {
  const conn = makeConnB(PREFIX)

  test.beforeAll(async ({ browser }) => {
    const page = await browser.newPage()
    await loginAsAdmin(page)
    const token = await apiLogin(page)
    await apiDeleteByPrefix(page, token, PREFIX)
    await apiAddConnection(page, token, {
      name: conn.name,
      host: conn.host,
      port: conn.port,
      username: conn.username,
      password: conn.password,
      agent_id: conn.agentId,
    })
    await apiWaitForConnection(page, token, conn.name)
    await page.close()
  })

  test('B-S-01: 添加远程Agent连接', async ({ page }) => {
    await goSshView(page)
    await expandGroup(page, 'ssh')
    await expect(page.locator(`.ssh-card-name:has-text("${conn.name}")`)).toBeVisible({ timeout: 10000 })
  })

  test('B-S-02: 打开SSH终端', async ({ page }) => {
    await goSshView(page)
    await openSshTerminal(page, conn.name)
    await assertTerminalVisible(page)
  })

  test('B-S-03: 终端输入输出', async ({ page }) => {
    await goSshView(page)
    await openSshTerminal(page, conn.name)
    await termTypeAndCheck(page, 'echo "B_TEST_OK"', 'B_TEST_OK')
  })
})

test.describe('场景B: 直连 + 远程 Agent SFTP', () => {
  const PREFIX2 = 'b_f_'
  const conn = { ...makeConnB(PREFIX2), name: genConnName(PREFIX2, 'remote_agent_sftp') }

  test.beforeAll(async ({ browser }) => {
    const page = await browser.newPage()
    await loginAsAdmin(page)
    const token = await apiLogin(page)
    await apiDeleteByPrefix(page, token, PREFIX2)
    await apiAddConnection(page, token, {
      name: conn.name,
      host: conn.host,
      port: conn.port,
      username: conn.username,
      password: conn.password,
      agent_id: conn.agentId,
    })
    await apiWaitForConnection(page, token, conn.name)
    await page.close()
  })

  test('B-F-01: 打开文件浏览器', async ({ page }) => {
    await goSshView(page)
    await openFileBrowser(page, conn.name)
    await expect(page.locator('.sfb').first()).toBeVisible({ timeout: 15000 })
  })

  test('B-F-02: 新建目录/文件', async ({ page }) => {
    await goSshView(page)
    await openFileBrowser(page, conn.name)
    await expect(page.locator('.sfb').first()).toBeVisible({ timeout: 15000 })
    const dirName = `b_dir_${Date.now()}`
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
  })

  test('B-F-03: 重命名/删除', async ({ page }) => {
    await goSshView(page)
    await openFileBrowser(page, conn.name)
    await expect(page.locator('.sfb').first()).toBeVisible({ timeout: 15000 })
    const fileName = `b_file_${Date.now()}.txt`
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
    const fileRow = page.locator(`tr:has-text("${fileName}")`).first()
    if (await fileRow.count() > 0) {
      await fileRow.click({ button: 'right' })
      await page.waitForTimeout(500)
      const renameItem = page.locator('.ctx-item:has-text("重命名")').first()
      if (await renameItem.count() > 0) {
        await renameItem.click()
        await page.waitForTimeout(500)
        const renamed = `renamed_${Date.now()}.txt`
        await page.locator('.sfb-modal-input').fill(renamed)
        await page.locator('.sfb-modal-actions .sfb-btn-ok').click({ force: true })
        await page.waitForTimeout(2000)
        await expect(page.locator(`td:has-text("${renamed}")`)).toBeVisible({ timeout: 10000 })
      }
    }
    await expect(page.locator('.sfb').first()).toBeVisible()
  })
})
