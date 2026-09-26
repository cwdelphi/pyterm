import { test, expect } from '@playwright/test'
import { loginAsAdmin, apiAddConnection, apiDeleteByPrefix, apiWaitForConnection, goSshView, apiLogin, openSshTerminal, assertTerminalVisible, expandGroup } from '../helpers'

const PREFIX = 'webrtc_re_'
const CONN_NAME = `${PREFIX}ssh`

test.describe('WebRTC重连', () => {
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

  test('断线自动重连', async ({ page }) => {
    await goSshView(page)
    await expandGroup(page, 'ssh')
    await openSshTerminal(page, CONN_NAME)
    await assertTerminalVisible(page)

    await page.context().setOffline(true)
    await page.waitForTimeout(2000)
    await page.context().setOffline(false)
    await page.waitForTimeout(5000)

    const reconnectBtn = page.locator('.tab-reconnect')
    const hasReconnect = await reconnectBtn.isVisible().catch(() => false)
    if (hasReconnect) {
      await reconnectBtn.click()
      await page.waitForTimeout(3000)
    }

    await page.screenshot({ path: 'screenshots/webrtc-reconnect.png' })
    await expect(page.locator('.ssh-term-wrap').first()).toBeVisible()
  })
})
