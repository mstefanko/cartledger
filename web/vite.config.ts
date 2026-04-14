import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import path from 'path'
import { readFileSync, writeFileSync } from 'fs'

// Stamps the service worker with a build version so browsers pick up updates
function swVersionPlugin() {
  return {
    name: 'sw-version',
    writeBundle() {
      const swPath = path.resolve(__dirname, 'dist/sw.js')
      try {
        const content = readFileSync(swPath, 'utf-8')
        const version = Date.now().toString(36)
        writeFileSync(swPath, content.replace(/__BUILD_VERSION__/g, version))
      } catch {
        // sw.js may not exist in dist if public/ copy failed
      }
    },
  }
}

export default defineConfig({
  plugins: [
    react(),
    tailwindcss(),
    swVersionPlugin(),
  ],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
  server: {
    proxy: {
      '/api': {
        target: 'http://localhost:8079',
        changeOrigin: true,
      },
    },
  },
})
