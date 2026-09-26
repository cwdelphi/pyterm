import { test, expect } from '@playwright/test';

test('login page zh/en toggle', async ({ page }) => {
  await page.goto('https://127.0.0.1:5588/', { waitUntil: 'networkidle' });
  await page.evaluate(() => localStorage.removeItem('lang'));
  await page.reload({ waitUntil: 'networkidle' });

  const title = page.locator('.sc-title');
  await expect(title).toHaveText('📌 典型使用场景');
  const badge = page.locator('.sc-badge').first();
  await expect(badge).toContainText('场景');
  // foot scenario Chinese
  await expect(page.locator('.sc-foot').first()).toContainText('目标服务器');
  // lang button visible
  const btn = page.locator('.lang-toggle');
  await expect(btn).toBeVisible();
  await btn.click();
  await expect(title).toHaveText('📌 Typical Scenarios');
  await expect(badge).toContainText('Scenario');
  await expect(page.locator('.sc-foot').first()).toContainText('target server');
  // localStorage persisted
  const lang = await page.evaluate(() => localStorage.getItem('lang'));
  expect(lang).toBe('en');
  // reload keeps English
  await page.reload({ waitUntil: 'networkidle' });
  await expect(page.locator('.sc-title')).toHaveText('📌 Typical Scenarios');
  // toggle back
  await page.locator('.lang-toggle').click();
  await expect(page.locator('.sc-title')).toHaveText('📌 典型使用场景');
  const lang2 = await page.evaluate(() => localStorage.getItem('lang'));
  expect(lang2).toBe('zh-CN');
});
