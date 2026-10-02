import { test, expect } from '@playwright/test'
import { assertTerminalVisible, termTypeAndCheck, expandAgentGroup } from '../helpers'
import { loginAs } from '../reliability-helpers'

// webterm 需要 ≥2.2.5 的 agent 二进制(本地 local-agent), 避免点到旧版远端 agent
const AGENT_NAME = process.env.WEBTERM_AGENT || 'local-agent'
// 「Agent 控制台」只显示**自有** Agent(共享来的不进控制台), 故必须以该 Agent 的属主账号登录
// local-agent / local-agent-2 已统一归属 admin(speedtest 资格同为 admin 属主), 遗留账号 inportb 不再使用
const OWNER_USER = process.env.WEBTERM_AGENT_USER || 'admin'
const OWNER_PASS = process.env.WEBTERM_AGENT_PASS || 'change_me_pass'

async function goOwnerSshView(page: import('@playwright/test').Page) {
  await loginAs(page, OWNER_USER, OWNER_PASS)
  await page.locator('.topmenu:has-text("远程管理")').first().click()
  await page.waitForTimeout(2500)
}
// docker 模式下 webterm 应打开宿主机控制台, 设 WEBTERM_HOSTNAME 时校验 hostname 为宿主机名
const HOST_HOSTNAME = process.env.WEBTERM_HOSTNAME || ''

function agentCard(page: import('@playwright/test').Page) {
  return page
    .locator('.agent-card:not(.offline)')
    .filter({ has: page.locator('.agent-card-name', { hasText: AGENT_NAME }) })
    .first()
}

test.describe('webterm Agent本地控制台', () => {
  test('Agent分组渲染、默认折叠、展开后在线卡可见', async ({ page }) => {
    await goOwnerSshView(page)
    await expect(page.locator('.agent-group')).toBeVisible({ timeout: 10000 })
    const arrow = page.locator('.agent-group .group-arrow').first()
    // 缺省折叠: 控制台不再默认展开 Agent 分组
    await expect(arrow).not.toHaveClass(/expanded/, { timeout: 5000 })
    await expect(page.locator('.agent-card')).toHaveCount(0, { timeout: 5000 })
    // 展开后卡片可见
    await expandAgentGroup(page)
    await expect(page.locator('.agent-card').first()).toBeVisible({ timeout: 10000 })
  })

  test('单击在线Agent卡直开webterm并执行命令', async ({ page }) => {
    let auditBody: string | null = null
    await page.route('**/api/webrtc/webterm-open', async route => {
      auditBody = route.request().postData()
      await route.continue()
    })
    await goOwnerSshView(page)
    await expandAgentGroup(page)
    const card = agentCard(page)
    await expect(card).toBeVisible({ timeout: 10000 })
    await card.click()

    // 直开终端页签
    await expect(page.locator('.ssh-tab')).toHaveCount(1, { timeout: 10000 })

    // shell 可交互 (TC-W01): 本地 PTY 回显
    await assertTerminalVisible(page)
    await termTypeAndCheck(page, 'echo webterm_e2e_ok', 'webterm_e2e_ok')

    // 打开留痕 (TC-W06)
    await expect
      .poll(() => (auditBody ? (JSON.parse(auditBody) as { agent_id: string }).agent_id : ''), {
        timeout: 10000,
      })
      .toBeTruthy()

    // 重复点击同一Agent → 聚焦既有页签, 不新开
    await card.click()
    await page.waitForTimeout(500)
    await expect(page.locator('.ssh-tab')).toHaveCount(1)
  })

  test('docker模式webterm=宿主机控制台(hostname/whoami/init)', async ({ page }) => {
    await goOwnerSshView(page)
    await expandAgentGroup(page)
    const card = agentCard(page)
    await expect(card).toBeVisible({ timeout: 10000 })
    await card.click()
    await expect(page.locator('.ssh-tab')).toHaveCount(1, { timeout: 10000 })
    await assertTerminalVisible(page)
    // 宿主机 root shell: whoami 恒为 root
    await termTypeAndCheck(page, 'whoami', 'root')
    // pid=host 判定: 容器内可见 init 为 systemd(宿主机), 容器自身 init(=wragent) 则非
    await termTypeAndCheck(page, 'cat /proc/1/comm', 'systemd')
    // 设了 WEBTERM_HOSTNAME 则强校验宿主机名
    if (HOST_HOSTNAME) {
      await termTypeAndCheck(page, 'hostname', HOST_HOSTNAME)
    }
    // 宿主机根目录可写(nsenter 后 / 即宿主机根, 证明 :rw 挂载生效)
    await termTypeAndCheck(page, 'touch /.webterm_rw_check && rm -f /.webterm_rw_check && echo rw_ok', 'rw_ok')
  })

  test('webterm标签页断开后可重连', async ({ page }) => {
    await goOwnerSshView(page)
    await expandAgentGroup(page)
    const card = agentCard(page)
    await expect(card).toBeVisible({ timeout: 10000 })
    await card.click()
    await expect(page.locator('.ssh-tab')).toHaveCount(1, { timeout: 10000 })
    await assertTerminalVisible(page)
    await termTypeAndCheck(page, 'echo webterm_reconnect_ok', 'webterm_reconnect_ok')

    // 关闭页签 → 重新打开仍是新 shell
    await page.locator('.ssh-tab .tab-close').first().click()
    await page.waitForTimeout(400)
    await expect(page.locator('.ssh-tab')).toHaveCount(0)
    await card.click()
    await expect(page.locator('.ssh-tab')).toHaveCount(1, { timeout: 10000 })
    await assertTerminalVisible(page)
    await termTypeAndCheck(page, 'echo webterm_reopen_ok', 'webterm_reopen_ok')
  })
})
