import { fileURLToPath } from 'node:url'
import { extname, resolve } from 'node:path'
import { readFile } from 'node:fs/promises'
import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vitest/config'

const root = fileURLToPath(new URL('.', import.meta.url))
const apiTarget = process.env.LAN_COPY_PROXY || 'http://127.0.0.1:8080'
type ProxyHook = { on(event: string, handler: (request: { setHeader(name: string, value: string): void }, incoming: { headers: { origin?: string } }) => void): void }
const proxyOptions = { target: apiTarget, changeOrigin: true, configure(proxy: ProxyHook) { proxy.on('proxyReq', (request, incoming) => { if (incoming.headers.origin) request.setHeader('Origin', new URL(apiTarget).origin) }) } }

export default defineConfig(({ command }) => ({
  root,
  plugins: [vue(), {
    name: 'local-vendor-assets',
    configureServer(server) {
      server.middlewares.use(async (request, response, next) => {
        const pathname = new URL(request.url || '/', 'http://localhost').pathname
        if (!pathname.startsWith('/vendor/')) return next()
        const file = resolve(root, '../tmp/frontend-vendor', `.${pathname.slice('/vendor'.length)}`)
        if (!file.startsWith(resolve(root, '../tmp/frontend-vendor') + '/')) {
          response.statusCode = 400
          return response.end()
        }
        try {
          const data = await readFile(file)
          const contentTypes: Record<string, string> = {
            '.mjs': 'text/javascript', '.js': 'text/javascript',
            '.wasm': 'application/wasm', '.css': 'text/css',
          }
          response.setHeader('Content-Type', contentTypes[extname(file)] || 'application/octet-stream')
          response.setHeader('Cache-Control', 'no-store')
          response.end(data)
        } catch { response.statusCode = 404; response.end('Not found') }
      })
    },
  }, {
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
  server: { proxy: Object.fromEntries(['/api', '/files', '/preview-files', '/thumbnails', '/logo', '/filesystem.html'].map(prefix => [prefix, proxyOptions])) },
  publicDir: resolve(root, 'public'),
  base: command === 'serve' ? '/' : '/_frontend/',
  build: {
    target: ['chrome90', 'safari15'],
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
