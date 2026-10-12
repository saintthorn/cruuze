import { fileURLToPath, URL } from 'node:url'

import tailwindcss from '@tailwindcss/vite'
import vue from '@vitejs/plugin-vue'
import type { Plugin } from 'vite'
import { defineConfig } from 'vitest/config'

/**
 * Writes asset-manifest.json: every file the build produced. The service
 * worker caches them all, so pages that were never visited work offline too.
 */
function assetManifest(): Plugin {
  return {
    name: 'cruuze-asset-manifest',
    apply: 'build',
    generateBundle(_, bundle) {
      const files = Object.keys(bundle).map((file) => `/${file}`)
      this.emitFile({ type: 'asset', fileName: 'asset-manifest.json', source: JSON.stringify({ files }) })
    },
  }
}

export default defineConfig({
  plugins: [vue(), tailwindcss(), assetManifest()],
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
  },
  server: {
    port: 5173,
    // The API sets cookies and checks Origin against APP_URL, so the browser
    // must talk to it through this origin.
    proxy: { '/api': { target: process.env.API_URL ?? 'http://localhost:8080', ws: true } },
  },
  test: {
    environment: 'node',
    // e2e/ holds Playwright tests, which have their own runner.
    include: ['src/**/*.test.ts'],
  },
})
