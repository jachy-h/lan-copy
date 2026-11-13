import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import FragmentSaveDialog from './FragmentSaveDialog.vue'

vi.mock('../ui/fileUi', () => ({ notify: vi.fn(), raiseToast: vi.fn() }))

const item = { id: '0011223344556677', kind: 'text' as const, name: '碎片.txt' }
const response = (body: unknown, status = 200) => ({ ok: status >= 200 && status < 300, status, json: async () => body })
let fetchMock: ReturnType<typeof vi.fn>
let wrapper: ReturnType<typeof mount>

beforeEach(() => {
  localStorage.clear()
  Object.defineProperty(HTMLDialogElement.prototype, 'showModal', { configurable: true, value() { this.open = true } })
  Object.defineProperty(HTMLDialogElement.prototype, 'close', { configurable: true, value() { this.open = false; this.dispatchEvent(new Event('close')) } })
  fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
    if (url === '/api/filesystem?path=') return response({ entries: [{ name: '相册', path: '相册', directory: true }, { name: 'file.txt', path: 'file.txt', directory: false }] })
    if (url === '/api/filesystem?path=%E7%9B%B8%E5%86%8C') return response({ entries: [] })
    if (url === '/api/texts/0011223344556677/save' && init?.method === 'POST') return response({ path: '相册/我的笔记.txt', name: '我的笔记.txt' }, 201)
    throw new Error(`Unexpected request: ${url}`)
  })
  vi.stubGlobal('fetch', fetchMock)
  wrapper = mount(FragmentSaveDialog, { attachTo: document.body, props: { open: true, item } })
})
afterEach(() => { wrapper?.unmount(); vi.unstubAllGlobals(); vi.clearAllMocks() })

describe('FragmentSaveDialog', () => {
  it('reveals and selects directories from the shared resource tree', async () => {
    await flushPromises()
    const treeFolder = wrapper.findAll('.fragment-save-tree .tree-label').find(label => label.text().includes('相册'))
    expect(treeFolder).toBeTruthy()
    await treeFolder!.trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('全部文件 / 相册')
    expect(wrapper.get('.fragment-save-tree .tree-row.active').text()).toContain('相册')
  })

  it('browses folders, edits the file name and saves into the selected shared directory', async () => {
    await flushPromises()
    expect(wrapper.text()).toContain('全部文件')
    expect(wrapper.text()).toContain('相册')
    expect(wrapper.find('.folder-choice').text()).toContain('相册')
    expect((wrapper.get('.fragment-save-name input').element as HTMLInputElement).value).toMatch(/^碎片-\d{8}-\d{4}\.txt$/)

    await wrapper.get('.folder-choice').trigger('click')
    await flushPromises()
    expect(fetchMock).toHaveBeenCalledWith('/api/filesystem?path=%E7%9B%B8%E5%86%8C', expect.anything())
    expect(wrapper.text()).toContain('全部文件 / 相册')

    await wrapper.get('.fragment-save-name input').setValue('我的笔记.txt')
    await wrapper.get('.fragment-save-form').trigger('submit')
    await flushPromises()
    const save = fetchMock.mock.calls.find(([url, init]) => url === '/api/texts/0011223344556677/save' && init?.method === 'POST')
    expect(JSON.parse(String(save?.[1]?.body))).toEqual({ path: '相册', name: '我的笔记.txt' })
    expect(wrapper.emitted('saved')?.[0]).toEqual(['相册/我的笔记.txt'])
    expect(localStorage.getItem('lan-copy:fragment-save-path')).toBe('相册')
  })

  it('does not close from a backdrop click while saving', async () => {
    let finish!: (value: ReturnType<typeof response>) => void
    fetchMock.mockImplementation(async (url: string) => {
      if (url.startsWith('/api/filesystem?')) return response({ entries: [] })
      if (url === '/api/texts/0011223344556677/save') return new Promise(resolve => { finish = resolve })
      throw new Error(`Unexpected request: ${url}`)
    })
    wrapper.unmount()
    wrapper = mount(FragmentSaveDialog, { attachTo: document.body, props: { open: true, item } })
    await flushPromises()
    await wrapper.get('.fragment-save-form').trigger('submit')
    await flushPromises()
    expect(wrapper.find('.fragment-save-dialog').exists()).toBe(true)
    await wrapper.get('.fragment-save-dialog').trigger('click')
    await flushPromises()
    expect(wrapper.find('.fragment-save-dialog').exists()).toBe(true)
    finish(response({ path: '碎片.txt', name: '碎片.txt' }, 201))
    await flushPromises()
  })
})
