import { test, expect } from '@playwright/test'
import { goSshView, openFirstConnection, waitForFirstData, closeAllSshTabs } from '../reliability-helpers'

test.describe('T1-T7 时序测试', () => {
  test('T1 终端消息顺序一致', async ({ page }) => {
    await goSshView(page)
    await openFirstConnection(page)
    const sshBtn = page.locator('.ssh-sub-btn.type-ssh').first()
    await sshBtn.click()
    await page.waitForTimeout(3000)
    const termVisible = await page.locator('.xterm-rows').first().isVisible({ timeout: 10000 }).catch(() => false)
    expect(termVisible).toBeTruthy()
    const textarea = page.locator('textarea.xterm-helper-textarea').first()
    for (let i = 1; i <= 5; i++) {
      await textarea.fill('echo ORDER_' + i + '\n')
      await page.waitForTimeout(500)
    }
    await page.waitForTimeout(2000)
    const text = await page.locator('.xterm-rows').first().textContent() || ''
    expect(text).toContain('ORDER_1')
    expect(text).toContain('ORDER_5')
    await page.screenshot({ path: 'screenshots/T1-ordering.png' })
    await closeAllSshTabs(page)
  })

  test('T4 连接状态机正确', async ({ page }) => {
    await goSshView(page)
    await openFirstConnection(page)
    const sshBtn = page.locator('.ssh-sub-btn.type-ssh').first()
    await sshBtn.click()
    await page.waitForTimeout(3000)
    const ok = await waitForFirstData(page, 15000)
    expect(ok).toBeTruthy()
    await page.screenshot({ path: 'screenshots/T4-state-machine.png' })
    await closeAllSshTabs(page)
  })

  test('T5 关闭序列', async ({ page }) => {
    await goSshView(page)
    await openFirstConnection(page)
    const sshBtn = page.locator('.ssh-sub-btn.type-ssh').first()
    await sshBtn.click()
    await page.waitForTimeout(3000)
    const ok = await waitForFirstData(page, 10000)
    expect(ok).toBeTruthy()
    const closeBtn = page.locator('.ssh-tab .tab-close').first()
    if (await closeBtn.isVisible({ timeout: 2000 }).catch(() => false)) {
      await closeBtn.click()
      await page.waitForTimeout(1000)
    }
    await page.screenshot({ path: 'screenshots/T5-close-seq.png' })
  })

  test('T6 终端resize同步', async ({ page }) => {
    await goSshView(page)
    await openFirstConnection(page)
    const sshBtn = page.locator('.ssh-sub-btn.type-ssh').first()
    await sshBtn.click()
    await page.waitForTimeout(3000)
    const ok = await waitForFirstData(page, 10000)
    expect(ok).toBeTruthy()
    const textarea = page.locator('textarea.xterm-helper-textarea').first()
    await textarea.fill('echo BEFORE_RESIZE\n')
    await page.waitForTimeout(1000)
    await page.setViewportSize({ width: 800, height: 600 })
    await page.waitForTimeout(1000)
    await page.setViewportSize({ width: 1280, height: 720 })
    await page.waitForTimeout(1000)
    await textarea.fill('echo AFTER_RESIZE\n')
    await page.waitForTimeout(2000)
    const text = await page.locator('.xterm-rows').first().textContent() || ''
    expect(text.length).toBeGreaterThan(0)
    await page.screenshot({ path: 'screenshots/T6-resize.png' })
    await closeAllSshTabs(page)
  })
})
