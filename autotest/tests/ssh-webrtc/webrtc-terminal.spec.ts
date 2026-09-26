import { test, expect } from '@playwright/test'
import { loginAsAdmin, apiAddConnection, apiDeleteByPrefix, apiWaitForConnection, goSshView, apiLogin, openSshTerminal, assertTerminalVisible, termTypeAndCheck, expandGroup } from '../helpers'

const PREFIX = 'webrtc_term_'
const CONN_NAME = `${PREFIX}ssh`

test.describe('WebRTC终端', () => {
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
      agent_id: 'local-agent',
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

  test('打开WebRTC终端', async ({ page }) => {
    await goSshView(page)
    await expandGroup(page, 'ssh')
    await openSshTerminal(page, CONN_NAME)
    await assertTerminalVisible(page)
    await expect(page.locator('.ssh-term-wrap').first()).toBeVisible()

    const activeTab = page.locator('.ssh-tab.active')
    await expect(activeTab).toBeVisible()
    await expect(activeTab.locator('.tab-webrtc')).toBeVisible()
    await page.screenshot({ path: 'screenshots/webrtc-terminal-open.png' })
  })

  test('WebRTC终端输入输出', async ({ page }) => {
    await goSshView(page)
    await openSshTerminal(page, CONN_NAME)
    await termTypeAndCheck(page, 'echo WebRTC_E2E_TEST', 'WebRTC_E2E_TEST')
    await page.screenshot({ path: 'screenshots/webrtc-terminal-io.png' })
  })

  test('WebRTC终端resize', async ({ page }) => {
    await goSshView(page)
    await openSshTerminal(page, CONN_NAME)
    await assertTerminalVisible(page)
    await page.setViewportSize({ width: 800, height: 600 })
    await page.waitForTimeout(1000)
    await page.setViewportSize({ width: 1280, height: 720 })
    await page.waitForTimeout(1000)
    await assertTerminalVisible(page)
    await page.screenshot({ path: 'screenshots/webrtc-terminal-resize.png' })
  })
})
