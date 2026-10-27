import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import FileExplorer from './FileExplorer.vue'
import * as dialogs from '../ui/dialog'
import * as contextMenu from '../ui/contextMenu'

vi.mock('../ui/dialog', () => ({ confirm: vi.fn().mockResolvedValue(false), confirmText: vi.fn().mockResolvedValue(false) }))
vi.mock('../ui/contextMenu', () => ({ open: vi.fn(), close: vi.fn(), register: vi.fn(), registerPage: vi.fn() }))

const entries = [
  { name: 'zeta.txt', path: 'zeta.txt', directory: false, modified: '2026-10-25T00:00:00Z', size: 10 },
  { name: 'beta', path: 'beta', directory: true, modified: '2026-10-24T00:00:00Z', size: 0 },
  { name: 'alpha.txt', path: 'alpha.txt', directory: false, modified: '2026-10-23T00:00:00Z', size: 2 },
  { name: 'recent.pdf', path: 'recent.pdf', directory: false, previewType: 'application/pdf', modified: new Date().toISOString(), size: 2048 },
]
const response = (body: unknown, status = 200) => ({ ok: status >= 200 && status < 300, status, json: async () => body })
let fetchMock: ReturnType<typeof vi.fn>
let wrapper: ReturnType<typeof mount>

beforeEach(async () => {
  Object.defineProperty(HTMLDialogElement.prototype, 'showModal', { configurable: true, value() { this.open = true } })
  Object.defineProperty(HTMLDialogElement.prototype, 'close', { configurable: true, value() { this.open = false; this.dispatchEvent(new Event('close')) } })
  window.location.hash = ''
  localStorage.clear()
  class TestEventSource { static OPEN = 1; readyState = 0; onopen: (() => void) | null = null; onerror: (() => void) | null = null; addEventListener() {}; close() {} }
  vi.stubGlobal('EventSource', TestEventSource)
  Object.defineProperty(window, 'EventSource', { configurable: true, value: TestEventSource })
  fetchMock = vi.fn(async (url: string) => {
    if (url.startsWith('/api/filesystem?')) return response({ entries: url.includes('path=beta') ? [] : entries })
    return response({})
  })
  vi.stubGlobal('fetch', fetchMock)
  wrapper = mount(FileExplorer, { attachTo: document.body })
  await flushPromises()
})

afterEach(() => { wrapper?.unmount(); vi.unstubAllGlobals(); window.location.hash = '' })

describe('FileExplorer', () => {
  it('reserves the empty area on initial load and keeps it visible during revalidation', async () => {
    wrapper.unmount()
    // Drain jsdom's pending history event from the test setup before remounting.
    await flushPromises()
    let finish!: (value: ReturnType<typeof response>) => void
    fetchMock.mockImplementation(() => new Promise(resolve => { finish = resolve }))
    wrapper = mount(FileExplorer, { attachTo: document.body })
    expect(wrapper.get('.empty').isVisible()).toBe(true)
    expect(wrapper.get('.empty').text()).toContain('正在读取文件列表')
    finish(response({ entries: [] }))
    await flushPromises()
    const empty = wrapper.get('.empty').element
    expect(wrapper.get('.empty').text()).toContain('这个文件夹还是空的')
    const menu = vi.mocked(contextMenu.registerPage).mock.calls.at(-1)![0]()
    const refreshing = menu.find(item => item.label === '刷新文件列表')!.action!()
    await flushPromises()
    expect(wrapper.get('.empty').element).toBe(empty)
    expect(wrapper.get('.empty').isVisible()).toBe(true)
    expect(wrapper.get('.empty').text()).not.toContain('正在读取文件列表')
    finish(response({ entries: [] }))
    await refreshing
    await flushPromises()
    expect(wrapper.get('.empty').text()).toContain('这个文件夹还是空的')
  })

  it('loads rows with directories first, sorts by name, and navigates into a folder', async () => {
    const names = wrapper.findAll('.file-name').map(element => element.text())
    expect(names).toEqual(['beta', 'alpha.txt', 'recent.pdf', 'zeta.txt'])
    expect(wrapper.find('.recent-tag').text()).toBe('新')
    await wrapper.get('[data-sort="name"]').trigger('click')
    expect(wrapper.findAll('.file-name').map(element => element.text())).toEqual(['beta', 'zeta.txt', 'recent.pdf', 'alpha.txt'])
    await wrapper.get('.folder-name').trigger('click')
    await flushPromises()
    expect(fetchMock).toHaveBeenLastCalledWith(expect.stringContaining('path=beta'), expect.objectContaining({ cache: 'no-store' }))
    expect(wrapper.get('.breadcrumbs').text()).toContain('beta')
  })

  it('clears row selection when clicking outside the file rows', async () => {
    const row = wrapper.get('.explorer-row[data-path="zeta.txt"]')
    await row.trigger('click')
    expect(row.classes()).toContain('selected')
    await wrapper.get('.explorer-navigation').trigger('click')
    expect(row.classes()).not.toContain('selected')
  })

  it('debounces recursive search and highlights matching names', async () => {
    vi.useFakeTimers()
    await wrapper.get('[aria-label="搜索当前文件夹及子文件夹"]').setValue('alpha')
    vi.advanceTimersByTime(220)
    await flushPromises()
    expect(fetchMock).toHaveBeenLastCalledWith(expect.stringContaining('q=alpha'), expect.objectContaining({ cache: 'no-store' }))
    expect(wrapper.get('mark').text()).toBe('alpha')
    vi.useRealTimers()
  })

  it('requires a second confirmation before recursively deleting a non-empty directory', async () => {
    vi.mocked(dialogs.confirm).mockResolvedValue(true)
    fetchMock.mockImplementationOnce(async () => response({ error: '目录包含内容', requiresConfirmation: true }, 409))
    fetchMock.mockImplementationOnce(async () => response({}))
    const row = wrapper.find('.explorer-row[data-path="beta"]').element
    const resolveMenu = vi.mocked(contextMenu.register).mock.calls[0][1]
    const actions = resolveMenu(new MouseEvent('contextmenu'), row)
    await actions.find(action => action.label === '删除')!.action?.()
    await flushPromises()
    const deleteCalls = fetchMock.mock.calls.filter(([url]) => url === '/api/filesystem/delete')
    expect(deleteCalls).toHaveLength(2)
    expect(JSON.parse(deleteCalls[0][1]?.body as string)).toEqual({ path: 'beta' })
    expect(JSON.parse(deleteCalls[1][1]?.body as string)).toEqual({ path: 'beta', recursive: true })
    expect(vi.mocked(dialogs.confirm)).toHaveBeenCalledTimes(2)
  })

  it('sends rename and mkdir operations with the existing API shape', async () => {
    await wrapper.get('.file-row').trigger('click')
    await wrapper.get('.row-menu-button').trigger('click')
    const resolver = vi.mocked(contextMenu.register).mock.calls[0][1]
    const item = wrapper.find('.explorer-row').element
    const menu = resolver(new MouseEvent('contextmenu'), item)
    const rename = menu.find((action: { label?: string }) => action.label === '重命名')
    expect(rename).toBeTruthy()
    await rename!.action?.()
    await flushPromises()
    const input = document.body.querySelector<HTMLInputElement>('#entryName')!
    input.value = 'renamed'; input.dispatchEvent(new Event('input', { bubbles: true }))
    document.body.querySelector('.fs-dialog form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
    await flushPromises()
    expect(fetchMock).toHaveBeenCalledWith('/api/filesystem/rename', expect.objectContaining({ method: 'POST', body: JSON.stringify({ path: 'beta', destination: '', name: 'renamed' }) }))
  })
  it('opens printing only from the supported single-file menu action', async () => {
    const row = wrapper.get('.explorer-row[data-path="recent.pdf"]')
    await row.trigger('click')
    expect(wrapper.emitted('print')).toBeUndefined()
    await row.get('.row-menu-button').trigger('click')
    const actions = vi.mocked(contextMenu.open).mock.calls.at(-1)![0]
    await actions.find(action => action.label === '打印')!.action!()
    expect(wrapper.emitted('print')).toEqual([['recent.pdf']])
    for (const path of ['alpha.txt', 'beta']) {
      await wrapper.get(`.explorer-row[data-path="${path}"] .row-menu-button`).trigger('click')
      expect(vi.mocked(contextMenu.open).mock.calls.at(-1)![0].some(action => action.label === '打印')).toBe(false)
    }
  })

})
