import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import AppHeader from './AppHeader.vue'
import { beginUpload, dismissUpload, minimizeUpload, startUpload, updateUploadProgress, uploadProgress } from '../upload/progress'

let wrapper: ReturnType<typeof mount> | undefined
const stubs = { SharePopover: true, SettingsDialog: true, ServiceStatus: true }

beforeEach(() => { vi.stubGlobal('fetch', vi.fn(async () => ({ ok: true, status: 200, json: async () => ({ bytes: 2048, files: 3 }) }))) })
afterEach(() => { wrapper?.unmount(); wrapper = undefined; dismissUpload(); document.body.replaceChildren(); vi.unstubAllGlobals() })

function render() {
  wrapper = mount(AppHeader, { props: { page: 'files', filesHref: '/' }, global: { stubs } })
}

describe('AppHeader upload indicator', () => {
  it('shows local storage usage from the logo tooltip', async () => {
    render()
    await flushPromises()
    expect(wrapper!.get('.storage-tooltip-usage').text()).toContain('2.0 KB')
    expect(wrapper!.get('.storage-tooltip').text()).toContain('3 个文件')
    expect(wrapper!.get('.storage-tooltip').text()).toContain('已开启端到端加密')
  })

  it('places the minimized transfer button immediately before the print button', async () => {
    render()
    expect(wrapper!.find('.transfer-access-button').exists()).toBe(false)
    expect(wrapper!.get('.hero-actions').element.firstElementChild?.classList.contains('print-access-button')).toBe(true)

    beginUpload()
    startUpload('正在上传 3 个文件到 全部文件')
    updateUploadProgress(45, 100)
    minimizeUpload()
    await wrapper!.vm.$nextTick()

    const button = wrapper!.get('.transfer-access-button')
    expect(button.attributes('aria-label')).toContain('45%')
    expect(button.classes()).toContain('active')
    expect(button.element.nextElementSibling?.classList.contains('print-access-button')).toBe(true)

    await button.trigger('click')
    expect(uploadProgress.open).toBe(true)
    expect(wrapper!.find('.transfer-access-button').exists()).toBe(false)
  })

  it('shows completion and failure states without transfer animation', async () => {
    render()
    beginUpload()
    startUpload('正在上传')
    updateUploadProgress(100, 100)
    minimizeUpload()
    await wrapper!.vm.$nextTick()
    expect(wrapper!.get('.transfer-access-button').classes()).toContain('active')

    uploadProgress.phase = 'complete'
    await wrapper!.vm.$nextTick()
    const complete = wrapper!.get('.transfer-access-button')
    expect(complete.classes()).not.toContain('active')
    expect(complete.find('.transfer-button-dot').exists()).toBe(true)
    expect(complete.attributes('aria-label')).toContain('上传完成')

    uploadProgress.phase = 'error'
    await wrapper!.vm.$nextTick()
    expect(wrapper!.get('.transfer-access-button').attributes('aria-label')).toContain('上传失败')
  })
})
