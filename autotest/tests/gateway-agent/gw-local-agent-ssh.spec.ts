import { test, expect } from '@playwright/test'
import { loginAsAdmin, apiAddConnection, apiDeleteByPrefix, apiWaitForConnection, goSshView, apiLogin, openSshTerminal, assertTerminalVisible, termTypeAndCheck, expandGroup } from '../helpers'

const BASE_URL = process.env.BASE_URL || 'https://127.0.0.1:5588'
const PREFIX = 'c_s_'

test.describe('阶段4: 本地网关+本地Agent SSH', () => {
  test.beforeAll(async ({ browser }) => {
    const page = await browser.newPage()
    await loginAsAdmin(page)
    const token = await apiLogin(page)
    await apiDeleteByPrefix(page, token, PREFIX)
    await apiAddConnection(page, token, {
      name: `${PREFIX}gw_local_ssh`, host: '127.0.0.1', port: 2222,
      username: 'sshuser', password: 'change_me_sshpass',
      agent_id: 'local-agent', gateway_id: 'local-gateway',
    })
    await apiWaitForConnection(page, token, `${PREFIX}gw_local_ssh`)
    await page.close()
  })

  test('C-S-01: 网关连接在侧栏显示', async ({ page }) => {
    await goSshView(page)
    await expandGroup(page, 'ssh')
    await expect(page.locator('.ssh-card-name:has-text("' + PREFIX + 'gw_local_ssh")')).toBeVisible({ timeout: 10000 })
  })

  test('C-S-02: 通过网关打开SSH终端', async ({ page }) => {
    await goSshView(page)
    await openSshTerminal(page, `${PREFIX}gw_local_ssh`)
    await assertTerminalVisible(page)
  })

  test('C-S-03: 通过网关终端交互', async ({ page }) => {
    await goSshView(page)
    await openSshTerminal(page, `${PREFIX}gw_local_ssh`)
    await termTypeAndCheck(page, 'echo "C_GW_LOCAL"', 'C_GW_LOCAL')
  })
})
