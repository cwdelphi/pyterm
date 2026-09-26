import { test, expect, type Page } from '@playwright/test'
import { loginAsAdmin } from './helpers'

async function gotoNav(page: Page, label: string) {
  await loginAsAdmin(page)
  await page.locator('nav .topmenu', { hasText: label }).click()
  await page.waitForTimeout(1000)
}

test('UI-V1: 远程管理页面加载', async ({ page }) => {
  const errors: string[] = []
  page.on('pageerror', e => errors.push(e.message))
  await gotoNav(page, '远程管理')
  await expect(page.locator('.ssh-wrap')).toBeVisible()
  expect(errors).toEqual([])
})

test('UI-V2: 系统管理页面加载', async ({ page }) => {
  const errors: string[] = []
  page.on('pageerror', e => errors.push(e.message))
  await gotoNav(page, '系统管理')
  await expect(page.locator('.admin-wrap')).toBeVisible()
  await expect(page.locator('.table-header').first()).toBeVisible()
  expect(errors).toEqual([])
})

test('UI-V3: 文档管理页面加载', async ({ page }) => {
  const errors: string[] = []
  page.on('pageerror', e => errors.push(e.message))
  await gotoNav(page, '文档管理')
  await expect(page.locator('.dm-wrap')).toBeVisible()
  expect(errors).toEqual([])
})

test('UI-V4: 网址管理页面加载', async ({ page }) => {
  const errors: string[] = []
  page.on('pageerror', e => errors.push(e.message))
  await gotoNav(page, '网址管理')
  await expect(page.locator('.lm-wrap')).toBeVisible()
  expect(errors).toEqual([])
})

test('UI-V5: 主题切换无报错', async ({ page }) => {
  const errors: string[] = []
  page.on('pageerror', e => errors.push(e.message))
  await loginAsAdmin(page)
  await page.locator('.theme-box .icon-btn').click()
  await page.waitForTimeout(300)
  await page.locator('.theme-pop .chip', { hasText: '暗色' }).click()
  await page.waitForTimeout(500)
  await page.locator('.theme-pop .chip', { hasText: '亮色' }).click()
  await page.waitForTimeout(500)
  expect(errors).toEqual([])
})
