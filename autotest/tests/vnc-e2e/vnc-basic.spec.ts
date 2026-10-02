import { test, expect } from '@playwright/test'
import { loginAsAdmin, apiAddConnection, apiDeleteByPrefix, goSshView, apiLogin, setupConsoleCapture, openVncTab, assertVncConnected, expandGroup } from '../helpers'

const PREFIX = 'vnc_e2e_'

test.describe('VNC E2E 测试', () => {
  test.beforeAll(async ({ browser }) => {
    const page = await browser.newPage()
    await loginAsAdmin(page)
    const token = await apiLogin(page)
    await apiDeleteByPrefix(page, token, PREFIX)
    await apiAddConnection(page, token, {
      name: `${PREFIX}vnc_test`,
      host: '127.0.0.1',
      port: 22,
      username: '',
      password: '',
      connection_type: 'vnc',
      vnc_port: 5900, // 对齐测试环境实际监听(pyterm_test_ssh 内 Xtigervnc)
      vnc_password: 'vncPass123',
    })
    await page.close()
  })

  test('V-01: VNC 连接建立', async ({ page }) => {
    const console = setupConsoleCapture(page, 'V-01')
    await goSshView(page)
    await expandGroup(page, 'vnc')
    await openVncTab(page, `${PREFIX}vnc_test`)
    await assertVncConnected(page)
    await page.screenshot({ path: 'screenshots/vnc-连接成功.png' })
    console.dumpLogs()
  })

  test('V-02: VNC 窗口调整', async ({ page }) => {
    const console = setupConsoleCapture(page, 'V-02')
    await goSshView(page)
    await expandGroup(page, 'vnc')
    await openVncTab(page, `${PREFIX}vnc_test`)
    await assertVncConnected(page)

    await page.setViewportSize({ width: 800, height: 600 })
    await page.waitForTimeout(2000)
    await page.screenshot({ path: 'screenshots/vnc-窗口调整-800x600.png' })

    await page.setViewportSize({ width: 1920, height: 1080 })
    await page.waitForTimeout(2000)
    await page.screenshot({ path: 'screenshots/vnc-窗口调整-1920x1080.png' })

    const errors = console.getLogs().filter(l => l.includes('[PAGE_ERROR]'))
    expect(errors.length).toBe(0)
    console.dumpLogs()
  })

  test('V-07: 无虚假 SSH connect', async ({ page }) => {
    const console = setupConsoleCapture(page, 'V-07')
    await goSshView(page)
    await expandGroup(page, 'vnc')
    await openVncTab(page, `${PREFIX}vnc_test`)
    await page.waitForTimeout(3000)

    const logs = console.getLogs()
    const sshErrors = logs.filter(l =>
      l.includes('SSH') && (l.includes('error') || l.includes('failed') || l.includes('refused'))
    )
    expect(sshErrors.length).toBe(0)
    console.dumpLogs()
  })
})
