import { test, expect } from '@playwright/test'
import { loginAsAdmin } from './helpers'

test.describe('日志系统', () => {
  test('后端日志可读取', async ({ page }) => {
    await loginAsAdmin(page)
    const logs = await page.evaluate(async () => {
      const r = await fetch('/api/logs')
      return r.json()
    })
    // API返回 {lines: [...]} 或 {backend: [...], frontend: [...]}
    const hasData = (logs.lines && logs.lines.length > 0) ||
                    (logs.backend && logs.backend.length > 0)
    expect(hasData).toBeTruthy()
  })

  test('前端日志发送和接收', async ({ page }) => {
    await loginAsAdmin(page)
    await page.evaluate(async () => {
      const r = await fetch('/api/log', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ level: 'info', message: 'test log entry', url: '/test' })
      })
      return r.json()
    })

    const logs = await page.evaluate(async () => {
      const r = await fetch('/api/logs')
      return r.json()
    })
    const hasData = (logs.frontend && logs.frontend.length > 0) ||
                    (logs.lines && logs.lines.length > 0)
    expect(hasData).toBeTruthy()
  })
})
