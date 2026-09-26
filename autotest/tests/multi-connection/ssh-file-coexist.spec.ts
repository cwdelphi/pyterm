import { test, expect, type Page } from '@playwright/test'
import { loginAsAdmin, apiLogin, apiAddConnection, apiDeleteByPrefix, setupConsoleCapture } from '../helpers'

const PREFIX = 'coexist_'
const TIMEOUT = 30000

async function goSshAndExpand(page: Page) {
  await loginAsAdmin(page)
  await page.locator('.topmenu').filter({ hasText: /远程管理/ }).first().click()
  await page.waitForTimeout(1000)
  const group = page.locator('.conn-group').filter({ hasText: 'SSH/FILE' }).first()
  await expect(group).toBeVisible({ timeout: 8000 })
  const arrow = group.locator('.group-arrow').first()
  const expanded = await arrow.evaluate(el => el.classList.contains('expanded')).catch(() => false)
  if (!expanded) {
    await group.locator('.group-header').first().click()
    await page.waitForTimeout(400)
  }
}

test.describe('SSH+FILE 共存', () => {
  test.beforeAll(async ({ browser }) => {
    const page = await browser.newPage()
    await loginAsAdmin(page)
    const token = await apiLogin(page)
    await apiDeleteByPrefix(page, token, PREFIX)
    await apiAddConnection(page, token, {
      name: `${PREFIX}sshfile`,
      host: '10.2.0.11',
      port: 22,
      username: 'root',
      password: 'cw',
      agent_id: 'local-agent',
      gateway_id: '',
      connection_type: 'ssh',
    })
    await page.close()
  })

  test.afterAll(async ({ browser }) => {
    const page = await browser.newPage()
    await loginAsAdmin(page)
    const token = await apiLogin(page)
    await apiDeleteByPrefix(page, token, PREFIX)
    await page.close()
  })

  test('SSH-FILE-01: 先 SSH 再 FILE，两会话均保持 connected', async ({ page }) => {
    test.setTimeout(90000)
    const console = setupConsoleCapture(page, 'SSH-FILE-01')
    await goSshAndExpand(page)

    const card = page.locator(`.ssh-card`).filter({ hasText: `${PREFIX}sshfile` }).first()
    await expect(card).toBeVisible({ timeout: 10000 })
    await card.locator('.ssh-card-main').click()
    await page.waitForTimeout(400)

    // 1) 打开 SSH
    await card.locator('.ssh-sub-btn.type-ssh').click()
    const sshTab = page.locator('.ssh-tab').filter({ hasText: `${PREFIX}sshfile` }).filter({ hasNotText: '文件' }).first()
    await expect(sshTab).toBeVisible({ timeout: TIMEOUT })
    await expect(sshTab.locator('.tab-dot.connected')).toBeVisible({ timeout: TIMEOUT })
    await expect(page.locator('textarea.xterm-helper-textarea').first()).toBeVisible({ timeout: TIMEOUT })

    // 终端可输入
    const ta = page.locator('textarea.xterm-helper-textarea').first()
    await ta.fill('echo COEXIST_SSH_OK\n')
    await page.waitForTimeout(1500)
    const termText = await page.locator('.xterm-rows').first().textContent().catch(() => '')
    expect(termText || '').toContain('COEXIST_SSH_OK')

    // 2) 打开 FILE（同连接）— 必须用「文件」页签，不能用 /file/（会误匹配 coexist_sshfile）
    await card.locator('.ssh-sub-btn.type-file').click()
    const fileTab = page.locator('.ssh-tab').filter({ hasText: `${PREFIX}sshfile` }).filter({ hasText: '文件' }).first()
    await expect(fileTab).toBeVisible({ timeout: TIMEOUT })
    await expect(fileTab).toHaveClass(/active/, { timeout: TIMEOUT })
    const filePane = page.locator('.ssh-term-wrap').filter({ has: page.locator('.sfb-breadcrumb') })
    await expect(filePane.locator('.sfb-breadcrumb')).toBeVisible({ timeout: TIMEOUT })

    // 3) 两个页签都在且 SSH 仍 connected（FILE 打开后未踢掉 SSH）
    const tabs = page.locator('.ssh-tab')
    expect(await tabs.count()).toBeGreaterThanOrEqual(2)

    const sshTab2 = page.locator('.ssh-tab').filter({ hasText: `${PREFIX}sshfile` }).filter({ hasNotText: '文件' }).first()
    await expect(sshTab2.locator('.tab-dot.connected')).toBeVisible({ timeout: 5000 })

    // 4) 切回 SSH 再执行命令，确认会话未被杀
    await sshTab2.click()
    await expect(sshTab2).toHaveClass(/active/)
    await page.waitForTimeout(500)
    const ta2 = page.locator('textarea.xterm-helper-textarea').first()
    await expect(ta2).toBeVisible({ timeout: 5000 })
    await ta2.fill('echo COEXIST_STILL_ALIVE\n')
    await page.waitForTimeout(2000)
    const termText2 = await page.locator('.xterm-rows').first().textContent().catch(() => '')
    expect(termText2 || '').toContain('COEXIST_STILL_ALIVE')

    // 5) 切到 FILE，列表仍可用；SSH 页签仍 connected
    await fileTab.click()
    await expect(fileTab).toHaveClass(/active/)
    await expect(filePane.locator('.sfb-breadcrumb')).toBeVisible({ timeout: 8000 })
    await expect(filePane.locator('.sfb-table, .sfb-breadcrumb').first()).toBeVisible({ timeout: 8000 })
    const fileRows = await filePane.locator('.sfb-row').count()
    expect(fileRows).toBeGreaterThan(0)
    await expect(sshTab2.locator('.tab-dot.connected')).toBeVisible({ timeout: 5000 })

    await page.screenshot({ path: 'screenshots/ssh-file-coexist.png' })
    console.dumpLogs?.()
  })
})
