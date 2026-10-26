import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import SharePopover from './SharePopover.vue'
import * as fileUi from '../ui/fileUi'
import * as qrCode from '../ui/qrcode'

vi.mock('../ui/fileUi', () => ({ notify: vi.fn(), copyText: vi.fn().mockResolvedValue(true) }))
vi.mock('../ui/qrcode', () => ({ createSVG: vi.fn(() => document.createElementNS('http://www.w3.org/2000/svg', 'svg')) }))
const response = (body: unknown) => ({ ok: true, status: 200, json: async () => body })
let fetchMock: ReturnType<typeof vi.fn>
let wrapper: ReturnType<typeof mount>
beforeEach(async () => {
  window.history.replaceState(null, '', '/')
  fetchMock = vi.fn(async () => response({ urls: ['http://10.0.0.3:8080', 'http://192.168.1.5:9000'] }))
  vi.stubGlobal('fetch', fetchMock)
  wrapper = mount(SharePopover, { attachTo: document.body })
  await flushPromises()
})
afterEach(() => { wrapper?.unmount(); vi.unstubAllGlobals(); window.history.replaceState(null, '', '/') })

describe('SharePopover', () => {
  it('uses discovered LAN URLs, sorts preferred addresses, and renders a generated QR SVG', async () => {
    expect(fetchMock).toHaveBeenCalledWith('/api/access', expect.objectContaining({ cache: 'no-store' }))
    const options = wrapper.findAll('#accessUrl option').map(option => option.attributes('value'))
    expect(options[0]).toBe('http://192.168.1.5:9000')
    expect(options).toContain('http://10.0.0.3:8080')
    await wrapper.get('#accessButton').trigger('click')
    expect(wrapper.get('#accessPanel').attributes('hidden')).toBeUndefined()
    expect(wrapper.find('.qr-code svg').exists()).toBe(true)
    expect(vi.mocked(qrCode.createSVG)).toHaveBeenCalledWith(options[0])
    await wrapper.get('#copyAccessUrl').trigger('click')
    expect(vi.mocked(fileUi.copyText)).toHaveBeenCalledWith(options[0])
    expect(vi.mocked(fileUi.notify)).toHaveBeenCalledWith('访问地址已复制', false)
  })
  it('closes hover popovers, pins clicks, and restores keyboard focus on Escape', async () => {
    vi.useFakeTimers()
    try {
      await wrapper.get('#shareAccess').trigger('pointerenter', { pointerType: 'mouse' })
      await wrapper.get('#shareAccess').trigger('pointerleave')
      await vi.advanceTimersByTimeAsync(200)
      expect(wrapper.get('#accessPanel').attributes('hidden')).toBeDefined()
      await wrapper.get('#accessButton').trigger('click')
      await wrapper.get('#shareAccess').trigger('pointerleave')
      await vi.advanceTimersByTimeAsync(200)
      expect(wrapper.get('#accessPanel').attributes('hidden')).toBeUndefined()
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
      await flushPromises()
      expect(wrapper.get('#accessPanel').attributes('hidden')).toBeDefined()
      expect(document.activeElement).toBe(wrapper.get('#accessButton').element)
    } finally { vi.useRealTimers() }
  })

})
