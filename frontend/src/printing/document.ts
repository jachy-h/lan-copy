import { loadPDF, type PDFLoadingTask, type PDFRenderTask } from '../preview/pdf'

export interface PrintLayout {
  width: number; height: number; left: number; top: number
  printableWidth: number; printableHeight: number; key: string
}
export interface PrintJob {
  id: number; printer: string; created: string
  status: 'preparing' | 'queued' | 'processing' | 'submitted' | 'failed'
  name?: string; pages?: number; systemId?: number; error?: string; warning?: string
}
export interface PrintSource {
  pages: number
  render(page: number, canvas: HTMLCanvasElement, layout: PrintLayout, signal: AbortSignal): Promise<void>
  close(): void
}
export const MAX_SOURCE_BYTES = 50 * 1024 * 1024
export const MAX_PAGES = 50
export const MAX_PAGE_BYTES = 12 * 1024 * 1024
export const MAX_DOCUMENT_BYTES = 128 * 1024 * 1024
export const MAX_PIXELS = 16_000_000
export const PRINT_DPI = 200

export function isPrintableFile(name: string) { return /\.(pdf|jpe?g|png)$/i.test(name) }

// Read one bounded snapshot through the existing shared-file boundary. Preview
// and final preparation both use this File; they never silently reread a changed
// shared path after the user has reviewed it.
export async function loadSharedPrintFile(path: string, signal: AbortSignal): Promise<File> {
  const parts = path.split('/')
  if (!isPrintableFile(path) || path.startsWith('/') || /[\\\0]/.test(path) || parts.some(part => !part || part === '.' || part === '..')) throw new Error('共享文件路径或格式无效')
  const response = await fetch(`/preview-files/${encodeURIComponent(path)}`, { cache: 'no-store', signal })
  if (!response.ok) {
    if (response.status === 404) throw new Error('共享文件已被移动或删除，请返回共享空间重新选择')
    throw new Error('无法读取共享文件，请返回共享空间重试')
  }
  const declaredSize = Number(response.headers.get('Content-Length'))
  if (Number.isFinite(declaredSize) && declaredSize > MAX_SOURCE_BYTES) {
    await response.body?.cancel().catch(() => {})
    throw new Error('共享文件超过 50 MiB 限制')
  }
  const chunks: ArrayBuffer[] = []
  let total = 0
  if (response.body) {
    const reader = response.body.getReader()
    try {
      while (true) {
        const { done, value } = await reader.read()
        check(signal)
        if (done) break
        total += value.byteLength
        if (total > MAX_SOURCE_BYTES) { await reader.cancel(); throw new Error('共享文件超过 50 MiB 限制') }
        chunks.push(new Uint8Array(value).buffer)
      }
    } finally { reader.releaseLock() }
  } else {
    const data = await response.arrayBuffer()
    if (data.byteLength > MAX_SOURCE_BYTES) throw new Error('共享文件超过 50 MiB 限制')
    chunks.push(data)
  }
  check(signal)
  return new File(chunks, parts.at(-1)!, { type: response.headers.get('Content-Type') || '' })
}

export function rasterSize(layout: PrintLayout) {
  const width = Math.round(layout.printableWidth * PRINT_DPI / 25.4)
  const height = Math.round(layout.printableHeight * PRINT_DPI / 25.4)
  if (!Number.isFinite(width) || !Number.isFinite(height) || width < 1 || height < 1 || width * height > MAX_PIXELS) throw new Error('打印页面过大，暂不支持此纸张')
  return { width, height }
}
export function fitRect(width: number, height: number, areaWidth: number, areaHeight: number) {
  if (![width, height, areaWidth, areaHeight].every(value => Number.isFinite(value) && value > 0)) throw new Error('页面尺寸无效')
  const scale = Math.min(areaWidth / width, areaHeight / height)
  return { width: width * scale, height: height * scale, x: (areaWidth - width * scale) / 2, y: (areaHeight - height * scale) / 2, scale }
}
function check(signal: AbortSignal) { if (signal.aborted) throw new DOMException('操作已取消', 'AbortError') }
function setup(canvas: HTMLCanvasElement, layout: PrintLayout) {
  const size = rasterSize(layout)
  canvas.width = size.width; canvas.height = size.height
  const context = canvas.getContext('2d')
  if (!context) throw new Error('浏览器无法创建打印画布')
  context.fillStyle = '#fff'; context.fillRect(0, 0, size.width, size.height)
  return context
}

export async function loadPrintSource(file: File, signal: AbortSignal): Promise<PrintSource> {
  if (!isPrintableFile(file.name)) throw new Error('仅支持 PDF、JPG/JPEG、PNG')
  if (file.size > MAX_SOURCE_BYTES) throw new Error('文件超过 50 MiB 限制')
  if (!file.size) throw new Error('不能打印空文件')
  const signature = new Uint8Array(await file.slice(0, 1024).arrayBuffer())
  check(signal)
  if (/\.pdf$/i.test(file.name)) {
    if (!new TextDecoder('latin1').decode(signature).includes('%PDF-')) throw new Error('文件内容不是有效的 PDF')
    const module = await loadPDF(); check(signal)
    module.GlobalWorkerOptions.workerSrc = '/vendor/pdfjs/legacy/build/pdf.worker.min.mjs'
    const data = new Uint8Array(await file.arrayBuffer()); check(signal)
    const task: PDFLoadingTask = module.getDocument({
      data, cMapUrl: '/vendor/pdfjs/cmaps/', cMapPacked: true,
      standardFontDataUrl: '/vendor/pdfjs/standard_fonts/', wasmUrl: '/vendor/pdfjs/wasm/',
      isEvalSupported: false, disableAutoFetch: true, disableStream: true,
    })
    let active: PDFRenderTask | undefined
    let closed = false
    const close = () => { if (closed) return; closed = true; active?.cancel(); void task.destroy().catch(() => {}); signal.removeEventListener('abort', close) }
    signal.addEventListener('abort', close, { once: true })
    try {
      const pdf = await task.promise; check(signal)
      if (pdf.numPages < 1 || pdf.numPages > MAX_PAGES) throw new Error('PDF 最多支持 50 页，请拆分后打印')
      return {
        pages: pdf.numPages, close,
        async render(index, canvas, layout, renderSignal) {
          check(renderSignal); if (closed) throw new Error('文件已关闭')
          const page = await pdf.getPage(index)
          const cancel = () => active?.cancel()
          renderSignal.addEventListener('abort', cancel, { once: true })
          try {
            check(renderSignal)
            const context = setup(canvas, layout)
            const base = page.getViewport({ scale: 1 })
            const fit = fitRect(base.width, base.height, canvas.width, canvas.height)
            const viewport = page.getViewport({ scale: fit.scale })
            active = page.render({ canvasContext: context, viewport, transform: [1, 0, 0, 1, fit.x, fit.y], intent: 'print' })
            await active.promise; check(renderSignal)
          } finally { active = undefined; renderSignal.removeEventListener('abort', cancel); page.cleanup() }
        },
      }
    } catch (cause) {
      close()
      if (cause instanceof Error && cause.name === 'PasswordException') throw new Error('暂不支持加密 PDF，请先另存为无密码 PDF')
      throw cause
    }
  }
  const isPNG = /\.png$/i.test(file.name)
  const valid = isPNG
    ? [137, 80, 78, 71, 13, 10, 26, 10].every((byte, i) => signature[i] === byte)
    : signature[0] === 255 && signature[1] === 216 && signature[2] === 255
  if (!valid) throw new Error('图片内容与文件格式不符')
  const bitmap = await createImageBitmap(file, { imageOrientation: 'from-image' })
  if (signal.aborted) { bitmap.close(); check(signal) }
  let closed = false
  const close = () => { if (closed) return; closed = true; bitmap.close(); signal.removeEventListener('abort', close) }
  signal.addEventListener('abort', close, { once: true })
  return {
    pages: 1, close,
    async render(_index, canvas, layout, renderSignal) {
      check(renderSignal); if (closed) throw new Error('文件已关闭')
      const context = setup(canvas, layout)
      const fit = fitRect(bitmap.width, bitmap.height, canvas.width, canvas.height)
      context.drawImage(bitmap, fit.x, fit.y, fit.width, fit.height)
    },
  }
}

export function pagePNG(canvas: HTMLCanvasElement): Promise<Blob> {
  return new Promise((resolve, reject) => canvas.toBlob(blob => {
    if (!blob) reject(new Error('无法准备打印页面'))
    else if (blob.size > MAX_PAGE_BYTES) reject(new Error('单页超过 12 MiB，请降低文档复杂度后重试'))
    else resolve(blob)
  }, 'image/png'))
}

export async function prepareDocument(source: PrintSource, name: string, printer: string, layout: PrintLayout, signal: AbortSignal, progress: (page: number) => void) {
  const body = new FormData()
  body.append('manifest', JSON.stringify({ printer, name, pages: source.pages, layoutKey: layout.key }))
  const canvas = document.createElement('canvas')
  let total = 0
  try {
    for (let i = 1; i <= source.pages; i++) {
      check(signal); progress(i)
      await source.render(i, canvas, layout, signal)
      const blob = await pagePNG(canvas); check(signal)
      total += blob.size
      if (total > MAX_DOCUMENT_BYTES) throw new Error('准备的打印页面超过 128 MiB，请拆分文件后打印')
      body.append('page', blob, `${String(i).padStart(3, '0')}.png`)
    }
    return body
  } finally { canvas.width = canvas.height = 0 }
}
