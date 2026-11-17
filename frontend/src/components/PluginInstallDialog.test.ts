import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import PluginInstallDialog from './PluginInstallDialog.vue'
import * as api from '../api/plugins'

vi.mock('../api/plugins', async importOriginal => {
  const actual = await importOriginal<typeof import('../api/plugins')>()
  return { ...actual, installPlugin: vi.fn(), waitForTask: vi.fn() }
})

const manifest = {
  schemaVersion: 1, id: 'demo', name: 'Demo', version: '1.0.0', protocolVersion: '1.0',
  runtime: 'http', containerPort: 8080,
  endpoints: { health: '/health', manifest: '/manifest', version: '/version', capabilities: '/capabilities' },
  capabilities: ['demo.echo'],
  defaults: { cpuLimit: 0.5, memoryMB: 512, pidsLimit: 128, requestTimeoutSeconds: 30, startupTimeoutSeconds: 60 },
}
const image = {
  id: 'img-1', pluginId: 'demo', runtimeImageId: 'sha256:1', reference: 'demo:1',
  imageName: 'demo', imageTag: '1', digest: 'sha256:digest', architecture: 'arm64', os: 'linux',
  sizeBytes: 1024, source: 'upload', manifest, createdAt: '2026-11-17T00:00:00Z',
}
const runtime = {
  type: 'docker', available: true, networkMode: 'loopback' as const,
  network: 'plugin-network', dataRoots: ['/tmp/plugins'],
}

let wrapper: ReturnType<typeof mount>

beforeEach(() => {
  Object.defineProperty(HTMLDialogElement.prototype, 'showModal', { configurable: true, value() { this.open = true } })
  Object.defineProperty(HTMLDialogElement.prototype, 'close', { configurable: true, value() { this.open = false; this.dispatchEvent(new Event('close')) } })
  vi.mocked(api.installPlugin).mockResolvedValue({ taskId: 'task-install' })
  vi.mocked(api.waitForTask).mockResolvedValue({
    id: 'task-install', kind: 'install', status: 'succeeded', progress: 100,
    createdAt: '2026-11-17T00:00:00Z', updatedAt: '2026-11-17T00:00:00Z',
  })
})

afterEach(() => {
  wrapper?.unmount()
  vi.clearAllMocks()
})

describe('PluginInstallDialog', () => {
  it('walks through review and configuration, then installs', async () => {
    wrapper = mount(PluginInstallDialog, { props: { images: [image], runtime }, attachTo: document.body })
    await flushPromises()
    expect(wrapper.text()).toContain('Demo')
    const next = () => wrapper.findAll('button').find(button => button.text() === '下一步')!
    await next().trigger('click')
    expect(wrapper.text()).toContain('插件 ID')
    expect(wrapper.text()).toContain('安全边界')
    await next().trigger('click')
    expect(wrapper.text()).toContain('环境变量')
    expect(wrapper.text()).toContain('持久化目录')
    const submit = wrapper.findAll('button').find(button => button.text() === '确认安装')!
    await submit.trigger('click')
    await flushPromises()
    expect(api.installPlugin).toHaveBeenCalledWith(expect.objectContaining({
      imageId: 'img-1', expectedPluginId: 'demo', start: true,
    }))
    expect(wrapper.emitted('installed')).toBeTruthy()
  })

  it('shows an empty state without importable images', async () => {
    wrapper = mount(PluginInstallDialog, { props: { images: [], runtime }, attachTo: document.body })
    await flushPromises()
    expect(wrapper.text()).toContain('没有可安装的镜像')
    expect(wrapper.findAll('button').find(button => button.text() === '下一步')!.attributes('disabled')).toBeDefined()
  })

  it('uses the default data root for a mount', async () => {
    wrapper = mount(PluginInstallDialog, { props: { images: [image], runtime }, attachTo: document.body })
    await flushPromises()
    const next = () => wrapper.findAll('button').find(button => button.text() === '下一步')!
    await next().trigger('click')
    await next().trigger('click')
    await wrapper.findAll('button').find(button => button.text() === '添加目录')!.trigger('click')
    await wrapper.findAll('button').find(button => button.text() === '用默认路径')!.trigger('click')
    const hostInput = wrapper.get('input[aria-label="宿主机路径"]')
    expect((hostInput.element as HTMLInputElement).value).toBe('/tmp/plugins/demo-data')
  })
})
