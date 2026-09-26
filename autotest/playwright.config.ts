import { defineConfig } from '@playwright/test'

const BASE_URL = process.env.BASE_URL || 'https://127.0.0.1:5588'

export default defineConfig({
  testDir: './tests',
  timeout: 120000,
  retries: 0,
  workers: 1,
  fullyParallel: false,
  use: {
    baseURL: BASE_URL,
    screenshot: 'on',
    trace: 'retain-on-failure',
    headless: true,
    viewport: { width: 1280, height: 720 },
    ignoreHTTPSErrors: true,
    launchOptions: {
      executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH || undefined,
    },
  },
  reporter: [
    ['list'],
    ['html', { open: 'never', outputFolder: 'playwright-report' }],
    ['json', { outputFile: 'artifacts/results.json' }],
  ],
  outputDir: './test-results',
  projects: [
    {
      name: 'reliability',
      testDir: './tests/reliability',
      use: { browserName: 'chromium' },
    },
    {
      name: 'timing',
      testDir: './tests/timing',
      use: { browserName: 'chromium' },
    },
    {
      name: 'latency',
      testDir: './tests/latency',
      use: { browserName: 'chromium' },
    },
    {
      name: 'performance',
      testDir: './tests/performance',
      use: { browserName: 'chromium' },
    },
    {
      name: 'diagnostics',
      testDir: './tests/diagnostics',
      use: { browserName: 'chromium' },
    },
    {
      name: 'remote',
      testDir: './tests/remote-full',
      fullyParallel: false,
      timeout: 600000,
      use: { browserName: 'chromium' },
    },
    {
      name: 'remote-legacy',
      testDir: './tests',
      testMatch: [
        /agent-direct\/.*\.spec\.ts$/,
        /gateway-agent\/.*\.spec\.ts$/,
        /gateway-lifecycle\/.*\.spec\.ts$/,
        /gateway-stability\/.*\.spec\.ts$/,
        /vnc-e2e\/.*\.spec\.ts$/,
        /sftp\/.*\.spec\.ts$/,
        /sftp-stability\/.*\.spec\.ts$/,
        /ssh-webrtc\/.*\.spec\.ts$/,
        /multi-connection\/.*\.spec\.ts$/,
        /agent\/.*\.spec\.ts$/,
        /agent-config-regression\.spec\.ts$/,
      ],
      use: { browserName: 'chromium' },
    },
  ],
})
