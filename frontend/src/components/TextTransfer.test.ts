import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import TextTransfer from './TextTransfer.vue'
import * as fileUi from '../ui/fileUi'
import * as dialogs from '../ui/dialog'

vi.mock('../ui/fileUi', () => ({ notify: vi.fn(), copyText: vi.fn().mockResolvedValue(true) }))
vi.mock('../ui/dialog', () => ({ confirm: vi.fn().mockResolvedValue(true), confirmText: vi.fn().mockResolvedValue(false) }))
vi.mock('../ui/contextMenu', () => ({ open: vi.fn(), close: vi.fn(), register: vi.fn(), registerPage: vi.fn() }))

const sample = { id: 'text-1', content: 'hello lan', created: new Date().toISOString() }
const response = (body: unknown, status = 200) => ({ ok: status >= 200 && status < 300, status, json: async () => body })
let fetchMock: ReturnType<typeof vi.fn>
let wrapper: ReturnType<typeof mount>

beforeEach(async () => {
  class TestEventSource { static OPEN = 1; readyState = 0; onopen: (() => void) | null = null; onerror: (() => void) | null = null; addEventListener() {}; close() {} }
  vi.stubGlobal('EventSource', TestEventSource)
  Object.defineProperty(window, 'EventSource', { configurable: true, value: TestEventSource })
  fetchMock = vi.fn(async (url: string) => url === '/api/texts' ? response({ texts: [sample] }) : response({}))
  vi.stubGlobal('fetch', fetchMock)
  wrapper = mount(TextTransfer, { attachTo: document.body })
  await flushPromises()
})
afterEach(() => { wrapper?.unmount(); vi.unstubAllGlobals() })

describe('TextTransfer', () => {
  it('reserves the initial empty area and refreshes an empty list without replacing its message', async () => {
    wrapper.unmount()
    let finish!: (value: ReturnType<typeof response>) => void
    fetchMock.mockImplementation(() => new Promise(resolve => { finish = resolve }))
    wrapper = mount(TextTransfer, { attachTo: document.body })
    expect(wrapper.get('#textEmptyState').isVisible()).toBe(true)
    expect(wrapper.get('#textEmptyState').text()).toContain('正在读取共享文本')
    finish(response({ texts: [] }))
    await flushPromises()
    const empty = wrapper.get('#textEmptyState').element
    await wrapper.get('#refreshBtn').trigger('click')
    expect(wrapper.get('#textEmptyState').element).toBe(empty)
    expect(wrapper.get('#textEmptyState').isVisible()).toBe(true)
    expect(wrapper.get('#textEmptyState').text()).toContain('还没有共享文本')
    expect(wrapper.get('#textSummary').text()).toBe('等待文字传入')
    finish(response({ texts: [] }))
    await flushPromises()
  })

  it('counts UTF-8 bytes and rejects content over the 64 KiB server limit', async () => {
    const input = wrapper.get('#textInput')
    await input.setValue('界'.repeat(21846)) // 65,538 UTF-8 bytes
    expect(wrapper.get('#textCount').classes()).toContain('over-limit')
    await wrapper.get('#shareTextBtn').trigger('click')
    expect(fetchMock.mock.calls.some(([url, init]) => url === '/api/texts' && init?.method === 'POST')).toBe(false)
    expect(vi.mocked(fileUi.notify)).toHaveBeenCalledWith('文本超过 64 KiB 限制', true)
  })

  it('publishes text as JSON and refreshes after a successful response', async () => {
    await wrapper.get('#textInput').setValue('command line')
    await wrapper.get('#shareTextBtn').trigger('click')
    await flushPromises()
    expect(fetchMock).toHaveBeenCalledWith('/api/texts', expect.objectContaining({ method: 'POST', body: JSON.stringify({ content: 'command line' }) }))
    expect((wrapper.get('#textInput').element as HTMLTextAreaElement).value).toBe('')
    expect(vi.mocked(fileUi.notify)).toHaveBeenCalledWith('文本已共享')
  })

  it('copies a shared item and confirms deletion before calling the API', async () => {
    await wrapper.get('[data-text-action="copy"]').trigger('click')
    expect(vi.mocked(fileUi.copyText)).toHaveBeenCalledWith(sample.content)
    await wrapper.get('[data-text-action="delete"]').trigger('click')
    await flushPromises()
    expect(vi.mocked(dialogs.confirm)).toHaveBeenCalledWith(expect.objectContaining({ danger: true, confirmText: '删除' }))
    expect(fetchMock).toHaveBeenCalledWith('/api/texts/text-1', expect.objectContaining({ method: 'DELETE' }))
  })
})
