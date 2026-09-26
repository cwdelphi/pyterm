// 阶段2: 诊断面板逐条截图（列表 + 时序图详情）
import { chromium } from 'playwright'
import fs from 'fs'

const BASE = 'https://127.0.0.1:5588'
const OUT = '/tmp/opencode/audit/diag'
fs.mkdirSync(OUT, { recursive: true })

const browser = await chromium.launch({ headless: true, executablePath: '/usr/bin/google-chrome' })
const ctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1600, height: 1000 } })
const page = await ctx.newPage()

await page.goto(BASE + '/', { waitUntil: 'domcontentloaded' })
await page.waitForTimeout(800)
const pw = page.locator('input[type="password"]')
if (await pw.count()) {
  await page.locator('input:not([type="password"]):not([type="checkbox"])').first().fill('admin')
  await pw.first().fill('change_me_pass')
  await page.locator('button').filter({ hasText: /登\s*录/ }).first().click()
  await page.waitForTimeout(2000)
}
// 进 系统管理 → 连接诊断
await page.locator('text=系统管理').first().click()
await page.waitForTimeout(1000)
await page.locator('.sidebar-nav .menu-item').filter({ hasText: '连接诊断' }).first().click()
await page.waitForTimeout(2500)
await page.screenshot({ path: `${OUT}/00-list.png`, fullPage: false })

// 记录行（按 conn 名截图每条详情）
const rows = page.locator('.dp-content tbody tr')
const n = await rows.count().catch(() => 0)
console.log('rows:', n)
for (let i = 0; i < n; i++) {
  const row = rows.nth(i)
  const name = ((await row.textContent().catch(() => '')) || '').replace(/\s+/g, ' ').trim().slice(0, 60)
  await row.locator('.dp-link').click()
  await page.waitForTimeout(3000)
  const hasSvg = await page.locator('.dp-mermaid svg').first().isVisible({ timeout: 5000 }).catch(() => false)
  const fallback = await page.locator('.dp-mermaid-fallback').isVisible().catch(() => false)
  await page.screenshot({ path: `${OUT}/${String(i).padStart(2, '0')}-detail.png`, fullPage: false })
  console.log(`  [${i}] ${name} | svg=${hasSvg} fallback=${fallback}`)
  // 返回列表
  await page.locator('.dp-back').click()
  await page.waitForTimeout(1500)
}

// 统计页签
await page.locator('button').filter({ hasText: '统计' }).first().click().catch(() => {})
await page.waitForTimeout(1500)
await page.screenshot({ path: `${OUT}/99-stats.png` }).catch(() => {})

console.log('files:', fs.readdirSync(OUT))
await browser.close()
