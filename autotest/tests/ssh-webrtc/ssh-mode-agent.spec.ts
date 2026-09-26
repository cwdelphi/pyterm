import { test, expect } from '@playwright/test'
import { loginAsAdmin, apiAddConnection, apiDeleteByPrefix, apiWaitForConnection, goSshView, apiLogin, openSshTerminal, openFileBrowser, assertTerminalVisible, expandGroup } from '../helpers'

const AGENT_ID = process.env.AGENT_ID || 'local-agent'
const PREFIX = 'agent_mode_'
const CONN_NAME = `${PREFIX}ssh`

test.describe('Agent模式 SSH/SFTP', () => {
  test.beforeAll(async ({ browser }) => {
    const page = await browser.newPage()
    await loginAsAdmin(page)
    const token = await apiLogin(page)
    await apiDeleteByPrefix(page, token, PREFIX)
    await apiAddConnection(page, token, {
      name: CONN_NAME,
      host: '127.0.0.1',
      port: 2222,
      username: 'sshuser',
      password: 'change_me_sshpass',
      agent_id: AGENT_ID,
    })
    await apiWaitForConnection(page, token, CONN_NAME)
    await page.close()
  })

  test.afterAll(async ({ browser }) => {
    const page = await browser.newPage()
    await loginAsAdmin(page)
    const token = await apiLogin(page)
    await apiDeleteByPrefix(page, token, PREFIX)
    await page.close()
  })

  test('A-01: Agent模式创建终端', async ({ page }) => {
    await goSshView(page)
    await expandGroup(page, 'ssh')
    await openSshTerminal(page, CONN_NAME)
    await assertTerminalVisible(page)
    await expect(page.locator('.ssh-term-wrap').first()).toBeVisible()
    await page.screenshot({ path: 'screenshots/ssh-agent-mode-terminal.png' })
  })

  test('A-05: Agent模式Tab图标和状态栏', async ({ page }) => {
    await goSshView(page)
    await openSshTerminal(page, CONN_NAME)
    await assertTerminalVisible(page)

    const tab = page.locator('.ssh-tab').filter({ hasText: CONN_NAME }).first()
    await expect(tab).toBeVisible()
    await expect(tab.locator('.tab-webrtc')).toBeVisible()

    const statusbar = page.locator('.ssh-statusbar')
    await expect(statusbar).toContainText('Agent')
    await page.screenshot({ path: 'screenshots/ssh-agent-mode-statusbar.png' })
  })

  test('A-02: Agent模式SFTP', async ({ page }) => {
    await goSshView(page)
    await openFileBrowser(page, CONN_NAME)
    await expect(page.locator('.sfb').first()).toBeVisible({ timeout: 15000 })
    await page.screenshot({ path: 'screenshots/ssh-agent-mode-sftp.png' })
  })

  test('A-03: Agent模式断线重连', async ({ page }) => {
    await goSshView(page)
    await openSshTerminal(page, CONN_NAME)
    await assertTerminalVisible(page)

    const tab = page.locator('.ssh-tab').filter({ hasText: CONN_NAME }).first()
    await expect(tab).toBeVisible()

    const reconnectBtn = tab.locator('.tab-reconnect')
    if (await reconnectBtn.isVisible().catch(() => false)) {
      await reconnectBtn.click()
      await page.waitForTimeout(3000)
      await page.screenshot({ path: 'screenshots/ssh-agent-mode-reconnect.png' })
    } else {
      const dot = tab.locator('.tab-dot')
      await expect(dot).toHaveClass(/connected|connecting/)
      await page.screenshot({ path: 'screenshots/ssh-agent-mode-connected.png' })
    }
  })
})
