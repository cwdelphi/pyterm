const { chromium } = require('playwright');

(async () => {
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({ viewport: { width: 1280, height: 800 } });
  const page = await context.newPage();

  const logs = [];
  page.on('console', msg => {
    const text = `[${msg.type()}] ${msg.text()}`;
    logs.push(text);
  });
  page.on('pageerror', err => logs.push(`[PAGE_ERROR] ${err.message}`));

  try {
    // 1. Login
    console.log('=== Step 1: Login ===');
    await page.goto('http://192.0.2.50:5588', { waitUntil: 'networkidle', timeout: 15000 });
    await page.fill('input[placeholder="请输入用户名"]', 'admin');
    await page.fill('input[placeholder="请输入密码"]', 'change_me_pass');
    await page.click('button:has-text("登")');
    await page.waitForTimeout(3000);
    await page.screenshot({ path: '/tmp/t02_after_login.png' });
    console.log('Login done, URL:', page.url());

    // 2. Click SSH management in top nav
    console.log('=== Step 2: Go to SSH management ===');
    const allBtns = await page.locator('button, a, [class*="nav"]').allTextContents();
    console.log('Nav items:', allBtns.filter(t => t.trim()).slice(0, 20).join(' | '));

    // Find SSH in nav
    await page.locator('button').filter({ hasText: /SSH/ }).first().click({ timeout: 5000 });
    await page.waitForTimeout(2000);
    await page.screenshot({ path: '/tmp/t03_ssh_page.png' });
    console.log('SSH page loaded');

    // 3. Find TestServer-Agent
    console.log('=== Step 3: Find TestServer-Agent ===');
    const sidebarTexts = await page.locator('.si-name').allTextContents();
    console.log('Sidebar items:', sidebarTexts.join(' | '));

    // Click on TestServer-Agent to expand
    const agentItem = page.locator('.si-name').filter({ hasText: 'TestServer-Agent' });
    if (await agentItem.count() > 0) {
      // Click the parent button/row
      await agentItem.first().locator('..').locator('..').locator('button').first().click();
      await page.waitForTimeout(1000);
      await page.screenshot({ path: '/tmp/t04_expanded.png' });
      console.log('Expanded TestServer-Agent');

      // Click SSH submenu
      const sshSub = page.locator('.ssh-submenu-item').filter({ hasText: /SSH/ }).first();
      if (await sshSub.count() > 0) {
        await sshSub.dblclick();
        console.log('Double-clicked SSH submenu');
      } else {
        console.log('SSH submenu not found, trying alternatives...');
        await page.locator('.ssh-submenu-item').first().dblclick();
      }
    } else {
      console.log('TestServer-Agent NOT found');
    }

    await page.waitForTimeout(3000);
    await page.screenshot({ path: '/tmp/t05_terminal.png' });

    // 4. Wait for WebRTC activity
    console.log('=== Step 4: Waiting 15s for WebRTC ===');
    await page.waitForTimeout(15000);
    await page.screenshot({ path: '/tmp/t06_final.png' });

  } catch (err) {
    console.error('ERROR:', err.message);
    await page.screenshot({ path: '/tmp/error.png' }).catch(() => {});
  }

  console.log('\n=== ALL CONSOLE LOGS ===');
  logs.forEach(l => console.log(l));
  console.log('=== END ===');

  await browser.close();
})();
