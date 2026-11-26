/// <reference types="node" />
import { defineConfig, devices } from '@playwright/test'

// The workers read the port that the main process picked.
process.env.SHEEPDOG_E2E_PORT ||= String(20000 + Math.floor(Math.random() * 40000))
const port = Number(process.env.SHEEPDOG_E2E_PORT)

export default defineConfig({
  testDir: 'e2e',
  testMatch: '*.e2e.ts',
  reporter: 'list',
  use: {
    baseURL: `http://127.0.0.1:${port}`,
    viewport: { width: 1440, height: 900 },
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'], viewport: { width: 1440, height: 900 } } }],
  webServer: {
    command: `vite preview --host 127.0.0.1 --port ${port} --strictPort`,
    url: `http://127.0.0.1:${port}`,
    reuseExistingServer: false,
  },
})
