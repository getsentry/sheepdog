import react from '@vitejs/plugin-react'
import { loadEnv } from 'vite'
import { defineConfig } from 'vitest/config'

// SHEEPDOG_API_URL points the dev server at a server on another address.
export default defineConfig(({ mode }) => ({
  plugins: [react()],
  build: {
    outDir: '../server/internal/webui/dist',
    emptyOutDir: true,
  },
  server: {
    proxy: {
      '/api': loadEnv(mode, '.', '').SHEEPDOG_API_URL || 'http://127.0.0.1:8080',
    },
  },
  test: {
    environment: 'jsdom',
  },
}))
