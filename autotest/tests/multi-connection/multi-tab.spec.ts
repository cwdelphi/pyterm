import { test, expect } from '@playwright/test'
import { loginAsAdmin, apiAddConnection, apiDeleteByPrefix, goSshView, apiLogin, setupConsoleCapture, expandGroup } from '../helpers'

const PREFIX = 'multi_'

test.describe('多连接隔离测试', () => {
  test.beforeAll(async ({ browser }) => {
    const page = await browser.newPage()
    await loginAsAdmin(page)
    const token = await apiLogin(page)
    await apiDeleteByPrefix(page, token, PREFIX)
    for (let i = 1; i <= 3; i++) {
      await apiAddConnection(page, token, {
        name: `${PREFIX}conn${i}`, host: '127.0.0.1', port: 2222,
        username: 'sshuser', password: 'change_me_sshpass', agent_id: 'local-agent',
      })
    }
    await page.close()
  })

  test('M-01: 3个终端并发', async ({ page }) => {
    const console = setupConsoleCapture(page, 'M-01')
    await goSshView(page)
    await expandGroup(page, 'ssh')

    for (const name of [`${PREFIX}conn1`, `${PREFIX}conn2`, `${PREFIX}conn3`]) {
      const card = page.locator(`.ssh-card:has-text("${name}")`).first()
      await card.locator('.ssh-card-main').click()
      await page.waitForTimeout(300)
      const sshBtn = page.locator('.ssh-sub-btn.type-ssh').first()
      await sshBtn.click()
      await page.waitForTimeout(2000)
    }

    const tabs = page.locator('.ssh-tab')
    expect(await tabs.count()).toBeGreaterThanOrEqual(3)

    for (let i = 0; i < 3; i++) {
      const tab = tabs.nth(i)
      await tab.click()
      await page.waitForTimeout(500)
      const textarea = page.locator('textarea.xterm-helper-textarea').first()
      if (await textarea.isVisible()) {
        await textarea.fill(`echo "TAB_${i}_TEST"\n`)
        await page.waitForTimeout(1000)
      }
    }

    await page.screenshot({ path: 'screenshots/multi-3tabs.png' })
    console.dumpLogs()
  })
})
