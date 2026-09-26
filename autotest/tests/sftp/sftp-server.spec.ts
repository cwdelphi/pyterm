import { test, expect } from '@playwright/test'
import { loginAsAdmin, apiLogin } from '../helpers'

const BASE_URL = process.env.BASE_URL || 'https://127.0.0.1:5588'

test.describe('SFTP服务器', () => {
  test('SFTP状态接口可用', async ({ page }) => {
    await loginAsAdmin(page)
    const token = await apiLogin(page)
    const resp = await page.request.get(`${BASE_URL}/api/sftp/status`, {
      headers: { Authorization: `Bearer ${token}` },
    })
    expect(resp.ok()).toBeTruthy()
    const status = await resp.json()
    expect(status).toHaveProperty('running')
    expect(typeof status.running).toBe('boolean')
  })

  test('SFTP配置读取', async ({ page }) => {
    await loginAsAdmin(page)
    const token = await apiLogin(page)
    const resp = await page.request.get(`${BASE_URL}/api/sftp/config`, {
      headers: { Authorization: `Bearer ${token}` },
    })
    expect(resp.ok()).toBeTruthy()
    const cfg = await resp.json()
    expect(cfg).toBeTruthy()
    expect(typeof cfg.share_dir === 'string' || cfg.share_dir === undefined).toBeTruthy()
    if (cfg.share_dir !== undefined) {
      expect(String(cfg.share_dir).length).toBeGreaterThan(0)
    }
  })

  test('SFTP日志接口', async ({ page }) => {
    await loginAsAdmin(page)
    const token = await apiLogin(page)
    const resp = await page.request.get(`${BASE_URL}/api/sftp/log`, {
      headers: { Authorization: `Bearer ${token}` },
    })
    expect(resp.ok()).toBeTruthy()
    const data = await resp.json()
    expect(Array.isArray(data.log)).toBeTruthy()
  })

  test('SFTP连接命令信息完整', async ({ page }) => {
    await loginAsAdmin(page)
    const token = await apiLogin(page)
    const cfgResp = await page.request.get(`${BASE_URL}/api/sftp/config`, {
      headers: { Authorization: `Bearer ${token}` },
    })
    const cfg = await cfgResp.json()
    expect(cfg.port).toBeGreaterThan(0)
    expect(String(cfg.share_dir || '').length).toBeGreaterThan(0)
    await page.screenshot({ path: 'screenshots/sftp-started.png' })
  })
})
