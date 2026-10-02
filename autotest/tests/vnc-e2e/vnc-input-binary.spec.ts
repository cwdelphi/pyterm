import { test, expect } from '@playwright/test'
import {
  loginAsAdmin, apiAddConnection, apiDeleteByPrefix, apiWaitForConnection,
  goSshView, apiLogin, expandGroup, openVncTab, assertVncConnected,
} from '../helpers'

const PREFIX = 'e11_vnc_'
const GATEWAY_ID = process.env.GATEWAY_ID || 'local-gateway'
const MSG_VNC_INPUT = 0x23

/** 网关模式建链在长跑尾部可能 >10s 甚至首连卡在「等待WebRTC通道」：轮询 + 关标签重开一次 */
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

test.describe('E11 VNC 输入二进制帧', () => {
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

  test('网关模式鼠标帧为二进制 [0x23] 且无 btoa/JSON 包装', async ({ page }) => {
    await loginAsAdmin(page)

    const wsUrls: string[] = []
    const sent: { url: string; payload: string | Buffer }[] = []
    page.on('websocket', (ws) => {
      wsUrls.push(ws.url())
      ws.on('framesent', (f) => sent.push({ url: ws.url(), payload: f.payload as string | Buffer }))
    })

    await goSshView(page)
    await expandGroup(page, 'vnc')
    await openVncTab(page, `${PREFIX}gw`)
    if (!(await waitVncConnected(page, 30000))) {
      await page.locator('.ssh-tab').filter({ hasText: PREFIX }).locator('.tab-close').first().click().catch(() => undefined)
      await page.waitForTimeout(3000)
      await openVncTab(page, `${PREFIX}gw`)
      await assertVncConnected(page)
    }

    // 鼠标移动 + 点击，产生真实 RFB pointer 事件
    const canvas = page.locator('.vnc-canvas-wrap canvas').first()
    const box = await canvas.boundingBox()
    expect(box).not.toBeNull()
    if (!box) return
    for (let i = 0; i < 8; i++) {
      await page.mouse.move(box.x + 40 + i * 15, box.y + 30 + i * 9)
      await page.waitForTimeout(120)
    }
    await page.mouse.click(box.x + 80, box.y + 60)
    await page.waitForTimeout(800)
    await page.screenshot({ path: 'screenshots/e11-vnc-gateway-input.png' })

    // 网关模式必须走网关 WS（非 md 信令 /api/ws/webrtc）
    expect(wsUrls.some((u) => /\/ws(\?|$)/.test(u)), `ws urls: ${wsUrls.join(' , ')}`).toBe(true)

    const binary = sent.filter((f) => typeof f.payload !== 'string' && Buffer.isBuffer(f.payload))
    expect(binary.length, '应有二进制上行帧').toBeGreaterThan(0)

    const vncInput = binary.filter((f) => (f.payload as Buffer)[0] === MSG_VNC_INPUT)
    expect(vncInput.length, `应有 [0x23] VNC 输入帧，共 ${binary.length} 条二进制帧`).toBeGreaterThan(0)
    expect(vncInput[0].payload.length).toBeGreaterThan(1)

    // 回归（R3/D5）：不允许 JSON + base64(btoa) 封装的 vnc_input 文本帧
    const jsonVnc = sent.filter(
      (f) => typeof f.payload === 'string' && /"type"\s*:\s*"vnc_input"/.test(f.payload as string),
    )
    expect(jsonVnc.length, '不应存在 JSON 封装的 vnc_input 帧').toBe(0)
    const btoaVnc = sent.filter(
      (f) => typeof f.payload === 'string' && /btoa|AAAAA[A-Za-z0-9+/]{20,}/.test(f.payload as string),
    )
    expect(btoaVnc.length, '不应存在 base64 编码的鼠标帧').toBe(0)
  })
})
