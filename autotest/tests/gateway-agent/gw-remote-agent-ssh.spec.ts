import { test, expect } from '@playwright/test'
import { loginAsAdmin, apiAddConnection, apiDeleteByPrefix, apiWaitForConnection, goSshView, apiLogin, openSshTerminal, assertTerminalVisible, termTypeAndCheck, expandGroup } from '../helpers'

const PREFIX = 'd_s_'

test.describe('阶段6: 本地网关+远程Agent SSH', () => {
  test.beforeAll(async ({ browser }) => {
    const page = await browser.newPage()
    await loginAsAdmin(page)
    const token = await apiLogin(page)
    await apiDeleteByPrefix(page, token, PREFIX)
    await apiAddConnection(page, token, {
      name: `${PREFIX}gw_remote_ssh`, host: '203.0.113.10', port: 2222,
      username: 'sshuser', password: 'change_me_sshpass',
      agent_id: 'remote-agent', gateway_id: 'local-gateway',
    })
    await apiWaitForConnection(page, token, `${PREFIX}gw_remote_ssh`)
    await page.close()
  })

  test('D-S-01: 网关+远程Agent连接在侧栏显示', async ({ page }) => {
    await goSshView(page)
    await expandGroup(page, 'ssh')
    await expect(page.locator('.ssh-card-name:has-text("' + PREFIX + 'gw_remote_ssh")')).toBeVisible({ timeout: 10000 })
  })

  test('D-S-02: 通过网关+远程Agent打开SSH终端', async ({ page }) => {
    await goSshView(page)
    await openSshTerminal(page, `${PREFIX}gw_remote_ssh`)
    await assertTerminalVisible(page)
  })

  test('D-S-03: 通过网关+远程Agent终端交互', async ({ page }) => {
    await goSshView(page)
    await openSshTerminal(page, `${PREFIX}gw_remote_ssh`)
    await termTypeAndCheck(page, 'echo "D_GW_REMOTE"', 'D_GW_REMOTE')
  })
})
