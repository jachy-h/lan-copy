import { fileURLToPath } from 'node:url'
import { resolve } from 'node:path'
import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vitest/config'

const root = fileURLToPath(new URL('.', import.meta.url))
const apiTarget = process.env.LAN_COPY_PROXY || 'http://127.0.0.1:8080'
type ProxyHook = { on(event: string, handler: (request: { setHeader(name: string, value: string): void }, incoming: { headers: { origin?: string } }) => void): void }
const proxyOptions = { target: apiTarget, changeOrigin: true, configure(proxy: ProxyHook) { proxy.on('proxyReq', (request, incoming) => { if (incoming.headers.origin) request.setHeader('Origin', new URL(apiTarget).origin) }) } }

export default defineConfig(({ command }) => ({
  root,
  plugins: [vue(), {
    name: 'text-transfer-route',
    configureServer(server) {
      server.middlewares.use((request, response, next) => {
        const [pathname] = (request.url || '').split('?')
        const query = (request.url || '').slice(pathname.length)
        if (pathname === '/transfer.html') {
          response.writeHead(308, { Location: `/transfer-text${query}`, 'Cache-Control': 'no-cache' })
          response.end()
          return
        }
        if (pathname === '/transfer-text') request.url = `/transfer.html${query}`
        if (pathname === '/print-queue') request.url = `/print.html${query}`
        next()
      })
    },
  }],
  server: { proxy: Object.fromEntries(['/api', '/files', '/preview-files', '/thumbnails', '/vendor', '/logo', '/filesystem.html'].map(prefix => [prefix, proxyOptions])) },
  publicDir: resolve(root, 'public'),
  base: command === 'serve' ? '/' : '/_frontend/',
  build: {
    outDir: resolve(root, '../tmp/frontend-dist'),
    emptyOutDir: true,
    sourcemap: false,
    manifest: true,
    rollupOptions: {
      input: {
        index: resolve(root, 'index.html'),
        transfer: resolve(root, 'transfer.html'),
        preview: resolve(root, 'preview.html'),
        print: resolve(root, 'print.html'),

      },
      output: {
        entryFileNames: 'assets/[name]-[hash].js',
        chunkFileNames: 'assets/[name]-[hash].js',
        assetFileNames: 'assets/[name]-[hash][extname]',
      },
    },
  },
  test: { environment: 'jsdom', include: ['src/**/*.test.ts'] },
}))
