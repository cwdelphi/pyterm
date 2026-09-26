// 用户管理/网关管理 UI 验证截图：admin 禁删、批量删除按钮、表头版本、网关徽标
import { chromium } from 'playwright'
import fs from 'fs'

const BASE = 'https://127.0.0.1:5588'
const OUT = '/tmp/opencode/audit/ui'
fs.mkdirSync(OUT, { recursive: true })

const browser = await chromium.launch({ headless: true, executablePath: '/usr/bin/google-chrome' })
const ctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1600, height: 1000 } })
const page = await ctx.newPage()
const result = {}

await page.goto(BASE + '/', { waitUntil: 'domcontentloaded' })
await page.waitForTimeout(800)
const pw = page.locator('input[type="password"]')
if (await pw.count()) {
  await page.locator('input:not([type="password"]):not([type="checkbox"])').first().fill('admin')
  await pw.first().fill('change_me_pass')
  await page.locator('button').filter({ hasText: /登\s*录/ }).first().click()
  await page.waitForTimeout(2000)
}

await page.locator('text=系统管理').first().click()
await page.waitForTimeout(1000)
await page.locator('.sidebar-nav .menu-item').filter({ hasText: '用户管理' }).first().click()
await page.waitForTimeout(1500)

const rows = page.locator('tbody tr')
result.userRows = await rows.count()
result.deleteBtnCount = await page.locator('tbody .text-btn.danger').count()
result.adminCheckboxDisabled = await page.locator('tbody input[type=checkbox]').first().isDisabled()
const batchBtn = page.locator('button').filter({ hasText: '批量删除' }).first()
result.batchBtnVisible = await batchBtn.isVisible()
result.batchBtnDisabled = await batchBtn.isDisabled()
result.batchBtnText = (await batchBtn.textContent().catch(() => '')).trim()
result.usersHeading = (await page.locator('.admin-header h2').first().textContent().catch(() => '')).trim()
await page.screenshot({ path: `${OUT}/10-users.png` })

await page.locator('.sidebar-nav .menu-item').filter({ hasText: '网关管理' }).first().click()
await page.waitForTimeout(2000)
const headers = await page.locator('thead th').allTextContents()
result.gwHeaders = headers.map(h => h.trim())
result.gwHeaderHasVersion = result.gwHeaders.includes('版本')
result.gwHeaderStillV = result.gwHeaders.some(h => h === 'v' || h === 'V')
result.gwBadge = (await page.locator('.ver-badge').first().textContent().catch(() => '')).trim()
result.gwHeading = (await page.locator('.admin-header h2').first().textContent().catch(() => '')).trim()
await page.screenshot({ path: `${OUT}/11-gateways.png` })

console.log(JSON.stringify(result, null, 1))
console.log('files:', fs.readdirSync(OUT))
await browser.close()
