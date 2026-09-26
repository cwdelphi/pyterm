import { test, expect } from '@playwright/test'
import { goSshView, openFirstConnection, waitForFirstData, termTypeAndRead } from '../reliability-helpers'

test.describe('R4 长连接稳定性', () => {
  test('R4 保持连接30s持续收发数据', async ({ page }) => {
    await goSshView(page)
    await openFirstConnection(page)
    const sshBtn = page.locator('.ssh-sub-btn.type-ssh').first()
    await sshBtn.click()
    await page.waitForTimeout(3000)
    const ok = await waitForFirstData(page, 10000)
    expect(ok).toBeTruthy()
    let allOk = true
    for (let i = 0; i < 6; i++) {
      const ok = await termTypeAndRead(page, 'echo STABILITY_' + i, 'STABILITY_' + i)
      if (!ok) { allOk = false; break }
      await page.waitForTimeout(4000)
    }
    expect(allOk).toBeTruthy()
    await page.screenshot({ path: 'screenshots/R4-stability.png' })
  })
})
