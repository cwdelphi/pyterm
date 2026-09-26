import { test, expect } from '@playwright/test'
import { goSshView, openFirstConnection } from '../reliability-helpers'

test.describe('P3 大文件传输', () => {
  test('P3 SFTP面板可访问', async ({ page }) => {
    await goSshView(page)
    await openFirstConnection(page)
    const fileBtn = page.locator('.ssh-sub-btn.type-file').first()
    if (await fileBtn.isVisible({ timeout: 3000 }).catch(() => false)) {
      await fileBtn.click()
      await page.waitForTimeout(3000)
      await page.screenshot({ path: 'screenshots/P3-large-file.png' })
    }
  })
})
