import { test, expect } from '@playwright/test'
import { loginAsAdmin, apiLogin, apiAddConnection } from '../helpers'

function db(sql: string): string {
  try {
    const { execSync } = require('child_process')
    return execSync(`docker exec pyterm_mariadb mariadb -u ppy -pchange_me_pass ppy_tools -N -e "${sql}"`, { encoding: 'utf-8', timeout: 10000 }).trim()
  } catch (e: any) {
    return 'ERROR: ' + (e.stderr || e.message).substring(0, 200)
  }
}

function sleep(ms: number) { return new Promise(r => setTimeout(r, ms)) }

const TESTS = [
  { name: 'T1-SSH直连-本机', conn_type: 'ssh', host: '10.2.0.11', port: 22, username: 'root', password: 'cw', agent_id: 'local-agent', gw: '' },
  { name: 'T2-SSH直连-远程', conn_type: 'ssh', host: '192.0.2.39', port: 22, username: 'root', password: 'cw', agent_id: 'remote-agent', gw: '' },
  { name: 'T3-SSH网关-本地', conn_type: 'ssh', host: '10.2.0.11', port: 22, username: 'root', password: 'cw', agent_id: 'local-agent', gw: 'local-gateway' },
  { name: 'T4-SSH网关-远程', conn_type: 'ssh', host: '192.0.2.39', port: 22, username: 'root', password: 'cw', agent_id: 'remote-agent', gw: 'local-gateway' },
  { name: 'T5-VNC直连-tx', conn_type: 'vnc', host: 'portal.example.com', port: 5900, username: '', password: 'cw', agent_id: 'local-agent', gw: '' },
  { name: 'T6-VNC直连-内网', conn_type: 'vnc', host: '192.0.2.50', port: 5900, username: '', password: 'cw', agent_id: 'local-agent', gw: '' },
  { name: 'T7-VNC网关-tx', conn_type: 'vnc', host: 'portal.example.com', port: 5900, username: '', password: 'cw', agent_id: 'local-agent', gw: 'local-gateway' },
  { name: 'T8-VNC网关-内网', conn_type: 'vnc', host: '192.0.2.50', port: 5900, username: '', password: 'cw', agent_id: 'local-agent', gw: 'local-gateway' },
]

test.describe('Timeline 8-config test', () => {
  for (const cfg of TESTS) {
    test(cfg.name, async ({ page }) => {
      test.setTimeout(60000)

      // 1. 登录
      await loginAsAdmin(page)
      const token = await apiLogin(page)
      expect(token.length).toBeGreaterThan(0)

      // 2. 创建连接
      await apiAddConnection(page, token, {
        name: cfg.name,
        host: cfg.host,
        port: cfg.port,
        username: cfg.username || '',
        password: cfg.password,
        agent_id: cfg.agent_id,
        gateway_id: cfg.gw,
        connection_type: cfg.conn_type,
        vnc_port: 5900,
        vnc_password: 'cw',
      })
      console.log(`  Created: ${cfg.name}`)

      // 3. 刷新页面让Vue状态同步
      await page.goto('/')
      await page.waitForLoadState('networkidle')
      await page.waitForTimeout(1000)

      // 4. 点击远程管理菜单
      await page.locator('.topmenu').filter({ hasText: /远程管理/ }).first().click()
      await page.waitForTimeout(1500)

      // 5. 展开对应分组
      const groupLabel = cfg.conn_type === 'vnc' ? 'VNC' : 'SSH/FILE'
      const group = page.locator('.conn-group').filter({ hasText: groupLabel }).first()
      const arrow = group.locator('.group-arrow').first()
      const isExpanded = await arrow.evaluate(el => el.classList.contains('expanded'))
      if (!isExpanded) {
        await group.locator('.group-header').first().click()
        await page.waitForTimeout(500)
      }

      // 6. 找到卡片并打开
      const card = group.locator('.ssh-card').filter({ hasText: cfg.name }).first()
      await expect(card).toBeVisible({ timeout: 10000 })
      await card.locator('.ssh-card-main').click()
      await page.waitForTimeout(500)

      // 7. 打开会话
      if (cfg.conn_type === 'vnc') {
        const btn = page.locator('.ssh-sub-btn.type-vnc').first()
        await btn.click()
        await sleep(20000)
      } else {
        const btn = page.locator('.ssh-sub-btn.type-ssh').first()
        await btn.click()
        await sleep(15000)
      }

      await page.screenshot({ path: `screenshots/${cfg.name}.png` })

      // 8. 关闭标签页触发 reportDiagnostic
      const closeBtn = page.locator('.tab-close').first()
      if (await closeBtn.isVisible({ timeout: 3000 }).catch(() => false)) {
        await closeBtn.click()
      }
      await sleep(5000)
      console.log(`  Done: ${cfg.name}`)
    })
  }
})
