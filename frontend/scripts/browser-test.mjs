import assert from 'node:assert/strict'
import { spawn, spawnSync } from 'node:child_process'
import { mkdtemp, mkdir, writeFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { createRequire } from 'node:module'
import net from 'node:net'
import http from 'node:http'
import { once } from 'node:events'
import { chromium, firefox, webkit } from 'playwright'
import { testUpgrade } from './upgrade-test.mjs'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')
const require = createRequire(import.meta.url)
const JSZip = require('../../web/vendor/docx-preview/jszip.min.js')
const temporary = await mkdtemp(path.join(tmpdir(), 'lan-copy-browser-'))
const shared = path.join(temporary, 'shared')
await mkdir(shared)
const binary = path.join(temporary, process.platform === 'win32' ? 'lan-copy.exe' : 'lan-copy')
const build = spawnSync('go', ['build', '-o', binary, '.'], { cwd: root, encoding: 'utf8' })
assert.equal(build.status, 0, build.stderr)
const socket = net.createServer(); socket.listen(0, '127.0.0.1'); await once(socket, 'listening')
const port = socket.address().port; await new Promise(resolve => socket.close(resolve))
const backendOrigin = `http://127.0.0.1:${port}`
const service = spawn(binary, ['-listen', `127.0.0.1:${port}`, '-dir', shared, '-autostart'], { env: { ...process.env, LAN_COPY_DISABLE_UPDATE: '1', LAN_COPY_DISABLE_AUTOSTART: '1' } })
let output = ''; service.stderr.on('data', data => { output += data })
const delay = ms => new Promise(resolve => setTimeout(resolve, ms))
let browser, handleDocument
// Keep binary uploads on their original URL. WebKit's interception API omits
// file data and rewriting the URL can reconstruct an incomplete multipart body.
const proxy = http.createServer((request, response) => {
  if (new URL(request.url, backendOrigin).pathname === '/api/printing/documents') {
    if (handleDocument) { void handleDocument(request, response); return }
    response.writeHead(503); response.end('Test document receiver inactive'); return
  }
  const upstream = http.request(new URL(request.url, backendOrigin), { method: request.method, headers: request.headers }, incoming => {
    response.writeHead(incoming.statusCode, incoming.headers); incoming.pipe(response)
  })
  upstream.on('error', () => { if (response.headersSent) response.destroy(); else { response.writeHead(502); response.end() } })
  response.on('close', () => upstream.destroy())
  request.pipe(upstream)
})
proxy.listen(0, '127.0.0.1'); await once(proxy, 'listening')
const origin = `http://127.0.0.1:${proxy.address().port}`

function pdf(jpeg) {
  const stream = commands => `<< /Length ${Buffer.byteLength(commands)} >>\nstream\n${commands}endstream`
  const objects = [
    '<< /Type /Catalog /Pages 2 0 R >>',
    '<< /Type /Pages /Kids [3 0 R 5 0 R] /Count 2 >>',
    '<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 300] /Resources << /XObject << /Im 7 0 R >> /Font << /F1 8 0 R >> >> /Contents 4 0 R >>',
    stream('q 200 0 0 200 20 20 cm /Im Do Q\nBT /F1 16 Tf 20 275 Td (PDF Print Preview) Tj 0 -20 Td /F1 9 Tf (Lan Copy - Page 1 of 2) Tj ET\n'),
    '<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 300] /Resources << /Font << /F1 8 0 R >> >> /Contents 6 0 R >>',
    stream('BT /F1 16 Tf 20 275 Td (Document continuation) Tj 0 -24 Td /F1 10 Tf (Page 2 of 2 - Ready for printing) Tj ET\n'),
    Buffer.concat([Buffer.from(`<< /Type /XObject /Subtype /Image /Width 8 /Height 8 /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /DCTDecode /Length ${jpeg.length} >>\nstream\n`), jpeg, Buffer.from('\nendstream')]),
    '<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>',
  ]
  const parts = [Buffer.from('%PDF-1.4\n')], offsets = [0]
  for (let i = 0; i < objects.length; i++) {
    offsets.push(Buffer.concat(parts).length)
    parts.push(Buffer.from(`${i + 1} 0 obj\n`), Buffer.isBuffer(objects[i]) ? objects[i] : Buffer.from(objects[i]), Buffer.from('\nendobj\n'))
  }
  const xref = Buffer.concat(parts).length
  parts.push(Buffer.from(`xref\n0 ${objects.length + 1}\n0000000000 65535 f \n${offsets.slice(1).map(offset => `${String(offset).padStart(10, '0')} 00000 n \n`).join('')}trailer\n<< /Size ${objects.length + 1} /Root 1 0 R >>\nstartxref\n${xref}\n%%EOF`))
  return Buffer.concat(parts)
}
async function officeFixtures() {
  const docx = new JSZip()
  docx.file('[Content_Types].xml', '<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>')
  docx.file('_rels/.rels', '<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>')
  docx.file('word/document.xml', '<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>DOCX browser fixture</w:t></w:r></w:p><w:sectPr/></w:body></w:document>')
  await writeFile(path.join(shared, 'sample.docx'), await docx.generateAsync({ type: 'nodebuffer' }))
  const xlsx = new JSZip()
  xlsx.file('[Content_Types].xml', '<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/></Types>')
  xlsx.file('_rels/.rels', '<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>')
  xlsx.file('xl/workbook.xml', '<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="Sheet1" sheetId="1" r:id="rId1"/></sheets></workbook>')
  xlsx.file('xl/_rels/workbook.xml.rels', '<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/></Relationships>')
  xlsx.file('xl/worksheets/sheet1.xml', '<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData><row r="1"><c r="A1" t="inlineStr"><is><t>XLSX browser fixture</t></is></c></row></sheetData></worksheet>')
  await writeFile(path.join(shared, 'sample.xlsx'), await xlsx.generateAsync({ type: 'nodebuffer' }))
  await writeFile(path.join(shared, 'sample.txt'), 'preview text fixture\n'.repeat(20000))
}

try {
  await officeFixtures()
  await mkdir(path.join(shared, 'spa-folder'))
  await mkdir(path.join(shared, 'print-folder'))
  let ready = false
  for (let attempt = 0; attempt < 100; attempt++) {
    if (await fetch(origin + '/healthz').then(response => response.ok).catch(() => false)) { ready = true; break }
    if (service.exitCode !== null) break
    await delay(100)
  }
  assert.ok(ready, output)
  const names = (process.env.LAN_COPY_BROWSERS || 'chromium,firefox,webkit').split(',')
  for (const name of names) {
    browser = await ({ chromium, firefox, webkit })[name].launch()
    const context = await browser.newContext()
    const page = await context.newPage(), errors = [], violations = []
    page.on('pageerror', error => errors.push(error.message))
    await page.addInitScript(() => {
      window.__cspViolations = []
      document.addEventListener('securitypolicyviolation', event => window.__cspViolations.push(`${event.violatedDirective}: ${event.blockedURI}`))
    })
    await page.goto(origin)
    await page.waitForSelector('.explorer-row')
    const jpeg = await page.evaluate(() => {
      const canvas = document.createElement('canvas'); canvas.width = canvas.height = 8
      const ctx = canvas.getContext('2d'); ctx.fillStyle = 'red'; ctx.fillRect(0, 0, 8, 8)
      return canvas.toDataURL('image/jpeg').split(',')[1]
    })
    await writeFile(path.join(shared, 'sample.pdf'), pdf(Buffer.from(jpeg, 'base64')))
    const sharedPrintName = '预算 +100% #.PDF'
    const sharedPrintPath = `print-folder/${sharedPrintName}`
    await writeFile(path.join(shared, sharedPrintPath), pdf(Buffer.from(jpeg, 'base64')))
    const headers = await fetch(origin).then(response => response.headers)
    assert.equal(headers.get('cache-control'), 'no-cache')
    const moduleURL = await page.locator('script[type="module"]').first().getAttribute('src')
    assert.match(moduleURL, /-[\w-]{8}\.js$/)
    assert.match((await fetch(origin + moduleURL)).headers.get('cache-control'), /immutable/)
    const missing = await fetch(origin + '/_frontend/assets/old-12345678.js')
    assert.equal(missing.status, 404); assert.equal(missing.headers.get('cache-control'), 'no-cache')
    const ranged = await fetch(origin + '/preview-files/sample.txt', { headers: { Range: 'bytes=0-15' } })
    assert.equal(ranged.status, 206); assert.equal((await ranged.arrayBuffer()).byteLength, 16)
    // Shared shell survives same-document navigation, with history, draft and folder state.
    const documents = []
    const trackDocuments = request => { if (request.isNavigationRequest() && request.frame() === page.mainFrame()) documents.push(request.url()) }
    page.on('request', trackDocuments)
    await page.locator('.explorer-row[data-path="spa-folder"]').dblclick()
    await page.waitForURL('**/#path=spa-folder')
    await page.locator('header').evaluate(header => { header.dataset.spaMarker = 'retained' })
    await page.getByRole('link', { name: '文本快传', exact: true }).click()
    await page.locator('#textInput').fill('SPA draft')
    await page.getByRole('link', { name: '共享空间', exact: true }).click()
    await page.waitForURL('**/#path=spa-folder')
    assert.equal(await page.locator('header').getAttribute('data-spa-marker'), 'retained')
    await page.goBack()
    await page.waitForSelector('#textInput')
    assert.equal(await page.locator('#textInput').inputValue(), 'SPA draft')
    await page.goForward()
    await page.waitForSelector('.explorer')
    assert.equal(new URL(page.url()).hash, '#path=spa-folder')
    assert.deepEqual(documents, [], 'SPA navigation requested a new document')
    page.off('request', trackDocuments)
    for (const width of [1280, 768, 390]) {
      await page.setViewportSize({ width, height: 900 })
      const layout = await page.locator('.hero-actions').evaluate(actions => {
        const share = actions.querySelector('#accessButton').getBoundingClientRect()
        const status = actions.querySelector('#serviceStatus').getBoundingClientRect()
        const settings = actions.querySelector('.settings-trigger, #settingsButton, #settingsBtn')?.getBoundingClientRect()
        return { share: share.right, status: status.left, right: status.right, viewport: innerWidth, settings: settings?.right }
      })
      assert.ok(layout.share <= layout.status && layout.right <= layout.viewport, `status layout at ${width}`)
      if (layout.settings) assert.ok(layout.settings <= layout.status)
      if (name === 'chromium') {
        const screenshots = path.join(root, 'tmp/v0.5.2-screenshots')
        await mkdir(screenshots, { recursive: true })
        await page.screenshot({ path: path.join(screenshots, `files-${width}.png`), fullPage: true })
        await page.getByRole('link', { name: '文本快传', exact: true }).click()
        await page.waitForSelector('#textInput')
        await page.screenshot({ path: path.join(screenshots, `transfer-${width}.png`), fullPage: true })
        await page.getByRole('link', { name: '共享空间', exact: true }).click()
        await page.waitForSelector('.explorer')
      }
    }
    // Print entry and direct route use the embedded page. Mock native responses
    // for UI coverage; never send a physical page from browser regression tests.
    await page.goto(origin + '/print-queue')
    await page.waitForSelector('#print-printer')
    assert.equal(await page.title(), '打印队列 · Lan Copy')
    if (process.platform !== 'win32') {
      await page.getByText('当前仅支持 Windows 主机打印', { exact: false }).waitFor()
      assert.ok(await page.getByRole('button', { name: '打印测试页', exact: true }).isDisabled())
    }
    let printJobs = [], testRequests = 0, documentRequests = 0
    handleDocument = async (request, response) => {
      response.setHeader('Access-Control-Allow-Origin', origin)
      response.setHeader('Access-Control-Allow-Headers', 'Content-Type, X-Lan-Copy-Action')
      response.setHeader('Access-Control-Allow-Methods', 'POST, OPTIONS')
      if (request.method === 'OPTIONS') { response.end(); return }
      try {
        assert.equal(request.headers['x-lan-copy-action'], 'print-document')
        const chunks = []
        for await (const chunk of request) chunks.push(chunk)
        const body = await new Response(Buffer.concat(chunks), { headers: { 'content-type': request.headers['content-type'] } }).formData()
        const manifest = JSON.parse(body.get('manifest'))
        assert.equal(manifest.printer, 'Office USB Printer')
        assert.equal(manifest.layoutKey, 'test-a4')
        const pages = body.getAll('page')
        assert.equal(pages.length, manifest.pages)
        for (let index = 0; index < pages.length; index++) {
          assert.equal(pages[index].name, `${String(index + 1).padStart(3, '0')}.png`)
          const bytes = Buffer.from(await pages[index].arrayBuffer())
          assert.equal(bytes.readUInt32BE(16), 1575)
          assert.equal(bytes.readUInt32BE(20), 2260)
        }
        documentRequests++
        printJobs = [{ id: 10 + documentRequests, name: manifest.name, pages: manifest.pages, printer: manifest.printer, created: '2026-10-27T01:00:00Z', status: 'submitted', systemId: 50 + documentRequests }]
        response.writeHead(202, { 'Content-Type': 'application/json' })
        response.end(JSON.stringify({ ...printJobs[0], status: 'queued' }))
      } catch (error) { errors.push(`Document receiver: ${error.message}`); response.writeHead(500); response.end(JSON.stringify({ error: error.message })) }
    }
    await page.route('**/api/printing**', async route => {
      const pathname = new URL(route.request().url()).pathname
      let data
      if (pathname === '/api/printing') data = { available: true, reason: '', printers: [{ name: 'Office USB Printer', default: true }, { name: 'Meeting Room', default: false }] }
      else if (pathname === '/api/printing/jobs') data = { jobs: printJobs }
      else if (pathname === '/api/printing/layout') data = { width: 210, height: 297, left: 5, top: 5, printableWidth: 200, printableHeight: 287, key: 'test-a4' }
      else if (pathname === '/api/printing/documents') { await route.continue(); return }
      else {
        assert.equal(route.request().headers()['x-lan-copy-action'], 'print-test')
        assert.equal(route.request().postDataJSON().printer, 'Office USB Printer')
        testRequests++
        printJobs = [{ id: 1, printer: 'Office USB Printer', created: '2026-10-27T01:00:00Z', status: 'submitted', systemId: 42 }]
        data = { ...printJobs[0], status: 'queued' }
      }
      await route.fulfill({ status: pathname.endsWith('/test') || pathname.endsWith('/documents') ? 202 : 200, contentType: 'application/json', body: JSON.stringify(data) })
    })
    await page.reload()
    await page.waitForFunction(() => document.querySelector('#print-printer').value === 'Office USB Printer')
    for (const width of [1280, 768, 390, 320]) {
      await page.setViewportSize({ width, height: 900 })
      assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), `print page overflow at ${width}`)
      assert.ok(await page.locator('.print-access-button').evaluate(button => button.getBoundingClientRect().right <= document.querySelector('#accessButton').getBoundingClientRect().left), 'print must precede share')
      if (name === 'chromium') {
        const screenshots = path.join(root, 'tmp/v0.5.5-screenshots')
        await mkdir(screenshots, { recursive: true })
        await page.screenshot({ path: path.join(screenshots, `print-${width}.png`), fullPage: true })
      }
    }
    await page.getByRole('button', { name: '打印测试页', exact: true }).click()
    await page.locator('.print-job-status.submitted').waitFor()
    assert.equal(testRequests, 1)
    assert.ok((await page.locator('.print-job-meta').textContent()).includes('#42'))
    // Real PDF.js and image decoding: only the device APIs are mocked.
    await page.setViewportSize({ width: 1280, height: 900 })
    const transparentPNG = await page.evaluate(() => {
      const canvas = document.createElement('canvas'); canvas.width = 400; canvas.height = 250
      const ctx = canvas.getContext('2d'); ctx.fillStyle = '#3977f6'; ctx.fillRect(40, 40, 320, 170)
      ctx.fillStyle = 'white'; ctx.font = '24px sans-serif'; ctx.fillText('Lan Copy print preview', 65, 135)
      return canvas.toDataURL('image/png').split(',')[1]
    })
    for (const file of [
      { name: 'sample.pdf', path: path.join(shared, 'sample.pdf'), pages: 2 },
      { name: 'photo.jpg', mimeType: 'image/jpeg', buffer: Buffer.from(jpeg, 'base64'), pages: 1 },
      { name: 'screenshot.png', mimeType: 'image/png', buffer: Buffer.from(transparentPNG, 'base64'), pages: 1 },
    ]) {
      await page.locator('input[aria-label="选择打印文件"]').setInputFiles(file.path || { name: file.name, mimeType: file.mimeType, buffer: file.buffer })
      await page.waitForFunction(() => { const button = document.querySelector('.document-submit'); return button && !button.disabled })
      const canvas = page.locator('.document-printable-area canvas')
      assert.equal(await canvas.getAttribute('width'), '1575')
      assert.deepEqual(await canvas.evaluate(element => [...element.getContext('2d').getImageData(0, 0, 1, 1).data]), [255, 255, 255, 255], `${file.name}: paper must be opaque white`)
      assert.ok(await canvas.evaluate(element => {
        const data = element.getContext('2d').getImageData(0, 0, element.width, element.height).data
        for (let i = 0; i < data.length; i += 4) if (data[i] < 150 || data[i + 1] < 150 || data[i + 2] < 150) return true
        return false
      }), `${file.name}: blank print preview`)
      if (file.pages === 2) {
        await page.getByRole('button', { name: '打印预览下一页', exact: true }).click()
        await page.getByText('第 2 / 2 页', { exact: true }).waitFor()
        await page.waitForFunction(() => !document.querySelector('.document-submit').disabled)
        await page.getByRole('button', { name: '打印预览上一页', exact: true }).click()
        await page.waitForFunction(() => !document.querySelector('.document-submit').disabled)
        if (name === 'chromium') {
          for (const width of [1280, 390]) {
            await page.setViewportSize({ width, height: 1000 })
            const screenshots = path.join(root, 'tmp/v0.5.6-screenshots')
            await mkdir(screenshots, { recursive: true })
            await page.screenshot({ path: path.join(screenshots, `pdf-preview-${width}.png`), fullPage: true })
          }
          await page.setViewportSize({ width: 390, height: 900 })
          await page.getByRole('button', { name: '放大打印预览', exact: true }).click()
          await page.getByRole('button', { name: '放大打印预览', exact: true }).click()
          assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'zoom must scroll inside the preview')
          await page.getByRole('button', { name: '缩小打印预览', exact: true }).click()
          await page.getByRole('button', { name: '缩小打印预览', exact: true }).click()
          await page.setViewportSize({ width: 1280, height: 900 })
        }
      }
      await page.locator('.document-submit').click()
      await page.locator('.print-job').filter({ hasText: file.name }).locator('.submitted').waitFor()
      assert.ok((await page.locator('.print-job-meta').textContent()).includes(`${file.pages} 页`))
    }
    assert.equal(documentRequests, 3)
    // A normal filename click still previews. Only the ellipsis menu's Print
    // action selects the shared source, and entering/reloading never prints it.
    await page.getByRole('link', { name: '共享空间', exact: true }).click()
    await page.locator('.explorer-row[data-path="print-folder"]').dblclick()
    await page.waitForURL('**/#path=print-folder')
    const sharedRow = page.locator(`.explorer-row[data-path="${sharedPrintPath}"]`)
    await sharedRow.getByRole('link', { name: sharedPrintName, exact: true }).click()
    await page.waitForSelector('dialog[open]')
    assert.equal(new URL(page.url()).pathname, '/')
    await page.locator('#previewClose').click()
    await sharedRow.getByRole('button', { name: `更多操作：${sharedPrintName}`, exact: true }).click()
    await page.getByRole('menuitem', { name: '打印', exact: true }).click()
    await page.waitForURL(url => url.pathname === '/print-queue' && url.searchParams.get('file') === sharedPrintPath)
    await page.waitForFunction(() => !document.querySelector('.document-submit').disabled)
    assert.ok((await page.locator('.document-shared-origin').textContent()).includes(sharedPrintPath))
    assert.equal(documentRequests, 3, 'entering print preview must not submit')
    await page.reload()
    await page.waitForFunction(() => !document.querySelector('.document-submit').disabled)
    await page.getByRole('link', { name: '共享空间', exact: true }).click()
    await page.waitForURL('**/#path=print-folder')
    await page.goBack()
    await page.waitForFunction(() => !document.querySelector('.document-submit').disabled)
    // The source is a snapshot: changing/removing the shared path cannot replace
    // the already-reviewed content during final page preparation.
    await rm(path.join(shared, sharedPrintPath))
    await page.locator('.document-submit').click()
    await page.locator('.print-job').filter({ hasText: sharedPrintName }).locator('.submitted').waitFor()
    assert.equal(documentRequests, 4)
    await page.reload()
    await page.getByText('共享文件已被移动或删除，请返回共享空间重新选择', { exact: true }).waitFor()
    assert.ok(await page.locator('.document-submit').isDisabled())

    await page.unroute('**/api/printing**')
    handleDocument = undefined
    await page.setViewportSize({ width: 1280, height: 900 })
    await page.goto(origin)
    await page.getByRole('link', { name: '文本快传', exact: true }).click()
    await page.getByRole('link', { name: '共享空间', exact: true }).click()
    await page.locator('.explorer-row[data-path="sample.txt"]').dblclick()
    await page.waitForSelector('dialog[open]')
    await page.goBack()
    await page.waitForSelector('#textInput')
    assert.equal(await page.locator('#previewDialog').evaluate(dialog => dialog.open), false, 'preview remained modal after history navigation')
    await page.goto(origin)
    // Both entry surfaces use the actual engine, local libraries and workers under Go CSP.
    for (const surface of ['dialog', 'standalone']) {
      for (const [file, selector, expected] of [
        ['sample.txt', '.preview-text', 'preview text fixture'],
        ['sample.docx', '.preview-docx-container', 'DOCX browser fixture'],
        ['sample.xlsx', '.preview-sheet-table', 'XLSX browser fixture'],
        ['sample.pdf', 'canvas', null],
      ]) {
        if (surface === 'standalone') await page.goto(`${origin}/preview.html?path=${file}`)
        else {
          await page.goto(origin)
          await page.locator(`.explorer-row[data-path="${file}"]`).dblclick()
        }
        await page.waitForSelector(selector, { timeout: 20000 })
        if (expected) assert.ok((await page.locator(selector).textContent()).includes(expected))
        if (file.endsWith('.pdf')) {
          await page.waitForFunction(() => document.querySelector('#previewStatus').textContent.includes('第 1 / 2 页'))
          assert.ok(await page.locator('canvas').evaluate(canvas => {
            const pixels = canvas.getContext('2d').getImageData(0, 0, canvas.width, canvas.height).data
            for (let index = 0; index < pixels.length; index += 4) if (pixels[index] > 150 && pixels[index + 1] < 100 && pixels[index + 2] < 100) return true
            return false
          }), 'PDF JPEG image was not rendered')
          await page.getByRole('button', { name: '下一页', exact: true }).click()
          await page.waitForFunction(() => document.querySelector('#previewStatus').textContent.includes('第 2 / 2 页'))
        }
        violations.push(...await page.evaluate(() => window.__cspViolations))
        if (surface === 'dialog') {
          await page.locator('#previewClose').click()
          assert.equal(await page.locator('#previewContent').textContent(), '')
          for (let attempt = 0; attempt < 50 && page.workers().length; attempt++) await delay(20)
          assert.equal(page.workers().length, 0, 'preview worker survived close')
        }
      }
    }
    // Repeated close while the real vendor module request is delayed.
    await page.goto(origin)
    await page.route('**/vendor/pdfjs/legacy/build/pdf.min.mjs', async route => { await delay(300); await route.continue().catch(() => {}) })
    for (let index = 0; index < 3; index++) {
      await page.locator('.explorer-row[data-path="sample.pdf"]').dblclick()
      await page.locator('#previewClose').click(); await delay(400)
      assert.equal(await page.locator('#previewContent').textContent(), '')
      assert.equal(await page.locator('.preview-pdf-tools').count(), 0)
    }
    await page.unroute('**/vendor/pdfjs/legacy/build/pdf.min.mjs')
    // Offline preview failure has a user-visible recovery path.
    await context.setOffline(true)
    await page.locator('.explorer-row[data-path="sample.txt"]').dblclick()
    await page.waitForFunction(() => document.querySelector('#previewStatus').textContent.includes('读取失败'))
    await context.setOffline(false); await page.locator('#previewClose').click()
    const legacy = await fetch(origin + '/transfer.html?from=bookmark', { redirect: 'manual' })
    assert.equal(legacy.status, 308)
    assert.equal(legacy.headers.get('location'), '/transfer-text?from=bookmark')
    await page.goto(origin + '/transfer.html')
    await page.waitForURL('**/transfer-text')
    await page.locator('#textInput').fill(`browser integration ${name}`)
    await page.getByRole('button', { name: '发布文本', exact: true }).click()
    await page.waitForFunction(text => [...document.querySelectorAll('.text-content')].some(node => node.textContent === text), `browser integration ${name}`)
    // Exercise the stale-chunk event emitted by Vite after deployment.
    await page.evaluate(() => sessionStorage.removeItem('lan-copy:asset-reload'))
    await Promise.all([page.waitForEvent('load'), page.evaluate(() => window.dispatchEvent(new Event('vite:preloadError', { cancelable: true })))])
    await page.waitForSelector('#textInput')
    await page.evaluate(() => window.dispatchEvent(new Event('vite:preloadError', { cancelable: true })))
    await page.waitForSelector('#asset-recovery')
    assert.deepEqual(errors, [], `uncaught errors (${name})`)
    assert.deepEqual(violations, [], `CSP violations (${name})`)
    console.log(`${name} ${browser.version()}: embedded HTML/CSS, PDF/JPG/PNG print preview and real raster upload, PDF image/pagination, DOCX, XLSX worker, Range, close, offline, text and cache recovery passed`)
    await context.close(); await browser.close(); browser = undefined
  }
  await testUpgrade(root, temporary, chromium)
} finally {
  await browser?.close()
  proxy.closeAllConnections(); await new Promise(resolve => proxy.close(resolve))
  service.kill('SIGTERM'); await Promise.race([once(service, 'exit'), delay(5000)])
  if (service.exitCode === null) service.kill('SIGKILL')
  await rm(temporary, { recursive: true, force: true })
}
