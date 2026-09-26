import { test, expect } from '@playwright/test'
import { loginAsAdmin, apiLogin, goAdminView } from '../helpers'

const BASE_URL = process.env.BASE_URL || 'https://127.0.0.1:5588'

test.describe('SFTP认证', () => {
  test('SFTP服务认证字段验证', async ({ page }) => {
    await loginAsAdmin(page)
    const token = await apiLogin(page)

    const resp = await page.request.get(`${BASE_URL}/api/sftp/config`, {
      headers: { Authorization: `Bearer ${token}` },
    })
    expect(resp.ok()).toBeTruthy()
    const cfg = await resp.json()
    expect(typeof cfg.username).toBe('string')
    expect(String(cfg.username).length).toBeGreaterThan(0)
    expect(typeof cfg.password === 'string' || typeof cfg.password === 'number').toBeTruthy()

    const statusResp = await page.request.get(`${BASE_URL}/api/sftp/status`, {
      headers: { Authorization: `Bearer ${token}` },
    })
    expect(statusResp.ok()).toBeTruthy()
    const status = await statusResp.json()
    expect(status).toHaveProperty('running')
    expect(typeof status.running).toBe('boolean')

    await goAdminView(page)
    await page.screenshot({ path: 'screenshots/sftp-auth-fields.png' })
  })
})
