import { test, expect } from '@playwright/test'
import { loginAsAdmin } from './helpers'

test.describe('文档管理', () => {
  test('页面加载和文件树', async ({ page }) => {
    await loginAsAdmin(page)
    await expect(page.locator('.brand')).toContainText('泡鱼终端')
    await expect(page.locator('.topmenu').first()).toBeVisible()

    await page.click('.topmenu:has-text("文档管理")')
    await expect(page.locator('.dm-sidebar')).toBeVisible()
    await expect(page.locator('.dm-tree')).toBeVisible()
  })

  test('点击文件后预览区域有内容', async ({ page }) => {
    await loginAsAdmin(page)
    await page.click('.topmenu:has-text("文档管理")')
    await page.waitForTimeout(500)

    const fileItem = page.locator('.dm-tree-item').filter({ hasText: '.md' }).first()
    await expect(fileItem).toBeVisible()
    await fileItem.click()
    await page.waitForTimeout(800)

    await expect(page.locator('.dm-breadcrumb')).toBeVisible()
    const htmlLen = await page.evaluate(() => {
      const el = document.querySelector('.dm-browse .markdown-body')
      return el ? el.innerHTML.length : 0
    })
    expect(htmlLen).toBeGreaterThan(0)
  })

  test('编辑模式切换', async ({ page }) => {
    await loginAsAdmin(page)
    await page.click('.topmenu:has-text("文档管理")')
    await page.waitForTimeout(500)

    const fileItem = page.locator('.dm-tree-item').filter({ hasText: '.md' }).first()
    await expect(fileItem).toBeVisible()
    await fileItem.click()
    await page.waitForTimeout(800)

    await page.locator('.tool-btn:has-text("编辑")').click()
    await page.waitForTimeout(500)
    await expect(page.locator('.dm-editor-full')).toBeVisible()

    await page.locator('.tool-btn:has-text("分屏")').click()
    await page.waitForTimeout(500)
    await expect(page.locator('.dm-edit-split')).toBeVisible()
  })

  test('格式化工具栏存在', async ({ page }) => {
    await loginAsAdmin(page)
    await page.click('.topmenu:has-text("文档管理")')
    await page.waitForTimeout(500)

    const fileItem = page.locator('.dm-tree-item').filter({ hasText: '.md' }).first()
    await expect(fileItem).toBeVisible()
    await fileItem.click()
    await page.waitForTimeout(800)

    await page.locator('.tool-btn:has-text("编辑")').click()
    await page.waitForTimeout(500)
    await expect(page.locator('.dm-format-bar')).toBeVisible()
    await expect(page.locator('.fmt-btn').first()).toBeVisible()
  })
})
