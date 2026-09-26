import { test, expect } from '@playwright/test'
import { loginAs, goSshView, openFirstConnection, termTypeAndRead, waitForDcOpen, waitForFirstData, closeAllSshTabs } from '../reliability-helpers'

test.describe('R1-R2 连接恢复', () => {
  test('R1 SSH断线后重连恢复', async ({ page }) => {
    await goSshView(page)
    await openFirstConnection(page)
    const sshBtn = page.locator('.ssh-sub-btn.type-ssh').first()
    await sshBtn.click()
    await page.waitForTimeout(3000)
    const ok = await waitForFirstData(page, 10000)
    expect(ok).toBeTruthy()
    const termVisible = await page.locator('.xterm').first().isVisible()
    expect(termVisible).toBeTruthy()
    const canExec = await termTypeAndRead(page, 'echo RECONNECT_TEST', 'RECONNECT_TEST')
    expect(canExec).toBeTruthy()
    await page.screenshot({ path: 'screenshots/R1-reconnect.png' })
  })

  test('R2 Agent重启后连接恢复', async ({ page }) => {
    await goSshView(page)
    await openFirstConnection(page)
    const sshBtn = page.locator('.ssh-sub-btn.type-ssh').first()
    await sshBtn.click()
    await page.waitForTimeout(3000)
    const ok1 = await waitForFirstData(page, 10000)
    expect(ok1).toBeTruthy()
    await page.screenshot({ path: 'screenshots/R2-before-restart.png' })
    await closeAllSshTabs(page)
    await page.waitForTimeout(2000)
    await openFirstConnection(page)
    await sshBtn.click()
    await page.waitForTimeout(3000)
    const ok2 = await waitForFirstData(page, 15000)
    expect(ok2).toBeTruthy()
    await page.screenshot({ path: 'screenshots/R2-after-restart.png' })
  })
})
