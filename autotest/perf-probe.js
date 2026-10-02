#!/usr/bin/env node
/* 传输优化 S7 性能探针（P1/P2/P5/P8/P9）— 新旧构建同口径，单会话测量
 * 用法: node perf-probe.js <label>        label=old|new
 * 产出: /tmp/perf-<label>.json
 *
 * 口径：
 *  - 连接 host=127.0.0.1 → md 侧 HTTP upload 500（host 走 query，前端传 form）→ 强制走信令/DC
 *    （旧=JSON+双重 base64，新=0x10/0x11 二进制帧）
 *  - 线字节 = 页面内 RTCDataChannel.send / WebSocket.send 实际字节数（addInitScript 打点）
 *  - 全程只登录一次、只建链一次，避免反复 page.goto 造成 ICE 重协商失败
 */
const { chromium } = require('playwright')
const fs = require('fs')
const { createHash } = require('crypto')

const BASE = process.env.BASE_URL || 'https://127.0.0.1:5588'
const LABEL = process.argv[2] || 'run'
const OUT = process.env.OUT || `/tmp/perf-${LABEL}.json`
const ADMIN_USER = 'admin'
const ADMIN_PASS = 'change_me_pass'
const SSH_PORT = 2222
const SSH_USER = 'sshuser'
const SSH_PASS = 'change_me_sshpass'
const HOST = '127.0.0.1'
const AGENT = 'local-agent'
const GW = 'local-gateway'
// 直连（''）或经网关（local-gateway）：本机环境网关中继腿 ICE 不稳（新旧栈均复现），默认直连保可测性
const GW_MODE = process.env.PERF_GATEWAY === '1' ? GW : ''
const CONN = `perf_${LABEL}`
const VCONN = `perfvnc_${LABEL}`
const TMP = '/tmp'
const SIZE_1M = 1024 * 1024
const SIZE_10M = 10 * 1024 * 1024
const REPS = Number(process.env.REPS || 3)
const LIST_REPS = Number(process.env.LIST_REPS || 5)
const ECHO_REPS = Number(process.env.ECHO_REPS || 20)
const VNC_MOVES = Number(process.env.VNC_MOVES || 10)
const PHASES = new Set((process.env.PHASES || 'list,upload,download,echo,vnc').split(','))

const md5 = (b) => createHash('md5').update(b).digest('hex')
const pct = (arr, p) => {
  if (!arr.length) return null
  const s = [...arr].sort((a, b) => a - b)
  return s[Math.min(s.length - 1, Math.ceil(p * s.length) - 1)]
}
const stat = (arr) => ({
  n: arr.length,
  min: arr.length ? Math.min(...arr) : null,
  p50: pct(arr, 0.5),
  p95: pct(arr, 0.95),
  max: arr.length ? Math.max(...arr) : null,
  avg: arr.length ? Math.round(arr.reduce((a, b) => a + b, 0) / arr.length) : null,
})

const INIT_COUNTER = `(() => {
  const now = () => Date.now()
  const st = { bytes: 0, frames: 0, t0: 0, t1: 0, rbytes: 0, rframes: 0, rt0: 0, rt1: 0, inlog: [], rb0: 0, pstats: [], hb: [] }
  try {
    let lastT = Date.now()
    setInterval(() => {
      const t = Date.now()
      const d = t - lastT
      lastT = t
      if (d > 150) { st.hb.push([t, d]); if (st.hb.length > 200) st.hb.shift() }
    }, 100)
  } catch (e) {}
  const len = (d) => {
    try {
      if (typeof d === 'string') return new TextEncoder().encode(d).length
      if (d instanceof ArrayBuffer) return d.byteLength
      if (ArrayBuffer.isView(d)) return d.byteLength
      if (typeof Blob !== 'undefined' && d instanceof Blob) return d.size
    } catch (e) {}
    return 0
  }
  const bumpOut = (d) => { const n = len(d); st.bytes += n; st.frames += 1; const t = now(); if (!st.t0) st.t0 = t; st.t1 = t }
  const bumpIn = (d) => { const n = len(d); st.rbytes += n; st.rframes += 1; const t = now(); if (!st.rt0) st.rt0 = t; st.rt1 = t; st.inlog.push([t - st.rb0, n]); if (st.inlog.length > 80) st.inlog.shift() }
  const wrapOnMsg = (proto) => {
    let owner = proto
    let d = null
    while (owner && !d) { d = Object.getOwnPropertyDescriptor(owner, 'onmessage'); if (!d) owner = Object.getPrototypeOf(owner) }
    if (!d || !d.set) return
    Object.defineProperty(proto, 'onmessage', {
      configurable: true,
      enumerable: d.enumerable !== false,
      get: d.get,
      set(v) {
        if (typeof v !== 'function') return d.set.call(this, v)
        const self = this
        return d.set.call(self, function (ev) {
          try { if (ev && ev.data !== undefined) bumpIn(ev.data) } catch (e) {}
          return v.apply(self, arguments)
        })
      },
    })
  }
  try {
    const od = RTCDataChannel.prototype.send
    RTCDataChannel.prototype.send = function (d) { bumpOut(d); return od.call(this, d) }
  } catch (e) {}
  try {
    const ow = WebSocket.prototype.send
    WebSocket.prototype.send = function (d) { bumpOut(d); return ow.call(this, d) }
  } catch (e) {}
  try { wrapOnMsg(RTCDataChannel.prototype) } catch (e) {}
  try { wrapOnMsg(WebSocket.prototype) } catch (e) {}
  try {
    const OPC = window.RTCPeerConnection
    if (OPC && !OPC.__wrapped) {
      const WP = class extends OPC {
        constructor (...args) {
          super(...args)
          try {
            const pc = this
            if (typeof pc.getStats === 'function' && !pc.__probeT) {
              pc.__probeT = setInterval(() => {
                try {
                  pc.getStats().then((r) => {
                    const o = { t: Date.now() }
                    r.forEach((s) => {
                      if (s.type === 'data-channel') { o.dcbR = s.bytesReceived; o.dcM = s.messagesReceived; o.dcbS = s.bytesSent }
                      else if (s.type === 'candidate-pair' && (s.state === 'succeeded' || s.nominated)) {
                        if (s.availableIncomingBitrate !== undefined) o.aiB = Math.round(s.availableIncomingBitrate)
                        if (s.availableOutgoingBitrate !== undefined) o.aoB = Math.round(s.availableOutgoingBitrate)
                        if (s.currentRoundTripTime !== undefined) o.rtt = Math.round(s.currentRoundTripTime * 1000)
                        if (s.bytesReceived !== undefined) o.cpR = s.bytesReceived
                      } else if (s.type === 'transport') {
                        if (s.bytesReceived !== undefined) o.trR = s.bytesReceived
                        if (s.bytesSent !== undefined) o.trS = s.bytesSent
                        if (s.packetsReceived !== undefined) o.pkR = s.packetsReceived
                        if (s.packetsLost !== undefined) o.pkL = s.packetsLost
                      }
                    })
                    if (st.pstats.length < 150) st.pstats.push(o)
                  }).catch(() => {})
                } catch (e) {}
              }, 1000)
            }
          } catch (e) {}
        }
      }
      window.RTCPeerConnection = WP
      WP.__wrapped = true
    }
  } catch (e) {}
  window.__wire = {
    read: () => ({ bytes: st.bytes, frames: st.frames, t0: st.t0, t1: st.t1, rbytes: st.rbytes, rframes: st.rframes, rt0: st.rt0, rt1: st.rt1, inlog: st.inlog.slice(), rb0: st.rb0, pstats: st.pstats.slice(), hb: st.hb.slice(), vis: (typeof document !== 'undefined' ? document.visibilityState : '?'), hidden: (typeof document !== 'undefined' ? document.hidden : null) }),
    reset: () => { st.bytes = 0; st.frames = 0; st.t0 = 0; st.t1 = 0; st.rbytes = 0; st.rframes = 0; st.rt0 = 0; st.rt1 = 0; st.inlog = []; st.pstats = []; st.hb = []; st.rb0 = now() },
  }
})()`

const R = {
  label: LABEL, ts: new Date().toISOString(), base: BASE,
  conn: CONN, host: HOST, gateway: GW,
  list_ms: [], upload: {}, download: [], vnc: {}, echo_ms: [], notes: [],
}

let browser, page, token
let pendingChooser = null
let expectingChooser = false

const pathBase = (p) => p.split('/').pop()

const PROGRESS = process.env.PERF_PROGRESS || '/tmp/perf-progress.log'
function step(msg) {
  try { fs.appendFileSync(PROGRESS, `${new Date().toISOString()} ${msg}\n`) } catch (e) {}
}
const SRC_DIR = '/tmp/perfsrc'
const RUNID = `-${Date.now().toString(36)}`
const madeFiles = []
function makeFile(size, tag) {
  const buf = Buffer.alloc(size)
  for (let i = 0; i < size; i++) buf[i] = (i * 131 + (i >> 7)) & 0xff
  const base = `perf_${LABEL}${RUNID}_${size}${tag}.bin`
  const local = `${SRC_DIR}/${base}`
  const remote = `${TMP}/${base}`
  fs.mkdirSync(SRC_DIR, { recursive: true })
  fs.writeFileSync(local, buf)
  madeFiles.push({ local, remote })
  return { local, remote, base, fp: local, md5: md5(buf), size }
}


async function api(method, p, data) {
  const opt = { headers: { Authorization: `Bearer ${token}` } }
  if (data !== undefined) opt.data = data
  return page.request[method](`${BASE}${p}`, opt)
}

async function uiLogin(p) {
  await p.goto(BASE + '/')
  await p.waitForLoadState('domcontentloaded')
  await p.waitForTimeout(500)
  const pw = p.locator('input[type="password"]').first()
  if (await pw.isVisible({ timeout: 3000 }).catch(() => false)) {
    const u = p.locator('input:not([type="password"]):not([type="checkbox"]):not([type="radio"])').first()
    await u.fill(ADMIN_USER)
    await pw.fill(ADMIN_PASS)
    await p.locator('button').filter({ hasText: /登\s*录/ }).first().click()
    await p.waitForTimeout(2000)
  }
}

async function goSshView(p) {
  const b = p.locator('.topmenu:has-text("远程管理")').first()
  if (await b.isVisible({ timeout: 3000 }).catch(() => false)) {
    await b.click()
    await p.waitForTimeout(800)
  }
}

async function expandGroup(p, groupType) {
  const label = groupType === 'ssh' ? 'SSH/FILE' : groupType === 'vnc' ? 'VNC' : 'RDP'
  const header = p.locator('.group-header', { hasText: label }).first()
  const arrow = header.locator('.group-arrow').first()
  const expanded = await arrow.evaluate((el) => el.classList.contains('expanded')).catch(() => false)
  if (!expanded) {
    await header.click()
    await p.waitForTimeout(500)
  }
}

async function clickConn(p, name) {
  let card = p.locator(`.ssh-card:has-text("${name}")`).first()
  if (!(await card.isVisible({ timeout: 2000 }).catch(() => false))) {
    await expandGroup(p, 'ssh')
    card = p.locator(`.ssh-card:has-text("${name}")`).first()
  }
  if (!(await card.isVisible({ timeout: 2000 }).catch(() => false))) {
    await expandGroup(p, 'vnc')
    card = p.locator(`.ssh-card:has-text("${name}")`).first()
  }
  if (!(await card.isVisible({ timeout: 8000 }).catch(() => false))) throw new Error(`card not visible: ${name}`)
  await card.locator('.ssh-card-main').click()
  await p.waitForTimeout(500)
  return card
}

/** 确保文件浏览器已打开且列表可用（返回 true=可用） */
async function ensureListUsable(p, attempts = 4) {
  for (let i = 0; i < attempts; i++) {
    step(`listUsable attempt ${i + 1}/${attempts}`)
    const row = p.locator('.sfb-row').first()
    const empty = p.locator('.sfb-empty')
    const okRow = await row.isVisible({ timeout: 6000 }).catch(() => false)
    if (okRow) return true
    const okEmpty = await empty.isVisible({ timeout: 1000 }).catch(() => false)
    if (okEmpty) {
      // 空目录：可能是 SFTP 超时，刷新重试
      const refresh = p.locator('.sfb-toolbtn').filter({ hasText: '刷新' }).first()
      if (await refresh.isVisible({ timeout: 2000 }).catch(() => false)) await refresh.click().catch(() => undefined)
      await p.waitForTimeout(2500)
      continue
    }
    await p.waitForTimeout(1500)
  }
  await p.screenshot({ path: `/tmp/perf-list-fail-${LABEL}.png` }).catch(() => undefined)
  return false
}

async function gotoTmp(p) {
  for (let attempt = 0; attempt < 6; attempt++) {
    const bc = await p.locator('.sfb-breadcrumb').textContent().catch(() => '')
    if (bc && bc.includes('tmp')) return
    const tmpRow = p.locator('.sfb-row').filter({ hasText: 'tmp' }).first()
    if (await tmpRow.isVisible({ timeout: 4000 }).catch(() => false)) {
      await tmpRow.dblclick()
      const ok = await p
        .waitForFunction(() => (document.querySelector('.sfb-breadcrumb')?.textContent || '').includes('tmp'), null, { timeout: 15000 })
        .then(() => true)
        .catch(() => false)
      if (ok) return
      continue
    }
    const refresh = p.locator('.sfb-toolbtn').filter({ hasText: '刷新' }).first()
    if (await refresh.isVisible({ timeout: 3000 }).catch(() => false)) await refresh.click().catch(() => undefined)
    await p.waitForTimeout(2500)
  }
  await p.screenshot({ path: `/tmp/perf-goto-tmp-fail-${LABEL}.png` }).catch(() => undefined)
  throw new Error('gotoTmp: /tmp row not found after retries')
}

async function wireRead(pg = page) {
  return pg.evaluate(() => (window.__wire ? window.__wire.read() : { bytes: -1, frames: -1 }))
}
async function wireReset(pg = page) {
  return pg.evaluate(() => (window.__wire ? window.__wire.reset() : undefined))
}

async function deleteIfPresent(name) {
  const row = page.locator('.sfb-row').filter({ hasText: name }).first()
  if (!(await row.isVisible({ timeout: 1500 }).catch(() => false))) return
  await row.click({ button: 'right' })
  const del = page.locator('.sfb-ctxmenu .ctx-item').filter({ hasText: '删除' }).first()
  if (await del.isVisible({ timeout: 2000 }).catch(() => false)) await del.click()
  await page.waitForTimeout(400)
  const ok = page.locator('.sfb-modal-actions .sfb-btn-ok, .modal-btn-primary').first()
  if (await ok.isVisible({ timeout: 800 }).catch(() => false)) await ok.click()
  await page.waitForTimeout(800)
}

async function clickUpload(file, tag) {
  let chooser = null
  expectingChooser = true
  try {
    for (let attempt = 0; attempt < 4 && !chooser; attempt++) {
      pendingChooser = null
      const clickErr = await page
        .locator('.sfb-toolbtn')
        .filter({ hasText: '上传' })
        .first()
        .click({ timeout: 8000 })
        .then(() => null)
        .catch((e) => String(e).split('\n')[0])
      const dl = Date.now() + 8000
      while (Date.now() < dl && !pendingChooser) await page.waitForTimeout(100)
      chooser = pendingChooser
      if (!chooser) {
        R.notes.push(`${tag}: filechooser attempt ${attempt + 1} missed${clickErr ? ` (click: ${clickErr})` : ''}`)
        await page.waitForTimeout(1200)
      }
    }
  } finally {
    expectingChooser = false
  }
  if (!chooser) {
    await page.screenshot({ path: `/tmp/perf-upload-fail-${LABEL}.png` }).catch(() => undefined)
    throw new Error(`${tag}: filechooser never fired`)
  }
  await chooser.setFiles(file.fp)
}

async function uploadOnce(file, tag) {
  await gotoTmp(page)
  if (await page.locator('.sfb-row').filter({ hasText: file.base }).first().isVisible({ timeout: 500 }).catch(() => false))
    R.notes.push(`${tag}: remote target already exists`)
  await wireReset()
  const t0 = Date.now()
  await clickUpload(file, tag)
  const wireDeadline = Date.now() + 20000
  let w = await wireRead()
  while (w.bytes === 0 && Date.now() < wireDeadline) {
    await page.waitForTimeout(150)
    w = await wireRead()
  }
  if (w.bytes === 0) R.notes.push(`${tag}: no uplink bytes observed`)
  const row = page.locator('.sfb-row').filter({ hasText: file.base }).first()
  await row.waitFor({ state: 'visible', timeout: 600000 })
  const expectTxt = file.size === SIZE_10M ? '10.0 MB' : '1.0 MB'
  const deadline = Date.now() + 60000
  let sizeTxt = ''
  while (Date.now() < deadline) {
    sizeTxt = (await row.locator('.col-size').textContent().catch(() => '')) || ''
    if (sizeTxt.includes(expectTxt)) break
    await page.waitForTimeout(200)
  }
  const ms = Date.now() - t0
  w = await wireRead()
  const ok = sizeTxt.includes(expectTxt)
  if (!ok) R.notes.push(`${tag}: size text "${sizeTxt.trim()}" != ${expectTxt}`)
  const wire_ms = w.t0 && w.t1 ? w.t1 - w.t0 : null
  return { ms, wire_ms, bytes: w.bytes, frames: w.frames, rbytes: w.rbytes, rframes: w.rframes, size_ok: ok }
}

async function downloadOnce(file, tag) {
  await gotoTmp(page)
  const row = page.locator('.sfb-row').filter({ hasText: file.base }).first()
  await row.waitFor({ state: 'visible', timeout: 60000 })
  await wireReset()
  const t0 = Date.now()
  const wait = page.waitForEvent('download', { timeout: 60000 })
  await row.click({ button: 'right' })
  await page.locator('.sfb-ctxmenu .ctx-item').filter({ hasText: '下载' }).first().click()
  const dl = await wait
  const save = `${TMP}/perf-dl-${LABEL}-${Date.now()}.bin`
  await dl.saveAs(save)
  const ms = Date.now() - t0
  const w = await wireRead()
  const buf = fs.readFileSync(save)
  const got = {
    ms, wire_ms: w.rt0 && w.rt1 ? w.rt1 - w.rt0 : null,
    bytes: w.rbytes, frames: w.rframes, up_bytes: w.bytes, up_frames: w.frames,
    size: buf.length,
    md5: md5(buf), ok: md5(buf) === file.md5 && buf.length === file.size,
  }
  fs.rmSync(save, { force: true })
  if (!got.ok) R.notes.push(`${tag}: downloaded md5/size mismatch`)
  return got
}

/** P8：列表首响应（点刷新 → 首行出现） */
async function measureListRefresh() {
  const out = []
  for (let i = 0; i < LIST_REPS; i++) {
    await gotoTmp(page)
    await wireReset()
    const t0 = Date.now()
    await page.locator('.sfb-toolbtn').filter({ hasText: '刷新' }).first().click()
    let ok = false
    const dl = Date.now() + 30000
    while (Date.now() < dl) {
      const n = await page.locator('.sfb-row').count().catch(() => 0)
      if (n > 0) { ok = true; break }
      await page.waitForTimeout(100)
    }
    out.push(Date.now() - t0)
    if (!ok) R.notes.push(`list#${i}: no rows within 30s`)
    await page.waitForTimeout(500)
  }
  return out
}

async function measureVnc() {
  await page.goto(BASE, { waitUntil: 'domcontentloaded' }).catch(() => undefined)
  await page.waitForTimeout(1500)
  await goSshView(page)
  await page.waitForTimeout(600)
  await expandGroup(page, 'vnc')
  let card = page.locator(`.ssh-card:has-text("${VCONN}")`).first()
  if (!(await card.isVisible({ timeout: 3000 }).catch(() => false))) await expandGroup(page, 'vnc')
  card = page.locator(`.ssh-card:has-text("${VCONN}")`).first()
  if (!(await card.isVisible({ timeout: 4000 }).catch(() => false))) {
    await page.reload({ waitUntil: 'domcontentloaded' }).catch(() => undefined)
    await page.waitForTimeout(1500)
    await goSshView(page)
    await expandGroup(page, 'vnc')
    card = page.locator(`.ssh-card:has-text("${VCONN}")`).first()
  }
  if (!(await card.isVisible({ timeout: 8000 }).catch(() => false))) { R.notes.push('vnc card not visible'); return { connected: false } }
  await card.locator('.ssh-card-main').click()
  await page.waitForTimeout(400)
  await page.locator('.ssh-sub-btn.type-vnc').first().click()
  const canvas = page.locator('.vnc-canvas-wrap canvas').first()
  const status = page.locator('.vnc-status').first()
  const dl = Date.now() + 60000
  let connected = false
  while (Date.now() < dl) {
    const vis = await canvas.isVisible().catch(() => false)
    const cls = (await status.getAttribute('class').catch(() => '')) || ''
    if (vis && /connected/.test(cls)) { connected = true; break }
    await page.waitForTimeout(500)
  }
  if (!connected) {
    const stTxt = ((await status.getAttribute('class').catch(() => '')) || '') + ' | ' + (await status.textContent().catch(() => '') || '')
    const wsTxt = await page.evaluate(() => {
      const el = document.querySelector('.vnc-status')
      return el ? el.outerHTML.slice(0, 300) : 'no-status-el'
    }).catch(() => 'eval-failed')
    R.notes.push(`vnc not connected; status=${stTxt}; html=${wsTxt}`)
    await page.screenshot({ path: `/tmp/perf-vnc-fail-${LABEL}.png` }).catch(() => undefined)
    return { connected: false }
  }
  await page.waitForTimeout(1500)
  const box = await canvas.boundingBox()
  await wireReset()
  const idle0 = Date.now()
  await page.waitForTimeout(1500)
  const idle = await wireRead()
  const idleMs = Date.now() - idle0
  await wireReset()
  const t0 = Date.now()
  if (box) {
    for (let i = 0; i < VNC_MOVES; i++) {
      await page.mouse.move(box.x + 40 + i * 12, box.y + 30 + i * 7)
      await page.waitForTimeout(120)
    }
    await page.mouse.click(box.x + Math.min(box.width - 10, 80), box.y + Math.min(box.height - 10, 60))
    await page.waitForTimeout(500)
  }
  const mvMs = Date.now() - t0
  const mv = await wireRead()
  await canvas.screenshot({ path: `/tmp/vnc-canvas-${LABEL}.png` }).catch(() => undefined)
  const baseRate = idleMs > 0 ? (idle.bytes * mvMs) / idleMs : 0
  const net = Math.max(0, mv.bytes - baseRate)
  return {
    connected: true, moves: VNC_MOVES, mv_ms: mvMs,
    mv_bytes: mv.bytes, mv_frames: mv.frames,
    mv_in_bytes: mv.rbytes, mv_in_frames: mv.rframes,
    idle_bytes: idle.bytes, idle_ms: idleMs,
    net_bytes: Math.round(net), per_move: Math.round(net / (VNC_MOVES + 1)),
  }
}

async function measureEcho() {
  await goSshView(page)
  // openSession 已展开过卡片：再点 card-main 会反向折叠，sub 按钮随之隐藏，
  // 且 fallback 的 page.locator('.ssh-sub-btn.type-ssh').first() 可能点到别的连接。
  const card = page.locator(`.ssh-card:has-text("${CONN}")`).first()
  const sub = card.locator('.ssh-sub-btn.type-ssh').first()
  if (!(await sub.isVisible({ timeout: 2000 }).catch(() => false))) {
    await clickConn(page, CONN)
    await page.waitForTimeout(600)
  }
  if (!(await sub.isVisible({ timeout: 4000 }).catch(() => false))) {
    R.notes.push('echo: ssh sub button not visible after expand')
    await page.screenshot({ path: `/tmp/perf-echo-nosub-${LABEL}.png` }).catch(() => undefined)
    return []
  }
  await sub.click({ timeout: 8000 }).catch((e) => R.notes.push(`echo: sub click ${String(e).split('\n')[0]}`))
  const ta = page.locator('textarea.xterm-helper-textarea').first()
  const hasTa = await ta.waitFor({ state: 'visible', timeout: 30000 }).then(() => true).catch(() => false)
  if (!hasTa) {
    R.notes.push(`echo: terminal textarea not visible (ta=${await page.locator('textarea.xterm-helper-textarea').count()} rows=${await page.locator('.xterm-rows').count()})`)
    await page.screenshot({ path: `/tmp/perf-echo-fail-${LABEL}.png` }).catch(() => undefined)
    return []
  }
  await ta.click()
  await page.waitForTimeout(800)
  const samples = []
  for (let i = 0; i < ECHO_REPS; i++) {
    const tokenStr = `Z${i}z`
    const t0 = Date.now()
    await ta.type(tokenStr, { delay: 0 })
    const dl = t0 + 8000
    let hit = null
    while (Date.now() < dl) {
      const txt = await page.locator('.xterm-rows').first().textContent().catch(() => '')
      if (txt && txt.includes(tokenStr)) { hit = Date.now() - t0; break }
      await page.waitForTimeout(15)
    }
    if (hit != null) samples.push(hit)
    await ta.press('Enter').catch(() => {})
    await page.waitForTimeout(80)
  }
  return samples
}

async function ensureConn(name, extra) {
  const listResp = await api('get', '/api/ssh')
  const listData = await listResp.json()
  const list = Array.isArray(listData) ? listData : (listData.connections || [])
  const found = list.find((c) => c.name === name)
  if (found) return found
  const resp = await api('post', '/api/ssh/add', {
    name, host: HOST, port: SSH_PORT, username: SSH_USER, auth_type: 'password',
    password: SSH_PASS, connection_mode: 'agent', agent_id: AGENT, gateway_id: GW_MODE,
    connection_type: 'ssh', vnc_port: 5900, vnc_password: 'vncPass123', ...extra,
  })
  return resp.json()
}


async function wireHealthy(pg = page) {
  try {
    const refresh = pg.locator('.sfb-toolbtn').filter({ hasText: '刷新' }).first()
    if (!(await refresh.isVisible({ timeout: 4000 }).catch(() => false))) return false
    await wireReset(pg)
    await refresh.click().catch(() => undefined)
    const dl = Date.now() + 12000
    while (Date.now() < dl) {
      const w = await wireRead(pg).catch(() => ({ rbytes: 0 }))
      if (w.rbytes > 0) return true
      await pg.waitForTimeout(300)
    }
    return false
  } catch (e) { return false }
}

/** S4 connect 相位：VNC 连接计时（在独立 page 上与 SSH 并发） */
async function connectVncOn(p) {
  let t0 = Date.now()
  try {
    await p.goto(BASE, { waitUntil: 'domcontentloaded' }).catch(() => undefined)
    await p.waitForTimeout(1500)
    await goSshView(p)
    await p.waitForTimeout(600)
    await expandGroup(p, 'vnc')
    let card = p.locator(`.ssh-card:has-text("${VCONN}")`).first()
    if (!(await card.isVisible({ timeout: 5000 }).catch(() => false))) {
      await expandGroup(p, 'vnc')
      card = p.locator(`.ssh-card:has-text("${VCONN}")`).first()
    }
    if (!(await card.isVisible({ timeout: 8000 }).catch(() => false))) return { ok: false, ms: Date.now() - t0, err: 'vnc card not visible' }
    t0 = Date.now()
    await card.locator('.ssh-card-main').click()
    await p.waitForTimeout(400)
    await p.locator('.ssh-sub-btn.type-vnc').first().click({ timeout: 8000 })
    const canvas = p.locator('.vnc-canvas-wrap canvas').first()
    const status = p.locator('.vnc-status').first()
    const dl = Date.now() + 60000
    while (Date.now() < dl) {
      const vis = await canvas.isVisible().catch(() => false)
      const cls = (await status.getAttribute('class').catch(() => '')) || ''
      if (vis && /connected/.test(cls)) return { ok: true, ms: Date.now() - t0 }
      await p.waitForTimeout(200)
    }
    const stTxt = ((await status.getAttribute('class').catch(() => '')) || '') + '|' + ((await status.textContent().catch(() => '')) || '')
    return { ok: false, ms: Date.now() - t0, err: `vnc timeout: ${stTxt.slice(0, 120)}` }
  } catch (e) {
    return { ok: false, ms: Date.now() - t0, err: String(e).split('\n')[0] }
  }
}

/** S4 connect 相位：SSH(FILE) 连接计时（主 page，与 VNC 并发） */
async function connectSshOn(p) {
  let t0 = Date.now()
  try {
    await goSshView(p)
    await clickConn(p, CONN)
    t0 = Date.now()
    await p.locator('.ssh-sub-btn.type-file').first().click({ timeout: 8000 })
    await p.locator('.sfb').first().waitFor({ state: 'visible', timeout: 30000 })
    const usable = await ensureListUsable(p, 2)
    if (!usable) return { ok: false, ms: Date.now() - t0, err: 'list unusable' }
    const healthy = await wireHealthy(p)
    return { ok: healthy, ms: Date.now() - t0, err: healthy ? undefined : 'dc not ready' }
  } catch (e) {
    return { ok: false, ms: Date.now() - t0, err: String(e).split('\n')[0] }
  }
}

/** 打开 FILE 面板并确认 DC 有入向流量（ICE/DC 就绪），失败返回 false */
async function openSession(tag) {
  for (let attempt = 0; attempt < 4; attempt++) {
    try {
      await goSshView(page)
      await clickConn(page, CONN)
      await page.locator('.ssh-sub-btn.type-file').first().click()
      await page.locator('.sfb').first().waitFor({ state: 'visible', timeout: 30000 })
      const usable = await ensureListUsable(page, 6)
      if (usable) {
        if (await wireHealthy()) return true
        R.notes.push(`${tag}: DC not ready (no inbound), retry ${attempt + 1}`)
      } else {
        R.notes.push(`${tag}: list unusable, retry ${attempt + 1}`)
      }
    } catch (e) {
      R.notes.push(`${tag}: openSession error ${String(e).split('\n')[0]}, retry ${attempt + 1}`)
    }
    await page.reload().catch(() => undefined)
    await page.waitForLoadState('domcontentloaded').catch(() => undefined)
    await page.waitForTimeout(3000)
  }
  return false
}

async function cleanup() {
  for (const f of madeFiles) {
    await page.request
      .post(`${BASE}/api/sftp-client/delete`, {
        headers: { Authorization: `Bearer ${token}` },
        data: { host: HOST, port: SSH_PORT, username: SSH_USER, password: SSH_PASS, path: f.remote },
      })
      .catch(() => undefined)
    fs.rmSync(f.local, { force: true })
  }
  const listResp = await api('get', '/api/ssh')
  const conns = await listResp.json().catch(() => [])
  const arr = Array.isArray(conns) ? conns : conns.data || []
  // connect 相位：连接由 fleet 脚本串行预建/清理（并发删会竞态 ssh_connections.json）
  if (PHASES.has('connect')) return
  for (const c of arr) {
    if (c.name && (c.name === CONN || c.name === VCONN)) {
      await api('post', '/api/ssh/delete', { id: c.id }).catch(() => undefined)
    }
  }
}

;(async () => {
  browser = await chromium.launch({ headless: true })
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true, acceptDownloads: true, viewport: { width: 1280, height: 720 } })
  await ctx.addInitScript(INIT_COUNTER)
  page = await ctx.newPage()
  page.on('pageerror', (e) => R.notes.push(`pageerror: ${e.message}`))
  // 关键：在任何点击前就挂 filechooser 监听（waitForEvent 与 input.click() 存在 CDP 拦截就绪竞态，
  // 未就绪时对话框会挂起并阻塞后续 chooser → 表现为 filechooser 永不触发）
  page.on('filechooser', (c) => {
    if (!expectingChooser) { c.setFiles([]).catch(() => undefined); return }
    pendingChooser = c
  })

  await uiLogin(page)
  const lr = await page.request.post(`${BASE}/api/auth/login`, { data: { username: ADMIN_USER, password: ADMIN_PASS } })
  token = (await lr.json()).token
  await ensureConn(CONN)
  await ensureConn(VCONN, { connection_type: 'vnc' })

  if (PHASES.has('connect')) {
    // ── S4 并发建连：同页 SSH(FILE) 与独立页 VNC 并发打开，各自计时 ──
    step('connect phase begin')
    const p2 = await ctx.newPage()
    p2.on('pageerror', (e) => R.notes.push(`p2 pageerror: ${e.message}`))
    const tAll = Date.now()
    const [ssh, vnc] = await Promise.all([connectSshOn(page), connectVncOn(p2)])
    R.connect = { ssh, vnc, total_ms: Date.now() - tAll }
    await p2.close().catch(() => undefined)
    step(`connect phase done ${JSON.stringify(R.connect)}`)
  }

  if (PHASES.has('file')) {
  const f1 = Array.from({ length: REPS }, (_, i) => makeFile(SIZE_1M, `_r${i}`))
  const f10 = Array.from({ length: REPS }, (_, i) => makeFile(SIZE_10M, `_r${i}`))
  // ── 单次建链：打开 FILE 面板并确认 DC 就绪（未就绪则 reload 重建）──
  step('open session')
  if (!(await openSession('session'))) throw new Error('file browser session not ready')

  // P8: 列表首响应
  R.list_ms = await measureListRefresh()
  R.p8_list = stat(R.list_ms)
  step('p8 phase start')

  // P1/P2: 上传
  R.upload['1MB'] = []
  step('upload phase start')
  for (let i = 0; i < REPS; i++) { step(`upload-1M-${i} begin`); const r = await uploadOnce(f1[i], `upload-1M-${i}`); step(`upload-1M-${i} done ${JSON.stringify(r)}`); R.upload['1MB'].push(r) }
  R.upload['10MB'] = []
  step('upload 10MB start')
  for (let i = 0; i < REPS; i++) { step(`upload-10M-${i} begin`); const r = await uploadOnce(f10[i], `upload-10M-${i}`); step(`upload-10M-${i} done ${JSON.stringify(r)}`); R.upload['10MB'].push(r) }
  R.p1_upload_1mb = {
    bytes: R.upload['1MB'].map((x) => x.bytes),
    frames: R.upload['1MB'].map((x) => x.frames),
    ms: R.upload['1MB'].map((x) => x.ms),
    wire_ms: R.upload['1MB'].map((x) => x.wire_ms),
  }
  R.p2_upload_10mb_ms = R.upload['10MB'].map((x) => x.ms)
  R.p2_upload_10mb_wire_ms = R.upload['10MB'].map((x) => x.wire_ms)
  R.p2_upload_10mb_bytes = R.upload['10MB'].map((x) => x.bytes)

  // P2: 下载 10MB（下载走入向大包，先确认 DC 健康）
  step('download phase start')
  if (!(await wireHealthy())) {
    R.notes.push('pre-download: DC unhealthy, rebuilding session')
    if (!(await openSession('pre-download'))) throw new Error('session rebuild failed before download')
  }
  for (let i = 0; i < REPS; i++) {
    step(`download-10M-${i} begin`)
    try {
      const r = await downloadOnce(f10[i], `download-10M-${i}`)
      step(`download-10M-${i} done ${JSON.stringify(r)}`)
      R.download.push(r)
    } catch (e) {
      const msg = String(e).split('\n')[0]
      const w = await wireRead().catch(() => null)
      R.notes.push(`download-10M-${i}: ${msg}; wire=${JSON.stringify(w)}`)
      step(`download-10M-${i} FAILED ${msg} wire=${JSON.stringify(w)}`)
      await page.screenshot({ path: `/tmp/perf-dl-fail-${LABEL}-${i}.png` }).catch(() => undefined)
      if (!(await wireHealthy())) { R.notes.push(`download-10M-${i}: rebuilding session`); await openSession('dl-retry') }
    }
  }
  R.p2_download_10mb_ms = R.download.map((x) => x.ms)
  R.p2_download_10mb_wire_ms = R.download.map((x) => x.wire_ms)
  R.p2_download_10mb_bytes = R.download.map((x) => x.bytes)

  // P9: 终端回显
  step('echo phase start')
  if (!(await wireHealthy())) {
    R.notes.push('pre-echo: DC unhealthy, rebuilding session')
    await openSession('pre-echo')
  }
  R.echo_ms = await measureEcho().catch((e) => { R.notes.push(`echo: ${String(e).split('\n')[0]}`); return [] })
  R.p9_echo = stat(R.echo_ms)
  }

  if (PHASES.has('vnc')) {
  // P5: VNC 上行帧体积
  step('vnc phase begin')
  R.vnc = await measureVnc().catch((e) => { R.notes.push(`vnc: ${String(e).split('\n')[0]}`); return {} })
  step('vnc phase start')
  }

  await cleanup()
  fs.writeFileSync(OUT, JSON.stringify(R, null, 2))
  console.log(JSON.stringify({
    label: LABEL,
    p8_list: R.p8_list,
    connect: R.connect,
    p1_bytes_1mb: R.p1_upload_1mb?.bytes,
    p1_frames_1mb: R.p1_upload_1mb?.frames,
    p1_ms_1mb: R.p1_upload_1mb?.ms,
    p1_wire_ms_1mb: R.p1_upload_1mb?.wire_ms,
    p2_upload_10mb: { ms: R.p2_upload_10mb_ms, wire_ms: R.p2_upload_10mb_wire_ms, bytes: R.p2_upload_10mb_bytes },
    p2_download_10mb: { ms: R.p2_download_10mb_ms, wire_ms: R.p2_download_10mb_wire_ms, bytes: R.p2_download_10mb_bytes },
    p5_vnc: R.vnc,
    p9_echo: R.p9_echo,
    notes: R.notes,
  }, null, 2))
  await browser.close()
})().catch(async (e) => {
  console.error('PROBE FAILED:', e)
  try { await page.screenshot({ path: `/tmp/perf-fail-${LABEL}.png` }) } catch (_) {}
  try { fs.writeFileSync(OUT, JSON.stringify(R, null, 2)) } catch (_) {}
  process.exit(1)
})
