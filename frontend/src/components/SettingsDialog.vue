<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { requestJSON } from '../api/client'
import { notify, raiseToast } from '../ui/fileUi'
import { confirm as confirmDialog, confirmText as confirmTyped } from '../ui/dialog'

interface AppInfo {
  version: string; os: string; arch: string; canShutdown: boolean
  updateEnabled: boolean; updateDisabledReason: string; signedUpdates: boolean
}
interface UpdateInfo { available: boolean; comparable: boolean; version: string; reason?: string; pageURL: string; canInstall: boolean }
interface TransferCounts { uploads: number; downloads: number }
interface UploadConfig { concurrentUploads: number; stagingGiB: number; idleTimeoutSeconds: number }
interface AutoStartState { enabled: boolean; available: boolean; reason?: string }
interface DeployTokenState { configured: boolean; source: 'none' | 'file' | 'environment' }
interface FragmentSettings { expiryDays: number; defaultExpiryDays: number; minExpiryDays: number; maxExpiryDays: number; editable: boolean; reason?: string }
interface StorageTypeUsage { kind: string; bytes: number; files: number }
interface ResourceUsage {
  storage: { bytes: number; files: number; types: StorageTypeUsage[] }
  uploads: { active: number; limit: number }
  staging: { usedBytes: number; limitBytes: number }
  downloads: { active: number }
}
interface AppUsage { cpuPercent: number; memoryBytes: number; cpuReady: boolean; sampledAt: string }
interface PluginUsageItem {
  pluginId: string; name: string; cpuPercent: number; memoryBytes: number
  memoryLimitBytes: number; pids: number; available: boolean; error?: string
}
interface PluginUsage {
  running: number; unavailable: number; cpuPercent: number
  memoryBytes: number; memoryLimitBytes: number; pids: number
  items: PluginUsageItem[]
}
interface SystemUsage { app: AppUsage; plugins: PluginUsage }

const storageKindLabels: Record<string, string> = {
  image: '图片', video: '视频', audio: '音频', document: '文档',
  archive: '压缩包', code: '代码/文本', other: '其他',
}
const storageKindColors: Record<string, string> = {
  image: '#3b82f6', video: '#8b5cf6', audio: '#ec4899', document: '#10b981',
  archive: '#f59e0b', code: '#06b6d4', other: '#94a3b8',
}

const isOpen = ref(false)
const dialogEl = ref<HTMLDialogElement | null>(null)
const triggerEl = ref<HTMLButtonElement | null>(null)
const tab = ref('about')
const info = ref<AppInfo | null>(null)
const releaseInfo = ref<UpdateInfo | null>(null)
const checking = ref(false)
const busy = ref(false)
const shutdownPending = ref(false)
const shutdownError = ref('')
const stopped = ref(false)
const startup = ref<AutoStartState | null>(null)
const startupBusy = ref(false)
const fragmentSettings = ref<FragmentSettings | null>(null)
const fragmentExpiryDays = ref(7)
const fragmentStatus = ref('正在读取碎片到期设置…')
const fragmentBusy = ref(false)
const uploadEditable = ref(false)
const uploadBusy = ref(false)
const uploadConfig = ref<UploadConfig>({ concurrentUploads: 1, stagingGiB: 1, idleTimeoutSeconds: 60 })
const usageText = ref('正在统计资源占用…')
const resourceUsage = ref<ResourceUsage | null>(null)
const resourceBusy = ref(false)
const resourceError = ref(false)
const systemUsage = ref<SystemUsage | null>(null)
const systemBusy = ref(false)
const systemError = ref(false)
const systemText = ref('')
let liveTimer: number | undefined
const uploadStatus = ref('正在读取上传限制…')
const updateStatus = ref('进入此菜单后自动检查新版本。')
const releaseError = ref(false)
const manualStatus = ref('正在检查更新条件…')
const manualError = ref(false)
const automaticStatus = ref('')
const automaticError = ref(false)
const deployState = ref<DeployTokenState | null>(null)
const deployToken = ref('')
const deployBusy = ref(false)
const deployStatus = ref('正在读取自动部署设置…')
const deployError = ref(false)
const progress = ref(0)
const installingUpload = ref(false)
const picker = ref<HTMLInputElement | null>(null)
const updateTabDot = computed(() => Boolean(releaseInfo.value?.available))
const storageTypes = computed(() => resourceUsage.value?.storage.types ?? [])
const appCpuText = computed(() => {
  const app = systemUsage.value?.app
  if (!app) return '读取中…'
  return app.cpuReady ? `${app.cpuPercent.toFixed(1)}%` : '采样中…'
})
const pluginMemoryText = computed(() => {
  const plugins = systemUsage.value?.plugins
  if (!plugins) return '读取中…'
  return plugins.memoryLimitBytes > 0 ? `${formatBytes(plugins.memoryBytes)} / ${formatBytes(plugins.memoryLimitBytes)}` : formatBytes(plugins.memoryBytes)
})
const storageChartLabel = computed(() => {
  const usage = resourceUsage.value
  if (!usage || usage.storage.files === 0) return '文件类型占用：共享空间中还没有文件'
  const parts = usage.storage.types.map(type => `${storageKindLabel(type.kind)} ${formatBytes(type.bytes)}，${type.files} 个文件`)
  return `文件类型占用：合计 ${formatBytes(usage.storage.bytes)}，${usage.storage.files} 个文件；${parts.join('；')}`
})

const showDialog = () => {
  isOpen.value = true
  if (!stopped.value) { void checkRelease(); void loadStartup(); void loadResources(); void loadDeployToken(); void loadFragmentSettings() }
}
const closeDialog = () => {
  if (dialogEl.value?.open) dialogEl.value.close()
  else isOpen.value = false
  automaticStatus.value = ''
  void nextTick(() => triggerEl.value?.focus())
}
watch(isOpen, async open => {
  if (!open) return
  await nextTick()
  if (dialogEl.value && !dialogEl.value.open) dialogEl.value.showModal()
  raiseToast()
})
function onNativeClose() { isOpen.value = false; automaticStatus.value = '' }
function onBackdrop(event: MouseEvent) {
  if (event.target === event.currentTarget) closeDialog()
}
function switchTab(next: string, focus = false) {
  tab.value = next
  if (focus) document.querySelector<HTMLButtonElement>(`[data-settings-tab="${next}"]`)?.focus()
  if (next === 'update') void checkRelease()
  if (next === 'deploy') void loadDeployToken()
  if (next === 'resources') void refreshResourceStats()
}
function onTabKey(event: KeyboardEvent, index: number) {
  const keys = ['ArrowUp', 'ArrowRight', 'ArrowDown', 'ArrowLeft', 'Home', 'End']
  if (!keys.includes(event.key)) return
  event.preventDefault()
  const tabs = ['about', 'general', 'resources', 'update', 'deploy', 'plugins']
  const next = event.key === 'Home' ? 0 : event.key === 'End' ? tabs.length - 1 : (index + (['ArrowUp', 'ArrowLeft'].includes(event.key) ? -1 : 1) + tabs.length) % tabs.length
  switchTab(tabs[next], true)
}

async function loadInfo() {
  try {
    info.value = await requestJSON<AppInfo>('/api/app', { cache: 'no-store', signal: AbortSignal.timeout(5000) })
    if (!busy.value) manualStatus.value = info.value.updateEnabled ? '更新期间服务会短暂离线，完成后此页面将自动刷新。' : info.value.updateDisabledReason
  } catch (error) {
    info.value = null
    manualStatus.value = message(error, '无法读取应用信息')
    manualError.value = true
  }
}
function message(error: unknown, fallback: string) { return error instanceof Error ? error.message : fallback }
const versionLabel = computed(() => info.value ? `当前版本 ${info.value.version} · ${info.value.os} / ${info.value.arch}` : '正在读取版本…')
const shutdownHint = computed(() => shutdownError.value || (stopped.value ? '服务已关闭，可以关闭此页面。' : shutdownPending.value ? '正在关闭…' : !info.value?.canShutdown ? '请通过本机 localhost 页面关闭软件。' : busy.value ? '请等待更新完成后再关闭软件。' : '关闭前会检查正在进行的文件传输。'))
const shutdownDisabled = computed(() => !info.value?.canShutdown || busy.value || shutdownPending.value || stopped.value)
const updateDisabled = computed(() => busy.value || checking.value || shutdownPending.value || stopped.value || !info.value?.updateEnabled)

async function loadStartup() {
  if (startupBusy.value) return
  try { startup.value = await requestJSON<AutoStartState>('/api/settings/autostart', { cache: 'no-store' }) }
  catch (error) { startup.value = { enabled: false, available: false, reason: message(error, '无法读取启动设置') } }
}async function saveStartup() {
  if (!startup.value?.available || startupBusy.value) return
  const enabled = startup.value.enabled
  startupBusy.value = true
  try {
    startup.value = await requestJSON<AutoStartState>('/api/settings/autostart', { method: 'PUT', headers: { 'Content-Type': 'application/json', 'X-Lan-Copy-Action': 'autostart' }, body: JSON.stringify({ enabled }) })
  } catch (error) {
    // A lost response may still have applied the change. Read back OS state.
    await loadStartup()
    startup.value = { ...(startup.value || { enabled, available: false }), reason: message(error, '无法保存启动设置') }
  } finally { startupBusy.value = false }
}
async function loadFragmentSettings() {
  try {
    fragmentSettings.value = await requestJSON<FragmentSettings>('/api/settings/fragments', { cache: 'no-store' })
    fragmentExpiryDays.value = fragmentSettings.value.expiryDays
    fragmentStatus.value = fragmentSettings.value.editable ? '仅影响之后发布的碎片，已发布的到期时间不变。' : fragmentSettings.value.reason || '当前不可编辑。'
  } catch (error) {
    fragmentSettings.value = null
    fragmentStatus.value = message(error, '无法读取碎片到期设置')
  }
}
async function saveFragmentSettings() {
  const current = fragmentSettings.value
  if (!current || fragmentBusy.value) return
  if (!Number.isInteger(fragmentExpiryDays.value) || fragmentExpiryDays.value < current.minExpiryDays || fragmentExpiryDays.value > current.maxExpiryDays) {
    fragmentStatus.value = `请输入 ${current.minExpiryDays}–${current.maxExpiryDays} 天。`
    return
  }
  fragmentBusy.value = true; fragmentStatus.value = '正在保存…'
  try {
    fragmentSettings.value = await requestJSON<FragmentSettings>('/api/settings/fragments', { method: 'PUT', headers: { 'Content-Type': 'application/json', 'X-Lan-Copy-Action': 'fragments' }, body: JSON.stringify({ expiryDays: fragmentExpiryDays.value }) })
    fragmentExpiryDays.value = fragmentSettings.value.expiryDays
    fragmentStatus.value = '已保存，之后发布的碎片将按新时间到期。'
  } catch (error) { fragmentStatus.value = message(error, '无法保存碎片到期设置') }
  finally { fragmentBusy.value = false }
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  const units = ['KiB', 'MiB', 'GiB', 'TiB']; let value = bytes
  for (const unit of units) { value /= 1024; if (value < 1024 || unit === 'TiB') return `${value.toFixed(value < 10 ? 1 : 0)} ${unit}` }
  return `${bytes} B`
}
function storageKindLabel(kind: string) { return storageKindLabels[kind] || kind }
function storageKindColor(kind: string) { return storageKindColors[kind] || storageKindColors.other }
function storagePercent(type: StorageTypeUsage) {
  const total = resourceUsage.value?.storage.bytes || 0
  return total > 0 ? type.bytes / total * 100 : 0
}
function formatPercent(value: number) { return value >= 10 ? `${value.toFixed(0)}%` : `${value.toFixed(1)}%` }
function pluginCpuText(item: PluginUsageItem) { return item.available ? `${item.cpuPercent.toFixed(1)}%` : '—' }
async function loadResourceStats() {
  resourceError.value = false
  usageText.value = '正在统计资源占用…'
  try {
    resourceUsage.value = await requestJSON<ResourceUsage>('/api/resource-usage', { cache: 'no-store' })
    usageText.value = '统计为当前时刻快照。'
  } catch (error) {
    resourceUsage.value = null
    resourceError.value = true
    usageText.value = message(error, '无法统计资源占用')
  }
}
async function refreshResourceStats() {
  if (resourceBusy.value) return
  resourceBusy.value = true
  try { await loadResourceStats() }
  finally { resourceBusy.value = false }
}
async function loadSystemUsage() {
  if (systemBusy.value) return
  systemBusy.value = true
  try {
    systemUsage.value = await requestJSON<SystemUsage>('/api/system-usage', { cache: 'no-store' })
    systemError.value = false
    systemText.value = ''
  } catch (error) {
    systemError.value = true
    systemText.value = message(error, '无法读取运行占用')
  } finally { systemBusy.value = false }
}
function stopLiveUsage() {
  if (liveTimer !== undefined) { window.clearInterval(liveTimer); liveTimer = undefined }
}
function startLiveUsage() {
  stopLiveUsage()
  void loadSystemUsage()
  liveTimer = window.setInterval(() => {
    if (document.visibilityState === 'visible') void loadSystemUsage()
  }, 2000)
}
function refreshResourcePanel() {
  void loadResources()
  void loadSystemUsage()
}
watch([isOpen, tab], ([open, current]) => {
  if (open && current === 'resources') startLiveUsage()
  else stopLiveUsage()
})
async function loadResources() {
  if (resourceBusy.value) return
  resourceBusy.value = true
  try {
    await loadResourceStats()
    try {
      const result = await requestJSON<{ config: UploadConfig; editable: boolean; reason?: string }>('/api/settings/upload-resources', { cache: 'no-store' })
      uploadConfig.value = result.config
      uploadEditable.value = result.editable
      uploadStatus.value = result.editable ? '仅影响后续新上传。' : result.reason || '当前不可编辑。'
    } catch (error) { uploadEditable.value = false; uploadStatus.value = message(error, '无法读取上传限制') }
  } finally { resourceBusy.value = false }
}
async function saveResources() {
  const config = uploadConfig.value
  if (!Number.isInteger(config.concurrentUploads) || config.concurrentUploads < 1 || config.concurrentUploads > 32 || !Number.isInteger(config.stagingGiB) || config.stagingGiB < 1 || config.stagingGiB > 1024 || !Number.isInteger(config.idleTimeoutSeconds) || config.idleTimeoutSeconds < 5 || config.idleTimeoutSeconds > 600) {
    uploadStatus.value = '请输入有效范围：并发 1–32、暂存 1–1024 GiB、空闲超时 5–600 秒。'; return
  }
  uploadBusy.value = true; uploadStatus.value = '正在保存…'
  try {
    const result = await requestJSON<{ config: UploadConfig; editable: boolean }>('/api/settings/upload-resources', { method: 'PUT', headers: { 'Content-Type': 'application/json', 'X-Lan-Copy-Action': 'upload-resources' }, body: JSON.stringify(config) })
    uploadConfig.value = result.config; uploadEditable.value = result.editable; uploadStatus.value = '已保存。'
    if (resourceUsage.value) {
      resourceUsage.value.uploads.limit = result.config.concurrentUploads
      resourceUsage.value.staging.limitBytes = result.config.stagingGiB * (1 << 30)
    }
  } catch (error) { uploadStatus.value = message(error, '无法保存上传限制') }
  finally { uploadBusy.value = false }
}

function deployStateLabel(state: DeployTokenState) {
  if (state.source === 'environment') return '当前使用启动环境变量 LAN_COPY_DEPLOY_TOKEN 中的令牌。保存新令牌后将改用页面设置。'
  if (state.source === 'file') return '已保存自动部署令牌，部署脚本可以使用同一个令牌。'
  return '未设置令牌。保存后自动部署接口立即启用。'
}
async function loadDeployToken() {
  try {
    const state = await requestJSON<DeployTokenState>('/api/settings/deploy-token', { cache: 'no-store' })
    deployState.value = state; deployStatus.value = deployStateLabel(state); deployError.value = false
  } catch (error) {
    deployState.value = null; deployStatus.value = message(error, '无法读取自动部署设置'); deployError.value = true
  }
}
async function saveDeployToken() {
  if (deployBusy.value || stopped.value) return
  const token = deployToken.value.trim()
  if (!token) { deployStatus.value = '请粘贴部署令牌。'; deployError.value = true; return }
  deployBusy.value = true; deployStatus.value = '正在保存…'; deployError.value = false
  try {
    deployState.value = await requestJSON<DeployTokenState>('/api/settings/deploy-token', { method: 'PUT', headers: { 'Content-Type': 'application/json', 'X-Lan-Copy-Action': 'deploy-token' }, body: JSON.stringify({ token }) })
    deployToken.value = ''; deployStatus.value = '部署令牌已保存，自动部署接口已启用。'; notify('部署令牌已保存')
  } catch (error) { deployStatus.value = message(error, '无法保存部署令牌'); deployError.value = true }
  finally { deployBusy.value = false }
}
async function clearDeployToken() {
  if (deployBusy.value || deployState.value?.source !== 'file') return
  if (!await confirmDialog({ title: '删除部署令牌？', message: '删除后自动部署接口将停止接受请求，直到重新保存令牌。', confirmText: '删除', danger: true })) return
  deployBusy.value = true; deployStatus.value = '正在删除…'; deployError.value = false
  try {
    deployState.value = await requestJSON<DeployTokenState>('/api/settings/deploy-token', { method: 'DELETE', headers: { 'X-Lan-Copy-Action': 'deploy-token' } })
    deployStatus.value = deployState.value.configured ? '页面保存的令牌已删除，继续使用启动环境变量中的令牌。' : '部署令牌已删除，自动部署接口已关闭。'
    notify('部署令牌已删除')
  } catch (error) { deployStatus.value = message(error, '无法删除部署令牌'); deployError.value = true }
  finally { deployBusy.value = false }
}

async function checkRelease() {
  if (busy.value || checking.value || shutdownPending.value || stopped.value) return
  checking.value = true; releaseInfo.value = null; automaticStatus.value = ''; updateStatus.value = '正在检查 GitHub 最新版本…'; releaseError.value = false
  try {
    await loadInfo()
    const result = await requestJSON<UpdateInfo>('/api/update/check', { cache: 'no-store', signal: AbortSignal.timeout(15000) })
    releaseInfo.value = result
    updateStatus.value = result.available ? `发现新版本 ${result.version}${result.reason ? ` · ${result.reason}` : ''}` : !result.comparable ? `最新版本: ${result.version}` : `当前无需更新（最新正式版 ${result.version}）。`
  } catch (error) { updateStatus.value = error instanceof DOMException && error.name === 'TimeoutError' ? '检查超时，请重试或使用离线更新包。' : message(error, '检查更新失败'); releaseError.value = true }
  finally { checking.value = false }
}
async function downloadUpdate() {
  if (updateDisabled.value || !releaseInfo.value?.canInstall) return
  await runInstall(async () => {
    automaticStatus.value = '正在下载更新包并校验，请稍候…'
    const response = await fetch('/api/update/download', { method: 'POST', headers: { 'X-Lan-Copy-Action': 'update' } })
    const result = await response.json()
    if (response.status !== 202) throw new Error(result.error || '下载更新失败')
    return result
  }, false, true)
}

async function shutdown() {
  if (shutdownDisabled.value || shutdownPending.value) return
  shutdownError.value = ''; shutdownPending.value = true
  try {
    const transfers = await requestJSON<TransferCounts & { stopping?: boolean }>('/api/shutdown/status', { cache: 'no-store' })
    if (transfers.stopping) throw new Error('服务正在关闭')
    let acknowledged = { uploads: transfers.uploads, downloads: transfers.downloads }
    const warning = acknowledged.uploads + acknowledged.downloads ? `\n\n当前有 ${acknowledged.uploads} 个上传、${acknowledged.downloads} 个下载正在进行。继续关闭可能中断这些传输。` : '\n\n当前没有正在进行的文件传输。'
    if (!await confirmDialog({ title: '关闭 Lan Copy？', message: `关闭后，所有设备都将无法继续访问。共享文件和文本会保留，可在此电脑上重新启动 Lan Copy。${warning}`, confirmText: '继续关闭', danger: true })) return
    let confirmation = ''
    const typed = (counts: TransferCounts) => confirmTyped({ title: '确认中断文件传输', message: `当前有 ${counts.uploads} 个上传、${counts.downloads} 个下载。继续关闭可能中断这些传输。请输入“强制关闭”确认。`, requiredText: '强制关闭', confirmText: '强制关闭' })
    if (acknowledged.uploads + acknowledged.downloads) { if (!await typed(acknowledged)) return; confirmation = '强制关闭' }
    let response!: Response
    for (let attempt = 0; attempt < 5; attempt++) {
      response = await fetch('/api/shutdown', { method: 'POST', headers: { 'X-Lan-Copy-Action': 'shutdown', 'Content-Type': 'application/json' }, body: JSON.stringify({ confirmation, ...acknowledged }) })
      if (response.status !== 409) break
      const conflict = await response.json()
      if (!conflict.requiresConfirmation) throw new Error(conflict.error || '关闭失败，请重试')
      acknowledged = { uploads: conflict.uploads, downloads: conflict.downloads }
      const changed = acknowledged.uploads + acknowledged.downloads ? `传输数量已变化。当前有 ${acknowledged.uploads} 个上传、${acknowledged.downloads} 个下载；强制关闭可能中断这些传输。` : '传输数量已变化，目前没有正在进行的上传或下载。'
      if (!await confirmDialog({ title: '请核对最新传输状态', message: changed, confirmText: '继续关闭', danger: true })) return
      confirmation = ''
      if (acknowledged.uploads + acknowledged.downloads) { if (!await typed(acknowledged)) return; confirmation = '强制关闭' }
    }
    if (response.status === 409) throw new Error('文件传输状态持续变化，请等待传输稳定后再关闭。')
    const result = await response.json().catch(() => ({}))
    if (!response.ok) throw new Error(result.error || `关闭失败（${response.status}）`)
    stopped.value = true
    window.dispatchEvent(new Event('lan-copy:shutdown'))
    notify('Lan Copy 已关闭，可以关闭此页面')
  } catch (error) { shutdownError.value = message(error, '关闭失败，请重试') }
  finally { shutdownPending.value = false }
}

function uploadFile(file: File): Promise<{ updateID: string }> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest(); xhr.open('POST', '/api/update'); xhr.setRequestHeader('X-Lan-Copy-Action', 'update'); xhr.setRequestHeader('Content-Type', 'application/octet-stream')
    xhr.upload.onprogress = event => { if (event.lengthComputable) { progress.value = event.loaded / event.total * 100; manualStatus.value = progress.value === 100 ? '正在校验更新包…' : `正在上传 ${Math.round(progress.value)}%` } }
    xhr.onload = () => { let result; try { result = JSON.parse(xhr.responseText) } catch { reject(new Error('更新服务返回了无效响应')); return } if (xhr.status !== 202) { reject(new Error(result.error || '更新失败')); return } resolve(result) }
    xhr.onerror = () => reject(new Error('连接中断。请稍后重新打开设置，检查当前版本与服务状态。'))
    xhr.send(file)
  })
}
async function waitForRestart(result: { updateID: string }, report: (text: string, error?: boolean) => void) {
  const deadline = Date.now() + 90000
  while (Date.now() < deadline) {
    await new Promise(resolve => setTimeout(resolve, 1000))
    try {
      const response = await fetch('/api/app', { cache: 'no-store', signal: AbortSignal.timeout(2000) })
      if (!response.ok) continue
      const current = await response.json()
      if (current.updateID === result.updateID) { report(`已更新至 ${current.version}，正在刷新…`); location.reload(); return }
      if (current.updateID === `rollback-${result.updateID}`) { report('新版本启动失败，已恢复旧版本。详情请查看程序目录下的 lan-copy-update.log。', true); return }
    } catch { /* service restart is expected to be temporarily offline */ }
  }
  report('重启未能在预期时间内完成。请查看程序目录下的 lan-copy-update.log，或手动启动程序。', true)
}
async function install(files: File[]) {
  if (busy.value || shutdownPending.value || stopped.value || !info.value?.updateEnabled) return
  if (files.length !== 1 || !/\.(zip|tar\.gz|tgz)$/i.test(files[0].name)) { manualStatus.value = '请选择一个 .zip 或 .tar.gz 发布包。'; manualError.value = true; return }
  if (files[0].size > 128 * 1024 * 1024) { manualStatus.value = '更新包不能超过 128 MiB。'; manualError.value = true; return }
  await runInstall(() => uploadFile(files[0]), true)
}
async function runInstall(start: () => Promise<{ updateID: string }>, uploading = false, automatic = false) {
  if (busy.value || shutdownPending.value || stopped.value || !info.value?.updateEnabled) return
  busy.value = true; installingUpload.value = uploading; progress.value = 0
  try {
    const result = await start(); installingUpload.value = false
    const report = (text: string, error = false) => { if (automatic) { automaticStatus.value = text; automaticError.value = error } else { manualStatus.value = text; manualError.value = error } }
    report('校验通过，正在安装并重启…')
    await waitForRestart(result, report)
  } catch (error) {
    if (automatic) { automaticStatus.value = message(error, '更新失败'); automaticError.value = true }
    else { manualStatus.value = message(error, '更新失败'); manualError.value = true }
  } finally { busy.value = false; installingUpload.value = false; await refreshInfo() }
}
async function refreshInfo() {
  try { info.value = await requestJSON<AppInfo>('/api/app', { cache: 'no-store', signal: AbortSignal.timeout(2000) }) }
  catch { /* retain last known service capabilities */ }
}
function consumeModalDrop(event: DragEvent) { event.preventDefault(); event.stopPropagation() }
function onDrop(event: DragEvent) { consumeModalDrop(event); void install(Array.from(event.dataTransfer?.files || [])) }
function onPicker(event: Event) { const input = event.target as HTMLInputElement; const files = Array.from(input.files || []); input.value = ''; void install(files) }
function openPicker() { if (!updateDisabled.value) picker.value?.click() }
function onVisibility() {
  if (document.visibilityState !== 'visible' || !isOpen.value) return
  void refreshInfo()
  if (tab.value === 'resources') void loadSystemUsage()
}
watch(isOpen, open => { if (open) document.addEventListener('visibilitychange', onVisibility); else document.removeEventListener('visibilitychange', onVisibility) })
onBeforeUnmount(() => { document.removeEventListener('visibilitychange', onVisibility); stopLiveUsage() })
</script>

<template>
  <button ref="triggerEl" class="share-access-button settings-trigger" type="button" aria-label="设置" title="设置" aria-haspopup="dialog" @click="showDialog">
    <svg viewBox="0 0 24 24" aria-hidden="true"><path d="m9 3-.6 2.2-2 .9-2-.6-2 3.5 1.5 1.6v2.3L2.4 15l2 3.5 2.1-.6 2 .9L9 21h4l.6-2.2 2-.9 2 .6 2-3.5-1.5-1.6v-2.3L19.6 9l-2-3.5-2.1.6-2-.9L13 3Z"/><circle cx="11" cy="12" r="3"/></svg>
  </button>
  <Teleport to="body">
    <dialog v-if="isOpen" ref="dialogEl" class="app-dialog settings-dialog" aria-labelledby="settingsTitle" @close="onNativeClose" @cancel.prevent="closeDialog" @click="onBackdrop" @dragenter="consumeModalDrop" @dragover="consumeModalDrop" @dragleave="consumeModalDrop" @drop="consumeModalDrop">
      <header class="settings-header"><h2 id="settingsTitle">设置</h2><button type="button" class="settings-close" aria-label="关闭设置" @click="closeDialog">×</button></header>
      <div class="settings-layout">
        <nav class="settings-menu" aria-label="设置菜单" role="tablist" aria-orientation="vertical">
          <button v-for="(entry, index) in [{id:'about',name:'关于',desc:'版本与项目'},{id:'general',name:'通用',desc:'启动与关闭'},{id:'resources',name:'资源',desc:'存储与上传限制'},{id:'update',name:'更新',desc:'检查并安装版本'},{id:'deploy',name:'部署',desc:'自动部署令牌'},{id:'plugins',name:'插件',desc:'安装与运行插件'}]" :key="entry.id" :id="`${entry.id}Tab`" type="button" role="tab" :data-settings-tab="entry.id" :aria-selected="tab===entry.id" :aria-controls="`${entry.id}Settings`" :tabindex="tab===entry.id?0:-1" @click="switchTab(entry.id)" @keydown="onTabKey($event,index)">
            <span>{{ entry.name }}<span v-if="entry.id==='update'&&updateTabDot" class="update-tab-dot" aria-label="有新版本" /></span><small>{{ entry.desc }}</small>
          </button>
        </nav>
        <div class="settings-content">
          <section v-show="tab==='about'" id="aboutSettings" role="tabpanel" aria-labelledby="aboutTab"><img :src="'/logo/lan-copy-logo.svg'" width="56" height="56" alt=""><h3>Lan Copy</h3><p>一个文件，整个局域网互传。</p><p class="settings-version">{{ versionLabel }}</p><a href="https://github.com/jachy-h/lan-copy" target="_blank" rel="noopener noreferrer">项目主页 ↗</a></section>
          <section v-show="tab==='general'" id="generalSettings" role="tabpanel" aria-labelledby="generalTab"><h3>启动设置</h3><label class="autostart-option"><span>开机自启动</span><input type="checkbox" class="autostart-toggle" role="switch" :checked="startup?.enabled||false" :disabled="!startup?.available||startupBusy||stopped" @change="startup && (startup.enabled=($event.target as HTMLInputElement).checked); saveStartup()"></label><p>登录此电脑后，自动在后台启动 Lan Copy，不打开浏览器。关闭软件后，下次登录仍会启动。</p><p>开启后请保留程序与共享文件夹的位置；移动程序后需重新开启此选项。</p><p class="autostart-status" role="status" aria-live="polite">{{ startup?.reason || (startup ? startup.enabled ? '已开启，下次登录时自动启动。' : '未开启开机自启动。' : '正在读取启动设置…') }}</p><div class="fragment-setting"><h3>碎片快传</h3><p>文字和图片碎片默认 7 天到期，到期后自动清理。修改后仅影响之后发布的碎片。</p><label class="upload-resource-field"><span>到期时间（天）<small>范围 {{ fragmentSettings?.minExpiryDays || 1 }}–{{ fragmentSettings?.maxExpiryDays || 365 }} 天</small></span><input v-model.number="fragmentExpiryDays" class="fragment-expiry-days" type="number" :min="fragmentSettings?.minExpiryDays || 1" :max="fragmentSettings?.maxExpiryDays || 365" step="1" :disabled="!fragmentSettings?.editable||fragmentBusy"></label><button class="primary fragment-setting-save" type="button" :disabled="!fragmentSettings?.editable||fragmentBusy" @click="saveFragmentSettings">保存设置</button><p class="fragment-status" role="status" aria-live="polite">{{ fragmentStatus }}</p></div><div class="shutdown-setting"><h3>关闭软件</h3><p>关闭后，所有设备都将无法继续访问。共享文件和文本会保留。</p><button class="secondary danger" type="button" :disabled="shutdownDisabled" aria-describedby="shutdownHint" @click="shutdown">{{ stopped ? '已关闭' : '关闭 Lan Copy' }}</button><p id="shutdownHint" role="status" aria-live="polite">{{ shutdownHint }}</p></div></section>
          <section v-show="tab==='resources'" id="resourcesSettings" role="tabpanel" aria-labelledby="resourcesTab">
            <div class="resource-heading"><h3>资源详情</h3><button class="secondary resource-refresh" type="button" :disabled="resourceBusy" @click="refreshResourcePanel">刷新</button></div>
            <p>查看共享空间、文件类型、传输任务和运行占用的当前状态。运行数据每 2 秒自动刷新。</p>
            <dl v-if="resourceUsage" class="resource-stats">
              <div class="resource-stat"><dt>共享文件</dt><dd><strong>{{ formatBytes(resourceUsage.storage.bytes) }}</strong><span>{{ resourceUsage.storage.files }} 个文件</span></dd></div>
              <div class="resource-stat"><dt>上传任务</dt><dd><strong>{{ resourceUsage.uploads.limit ? `${resourceUsage.uploads.active} / ${resourceUsage.uploads.limit}` : `${resourceUsage.uploads.active} / 未启用` }}</strong><span>进行中 / 并发上限</span></dd><progress v-if="resourceUsage.uploads.limit" :max="resourceUsage.uploads.limit" :value="Math.min(resourceUsage.uploads.active, resourceUsage.uploads.limit)" aria-label="上传并发占用"></progress></div>
              <div class="resource-stat"><dt>暂存空间</dt><dd><strong>{{ resourceUsage.staging.limitBytes ? `${formatBytes(resourceUsage.staging.usedBytes)} / ${formatBytes(resourceUsage.staging.limitBytes)}` : `${formatBytes(resourceUsage.staging.usedBytes)} / 未启用` }}</strong><span>已预留 / 配置上限</span></dd><progress v-if="resourceUsage.staging.limitBytes" :max="resourceUsage.staging.limitBytes" :value="Math.min(resourceUsage.staging.usedBytes, resourceUsage.staging.limitBytes)" aria-label="暂存空间占用"></progress></div>
              <div class="resource-stat"><dt>下载任务</dt><dd><strong>{{ resourceUsage.downloads.active }}</strong><span>个进行中</span></dd></div>
            </dl>
            <p class="resource-usage" :class="{error: resourceError}" role="status" aria-live="polite">{{ usageText }}</p>
            <div v-if="resourceUsage" class="storage-breakdown">
              <div class="storage-breakdown-head"><h4>文件类型</h4><span>合计 {{ formatBytes(resourceUsage.storage.bytes) }} · {{ resourceUsage.storage.files }} 个文件</span></div>
              <div v-if="resourceUsage.storage.files" class="storage-bar" role="img" :aria-label="storageChartLabel">
                <span v-for="type in storageTypes" :key="type.kind" class="storage-segment" :style="{ width: `${storagePercent(type)}%`, background: storageKindColor(type.kind) }" :title="`${storageKindLabel(type.kind)} ${formatBytes(type.bytes)}`"></span>
              </div>
              <p v-else class="resource-empty">共享空间中还没有文件。</p>
              <ul v-if="storageTypes.length" class="storage-legend">
                <li v-for="type in storageTypes" :key="type.kind"><span class="storage-dot" :style="{ background: storageKindColor(type.kind) }" aria-hidden="true"></span><span class="storage-legend-name">{{ storageKindLabel(type.kind) }}</span><b>{{ formatBytes(type.bytes) }}</b><span>{{ type.files }} 个 · {{ formatPercent(storagePercent(type)) }}</span></li>
              </ul>
            </div>
            <div class="live-usage">
              <div class="live-usage-head"><h4>运行占用</h4><span>每 2 秒自动刷新</span></div>
              <dl class="resource-stats live-stats">
                <div class="resource-stat"><dt>Lan Copy CPU</dt><dd><strong>{{ appCpuText }}</strong><span>进程单核占用</span></dd></div>
                <div class="resource-stat"><dt>Lan Copy 内存</dt><dd><strong>{{ systemUsage ? formatBytes(systemUsage.app.memoryBytes) : '读取中…' }}</strong><span>Go 运行时内存</span></dd></div>
                <div class="resource-stat"><dt>插件 CPU</dt><dd><strong>{{ systemUsage ? `${systemUsage.plugins.cpuPercent.toFixed(1)}%` : '读取中…' }}</strong><span>{{ systemUsage?.plugins.running || 0 }} 个运行中</span></dd></div>
                <div class="resource-stat"><dt>插件内存</dt><dd><strong>{{ pluginMemoryText }}</strong><span>{{ systemUsage?.plugins.unavailable ? `${systemUsage.plugins.unavailable} 个采样失败` : '已用 / 上限' }}</span></dd></div>
              </dl>
              <p v-if="systemError" class="resource-usage error" role="status" aria-live="polite">{{ systemText }}</p>
              <div v-if="systemUsage?.plugins.running" class="plugin-usage">
                <table class="plugin-usage-table">
                  <thead><tr><th>插件</th><th>CPU</th><th>内存</th><th>PID</th></tr></thead>
                  <tbody>
                    <tr v-for="item in systemUsage.plugins.items" :key="item.pluginId">
                      <td><strong>{{ item.name }}</strong><small>{{ item.pluginId }}</small><small v-if="!item.available" class="plugin-down">{{ item.error || '暂时不可用' }}</small></td>
                      <td>{{ pluginCpuText(item) }}</td>
                      <td>{{ item.available ? formatBytes(item.memoryBytes) : '—' }}</td>
                      <td>{{ item.available ? item.pids : '—' }}</td>
                    </tr>
                  </tbody>
                </table>
              </div>
              <p v-else-if="systemUsage" class="resource-empty">暂无运行中的插件。</p>
            </div>
            <section class="resource-config" aria-labelledby="resourceConfigTitle">
              <h3 id="resourceConfigTitle">资源配置</h3>
              <p>设置上传并发数、临时接收空间和连接空闲时间。限制仅影响之后开始的上传。</p>
              <label class="upload-resource-field"><span>并发上传数<small>同时接收文件的任务数量上限</small></span><input v-model.number="uploadConfig.concurrentUploads" class="upload-concurrent" type="number" min="1" max="32" step="1" :disabled="!uploadEditable||uploadBusy"></label>
              <label class="upload-resource-field"><span>暂存空间（GiB）<small>所有进行中上传共用的临时空间上限</small></span><input v-model.number="uploadConfig.stagingGiB" class="upload-staging" type="number" min="1" max="1024" step="1" :disabled="!uploadEditable||uploadBusy"></label>
              <label class="upload-resource-field"><span>读取空闲超时（秒）<small>上传连接持续无数据时自动中止的等待时间</small></span><input v-model.number="uploadConfig.idleTimeoutSeconds" class="upload-idle" type="number" min="5" max="600" step="1" :disabled="!uploadEditable||uploadBusy"></label>
              <button class="primary upload-resource-save" type="button" :disabled="!uploadEditable||uploadBusy" @click="saveResources">保存设置</button>
              <p class="upload-resource-status" role="status" aria-live="polite">{{ uploadStatus }}</p>
            </section>
          </section>
          <section v-show="tab==='update'" id="updateSettings" role="tabpanel" aria-labelledby="updateTab"><h3>自动更新</h3><p class="settings-version">{{ versionLabel }}</p><p>局域网内的访客也可以发起更新；更新的是运行 Lan Copy 的电脑。</p><p class="release-status" :class="{success:releaseInfo&&!releaseError,error:releaseError}" role="status" aria-live="polite">{{ updateStatus }}</p><div class="release-actions"><div class="release-install-group"><button v-if="releaseInfo?.available" class="primary release-install" type="button" :disabled="updateDisabled||!releaseInfo.canInstall" @click="downloadUpdate">下载并更新</button><p v-if="automaticStatus" class="release-install-status" :class="{error:automaticError}" role="status" aria-live="polite">{{ automaticStatus }}</p></div><a v-if="releaseInfo&&(releaseInfo.available||releaseInfo.comparable)" class="release-page" :href="releaseInfo.pageURL" target="_blank" rel="noopener noreferrer">前往查看 ↗</a><button class="secondary release-check" type="button" :disabled="updateDisabled" @click="checkRelease">重新检查</button></div><h3 class="manual-update-title">手动更新</h3><p class="update-explanation">拖入本机系统对应的新版发布包，校验通过后自动安装并重启。共享文件和文字会保留，请先等待其他文件传输完成。</p><button class="update-dropzone" type="button" :disabled="updateDisabled" @click="openPicker" @dragover.prevent="($event.currentTarget as HTMLButtonElement).classList.add('dragging')" @dragleave="($event.currentTarget as HTMLButtonElement).classList.remove('dragging')" @drop="onDrop"><strong>拖入更新包，或点击选择</strong><span>支持 .zip / .tar.gz · 最大 128 MiB</span></button><input ref="picker" type="file" accept=".zip,.gz,.tgz" hidden @change="onPicker"><p class="update-trust">{{ info?.signedUpdates ? '所有更新包必须通过内置可信公钥的签名验证。' : '尚未配置可信更新公钥，自动更新已禁用。' }}</p><progress class="update-progress" max="100" :value="progress" :hidden="!installingUpload" aria-label="更新包上传进度"/><p class="update-status" :class="{error:manualError}" role="status" aria-live="polite">{{ manualStatus }}</p></section>
          <section v-show="tab==='deploy'" id="deploySettings" role="tabpanel" aria-labelledby="deployTab"><h3>自动部署</h3><p>保存部署令牌后，发布脚本可以使用同一个令牌上传签名包并重启服务。任何能打开本页面的人都可以设置、修改或删除此令牌。</p><label class="upload-resource-field deploy-token-field"><span>部署令牌<small>从部署环境复制的令牌，或使用至少 32 位随机字符</small></span><input v-model="deployToken" class="deploy-token-input" type="password" autocomplete="new-password" autocapitalize="off" spellcheck="false" placeholder="粘贴部署令牌" :disabled="deployBusy||stopped" @keydown.enter.prevent="saveDeployToken"></label><div class="deploy-token-actions"><button class="primary deploy-token-save" type="button" :disabled="deployBusy||stopped" @click="saveDeployToken">保存令牌</button><button class="secondary danger deploy-token-clear" type="button" :disabled="deployBusy||stopped||deployState?.source!=='file'" @click="clearDeployToken">删除令牌</button></div><p class="deploy-token-status" :class="{error:deployError}" role="status" aria-live="polite">{{ deployStatus }}</p></section>
          <section v-show="tab==='plugins'" id="pluginsSettings" role="tabpanel" aria-labelledby="pluginsTab"><h3>插件服务</h3><p>安装、运行并调用独立的本地插件。插件以受限容器运行，局域网内可管理，请只安装可信镜像。</p><p>插件页面包含插件安装与启停、离线镜像导入、生命周期事件对接和智能表格 API 说明。</p><a class="primary settings-plugin-launch" href="/plugins" target="_blank" rel="noopener noreferrer">在新标签页打开插件服务 ↗</a></section>
        </div>
      </div>
    </dialog>
  </Teleport>
</template>
