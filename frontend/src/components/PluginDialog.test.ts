import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import PluginDialog from './PluginDialog.vue'
import type { PluginDialog as PluginDialogData } from '../api/plugins'

let wrapper: ReturnType<typeof mount> | undefined

beforeEach(() => {
  Object.defineProperty(HTMLDialogElement.prototype, 'showModal', { configurable: true, value() { this.open = true } })
  Object.defineProperty(HTMLDialogElement.prototype, 'close', { configurable: true, value() { this.open = false; this.dispatchEvent(new Event('close')) } })
})

afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  document.body.replaceChildren()
})

const dialog: PluginDialogData = {
  pluginId: 'invoice-ocr',
  pluginName: 'Invoice OCR',
  title: '确认发票信息',
  url: '/plugin-app/invoice-ocr/review/abc',
}

function render() {
  wrapper = mount(PluginDialog, { props: { dialog }, attachTo: document.body })
}

describe('PluginDialog', () => {
  it('renders the plugin page in a modal iframe', async () => {
    render()
    await flushPromises()
    expect(wrapper!.get('dialog').attributes('open')).not.toBeUndefined()
    expect(wrapper!.get('h2').text()).toBe('确认发票信息')
    expect(wrapper!.get('.plugin-app-dialog-plugin').text()).toBe('Invoice OCR')
    expect(wrapper!.get('iframe').attributes('src')).toBe(dialog.url)
    expect(wrapper!.get('iframe').attributes('referrerpolicy')).toBe('no-referrer')
  })

  it('accepts completion messages only from its own iframe and origin', async () => {
    render()
    await flushPromises()
    const frame = wrapper!.get('iframe').element as HTMLIFrameElement
    const source = frame.contentWindow

    window.dispatchEvent(new MessageEvent('message', {
      data: { type: 'lan-copy:plugin-dialog:complete', message: '已写入表格' },
      origin: 'https://evil.example',
      source,
    }))
    window.dispatchEvent(new MessageEvent('message', {
      data: { type: 'lan-copy:plugin-dialog:complete', message: '已写入表格' },
      origin: location.origin,
      source: window,
    }))
    expect(wrapper!.emitted('complete')).toBeUndefined()

    window.dispatchEvent(new MessageEvent('message', {
      data: { type: 'lan-copy:plugin-dialog:complete', message: '已写入表格', refresh: true },
      origin: location.origin,
      source,
    }))
    await flushPromises()
    expect(wrapper!.emitted('complete')?.[0]).toEqual(['已写入表格', true])
    expect(wrapper!.emitted('close')).toBeUndefined()
  })

  it('closes on the close button and reports a plain close', async () => {
    render()
    await flushPromises()
    await wrapper!.get('.plugin-app-dialog-close').trigger('click')
    expect(wrapper!.emitted('close')).toHaveLength(1)
  })
})
