import { test, expect } from '@playwright/test'
import { loginAsAdmin, apiLogin } from './helpers'

test.describe('日志系统', () => {
  test('未带 token 读日志被拒(S4)', async ({ page }) => {
    await page.goto('/')
    const status = await page.evaluate(async () => {
      const r = await fetch('/api/logs')
      return r.status
    })
    expect(status).toBe(401)
  })

  test('后端日志可读取(需 system:admin)', async ({ page }) => {
    await loginAsAdmin(page)
    const token = await apiLogin(page)
    const res = await page.evaluate(async (tk: string) => {
      const r = await fetch('/api/logs', { headers: { Authorization: `Bearer ${tk}` } })
      return { status: r.status, data: await r.json() }
    }, token)
    expect(res.status).toBe(200)
    // API返回 {lines: [...]} 或 {backend: [...], frontend: [...]}
    const hasData = (res.data.lines && res.data.lines.length > 0) ||
                    (res.data.backend && res.data.backend.length > 0)
    expect(hasData).toBeTruthy()
  })

  test('前端日志发送和接收', async ({ page }) => {
    await loginAsAdmin(page)
    const token = await apiLogin(page)
    // 匿名也可上报(登录前异常必须能落库)
    const posted = await page.evaluate(async () => {
      const r = await fetch('/api/log', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ level: 'info', message: 'test log entry', url: '/test' })
      })
      return { status: r.status, data: await r.json() }
    })
    expect(posted.status).toBe(200)
    expect(posted.data.ok).toBe(true)

    const res = await page.evaluate(async (tk: string) => {
      const r = await fetch('/api/logs', { headers: { Authorization: `Bearer ${tk}` } })
      return { status: r.status, data: await r.json() }
    }, token)
    expect(res.status).toBe(200)
    const hasData = (res.data.frontend && res.data.frontend.length > 0) ||
                    (res.data.lines && res.data.lines.length > 0)
    expect(hasData).toBeTruthy()
  })
})
