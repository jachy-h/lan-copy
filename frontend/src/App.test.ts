import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, onBeforeUnmount } from 'vue'
import { afterEach, describe, expect, it, vi } from 'vitest'
import App from './App.vue'

vi.mock('./ui/contextMenu', () => ({ close: vi.fn() }))
const filesUnmounted = vi.fn()
const Files = defineComponent({ setup() { onBeforeUnmount(filesUnmounted) }, template: '<section id="files-view">files</section>' })
const Texts = defineComponent({ props: ['modelValue'], emits: ['update:modelValue'], template: `<textarea id="draft" :value="modelValue" @input="$emit('update:modelValue', $event.target.value)" />` })
let wrapper: ReturnType<typeof mount>
function render() {
  wrapper = mount(App, { attachTo: document.body, global: { stubs: {
    FileExplorer: Files, TextTransfer: Texts, FilePreviewDialog: true, PrintQueue: { props: ['sharedPath'], emits: ['localFile'], template: '<section id="print-view">{{ sharedPath }}<button id="local-file" @click="$emit(\'localFile\')">选择本地文件</button></section>' },
    SharePopover: { template: '<button>分享</button>' }, SettingsDialog: { template: '<button>设置</button>' },
    ServiceStatus: { template: '<button>局域网在线</button>' },
  } } })
}
afterEach(() => { wrapper?.unmount(); document.body.replaceChildren(); history.replaceState(null, '', '/'); vi.clearAllMocks() })
describe('shared SPA', () => {
  it('switches without replacing the header, keeps draft and folder URL, and responds to history', async () => {
    history.replaceState(null, '', '/#path=photos')
    render()
    const header = wrapper.get('header').element
    expect(wrapper.get('.hero-actions').text()).toBe('分享设置局域网在线')
    await wrapper.get('a[href="/transfer-text"]').trigger('click')
    expect(location.pathname).toBe('/transfer-text')
    expect(wrapper.get('header').element).toBe(header)
    expect(wrapper.find('#files-view').exists()).toBe(false)
    expect(filesUnmounted).toHaveBeenCalledOnce()
    await wrapper.get('#draft').setValue('unfinished note')
    await wrapper.get('a[href="/print-queue"]').trigger('click')
    expect(wrapper.find('#print-view').exists()).toBe(true)
    expect(document.title).toBe('打印队列 · Lan Copy')
    expect(wrapper.get('.hero-actions').element.firstElementChild?.getAttribute('href')).toBe('/print-queue')
    await wrapper.get('a[href="/#path=photos"]').trigger('click')
    expect(location.hash).toBe('#path=photos')
    expect(document.title).toBe('共享空间 · Lan Copy')
    await wrapper.get('a[href="/transfer-text"]').trigger('click')
    expect((wrapper.get('#draft').element as HTMLTextAreaElement).value).toBe('unfinished note')
    history.replaceState(null, '', '/#path=other')
    window.dispatchEvent(new PopStateEvent('popstate'))
    await flushPromises()
    expect(wrapper.find('#files-view').exists()).toBe(true)
    expect(wrapper.get('a[aria-current="page"]').text()).toBe('共享空间')
  })
  it('opens the text route directly and preserves modified link clicks', async () => {
    history.replaceState(null, '', '/transfer-text')
    render()
    expect(wrapper.find('#draft').exists()).toBe(true)
    expect(document.title).toBe('文本快传 · Lan Copy')
    const event = new MouseEvent('click', { bubbles: true, cancelable: true, ctrlKey: true })
    const link = wrapper.get('a[href="/"]').element
    let preventedByApp = true
    link.addEventListener('click', observed => { preventedByApp = observed.defaultPrevented; observed.preventDefault() }, { once: true })
    link.dispatchEvent(event)
    expect(preventedByApp).toBe(false)
    expect(location.pathname).toBe('/transfer-text')
  })
  it('routes a shared print selection with its folder and clears the source on local replacement', async () => {
    history.replaceState(null, '', '/#path=资料')
    render()
    wrapper.findComponent(Files).vm.$emit('print', '资料/预算 + 100% #.PDF')
    await flushPromises()
    expect(location.pathname).toBe('/print-queue')
    expect(new URLSearchParams(location.search).get('file')).toBe('资料/预算 + 100% #.PDF')
    expect(wrapper.get('#print-view').text()).toContain('资料/预算 + 100% #.PDF')
    expect(decodeURIComponent(wrapper.get('.page-nav a').attributes('href')!)).toBe('/#path=资料')
    await wrapper.get('#local-file').trigger('click'); await flushPromises()
    expect(new URLSearchParams(location.search).has('file')).toBe(false)
    expect(wrapper.get('#print-view').text()).not.toContain('预算')
  })
  it('restores the shared source and folder on a direct print link and rejects external return links', () => {
    history.replaceState(null, '', '/print-queue?' + new URLSearchParams({ file: 'folder/a.pdf', from: '/#path=folder' }))
    render()
    expect(wrapper.get('#print-view').text()).toContain('folder/a.pdf')
    expect(wrapper.find('a[href="/#path=folder"]').exists()).toBe(true)
    wrapper.unmount()
    history.replaceState(null, '', '/print-queue?from=https://example.com')
    render()
    expect(wrapper.find('a[href="/"]').exists()).toBe(true)
  })

})
