import { test, expect } from '@playwright/test'
import {
  loginAsAdmin, apiAddConnection, apiDeleteByPrefix, apiWaitForConnection,
  goSshView, apiLogin, expandGroup, openVncTab,
} from '../helpers'

const PREFIX = 't8_ladder_'
const GATEWAY_ID = process.env.GATEWAY_ID || 'local-gateway'

async function waitVncConnected(page: import('@playwright/test').Page, timeoutMs: number) {
  const canvas = page.locator('.vnc-canvas-wrap canvas').first()
  const status = page.locator('.vnc-status').first()
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    const vis = await canvas.isVisible().catch(() => false)
    const cls = (await status.getAttribute('class').catch(() => '')) || ''
    if (vis && /connected/.test(cls)) return true
    await page.waitForTimeout(500)
  }
  return false
}

test.describe('T8 ICE 三级回退阶梯 L2→L3', () => {
  let token = ''

  test.beforeAll(async ({ browser }) => {
    const page = await browser.newPage()
    await loginAsAdmin(page)
    token = await apiLogin(page)
    await apiDeleteByPrefix(page, token, PREFIX)
    await apiAddConnection(page, token, {
      name: `${PREFIX}gw`,
      host: '127.0.0.1',
      port: 22,
      username: '',
      password: '',
      connection_type: 'vnc',
      vnc_port: 5900,
      vnc_password: 'vncPass123',
      gateway_id: GATEWAY_ID,
    })
    await apiWaitForConnection(page, token, `${PREFIX}gw`)
    await page.close()
  })

  test.afterAll(async ({ browser }) => {
    const page = await browser.newPage()
    await loginAsAdmin(page)
    await apiDeleteByPrefix(page, token, PREFIX)
    await page.close()
  })

  test('L2 ICE 失败后网关升级 L3 并连通', async ({ page }) => {
    await loginAsAdmin(page)
    await goSshView(page)
    await expandGroup(page, 'vnc')
    await openVncTab(page, `${PREFIX}gw`)

    // L2 全零候选 → pion Checking 超时(disconnected 5s + failed 25s) → 网关升级 L3 重建
    const ok = await waitVncConnected(page, 95000)
    await page.screenshot({ path: 'screenshots/t8-ice-ladder.png' }).catch(() => undefined)
    expect(ok, 'L2 失败升级 L3 后应在 95s 内连通').toBe(true)
  })
})
