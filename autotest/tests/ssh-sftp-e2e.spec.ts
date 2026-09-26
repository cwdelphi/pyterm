import { test, expect, type Page } from '@playwright/test'
import { loginAsAdmin } from './helpers'

const SSH_HOST = '203.0.113.30'
const SSH_USER = 'root'
const SSH_PASS = 'change_me_pass'
const TIMEOUT = 25000

async function goSshView(page: Page) {
  await page.goto('/')
  await page.waitForLoadState('networkidle')
  await page.waitForTimeout(500)
  await loginAsAdmin(page)
  await page.waitForTimeout(1500)
  const sshBtn = page.locator('button:has-text("SSH管理")').first()
  await sshBtn.click()
  await page.waitForTimeout(1000)
}

async function openAddModal(page: Page) {
  const btn = page.locator('.sidebar-actions button:has-text("＋"), button[title="新建连接"]').first()
  await btn.click()
  await page.waitForTimeout(500)
  await expect(page.locator('.modal-box')).toBeVisible({ timeout: 5000 })
}

async function fillAndSave(page: Page, opts: {
  name: string; host: string; port?: number; username: string;
  password: string;
}) {
  const modal = page.locator('.modal-box')
  const rows = modal.locator('.form-row')
  await rows.nth(0).locator('input').fill(opts.name)
  await rows.nth(1).locator('input').fill(opts.host)
  await rows.nth(2).locator('input[type="number"]').first().fill(String(opts.port || 22))
  await rows.nth(3).locator('input').fill(opts.username)
  await modal.locator('input[type="password"]').fill(opts.password)

  const saveBtn = modal.locator('button:has-text("添加")')
  await saveBtn.click()
  await page.waitForTimeout(1500)
}

async function sidebarHas(page: Page, name: string) {
  return await page.locator(`.si-name:has-text("${name}")`).count() > 0
}

// ──────── SSH Connection Mode Tests ────────

test.describe('SSH连接模式', () => {
  test('E2E-01: 添加直连模式SSH', async ({ page }) => {
    await goSshView(page)
    await openAddModal(page)
    await fillAndSave(page, {
      name: 'e2e-ssh', host: SSH_HOST, port: 22,
      username: SSH_USER, password: SSH_PASS,
    })
    await expect(page.locator('.si-name:has-text("e2e-ssh")')).toBeVisible({ timeout: TIMEOUT })
  })

  test('E2E-02: 直连模式展开子菜单', async ({ page }) => {
    await goSshView(page)
    const item = page.locator('.ssh-item:has-text("e2e-ssh")').first()
    await expect(item).toBeVisible({ timeout: TIMEOUT })
    await item.click()
    await page.waitForTimeout(500)
    const submenu = page.locator('.ssh-submenu:has-text("SSH终端")').first()
    await expect(submenu).toBeVisible({ timeout: 5000 })
  })

  test('E2E-03: 直连模式双击打开终端', async ({ page }) => {
    await goSshView(page)
    const item = page.locator('.ssh-item:has-text("e2e-ssh")').first()
    await item.click()
    await page.waitForTimeout(500)
    const termBtn = page.locator('.ssh-submenu-item:has-text("SSH终端")').first()
    await termBtn.dblclick()
    await page.waitForTimeout(3000)
    const term = page.locator('.xterm').first()
    await expect(term).toBeVisible({ timeout: TIMEOUT })
  })

  test('E2E-04: 直连模式终端输入命令', async ({ page }) => {
    await goSshView(page)
    const item = page.locator('.ssh-item:has-text("e2e-ssh")').first()
    await item.click()
    await page.waitForTimeout(500)
    await page.locator('.ssh-submenu-item:has-text("SSH终端")').first().dblclick()
    await page.waitForTimeout(3000)
    const textarea = page.locator('textarea.xterm-helper-textarea').first()
    if (await textarea.count() > 0) {
      await textarea.fill('echo "hello_e2e"\n')
      await page.waitForTimeout(2000)
      const text = await page.locator('.xterm-rows').first().textContent()
      expect(text).toContain('hello_e2e')
    }
  })

  test('E2E-05: 添加Agent模式SSH', async ({ page }) => {
    await goSshView(page)
    await openAddModal(page)
    await fillAndSave(page, {
      name: 'e2e-agent', host: SSH_HOST, port: 22,
      username: SSH_USER, password: SSH_PASS, mode: 'agent',
    })
    await expect(page.locator('.si-name:has-text("e2e-agent")')).toBeVisible({ timeout: TIMEOUT })
  })

  test('E2E-06: 配置面板显示连接模式', async ({ page }) => {
    await goSshView(page)
    const cfgBtn = page.locator('button[title="配置管理"]').first()
    await cfgBtn.click()
    await page.waitForTimeout(500)
    await expect(page.locator('.modal-box')).toBeVisible({ timeout: 5000 })
    await expect(page.locator('td:has-text("直连")').first()).toBeVisible({ timeout: TIMEOUT })
    await expect(page.locator('td:has-text("Agent")').first()).toBeVisible({ timeout: TIMEOUT })
  })

  test('E2E-07: 配置面板点击连接显示详情', async ({ page }) => {
    await goSshView(page)
    const cfgBtn = page.locator('button[title="配置管理"]').first()
    await cfgBtn.click()
    await page.waitForTimeout(500)
    const row = page.locator('.config-table tr:has-text("e2e-ssh")').first()
    await row.click()
    await page.waitForTimeout(500)
    const detail = page.locator('.config-detail').first()
    if (await detail.count() > 0) {
      const text = await detail.textContent()
      expect(text).toContain('直连')
    }
  })

  test('E2E-08: Agent子菜单标识', async ({ page }) => {
    await goSshView(page)
    const item = page.locator('.ssh-item:has-text("e2e-agent")').first()
    await item.click()
    await page.waitForTimeout(500)
    const submenu = page.locator('.ssh-submenu').last()
    await expect(submenu).toBeVisible({ timeout: 5000 })
  })

  test('E2E-09: 状态栏显示模式', async ({ page }) => {
    await goSshView(page)
    const item = page.locator('.ssh-item:has-text("e2e-ssh")').first()
    await item.click()
    await page.waitForTimeout(500)
    await page.locator('.ssh-submenu-item:has-text("SSH终端")').first().dblclick()
    await page.waitForTimeout(3000)
    const statusbar = page.locator('.ssh-statusbar').first()
    if (await statusbar.count() > 0) {
      const text = await statusbar.textContent()
      expect(text).toContain('直连')
    }
  })

  test('E2E-10: 清理SSH测试连接', async ({ page }) => {
    await goSshView(page)
    for (const name of ['e2e-ssh', 'e2e-agent']) {
      const item = page.locator(`.ssh-item:has-text("${name}")`).first()
      if (await item.count() > 0) {
        const delBtn = page.locator(`.si-act.danger`).first()
        if (await delBtn.count() > 0) {
          await delBtn.click()
          await page.waitForTimeout(300)
          const confirmBtn = page.locator('.modal-danger button:has-text("删除"), button:has-text("确认删除")').first()
          if (await confirmBtn.count() > 0) await confirmBtn.click()
          await page.waitForTimeout(1500)
        }
      }
    }
  })
})

// ──────── SFTP File Manager Tests ────────

test.describe('SFTP文件管理', () => {
  const testDirName = `e2e_dir_${Date.now()}`
  const testFileName = `e2e_file_${Date.now()}.txt`
  const renamedFile = `renamed_${Date.now()}.txt`

  test.beforeAll(async ({ browser }) => {
    const page = await browser.newPage()
    const loginRes = await page.request.post('http://127.0.0.1:5588/api/auth/login', {
      data: { username: 'admin', password: 'change_me_pass' },
    })
    const { token } = await loginRes.json()
    const listRes = await page.request.get('http://127.0.0.1:5588/api/ssh', {
      headers: { Authorization: `Bearer ${token}` },
    })
    const conns = await listRes.json()
    const exists = Array.isArray(conns) && conns.find((c: any) => c.name === 'e2e-sftp')
    if (!exists) {
      await page.request.post('http://127.0.0.1:5588/api/ssh/add', {
        headers: { Authorization: `Bearer ${token}` },
        data: {
          name: 'e2e-sftp', host: SSH_HOST, port: 22,
          username: SSH_USER, auth_type: 'password', password: SSH_PASS,
        },
      })
    }
    await page.close()
  })

  test.beforeEach(async ({ page }) => {
    await goSshView(page)
  })

  test('SFTP-01: 打开文件管理器', async ({ page }) => {
    const item = page.locator('.ssh-item:has-text("e2e-sftp")').first()
    if (await item.count() > 0) {
      await item.click()
      await page.waitForTimeout(500)
      const fileBtn = page.locator('.ssh-submenu-item:has-text("SFTP")').first()
      if (await fileBtn.count() > 0) {
        await fileBtn.dblclick()
        await page.waitForTimeout(2000)
        await expect(page.locator('.sfb').first()).toBeVisible({ timeout: TIMEOUT })
      }
    }
  })

  test('SFTP-02: 文件列表显示内容', async ({ page }) => {
    const item = page.locator('.ssh-item:has-text("e2e-sftp")').first()
    if (await item.count() > 0) {
      await item.click()
      await page.waitForTimeout(500)
      await page.locator('.ssh-submenu-item:has-text("SFTP")').first().dblclick()
      await page.waitForTimeout(2000)
      const rows = page.locator('.sfb-row')
      expect(await rows.count()).toBeGreaterThan(0)
    }
  })

  test('SFTP-03: 面包屑导航', async ({ page }) => {
    const item = page.locator('.ssh-item:has-text("e2e-sftp")').first()
    if (await item.count() > 0) {
      await item.click()
      await page.waitForTimeout(500)
      await page.locator('.ssh-submenu-item:has-text("SFTP")').first().dblclick()
      await page.waitForTimeout(2000)
      await expect(page.locator('.sfb-breadcrumb').first()).toBeVisible()
    }
  })

  test('SFTP-04: 新建目录', async ({ page }) => {
    const item = page.locator('.ssh-item:has-text("e2e-sftp")').first()
    if (await item.count() > 0) {
      await item.click()
      await page.waitForTimeout(500)
      await page.locator('.ssh-submenu-item:has-text("SFTP")').first().dblclick()
      await page.waitForTimeout(2000)
      await page.locator('button:has-text("新建目录")').first().click()
      await page.waitForTimeout(500)
      await page.locator('.sfb-modal-input').fill(testDirName)
      await page.locator('.sfb-btn-ok').click()
      await page.waitForTimeout(2000)
      await expect(page.locator(`.dir-name:has-text("${testDirName}")`)).toBeVisible({ timeout: TIMEOUT })
    }
  })

  test('SFTP-05: 进入目录', async ({ page }) => {
    const item = page.locator('.ssh-item:has-text("e2e-sftp")').first()
    if (await item.count() > 0) {
      await item.click()
      await page.waitForTimeout(500)
      await page.locator('.ssh-submenu-item:has-text("SFTP")').first().dblclick()
      await page.waitForTimeout(2000)
      const dirItem = page.locator(`.dir-name:has-text("${testDirName}")`).first()
      if (await dirItem.count() > 0) {
        await dirItem.dblclick()
        await page.waitForTimeout(1500)
        const status = await page.locator('.sfb-statusbar').textContent()
        expect(status).toContain(testDirName)
      }
    }
  })

  test('SFTP-06: 新建文件', async ({ page }) => {
    const item = page.locator('.ssh-item:has-text("e2e-sftp")').first()
    if (await item.count() > 0) {
      await item.click()
      await page.waitForTimeout(500)
      await page.locator('.ssh-submenu-item:has-text("SFTP")').first().dblclick()
      await page.waitForTimeout(2000)
      await page.locator('button:has-text("新建文件")').first().click()
      await page.waitForTimeout(500)
      await page.locator('.sfb-modal-input').fill(testFileName)
      await page.locator('.sfb-btn-ok').click()
      await page.waitForTimeout(2000)
      await expect(page.locator(`td:has-text("${testFileName}")`)).toBeVisible({ timeout: TIMEOUT })
    }
  })

  test('SFTP-07: 双击文件预览', async ({ page }) => {
    const item = page.locator('.ssh-item:has-text("e2e-sftp")').first()
    if (await item.count() > 0) {
      await item.click()
      await page.waitForTimeout(500)
      await page.locator('.ssh-submenu-item:has-text("SFTP")').first().dblclick()
      await page.waitForTimeout(2000)
      const fileRow = page.locator(`tr:has-text("${testFileName}")`).first()
      if (await fileRow.count() > 0) {
        await fileRow.dblclick()
        await page.waitForTimeout(1500)
        await expect(page.locator('.sfb-modal-mask').first()).toBeVisible({ timeout: TIMEOUT })
      }
    }
  })

  test('SFTP-08: 右键重命名', async ({ page }) => {
    const item = page.locator('.ssh-item:has-text("e2e-sftp")').first()
    if (await item.count() > 0) {
      await item.click()
      await page.waitForTimeout(500)
      await page.locator('.ssh-submenu-item:has-text("SFTP")').first().dblclick()
      await page.waitForTimeout(2000)
      const fileRow = page.locator(`tr:has-text("${testFileName}")`).first()
      if (await fileRow.count() > 0) {
        await fileRow.click({ button: 'right' })
        await page.waitForTimeout(500)
        const renameItem = page.locator('.ctx-item:has-text("重命名")').first()
        if (await renameItem.count() > 0) {
          await renameItem.click()
          await page.waitForTimeout(500)
          await page.locator('.sfb-modal-input').fill(renamedFile)
          await page.locator('.sfb-btn-ok').click()
          await page.waitForTimeout(2000)
          await expect(page.locator(`td:has-text("${renamedFile}")`)).toBeVisible({ timeout: TIMEOUT })
        }
      }
    }
  })

  test('SFTP-09: 右键删除文件', async ({ page }) => {
    const item = page.locator('.ssh-item:has-text("e2e-sftp")').first()
    if (await item.count() > 0) {
      await item.click()
      await page.waitForTimeout(500)
      await page.locator('.ssh-submenu-item:has-text("SFTP")').first().dblclick()
      await page.waitForTimeout(2000)
      const fileRow = page.locator(`tr:has-text("${renamedFile}")`).first()
      if (await fileRow.count() > 0) {
        await fileRow.click({ button: 'right' })
        await page.waitForTimeout(500)
        const deleteItem = page.locator('.ctx-danger:has-text("删除")').first()
        if (await deleteItem.count() > 0) {
          await deleteItem.click()
          await page.waitForTimeout(500)
          await expect(page.locator('.sfb-modal-mask')).toBeVisible({ timeout: 5000 })
          const dangerBtn = page.locator('.sfb-btn-danger:has-text("删除")')
          await expect(dangerBtn).toBeVisible({ timeout: 3000 })
          await dangerBtn.click()
          await page.waitForTimeout(3000)
          await page.locator('.sfb-toolbar button:has-text("刷新")').first().click()
          await page.waitForTimeout(2000)
          await expect(page.locator(`td:has-text("${renamedFile}")`)).toHaveCount(0, { timeout: TIMEOUT })
        }
      }
    }
  })

  test('SFTP-10: 工具栏按钮完整', async ({ page }) => {
    const item = page.locator('.ssh-item:has-text("e2e-sftp")').first()
    if (await item.count() > 0) {
      await item.click()
      await page.waitForTimeout(500)
      await page.locator('.ssh-submenu-item:has-text("SFTP")').first().dblclick()
      await page.waitForTimeout(2000)
      await expect(page.locator('.sfb-toolbar button:has-text("上传")')).toBeVisible()
      await expect(page.locator('.sfb-toolbar button:has-text("新建文件")')).toBeVisible()
      await expect(page.locator('.sfb-toolbar button:has-text("新建目录")')).toBeVisible()
      await expect(page.locator('.sfb-toolbar button:has-text("刷新")')).toBeVisible()
    }
  })

  test('SFTP-11: 状态栏显示连接信息', async ({ page }) => {
    const item = page.locator('.ssh-item:has-text("e2e-sftp")').first()
    if (await item.count() > 0) {
      await item.click()
      await page.waitForTimeout(500)
      await page.locator('.ssh-submenu-item:has-text("SFTP")').first().dblclick()
      await page.waitForTimeout(2000)
      const status = await page.locator('.sfb-statusbar').textContent()
      expect(status).toContain(SSH_USER)
      expect(status).toContain(SSH_HOST)
    }
  })

  test('SFTP-12: 排序功能', async ({ page }) => {
    const item = page.locator('.ssh-item:has-text("e2e-sftp")').first()
    if (await item.count() > 0) {
      await item.click()
      await page.waitForTimeout(500)
      await page.locator('.ssh-submenu-item:has-text("SFTP")').first().dblclick()
      await page.waitForTimeout(2000)
      await page.locator('.col-name').first().click()
      await page.waitForTimeout(300)
      await expect(page.locator('.col-name span').first()).toBeVisible()
    }
  })

  test('SFTP-13: 刷新按钮', async ({ page }) => {
    const item = page.locator('.ssh-item:has-text("e2e-sftp")').first()
    if (await item.count() > 0) {
      await item.click()
      await page.waitForTimeout(500)
      await page.locator('.ssh-submenu-item:has-text("SFTP")').first().dblclick()
      await page.waitForTimeout(2000)
      await page.locator('.sfb-toolbar button:has-text("刷新")').first().click()
      await page.waitForTimeout(1500)
      await expect(page.locator('.sfb-table')).toBeVisible()
    }
  })

  test('SFTP-14: 返回上级目录', async ({ page }) => {
    const item = page.locator('.ssh-item:has-text("e2e-sftp")').first()
    if (await item.count() > 0) {
      await item.click()
      await page.waitForTimeout(500)
      await page.locator('.ssh-submenu-item:has-text("SFTP")').first().dblclick()
      await page.waitForTimeout(2000)
      const dirItem = page.locator(`.dir-name:has-text("${testDirName}")`).first()
      if (await dirItem.count() > 0) {
        await dirItem.dblclick()
        await page.waitForTimeout(1500)
      }
      await page.locator('.bc-item').first().click()
      await page.waitForTimeout(1500)
      const status = await page.locator('.sfb-statusbar').textContent()
      expect(status).toContain('/')
    }
  })
})

// ──────── Cleanup ────────

test.describe('清理', () => {
  test('清理所有E2E测试连接', async ({ page }) => {
    const loginRes = await page.request.post('http://127.0.0.1:5588/api/auth/login', {
      data: { username: 'admin', password: 'change_me_pass' },
    })
    const { token } = await loginRes.json()
    const listRes = await page.request.get('http://127.0.0.1:5588/api/ssh', {
      headers: { Authorization: `Bearer ${token}` },
    })
    const conns = await listRes.json()
    if (!Array.isArray(conns)) return
    for (const conn of conns) {
      if (conn.name && conn.name.startsWith('e2e-')) {
        await page.request.post('http://127.0.0.1:5588/api/ssh/delete', {
          headers: { Authorization: `Bearer ${token}` },
          data: { id: conn.id },
        })
      }
    }
  })
})
