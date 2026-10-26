import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mountPreview } from './engine'
import { loadPDF, type PDFDocument, type PDFModule, type PDFPage } from './pdf'

vi.mock('./pdf', () => ({ loadPDF: vi.fn() }))
function deferred<T>() { let resolve!: (value: T) => void; const promise = new Promise<T>(done => { resolve = done }); return { promise, resolve } }
const flush = async () => { for (let i = 0; i < 12; i++) await Promise.resolve() }
let root: HTMLElement
let preview: ReturnType<typeof mountPreview> | undefined
const cleanup: (() => void)[] = []

beforeEach(() => {
  root = document.createElement('section')
  root.innerHTML = '<p id="previewStatus"></p><div id="textTools" class="hidden"><select id="textEncoding"><option value="utf-8">UTF-8</option></select></div><div id="previewContent"></div>'
  document.body.append(root)
  vi.stubGlobal('fetch', vi.fn(async () => new Response(null, { headers: { 'Content-Type': 'application/pdf' } })))
})
afterEach(() => { preview?.destroy(); preview = undefined; cleanup.splice(0).forEach(fn => fn()); root.remove(); vi.unstubAllGlobals(); vi.restoreAllMocks(); vi.mocked(loadPDF).mockReset() })

function pdfFixture() {
  const renderTask = { promise: Promise.resolve(), cancel: vi.fn() }
  const page: PDFPage = { cleanup: vi.fn(), getViewport: ({ scale }) => ({ width: 300 * scale, height: 400 * scale }), render: vi.fn(() => renderTask) }
  const document: PDFDocument = { numPages: 2, getPage: vi.fn(async () => page) }
  const task = { promise: Promise.resolve(document), destroy: vi.fn(async () => {}) }
  const module: PDFModule = { GlobalWorkerOptions: { workerSrc: '' }, getDocument: vi.fn(() => task) }
  vi.mocked(loadPDF).mockResolvedValue(module)
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue({} as CanvasRenderingContext2D)
  return { page, document, task, module, renderTask }
}

describe('preview engine lifetime', () => {
  it('never creates a PDF task when its module resolves after close', async () => {
    const { module } = pdfFixture(), pending = deferred<PDFModule>()
    vi.mocked(loadPDF).mockReturnValue(pending.promise)
    preview = mountPreview(root, 'one.pdf'); await flush()
    preview.destroy(); pending.resolve(module); await flush()
    expect(module.getDocument).not.toHaveBeenCalled()
    expect(root.querySelector('canvas')).toBeNull()
  })

  it('does not let an old document write into a new preview and destroys once', async () => {
    const { document, task } = pdfFixture(), pending = deferred<PDFDocument>()
    task.promise = pending.promise
    preview = mountPreview(root, 'one.pdf'); await flush(); preview.destroy()
    root.querySelector('#previewContent')!.textContent = 'new preview'
    preview.destroy(); pending.resolve(document); await flush()
    expect(task.destroy).toHaveBeenCalledOnce()
    expect(root.querySelector('#previewContent')!.textContent).toBe('new preview')
    expect(root.querySelector('.preview-pdf-tools')).toBeNull()
  })

  it('cleans up a page resolving after close without rendering', async () => {
    const { document, page } = pdfFixture(), pending = deferred<PDFPage>()
    vi.mocked(document.getPage).mockReturnValue(pending.promise)
    preview = mountPreview(root, 'one.pdf'); await flush(); preview.destroy()
    pending.resolve(page); await flush()
    expect(page.render).not.toHaveBeenCalled()
    expect(page.cleanup).toHaveBeenCalled()
  })

  it('supports pagination and zoom and cancels an active render on close', async () => {
    const { document, page, renderTask } = pdfFixture()
    preview = mountPreview(root, 'one.pdf'); await flush()
    expect(root.querySelector('#previewStatus')!.textContent).toContain('第 1 / 2 页')
    ;[...root.querySelectorAll('button')].find(button => button.textContent === '下一页')!.click()
    await flush(); expect(document.getPage).toHaveBeenLastCalledWith(2)
    const pending = deferred<void>(); renderTask.promise = pending.promise
    const zoom = root.querySelector('select[aria-label="PDF 缩放"]') as HTMLSelectElement
    zoom.value = '1.5'; zoom.dispatchEvent(new Event('change')); await flush()
    expect(page.render).toHaveBeenCalledTimes(3)
    preview.destroy(); expect(renderTask.cancel).toHaveBeenCalledOnce()
    pending.resolve(); await flush()
    expect(root.querySelector('canvas')).toBeNull()
  })

  it('terminates a spreadsheet worker and ignores results after close', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(new ArrayBuffer(4), { headers: { 'Content-Type': 'application/vnd.ms-excel' } })))
    const worker = { onmessage: null as ((event: MessageEvent) => void) | null, onerror: null, postMessage: vi.fn(), terminate: vi.fn() }
    vi.stubGlobal('Worker', class { constructor() { return worker } })
    preview = mountPreview(root, 'one.xls'); await flush()
    expect(worker.postMessage).toHaveBeenCalledOnce()
    preview.destroy(); await flush()
    worker.onmessage?.(new MessageEvent('message', { data: { id: 1, names: ['old'], hasMoreSheets: false } }))
    expect(worker.terminate).toHaveBeenCalledOnce()
    expect(root.querySelector('#previewContent')!.children).toHaveLength(0)
  })

  it('ignores a text body that finishes after close', async () => {
    const bytes = deferred<ArrayBuffer>()
    vi.stubGlobal('fetch', vi.fn().mockResolvedValueOnce(new Response(null, { headers: { 'Content-Type': 'text/plain' } })).mockResolvedValueOnce({ ok: true, arrayBuffer: () => bytes.promise }))
    preview = mountPreview(root, 'one.txt'); await flush(); preview.destroy()
    bytes.resolve(new TextEncoder().encode('old text').buffer); await flush()
    expect(root.querySelector('pre')).toBeNull()
    expect(root.querySelector('#textTools')!.classList.contains('hidden')).toBe(true)
  })
})
