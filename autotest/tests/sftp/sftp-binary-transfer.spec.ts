import { test, expect } from '@playwright/test'
import { createHash } from 'node:crypto'
import * as fs from 'node:fs'
import * as os from 'node:os'
import * as path from 'node:path'
import {
  loginAsAdmin, apiAddConnection, apiDeleteByPrefix, apiWaitForConnection,
  goSshView, apiLogin, openFileBrowser,
} from '../helpers'

const BASE = process.env.BASE_URL || 'https://127.0.0.1:5588'
const PREFIX = 'e10_bin_'
const SSH_PORT = Number(process.env.SSH_PORT || 2222)
const SSH_USER = process.env.SSH_USER || 'sshuser'
const SSH_PASS = process.env.SSH_PASS || 'change_me_sshpass'
const AGENT = 'local-agent'
// 127.0.0.1 在 md 容器内不存在 → md 的 /api/sftp-client/* 必须用 docker0 网关地址；
// 127.0.0.1 连接则故意让 HTTP 兜底失败，强制走 WebRTC 二进制写入（T1.3 分片上传）
const HOST_HTTP = process.env.SFTP_HTTP_HOST || '172.17.0.1'
const HOST_AGENT = process.env.SFTP_AGENT_HOST || '127.0.0.1'

// sshuser 对 / 无写权限（mkdir/write 均 Permission denied），所有落盘用 /tmp
const TMP = '/tmp'
const BIG_NAME = 'e10_8mb.bin'
const BIG_SIZE = 8 * 1024 * 1024
const BIG_DIR = 'e10_big_dir'
const BIG_COUNT = 520

const md5 = (buf: Buffer | Uint8Array) => createHash('md5').update(buf).digest('hex')

let token = ''
let bigLocalPath = ''
let bigMd5 = ''

async function sftpPost(page: import('@playwright/test').Page, api: string, data: Record<string, unknown>) {
  return page.request.post(`${BASE}/api/sftp-client/${api}`, {
    headers: { Authorization: `Bearer ${token}` },
    data: { host: HOST_HTTP, port: SSH_PORT, username: SSH_USER, password: SSH_PASS, ...data },
  })
}

async function downloadMd5(page: import('@playwright/test').Page, remotePath: string): Promise<string> {
  const resp = await sftpPost(page, 'download', { path: remotePath })
  expect(resp.status(), `download ${remotePath}`).toBe(200)
  return md5(await resp.body())
}

/** 文件浏览器默认落在 /，进入 /tmp 后再做任何写操作（已停在 /tmp 时直接返回） */
async function gotoTmp(page: import('@playwright/test').Page) {
  const bc = await page.locator('.sfb-breadcrumb').textContent().catch(() => '')
  if (bc && bc.includes('tmp')) return
  const tmpRow = page.locator('.sfb-row').filter({ hasText: 'tmp' }).first()
  if (!(await tmpRow.isVisible().catch(() => false))) {
    await page.locator('.sfb-toolbtn').filter({ hasText: '刷新' }).first().click()
    await expect(tmpRow).toBeVisible({ timeout: 15000 })
  }
  await tmpRow.dblclick()
  await expect(page.locator('.sfb-breadcrumb')).toContainText('tmp', { timeout: 15000 })
}

test.describe('E10 SFTP 二进制传输', () => {
  test.beforeAll(async ({ browser }) => {
    const page = await browser.newPage()
    await loginAsAdmin(page)
    token = await apiLogin(page)
    await apiDeleteByPrefix(page, token, PREFIX)
    await apiAddConnection(page, token, {
      name: `${PREFIX}agent`, host: HOST_AGENT, port: SSH_PORT,
      username: SSH_USER, password: SSH_PASS, agent_id: AGENT,
    })
    await apiAddConnection(page, token, {
      name: `${PREFIX}http`, host: HOST_HTTP, port: SSH_PORT,
      username: SSH_USER, password: SSH_PASS, agent_id: AGENT,
    })
    await apiWaitForConnection(page, token, `${PREFIX}agent`)
    await apiWaitForConnection(page, token, `${PREFIX}http`)

    const buf = Buffer.alloc(BIG_SIZE)
    for (let i = 0; i < BIG_SIZE; i++) buf[i] = (i * 31 + (i >> 11)) & 0xff
    bigLocalPath = path.join(fs.mkdtempSync(path.join(os.tmpdir(), 'e10-')), BIG_NAME)
    fs.writeFileSync(bigLocalPath, buf)
    bigMd5 = md5(buf)
    await page.close()
  })

  test.afterAll(async ({ browser }) => {
    const page = await browser.newPage()
    await loginAsAdmin(page)
    const t = token || (await apiLogin(page))
    token = t
    await sftpPost(page, 'delete', { path: `${TMP}/${BIG_NAME}` }).catch(() => {})
    for (let i = 0; i < BIG_COUNT; i += 8) {
      const batch: Promise<unknown>[] = []
      for (let j = i; j < Math.min(i + 8, BIG_COUNT); j++) {
        const name = `e10_${String(j).padStart(4, '0')}`
        batch.push(sftpPost(page, 'delete', { path: `${TMP}/${BIG_DIR}/${name}` }).catch(() => undefined))
      }
      await Promise.all(batch)
    }
    await sftpPost(page, 'delete', { path: `${TMP}/${BIG_DIR}` }).catch(() => {})
    await apiDeleteByPrefix(page, token, PREFIX)
    await page.close()
  }, { timeout: 600000 })

  test('8MB 上传（HTTP 兜底失败 → WebRTC 二进制分片）md5 比对', async ({ page }) => {
    await loginAsAdmin(page)
    await goSshView(page)
    await openFileBrowser(page, `${PREFIX}agent`)
    await expect(page.locator('.sfb').first()).toBeVisible({ timeout: 15000 })
    await gotoTmp(page)

    const [chooser] = await Promise.all([
      page.waitForEvent('filechooser', { timeout: 15000 }),
      page.locator('.sfb-toolbtn').filter({ hasText: '上传' }).first().click(),
    ])
    await chooser.setFiles(bigLocalPath)

    const row = page.locator('.sfb-row').filter({ hasText: BIG_NAME }).first()
    await expect(row).toBeVisible({ timeout: 120000 })
    await expect(row.locator('.col-size')).toContainText('8.0 MB', { timeout: 10000 })
    await page.screenshot({ path: 'screenshots/e10-8mb-upload.png' })

    expect(await downloadMd5(page, `${TMP}/${BIG_NAME}`)).toBe(bigMd5)
  })

  test('8MB 下载 md5 比对', async ({ page }) => {
    await loginAsAdmin(page)
    // upload 端点的 host/port/username/password/path 是 query 参数（未声明 Form()），file 走 multipart
    const upUrl = `${BASE}/api/sftp-client/upload?${new URLSearchParams({
      host: HOST_HTTP, port: String(SSH_PORT), username: SSH_USER, password: SSH_PASS, path: TMP,
    }).toString()}`
    const up = await page.request.post(upUrl, {
      headers: { Authorization: `Bearer ${token}` },
      multipart: {
        file: { name: BIG_NAME, mimeType: 'application/octet-stream', buffer: fs.readFileSync(bigLocalPath) },
      },
    })
    expect(up.status(), (await up.text()).slice(0, 300)).toBe(200)

    await goSshView(page)
    await openFileBrowser(page, `${PREFIX}http`)
    await expect(page.locator('.sfb').first()).toBeVisible({ timeout: 15000 })
    await gotoTmp(page)
    const row = page.locator('.sfb-row').filter({ hasText: BIG_NAME }).first()
    await expect(row).toBeVisible({ timeout: 30000 })

    await row.click({ button: 'right' })
    const [dl] = await Promise.all([
      page.waitForEvent('download', { timeout: 60000 }),
      page.locator('.sfb-ctxmenu .ctx-item').filter({ hasText: '下载' }).first().click(),
    ])
    const savePath = path.join(os.tmpdir(), `e10-dl-${Date.now()}.bin`)
    await dl.saveAs(savePath)
    const stat = fs.statSync(savePath)
    expect(stat.size).toBe(BIG_SIZE)
    expect(md5(fs.readFileSync(savePath))).toBe(bigMd5)
  })

  test('0 字节读写', async ({ page }) => {
    await loginAsAdmin(page)
    await goSshView(page)
    await openFileBrowser(page, `${PREFIX}http`)
    await gotoTmp(page)
    const name = `e10_zero_${Date.now()}.txt`
    await page.locator('.sfb-toolbtn').filter({ hasText: '新建文件' }).first().click()
    await page.waitForTimeout(500)
    await page.locator('.sfb-modal-input').fill(name)
    await page.locator('.sfb-modal-actions .sfb-btn-ok').click({ force: true })
    await page.waitForTimeout(2000)

    const row = page.locator('.sfb-row').filter({ hasText: name }).first()
    await expect(row).toBeVisible({ timeout: 15000 })
    await expect(row.locator('.col-size')).toContainText('0 B')

    // 读：编辑器打开 0 字节文件应为空
    await row.click({ button: 'right' })
    await page.locator('.sfb-ctxmenu .ctx-item').filter({ hasText: '预览/编辑' }).first().click()
    await expect(page.locator('.fem').first()).toBeVisible({ timeout: 15000 })
    await expect(page.locator('.cm-content').first()).toHaveText('')
    // 写：内容无变化时「保存」应禁用（空文件不触发空写）；0 字节写入已由上面「新建文件」落盘
    await expect(page.locator('.fem-primary').first()).toBeDisabled()
    await page.locator('.fem-btn').filter({ hasText: '✕' }).first().click()
    await page.waitForTimeout(1000)

    const st = await sftpPost(page, 'stat', { path: `${TMP}/${name}` })
    expect(st.status()).toBe(200)
    const body = await st.json()
    expect(Number(body.size ?? body.length ?? -1)).toBe(0)
    await sftpPost(page, 'delete', { path: `${TMP}/${name}` })
  })

  test('UTF-8 内容读写', async ({ page }) => {
    await loginAsAdmin(page)
    const content = '中文测试Ωθπ 🐟 泡鱼终端 emoji😀 + 符号→←'
    await goSshView(page)
    await openFileBrowser(page, `${PREFIX}http`)
    await gotoTmp(page)
    const name = `e10_utf8_${Date.now()}.txt`
    await page.locator('.sfb-toolbtn').filter({ hasText: '新建文件' }).first().click()
    await page.waitForTimeout(500)
    await page.locator('.sfb-modal-input').fill(name)
    await page.locator('.sfb-modal-actions .sfb-btn-ok').click({ force: true })
    await page.waitForTimeout(2000)

    const row = page.locator('.sfb-row').filter({ hasText: name }).first()
    await expect(row).toBeVisible({ timeout: 15000 })
    await row.click({ button: 'right' })
    await page.locator('.sfb-ctxmenu .ctx-item').filter({ hasText: '预览/编辑' }).first().click()
    await expect(page.locator('.fem').first()).toBeVisible({ timeout: 15000 })
    await page.locator('.cm-content').first().fill(content)
    await page.locator('.fem-primary').first().click()
    await page.waitForTimeout(2500)
    await page.locator('.fem-btn').filter({ hasText: '✕' }).first().click()
    await page.waitForTimeout(1000)

    // 落盘字节与写入内容逐字节一致（UTF-8 无损）
    const rd = await sftpPost(page, 'read', { path: `${TMP}/${name}` })
    expect(rd.status()).toBe(200)
    const body = await rd.json()
    const raw = typeof body.content === 'string' ? body.content : String(body.content ?? '')
    // /sftp-client/read 返回 base64（encoding: "base64"）
    expect(body.encoding ?? 'base64').toBe('base64')
    const got = Buffer.from(raw, 'base64').toString('utf8')
    expect(got).toBe(content)
    expect(md5(Buffer.from(content, 'utf8'))).toBe(md5(Buffer.from(got, 'utf8')))

    // 读：重新打开编辑器，内容可回读
    await row.click({ button: 'right' })
    await page.locator('.sfb-ctxmenu .ctx-item').filter({ hasText: '预览/编辑' }).first().click()
    await expect(page.locator('.fem').first()).toBeVisible({ timeout: 15000 })
    await expect(page.locator('.cm-content').first()).toContainText('中文测试', { timeout: 15000 })
    await page.locator('.fem-btn').filter({ hasText: '✕' }).first().click()
    await sftpPost(page, 'delete', { path: `${TMP}/${name}` })
  })

  test('>500 项大目录列表（分片 META 重组）', { timeout: 600000 }, async ({ page }) => {
    await loginAsAdmin(page)
    // 并发 mkdir >8 触发 SSH 并发限流（c=25 时约 20% 返回 500）→ 低并发多轮补齐直到 count 收敛
    await sftpPost(page, 'mkdir', { path: `${TMP}/${BIG_DIR}` })
    const names = Array.from({ length: BIG_COUNT }, (_, i) => `e10_${String(i).padStart(4, '0')}`)
    let items: unknown[] = []
    for (let round = 0; round < 8; round++) {
      const lst = await sftpPost(page, 'list', { path: `${TMP}/${BIG_DIR}` })
      expect(lst.status()).toBe(200)
      items = (((await lst.json()).items ?? []) as unknown[])
      const have = new Set(items.map((x: any) => x.name))
      const missing = names.filter((n) => !have.has(n))
      if (!missing.length) break
      for (let i = 0; i < missing.length; i += 8) {
        await Promise.all(
          missing.slice(i, i + 8).map((n) =>
            sftpPost(page, 'mkdir', { path: `${TMP}/${BIG_DIR}/${n}` }).catch(() => undefined)))
      }
    }
    expect(items.length).toBe(BIG_COUNT)

    await goSshView(page)
    await openFileBrowser(page, `${PREFIX}http`)
    await expect(page.locator('.sfb').first()).toBeVisible({ timeout: 15000 })
    await gotoTmp(page)
    const dirRow = page.locator('.sfb-row').filter({ hasText: BIG_DIR }).first()
    await expect(dirRow).toBeVisible({ timeout: 30000 })
    await dirRow.dblclick()
    await expect(page.locator('.sfb-row').first()).toBeVisible({ timeout: 60000 })
    await expect(page.locator('.sfb-row')).toHaveCount(BIG_COUNT, { timeout: 30000 })
    await expect(page.locator('.sfb-statusbar')).toContainText(`${BIG_COUNT} 项`)
    await page.screenshot({ path: 'screenshots/e10-big-dir.png' })
  })
})
