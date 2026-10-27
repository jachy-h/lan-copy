import { cp, mkdir, mkdtemp, readFile, readdir, rename, rm } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

async function listFiles(directory, relative = '') {
  const found = []
  for (const item of await readdir(directory, { withFileTypes: true })) {
    const name = path.posix.join(relative, item.name)
    if (item.isSymbolicLink()) throw new Error(`Unexpected symlink: ${name}`)
    if (item.isDirectory()) found.push(...await listFiles(path.join(directory, item.name), name))
    else found.push(name)
  }
  return found.sort()
}

// Only these generated targets are replaced; local vendor and logo sources stay intact.
export async function syncAssets(projectRoot, { check = false, move = rename } = {}) {
  const buildRoot = path.join(projectRoot, 'tmp/frontend-dist')
  const webRoot = path.join(projectRoot, 'web')
  const pages = ['index.html', 'transfer.html', 'preview.html', 'print.html']
  const manifest = JSON.parse(await readFile(path.join(buildRoot, '.vite/manifest.json'), 'utf8'))
  const built = (await listFiles(buildRoot)).filter(file => !file.startsWith('.vite/'))
  function requireFile(file) {
    if (!built.includes(file) || file.includes('..') || path.posix.isAbsolute(file)) throw new Error(`Missing or invalid build resource: ${file}`)
  }
  // Vite may merge identical page entries in its manifest; validate emitted HTML directly.
  for (const page of pages) requireFile(page)
  // Validate the entire graph, including lazy chunks, CSS and emitted assets.
  for (const chunk of Object.values(manifest)) {
    requireFile(chunk.file)
    for (const file of [...(chunk.css || []), ...(chunk.assets || [])]) requireFile(file)
    for (const key of [...(chunk.imports || []), ...(chunk.dynamicImports || [])]) {
      if (!manifest[key]) throw new Error(`Missing manifest dependency: ${key}`)
    }
  }
  for (const page of pages) {
    const html = await readFile(path.join(buildRoot, page), 'utf8')
    if (/\/src\//.test(html)) throw new Error(`Uncompiled source in ${page}`)
    for (const match of html.matchAll(/(?:src|href)="([^"#]+)"/g)) {
      const url = match[1].split(/[?#]/)[0]
      if (url.startsWith('/_frontend/')) requireFile(url.slice('/_frontend/'.length))
      else if (url.startsWith('/') && !['/', '/transfer-text', '/print-queue', '/preview.html'].includes(url)) {
        await readFile(path.join(webRoot, url.slice(1)))
      }
    }
  }
  const generated = built.filter(file => !pages.includes(file))
  if (generated.some(file => file.startsWith('.') || file.endsWith('.map'))) throw new Error('Unexpected private build resource')
  if (check) {
    const actual = await listFiles(path.join(webRoot, '_frontend'))
    if (JSON.stringify(actual) !== JSON.stringify(generated)) throw new Error('Frontend file list differs; run make frontend-sync')
    for (const file of built) {
      const destination = pages.includes(file) ? path.join(webRoot, file) : path.join(webRoot, '_frontend', file)
      if (!(await readFile(path.join(buildRoot, file))).equals(await readFile(destination))) throw new Error(`Frontend differs: ${file}; run make frontend-sync`)
    }
    return
  }
  // Stage outside the embedded tree. Preflight and staging never remove working assets.
  const staging = await mkdtemp(path.join(projectRoot, 'tmp/frontend-sync-'))
  const targets = ['_frontend', ...pages]
  const backedUp = [], installed = []
  let keepStaging = false
  try {
    await mkdir(path.join(staging, 'new/_frontend'), { recursive: true })
    await mkdir(path.join(staging, 'old'), { recursive: true })
    for (const file of built) {
      const destination = path.join(staging, 'new', pages.includes(file) ? file : `_frontend/${file}`)
      await mkdir(path.dirname(destination), { recursive: true })
      await cp(path.join(buildRoot, file), destination)
    }
    for (const target of targets) {
      try { await move(path.join(webRoot, target), path.join(staging, 'old', target)); backedUp.push(target) }
      catch (error) { if (error.code !== 'ENOENT') throw error }
      await move(path.join(staging, 'new', target), path.join(webRoot, target))
      installed.push(target)
    }
  } catch (error) {
    keepStaging = true
    const failures = [error]
    for (const target of installed.reverse()) {
      try { await rm(path.join(webRoot, target), { recursive: true, force: true }) } catch (failure) { failures.push(failure) }
    }
    // Use real rename for recovery, independent of an injected failing install operation.
    for (const target of backedUp.reverse()) {
      try { await rename(path.join(staging, 'old', target), path.join(webRoot, target)) } catch (failure) { failures.push(failure) }
    }
    if (failures.length > 1) throw new AggregateError(failures, `Frontend rollback incomplete; recovery files retained in ${staging}`)
    keepStaging = false
    throw error
  } finally { if (!keepStaging) await rm(staging, { recursive: true, force: true }) }
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')
  await syncAssets(root, { check: process.argv.includes('--check') })
  console.log('Frontend HTML and complete resource tree are synchronized')
}
