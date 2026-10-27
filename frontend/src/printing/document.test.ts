import { File as NodeFile } from 'node:buffer'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { fitRect, loadPrintSource, loadSharedPrintFile, isPrintableFile, prepareDocument, rasterSize, type PrintLayout } from './document'
import { loadPDF } from '../preview/pdf'

vi.mock('../preview/pdf', () => ({ loadPDF: vi.fn() }))
const layout: PrintLayout = { width: 210, height: 297, left: 5, top: 5, printableWidth: 200, printableHeight: 287, key: 'layout' }
const file = (name: string, bytes: string | Uint8Array) => new NodeFile([bytes], name) as unknown as File
const context = { fillStyle: '', fillRect: vi.fn(), drawImage: vi.fn() }
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); vi.mocked(loadPDF).mockReset() })

describe('print page renderer', () => {
  it('contains portrait and landscape content without cropping and bounds raster memory', () => {
    expect(fitRect(400, 200, 200, 300)).toEqual({ width: 200, height: 100, x: 0, y: 100, scale: .5 })
    expect(fitRect(100, 400, 200, 300)).toEqual({ width: 75, height: 300, x: 62.5, y: 0, scale: .75 })
    expect(rasterSize(layout)).toEqual({ width: 1575, height: 2260 })
    expect(() => rasterSize({ ...layout, printableWidth: 2000 })).toThrow('过大')
    expect(() => fitRect(0, 100, 200, 300)).toThrow('尺寸无效')
  })
  it('rejects unsupported, renamed, empty, and oversized input before rendering', async () => {
    const signal = new AbortController().signal
    await expect(loadPrintSource(file('a.docx', 'x'), signal)).rejects.toThrow('仅支持')
    await expect(loadPrintSource(file('a.png', 'not png'), signal)).rejects.toThrow('不符')
    await expect(loadPrintSource(file('a.pdf', 'not pdf'), signal)).rejects.toThrow('有效的 PDF')
    await expect(loadPrintSource(file('a.jpg', ''), signal)).rejects.toThrow('空文件')
    await expect(loadPrintSource({ name: 'a.pdf', size: 51 * 1024 * 1024 } as File, signal)).rejects.toThrow('50 MiB')
  })
  it('uses EXIF-aware image decoding, white paper and the same render path for multipart pages', async () => {
    const bitmap = { width: 400, height: 200, close: vi.fn() }
    const decode = vi.fn(async () => bitmap)
    vi.stubGlobal('createImageBitmap', decode)
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(context as unknown as CanvasRenderingContext2D)
    vi.spyOn(HTMLCanvasElement.prototype, 'toBlob').mockImplementation(callback => callback(new Blob(['PNG'], { type: 'image/png' })))
    const input = file('photo.JPG', new Uint8Array([255, 216, 255]))
    const source = await loadPrintSource(input, new AbortController().signal)
    const canvas = document.createElement('canvas')
    await source.render(1, canvas, layout, new AbortController().signal)
    expect(decode).toHaveBeenCalledWith(input, { imageOrientation: 'from-image' })
    expect(context.fillStyle).toBe('#fff')
    expect(canvas.width).toBe(1575)
    const body = await prepareDocument(source, input.name, 'printer', layout, new AbortController().signal, vi.fn())
    expect(JSON.parse(body.get('manifest') as string)).toEqual({ printer: 'printer', name: 'photo.JPG', pages: 1, layoutKey: 'layout' })
    expect(body.getAll('page')).toHaveLength(1)
    expect((body.get('page') as File).name).toBe('001.png')
    source.close(); source.close()
    expect(bitmap.close).toHaveBeenCalledOnce()
  })
  it('renders PDF using print intent and closes its worker; rejects over-limit PDFs', async () => {
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(context as unknown as CanvasRenderingContext2D)
    const cleanup = vi.fn(), render = vi.fn((_options: unknown) => ({ promise: Promise.resolve(), cancel: vi.fn() })), destroy = vi.fn(async () => {})
    const pdf = { numPages: 2, getPage: vi.fn(async () => ({ cleanup, getViewport: ({ scale }: { scale: number }) => ({ width: 300 * scale, height: 300 * scale }), render })) }
    const getDocument = vi.fn(() => ({ promise: Promise.resolve(pdf), destroy }))
    vi.mocked(loadPDF).mockResolvedValue({ GlobalWorkerOptions: { workerSrc: '' }, getDocument })
    const source = await loadPrintSource(file('report.pdf', '%PDF-1.4'), new AbortController().signal)
    expect(source.pages).toBe(2)
    await source.render(2, document.createElement('canvas'), layout, new AbortController().signal)
    expect(render.mock.calls[0]?.[0]).toMatchObject({ intent: 'print', transform: [1, 0, 0, 1, 0, 342.5] })
    expect(cleanup).toHaveBeenCalledOnce()
    source.close(); expect(destroy).toHaveBeenCalledOnce()
    pdf.numPages = 51
    await expect(loadPrintSource(file('long.pdf', '%PDF-1.4'), new AbortController().signal)).rejects.toThrow('50 页')
    expect(destroy).toHaveBeenCalledTimes(2)
  })
  it('does not prepare any page after cancellation', async () => {
    const control = new AbortController(); control.abort()
    const render = vi.fn()
    await expect(prepareDocument({ pages: 2, render, close() {} }, 'a.pdf', 'p', layout, control.signal, vi.fn())).rejects.toMatchObject({ name: 'AbortError' })
    expect(render).not.toHaveBeenCalled()
  })
  it('reads a bounded shared snapshot with an encoded path and its original basename', async () => {
    const fetch = vi.fn(async () => new Response('%PDF-1.4', { headers: { 'Content-Type': 'application/pdf' } }))
    vi.stubGlobal('fetch', fetch)
    const path = '资料/预算 + 100% #.PDF', control = new AbortController()
    const snapshot = await loadSharedPrintFile(path, control.signal)
    expect(fetch).toHaveBeenCalledWith(`/preview-files/${encodeURIComponent(path)}`, { cache: 'no-store', signal: control.signal })
    expect(snapshot.name).toBe('预算 + 100% #.PDF')
    expect(snapshot.size).toBe(8)
    expect(snapshot.type).toBe('application/pdf')
    for (const name of ['a.pdf', 'a.JPG', 'a.JPEG', 'a.png']) expect(isPrintableFile(name)).toBe(true)
    for (const name of ['a.docx', 'a.pdf.exe', 'a.svg']) expect(isPrintableFile(name)).toBe(false)
  })
  it('rejects shared paths, missing files, oversized headers and oversized streams', async () => {
    const fetch = vi.fn()
    vi.stubGlobal('fetch', fetch)
    const signal = new AbortController().signal
    for (const path of ['../secret.pdf', '/a.pdf', 'a/../b.pdf', 'a\\b.pdf', 'a.docx']) await expect(loadSharedPrintFile(path, signal)).rejects.toThrow('无效')
    expect(fetch).not.toHaveBeenCalled()
    fetch.mockResolvedValueOnce(new Response('', { status: 404 }))
    await expect(loadSharedPrintFile('gone.pdf', signal)).rejects.toThrow('移动或删除')
    fetch.mockResolvedValueOnce(new Response('x', { headers: { 'Content-Length': String(51 * 1024 * 1024) } }))
    await expect(loadSharedPrintFile('large.pdf', signal)).rejects.toThrow('50 MiB')
    let chunks = 0
    const stream = new ReadableStream({ pull(controller) { if (chunks++ < 51) controller.enqueue(new Uint8Array(1024 * 1024)); else controller.close() } })
    fetch.mockResolvedValueOnce(new Response(stream))
    await expect(loadSharedPrintFile('large.pdf', signal)).rejects.toThrow('50 MiB')
  })

})
