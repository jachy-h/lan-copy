import { cp, lstat, mkdir, readFile, readdir, rm } from 'node:fs/promises'
import path from 'node:path'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'

const frontendRoot = fileURLToPath(new URL('..', import.meta.url))
const projectRoot = path.resolve(frontendRoot, '..')
const outputRoot = path.join(projectRoot, 'tmp/frontend-vendor')
const require = createRequire(import.meta.url)

function packageRoot(name, entry) {
  const resolved = require.resolve(entry)
  return name === 'pdfjs-dist' || name === 'jszip'
    ? path.dirname(require.resolve(`${name}/package.json`))
    : path.dirname(resolved)
}

async function copyTree(source, destination) {
  const info = await lstat(source)
  if (info.isSymbolicLink()) throw new Error(`Unexpected dependency symlink: ${source}`)
  if (info.isDirectory()) {
    await mkdir(destination, { recursive: true })
    for (const name of await readdir(source)) await copyTree(path.join(source, name), path.join(destination, name))
  } else if (info.isFile()) {
    await mkdir(path.dirname(destination), { recursive: true })
    await cp(source, destination)
  } else throw new Error(`Unsupported dependency file: ${source}`)
}

async function copyFile(source, destination) {
  await copyTree(source, path.join(outputRoot, destination))
}

export async function generateVendor() {
  const pdf = packageRoot('pdfjs-dist', 'pdfjs-dist/package.json')
  const docx = path.resolve(packageRoot('docx-preview', 'docx-preview'), '..')
  const jszip = packageRoot('jszip', 'jszip/package.json')
  const xlsx = packageRoot('xlsx', 'xlsx')
  const resources = [
    [pdf, 'legacy/build/pdf.min.mjs', 'pdfjs/legacy/build/pdf.min.mjs'],
    [pdf, 'legacy/build/pdf.worker.min.mjs', 'pdfjs/legacy/build/pdf.worker.min.mjs'],
    [pdf, 'cmaps', 'pdfjs/cmaps'],
    [pdf, 'standard_fonts', 'pdfjs/standard_fonts'],
    [pdf, 'wasm', 'pdfjs/wasm'],
    [pdf, 'LICENSE', 'pdfjs/LICENSE'],
    [docx, 'dist/docx-preview.min.js', 'docx-preview/docx-preview.min.js'],
    [docx, 'LICENSE', 'docx-preview/LICENSE-docx-preview'],
    [jszip, 'dist/jszip.min.js', 'docx-preview/jszip.min.js'],
    [jszip, 'LICENSE.markdown', 'docx-preview/LICENSE-JSZip.markdown'],
    [xlsx, 'dist/xlsx.full.min.js', 'sheetjs/xlsx.full.min.js'],
    [xlsx, 'LICENSE', 'sheetjs/LICENSE'],
  ]
  await rm(outputRoot, { recursive: true, force: true })
  await mkdir(outputRoot, { recursive: true })
  try {
    for (const [root, source, target] of resources) await copyFile(path.join(root, source), target)
    for (const name of ['pdfjs', 'docx-preview', 'sheetjs']) {
      await copyFile(path.join(frontendRoot, 'vendor', name, 'README.md'), `${name}/README.md`)
    }
    await copyFile(path.join(frontendRoot, 'vendor/sheetjs/worker.js'), 'sheetjs/worker.js')
    return outputRoot
  } catch (error) {
    await rm(outputRoot, { recursive: true, force: true })
    throw error
  }
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  await generateVendor()
  console.log('Vendor assets generated from npm dependencies')
}
