import path from 'node:path'

import { defineConfig, devices } from '@playwright/test'

// The tests run against their own servers on their own ports, so they never
// touch (or get confused by) a development server that happens to be up.
const API_PORT = 8081
const WEB_PORT = 5174
const WEB_URL = `http://localhost:${WEB_PORT}`

/** Where the API writes outgoing email, which is how tests get sign-in links. */
export const MAIL_DIR = path.join(import.meta.dirname, 'e2e', '.mail')

export default defineConfig({
  testDir: 'e2e',
  globalSetup: './e2e/global-setup.ts',
  // Every test makes its own users and workspace, so they can run side by side.
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [['list'], ['html', { open: 'never' }]] : 'list',
  use: {
    baseURL: WEB_URL,
    trace: 'retain-on-failure',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
  webServer: [
    {
      command: 'go run ./cmd/cruuze',
      cwd: '../server',
      url: `http://localhost:${API_PORT}/readyz`,
      timeout: 120_000,
      reuseExistingServer: false,
      env: {
        // A database of its own: the tests leave their workspaces behind.
        DATABASE_URL: process.env.E2E_DATABASE_URL ?? 'postgres://cruuze:cruuze@localhost:5432/cruuze_e2e?sslmode=disable',
        LISTEN_ADDR: `:${API_PORT}`,
        APP_URL: WEB_URL,
        MAIL_DIR,
        STORAGE_DIR: path.join(import.meta.dirname, 'e2e', '.attachments'),
        // Notification emails would only add noise to the mail directory.
        NOTIFICATION_EMAIL_DELAY: '-1s',
      },
    },
    {
      command: `npm run dev -- --port ${WEB_PORT} --strictPort`,
      url: WEB_URL,
      reuseExistingServer: false,
      env: { API_URL: `http://localhost:${API_PORT}` },
    },
  ],
})
