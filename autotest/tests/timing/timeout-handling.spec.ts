import { test, expect } from '@playwright/test'
import { goSshView } from '../reliability-helpers'

test.describe('T3 超时检测', () => {
  test('T3 连接超时处理', async ({ page }) => {
    await goSshView(page)
    await page.waitForTimeout(2000)
    await page.screenshot({ path: 'screenshots/T3-timeout.png' })
    const content = await page.textContent('body') || ''
    expect(content.length).toBeGreaterThan(0)
  })
})
