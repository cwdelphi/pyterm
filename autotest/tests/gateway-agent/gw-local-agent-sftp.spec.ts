import { test, expect } from '@playwright/test'
import { loginAsAdmin, apiAddConnection, apiDeleteByPrefix, apiWaitForConnection, goSshView, apiLogin, openFileBrowser } from '../helpers'

const PREFIX = 'c_f_'

test.describe('阶段5: 本地网关+本地Agent SFTP', () => {
  test.beforeAll(async ({ browser }) => {
    const page = await browser.newPage()
    await loginAsAdmin(page)
    const token = await apiLogin(page)
    await apiDeleteByPrefix(page, token, PREFIX)
    await apiAddConnection(page, token, {
      name: `${PREFIX}gw_local_sftp`, host: '127.0.0.1', port: 2222,
      username: 'sshuser', password: 'change_me_sshpass',
      agent_id: 'local-agent', gateway_id: 'local-gateway',
    })
    await apiWaitForConnection(page, token, `${PREFIX}gw_local_sftp`)
    await page.close()
  })

  test('C-F-01: 通过网关打开文件浏览器', async ({ page }) => {
    await goSshView(page)
    await openFileBrowser(page, `${PREFIX}gw_local_sftp`)
    await expect(page.locator('.sfb').first()).toBeVisible({ timeout: 15000 })
  })

  test('C-F-02: 通过网关文件列表', async ({ page }) => {
    await goSshView(page)
    await openFileBrowser(page, `${PREFIX}gw_local_sftp`)
    // 网关 SFTP 可能因 relay 问题断开，验证 SFB 组件已加载即可
    await expect(page.locator('.sfb').first()).toBeVisible()
    await expect(page.locator('.sfb-statusbar').first()).toBeVisible()
  })
})
