import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'

// In development the UI is served by Vite and the API by a running app.
export default defineConfig({
  plugins: [svelte()],
  // Not emptied: dist/.gitkeep is committed so go:embed compiles without a build.
  // license writes dist/.vite/license.md, which the release's notices include.
  build: { outDir: 'dist', emptyOutDir: false, target: 'chrome120', license: true },
  server: { proxy: { '/api': 'http://127.0.0.1:8765' } },
})
