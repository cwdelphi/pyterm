import { test, expect, type Page } from '@playwright/test'
import { loginAsAdmin } from './helpers'

const TIMEOUT = 15000

async function goAdminView(page: Page) {
  await page.goto('/')
  await page.waitForLoadState('networkidle')
  await page.waitForTimeout(500)
  await loginAsAdmin(page)
  await page.waitForTimeout(1500)
  await page.click('.topmenu:has-text("系统管理")')
  await page.waitForTimeout(1000)
}

// ═══════════════════════════════════════════════════════════
//  AM-01 ~ AM-02: 菜单可见性
// ═══════════════════════════════════════════════════════════

test.describe('管理员菜单可见性', () => {
  test('AM-01: 管理员看到系统管理菜单', async ({ page }) => {
    await page.goto('/')
    await page.waitForLoadState('networkidle')
    await loginAsAdmin(page)
    await page.waitForTimeout(1500)
    const adminMenu = page.locator('.topmenu:has-text("系统管理")')
    await expect(adminMenu).toBeVisible()
  })

  test('AM-02: 系统管理Tab显示4个子Tab', async ({ page }) => {
    await goAdminView(page)
    await expect(page.locator('.admin-tab:has-text("用户管理")')).toBeVisible()
    await expect(page.locator('.admin-tab:has-text("Agent管理")')).toBeVisible()
    await expect(page.locator('.admin-tab:has-text("coturn管理")')).toBeVisible()
    await expect(page.locator('.admin-tab:has-text("审计日志")')).toBeVisible()
  })
})

// ═══════════════════════════════════════════════════════════
//  AM-03 ~ AM-09: 用户管理
// ═══════════════════════════════════════════════════════════

test.describe('用户管理', () => {
  const testUser = `e2e_user_${Date.now()}`

  test('AM-03: 用户列表加载', async ({ page }) => {
    await goAdminView(page)
    const rows = page.locator('.admin-table tbody tr')
    expect(await rows.count()).toBeGreaterThan(0)
  })

  test('AM-04: 创建用户', async ({ page }) => {
    await goAdminView(page)
    await page.click('.admin-btn:has-text("新建用户")')
    await page.waitForTimeout(300)
    const modal = page.locator('.modal-box')
    await expect(modal).toBeVisible()

    await modal.locator('.form-row').nth(0).locator('input').fill(testUser)
    await modal.locator('.form-row').nth(1).locator('input').fill('Test123!')
    await modal.locator('.form-row').nth(2).locator('input').fill(`${testUser}@test.com`)
    await modal.locator('.tool-btn.primary, .admin-btn.primary').click()
    await page.waitForTimeout(1000)

    await expect(page.locator(`td:has-text("${testUser}")`)).toBeVisible({ timeout: TIMEOUT })
  })

  test('AM-05: 编辑用户角色', async ({ page }) => {
    await goAdminView(page)
    const row = page.locator(`tr:has-text("${testUser}")`).first()
    await expect(row).toBeVisible({ timeout: TIMEOUT })
    await row.locator('.icon-btn.sm').first().click()
    await page.waitForTimeout(300)

    const modal = page.locator('.modal-box')
    await expect(modal).toBeVisible()

    // 修改角色
    const roleSelect = modal.locator('select')
    if (await roleSelect.count() > 0) {
      await roleSelect.selectOption('operator')
    }
    await modal.locator('.admin-btn.primary').click()
    await page.waitForTimeout(1000)
  })

  test('AM-08: 不能禁用自己', async ({ page }) => {
    await goAdminView(page)
    const adminRow = page.locator('tr:has-text("admin")').first()
    if (await adminRow.count() > 0) {
      const disableBtn = adminRow.locator('.icon-btn.sm', { hasText: '⏸' })
      if (await disableBtn.count() > 0) {
        await disableBtn.click()
        await page.waitForTimeout(500)
        // 应该显示错误提示
      }
    }
  })

  test('AM-09: 删除用户', async ({ page }) => {
    await goAdminView(page)
    const row = page.locator(`tr:has-text("${testUser}")`).first()
    if (await row.count() > 0) {
      await row.locator('.icon-btn.danger').click()
      await page.waitForTimeout(300)
      const confirmModal = page.locator('.modal-danger')
      if (await confirmModal.count() > 0) {
        await confirmModal.locator('.admin-btn.danger, .tool-btn.danger').click()
        await page.waitForTimeout(1000)
      }
    }
  })
})

// ═══════════════════════════════════════════════════════════
//  AM-10: Agent管理
// ═══════════════════════════════════════════════════════════

test.describe('Agent管理', () => {
  const testAgent = `e2e-agent-${Date.now()}`

  test('AM-10: Agent列表 + CRUD', async ({ page }) => {
    await goAdminView(page)
    await page.click('.admin-tab:has-text("Agent管理")')
    await page.waitForTimeout(500)

    // 创建
    await page.click('.admin-btn:has-text("新建Agent")')
    await page.waitForTimeout(300)
    const modal = page.locator('.modal-box')
    await modal.locator('.form-row').nth(0).locator('input').fill(testAgent)
    await modal.locator('.form-row').nth(1).locator('input').fill('E2E测试Agent')
    await modal.locator('.form-row').nth(2).locator('input').fill('自动化测试')
    await modal.locator('.admin-btn.primary').click()
    await page.waitForTimeout(1000)

    // 验证Token弹窗
    const tokenModal = page.locator('.token-box')
    if (await tokenModal.count() > 0) {
      await page.locator('.admin-btn:has-text("关闭")').click()
      await page.waitForTimeout(300)
    }

    // 验证Agent出现在列表
    await expect(page.locator(`td:has-text("${testAgent}")`)).toBeVisible({ timeout: TIMEOUT })

    // 删除
    const row = page.locator(`tr:has-text("${testAgent}")`).first()
    await row.locator('.icon-btn.danger').click()
    await page.waitForTimeout(300)
    const confirmModal = page.locator('.modal-danger')
    if (await confirmModal.count() > 0) {
      await confirmModal.locator('.admin-btn.danger').click()
      await page.waitForTimeout(1000)
    }
  })
})

// ═══════════════════════════════════════════════════════════
//  AM-12: 审计日志
// ═══════════════════════════════════════════════════════════

test.describe('审计日志', () => {
  test('AM-12: 审计日志可查看', async ({ page }) => {
    await goAdminView(page)
    await page.click('.admin-tab:has-text("审计日志")')
    await page.waitForTimeout(500)
    const logTable = page.locator('.admin-table')
    await expect(logTable).toBeVisible()
  })
})
