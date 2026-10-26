import { flushPromises, mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import { afterEach, describe, expect, it } from 'vitest'
import ServiceStatus from './ServiceStatus.vue'

afterEach(() => { document.body.replaceChildren() })
describe('ServiceStatus', () => {
  it('tracks successful refresh, connection errors, recovery, and shutdown', async () => {
    const wrapper = mount(ServiceStatus, { attachTo: document.body })
    window.dispatchEvent(new Event('lan-copy:data-updated'))
    await flushPromises()
    expect(wrapper.get('#serviceStatus').text()).toContain('局域网在线')
    expect(wrapper.get('#dataUpdatedAt').attributes('datetime')).not.toBe('')
    const lastSuccess = wrapper.get('#dataUpdatedAt').attributes('datetime')
    window.dispatchEvent(new Event('lan-copy:data-failed'))
    await nextTick()
    expect(wrapper.get('#serviceStatus').text()).toContain('连接异常')
    expect(wrapper.get('#dataUpdatedAt').attributes('datetime')).toBe(lastSuccess)
    window.dispatchEvent(new Event('lan-copy:data-updated'))
    await nextTick()
    expect(wrapper.get('#serviceStatus').text()).toContain('局域网在线')
    window.dispatchEvent(new Event('lan-copy:shutdown'))
    await nextTick()
    window.dispatchEvent(new Event('lan-copy:data-updated'))
    expect(wrapper.get('#serviceStatus').text()).toContain('服务已关闭')
    expect(wrapper.get('#dataRefreshHint').text()).toContain('已停止自动更新')
    wrapper.unmount()
  })

  it('opens from keyboard focus and closes with Escape', async () => {
    const wrapper = mount(ServiceStatus, { attachTo: document.body })
    await wrapper.get('#statusAccess').trigger('focusin')
    expect(wrapper.get('#serviceStatusPopover').attributes('hidden')).toBeUndefined()
    await wrapper.get('#statusAccess').trigger('keydown', { key: 'Escape' })
    expect(wrapper.get('#serviceStatusPopover').attributes('hidden')).toBeDefined()
    wrapper.unmount()
  })
})
