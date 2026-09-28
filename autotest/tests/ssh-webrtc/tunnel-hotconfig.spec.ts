import { test, expect } from '@playwright/test'
import { goSshView, assertTerminalVisible, termTypeAndCheck, apiLogin } from '../helpers'

// TC-PL03/04: 热改隧道配置 —— 仅 tunnel 插件动, webterm 会话不断, SOCKS5 不受影响
const AGENT_ID = process.env.WEBTERM_AGENT || 'local-agent'
const OLD_PORT = 3333
const NEW_PORT = 3456

function agentCard(page: import('@playwright/test').Page) {
  return page
    .locator('.agent-card:not(.offline)')
    .filter({ has: page.locator('.agent-card-name', { hasText: AGENT_ID }) })
    .first()
}

// webterm shell 内探测 LISTEN 端口(容器无 ss/nc; Go 监听在 tcp6 双栈, 需同时扫 tcp+tcp6)
function listenCheck(port: number): string {
  const hex = port.toString(16).toUpperCase().padStart(4, '0')
  return `echo -n LISTEN${port}=; awk '$2 ~ /:${hex}$/ && $4=="0A" {n++} END {print n+0}' /proc/net/tcp /proc/net/tcp6`
}

test.describe('TC-PL03/04 隧道热配置接线', () => {
  test('热改隧道端口: webterm不断/新端口生效/旧端口关闭/SOCKS5不受影响', async ({ page }) => {
    await goSshView(page)
    const card = agentCard(page)
    await expect(card).toBeVisible({ timeout: 10000 })
    await card.click()
    await expect(page.locator('.ssh-tab')).toHaveCount(1, { timeout: 10000 })
    await assertTerminalVisible(page)
    await termTypeAndCheck(page, 'echo pl03_before', 'pl03_before')

    const token = await apiLogin(page)
    const hdrs = { Authorization: `Bearer ${token}` }

    // GET 当前配置(懒迁移视图: 旧格式也应带 plugins{tunnel,socks5})
    const g = await page.request.get(`/api/admin/agents/${AGENT_ID}/config`, { headers: hdrs })
    expect(g.ok(), `GET config ${g.status()}`).toBeTruthy()
    const cfg = (await g.json()).config
    expect(cfg.plugins?.tunnel, 'plugins.tunnel 视图').toBeTruthy()
    expect(cfg.plugins?.socks5, 'plugins.socks5 视图').toBeTruthy()

    const tunnels: any[] = cfg.plugins.tunnel.tunnels || []
    const tcp = tunnels.find((t) => (t.protocol || 'tcp') === 'tcp')
    expect(tcp, 'tcp tunnel exists').toBeTruthy()
    const originalPort = tcp.local_port
    const target = originalPort === NEW_PORT ? OLD_PORT : NEW_PORT

    // 深拷贝原始 plugins(避免下面的 mutation 泄漏进 restore)
    const originalPlugins = JSON.parse(JSON.stringify(cfg.plugins))

    const putCfg = (plugins: any) =>
      page.request.put(`/api/admin/agents/${AGENT_ID}/config`, {
        headers: { ...hdrs, 'Content-Type': 'application/json' },
        data: {
          ws_reconnect_interval: cfg.ws_reconnect_interval ?? 5,
          ws_heartbeat_interval: cfg.ws_heartbeat_interval ?? 30,
          ice_cooldown: cfg.ice_cooldown ?? 2,
          log_level: cfg.log_level ?? 'info',
          plugins,
        },
      })

    const restore = async () => {
      const r = await putCfg(originalPlugins)
      expect(r.ok(), `restore PUT ${r.status()}: ${await r.text()}`).toBeTruthy()
    }

    try {
      // 热改: tcp 隧道端口切换, socks5 原样
      tcp.local_port = target
      const p = await putCfg(cfg.plugins)
      expect(p.ok(), `PUT config ${p.status()}: ${await p.text()}`).toBeTruthy()

      await page.waitForTimeout(3000) // 等 config_update 热推送

      // TC-PL03: webterm 会话跨配置推送不断
      await termTypeAndCheck(page, 'echo pl03_after', 'pl03_after')

      // 新端口已监听 / 旧端口已关闭
      await termTypeAndCheck(page, listenCheck(target), `LISTEN${target}=1`)
      await termTypeAndCheck(page, listenCheck(originalPort), `LISTEN${originalPort}=0`)

      // TC-PL04: SOCKS5 监听不受 tunnel 配置变更影响(固定 2080)
      await termTypeAndCheck(page, listenCheck(2080), 'LISTEN2080=1')
    } finally {
      await restore()
      await page.waitForTimeout(2500)
      // 恢复后原端口重新生效
      await termTypeAndCheck(page, listenCheck(originalPort), `LISTEN${originalPort}=1`)
      await termTypeAndCheck(page, listenCheck(target), `LISTEN${target}=0`)
      await termTypeAndCheck(page, 'echo pl04_restored', 'pl04_restored')
    }
  })
})
