import { test, expect } from '@playwright/test'
import { loginAsAdmin, apiLogin } from './helpers'

async function getTree(page: import('@playwright/test').Page): Promise<any[]> {
  const token = await apiLogin(page)
  const resp = await page.request.get('/api/docs/tree', {
    headers: { Authorization: `Bearer ${token}` },
  })
  expect(resp.ok()).toBeTruthy()
  return await resp.json()
}

async function getFirstFile(page: import('@playwright/test').Page): Promise<string> {
  const tree = await getTree(page)
  const walk = (nodes: any[]): string | null => {
    for (const n of nodes) {
      if (n.type === 'file') return n.name
      if (n.children) {
        const f = walk(n.children)
        if (f) return f
      }
    }
    return null
  }
  const name = walk(tree)
  if (!name) throw new Error('no markdown files found in tree')
  return name
}

test.describe('Document Tree Improvements', () => {
  test.beforeEach(async ({ page }) => {
    await loginAsAdmin(page)
    await page.click('text=文档管理')
    await page.waitForSelector('.dm-sidebar')
  })

  test('sidebar can be collapsed and expanded', async ({ page }) => {
    const sidebar = page.locator('.dm-sidebar')
    await expect(sidebar).not.toHaveClass(/collapsed/)

    await page.click('.collapse-btn')
    await expect(sidebar).toHaveClass(/collapsed/)

    await page.click('.collapse-btn')
    await expect(sidebar).not.toHaveClass(/collapsed/)
  })

  test('file tree filter works', async ({ page }) => {
    const filter = page.locator('.dm-tree-filter')
    await expect(filter).toBeVisible()

    await filter.fill('首页')
    await page.waitForTimeout(300)

    const treeItems = page.locator('.dm-tree-row')
    const count = await treeItems.count()
    expect(count).toBeGreaterThan(0)
  })

  test('recent files section exists', async ({ page }) => {
    const firstName = await getFirstFile(page)

    await page.locator('.dm-tree-row', { hasText: firstName }).first().click()
    await page.waitForTimeout(500)

    const recent = page.locator('.dm-recent')
    await expect(recent).toBeVisible()
    await expect(recent).toContainText(firstName)
  })

  test('TOC toggle button appears after opening a doc', async ({ page }) => {
    const firstName = await getFirstFile(page)

    await page.locator('.dm-tree-row', { hasText: firstName }).first().click()
    await page.waitForTimeout(1000)

    const activeRow = page.locator('.dm-tree-row.active')
    await expect(activeRow).toHaveCount(1)

    const tocToggle = page.locator('.dm-toc-toggle')
    await expect(tocToggle).toBeVisible()

    await tocToggle.click()
    const toc = page.locator('.dm-toc')
    await expect(toc).toBeVisible()
  })

  test('non-document files are filtered out', async ({ page }) => {
    const tree = await getTree(page)

    const checkNode = (nodes: any[]): boolean => {
      for (const n of nodes) {
        if (n.type === 'file' && !n.name.endsWith('.md') && !n.name.endsWith('.txt')) {
          return false
        }
        if (n.children && !checkNode(n.children)) return false
      }
      return true
    }
    expect(checkNode(tree)).toBe(true)
  })
})
