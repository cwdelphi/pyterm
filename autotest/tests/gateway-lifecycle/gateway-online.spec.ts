import { test, expect } from '@playwright/test'
import { loginAsAdmin, apiLogin, goAdminView } from '../helpers'

const BASE_URL = process.env.BASE_URL || 'https://127.0.0.1:5588'

test.describe('阶段1: 本地网关生命周期', () => {
  test('GW-01: 本地网关在线状态', async ({ page }) => {
    const token = await apiLogin(page)
    const resp = await page.request.get(`${BASE_URL}/api/admin/gateways`, {
      headers: { Authorization: `Bearer ${token}` },
    })
    const data = await resp.json()
    const gw = data.gateways.find((g: any) => g.id === 'local-gateway')
    expect(gw).toBeTruthy()
    expect(gw.online).toBe(true)
  })

  test('GW-02: 本地网关配置信息', async ({ page }) => {
    const token = await apiLogin(page)
    const resp = await page.request.get(`${BASE_URL}/api/admin/gateways`, {
      headers: { Authorization: `Bearer ${token}` },
    })
    const data = await resp.json()
    const gw = data.gateways.find((g: any) => g.id === 'local-gateway')
    expect(gw).toBeTruthy()
    expect(gw.id).toBe('local-gateway')
    expect(gw.name).toBe('本地网关')
    expect(gw.url).toContain('5599')
  })

  test('GW-03: 本地网关Token验证', async ({ page }) => {
    const token = await apiLogin(page)
    const resp = await page.request.get(`${BASE_URL}/api/admin/gateways`, {
      headers: { Authorization: `Bearer ${token}` },
    })
    const data = await resp.json()
    const gw = data.gateways.find((g: any) => g.id === 'local-gateway')
    expect(gw).toBeTruthy()
    expect(gw.token).toBeTruthy()
    expect(gw.token.length).toBeGreaterThan(10)
  })

  test('GW-04: 本地网关在线列表', async ({ page }) => {
    const token = await apiLogin(page)
    const resp = await page.request.get(`${BASE_URL}/api/webrtc/gateways`, {
      headers: { Authorization: `Bearer ${token}` },
    })
    const data = await resp.json()
    expect(data.gateways).toBeDefined()
    expect(Array.isArray(data.gateways)).toBe(true)
    const gw = data.gateways.find((g: any) => g.id === 'local-gateway')
    expect(gw).toBeTruthy()
    expect(gw.status).toBe('online')
  })

  test('GW-05: 管理后台网关管理页面', async ({ page }) => {
    await goAdminView(page)
    // 点击网关管理 tab
    const gwTab = page.locator('.menu-item:has-text("网关")').first()
    await gwTab.click()
    await page.waitForTimeout(1000)
    // 检查网关表格
    await expect(page.locator('.gw-table')).toBeVisible({ timeout: 10000 })
    // id 单元格含 local-gateway；名称单元格为中文名，避免 strict mode 双匹配
    await expect(page.locator('td.id-cell:has-text("local-gateway")').first()).toBeVisible({ timeout: 5000 })
  })
})
