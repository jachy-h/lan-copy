import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import PreviewPage from './PreviewPage.vue'
import { mountPreview } from '../preview/engine'

vi.mock('../preview/engine', () => ({ mountPreview: vi.fn(() => ({ destroy: vi.fn() })) }))

afterEach(() => { window.history.replaceState(null, '', '/') })

describe('PreviewPage', () => {
  it('preserves the preview query, download URL, and transfer return destination', async () => {
    window.history.replaceState(null, '', '/preview.html?path=folder%2Fone%20%23.pdf&from=transfer')
    const wrapper = mount(PreviewPage, { attachTo: document.body })
    await flushPromises()
    expect(wrapper.get('#previewTitle').text()).toBe('one #.pdf')
    expect(wrapper.get('#previewBack').attributes('href')).toBe('/transfer-text')
    expect(wrapper.get('#previewDownload').attributes('href')).toContain('/files/folder%2Fone%20%23.pdf')
    expect(vi.mocked(mountPreview)).toHaveBeenCalledWith(expect.any(HTMLElement), 'folder/one #.pdf')
    const engine = vi.mocked(mountPreview).mock.results.at(-1)?.value
    wrapper.unmount()
    expect(engine.destroy).toHaveBeenCalledOnce()
  })
})
