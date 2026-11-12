import assert from 'node:assert/strict'
import test from 'node:test'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import { generateVendor } from './vendor-assets.mjs'

test('dev text route serves the shared app and redirects legacy GET/HEAD with query intact', async () => {
  const server = await createServer({
    configFile: fileURLToPath(new URL('../vite.config.ts', import.meta.url)),
    logLevel: 'silent',
    server: { host: '127.0.0.1', port: 0 },
  })
  try {
    await server.listen()
    const origin = `http://127.0.0.1:${server.httpServer.address().port}`
    for (const method of ['GET', 'HEAD']) {
      const response = await fetch(origin + '/transfer.html?from=bookmark&value=%E4%B8%AD', { method, redirect: 'manual' })
      assert.equal(response.status, 308)
      assert.equal(response.headers.get('location'), '/transfer-text?from=bookmark&value=%E4%B8%AD')
      const print = await fetch(origin + '/print-queue?from=bookmark', { method })
      assert.equal(print.status, 200)
      if (method === 'GET') assert.ok((await print.text()).includes('正在读取打印机'))
      const page = await fetch(origin + '/transfer-text?from=bookmark', { method })
      assert.equal(page.status, 200)
      if (method === 'GET') {
        const html = await page.text()
        assert.ok(html.includes('/src/pages/app.ts'))
        assert.ok(html.includes('正在读取共享文本'))
      }
    }
  } finally { await server.close() }
})


test('dev vendor assets provide module and WebAssembly MIME types', async () => {
  await generateVendor()
  const server = await createServer({
    configFile: fileURLToPath(new URL('../vite.config.ts', import.meta.url)),
    logLevel: 'silent', server: { host: '127.0.0.1', port: 0 },
  })
  try {
    await server.listen()
    const origin = `http://127.0.0.1:${server.httpServer.address().port}`
    for (const [path, type] of [
      ['pdfjs/legacy/build/pdf.min.mjs', 'text/javascript'],
      ['pdfjs/legacy/build/pdf.worker.min.mjs', 'text/javascript'],
      ['sheetjs/worker.js', 'text/javascript'],
      ['pdfjs/wasm/openjpeg.wasm', 'application/wasm'],
    ]) {
      const response = await fetch(`${origin}/vendor/${path}`)
      assert.equal(response.status, 200, path)
      assert.equal(response.headers.get('content-type'), type, path)
      await response.arrayBuffer()
    }
  } finally { await server.close() }
})
