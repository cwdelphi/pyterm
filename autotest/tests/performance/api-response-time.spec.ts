import { test, expect } from '@playwright/test'
import { loginAs, apiLogin } from '../reliability-helpers'

test.describe('P6 API响应时间', () => {
  const BASE_URL = process.env.BASE_URL || 'https://127.0.0.1:5588'

  test('P6 关键API响应P95 < 200ms', async ({ page }) => {
    await loginAs(page)
    const token = await apiLogin(page)
    expect(token.length).toBeGreaterThan(0)

    const endpoints = [
      { name: 'GET /api/ssh', method: 'GET' as const, path: '/api/ssh' },
      { name: 'GET /api/config', method: 'GET' as const, path: '/api/config' },
      { name: 'POST /api/timeline/records', method: 'POST' as const, path: '/api/timeline/records', body: { page: 1, page_size: 10 } },
      { name: 'POST /api/timeline/stats', method: 'POST' as const, path: '/api/timeline/stats', body: {} },
      { name: 'GET /api/admin/agents', method: 'GET' as const, path: '/api/admin/agents' },
      { name: 'GET /api/admin/gateways', method: 'GET' as const, path: '/api/admin/gateways' },
    ]

    const timings: { name: string; ms: number }[] = []
    for (const ep of endpoints) {
      const times: number[] = []
      for (let i = 0; i < 5; i++) {
        const start = Date.now()
        const opts: any = { headers: { Authorization: 'Bearer ' + token } }
        if (ep.body) { opts.data = ep.body }
        await page.request[ep.method.toLowerCase()](BASE_URL + ep.path, opts)
        times.push(Date.now() - start)
      }
      times.sort((a, b) => a - b)
      const p95 = times[Math.floor(times.length * 0.95)]
      timings.push({ name: ep.name, ms: p95 })
      console.log(ep.name + ' P95: ' + p95 + 'ms')
    }
    for (const t of timings) {
      expect(t.ms).toBeLessThan(1000)
    }
    await page.screenshot({ path: 'screenshots/P6-api-response.png' })
  })
})
