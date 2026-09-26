import { test, expect } from '@playwright/test'
import * as fs from 'fs'
import * as path from 'path'

const ARTIFACT_DIR = path.resolve(__dirname, '../../artifacts')
const RESULTS_FILE = path.join(ARTIFACT_DIR, 'remote-cases.json')
const RESULTS_LOG = path.join(ARTIFACT_DIR, 'remote-cases.jsonl')
const META_FILE = path.join(ARTIFACT_DIR, 'remote-timeline-meta.json')
const REPORT_FILE = path.resolve(__dirname, '../../../docs/远程管理测试报告_v1.0.md')

interface CaseResult {
  caseId: string
  connId: string
  name: string
  connType: string
  agentId: string
  gatewayId: string
  expectedPathMode: string
  status: 'passed' | 'failed' | 'skipped'
  error?: string
  roomId?: string
  pathMode?: string
  durationTotal?: number
  durationWs?: number
  durationSignal?: number
  durationIce?: number
  durationDc?: number
  durationData?: number
  agentConnectMs?: number
  success?: number
  errorStage?: string
  errorMsg?: string
  screenshot?: string
  steps?: number
  totalSteps?: number
}

function fmt(n?: number | null): string {
  if (n === undefined || n === null || Number.isNaN(n)) return '-'
  return typeof n === 'number' ? (Math.round(n * 10) / 10).toString() : String(n)
}

function statusEmoji(s: string): string {
  if (s === 'passed') return '✅'
  if (s === 'skipped') return '⏭️'
  return '❌'
}

function loadResults(): CaseResult[] {
  const map = new Map<string, CaseResult>()
  if (fs.existsSync(RESULTS_LOG)) {
    for (const line of fs.readFileSync(RESULTS_LOG, 'utf8').split('\n')) {
      if (!line.trim()) continue
      try {
        const r = JSON.parse(line) as CaseResult
        if (r && r.caseId) map.set(r.caseId, r)
      } catch {}
    }
  }
  try {
    if (fs.existsSync(RESULTS_FILE)) {
      const fromJson = JSON.parse(fs.readFileSync(RESULTS_FILE, 'utf8') || '[]')
      if (Array.isArray(fromJson)) {
        for (const r of fromJson as CaseResult[]) {
          if (r && r.caseId && !map.has(r.caseId)) map.set(r.caseId, r)
        }
      }
    }
  } catch {}
  const list = Array.from(map.values())
  if (list.length) {
    try { fs.writeFileSync(RESULTS_FILE, JSON.stringify(list, null, 2)) } catch {}
  }
  return list
}

test.describe('RC-REP 生成测试报告', () => {
  test('生成 docs/远程管理测试报告_v1.0.md', async ({ page }) => {
    expect(
      fs.existsSync(RESULTS_FILE) || fs.existsSync(RESULTS_LOG),
      `missing ${RESULTS_FILE} / ${RESULTS_LOG}`,
    ).toBeTruthy()
    const results = loadResults()
    console.log(`[RC-REP] loaded cases=${results.length} ids=${results.map(r => r.caseId).join(',')}`)
    expect(results.length).toBeGreaterThan(0)

    let meta: any = { stats: {}, recent: [], failures: [] }
    if (fs.existsSync(META_FILE)) {
      meta = JSON.parse(fs.readFileSync(META_FILE, 'utf8'))
    }

    const passed = results.filter(r => r.status === 'passed')
    const failed = results.filter(r => r.status === 'failed')
    const skipped = results.filter(r => r.status === 'skipped')
    const connCases = results.filter(r => ['ssh', 'vnc', 'sftp'].includes(r.connType))

    const byPath: Record<string, CaseResult[]> = {}
    for (const r of results) {
      if (!r.pathMode && !r.expectedPathMode) continue
      const key = r.pathMode || r.expectedPathMode || '-'
      byPath[key] = byPath[key] || []
      byPath[key].push(r)
    }

    const now = new Date().toISOString().replace('T', ' ').slice(0, 19)
    const stats = meta.stats || {}
    const lines: string[] = []
    lines.push('# 远程管理测试报告 v1.0')
    lines.push('')
    lines.push(`> 生成时间：${now} UTC+0 环境时间 | 框架：Playwright \`--project=remote\``)
    lines.push('')
    lines.push('## 1. 摘要')
    lines.push('')
    lines.push('| 指标 | 值 |')
    lines.push('|------|----|')
    lines.push(`| 用例总数 | ${results.length} |`)
    lines.push(`| 通过 | ${passed.length} |`)
    lines.push(`| 失败 | ${failed.length} |`)
    lines.push(`| 跳过 | ${skipped.length} |`)
    lines.push(`| 通过率 | ${results.length ? ((passed.length / results.length) * 100).toFixed(1) : '0'}% |`)
    lines.push(`| Timeline 总连接 | ${stats.total ?? '-'} |`)
    lines.push(`| Timeline 成功率 | ${stats.success_rate != null ? stats.success_rate + '%' : '-'} |`)
    lines.push(`| Timeline 平均耗时 | ${stats.avg_total != null ? stats.avg_total + 'ms' : '-'} |`)
    lines.push('')

    lines.push('## 2. 逐连接结果')
    lines.push('')
    lines.push('| 编号 | 名称 | 类型 | Agent | 网关 | 期望路径 | 实测路径 | 状态 | 耗时(ms) | 截图 | 备注 |')
    lines.push('|------|------|------|-------|------|----------|----------|------|----------|------|------|')
    const ordered = [...results].sort((a, b) => a.caseId.localeCompare(b.caseId, 'en', { numeric: true }))
    for (const r of ordered) {
      const note = r.status === 'failed'
        ? (r.errorMsg || r.error || r.errorStage || '').replace(/\|/g, '/').slice(0, 80)
        : (r.status === 'skipped' ? (r.error || 'skipped').replace(/\|/g, '/') : '')
      lines.push(
        `| ${r.caseId} | ${r.name} | ${r.connType} | ${r.agentId || '-'} | ${r.gatewayId || '-'} | ${r.expectedPathMode} | ${r.pathMode || '-'} | ${statusEmoji(r.status)} ${r.status} | ${fmt(r.durationTotal)} | ${r.screenshot ? `\`${r.screenshot}\`` : '-'} | ${note} |`,
      )
    }
    lines.push('')

    lines.push('## 3. path_mode 聚合')
    lines.push('')
    lines.push('| path_mode | 用例数 | 通过 | 失败/跳过 | 平均耗时(ms) |')
    lines.push('|-----------|--------|------|-----------|--------------|')
    for (const [mode, list] of Object.entries(byPath)) {
      const p = list.filter(x => x.status === 'passed')
      const bad = list.filter(x => x.status !== 'passed').length
      const durs = p.map(x => x.durationTotal).filter((x): x is number => typeof x === 'number')
      const avg = durs.length ? (durs.reduce((a, b) => a + b, 0) / durs.length) : null
      lines.push(`| ${mode} | ${list.length} | ${p.length} | ${bad} | ${fmt(avg)} |`)
    }
    lines.push('')

    lines.push('## 4. 分阶段耗时（客户端 timeline 上报）')
    lines.push('')
    lines.push('| 编号 | 名称 | WS | 信令 | ICE | DC | 首数据 | Agent全链路 |')
    lines.push('|------|------|----|------|-----|----|--------|-------------|')
    for (const r of ordered.filter(x => x.durationWs !== undefined || x.durationTotal !== undefined)) {
      if (!['ssh', 'vnc'].includes(r.connType)) continue
      lines.push(
        `| ${r.caseId} | ${r.name} | ${fmt(r.durationWs)} | ${fmt(r.durationSignal)} | ${fmt(r.durationIce)} | ${fmt(r.durationDc)} | ${fmt(r.durationData)} | ${fmt(r.agentConnectMs)} |`,
      )
    }
    lines.push('')

    lines.push('## 5. 失败与跳过明细')
    lines.push('')
    const bad = results.filter(r => r.status !== 'passed')
    if (!bad.length) {
      lines.push('_无失败/跳过用例。_')
    } else {
      lines.push('| 编号 | 名称 | 状态 | 错误 | room_id |')
      lines.push('|------|------|------|------|---------|')
      for (const r of bad) {
        const err = (r.errorMsg || r.error || '').replace(/\|/g, '/').replace(/\n/g, ' ').slice(0, 160)
        lines.push(`| ${r.caseId} | ${r.name} | ${r.status} | ${err || '-'} | ${r.roomId || '-'} |`)
      }
    }
    lines.push('')

    lines.push('## 6. Timeline 服务端交叉校验（近 50 条）')
    lines.push('')
    lines.push('| room_id | 名称 | 类型 | 路径 | 成功 | 耗时(ms) |')
    lines.push('|---------|------|------|------|------|----------|')
    for (const r of (meta.recent || []).slice(0, 20)) {
      lines.push(
        `| \`${r.room_id}\` | ${r.conn_name || '-'} | ${r.conn_type || '-'} | ${r.path_mode || '-'} | ${r.success ? '✅' : '❌'} | ${fmt(r.duration_total)} |`,
      )
    }
    lines.push('')

    lines.push('## 7. 截图索引')
    lines.push('')
    lines.push('| 编号 | 路径 |')
    lines.push('|------|------|')
    for (const r of ordered.filter(x => x.screenshot)) {
      lines.push(`| ${r.caseId} | ${r.screenshot} |`)
    }
    lines.push('')

    lines.push('## 8. 环境与执行')
    lines.push('')
    lines.push('- base: `https://127.0.0.1:5588`（admin）')
    lines.push('- 命令: `cd autotest && npx playwright test --project=remote`')
    lines.push('- HTML 报告: `npm run report`（`playwright-report/`）')
    lines.push('- JSON: `artifacts/results.json`')
    lines.push('- 用例明细: `autotest/artifacts/remote-cases.json` + `remote-cases.jsonl`')
    lines.push('')
    lines.push('---')
    lines.push('')
    lines.push('| 版本 | 日期 | 说明 |')
    lines.push('|------|------|------|')
    lines.push(`| v1.0 | ${now.slice(0, 10)} | 首次全量 9 连接 + SFTP/诊断/Timeline 报告 |`)
    lines.push('')

    fs.mkdirSync(path.dirname(REPORT_FILE), { recursive: true })
    fs.writeFileSync(REPORT_FILE, lines.join('\n'), 'utf8')
    console.log(`[RC-REP] wrote ${REPORT_FILE} cases=${results.length} pass=${passed.length} fail=${failed.length} skip=${skipped.length}`)
    expect(fs.existsSync(REPORT_FILE)).toBeTruthy()
    expect(passed.length + skipped.length).toBeGreaterThan(0)
  })
})
