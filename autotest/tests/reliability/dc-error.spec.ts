import { test, expect } from '@playwright/test'
import { goSshView, openFirstConnection, waitForFirstData } from '../reliability-helpers'

test.describe('R6 DataChannel错误恢复', () => {
  test('R6 DC连接后正常工作', async ({ page }) => {
    await goSshView(page)
    await openFirstConnection(page)
    const sshBtn = page.locator('.ssh-sub-btn.type-ssh').first()
    await sshBtn.click()
    await page.waitForTimeout(3000)
    const ok = await waitForFirstData(page, 10000)
    expect(ok).toBeTruthy()
    const textarea = page.locator('textarea.xterm-helper-textarea').first()
    await textarea.fill('echo DC_TEST\n')
    await page.waitForTimeout(1000)
    await textarea.fill('echo DC_TEST_2\n')
    await page.waitForTimeout(1000)
    await page.screenshot({ path: 'screenshots/R6-dc-stable.png' })
  })
})
