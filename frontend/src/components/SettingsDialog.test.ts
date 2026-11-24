import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import SettingsDialog from './SettingsDialog.vue'
import * as dialogs from '../ui/dialog'

vi.mock('../ui/dialog', () => ({ confirm: vi.fn().mockResolvedValue(false), confirmText: vi.fn().mockResolvedValue(false) }))

const app = { version: '1.0.0', os: 'darwin', arch: 'arm64', canShutdown: true, updateEnabled: true, updateDisabledReason: '', signedUpdates: true }
const noUpdate = { version: '1.0.0', comparable: true, available: false, canInstall: false, pageURL: 'https://example.test/release' }
const response = (body: unknown, status = 200) => ({ ok: status >= 200 && status < 300, status, json: async () => body })
let fetchMock: ReturnType<typeof vi.fn>
let wrapper: ReturnType<typeof mount>

beforeEach(async () => {
  Object.defineProperty(HTMLDialogElement.prototype, 'showModal', { configurable: true, value() { this.open = true } })
  Object.defineProperty(HTMLDialogElement.prototype, 'close', { configurable: true, value() { this.open = false; this.dispatchEvent(new Event('close')) } })
  fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
    if (url === '/api/app') return response(app)
    if (url === '/api/update/check') return response(noUpdate)
    if (url === '/api/settings/autostart') return response({ enabled: false, available: false, reason: '本机不可用' })
    if (url === '/api/settings/upload-resources') {
      if (init?.method === 'PUT') return response({ config: JSON.parse(String(init.body)), editable: true })
      return response({ config: { concurrentUploads: 3, stagingGiB: 20, idleTimeoutSeconds: 60 }, editable: true })
    }
    if (url === '/api/settings/fragments') return response({ expiryDays: init?.method === 'PUT' ? 14 : 7, defaultExpiryDays: 7, minExpiryDays: 1, maxExpiryDays: 365, editable: true })
    if (url === '/api/settings/deploy-token') {
      if (init?.method === 'PUT') return response({ configured: true, source: 'file' })
      if (init?.method === 'DELETE') return response({ configured: false, source: 'none' })
      return response({ configured: false, source: 'none' })
    }
    if (url === '/api/files') return response({ files: [{ size: 1024 }] })
    if (url === '/api/resource-usage') return response({
      storage: {
        bytes: 1024, files: 2,
        types: [
          { kind: 'image', bytes: 768, files: 1 },
          { kind: 'document', bytes: 256, files: 1 },
        ],
      },
      uploads: { active: 1, limit: 3 },
      staging: { usedBytes: 0, limitBytes: 20 * (1 << 30) },
      downloads: { active: 0 },
    })
    if (url === '/api/system-usage') return response({
      app: { cpuPercent: 1.2, memoryBytes: 2 * (1 << 20), cpuReady: true, sampledAt: '2026-11-20T12:00:00Z' },
      plugins: {
        running: 1, unavailable: 0, cpuPercent: 3.4,
        memoryBytes: 1 << 20, memoryLimitBytes: 4 * (1 << 20), pids: 5,
        items: [{
          pluginId: 'demo', name: '示例插件', cpuPercent: 3.4,
          memoryBytes: 1 << 20, memoryLimitBytes: 4 * (1 << 20), pids: 5, available: true,
        }],
        sampledAt: '2026-11-20T12:00:00Z',
      },
    })
    if (url === '/api/shutdown/status') return response({ uploads: 0, downloads: 0 })
    throw new Error(`Unexpected request: ${url}`)
  })
  vi.stubGlobal('fetch', fetchMock)
  wrapper = mount(SettingsDialog, { attachTo: document.body })
  await wrapper.get('[aria-label="设置"]').trigger('click')
  await flushPromises()
})

afterEach(() => {
  wrapper?.unmount()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
  vi.mocked(dialogs.confirm).mockReset().mockResolvedValue(false)
  vi.mocked(dialogs.confirmText).mockReset().mockResolvedValue(false)
})
const inDialog = (selector: string) => {
  const element = document.body.querySelector(selector)
  if (!element) throw new Error(`Missing dialog element: ${selector}`)
  return element
}

describe('SettingsDialog', () => {
  it('loads settings, preserves visitor read-only state, and checks release on its tab', async () => {
    expect(document.body.textContent).toContain('当前版本 1.0.0 · darwin / arm64')
    expect(document.body.textContent).toContain('共享文件')
    expect(document.body.textContent).toContain('1.0 KiB')
    expect(document.body.textContent).toContain('2 个文件')
    expect(document.body.textContent).toContain('1 / 3')
    expect(document.body.textContent).toContain('0 B / 20 GiB')
    expect(document.body.textContent).toContain('统计为当前时刻快照。')
    const autostart = inDialog('.autostart-toggle') as HTMLInputElement
    expect(autostart.disabled).toBe(true)
    ;(inDialog('[data-settings-tab="update"]') as HTMLButtonElement).click()
    await flushPromises()
    expect(fetchMock).toHaveBeenCalledWith('/api/update/check', expect.objectContaining({ cache: 'no-store' }))
    expect(inDialog('.release-status').textContent).toContain('当前无需更新')
    expect(inDialog('.release-page').getAttribute('href')).toBe(noUpdate.pageURL)
  })

  it('shows file type stack and live process and plugin usage', async () => {
    ;(inDialog('[data-settings-tab="resources"]') as HTMLButtonElement).click()
    await flushPromises()
    const panel = inDialog('#resourcesSettings')
    expect(panel.textContent).toContain('资源详情')
    expect(panel.textContent).toContain('资源配置')
    expect(panel.textContent).toContain('合计 1.0 KiB · 2 个文件')
    const segments = Array.from(inDialog('.storage-bar').children) as HTMLElement[]
    expect(segments).toHaveLength(2)
    expect(segments[0].style.width).toBe('75%')
    expect(segments[1].style.width).toBe('25%')
    expect(inDialog('.storage-legend').textContent).toContain('图片')
    expect(inDialog('.storage-legend').textContent).toContain('768 B')
    expect(inDialog('.storage-legend').textContent).toContain('75%')
    expect(inDialog('.live-stats').textContent).toContain('1.2%')
    expect(inDialog('.live-stats').textContent).toContain('2.0 MiB')
    expect(inDialog('.live-stats').textContent).toContain('3.4%')
    expect(inDialog('.live-stats').textContent).toContain('1.0 MiB / 4.0 MiB')
    const row = inDialog('.plugin-usage-table tbody tr')
    expect(row.textContent).toContain('示例插件')
    expect(row.textContent).toContain('3.4%')
    expect(row.textContent).toContain('1.0 MiB')
    expect(row.textContent).toContain('5')
  })

  it('schedules live usage refresh and stops it when the dialog closes', async () => {
    const setInterval = vi.spyOn(window, 'setInterval')
    const clearInterval = vi.spyOn(window, 'clearInterval')
    ;(inDialog('[data-settings-tab="resources"]') as HTMLButtonElement).click()
    await flushPromises()
    expect(setInterval).toHaveBeenCalledWith(expect.any(Function), 2000)
    ;(inDialog('.settings-close') as HTMLButtonElement).click()
    await flushPromises()
    expect(clearInterval).toHaveBeenCalled()
  })

  it('refreshes resource occupancy and keeps limits usable when stats fail', async () => {
    ;(inDialog('[data-settings-tab="resources"]') as HTMLButtonElement).click()
    await flushPromises()
    const usageCalls = () => fetchMock.mock.calls.filter(([url]) => url === '/api/resource-usage').length
    const before = usageCalls()
    ;(inDialog('.resource-refresh') as HTMLButtonElement).click()
    await flushPromises()
    expect(usageCalls()).toBe(before + 1)

    const base = fetchMock.getMockImplementation() as ((url: string, init?: RequestInit) => unknown) | undefined
    fetchMock.mockImplementation((url: string, init?: RequestInit) => url === '/api/resource-usage' ? Promise.reject(new Error('统计失败')) : base!(url, init))
    ;(inDialog('.resource-refresh') as HTMLButtonElement).click()
    await flushPromises()
    expect(inDialog('.resource-usage').textContent).toContain('统计失败')
    expect((inDialog('.upload-concurrent') as HTMLInputElement).disabled).toBe(false)
  })

  it('keeps the upload action header and rejects out-of-range resource settings locally', async () => {
    ;(inDialog('[data-settings-tab="resources"]') as HTMLButtonElement).click()
    const concurrent = inDialog('.upload-concurrent') as HTMLInputElement
    concurrent.value = '33'; concurrent.dispatchEvent(new Event('input', { bubbles: true })); concurrent.dispatchEvent(new Event('change', { bubbles: true }))
    ;(inDialog('.upload-resource-save') as HTMLButtonElement).click()
    await flushPromises()
    expect(inDialog('.upload-resource-status').textContent).toContain('并发 1–32')
    expect(fetchMock.mock.calls.some(([url, init]) => url === '/api/settings/upload-resources' && init?.method === 'PUT')).toBe(false)
    concurrent.value = '4'; concurrent.dispatchEvent(new Event('input', { bubbles: true })); concurrent.dispatchEvent(new Event('change', { bubbles: true }))
    ;(inDialog('.upload-resource-save') as HTMLButtonElement).click()
    await flushPromises()
    const write = fetchMock.mock.calls.find(([url, init]) => url === '/api/settings/upload-resources' && init?.method === 'PUT')
    expect(new Headers(write?.[1]?.headers).get('X-Lan-Copy-Action')).toBe('upload-resources')
    expect(inDialog('.resource-stats').textContent).toContain('1 / 4')
  })

  it('saves the fragment expiry in days with the local action header', async () => {
    ;(inDialog('[data-settings-tab="general"]') as HTMLButtonElement).click()
    const input = inDialog('.fragment-expiry-days') as HTMLInputElement
    expect(input.value).toBe('7')
    input.value = '14'; input.dispatchEvent(new Event('input', { bubbles: true })); input.dispatchEvent(new Event('change', { bubbles: true }))
    ;(inDialog('.fragment-setting-save') as HTMLButtonElement).click()
    await flushPromises()
    const write = fetchMock.mock.calls.find(([url, init]) => url === '/api/settings/fragments' && init?.method === 'PUT')
    expect(write).toBeTruthy()
    expect(new Headers(write?.[1]?.headers).get('X-Lan-Copy-Action')).toBe('fragments')
    expect(JSON.parse(String(write?.[1]?.body))).toEqual({ expiryDays: 14 })
    expect(inDialog('.fragment-status').textContent).toContain('已保存')
  })

  it('offers the plugin service as a new-tab entry from settings', async () => {
    ;(inDialog('[data-settings-tab="plugins"]') as HTMLButtonElement).click()
    await flushPromises()
    const link = inDialog('.settings-plugin-launch') as HTMLAnchorElement
    expect(link.getAttribute('href')).toBe('/plugins')
    expect(link.getAttribute('target')).toBe('_blank')
    expect(link.getAttribute('rel')).toContain('noopener')
    expect(link.getAttribute('rel')).toContain('noreferrer')
    expect(link.textContent).toContain('在新标签页打开插件服务')
  })

  it('saves and deletes the deploy token from the settings page', async () => {
    vi.mocked(dialogs.confirm).mockResolvedValue(true)
    ;(inDialog('[data-settings-tab="deploy"]') as HTMLButtonElement).click()
    await flushPromises()
    expect(inDialog('.deploy-token-status').textContent).toContain('未设置令牌')

    const input = inDialog('.deploy-token-input') as HTMLInputElement
    input.value = 'pasted-token'
    input.dispatchEvent(new Event('input', { bubbles: true }))
    ;(inDialog('.deploy-token-save') as HTMLButtonElement).click()
    await flushPromises()
    const save = fetchMock.mock.calls.find(([url, init]) => url === '/api/settings/deploy-token' && init?.method === 'PUT')
    expect(save).toBeTruthy()
    expect(new Headers(save?.[1]?.headers).get('X-Lan-Copy-Action')).toBe('deploy-token')
    expect(JSON.parse(String(save?.[1]?.body))).toEqual({ token: 'pasted-token' })
    expect(inDialog('.deploy-token-status').textContent).toContain('部署令牌已保存')
    expect((inDialog('.deploy-token-input') as HTMLInputElement).value).toBe('')

    ;(inDialog('.deploy-token-clear') as HTMLButtonElement).click()
    await flushPromises()
    const remove = fetchMock.mock.calls.find(([url, init]) => url === '/api/settings/deploy-token' && init?.method === 'DELETE')
    expect(remove).toBeTruthy()
    expect(new Headers(remove?.[1]?.headers).get('X-Lan-Copy-Action')).toBe('deploy-token')
    expect(inDialog('.deploy-token-status').textContent).toContain('自动部署接口已关闭')
    expect((inDialog('.deploy-token-clear') as HTMLButtonElement).disabled).toBe(true)
  })

  it('does not shut down when the confirmation is cancelled', async () => {
    ;(inDialog('[data-settings-tab="general"]') as HTMLButtonElement).click()
    ;(inDialog('.shutdown-setting button') as HTMLButtonElement).click()
    await flushPromises()
    expect(vi.mocked(dialogs.confirm)).toHaveBeenCalledOnce()
    expect(fetchMock).not.toHaveBeenCalledWith('/api/shutdown', expect.anything())
    expect(inDialog('#shutdownHint').textContent).toContain('关闭前会检查')
  })

  it('rechecks transfer counts after a 409 and sends typed confirmation with the current counts', async () => {
    const confirm = vi.fn().mockResolvedValue(true)
    const confirmText = vi.fn().mockResolvedValue(true)
    vi.mocked(dialogs.confirm).mockImplementation(confirm)
    vi.mocked(dialogs.confirmText).mockImplementation(confirmText)
    const shutdownCalls: { url: string; init?: RequestInit }[] = []
    fetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
      if (url === '/api/shutdown/status') return response({ uploads: 1, downloads: 0 })
      if (url === '/api/shutdown') {
        shutdownCalls.push({ url, init })
        if (shutdownCalls.length === 1) return response({ requiresConfirmation: true, uploads: 2, downloads: 1 }, 409)
        return response({ status: 'shutting_down' }, 202)
      }
      if (url === '/api/app') return response(app)
      throw new Error(`Unexpected request: ${url}`)
    })
    const dispatch = vi.spyOn(window, 'dispatchEvent')
    ;(inDialog('[data-settings-tab="general"]') as HTMLButtonElement).click()
    ;(inDialog('.shutdown-setting button') as HTMLButtonElement).click()
    await flushPromises()
    expect(confirm).toHaveBeenCalledTimes(2)
    expect(confirmText).toHaveBeenCalledTimes(2)
    expect(shutdownCalls.map(call => JSON.parse(String(call.init?.body)))).toEqual([
      { confirmation: '强制关闭', uploads: 1, downloads: 0 },
      { confirmation: '强制关闭', uploads: 2, downloads: 1 },
    ])
    expect(new Headers(shutdownCalls[0].init?.headers).get('X-Lan-Copy-Action')).toBe('shutdown')
    expect(dispatch).toHaveBeenCalledWith(expect.objectContaining({ type: 'lan-copy:shutdown' }))
  })

  it('cycles the accessible tabs with arrow keys and returns focus to the trigger on close', async () => {
    const general = inDialog('[data-settings-tab="general"]') as HTMLButtonElement
    const plugins = inDialog('[data-settings-tab="plugins"]')
    const about = inDialog('[data-settings-tab="about"]')
    about.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }))
    await flushPromises()
    expect(general.getAttribute('aria-selected')).toBe('true')
    about.dispatchEvent(new KeyboardEvent('keydown', { key: 'End', bubbles: true }))
    await flushPromises()
    expect(plugins.getAttribute('aria-selected')).toBe('true')
    ;(inDialog('.settings-close') as HTMLButtonElement).click()
    await flushPromises()
    expect(document.activeElement).toBe(wrapper.get('[aria-label="设置"]').element)
  })
})
