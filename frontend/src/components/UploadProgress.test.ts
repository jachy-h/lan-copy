import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it } from 'vitest'
import UploadProgress from './UploadProgress.vue'
import { beginUpload, completeUpload, dismissUpload, openUpload, startUpload, updateReading, updateUploadProgress } from '../upload/progress'

let wrapper: ReturnType<typeof mount> | undefined

afterEach(() => { wrapper?.unmount(); wrapper = undefined; dismissUpload(); document.body.replaceChildren() })

function render() {
  wrapper = mount(UploadProgress, { attachTo: document.body, global: { stubs: { teleport: true } } })
}

describe('UploadProgress dialog', () => {
  it('stays hidden until an upload starts', () => {
    render()
    expect(wrapper!.find('.transfer-dialog').exists()).toBe(false)
  })

  it('shows reading and transfer progress, then minimizes and restores', async () => {
    beginUpload()
    render()
    await flushPromises()
    expect(wrapper!.get('.transfer-dialog').text()).toContain('正在读取文件和文件夹')
    expect(wrapper!.get('.transfer-progress').attributes('aria-valuenow')).toBeUndefined()

    updateReading(2)
    await flushPromises()
    expect(wrapper!.text()).toContain('2 项')

    startUpload('正在上传 2 个文件到 全部文件')
    updateUploadProgress(512, 1024)
    await flushPromises()
    expect(wrapper!.get('#transferDialogTitle').text()).toBe('正在传输文件')
    expect(wrapper!.get('.transfer-progress').attributes('aria-valuenow')).toBe('50')
    expect(wrapper!.get('.transfer-dialog-meta').text()).toContain('50%')
    expect(wrapper!.get('.transfer-dialog-meta').text()).toContain('512 B / 1.0 KB')

    await wrapper!.get('.transfer-dialog-collapse').trigger('click')
    expect(wrapper!.find('.transfer-dialog').exists()).toBe(false)

    openUpload()
    await flushPromises()
    expect(wrapper!.find('.transfer-dialog').exists()).toBe(true)
  })

  it('reports completion and dismisses the finished task', async () => {
    beginUpload()
    startUpload('正在上传 2 个文件到 全部文件')
    updateUploadProgress(1024, 1024)
    completeUpload('2 个文件已上传到 全部文件')
    render()
    await flushPromises()
    expect(wrapper!.get('#transferDialogTitle').text()).toBe('上传完成')
    expect(wrapper!.get('.transfer-progress').attributes('aria-valuenow')).toBe('100')
    expect(wrapper!.get('.transfer-dialog-collapse').text()).toContain('关闭')

    await wrapper!.get('.transfer-dialog-collapse').trigger('click')
    expect(wrapper!.find('.transfer-dialog').exists()).toBe(false)
  })
})
