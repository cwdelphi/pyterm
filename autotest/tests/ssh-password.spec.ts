import { test, expect } from '@playwright/test'
import { loginAsAdmin } from './helpers'

test.describe('SSH Password Preservation', () => {
  test('editing connection preserves password via API', async ({ page }) => {
    await loginAsAdmin(page)
    const token = await page.evaluate(() => localStorage.getItem('token') || '')
    const authHeaders = { Authorization: `Bearer ${token}` }

    // Cleanup
    const existing = await (await page.request.get('/api/ssh', { headers: authHeaders })).json()
    if (Array.isArray(existing)) {
      for (const c of existing.filter((x: any) => x.name === 'pw_test')) {
        await page.request.post('/api/ssh/delete', { headers: authHeaders, data: { id: c.id } })
      }
    }

    // Add connection with password via API
    const addResp = await page.request.post('/api/ssh/add', {
      headers: authHeaders,
      data: { name: 'pw_test', host: '10.0.0.1', port: 22, username: 'root', password: 'secret123' }
    })
    const added = await addResp.json()
    expect(added.id).toBeTruthy()

    // Update without password - should preserve
    const updateResp = await page.request.post('/api/ssh/update', {
      headers: authHeaders,
      data: { id: added.id, name: 'pw_test', host: '10.0.0.1', port: 22, username: 'root' }
    })
    expect(updateResp.status()).toBe(200)

    // Verify password is still set
    const listResp = await page.request.get('/api/ssh', { headers: authHeaders })
    const conns = await listResp.json()
    const conn = conns.find((c: any) => c.name === 'pw_test')
    expect(conn?.has_password).toBe(true)

    // Verify password is NOT exposed in list
    expect(conn).not.toHaveProperty('password')

    // Cleanup
    await page.request.post('/api/ssh/delete', { headers: authHeaders, data: { id: added.id } })
  })

  test('connection list shows has_password flag', async ({ page }) => {
    await loginAsAdmin(page)
    const token = await page.evaluate(() => localStorage.getItem('token') || '')
    const resp = await page.request.get('/api/ssh', {
      headers: { Authorization: `Bearer ${token}` }
    })
    const conns = await resp.json()
    expect(Array.isArray(conns)).toBeTruthy()
    for (const conn of conns) {
      expect(conn).toHaveProperty('has_password')
      expect(conn).not.toHaveProperty('password')
    }
  })
})
