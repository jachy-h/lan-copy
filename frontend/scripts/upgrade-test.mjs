import assert from 'node:assert/strict'
import { spawn, spawnSync } from 'node:child_process'
import { cp, mkdir, readdir, readFile, writeFile } from 'node:fs/promises'
import path from 'node:path'
import net from 'node:net'
import { once } from 'node:events'
import { build } from 'vite'
import { syncAssets } from './sync-assets.mjs'

const delay = ms => new Promise(resolve => setTimeout(resolve, ms))

// Build two isolated snapshots with distinct content hashes. A test-only lazy import
// exercises Vite's actual preloadError path without changing application sources.
export async function testUpgrade(root, temporary, chromium) {
  const snapshot = path.join(temporary, 'upgrade-source')
  await mkdir(snapshot)
  for (const file of await readdir(root)) if (file.endsWith('.go') || file === 'go.mod') await cp(path.join(root, file), path.join(snapshot, file))
  await cp(path.join(root, 'internal'), path.join(snapshot, 'internal'), { recursive: true })
  await cp(path.join(root, 'web'), path.join(snapshot, 'web'), { recursive: true })
  for (const version of ['A', 'B']) {
    await build({ configFile: path.join(root, 'frontend/vite.config.ts'), logLevel: 'silent', plugins: [{
      name: 'upgrade-test-probe',
      transform(code, id) { if (id.endsWith('/src/pages/app.ts')) return code + '\nwindow.__loadUpgradeProbe = () => import("./preview")' },
    }], build: { outDir: path.join(snapshot, 'tmp/frontend-dist'), rollupOptions: { output: { banner: `window.__upgradeBuild = "${version}";` } } } })
    await syncAssets(snapshot)
    const result = spawnSync('go', ['build', '-o', path.join(temporary, `upgrade-${version}`), '.'], { cwd: snapshot, encoding: 'utf8' })
    assert.equal(result.status, 0, result.stderr)
    const html = await readFile(path.join(snapshot, 'web/index.html'), 'utf8')
    assert.ok(html.includes('/_frontend/assets/'))
  }
  const socket = net.createServer(); socket.listen(0, '127.0.0.1'); await once(socket, 'listening')
  const port = socket.address().port; await new Promise(resolve => socket.close(resolve))
  const origin = `http://127.0.0.1:${port}`, shared = path.join(temporary, 'upgrade-shared')
  await mkdir(shared); await writeFile(path.join(shared, 'fixture.txt'), 'upgrade fixture')
  let service
  async function start(version) {
    service = spawn(path.join(temporary, `upgrade-${version}`), ['-listen', `127.0.0.1:${port}`, '-dir', shared, '-autostart'], { stdio: 'ignore' })
    for (let i = 0; i < 100; i++) { if (await fetch(origin + '/healthz').then(r => r.ok).catch(() => false)) return; await delay(100) }
    throw new Error('Upgrade service did not start')
  }
  async function stop() { if (service && service.exitCode === null) { const exited = once(service, 'exit'); service.kill('SIGTERM'); await exited } }
  const browser = await chromium.launch()
  try {
    const page = await browser.newPage()
    await start('A'); await page.goto(origin); await page.waitForSelector('.explorer-row')
    assert.equal(await page.evaluate(() => window.__upgradeBuild), 'A')
    await stop(); await start('B')
    // The old tab requests an A-only lazy chunk from B; Vite emits preloadError,
    // the application reloads once, and the browser receives B HTML and chunks.
    await Promise.all([
      page.waitForEvent('load'),
      page.evaluate(() => { void window.__loadUpgradeProbe().catch(() => {}) }),
    ])
    await page.waitForSelector('.explorer-row')
    assert.equal(await page.evaluate(() => window.__upgradeBuild), 'B')
    await page.reload(); await page.waitForSelector('.explorer-row')
    assert.equal(await page.evaluate(() => window.__upgradeBuild), 'B')
    console.log('chromium: A → B binary upgrade, stale lazy chunk recovery and cached refresh passed')
  } finally { await browser.close(); await stop() }
}
