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
  root.innerHTML = '<p id="previewStatus"></p><div id="previewTools"></div><div id="textTools" class="hidden"><select id="textEncoding"><option value="utf-8">UTF-8</option><option value="gb18030">GB18030</option><option value="utf-16le">UTF-16 LE</option><option value="utf-16be">UTF-16 BE</option></select></div><div id="previewContent"></div>'
  document.body.append(root)
  vi.stubGlobal('IntersectionObserver', class { observe() {} disconnect() {} })
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
  it('exposes module failures and browser diagnostics safely and removes them on close', async () => {
    vi.mocked(loadPDF).mockRejectedValue(new TypeError('Promise.withResolvers is not a function <script>'))
    preview = mountPreview(root, 'one.pdf'); await flush()
    expect(root.querySelector('#previewStatus')!.textContent).toContain('加载 PDF 组件')
    expect(root.querySelector('.preview-error-details')!.textContent).toContain('Promise.withResolvers')
    expect(root.querySelector('.preview-error-details')!.textContent).toContain('浏览器：')
    expect(root.querySelector('script')).toBeNull()
    preview.destroy()
    expect(root.querySelector('.preview-error-details')).toBeNull()
    expect((root.querySelector('#previewStatus') as HTMLElement).hidden).toBe(false)
  })

  it('keeps HTTP failures distinguishable from damaged documents', async () => {
    vi.mocked(fetch).mockResolvedValue(new Response(null, { status: 503 }))
    preview = mountPreview(root, 'one.pdf'); await flush()
    expect(root.querySelector('.preview-error-details')!.textContent).toContain('HTTP 503')
    expect(loadPDF).not.toHaveBeenCalled()
  })

  it('continues reading without IntersectionObserver', async () => {
    pdfFixture()
    vi.stubGlobal('IntersectionObserver', undefined)
    preview = mountPreview(root, 'one.pdf'); await flush()
    expect(root.querySelectorAll('canvas')).toHaveLength(2)
  })

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

  it('renders nearby pages continuously and cancels rendering on close', async () => {
    const { document, page, renderTask } = pdfFixture()
    let intersect!: (entries: Partial<IntersectionObserverEntry>[]) => void
    const disconnect = vi.fn()
    vi.stubGlobal('IntersectionObserver', class {
      constructor(callback: typeof intersect) { intersect = callback }
      observe() {} disconnect = disconnect
    })
    preview = mountPreview(root, 'one.pdf'); await flush()
    expect((root.querySelector('select[aria-label="PDF 缩放"]') as HTMLSelectElement).value).toBe('1')
    const frames = [...root.querySelectorAll('.preview-pdf-page')]
    expect(frames).toHaveLength(2)
    expect(page.render).not.toHaveBeenCalled()
    intersect(frames.map(target => ({target, isIntersecting: true})))
    await flush()
    expect(root.querySelectorAll('canvas')).toHaveLength(2)
    expect(document.getPage).toHaveBeenLastCalledWith(2)
    intersect([{target: frames[0], isIntersecting: false}])
    expect(frames[0].querySelector('canvas')).toBeNull()
    const pending = deferred<void>(); renderTask.promise = pending.promise
    const zoom = root.querySelector('select[aria-label="PDF 缩放"]') as HTMLSelectElement
    zoom.value = '1.5'; zoom.dispatchEvent(new Event('change')); await flush()
    preview.destroy(); expect(renderTask.cancel).toHaveBeenCalled()
    expect(disconnect).toHaveBeenCalledOnce()
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

  function delimitedFetch(body: Uint8Array<ArrayBuffer>, total = body.byteLength) {
    return vi.fn(async (_url: string, init?: RequestInit) => init?.method === 'HEAD'
      ? new Response(null, { headers: { 'Content-Type': 'text/plain; charset=utf-8', 'Content-Length': String(total) } })
      : new Response(body, { headers: { 'Content-Type': 'text/plain; charset=utf-8' } }))
  }

  it('renders a CSV file as a table and toggles back to raw text', async () => {
    const body = new TextEncoder().encode('id,name\n1,"a, b"\n2,Carol\n')
    vi.stubGlobal('fetch', delimitedFetch(body))
    preview = mountPreview(root, 'data/orders.csv'); await flush()
    const table = root.querySelector('.preview-sheet-table')!
    expect(table).not.toBeNull()
    expect([...table.querySelectorAll('thead th')].map(cell => cell.textContent)).toEqual(['', 'id', 'name'])
    expect([...table.querySelectorAll('tbody tr')].map(row => [...row.children].map(cell => cell.textContent))).toEqual([
      ['1', '1', 'a, b'],
      ['2', '2', 'Carol'],
    ])
    expect(root.querySelector('pre.preview-text')).toBeNull()
    expect(root.querySelector('#textTools')!.classList.contains('hidden')).toBe(false)
    expect(root.querySelector('#previewStatus')!.textContent).toContain('CSV 表格 · 2 行 × 2 列')
    const toggle = root.querySelector<HTMLButtonElement>('.preview-csv-tools button')!
    expect(toggle.textContent).toBe('原始文本')
    toggle.click(); await flush()
    expect(root.querySelector('.preview-sheet-table')).toBeNull()
    expect(root.querySelector('pre.preview-text')!.textContent).toContain('id,name')
    expect(root.querySelector('#previewStatus')!.textContent).toContain('CSV 原始文本')
    expect(toggle.textContent).toBe('表格')
    toggle.click(); await flush()
    expect(root.querySelectorAll('.preview-sheet-table tbody tr')).toHaveLength(2)
  })

  it('renders TSV files with the tab delimiter', async () => {
    const body = new TextEncoder().encode('id\tname\n1\tAlice\n')
    vi.stubGlobal('fetch', delimitedFetch(body))
    preview = mountPreview(root, 'data/orders.tsv'); await flush()
    expect([...root.querySelectorAll('.preview-sheet-table tbody tr')].map(row => [...row.children].map(cell => cell.textContent))).toEqual([['1', '1', 'Alice']])
    expect(root.querySelector('#previewStatus')!.textContent).toContain('TSV 表格')
  })

  it('drops the incomplete last record when a CSV read is truncated', async () => {
    const body = new TextEncoder().encode('id,name\n1,Alice\n2,Par')
    vi.stubGlobal('fetch', delimitedFetch(body, body.byteLength + 10))
    preview = mountPreview(root, 'data/orders.csv'); await flush()
    expect(root.querySelectorAll('.preview-sheet-table tbody tr')).toHaveLength(1)
    expect(root.querySelector('#previewStatus')!.textContent).toContain('仅读取前 1 MiB')
  })

  it('detects a UTF-16LE BOM in CSV previews', async () => {
    const text = 'id,name\n1,中文\n'
    const body = new Uint8Array(2 + text.length * 2)
    body.set([0xff, 0xfe])
    for (let index = 0; index < text.length; index++) {
      const code = text.charCodeAt(index)
      body[2 + index * 2] = code & 0xff
      body[3 + index * 2] = code >> 8
    }
    vi.stubGlobal('fetch', delimitedFetch(body))
    preview = mountPreview(root, 'data/orders.csv'); await flush()
    expect((root.querySelector('#textEncoding') as HTMLSelectElement).value).toBe('utf-16le')
    expect(root.querySelector('.preview-sheet-table tbody tr td:nth-child(3)')!.textContent).toBe('中文')
  })

  it('shows an empty state for a CSV without any records', async () => {
    vi.stubGlobal('fetch', delimitedFetch(new TextEncoder().encode('\n\n')))
    preview = mountPreview(root, 'data/empty.csv'); await flush()
    expect(root.querySelector('.preview-sheet-table')).toBeNull()
    expect(root.querySelector('#previewContent')!.textContent).toContain('空表格')
    expect(root.querySelector('#previewStatus')!.textContent).toContain('为空')
  })
})
