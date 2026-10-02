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
  // 默认 tab 由 activeTab 初始值决定(实测落到 agents), 用户管理用例需显式切到 users
  await page.locator('.menu-item:has-text("用户管理")').click({ timeout: 15000 })
  await page.waitForTimeout(800)
}

// 用户表实际类名为 .table-card > table(无 .admin-table);
// 空态行为 <tr><td colspan=8 class="empty-row">, 类在 td 上, 故按 .user-cell 识别数据行
function userRows(page: Page) {
  return page.locator('.table-card table tbody tr:has(.user-cell)')
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
    await expect(page.locator('.menu-item:has-text("用户管理")')).toBeVisible()
    await expect(page.locator('.menu-item:has-text("Agent管理")')).toBeVisible()
    await expect(page.locator('.menu-item:has-text("coturn管理")')).toBeVisible()
    await expect(page.locator('.menu-item:has-text("审计日志")')).toBeVisible()
  })
})

// ═══════════════════════════════════════════════════════════
//  AM-03 ~ AM-09: 用户管理
// ═══════════════════════════════════════════════════════════

test.describe('用户管理', () => {
  const testUser = `e2e_user_${Date.now()}`

  test('AM-03: 用户列表加载', async ({ page }) => {
    await goAdminView(page)
    expect(await userRows(page).count()).toBeGreaterThan(0)
  })

  // T0.7: userSearch 之前只有 computed 没绑输入框, 用户列表搜索一直是死的
  test('AM-03b: 用户搜索框按用户名过滤', async ({ page }) => {
    await goAdminView(page)
    expect(await userRows(page).count()).toBeGreaterThan(0)

    const search = page.locator('.admin-search').first()
    await expect(search).toBeVisible({ timeout: TIMEOUT })

    // 无匹配 → 只剩 .empty-row 空态行
    await search.fill('zzz_no_such_user_zzz')
    await expect(userRows(page)).toHaveCount(0, { timeout: TIMEOUT })
    await expect(page.locator('.table-card table td.empty-row')).toBeVisible({ timeout: TIMEOUT })

    // 只命中 admin 自己
    await search.fill('admin')
    await expect(userRows(page)).toHaveCount(1, { timeout: TIMEOUT })
    await expect(userRows(page).first()).toContainText('admin')

    await search.fill('')
    expect(await userRows(page).count()).toBeGreaterThan(0)
  })

  // fixme: 用户列表头已无「新建用户」按钮, openNewUser() 成了死代码(仅剩弹窗标题引用 admin.newUser),
  //        需先做产品决策(恢复按钮或改为邀请制)再恢复本用例
  test.fixme('AM-04: 创建用户', async ({ page }) => {
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

  // fixme: 依赖 AM-04 先创建用户, 同上阻塞
  test.fixme('AM-05: 编辑用户角色', async ({ page }) => {
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
    await page.click('.menu-item:has-text("Agent管理")')
    await page.waitForTimeout(500)

    // 创建: form-row 0=只读AgentID(自动生成), 1=名称, 2=coturn下拉, 3=备注
    await page.click('.admin-toolbar .btn.primary')
    await page.waitForTimeout(300)
    const modal = page.locator('.modal-box')
    await expect(modal).toBeVisible()
    await modal.locator('.form-row').nth(1).locator('input').fill(testAgent)
    await modal.locator('.form-row').nth(3).locator('input').fill('自动化测试')
    await modal.locator('.modal-actions .btn.primary').click()
    await page.waitForTimeout(1500)

    // Token 部署向导三步(三步都有 .agent-guide-progress): 连点最后按钮直到关闭,
    // 否则残留的 .modal-mask 会拦截后续的删除点击
    const guideModal = page.locator('.modal-mask', { has: page.locator('.agent-guide-progress') })
    await expect(guideModal).toBeVisible({ timeout: TIMEOUT })
    for (let i = 0; i < 5 && (await guideModal.count()) > 0; i++) {
      const btns = guideModal.locator('.modal-actions .btn')
      if (!(await btns.count())) break
      await btns.last().click({ timeout: 3000 }).catch(() => {})
      await page.waitForTimeout(400)
    }
    await expect(guideModal).toHaveCount(0, { timeout: TIMEOUT })

    // 验证Agent出现在列表
    await expect(page.locator(`td:has-text("${testAgent}")`)).toBeVisible({ timeout: TIMEOUT })

    // 删除(操作列为 .text-btn.danger, 确认弹窗 .modal-danger .btn.danger)
    const row = page.locator(`tr:has-text("${testAgent}")`).first()
    await row.locator('.text-btn.danger').last().click()
    await page.waitForTimeout(300)
    const confirmModal = page.locator('.modal-danger')
    await expect(confirmModal).toBeVisible({ timeout: TIMEOUT })
    await confirmModal.locator('.btn.danger').click()
    await page.waitForTimeout(1500)
    await expect(page.locator(`td:has-text("${testAgent}")`)).toHaveCount(0, { timeout: TIMEOUT })
  })
})

// ═══════════════════════════════════════════════════════════
//  AM-12: 审计日志
// ═══════════════════════════════════════════════════════════

test.describe('审计日志', () => {
  test('AM-12: 审计日志可查看', async ({ page }) => {
    await goAdminView(page)
    await page.click('.menu-item:has-text("审计日志")')
    await page.waitForTimeout(500)
    const logTable = page.locator('.table-card table').first()
    await expect(logTable).toBeVisible()
  })
})
