import { test } from 'node:test'
import assert from 'node:assert/strict'
import { mkdtemp, mkdir, writeFile, readFile, rm, rename } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { syncAssets } from './sync-assets.mjs'

async function fixture(t) {
  const root = await mkdtemp(path.join(tmpdir(), 'lan-copy-sync-'))
  t.after(() => rm(root, { recursive: true, force: true }))
  const build = path.join(root, 'tmp/frontend-dist')
  await mkdir(path.join(build, '.vite'), { recursive: true })
  await mkdir(path.join(build, 'assets/nested'), { recursive: true })
  await mkdir(path.join(root, 'web/_frontend'), { recursive: true })
  const manifest = {}
  for (const page of ['index.html', 'transfer.html', 'preview.html', 'print.html']) {
    manifest[page] = { file: page, isEntry: true, imports: ['main'] }
    await writeFile(path.join(build, page), '<script type="module" src="/_frontend/assets/main-hash.js"></script><link rel="stylesheet" href="/_frontend/assets/style-hash.css">')
    await writeFile(path.join(root, 'web', page), 'old HTML')
  }
  manifest.main = { file: 'assets/main-hash.js', css: ['assets/style-hash.css'], dynamicImports: ['lazy'], assets: ['assets/nested/font.woff2'] }
  manifest.lazy = { file: 'assets/lazy-hash.js' }
  for (const file of ['main-hash.js', 'style-hash.css', 'lazy-hash.js', 'nested/font.woff2']) await writeFile(path.join(build, 'assets', file), file)
  await writeFile(path.join(build, '.vite/manifest.json'), JSON.stringify(manifest))
  await writeFile(path.join(root, 'web/_frontend/obsolete.js'), 'old JS')
  return { root, build }
}

test('syncs HTML, lazy imports, styles and nested assets; detects drift and deleted files', async t => {
  const { root } = await fixture(t)
  await syncAssets(root)
  await syncAssets(root, { check: true })
  assert.equal(await readFile(path.join(root, 'web/_frontend/assets/nested/font.woff2'), 'utf8'), 'nested/font.woff2')
  await assert.rejects(readFile(path.join(root, 'web/_frontend/obsolete.js')))
  await writeFile(path.join(root, 'web/index.html'), 'drift')
  await assert.rejects(syncAssets(root, { check: true }), /differs/)
})

test('invalid graph never modifies working assets', async t => {
  const { root, build } = await fixture(t)
  await rm(path.join(build, 'assets/lazy-hash.js'))
  await assert.rejects(syncAssets(root), /Missing/)
  assert.equal(await readFile(path.join(root, 'web/index.html'), 'utf8'), 'old HTML')
})

test('failed multi-target installation restores all HTML and resources', async t => {
  const { root } = await fixture(t)
  await assert.rejects(syncAssets(root, { move: async (from, to) => {
    if (from.endsWith('/new/transfer.html')) throw new Error('injected failure')
    return rename(from, to)
  } }), /injected/)
  assert.equal(await readFile(path.join(root, 'web/index.html'), 'utf8'), 'old HTML')
  assert.equal(await readFile(path.join(root, 'web/transfer.html'), 'utf8'), 'old HTML')
  assert.equal(await readFile(path.join(root, 'web/_frontend/obsolete.js'), 'utf8'), 'old JS')
})

test('real Vite compilation keeps scoped CSS, lazy chunks and imported nested assets', async t => {
  const { build } = await import('vite')
  const { default: vue } = await import('@vitejs/plugin-vue')
  const frontend = path.resolve(import.meta.dirname, '..')
  const root = await mkdtemp(path.join(frontend, 'node_modules/.asset-fixture-'))
  t.after(() => rm(root, { recursive: true, force: true }))
  await mkdir(path.join(root, 'web')); await mkdir(path.join(root, 'nested'))
  for (const page of ['index.html', 'transfer.html', 'preview.html', 'print.html']) await writeFile(path.join(root, page), '<div id="app"></div><script type="module" src="/main.ts"></script>')
  await writeFile(path.join(root, 'main.ts'), 'import {createApp} from "vue"; window.openLazy = async () => createApp((await import("./Lazy.vue")).default).mount("#app")')
  await writeFile(path.join(root, 'Lazy.vue'), '<template><div class="probe">lazy</div></template><style scoped>.probe { background: url("./nested/marker.svg") }</style>')
  await writeFile(path.join(root, 'nested/marker.svg'), '<svg xmlns="http://www.w3.org/2000/svg" width="8" height="8"><rect width="8" height="8"/></svg>')
  await build({ configFile: false, root, base: '/_frontend/', plugins: [vue()], logLevel: 'silent', build: {
    outDir: path.join(root, 'tmp/frontend-dist'), manifest: true, assetsInlineLimit: 0,
    rollupOptions: { input: ['index.html', 'transfer.html', 'preview.html', 'print.html'].map(page => path.join(root, page)) },
  } })
  await syncAssets(root); await syncAssets(root, { check: true })
  const manifest = JSON.parse(await readFile(path.join(root, 'tmp/frontend-dist/.vite/manifest.json')))
  const lazy = Object.values(manifest).find(chunk => chunk.isDynamicEntry)
  assert.ok(lazy.css.length)
  assert.match(await readFile(path.join(root, 'web/_frontend', lazy.css[0]), 'utf8'), /data-v-/)
  assert.ok(Object.values(manifest).some(chunk => chunk.file.endsWith('.svg')))
})
