import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'

// In development the UI is served by Vite and the API by a running app.
export default defineConfig({
  plugins: [svelte()],
  build: { outDir: 'dist', emptyOutDir: true, target: 'chrome120' },
  server: { proxy: { '/api': 'http://127.0.0.1:8765' } },
})
