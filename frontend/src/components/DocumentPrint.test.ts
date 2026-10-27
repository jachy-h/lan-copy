import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import DocumentPrint from './DocumentPrint.vue'
import { requestJSON } from '../api/client'
import { loadPrintSource, loadSharedPrintFile, prepareDocument } from '../printing/document'

vi.mock('../api/client', () => ({ requestJSON: vi.fn() }))
vi.mock('../printing/document', () => ({ loadPrintSource: vi.fn(), loadSharedPrintFile: vi.fn(), prepareDocument: vi.fn() }))
const layout = { width: 210, height: 297, left: 5, top: 5, printableWidth: 200, printableHeight: 287, key: 'key' }
const render = vi.fn(async () => {}), close = vi.fn()
let wrapper: ReturnType<typeof mount>
beforeEach(() => {
  vi.mocked(loadSharedPrintFile).mockReset(); vi.mocked(requestJSON).mockReset(); vi.mocked(loadPrintSource).mockReset(); vi.mocked(prepareDocument).mockReset(); render.mockClear(); close.mockClear()
  vi.mocked(requestJSON).mockResolvedValue(layout)
  vi.mocked(loadPrintSource).mockResolvedValue({ pages: 2, render, close })
  vi.mocked(prepareDocument).mockResolvedValue(new FormData())
})
afterEach(() => wrapper?.unmount())
function setup() { wrapper = mount(DocumentPrint, { props: { printer: 'USB', enabled: true, offline: false, revision: 1 } }) }
async function pick(name = 'report.pdf') {
  const input = wrapper.get('input').element as HTMLInputElement
  Object.defineProperty(input, 'files', { configurable: true, value: [new File(['%PDF-'], name)] })
  await wrapper.get('input').trigger('change'); await flushPromises()
}
describe('document print', () => {
  it('requires a ready preview, supports PDF pagination, and submits only after confirmation', async () => {
    setup(); await flushPromises()
    expect(wrapper.get('.document-submit').attributes('disabled')).toBeDefined()
    await pick()
    expect(render).toHaveBeenCalled()
    expect(wrapper.get('.document-submit').attributes('disabled')).toBeUndefined()
    const renders = render.mock.calls.length
    await wrapper.get('[aria-label="放大打印预览"]').trigger('click')
    expect(wrapper.get('.document-preview-paper').attributes('style')).toContain('width: 390px')
    expect(render.mock.calls).toHaveLength(renders)
    await wrapper.get('[aria-label="打印预览下一页"]').trigger('click'); await flushPromises()
    expect(wrapper.text()).toContain('第 2 / 2 页')
    const job = { id: 1, status: 'queued', name: 'report.pdf', pages: 2 }
    vi.mocked(requestJSON).mockResolvedValueOnce(job)
    await wrapper.get('.document-submit').trigger('click'); await flushPromises()
    expect(prepareDocument).toHaveBeenCalledOnce()
    expect(wrapper.emitted('submitted')?.[0]).toEqual([job])
    expect(wrapper.emitted('busy')?.map(event => event[0])).toEqual([true, false])
    expect(wrapper.text()).toContain('已创建 2 页打印任务')
  })
  it('invalidates the old preview if layout refresh fails and closes resources on file replacement', async () => {
    setup(); await flushPromises(); await pick()
    vi.mocked(requestJSON).mockRejectedValueOnce(new Error('printer offline'))
    await wrapper.setProps({ printer: 'Other' }); await flushPromises()
    expect(wrapper.get('.document-submit').attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('printer offline')
    await pick('new.pdf')
    expect(close).toHaveBeenCalled()
    expect(wrapper.get('.document-submit').attributes('disabled')).toBeDefined()
  })
  it('shows file errors and prevents submission after shutdown', async () => {
    setup(); await flushPromises()
    vi.mocked(loadPrintSource).mockRejectedValueOnce(new Error('仅支持 PDF、JPG/JPEG、PNG'))
    await pick('bad.docx')
    expect(wrapper.text()).toContain('仅支持 PDF')
    await pick(); await wrapper.setProps({ offline: true })
    expect(wrapper.get('.document-submit').attributes('disabled')).toBeDefined()
  })
  it('loads a shared file into preview without submitting and preserves a local replacement', async () => {
    const shared = new File(['%PDF-'], '共享.pdf')
    vi.mocked(loadSharedPrintFile).mockResolvedValue(shared)
    wrapper = mount(DocumentPrint, { props: { printer: 'USB', enabled: true, offline: false, revision: 1, sharedPath: '资料/共享.pdf' } })
    await flushPromises()
    expect(loadSharedPrintFile).toHaveBeenCalledWith('资料/共享.pdf', expect.any(AbortSignal))
    expect(loadPrintSource).toHaveBeenCalledWith(shared, expect.any(AbortSignal))
    expect(wrapper.get('.document-shared-origin').text()).toContain('资料/共享.pdf')
    expect(wrapper.get('.document-submit').attributes('disabled')).toBeUndefined()
    expect(prepareDocument).not.toHaveBeenCalled()
    await pick('local.pdf')
    await wrapper.setProps({ sharedPath: '' }); await flushPromises()
    expect(wrapper.find('.document-shared-origin').exists()).toBe(false)
    expect(wrapper.get('.print-file-picker').text()).toContain('local.pdf')
    expect(wrapper.get('.document-submit').attributes('disabled')).toBeUndefined()
    expect(wrapper.emitted('localFile')).toHaveLength(1)
  })
  it('shows shared-file failures and never lets an old read overwrite a newer selection', async () => {
    let finish!: (file: File) => void
    vi.mocked(loadSharedPrintFile).mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    wrapper = mount(DocumentPrint, { props: { printer: 'USB', enabled: true, offline: false, revision: 1, sharedPath: 'old.pdf' } })
    await flushPromises()
    expect(wrapper.get('.document-submit').attributes('disabled')).toBeDefined()
    await pick('new.pdf')
    finish(new File(['%PDF-'], 'old.pdf')); await flushPromises()
    expect(wrapper.get('.print-file-picker').text()).toContain('new.pdf')
    expect(loadPrintSource).toHaveBeenCalledTimes(1)
    vi.mocked(loadSharedPrintFile).mockRejectedValueOnce(new Error('共享文件已被移动或删除'))
    await wrapper.setProps({ sharedPath: 'missing.pdf' }); await flushPromises()
    expect(wrapper.text()).toContain('共享文件已被移动或删除')
    expect(wrapper.get('.document-submit').attributes('disabled')).toBeDefined()
  })

})
