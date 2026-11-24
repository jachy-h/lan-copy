import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import PluginManager from './PluginManager.vue'
import * as api from '../api/plugins'

vi.mock('../api/plugins', async importOriginal => {
  const actual = await importOriginal<typeof import('../api/plugins')>()
  return {
    ...actual,
    getPluginRuntime: vi.fn(),
    listPlugins: vi.fn(),
    listPluginImages: vi.fn(),
    pluginAction: vi.fn(),
    waitForTask: vi.fn(),
    uninstallPlugin: vi.fn(),
    deletePluginImage: vi.fn(),
    startPluginRuntime: vi.fn(),
    uploadPluginImage: vi.fn(),
    getPluginEventCatalog: vi.fn(),
    getPluginEvents: vi.fn(),
    sendPluginTestEvent: vi.fn(),
  }
})

const runtime = {
  type: 'docker', available: true, version: '29.0', os: 'linux', architecture: 'arm64',
  networkMode: 'loopback' as const, network: 'plugin-network', dataRoots: ['/tmp/plugins'],
  apiBaseUrl: 'http://host.docker.internal:8080',
  apiBaseUrlReason: 'Docker 使用 host.docker.internal',
}
const manifest = {
  schemaVersion: 1, id: 'demo', name: 'Demo', version: '1.0.0', protocolVersion: '1.0',
  runtime: 'http', containerPort: 8080,
  endpoints: { health: '/health', manifest: '/manifest', version: '/version', capabilities: '/capabilities' },
  capabilities: ['demo.echo'],
}
const image = {
  id: 'img-1', pluginId: 'demo', runtimeImageId: 'sha256:1', reference: 'demo:1',
  imageName: 'demo', imageTag: '1', digest: 'sha256:digest', architecture: 'arm64', os: 'linux',
  sizeBytes: 1024, source: 'upload', manifest, createdAt: '2026-11-17T00:00:00Z',
}
const pluginView = {
  plugin: {
    id: 'demo', name: 'Demo', protocolVersion: '1.0', currentVersion: '1.0.0',
    status: 'stopped' as const, createdAt: '2026-11-17T00:00:00Z', updatedAt: '2026-11-17T00:00:00Z',
  },
  image,
  instance: {
    id: 'inst-1', pluginId: 'demo', imageId: 'img-1', runtimeType: 'docker',
    runtimeInstanceId: 'c1', containerName: 'lan-copy-plugin-demo', status: 'stopped' as const,
    healthStatus: 'unknown' as const, restartCount: 0,
    config: {
      env: [], cpuLimit: 0.5, memoryMB: 512, pidsLimit: 128, restartPolicy: 'no', mounts: [],
      healthCheck: { path: '/health', intervalSeconds: 15, timeoutSeconds: 3, retries: 3, startupTimeoutSeconds: 60 },
      requestTimeoutSeconds: 30, logMaxSizeMB: 10, logMaxFiles: 2,
    },
    createdAt: '2026-11-17T00:00:00Z', updatedAt: '2026-11-17T00:00:00Z',
  },
  metrics: { calls: 0, errors: 0, totalDurationMs: 0, lastDurationMs: 0 },
}

let wrapper: ReturnType<typeof mount>

beforeEach(() => {
  history.replaceState(null, '', '/plugins')
  Object.defineProperty(HTMLDialogElement.prototype, 'showModal', { configurable: true, value() { this.open = true } })
  Object.defineProperty(HTMLDialogElement.prototype, 'close', { configurable: true, value() { this.open = false; this.dispatchEvent(new Event('close')) } })
  vi.mocked(api.getPluginRuntime).mockResolvedValue(runtime)
  vi.mocked(api.listPlugins).mockResolvedValue({ plugins: [pluginView] })
  vi.mocked(api.listPluginImages).mockResolvedValue({ images: [image] })
})

afterEach(() => {
  wrapper?.unmount()
  history.replaceState(null, '', '/')
  vi.clearAllMocks()
})

describe('PluginManager', () => {
  it('shows runtime degradation and disables management', async () => {
    vi.mocked(api.getPluginRuntime).mockResolvedValue({ ...runtime, available: false, error: '无法连接容器运行时' })
    vi.mocked(api.listPlugins).mockResolvedValue({ plugins: [] })
    vi.mocked(api.listPluginImages).mockResolvedValue({ images: [] })
    wrapper = mount(PluginManager)
    await flushPromises()
    expect(wrapper.text()).toContain('容器运行时不可用')
    expect(wrapper.text()).toContain('无法连接容器运行时')
    expect(wrapper.text()).toContain('还没有安装插件')
    expect(wrapper.get('.plugins-heading-actions .primary').attributes('disabled')).toBeDefined()
  })

  it('labels the detected runtime', async () => {
    vi.mocked(api.getPluginRuntime).mockResolvedValue({ ...runtime, type: 'podman', version: '5.2.2' })
    wrapper = mount(PluginManager)
    await flushPromises()
    expect(wrapper.text()).toContain('Podman 5.2.2')
    expect(wrapper.text()).not.toContain('Docker 5.2.2')
    expect(wrapper.text()).toContain('插件 API 地址')
    expect(wrapper.text()).toContain('http://host.docker.internal:8080')
    expect(wrapper.text()).toContain('Docker 使用 host.docker.internal')
  })

  it('links to a running plugin interface', async () => {
    const runningView = {
      ...pluginView,
      image: { ...image, manifest: { ...manifest, ui: { path: '/' } } },
      instance: { ...pluginView.instance, status: 'running' as const, healthStatus: 'healthy' as const },
    }
    vi.mocked(api.listPlugins).mockResolvedValue({ plugins: [runningView] })
    wrapper = mount(PluginManager)
    await flushPromises()
    const link = wrapper.get('a.plugin-ui-link')
    expect(link.text()).toBe('打开界面')
    expect(link.attributes('href')).toBe('/plugin-app/demo/')
    expect(link.attributes('target')).toBe('_blank')
    expect(link.attributes('aria-disabled')).toBe('false')
  })

  it('disables the plugin interface link while stopped', async () => {
    const stoppedView = {
      ...pluginView,
      image: { ...image, manifest: { ...manifest, ui: {} } },
    }
    vi.mocked(api.listPlugins).mockResolvedValue({ plugins: [stoppedView] })
    wrapper = mount(PluginManager)
    await flushPromises()
    const link = wrapper.get('a.plugin-ui-link')
    expect(link.attributes('aria-disabled')).toBe('true')
    expect(link.classes()).toContain('disabled')
  })

  it('hides the interface link for plugins without a ui', async () => {
    wrapper = mount(PluginManager)
    await flushPromises()
    expect(wrapper.find('a.plugin-ui-link').exists()).toBe(false)
  })

  it('starts Podman from the unavailable banner', async () => {
    const unavailable = { ...runtime, available: false, startAvailable: true, error: '未找到可用的 Docker 或 Podman API 端点' }
    vi.mocked(api.getPluginRuntime).mockResolvedValueOnce(unavailable).mockResolvedValue(runtime)
    vi.mocked(api.listPlugins).mockResolvedValue({ plugins: [] })
    vi.mocked(api.listPluginImages).mockResolvedValue({ images: [] })
    vi.mocked(api.startPluginRuntime).mockResolvedValue({ taskId: 'task-start' })
    vi.mocked(api.waitForTask).mockResolvedValue({
      id: 'task-start', kind: 'runtime-start', status: 'succeeded', progress: 100,
      message: '完成', createdAt: '2026-11-17T00:00:00Z', updatedAt: '2026-11-17T00:00:00Z',
    })
    wrapper = mount(PluginManager)
    await flushPromises()
    expect(wrapper.text()).toContain('容器运行时不可用')
    const button = wrapper.findAll('button').find(item => item.text() === '启动 Podman')
    expect(button).toBeTruthy()
    await button!.trigger('click')
    await flushPromises()
    expect(api.startPluginRuntime).toHaveBeenCalledTimes(1)
    expect(wrapper.text()).toContain('Docker 29.0')
  })

  it('lists plugins and starts one through a task', async () => {
    vi.mocked(api.pluginAction).mockResolvedValue({ taskId: 'task-1' })
    vi.mocked(api.waitForTask).mockResolvedValue({
      id: 'task-1', kind: 'start', status: 'succeeded', progress: 100,
      createdAt: '2026-11-17T00:00:00Z', updatedAt: '2026-11-17T00:00:00Z',
    })
    wrapper = mount(PluginManager)
    await flushPromises()
    expect(wrapper.text()).toContain('Demo')
    expect(wrapper.text()).toContain('已停止')
    const start = wrapper.findAll('button').find(button => button.text() === '启动')!
    await start.trigger('click')
    await flushPromises()
    expect(api.pluginAction).toHaveBeenCalledWith('demo', 'start')
    expect(api.waitForTask).toHaveBeenCalledWith('task-1', expect.any(Object))
  })

  it('shows imported images with digest and architecture', async () => {
    wrapper = mount(PluginManager)
    await flushPromises()
    const imageTab = wrapper.findAll('[role="tab"]').find(tab => tab.text().startsWith('镜像'))!
    await imageTab.trigger('click')
    expect(wrapper.text()).toContain('sha256:digest')
    expect(wrapper.text()).toContain('linux/arm64')
  })

  it('copies a complete agent prompt with one click', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    wrapper = mount(PluginManager)
    await flushPromises()
    const button = wrapper.findAll('button').find(item => item.text() === '复制 Agent Prompt')!
    await button.trigger('click')
    await flushPromises()
    expect(writeText).toHaveBeenCalledTimes(1)
    expect(writeText.mock.calls[0][0]).toContain('io.lan-copy.plugin.manifest')
    expect(writeText.mock.calls[0][0]).toContain('/api/plugins/install')
    expect(writeText.mock.calls[0][0]).toContain('POST /events')
    expect(writeText.mock.calls[0][0]).toContain('LAN_COPY_EVENT_TOKEN')
  })

  it('states that LAN devices can manage plugins', async () => {
    wrapper = mount(PluginManager)
    await flushPromises()
    expect(wrapper.text()).toContain('局域网内可管理')
    expect(wrapper.text()).toContain('请只安装可信镜像')
    expect(wrapper.text()).not.toContain('仅限本机')
  })

  it('documents lifecycle events and sends a test event', async () => {
    vi.mocked(api.listPlugins).mockResolvedValue({
      plugins: [{
        ...pluginView,
        instance: { ...pluginView.instance, status: 'running' as const, healthStatus: 'healthy' as const },
      }],
    })
    vi.mocked(api.getPluginEventCatalog).mockResolvedValue({
      events: [{
        type: 'file.uploaded', category: '文件',
        description: '上传事务提交成功后发送。',
        data: { destination: 'docs' },
      }],
    })
    vi.mocked(api.getPluginEvents).mockResolvedValue({
      endpoint: '/events', subscriptions: ['file.*'],
      stats: { queued: 2, delivered: 1, failed: 0, dropped: 0 },
      deliveries: [],
    })
    vi.mocked(api.sendPluginTestEvent).mockResolvedValue({ eventId: 'evt-1' })
    wrapper = mount(PluginManager)
    await flushPromises()
    const eventsTab = wrapper.findAll('[role="tab"]').find(tab => tab.text().startsWith('对接'))!
    await eventsTab.trigger('click')
    await flushPromises()
    expect(api.getPluginEventCatalog).toHaveBeenCalledTimes(1)
    expect(api.getPluginEvents).toHaveBeenCalledWith('demo', expect.anything())
    expect(wrapper.text()).toContain('智能表格 API')
    expect(wrapper.text()).toContain('LAN_COPY_API_TOKEN')
    expect(wrapper.text()).toContain('lock-and-update')
    expect(wrapper.text()).toContain('插件 Web 界面')
    expect(wrapper.text()).toContain('/plugin-app/')
    expect(wrapper.text()).toContain('X-Forwarded-Prefix')
    expect(wrapper.text()).toContain('事件目录')
    expect(wrapper.text()).toContain('file.uploaded')
    expect(wrapper.text()).toContain('LAN_COPY_EVENT_TOKEN')
    expect(wrapper.text()).toContain('file.*')
    expect(wrapper.text()).toContain('已投递')
    const testButton = wrapper.findAll('button').find(button => button.text() === '发送测试事件')!
    expect(testButton.attributes('disabled')).toBeUndefined()
    await testButton.trigger('click')
    await flushPromises()
    expect(api.sendPluginTestEvent).toHaveBeenCalledWith('demo')
  })

  it('uses a URL subroute for each tab and restores it on popstate', async () => {
    vi.mocked(api.getPluginEventCatalog).mockResolvedValue({ events: [] })
    vi.mocked(api.getPluginEvents).mockResolvedValue({
      endpoint: '/events', subscriptions: [],
      stats: { queued: 0, delivered: 0, failed: 0, dropped: 0 }, deliveries: [],
    })
    wrapper = mount(PluginManager)
    await flushPromises()
    expect(location.pathname).toBe('/plugins/installed')
    const imageTab = wrapper.findAll('[role="tab"]').find(tab => tab.text().startsWith('镜像'))!
    await imageTab.trigger('click')
    expect(location.pathname).toBe('/plugins/images')
    const integrationTab = wrapper.findAll('[role="tab"]').find(tab => tab.text().startsWith('对接'))!
    await integrationTab.trigger('click')
    expect(location.pathname).toBe('/plugins/integration')
    history.pushState(null, '', '/plugins/installed')
    window.dispatchEvent(new PopStateEvent('popstate'))
    await flushPromises()
    expect(wrapper.findAll('[role="tab"]').find(tab => tab.text().startsWith('插件'))!.attributes('aria-selected')).toBe('true')
  })

  it('opens the integration subroute directly', async () => {
    history.replaceState(null, '', '/plugins/integration')
    vi.mocked(api.getPluginEventCatalog).mockResolvedValue({ events: [] })
    vi.mocked(api.getPluginEvents).mockResolvedValue({
      endpoint: '/events', subscriptions: [],
      stats: { queued: 0, delivered: 0, failed: 0, dropped: 0 }, deliveries: [],
    })
    wrapper = mount(PluginManager)
    await flushPromises()
    expect(wrapper.findAll('[role="tab"]').find(tab => tab.text().startsWith('对接'))!.attributes('aria-selected')).toBe('true')
    expect(wrapper.text()).toContain('生命周期事件、智能表格 API 与 Web 界面')
  })
})
