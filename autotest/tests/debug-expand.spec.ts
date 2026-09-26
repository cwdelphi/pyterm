import { test, expect } from '@playwright/test'
import { loginAsAdmin, goSshView } from './helpers'

test('debug expand', async ({ page }) => {
  await goSshView(page)
  const headers = page.locator('.group-header')
  console.log('group-header count:', await headers.count())
  for (let i = 0; i < await headers.count(); i++) {
    const text = await headers.nth(i).textContent()
    console.log(`  header[${i}]: "${text}"`)
  }
  const sshHeader = page.locator('.group-header').filter({ hasText: 'SSH/FILE' }).first()
  console.log('SSH header visible:', await sshHeader.isVisible())
  await sshHeader.click()
  await page.waitForTimeout(500)
  await page.screenshot({ path: 'screenshots/debug-expand.png' })
  const cards = page.locator('.ssh-card')
  console.log('ssh-card count after expand:', await cards.count())
})
