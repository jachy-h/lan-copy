import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import PrintQueue from './PrintQueue.vue'
import { requestJSON } from '../api/client'

vi.mock('../api/client', () => ({ requestJSON: vi.fn() }))
const request = vi.mocked(requestJSON)
let wrapper: ReturnType<typeof mount>
const printers = [{ name: 'First', default: false }, { name: 'Office USB', default: true }]
beforeEach(() => { vi.useFakeTimers(); request.mockReset() })
afterEach(() => { wrapper?.unmount(); vi.useRealTimers() })

describe('print queue', () => {
  it('selects the system default, submits the chosen printer once, and polls without reporting paper completion', async () => {
    const job = { id: 1, printer: 'First', created: '2026-10-27T01:00:00Z', status: 'queued', systemId: 0 }
    let submitted = false
    request.mockImplementation(async (path, init) => {
      if (path === '/api/printing') return { available: true, reason: '', printers }
      if (path === '/api/printing/test') { submitted = true; expect(JSON.parse(init!.body as string)).toEqual({ printer: 'First' }); return job }
      return { jobs: submitted ? [job] : [] }
    })
    wrapper = mount(PrintQueue, { global: { stubs: { DocumentPrint: true } } }); await flushPromises()
    expect((wrapper.get('select').element as HTMLSelectElement).value).toBe('Office USB')
    await wrapper.get('select').setValue('First')
    await wrapper.get('.print-test-button').trigger('click'); await flushPromises()
    expect(wrapper.get('.print-test-button').attributes('disabled')).toBeDefined()
    await wrapper.get('.print-test-button').trigger('click')
    expect(request.mock.calls.filter(([path]) => path === '/api/printing/test')).toHaveLength(1)
    job.status = 'submitted'; job.systemId = 42
    await vi.advanceTimersByTimeAsync(1500); await flushPromises()
    expect(wrapper.get('.print-job-status').text()).toBe('已提交')
    expect(wrapper.text()).toContain('系统任务 #42')
    expect(wrapper.text()).toContain('实际出纸情况请在打印机或主机上确认')
    expect(wrapper.get('.print-test-button').attributes('disabled')).toBeUndefined()
    expect(wrapper.text()).not.toContain('打印完成')
  })

  it('shows unsupported and missing-printer states and recovers by refreshing', async () => {
    request.mockImplementation(async path => path === '/api/printing' ? { available: false, reason: '当前仅支持 Windows 主机打印', printers: [] } : { jobs: [] })
    wrapper = mount(PrintQueue, { global: { stubs: { DocumentPrint: true } } }); await flushPromises()
    expect(wrapper.text()).toContain('当前仅支持 Windows 主机打印')
    expect(wrapper.get('.print-test-button').attributes('disabled')).toBeDefined()
    request.mockImplementation(async path => path === '/api/printing' ? { available: true, reason: '', printers: [] } : { jobs: [] })
    await wrapper.get('.print-text-button').trigger('click'); await flushPromises()
    expect(wrapper.text()).toContain('未找到实体打印机')
    request.mockImplementation(async path => path === '/api/printing' ? { available: true, reason: '', printers } : { jobs: [] })
    await wrapper.get('.print-text-button').trigger('click'); await flushPromises()
    expect(wrapper.get('.print-test-button').attributes('disabled')).toBeUndefined()
  })

  it('keeps a valid selection on refresh and disables printing when job state is unknown', async () => {
    let failJobs = false
    request.mockImplementation(async path => {
      if (path === '/api/printing') return { available: true, reason: '', printers }
      if (failJobs) throw new Error('network')
      return { jobs: [] }
    })
    wrapper = mount(PrintQueue, { global: { stubs: { DocumentPrint: true } } }); await flushPromises()
    await wrapper.get('select').setValue('First')
    await wrapper.get('.print-text-button').trigger('click'); await flushPromises()
    expect((wrapper.get('select').element as HTMLSelectElement).value).toBe('First')
    failJobs = true
    await vi.advanceTimersByTimeAsync(5000); await flushPromises()
    expect(wrapper.text()).toContain('任务状态暂时无法更新')
    expect(wrapper.get('.print-test-button').attributes('disabled')).toBeDefined()
    const count = request.mock.calls.length
    window.dispatchEvent(new Event('lan-copy:shutdown'))
    await vi.advanceTimersByTimeAsync(10000)
    expect(request.mock.calls).toHaveLength(count)
    expect(wrapper.text()).toContain('服务已关闭')
  })

  it('reports submission errors without automatically resending', async () => {
    request.mockImplementation(async path => {
      if (path === '/api/printing') return { available: true, reason: '', printers }
      if (path === '/api/printing/test') throw new Error('connection lost')
      return { jobs: [] }
    })
    wrapper = mount(PrintQueue, { global: { stubs: { DocumentPrint: true } } }); await flushPromises()
    await wrapper.get('.print-test-button').trigger('click'); await flushPromises()
    expect(wrapper.text()).toContain('先核对任务记录和 Windows 打印队列')
    await vi.advanceTimersByTimeAsync(10000)
    expect(request.mock.calls.filter(([path]) => path === '/api/printing/test')).toHaveLength(1)
  })
})
