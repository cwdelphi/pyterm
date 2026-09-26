import { test, expect } from '@playwright/test'
import { loginAsAdmin, apiAddConnection, apiDeleteByPrefix, goSshView, apiLogin, openFileBrowser, setupConsoleCapture, expandGroup } from '../helpers'

const PREFIX = 'sftp_stab_'

test.describe('SFTP 稳定性测试', () => {
  test.beforeAll(async ({ browser }) => {
    const page = await browser.newPage()
    await loginAsAdmin(page)
    const token = await apiLogin(page)
    await apiDeleteByPrefix(page, token, PREFIX)
    await apiAddConnection(page, token, {
      name: `${PREFIX}sftp_test`, host: '127.0.0.1', port: 2222,
      username: 'sshuser', password: 'change_me_sshpass', agent_id: 'local-agent',
    })
    await page.close()
  })

  test('S-01: 连续浏览目录', async ({ page }) => {
    const console = setupConsoleCapture(page, 'S-01')
    await goSshView(page)
    await expandGroup(page, 'ssh')
    await openFileBrowser(page, `${PREFIX}sftp_test`)

    for (let i = 0; i < 10; i++) {
      const row = page.locator('.sfb-row').first()
      if (await row.isVisible()) {
        const name = await row.locator('.col-name').textContent()
        if (name && !name.includes('.')) {
          await row.dblclick()
          await page.waitForTimeout(1000)
        }
      }
    }

    await expect(page.locator('.sfb').first()).toBeVisible()
    await page.screenshot({ path: 'screenshots/sftp-连续浏览.png' })

    const logs = console.getLogs()
    const timeouts = logs.filter(l => l.includes('超时') || l.includes('timeout'))
    expect(timeouts.length).toBe(0)
    console.dumpLogs()
  })
})
