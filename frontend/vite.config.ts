import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'

// In development the UI is served by Vite and the API by a running app.
export default defineConfig({
  plugins: [svelte()],
  // Not emptied: dist/.gitkeep is committed so go:embed compiles without a build.
  build: { outDir: 'dist', emptyOutDir: false, target: 'chrome120' },
  server: { proxy: { '/api': 'http://127.0.0.1:8765' } },
})
