import { test, expect } from '@playwright/test'
import { loginAsAdmin, apiAddConnection, apiDeleteByPrefix, goSshView, apiLogin, openVncTab, expandGroup } from '../helpers'

const PREFIX = 'vnc_debug_'

test('VNC debug', async ({ page }) => {
  const logs: string[] = []
  page.on('console', m => {
    const t = m.text()
    logs.push(t)
    console.log('[BROWSER]', t)
  })
  page.on('pageerror', err => {
    logs.push(`[PAGE_ERROR] ${err.message}`)
    console.log('[PAGE_ERROR]', err.message)
  })

  // Step 1: Login and set up connection via API
  await page.goto('/')
  await page.waitForLoadState('networkidle')
  await page.waitForTimeout(500)
  await page.locator('input').nth(0).fill('admin')
  await page.locator('input').nth(1).fill('change_me_pass')
  await page.locator('button[type="submit"], button:has-text("登录")').first().click()
  await page.waitForTimeout(2000)

  const token = await apiLogin(page)
  await apiDeleteByPrefix(page, token, PREFIX)
  await apiAddConnection(page, token, {
    name: `${PREFIX}vnc_test`,
    host: '127.0.0.1',
    port: 22,
    username: '',
    password: '',
    connection_type: 'vnc',
    vnc_port: 6000,
    vnc_password: 'vncPass123',
  })

  // Step 2: Navigate to SSH/远程管理 view (reloads to pick up fresh connection list)
  await page.goto('/')
  await page.waitForLoadState('networkidle')
  await page.locator('.topmenu:has-text("远程管理")').first().click()
  await page.waitForTimeout(1500)

  // Step 3: Expand VNC group and open VNC tab
  await expandGroup(page, 'vnc')
  await openVncTab(page, `${PREFIX}vnc_test`)

  // Step 4: Wait for VNC connection to establish
  await page.waitForTimeout(20000)
  await page.screenshot({ path: 'screenshots/vnc-debug.png' })
  console.log('=== ALL LOGS ===')
  logs.forEach(l => console.log(l))
})
