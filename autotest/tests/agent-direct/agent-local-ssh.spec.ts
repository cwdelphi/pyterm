import { test, expect } from '@playwright/test'
import { loginAsAdmin, apiAddConnection, apiDeleteByPrefix, apiWaitForConnection, goSshView, apiLogin, clickConn, openSshTerminal, assertTerminalVisible, termTypeAndCheck, openAddModal, fillAndSave, sidebarHas, expandGroup } from '../helpers'

const PREFIX = 'a_s_'

test.describe('阶段2: 直连+本地Agent SSH', () => {
  test.beforeAll(async ({ browser }) => {
    const page = await browser.newPage()
    await loginAsAdmin(page)
    const token = await apiLogin(page)
    await apiDeleteByPrefix(page, token, PREFIX)
    await apiAddConnection(page, token, {
      name: `${PREFIX}local_ssh`, host: '127.0.0.1', port: 2222,
      username: 'sshuser', password: 'change_me_sshpass', agent_id: 'local-agent',
    })
    await apiWaitForConnection(page, token, `${PREFIX}local_ssh`)
    await page.close()
  })

  test('A-S-01: 侧栏显示连接', async ({ page }) => {
    await goSshView(page)
    await expandGroup(page, 'ssh')
    await expect(page.locator('.ssh-card-name:has-text("' + PREFIX + 'local_ssh")')).toBeVisible({ timeout: 10000 })
  })

  test('A-S-02: 打开SSH终端', async ({ page }) => {
    await goSshView(page)
    await openSshTerminal(page, `${PREFIX}local_ssh`)
    await assertTerminalVisible(page)
  })

  test('A-S-03: 终端输入输出', async ({ page }) => {
    await goSshView(page)
    await openSshTerminal(page, `${PREFIX}local_ssh`)
    await termTypeAndCheck(page, 'echo "A_TEST_OK"', 'A_TEST_OK')
  })
})
