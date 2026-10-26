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
    FileExplorer: Files, TextTransfer: Texts, FilePreviewDialog: true,
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
    wrapper.get('a[href="/"]').element.dispatchEvent(event)
    expect(event.defaultPrevented).toBe(false)
    expect(location.pathname).toBe('/transfer-text')
  })
})
