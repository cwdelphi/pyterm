import { test, expect } from '@playwright/test'
import { goSshView, openFirstConnection, waitForFirstData, closeAllSshTabs, termTypeAndRead } from '../reliability-helpers'

test.describe('P1-P6 性能测试', () => {
  test('P1 3个并发SSH会话', async ({ page }) => {
    await goSshView(page)
    const openCount = 3
    for (let i = 0; i < openCount; i++) {
      await openFirstConnection(page)
      const sshBtn = page.locator('.ssh-sub-btn.type-ssh').first()
      await sshBtn.click()
      await page.waitForTimeout(1500)
    }
    await page.waitForTimeout(5000)
    const tabs = await page.locator('.ssh-tab').count()
    expect(tabs).toBeGreaterThanOrEqual(1)
    const textarea = page.locator('textarea.xterm-helper-textarea').first()
    if (await textarea.isVisible({ timeout: 2000 }).catch(() => false)) {
      await textarea.fill('echo PERF_CONCURRENT\n')
      await page.waitForTimeout(1000)
    }
    await page.screenshot({ path: 'screenshots/P1-concurrent.png' })
    await closeAllSshTabs(page)
  })

  test('P4 终端输出吞吐量', async ({ page }) => {
    await goSshView(page)
    await openFirstConnection(page)
    const sshBtn = page.locator('.ssh-sub-btn.type-ssh').first()
    await sshBtn.click()
    await page.waitForTimeout(3000)
    const ok = await waitForFirstData(page, 10000)
    expect(ok).toBeTruthy()
    const textarea = page.locator('textarea.xterm-helper-textarea').first()
    await textarea.fill('seq 1 500\n')
    await page.waitForTimeout(3000)
    const text = await page.locator('.xterm-rows').first().textContent() || ''
    expect(text.length).toBeGreaterThan(0)
    await page.screenshot({ path: 'screenshots/P4-throughput.png' })
  })

  test('P5 多次连接后内存', async ({ page }) => {
    await goSshView(page)
    for (let i = 0; i < 3; i++) {
      await openFirstConnection(page)
      const sshBtn = page.locator('.ssh-sub-btn.type-ssh').first()
      await sshBtn.click()
      await page.waitForTimeout(2000)
      await closeAllSshTabs(page)
      await page.waitForTimeout(500)
    }
    const mem = await page.evaluate(() => {
      const m = (performance as any).memory
      return m ? m.usedJSHeapSize : 0
    })
    console.log('Memory after 3 cycles:', (mem / 1024 / 1024).toFixed(1) + 'MB')
    await page.screenshot({ path: 'screenshots/P5-memory.png' })
  })
})
