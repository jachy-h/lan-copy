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
  fetchMock = vi.fn(async (url: string) => {
    if (url === '/api/app') return response(app)
    if (url === '/api/update/check') return response(noUpdate)
    if (url === '/api/settings/autostart') return response({ enabled: false, available: false, reason: '本机不可用' })
    if (url === '/api/settings/upload-resources') return response({ config: { concurrentUploads: 3, stagingGiB: 20, idleTimeoutSeconds: 60 }, editable: true })
    if (url === '/api/files') return response({ files: [{ size: 1024 }] })
    if (url === '/api/shutdown/status') return response({ uploads: 0, downloads: 0 })
    throw new Error(`Unexpected request: ${url}`)
  })
  vi.stubGlobal('fetch', fetchMock)
  wrapper = mount(SettingsDialog, { attachTo: document.body })
  await wrapper.get('[aria-label="设置"]').trigger('click')
  await flushPromises()
})

afterEach(() => { wrapper?.unmount(); vi.unstubAllGlobals() })
const inDialog = (selector: string) => {
  const element = document.body.querySelector(selector)
  if (!element) throw new Error(`Missing dialog element: ${selector}`)
  return element
}

describe('SettingsDialog', () => {
  it('loads settings, preserves visitor read-only state, and checks release on its tab', async () => {
    expect(document.body.textContent).toContain('当前版本 1.0.0 · darwin / arm64')
    expect(document.body.textContent).toContain('已占用存储空间：1.0 KiB（1 个文件）')
    const autostart = inDialog('.autostart-toggle') as HTMLInputElement
    expect(autostart.disabled).toBe(true)
    ;(inDialog('[data-settings-tab="update"]') as HTMLButtonElement).click()
    await flushPromises()
    expect(fetchMock).toHaveBeenCalledWith('/api/update/check', expect.objectContaining({ cache: 'no-store' }))
    expect(inDialog('.release-status').textContent).toContain('当前无需更新')
    expect(inDialog('.release-page').getAttribute('href')).toBe(noUpdate.pageURL)
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
    const about = inDialog('[data-settings-tab="about"]')
    about.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }))
    await flushPromises()
    expect(general.getAttribute('aria-selected')).toBe('true')
    ;(inDialog('.settings-close') as HTMLButtonElement).click()
    await flushPromises()
    expect(document.activeElement).toBe(wrapper.get('[aria-label="设置"]').element)
  })
})
