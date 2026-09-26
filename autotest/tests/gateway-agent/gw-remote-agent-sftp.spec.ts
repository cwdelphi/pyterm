import { test, expect } from '@playwright/test'
import { loginAsAdmin, apiAddConnection, apiDeleteByPrefix, apiWaitForConnection, goSshView, apiLogin, openFileBrowser } from '../helpers'

const PREFIX = 'd_f_'

test.describe('阶段7: 本地网关+远程Agent SFTP', () => {
  test.beforeAll(async ({ browser }) => {
    const page = await browser.newPage()
    await loginAsAdmin(page)
    const token = await apiLogin(page)
    await apiDeleteByPrefix(page, token, PREFIX)
    await apiAddConnection(page, token, {
      name: `${PREFIX}gw_remote_sftp`, host: '203.0.113.10', port: 2222,
      username: 'sshuser', password: 'change_me_sshpass',
      agent_id: 'remote-agent', gateway_id: 'local-gateway',
    })
    await apiWaitForConnection(page, token, `${PREFIX}gw_remote_sftp`)
    await page.close()
  })

  test('D-F-01: 通过网关+远程Agent打开文件浏览器', async ({ page }) => {
    await goSshView(page)
    await openFileBrowser(page, `${PREFIX}gw_remote_sftp`)
    await expect(page.locator('.sfb').first()).toBeVisible({ timeout: 15000 })
  })

  test('D-F-02: 通过网关+远程Agent文件列表', async ({ page }) => {
    await goSshView(page)
    await openFileBrowser(page, `${PREFIX}gw_remote_sftp`)
    await expect(page.locator('.sfb').first()).toBeVisible()
    await expect(page.locator('.sfb-statusbar').first()).toBeVisible()
  })
})
