import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import FragmentTransfer from './FragmentTransfer.vue'
import * as fileUi from '../ui/fileUi'
import * as dialogs from '../ui/dialog'

vi.mock('../ui/fileUi', () => ({ notify: vi.fn(), copyText: vi.fn().mockResolvedValue(true), raiseToast: vi.fn() }))
vi.mock('../ui/dialog', () => ({ confirm: vi.fn().mockResolvedValue(true), confirmText: vi.fn().mockResolvedValue(false) }))
vi.mock('../ui/contextMenu', () => ({ open: vi.fn(), close: vi.fn(), register: vi.fn(), registerPage: vi.fn() }))

const sample = { id: 'text-1', content: 'hello lan', created: new Date().toISOString() }
const response = (body: unknown, status = 200) => ({ ok: status >= 200 && status < 300, status, json: async () => body })
let fetchMock: ReturnType<typeof vi.fn>
let wrapper: ReturnType<typeof mount>

beforeEach(async () => {
  Object.defineProperty(HTMLDialogElement.prototype, 'showModal', { configurable: true, value() { this.open = true } })
  Object.defineProperty(HTMLDialogElement.prototype, 'close', { configurable: true, value() { this.open = false; this.dispatchEvent(new Event('close')) } })
  class TestEventSource { static OPEN = 1; readyState = 0; onopen: (() => void) | null = null; onerror: (() => void) | null = null; addEventListener() {}; close() {} }
  vi.stubGlobal('EventSource', TestEventSource)
  Object.defineProperty(window, 'EventSource', { configurable: true, value: TestEventSource })
  fetchMock = vi.fn(async (url: string) => url === '/api/texts' ? response({ texts: [sample] }) : response({}))
  vi.stubGlobal('fetch', fetchMock)
  wrapper = mount(FragmentTransfer, { attachTo: document.body })
  await flushPromises()
})
afterEach(() => { wrapper?.unmount(); vi.unstubAllGlobals() })

describe('FragmentTransfer', () => {
  it('reserves the initial empty area and refreshes an empty list without replacing its message', async () => {
    wrapper.unmount()
    let finish!: (value: ReturnType<typeof response>) => void
    fetchMock.mockImplementation(() => new Promise(resolve => { finish = resolve }))
    wrapper = mount(FragmentTransfer, { attachTo: document.body })
    expect(wrapper.get('#textEmptyState').isVisible()).toBe(true)
    expect(wrapper.get('#textEmptyState').text()).toContain('正在读取内容')
    finish(response({ texts: [] }))
    await flushPromises()
    const empty = wrapper.get('#textEmptyState').element
    await wrapper.get('#refreshBtn').trigger('click')
    expect(wrapper.get('#textEmptyState').element).toBe(empty)
    expect(wrapper.get('#textEmptyState').isVisible()).toBe(true)
    expect(wrapper.get('#textEmptyState').text()).toContain('还没有保存碎片')
    expect(wrapper.get('#textSummary').text()).toBe('等待内容传入')
    finish(response({ texts: [] }))
    await flushPromises()
  })

  it('counts characters and rejects content over the 100,000 character limit', async () => {
    const input = wrapper.get('#textInput')
    await input.setValue('界'.repeat(100001))
    expect(wrapper.get('#textCount').classes()).toContain('over-limit')
    await wrapper.get('#shareTextBtn').trigger('click')
    expect(fetchMock.mock.calls.some(([url, init]) => url === '/api/texts' && init?.method === 'POST')).toBe(false)
    expect(vi.mocked(fileUi.notify)).toHaveBeenCalledWith('文本超过 100,000 字限制', true)
  })

  it('does not add the same clipboard image twice from files and items', async () => {
    const first = new File([new Uint8Array([137, 80, 78, 71])], 'image.png', { type: 'image/png', lastModified: 1 })
    const second = new File([new Uint8Array([137, 80, 78, 71])], 'image.png', { type: 'image/png', lastModified: 2 })
    const event = new Event('paste', { bubbles: true }) as ClipboardEvent
    Object.defineProperty(event, 'clipboardData', { value: { files: [first], items: [{ kind: 'file', type: 'image/png', getAsFile: () => second }] } })
    document.dispatchEvent(event)
    await flushPromises()
    expect(wrapper.findAll('.fragment-attachment')).toHaveLength(1)
  })

  it('removes a pending image before pasting a replacement', async () => {
    const paste = (file: File) => {
      const event = new Event('paste', { bubbles: true }) as ClipboardEvent
      Object.defineProperty(event, 'clipboardData', { value: { files: [], items: [{ kind: 'file', type: file.type, getAsFile: () => file }] } })
      document.dispatchEvent(event)
    }
    paste(new File([new Uint8Array([137, 80, 78, 71])], 'image.png', { type: 'image/png', lastModified: 1 }))
    await flushPromises()
    expect(wrapper.findAll('.fragment-attachment')).toHaveLength(1)
    await wrapper.get('.fragment-attachment-remove').trigger('click')
    expect(wrapper.findAll('.fragment-attachment')).toHaveLength(0)
    paste(new File([new Uint8Array([137, 80, 78, 72])], 'image.png', { type: 'image/png', lastModified: 2 }))
    await flushPromises()
    expect(wrapper.findAll('.fragment-attachment')).toHaveLength(1)
  })

  it('renders safe Markdown and detected links as new-tab anchors', async () => {
    wrapper.unmount()
    fetchMock.mockImplementation(async () => response({ texts: [{ ...sample, content: 'see https://example.com/a，和 www.example.org。 **重点** `code` <script>alert(1)</script>' }] }))
    wrapper = mount(FragmentTransfer, { attachTo: document.body })
    await flushPromises()
    const links = wrapper.findAll('.text-content a')
    expect(links.map(link => link.attributes('href'))).toEqual(['https://example.com/a', 'https://www.example.org'])
    expect(links.every(link => link.attributes('target') === '_blank' && link.attributes('rel') === 'noopener noreferrer')).toBe(true)
    expect(wrapper.get('.text-content strong').text()).toBe('重点')
    expect(wrapper.get('.text-content code').text()).toBe('code')
    expect(wrapper.find('.text-content script').exists()).toBe(false)
    expect(wrapper.get('.text-content').text()).toContain('<script>alert(1)</script>')
  })

  it('publishes text as JSON and refreshes after a successful response', async () => {
    await wrapper.get('#textInput').setValue('command line')
    await wrapper.get('#shareTextBtn').trigger('click')
    await flushPromises()
    expect(fetchMock).toHaveBeenCalledWith('/api/texts', expect.objectContaining({ method: 'POST', body: JSON.stringify({ content: 'command line' }) }))
    expect((wrapper.get('#textInput').element as HTMLTextAreaElement).value).toBe('')
    expect(vi.mocked(fileUi.notify)).toHaveBeenCalledWith('碎片已保存')
  })

  it('collects pasted images in the composer and sends them together with text', async () => {
    await wrapper.get('#textInput').setValue('带图片的说明')
    const file = new File([new Uint8Array([137, 80, 78, 71])], 'pasted.png', { type: 'image/png' })
    const event = new Event('paste', { bubbles: true }) as ClipboardEvent
    const item = { kind: 'file', type: 'image/png', getAsFile: () => file }
    Object.defineProperty(event, 'clipboardData', { value: { files: [], items: [item] } })
    document.dispatchEvent(event)
    await flushPromises()
    expect(wrapper.findAll('.fragment-attachment')).toHaveLength(1)
    expect(fetchMock.mock.calls.some(([url]) => url === '/api/texts/fragments')).toBe(false)

    await wrapper.get('#shareTextBtn').trigger('click')
    await flushPromises()
    const upload = fetchMock.mock.calls.find(([url]) => url === '/api/texts/fragments')
    expect(upload?.[1]?.method).toBe('POST')
    const form = upload?.[1]?.body as FormData
    expect(form).toBeInstanceOf(FormData)
    expect(form.get('content')).toBe('带图片的说明')
    expect(form.getAll('images')).toHaveLength(1)
    expect(vi.mocked(fileUi.notify)).toHaveBeenCalledWith('图文碎片已保存')
  })

  it('opens a pending image preview and returns focus on backdrop close', async () => {
    const file = new File([new Uint8Array([137, 80, 78, 71])], 'image.png', { type: 'image/png' })
    const event = new Event('paste', { bubbles: true }) as ClipboardEvent
    Object.defineProperty(event, 'clipboardData', { value: { files: [], items: [{ kind: 'file', type: 'image/png', getAsFile: () => file }] } })
    document.dispatchEvent(event)
    await flushPromises()
    const trigger = wrapper.get('.fragment-attachment-open')
    await trigger.trigger('click')
    await flushPromises()
    expect(wrapper.find('.fragment-image-preview').exists()).toBe(true)
    expect(wrapper.text()).toContain('图片预览')
    await wrapper.get('.fragment-image-preview-dialog h2').trigger('click')
    await flushPromises()
    expect(wrapper.find('.fragment-image-preview').exists()).toBe(true)
    await wrapper.get('.fragment-image-preview-dialog').trigger('click')
    await flushPromises()
    expect(wrapper.find('.fragment-image-preview').exists()).toBe(false)
    expect(document.activeElement).toBe(trigger.element)
  })

  it('keeps the image picker, character count and save shortcut on one row', async () => {
    expect(wrapper.find('.fragment-compose-actions .fragment-image-picker').exists()).toBe(true)
    const submit = wrapper.get('.fragment-compose-submit')
    expect(submit.element.firstElementChild).toBe(wrapper.get('#textCount').element)
    expect(wrapper.get('.fragment-save-shortcut').text()).toMatch(/^(⌘ \+ Enter|Ctrl \+ Enter)$/)
    expect(wrapper.text()).not.toContain('文字和图片会作为同一条碎片发送')
  })

  it('keeps the composer plain without format buttons or Markdown tips', async () => {
    expect(wrapper.find('.fragment-editor-toolbar').exists()).toBe(false)
    for (const label of ['加粗', '斜体', '行内代码', '插入链接']) expect(wrapper.find(`[aria-label="${label}"]`).exists()).toBe(false)
    expect(wrapper.text()).not.toContain('支持加粗、斜体、代码和链接')
    expect(wrapper.get('#textInput').attributes('placeholder')).toContain('写点文字')
  })

  it('leaves Ctrl+B, Ctrl+I and Ctrl+K to the browser instead of inserting Markdown', async () => {
    const input = wrapper.get('#textInput')
    await input.setValue('keep me')
    for (const key of ['b', 'i', 'k']) {
      const event = new KeyboardEvent('keydown', { key, ctrlKey: true, cancelable: true, bubbles: true })
      input.element.dispatchEvent(event)
      expect(event.defaultPrevented).toBe(false)
    }
    expect((input.element as HTMLTextAreaElement).value).toBe('keep me')
  })

  it('renders mixed text and image fragments as a full row even with a narrow image', async () => {
    wrapper.unmount()
    const image = {
      id: '0011223344556677', kind: 'mixed', content: '一张截图', created: new Date(Date.now() - 4 * 86400000).toISOString(),
      expiresAt: new Date(Date.now() + 3 * 86400000).toISOString(),
      images: [{ name: '截图.png', size: 2048, width: 100, height: 300 }],
    }
    fetchMock.mockImplementation(async (url: string) => url === '/api/texts' ? response({ texts: [image] }) : response({}))
    wrapper = mount(FragmentTransfer, { attachTo: document.body })
    await flushPromises()
    expect(wrapper.get('.fragment-image-grid img').attributes('src')).toBe('/api/texts/0011223344556677/images/0')
    expect(wrapper.get('.fragment-row').classes()).toContain('wide')
    expect(wrapper.get('.text-content').text()).toBe('一张截图')
    expect(wrapper.get('.fragment-expiry').text()).toContain('天后到期')
    expect(Number(wrapper.get('[role="progressbar"]').attributes('aria-valuenow'))).toBeGreaterThan(0)
    expect(wrapper.find('[data-text-action="copy"]').exists()).toBe(true)
    expect(wrapper.find('.fragment-image-grid a').exists()).toBe(false)
    await wrapper.get('.fragment-image-open').trigger('click')
    await flushPromises()
    expect(wrapper.get('.fragment-image-preview').attributes('src')).toBe('/api/texts/0011223344556677/images/0')
    expect(wrapper.text()).toContain('图片预览')
    await wrapper.get('.fragment-image-preview-dialog .dialog-actions button').trigger('click')
    await flushPromises()
    expect(wrapper.find('.fragment-image-preview').exists()).toBe(false)
  })

  it('shows remaining hours and minutes for fragments expiring within a day', async () => {
    wrapper.unmount()
    const soon = { id: '0011223344556678', content: 'expires soon', created: new Date(Date.now() - 6 * 86400000).toISOString(), expiresAt: new Date(Date.now() + 6.5 * 3600000).toISOString() }
    fetchMock.mockImplementation(async (url: string) => url === '/api/texts' ? response({ texts: [soon] }) : response({}))
    wrapper = mount(FragmentTransfer, { attachTo: document.body })
    await flushPromises()
    expect(wrapper.get('.fragment-expiry').text()).toMatch(/^\d{2}:\d{2} 后到期$/)
    expect(wrapper.get('[role="progressbar"]').attributes('aria-valuetext')).toContain('到期')
  })

  it('opens the save dialog for a fragment', async () => {
    await wrapper.get('[data-text-action="save"]').trigger('click')
    await flushPromises()
    expect(document.body.textContent).toContain('存入内网空间')
    expect(fetchMock.mock.calls.some(([url]) => String(url).startsWith('/api/filesystem?'))).toBe(true)
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
