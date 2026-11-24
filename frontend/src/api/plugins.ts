import { ApiError, requestJSON } from './client'

export type PluginStatus =
  | 'not_installed' | 'installing' | 'stopped' | 'starting' | 'running'
  | 'unhealthy' | 'stopping' | 'failed' | 'uninstalling'
export type HealthStatus = 'unknown' | 'checking' | 'healthy' | 'unhealthy'

export interface PluginRuntime {
  type: string
  available: boolean
  version?: string
  apiVersion?: string
  os?: string
  architecture?: string
  networkMode: 'loopback' | 'container'
  network: string
  apiBaseUrl?: string
  apiBaseUrlReason?: string
  dataRoots?: string[]
  error?: string
  orphans?: string[]
  startAvailable?: boolean
}

export interface Plugin {
  id: string; name: string; description?: string; category?: string
  protocolVersion: string; currentVersion?: string
  status: PluginStatus; createdAt: string; updatedAt: string
}

export interface ManifestEndpoints {
  health: string; manifest: string; version: string; capabilities: string; events?: string
}
export interface ManifestDefaults {
  cpuLimit?: number; memoryMB?: number; pidsLimit?: number
  requestTimeoutSeconds?: number; startupTimeoutSeconds?: number
}
export interface ManifestUI {
  path?: string
}
export interface PluginManifest {
  schemaVersion: number; id: string; name: string; version: string
  description?: string; protocolVersion: string; runtime: string; category?: string
  containerPort: number; endpoints: ManifestEndpoints; capabilities: string[]
  events?: string[]
  ui?: ManifestUI
  defaults?: ManifestDefaults
}

export interface PluginImage {
  id: string; pluginId: string; runtimeImageId: string; reference: string
  imageName: string; imageTag?: string; digest: string; registry?: string
  architecture: string; os: string; sizeBytes: number; source: string
  archiveSha256?: string; manifest: PluginManifest; createdAt: string
}

export interface EnvVar { name: string; value?: string; secret?: boolean; clear?: boolean }
export interface Mount { hostPath: string; containerPath: string; readOnly?: boolean }
export interface HealthConfig {
  path: string; intervalSeconds: number; timeoutSeconds: number
  retries: number; startupTimeoutSeconds: number
}
export interface PluginConfig {
  env: EnvVar[]; cpuLimit: number; memoryMB: number; pidsLimit: number
  restartPolicy: string; mounts: Mount[]; healthCheck: HealthConfig
  requestTimeoutSeconds: number; logMaxSizeMB: number; logMaxFiles: number
}

export interface PluginInstance {
  id: string; pluginId: string; imageId: string; runtimeType: string
  runtimeInstanceId: string; containerName: string; internalAddress?: string
  hostPort?: number; status: PluginStatus; healthStatus: HealthStatus
  statusReason?: string; warnings?: string[]; restartCount: number
  startedAt?: string; lastHealthAt?: string
  config: PluginConfig; createdAt: string; updatedAt: string
}

export interface CallMetrics {
  calls: number; errors: number; totalDurationMs: number
  lastDurationMs: number; lastErrorAt?: string
}

export interface PluginView {
  plugin: Plugin
  image?: PluginImage
  instance?: PluginInstance
  metrics: CallMetrics
}

export interface HealthRecord {
  at: string; status: string; statusCode?: number; latencyMs: number; error?: string
}

export interface PluginDetail {
  plugin: Plugin
  images: PluginImage[]
  instance?: PluginInstance
  manifest: PluginManifest
  healthRecords: HealthRecord[]
  metrics: CallMetrics
  runtime: PluginRuntime
}

export interface PluginStats {
  cpuPercent: number; memoryBytes: number; memoryLimitBytes: number
  pids: number; sampledAt: string
}

export interface PluginStatusView {
  pluginId: string; instanceId?: string; status: PluginStatus
  healthStatus: HealthStatus; statusReason?: string; warnings?: string[]
  address?: string; startedAt?: string; lastHealthAt?: string
  restartCount: number; healthRecords: HealthRecord[]
  stats?: PluginStats; metrics: CallMetrics
}

export interface PluginEventCatalogEntry {
  type: string; category: string; description: string; data: unknown
}
export interface PluginEventStats {
  queued: number; delivered: number; failed: number; dropped: number
  lastDeliveredAt?: string; lastErrorAt?: string; lastError?: string
}
export interface PluginEventDelivery {
  eventId: string; type: string; at: string
  status: 'delivered' | 'failed' | 'dropped'
  attempts: number; statusCode?: number; durationMs: number; error?: string
}
export interface PluginEventEndpoint {
  endpoint: string; subscriptions: string[]
  stats: PluginEventStats; deliveries: PluginEventDelivery[]
}

export type TaskStatus = 'pending' | 'running' | 'succeeded' | 'failed'
export interface PluginTask {
  id: string; kind: string; pluginId?: string; status: TaskStatus
  progress: number; message?: string; error?: string
  result?: unknown; createdAt: string; updatedAt: string
}

const actionHeaders = { 'X-Lan-Copy-Action': 'plugins' }

export function getPluginRuntime(signal?: AbortSignal) {
  return requestJSON<PluginRuntime>('/api/plugins/runtime', { cache: 'no-store', signal })
}
export function startPluginRuntime() {
  return requestJSON<{ taskId: string }>('/api/plugins/runtime/start', {
    method: 'POST', headers: actionHeaders,
  })
}
export function listPlugins(signal?: AbortSignal) {
  return requestJSON<{ plugins: PluginView[] }>('/api/plugins', { cache: 'no-store', signal })
}
export function getPlugin(id: string, signal?: AbortSignal) {
  return requestJSON<PluginDetail>(`/api/plugins/${encodeURIComponent(id)}`, { cache: 'no-store', signal })
}
export function listPluginImages(signal?: AbortSignal) {
  return requestJSON<{ images: PluginImage[] }>('/api/plugins/images', { cache: 'no-store', signal })
}
export function deletePluginImage(id: string) {
  return requestJSON<void>(`/api/plugins/images/${encodeURIComponent(id)}`, { method: 'DELETE', headers: actionHeaders })
}
export function installPlugin(input: {
  imageId: string; expectedPluginId?: string; config: Partial<PluginConfig>; start: boolean
}) {
  return requestJSON<{ taskId: string }>('/api/plugins/install', {
    method: 'POST',
    headers: { ...actionHeaders, 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  })
}
export function pluginAction(id: string, action: 'start' | 'stop' | 'restart') {
  return requestJSON<{ taskId: string }>(`/api/plugins/${encodeURIComponent(id)}/${action}`, {
    method: 'POST', headers: actionHeaders,
  })
}
export function uninstallPlugin(id: string, options: { deleteImages: boolean; deleteData: boolean; confirmDataDeletion?: boolean }) {
  return requestJSON<{ taskId: string }>(`/api/plugins/${encodeURIComponent(id)}`, {
    method: 'DELETE',
    headers: { ...actionHeaders, 'Content-Type': 'application/json' },
    body: JSON.stringify(options),
  })
}
export function getPluginStatus(id: string, signal?: AbortSignal) {
  return requestJSON<PluginStatusView>(`/api/plugins/${encodeURIComponent(id)}/status`, { cache: 'no-store', signal })
}
export function getPluginEventCatalog(signal?: AbortSignal) {
  return requestJSON<{ events: PluginEventCatalogEntry[] }>('/api/plugins/event-catalog', { cache: 'no-store', signal })
}
export function getPluginEvents(id: string, signal?: AbortSignal) {
  return requestJSON<PluginEventEndpoint>(`/api/plugins/${encodeURIComponent(id)}/events`, { cache: 'no-store', signal })
}
export function sendPluginTestEvent(id: string) {
  return requestJSON<{ eventId: string }>(`/api/plugins/${encodeURIComponent(id)}/events/test`, {
    method: 'POST', headers: actionHeaders,
  })
}
export function getPluginConfig(id: string, signal?: AbortSignal) {
  return requestJSON<PluginConfig>(`/api/plugins/${encodeURIComponent(id)}/config`, { cache: 'no-store', signal })
}
export function updatePluginConfig(id: string, config: PluginConfig) {
  return requestJSON<PluginConfig>(`/api/plugins/${encodeURIComponent(id)}/config`, {
    method: 'PUT',
    headers: { ...actionHeaders, 'Content-Type': 'application/json' },
    body: JSON.stringify(config),
  })
}
export function getPluginTask(id: string, signal?: AbortSignal) {
  return requestJSON<PluginTask>(`/api/plugins/tasks/${encodeURIComponent(id)}`, { cache: 'no-store', signal })
}
export async function getPluginLogs(id: string, tail = 200, signal?: AbortSignal) {
  const response = await fetch(`/api/plugins/${encodeURIComponent(id)}/logs?tail=${tail}`, { cache: 'no-store', signal })
  if (!response.ok) {
    const body = await response.json().catch(() => ({})) as { error?: string }
    throw new ApiError(body.error || `无法读取日志（${response.status}）`, response.status, body)
  }
  return response.text()
}

export function uploadPluginImage(file: File, onProgress?: (percent: number) => void, signal?: AbortSignal) {
  return new Promise<{ taskId: string }>((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    xhr.open('POST', '/api/plugins/images/upload')
    xhr.setRequestHeader('Accept', 'application/json')
    xhr.setRequestHeader('X-Lan-Copy-Action', 'plugins-image')
    xhr.upload.onprogress = event => {
      if (event.lengthComputable && onProgress) onProgress(Math.round(event.loaded / event.total * 100))
    }
    xhr.onload = () => {
      let body: { taskId?: string; error?: string } = {}
      try { body = JSON.parse(xhr.responseText) as typeof body } catch {}
      if (xhr.status >= 200 && xhr.status < 300 && body.taskId) resolve({ taskId: body.taskId })
      else reject(new Error(body.error || `上传失败（${xhr.status}）`))
    }
    xhr.onerror = () => reject(new Error('镜像上传失败，请检查连接'))
    xhr.onabort = () => reject(new DOMException('上传已取消', 'AbortError'))
    signal?.addEventListener('abort', () => xhr.abort(), { once: true })
    const form = new FormData()
    form.append('image', file, file.name)
    xhr.send(form)
  })
}

export async function waitForTask(taskId: string, options: {
  signal?: AbortSignal
  onUpdate?: (task: PluginTask) => void
  interval?: number
} = {}): Promise<PluginTask> {
  const interval = options.interval ?? 800
  for (;;) {
    if (options.signal?.aborted) throw new DOMException('操作已取消', 'AbortError')
    const task = await getPluginTask(taskId, options.signal)
    options.onUpdate?.(task)
    if (task.status === 'succeeded') return task
    if (task.status === 'failed') throw new Error(task.error || '操作失败')
    await new Promise(resolve => setTimeout(resolve, interval))
  }
}

export function statusLabel(status: PluginStatus): string {
  return {
    not_installed: '未安装', installing: '安装中', stopped: '已停止', starting: '启动中',
    running: '运行中', unhealthy: '不健康', stopping: '停止中', failed: '失败', uninstalling: '卸载中',
  }[status] || status
}

export function healthLabel(status: HealthStatus): string {
  return { unknown: '未检查', checking: '检查中', healthy: '健康', unhealthy: '异常' }[status] || status
}

export interface PluginDialog {
  pluginId: string
  pluginName: string
  title: string
  url: string
}

// parsePluginDialogs keeps only same-origin plugin proxy paths. The server
// already validates them, but the browser is the final place that must refuse
// an unexpected external URL before putting it into an iframe.
export function parsePluginDialogs(value: unknown): PluginDialog[] {
  if (!Array.isArray(value)) return []
  const dialogs: PluginDialog[] = []
  for (const item of value) {
    if (!item || typeof item !== 'object') continue
    const entry = item as Record<string, unknown>
    const pluginId = typeof entry.pluginId === 'string' ? entry.pluginId : ''
    const pluginName = typeof entry.pluginName === 'string' ? entry.pluginName : ''
    const title = typeof entry.title === 'string' ? entry.title : ''
    const url = typeof entry.url === 'string' ? entry.url : ''
    if (!pluginId || !url.startsWith('/plugin-app/') || url.startsWith('//') || url.includes('..')) continue
    dialogs.push({ pluginId, pluginName, title: title || pluginName || '插件处理', url })
  }
  return dialogs
}
