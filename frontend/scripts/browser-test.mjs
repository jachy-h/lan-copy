import assert from 'node:assert/strict'
import { spawn, spawnSync } from 'node:child_process'
import { mkdtemp, mkdir, writeFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { createRequire } from 'node:module'
import net from 'node:net'
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
const origin = `http://127.0.0.1:${port}`
const service = spawn(binary, ['-listen', `127.0.0.1:${port}`, '-dir', shared, '-autostart'], { env: { ...process.env, LAN_COPY_DISABLE_UPDATE: '1', LAN_COPY_DISABLE_AUTOSTART: '1' } })
let output = ''; service.stderr.on('data', data => { output += data })
const delay = ms => new Promise(resolve => setTimeout(resolve, ms))
let browser

function pdf(jpeg) {
  const objects = [
    '<< /Type /Catalog /Pages 2 0 R >>',
    '<< /Type /Pages /Kids [3 0 R 5 0 R] /Count 2 >>',
    '<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 300] /Resources << /XObject << /Im 7 0 R >> >> /Contents 4 0 R >>',
    '<< /Length 32 >>\nstream\nq 200 0 0 200 20 20 cm /Im Do Q\nendstream',
    '<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 300] /Resources << >> /Contents 6 0 R >>',
    '<< /Length 0 >>\nstream\n\nendstream',
    Buffer.concat([Buffer.from(`<< /Type /XObject /Subtype /Image /Width 8 /Height 8 /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /DCTDecode /Length ${jpeg.length} >>\nstream\n`), jpeg, Buffer.from('\nendstream')]),
  ]
  const parts = [Buffer.from('%PDF-1.4\n')], offsets = [0]
  for (let i = 0; i < objects.length; i++) {
    offsets.push(Buffer.concat(parts).length)
    parts.push(Buffer.from(`${i + 1} 0 obj\n`), Buffer.isBuffer(objects[i]) ? objects[i] : Buffer.from(objects[i]), Buffer.from('\nendobj\n'))
  }
  const xref = Buffer.concat(parts).length
  parts.push(Buffer.from(`xref\n0 8\n0000000000 65535 f \n${offsets.slice(1).map(offset => `${String(offset).padStart(10, '0')} 00000 n \n`).join('')}trailer\n<< /Size 8 /Root 1 0 R >>\nstartxref\n${xref}\n%%EOF`))
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
    console.log(`${name} ${browser.version()}: embedded HTML/CSS, PDF image/pagination, DOCX, XLSX worker, Range, close, offline, text and cache recovery passed`)
    await context.close(); await browser.close(); browser = undefined
  }
  await testUpgrade(root, temporary, chromium)
} finally {
  await browser?.close()
  service.kill('SIGTERM'); await Promise.race([once(service, 'exit'), delay(5000)])
  if (service.exitCode === null) service.kill('SIGKILL')
  await rm(temporary, { recursive: true, force: true })
}
