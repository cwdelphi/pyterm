const { chromium } = require('playwright')
const BASE = 'https://127.0.0.1:5588'
const LABEL = process.env.LABEL || 'old'
;(async () => {
  const browser = await chromium.launch({ headless: true })
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 720 } })
  const page = await ctx.newPage()
  page.on('console', (m) => { if (m.type() === 'error') console.log('CERR:', m.text().slice(0, 200)) })
  await page.goto(BASE + '/')
  await page.waitForLoadState('domcontentloaded'); await page.waitForTimeout(500)
  const pw = page.locator('input[type="password"]').first()
  if (await pw.isVisible({ timeout: 3000 }).catch(() => false)) {
    await page.locator('input:not([type="password"])').first().fill('admin')
    await pw.fill('change_me_pass')
    await page.locator('button').filter({ hasText: /登\s*录/ }).first().click()
    await page.waitForTimeout(2000)
  }
  await page.goto(BASE + '/'); await page.waitForTimeout(1500)
  const header = page.locator('.group-header', { hasText: 'SSH/FILE' }).first()
  const expanded = await header.locator('.group-arrow').first().evaluate((el) => el.classList.contains('expanded')).catch(() => false)
  if (!expanded) { await header.click(); await page.waitForTimeout(600) }
  const card = page.locator('.ssh-card:has-text("' + (process.env.CONN || 'perf_old') + '")').first()
  await card.locator('.ssh-card-main').click(); await page.waitForTimeout(800)
  const sub = card.locator('.ssh-sub-btn.type-ssh').first()
  console.log('sub visible:', await sub.isVisible().catch(() => false))
  await sub.click({ timeout: 10000 }).catch((e) => console.log('sub click fail', String(e).split('\n')[0]))
  const ta = page.locator('textarea.xterm-helper-textarea').first()
  const ok = await ta.waitFor({ state: 'visible', timeout: 30000 }).then(() => true).catch(() => false)
  console.log('textarea visible:', ok)
  await page.screenshot({ path: `/tmp/echo-${LABEL}.png` }).catch(() => undefined)
  if (!ok) { await browser.close(); process.exit(3) }
  await ta.click()
  await page.waitForTimeout(1000)
  const samples = []
  for (let i = 0; i < 15; i++) {
    const tok = `Q${i}q`
    const t0 = Date.now()
    await ta.type(tok, { delay: 0 })
    let hit = null
    const dl = t0 + 8000
    while (Date.now() < dl) {
      const txt = await page.locator('.xterm-rows').first().textContent().catch(() => '')
      if (txt && txt.includes(tok)) { hit = Date.now() - t0; break }
      await page.waitForTimeout(15)
    }
    if (hit != null) samples.push(hit)
    await ta.press('Enter').catch(() => {})
    await page.waitForTimeout(80)
  }
  const sorted = [...samples].sort((a, b) => a - b)
  const p95 = sorted.length ? sorted[Math.min(sorted.length - 1, Math.ceil(0.95 * sorted.length) - 1)] : null
  console.log('ECHO', JSON.stringify({ n: samples.length, samples, p95 }))
  await browser.close()
})().catch((e) => { console.error('ERR', e); process.exit(1) })
