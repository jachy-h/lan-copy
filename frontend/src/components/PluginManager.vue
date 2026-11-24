<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import {
  deletePluginImage, getPluginEventCatalog, getPluginEvents, getPluginRuntime,
  healthLabel, listPluginImages, listPlugins, pluginAction, sendPluginTestEvent,
  startPluginRuntime, statusLabel, uninstallPlugin, uploadPluginImage, waitForTask,
  type PluginEventCatalogEntry, type PluginEventEndpoint,
  type PluginImage, type PluginRuntime, type PluginTask, type PluginView,
} from '../api/plugins'
import { notify, copyText } from '../ui/fileUi'
import { confirm, confirmText } from '../ui/dialog'
import { buildAgentPrompt } from '../pluginPrompt'
import PluginDetail from './PluginDetail.vue'
import PluginInstallDialog from './PluginInstallDialog.vue'

const runtime = ref<PluginRuntime | null>(null)
const plugins = ref<PluginView[]>([])
const images = ref<PluginImage[]>([])
const loading = ref(true)
const loadError = ref('')
type PluginTab = 'installed' | 'images' | 'integration'
const tabPaths: Record<PluginTab, string> = {
  installed: '/plugins/installed',
  images: '/plugins/images',
  integration: '/plugins/integration',
}
function tabFromLocation(): PluginTab {
  const match = location.pathname.match(/^\/plugins\/(installed|images|integration)\/?$/)
  if (match) return match[1] as PluginTab
  return 'installed'
}
const tab = ref<PluginTab>(tabFromLocation())
const activeTask = ref<PluginTask | null>(null)
const taskError = ref('')
const installOpen = ref(false)
const detailId = ref('')
const uploading = ref(false)
const uploadPercent = ref(0)
const uploadError = ref('')
const fileInput = ref<HTMLInputElement | null>(null)
const eventCatalog = ref<PluginEventCatalogEntry[]>([])
const eventEndpointMap = ref<Record<string, PluginEventEndpoint>>({})
const eventCatalogError = ref('')
const eventStatusError = ref('')
const eventNotice = ref('')
const eventsLoading = ref(false)
const testingEvent = ref('')
let refreshController: AbortController | null = null
let taskController: AbortController | null = null
let eventController: AbortController | null = null
let eventNoticeTimer: ReturnType<typeof setTimeout> | undefined
let refreshTimer: ReturnType<typeof setInterval> | undefined
let refreshAgain = false

const MAX_IMAGE_BYTES = 2 * 1024 * 1024 * 1024
const taskActive = computed(() => activeTask.value !== null)
const canManage = computed(() => runtime.value?.available === true && !taskActive.value)
const runtimeName = computed(() => runtime.value?.type === 'podman' ? 'Podman' : 'Docker')
const startingRuntime = ref(false)

function message(error: unknown, fallback = '操作失败') {
  return error instanceof Error ? error.message : fallback
}

function formatDate(value?: string) {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '—'
  return date.toLocaleString('zh-CN', { hour12: false })
}

function formatBytes(bytes: number) {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const index = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1)
  return `${(bytes / 1024 ** index).toFixed(index === 0 ? 0 : 1)} ${units[index]}`
}

function imageVersion(view: PluginView) {
  return view.image ? `${view.image.imageName}:${view.image.imageTag || 'latest'}` : '—'
}

function isBusy(view: PluginView) {
  if (taskActive.value) return true
  if (!view.instance) return false
  return ['installing', 'starting', 'stopping', 'uninstalling'].includes(view.instance.status)
}

function pluginUIUrl(id: string) {
  return `/plugin-app/${encodeURIComponent(id)}/`
}

function hasPluginUI(view: PluginView) {
  return Boolean(view.image?.manifest?.ui)
}

function canOpenPluginUI(view: PluginView) {
  return hasPluginUI(view) && view.instance?.status === 'running' && view.instance?.healthStatus === 'healthy'
}

function pluginUITitle(view: PluginView) {
  return canOpenPluginUI(view) ? '打开插件界面（局域网设备使用同一地址）' : '插件运行且健康后可打开界面'
}

function openPluginUI(event: MouseEvent, view: PluginView) {
  if (!canOpenPluginUI(view)) event.preventDefault()
}

async function refresh() {
  if (refreshController) {
    refreshAgain = true
    return
  }
  const controller = new AbortController()
  refreshController = controller
  try {
    const [runtimeResult, pluginResult, imageResult] = await Promise.all([
      getPluginRuntime(controller.signal),
      listPlugins(controller.signal),
      listPluginImages(controller.signal),
    ])
    if (controller.signal.aborted) return
    runtime.value = runtimeResult
    plugins.value = pluginResult.plugins
    images.value = imageResult.images
    loadError.value = ''
  } catch (error) {
    if (!controller.signal.aborted) {
      loadError.value = message(error, '无法读取插件状态')
    }
  } finally {
    if (refreshController === controller) {
      refreshController = null
      loading.value = false
      if (refreshAgain) {
        refreshAgain = false
        void refresh()
      }
    }
  }
}

async function runTask(taskId: string, success: string) {
  if (taskActive.value) return
  taskController?.abort()
  const controller = new AbortController()
  taskController = controller
  taskError.value = ''
  try {
    await waitForTask(taskId, {
      signal: controller.signal,
      onUpdate: task => { activeTask.value = task },
    })
    notify(success)
  } catch (error) {
    if (controller.signal.aborted) return
    taskError.value = message(error)
    notify(taskError.value, true)
  } finally {
    if (taskController === controller) {
      taskController = null
      activeTask.value = null
      await refresh()
    }
  }
}

async function startRuntime() {
  if (startingRuntime.value || taskActive.value) return
  startingRuntime.value = true
  try {
    const { taskId } = await startPluginRuntime()
    await runTask(taskId, '容器运行时已启动')
  } catch (error) {
    notify(message(error), true)
  } finally {
    startingRuntime.value = false
  }
}

async function lifecycle(view: PluginView, action: 'start' | 'stop' | 'restart') {
  if (!canManage.value) return
  try {
    const { taskId } = await pluginAction(view.plugin.id, action)
    await runTask(taskId, `已提交${action === 'start' ? '启动' : action === 'stop' ? '停止' : '重启'}操作`)
  } catch (error) {
    notify(message(error), true)
  }
}

async function uninstall(view: PluginView) {
  if (!canManage.value) return
  const deleteImages = await confirm({
    title: `卸载 ${view.plugin.name}`,
    message: '将停止并删除插件容器与插件记录。默认保留镜像和持久化数据。',
    confirmText: '继续卸载',
    danger: true,
  })
  if (!deleteImages) return
  const deleteData = await confirm({
    title: '保留持久化数据？',
    message: '默认保留插件数据目录。选择“删除数据”将永久删除该插件挂载的宿主机目录内容，无法恢复。',
    confirmText: '删除数据',
    danger: true,
  })
  if (deleteData) {
    const typed = await confirmText({
      title: '确认删除持久化数据',
      message: '此操作会删除插件配置的宿主机持久化目录。请确认已备份需要保留的数据。',
      requiredText: '删除数据',
      confirmText: '永久删除',
    })
    if (!typed) return
  }
  try {
    const { taskId } = await uninstallPlugin(view.plugin.id, {
      deleteImages: false, deleteData, confirmDataDeletion: deleteData,
    })
    await runTask(taskId, deleteData ? '插件与数据已删除' : '插件已卸载，镜像和数据已保留')
  } catch (error) {
    notify(message(error), true)
  }
}

async function removeImage(image: PluginImage) {
  if (!canManage.value) return
  const accepted = await confirm({
    title: '删除镜像',
    message: `将删除镜像记录并从容器运行时移除 ${image.imageName}:${image.imageTag || 'latest'}。正在使用的镜像不能删除。`,
    confirmText: '删除镜像', danger: true,
  })
  if (!accepted) return
  try {
    await deletePluginImage(image.id)
    notify('镜像已删除')
    await refresh()
  } catch (error) {
    notify(message(error), true)
  }
}

function pickImage() {
  uploadError.value = ''
  fileInput.value?.click()
}

async function onFileSelected(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file || uploading.value) return
  if (!/\.(tar|tar\.gz|tgz)$/i.test(file.name)) {
    uploadError.value = '只支持 .tar、.tar.gz 或 .tgz 镜像包'
    return
  }
  if (file.size > MAX_IMAGE_BYTES) {
    uploadError.value = '镜像包不能超过 2 GiB'
    return
  }
  uploading.value = true
  uploadPercent.value = 0
  uploadError.value = ''
  try {
    const { taskId } = await uploadPluginImage(file, percent => { uploadPercent.value = percent })
    uploading.value = false
    await runTask(taskId, '镜像已导入')
    tab.value = 'images'
  } catch (error) {
    uploading.value = false
    uploadError.value = message(error, '镜像上传失败')
  }
}

function openInstall() {
  installOpen.value = true
}

async function copyAgentPrompt() {
  const copied = await copyText(buildAgentPrompt(location.origin))
  notify(copied ? 'Agent Prompt 已复制，可直接粘贴给编码 Agent' : '复制失败，请检查浏览器权限', !copied)
}

const manifestSnippet = `"endpoints": {
  "health": "/health",
  "manifest": "/manifest",
  "version": "/version",
  "capabilities": "/capabilities",
  "events": "/events"
},
"events": ["file.uploaded", "folder.*", "text.*"]`

const uiSnippet = `"ui": {
  "path": "/"
}`

const envelopeSnippet = `POST /events
Content-Type: application/json
X-Lan-Copy-Event: folder.created
X-Lan-Copy-Event-Id: evt-...
X-Lan-Copy-Timestamp: 1763435045
X-Lan-Copy-Signature: v1=<hmac-sha256>

{
  "schemaVersion": 1,
  "id": "evt-...",
  "type": "folder.created",
  "occurredAt": "2026-11-18T03:04:05Z",
  "data": { "path": "docs/2026", "name": "2026" }
}`

const verifySnippet = `token := os.Getenv("LAN_COPY_EVENT_TOKEN")
body, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
timestamp := r.Header.Get("X-Lan-Copy-Timestamp")
mac := hmac.New(sha256.New, []byte(token))
mac.Write([]byte(timestamp))
mac.Write([]byte("."))
mac.Write(body)
expected := "v1=" + hex.EncodeToString(mac.Sum(nil))
if !hmac.Equal([]byte(expected), []byte(r.Header.Get("X-Lan-Copy-Signature"))) {
    http.Error(w, "invalid signature", http.StatusUnauthorized)
    return
}
// 再按 body 中的 id 做幂等处理，成功后返回 2xx。`

const tableEnvSnippet = `LAN_COPY_API_URL=http://host.docker.internal:8080
LAN_COPY_API_TOKEN=<每实例令牌>`

const tableQuerySnippet = `POST $LAN_COPY_API_URL/api/plugin-api/tables/query
Authorization: Bearer $LAN_COPY_API_TOKEN
Content-Type: application/json

{
  "path": "data/orders.csv",
  "q": "pending",
  "filters": [{ "column": "status", "op": "eq", "value": "pending" }],
  "limit": 100,
  "offset": 0
}`

const tableUpdateSnippet = `POST $LAN_COPY_API_URL/api/plugin-api/tables/lock-and-update
Authorization: Bearer $LAN_COPY_API_TOKEN
Content-Type: application/json

{
  "path": "data/orders.csv",
  "expectedVersion": "sha256:...",
  "keyColumn": "id",
  "operations": [
    { "kind": "update", "key": "A-1", "changes": { "status": "done" } }
  ]
}`

async function loadEventCatalog() {
  try {
    const result = await getPluginEventCatalog()
    eventCatalog.value = result.events
    eventCatalogError.value = ''
  } catch (error) {
    eventCatalogError.value = message(error, '无法读取事件目录')
  }
}

async function loadPluginEvents() {
  if (eventsLoading.value || plugins.value.length === 0) {
    if (plugins.value.length === 0) eventEndpointMap.value = {}
    return
  }
  eventController?.abort()
  const controller = new AbortController()
  eventController = controller
  eventsLoading.value = true
  try {
    const ids = plugins.value.map(view => view.plugin.id)
    const results = await Promise.allSettled(ids.map(id => getPluginEvents(id, controller.signal)))
    if (controller.signal.aborted) return
    const next: Record<string, PluginEventEndpoint> = {}
    let failures = 0
    results.forEach((result, index) => {
      if (result.status === 'fulfilled') next[ids[index]] = result.value
      else failures++
    })
    eventEndpointMap.value = next
    eventStatusError.value = failures === ids.length ? '无法读取事件投递状态，请刷新重试' : ''
  } finally {
    if (eventController === controller) {
      eventController = null
      eventsLoading.value = false
    }
  }
}

function selectTab(next: PluginTab) {
  tab.value = next
  const path = tabPaths[next]
  if (location.pathname !== path) history.pushState(null, '', path)
  if (next !== 'integration') return
  if (eventCatalog.value.length === 0) void loadEventCatalog()
  if (plugins.value.length === 0) void refresh().then(() => { if (tab.value === 'integration') void loadPluginEvents() })
  else void loadPluginEvents()
}

function onPopState() {
  const next = tabFromLocation()
  if (next === tab.value) return
  tab.value = next
  if (next === 'integration') {
    if (eventCatalog.value.length === 0) void loadEventCatalog()
    void loadPluginEvents()
  }
}

function endpointFor(view: PluginView) {
  return eventEndpointMap.value[view.plugin.id]
}

function subscriptionsFor(view: PluginView) {
  const endpoint = endpointFor(view)
  if (endpoint && endpoint.subscriptions.length > 0) return endpoint.subscriptions
  return view.image?.manifest?.events || []
}

function canTestEvent(view: PluginView) {
  return Boolean(runtime.value?.available && view.instance?.status === 'running' &&
    view.instance?.healthStatus === 'healthy' && !testingEvent.value)
}

async function testEvent(view: PluginView) {
  if (testingEvent.value) return
  testingEvent.value = view.plugin.id
  eventNotice.value = ''
  eventStatusError.value = ''
  try {
    const { eventId } = await sendPluginTestEvent(view.plugin.id)
    eventNotice.value = `测试事件已入队（${eventId}），正在等待插件响应`
    if (eventNoticeTimer) clearTimeout(eventNoticeTimer)
    eventNoticeTimer = setTimeout(() => { void loadPluginEvents() }, 700)
  } catch (error) {
    eventStatusError.value = message(error, '发送测试事件失败')
  } finally {
    testingEvent.value = ''
  }
}

function compactData(value: unknown) {
  try { return JSON.stringify(value) } catch { return '' }
}

async function copySnippet(text: string, label: string) {
  const copied = await copyText(text)
  notify(copied ? `${label}已复制` : '复制失败，请检查浏览器权限', !copied)
}

function installed() {
  installOpen.value = false
  tab.value = 'installed'
  void refresh()
}

onMounted(() => {
  if (location.pathname === '/plugins' || location.pathname === '/plugins.html') {
    history.replaceState(null, '', tabPaths.installed + location.search)
  }
  window.addEventListener('popstate', onPopState)
  void refresh().then(() => {
    if (tab.value === 'integration') {
      void loadEventCatalog()
      void loadPluginEvents()
    }
  })
  refreshTimer = setInterval(() => {
    if (document.hidden) return
    void refresh().then(() => {
      if (tab.value === 'integration') void loadPluginEvents()
    })
  }, 5000)
})
onBeforeUnmount(() => {
  window.removeEventListener('popstate', onPopState)
  if (refreshTimer) clearInterval(refreshTimer)
  refreshController?.abort()
  taskController?.abort()
  eventController?.abort()
  if (eventNoticeTimer) clearTimeout(eventNoticeTimer)
})
</script>

<template>
  <section class="plugins-page">
    <div class="plugins-heading">
      <div>
        <span class="plugins-eyebrow">局域网服务 · 容器运行时</span>
        <h2>插件服务</h2>
        <p>安装、运行并调用独立的本地插件。插件以受限容器运行，局域网内可管理。</p>
      </div>
      <div class="plugins-heading-actions">
        <button class="secondary" type="button" title="复制一段可直接交给编码 Agent 的插件改造说明" @click="copyAgentPrompt">复制 Agent Prompt</button>
        <button class="secondary" type="button" :disabled="loading" @click="refresh">刷新</button>
        <button class="primary" type="button" :disabled="!canManage" @click="openInstall">安装插件</button>
      </div>
    </div>

    <div class="plugin-runtime" :class="{ offline: runtime && !runtime.available }" role="status">
      <span class="plugin-runtime-dot" aria-hidden="true"></span>
      <div class="plugin-runtime-text">
        <strong v-if="runtime?.available">{{ runtimeName }} {{ runtime.version || '已连接' }}</strong>
        <strong v-else-if="runtime">容器运行时不可用</strong>
        <strong v-else>正在检测容器运行时…</strong>
        <span v-if="runtime?.available">{{ runtime.os }}/{{ runtime.architecture }} · 网络 {{ runtime.networkMode === 'container' ? '容器 DNS' : '本机回环' }} · {{ runtime.network }}</span>
        <span v-if="runtime?.apiBaseUrl" class="plugin-runtime-api">
          插件 API 地址 <code>{{ runtime.apiBaseUrl }}</code><template v-if="runtime.apiBaseUrlReason"> · {{ runtime.apiBaseUrlReason }}</template>
        </span>
        <span v-else-if="runtime?.apiBaseUrlReason" class="plugin-runtime-api">
          插件 API 地址未注入 · {{ runtime.apiBaseUrlReason }}
        </span>
        <span v-if="runtime && !runtime.available && runtime.error">{{ runtime.error }}</span>
        <span v-if="!runtime">插件管理使用 Docker / Podman 兼容 API，不执行容器命令行。</span>
      </div>
      <button v-if="runtime && !runtime.available && runtime.startAvailable" class="secondary plugin-runtime-start" type="button" :disabled="startingRuntime || taskActive" @click="startRuntime">
        {{ startingRuntime ? '正在启动 Podman…' : '启动 Podman' }}
      </button>
      <a v-if="runtime?.available && tab === 'images'" class="plugin-runtime-link" href="#plugin-images">上传镜像</a>
    </div>

    <p class="plugin-lan-warning" role="note">
      插件管理对能访问本服务的局域网设备开放。写操作仍要求同源与动作头，但这不是身份认证；请只安装可信镜像，并在需要时通过防火墙或反向代理限制访问。
    </p>

    <div v-if="activeTask" class="plugin-task" aria-live="polite">
      <div class="plugin-task-head">
        <strong>{{ activeTask.message || '正在执行' }}</strong>
        <span>{{ activeTask.progress >= 0 ? `${activeTask.progress}%` : '处理中' }}</span>
      </div>
      <div class="plugin-task-track"><span :style="{ width: `${activeTask.progress >= 0 ? activeTask.progress : 15}%` }"></span></div>
    </div>
    <p v-if="taskError" class="plugin-inline-error" role="alert">{{ taskError }}</p>
    <p v-if="loadError" class="plugin-inline-error" role="alert">{{ loadError }}</p>

    <div class="plugin-tabs" role="tablist" aria-label="插件、镜像与对接">
      <button type="button" role="tab" :aria-selected="tab === 'installed'" @click="selectTab('installed')">插件<span>{{ plugins.length }}</span></button>
      <button type="button" role="tab" :aria-selected="tab === 'images'" @click="selectTab('images')">镜像<span>{{ images.length }}</span></button>
      <button type="button" role="tab" :aria-selected="tab === 'integration'" @click="selectTab('integration')">对接<span>{{ eventCatalog.length || '说明' }}</span></button>
    </div>

    <section v-if="tab === 'installed'" class="plugin-panel" aria-label="插件列表">
      <div v-if="loading" class="plugin-empty"><strong>正在读取插件…</strong><p>请稍候</p></div>
      <div v-else-if="plugins.length === 0" class="plugin-empty">
        <div class="plugin-empty-icon" aria-hidden="true">◇</div>
        <strong>还没有安装插件</strong>
        <p>先在“镜像”中上传容器镜像包，然后选择镜像安装插件。</p>
        <button class="secondary" type="button" :disabled="!canManage" @click="tab = 'images'">前往镜像管理</button>
      </div>
      <div v-else class="plugin-table" role="table" aria-label="插件列表">
        <div class="plugin-row plugin-row-head" role="row">
          <span role="columnheader">插件</span>
          <span role="columnheader">版本</span>
          <span role="columnheader">状态</span>
          <span role="columnheader">健康</span>
          <span role="columnheader">最近启动</span>
          <span role="columnheader" class="plugin-actions-head">操作</span>
        </div>
        <div v-for="view in plugins" :key="view.plugin.id" class="plugin-row" role="row">
          <div class="plugin-identity" role="cell">
            <strong>{{ view.plugin.name }}</strong>
            <code>{{ view.plugin.id }}</code>
            <small v-if="view.plugin.description">{{ view.plugin.description }}</small>
            <small v-if="view.instance?.statusReason" class="plugin-reason">{{ view.instance.statusReason }}</small>
          </div>
          <div class="plugin-versions" role="cell">
            <span>插件 {{ view.plugin.currentVersion || '—' }}</span>
            <small>{{ imageVersion(view) }}</small>
          </div>
          <div role="cell">
            <span class="plugin-badge" :class="`status-${view.instance?.status || view.plugin.status}`">
              {{ statusLabel(view.instance?.status || view.plugin.status) }}
            </span>
          </div>
          <div role="cell">
            <span class="plugin-badge" :class="`health-${view.instance?.healthStatus || 'unknown'}`">
              {{ healthLabel(view.instance?.healthStatus || 'unknown') }}
            </span>
          </div>
          <div class="plugin-time" role="cell">{{ formatDate(view.instance?.startedAt) }}</div>
          <div class="plugin-actions" role="cell">
            <a
              v-if="hasPluginUI(view)"
              class="secondary plugin-ui-link"
              :class="{ disabled: !canOpenPluginUI(view) }"
              :href="pluginUIUrl(view.plugin.id)"
              :aria-disabled="!canOpenPluginUI(view)"
              :title="pluginUITitle(view)"
              target="_blank"
              rel="noopener"
              @click="openPluginUI($event, view)"
            >打开界面</a>
            <button class="secondary" type="button" :disabled="!canManage || isBusy(view) || !view.instance" @click="lifecycle(view, 'start')">启动</button>
            <button class="secondary" type="button" :disabled="!canManage || isBusy(view) || !view.instance" @click="lifecycle(view, 'stop')">停止</button>
            <button class="secondary" type="button" :disabled="!canManage || isBusy(view) || !view.instance" @click="lifecycle(view, 'restart')">重启</button>
            <button class="secondary" type="button" :disabled="!view.instance" @click="detailId = view.plugin.id">详情</button>
            <button class="secondary plugin-danger" type="button" :disabled="!canManage || isBusy(view) || !view.instance" @click="uninstall(view)">卸载</button>
          </div>
        </div>
      </div>
    </section>

    <section v-else-if="tab === 'images'" id="plugin-images" class="plugin-panel" aria-label="镜像管理">
      <div class="plugin-upload" :class="{ disabled: !canManage }">
        <div>
          <strong>上传离线镜像</strong>
          <p>支持 <code>docker save</code> 生成的 .tar、.tar.gz 或 .tgz，最大 2 GiB。上传只导入镜像，不会自动安装或运行。</p>
          <p class="plugin-upload-note">镜像必须通过 <code>io.lan-copy.plugin.manifest</code> 标签携带插件协议，并以非 root 用户运行。</p>
        </div>
        <div class="plugin-upload-actions">
          <input ref="fileInput" type="file" accept=".tar,.tar.gz,.tgz,application/x-tar,application/gzip" hidden @change="onFileSelected">
          <button class="secondary" type="button" :disabled="!canManage || uploading" @click="pickImage">
            {{ uploading ? `正在上传 ${uploadPercent}%` : '选择镜像包' }}
          </button>
          <div v-if="uploading" class="plugin-upload-track"><span :style="{ width: `${uploadPercent}%` }"></span></div>
          <p v-if="uploadError" class="plugin-inline-error" role="alert">{{ uploadError }}</p>
        </div>
      </div>

      <div v-if="images.length === 0" class="plugin-empty">
        <div class="plugin-empty-icon" aria-hidden="true">⬡</div>
        <strong>还没有导入镜像</strong>
        <p>上传插件镜像包后，可在此查看镜像版本、Digest 和架构。</p>
      </div>
      <div v-else class="plugin-table plugin-image-table" role="table" aria-label="镜像列表">
        <div class="plugin-row plugin-row-head" role="row">
          <span role="columnheader">镜像</span>
          <span role="columnheader">插件</span>
          <span role="columnheader">Digest</span>
          <span role="columnheader">架构</span>
          <span role="columnheader">大小</span>
          <span role="columnheader" class="plugin-actions-head">操作</span>
        </div>
        <div v-for="image in images" :key="image.id" class="plugin-row" role="row">
          <div class="plugin-identity" role="cell">
            <strong>{{ image.imageName }}:{{ image.imageTag || 'latest' }}</strong>
            <small>{{ formatDate(image.createdAt) }} · {{ image.source === 'upload' ? '离线导入' : image.source }}</small>
          </div>
          <div class="plugin-versions" role="cell">
            <code>{{ image.pluginId }}</code>
            <small>{{ image.manifest?.name }} {{ image.manifest?.version }}</small>
          </div>
          <div class="plugin-digest" role="cell" :title="image.digest">{{ image.digest }}</div>
          <div role="cell">{{ image.os }}/{{ image.architecture }}</div>
          <div role="cell">{{ formatBytes(image.sizeBytes) }}</div>
          <div class="plugin-actions" role="cell">
            <button class="secondary" type="button" :disabled="!canManage" @click="detailId = image.pluginId">查看插件</button>
            <button class="secondary plugin-danger" type="button" :disabled="!canManage" @click="removeImage(image)">删除镜像</button>
          </div>
        </div>
      </div>
    </section>

    <section v-else class="plugin-panel plugin-events-panel" aria-label="插件对接">
      <div class="plugin-events-intro">
        <div>
          <span class="plugins-eyebrow">插件对接</span>
          <h3>生命周期事件、智能表格 API 与 Web 界面</h3>
          <p>插件可以通过签名事件感知文件变化，使用注入的地址和令牌读写 CSV 表格，也可以在 Manifest 中声明 ui，把自带界面开放到局域网。</p>
        </div>
        <button class="secondary" type="button" @click="copyAgentPrompt">复制对接说明</button>
      </div>

      <p v-if="eventCatalogError" class="plugin-inline-error" role="alert">{{ eventCatalogError }}</p>
      <p v-if="eventStatusError" class="plugin-inline-error" role="alert">{{ eventStatusError }}</p>
      <p v-if="eventNotice" class="plugin-event-notice" role="status">{{ eventNotice }}</p>

      <section class="plugin-event-card" aria-labelledby="plugin-web-ui">
        <div class="plugin-event-card-head">
          <div>
            <h3 id="plugin-web-ui">插件 Web 界面</h3>
            <p>在 Manifest 中声明 <code>ui</code> 后，Lan Copy 为插件提供局域网入口，并把请求反向代理到容器端口。</p>
          </div>
          <button class="secondary" type="button" @click="copySnippet(uiSnippet, 'Web 界面声明示例')">复制</button>
        </div>
        <pre class="plugin-code">{{ uiSnippet }}</pre>
        <p class="plugin-step-hint">入口固定为 <code>/plugin-app/&lt;插件ID&gt;/</code>。例如插件 ID 是 <code>invoice-ocr</code>，局域网设备访问 <code>http://&lt;lan-copy&gt;/plugin-app/invoice-ocr/</code>；省略 <code>ui</code> 表示没有界面。</p>
        <ul class="plugin-event-notes">
          <li><code>ui.path</code> 是界面在容器内的挂载路径，默认 <code>/</code>；必须以 <code>/</code> 开头，不能包含 <code>..</code>、查询串或片段。</li>
          <li>插件必须读取 <code>X-Forwarded-Prefix</code>（例如 <code>/plugin-app/invoice-ocr</code>）生成资源地址、<code>&lt;base href&gt;</code>、前端路由 base 和 Cookie <code>Path</code>，不要写死 <code>/assets/...</code>。</li>
          <li>实例运行且健康时才能打开；停止或不健康返回 503。代理支持 SSE、上传下载和 WebSocket。</li>
          <li>页面与 Lan Copy 同源，写接口需要插件自行处理 CSRF 与鉴权；只安装可信镜像。</li>
        </ul>
      </section>

      <section class="plugin-event-card" aria-labelledby="plugin-table-api">
        <div class="plugin-event-card-head">
          <div>
            <h3 id="plugin-table-api">智能表格 API</h3>
            <p>安装时自动注入地址和令牌。令牌只授权表格 API，不能访问插件管理、普通文件、更新或关停接口；停止实例后令牌失效。</p>
          </div>
          <button class="secondary" type="button" @click="copySnippet(tableQuerySnippet + '\n\n' + tableUpdateSnippet, '智能表格对接示例')">复制</button>
        </div>
        <pre class="plugin-code">{{ tableEnvSnippet }}</pre>
        <div class="plugin-event-grid">
          <div class="plugin-event-subcard">
            <h4>查询</h4>
            <pre class="plugin-code">{{ tableQuerySnippet }}</pre>
          </div>
          <div class="plugin-event-subcard">
            <h4>原子更新</h4>
            <pre class="plugin-code">{{ tableUpdateSnippet }}</pre>
          </div>
        </div>
        <ul class="plugin-event-notes">
          <li>查询使用 <code>POST /api/plugin-api/tables/query</code>，支持全文搜索、列筛选、排序和分页。</li>
          <li><code>lock_and_update</code> 必须提交查询返回的 <code>version</code> 和唯一非空的 <code>keyColumn</code>；版本冲突返回 409，重新查询后再重试。</li>
          <li>CSV 单文件不超过 8 MiB、50,000 行、200 列；写入会原子替换原文件。</li>
        </ul>
      </section>

      <section class="plugin-event-card" aria-labelledby="plugin-event-catalog">
        <div class="plugin-event-card-head">
          <div>
            <h3 id="plugin-event-catalog">事件目录</h3>
            <p>路径均为共享根目录内的相对路径。失败、取消和回滚的操作不会产生事件。</p>
          </div>
          <span>{{ eventCatalog.length }} 个事件</span>
        </div>
        <div class="plugin-event-table" role="table" aria-label="生命周期事件目录">
          <div class="plugin-event-row plugin-event-row-head" role="row">
            <span role="columnheader">事件</span>
            <span role="columnheader">分类</span>
            <span role="columnheader">触发时机</span>
            <span role="columnheader">示例 data</span>
          </div>
          <div v-for="entry in eventCatalog" :key="entry.type" class="plugin-event-row" role="row">
            <code role="cell">{{ entry.type }}</code>
            <span role="cell" class="plugin-event-category">{{ entry.category }}</span>
            <span role="cell">{{ entry.description }}</span>
            <code role="cell" class="plugin-event-data">{{ compactData(entry.data) }}</code>
          </div>
          <div v-if="eventCatalog.length === 0" class="plugin-event-row">
            <span>正在读取事件目录…</span>
          </div>
        </div>
      </section>

      <div class="plugin-event-grid">
        <section class="plugin-event-card" aria-labelledby="plugin-event-declare">
          <div class="plugin-event-card-head">
            <h3 id="plugin-event-declare">1. 声明订阅</h3>
            <button class="secondary" type="button" @click="copySnippet(manifestSnippet, 'Manifest 片段')">复制</button>
          </div>
          <p>在镜像 Manifest 中声明要接收的事件和端点。省略 <code>events</code> 表示不接收任何推送，旧插件无需改动。</p>
          <pre class="plugin-code">{{ manifestSnippet }}</pre>
          <p class="plugin-step-hint">订阅支持精确类型（<code>file.uploaded</code>）、单层通配（<code>file.*</code>、<code>folder.*</code>、<code>text.*</code>）和全局通配 <code>*</code>，最多 32 项。</p>
        </section>

        <section class="plugin-event-card" aria-labelledby="plugin-event-receive">
          <div class="plugin-event-card-head">
            <h3 id="plugin-event-receive">2. 接收事件</h3>
            <button class="secondary" type="button" @click="copySnippet(envelopeSnippet, '事件格式')">复制</button>
          </div>
          <p>实现 <code>POST /events</code>，读取请求体并以 2xx 响应。Lan Copy 会带上事件类型、事件 ID、时间戳和签名头。</p>
          <pre class="plugin-code">{{ envelopeSnippet }}</pre>
          <p class="plugin-step-hint">默认超时 5 秒，最多尝试 3 次；网络错误、超时、<code>429</code> 和 <code>5xx</code> 会重试，其余 <code>4xx</code> 立即丢弃。请按 <code>id</code> 做幂等处理。</p>
        </section>
      </div>

      <section class="plugin-event-card" aria-labelledby="plugin-event-signature">
        <div class="plugin-event-card-head">
          <div>
            <h3 id="plugin-event-signature">3. 校验签名</h3>
            <p>事件密钥由 Lan Copy 在安装时生成，并通过保留环境变量 <code>LAN_COPY_EVENT_TOKEN</code> 注入容器；配置中不能覆盖。</p>
          </div>
          <button class="secondary" type="button" @click="copySnippet(verifySnippet, '签名校验示例')">复制</button>
        </div>
        <pre class="plugin-code">{{ verifySnippet }}</pre>
        <ul class="plugin-event-notes">
          <li>签名是 <code>HMAC-SHA256(timestamp + "." + 原始请求体)</code>，请求头格式为 <code>v1=&lt;hex&gt;</code>。</li>
          <li>建议校验时间戳在 5 分钟内，并使用常量时间比较。</li>
          <li>插件停止、启动中、健康异常或队列满时，事件会被丢弃且不会拖慢文件操作。</li>
        </ul>
      </section>

      <section class="plugin-event-card" aria-labelledby="plugin-event-status">
        <div class="plugin-event-card-head">
          <div>
            <h3 id="plugin-event-status">交付状态</h3>
            <p>记录只保留在内存中，最近 50 条；Lan Copy 重启后清空。连续失败 5 次会暂停投递 60 秒。</p>
          </div>
          <span v-if="eventsLoading">正在刷新…</span>
        </div>

        <div v-if="plugins.length === 0" class="plugin-empty compact">
          <strong>还没有可接收事件的插件</strong>
          <p>先安装并启动插件，再在 Manifest 中声明 <code>events</code>。</p>
        </div>
        <div v-else class="plugin-event-plugin-list">
          <article v-for="view in plugins" :key="view.plugin.id" class="plugin-event-plugin">
            <div class="plugin-event-plugin-head">
              <div class="plugin-identity">
                <strong>{{ view.plugin.name }}</strong>
                <code>{{ view.plugin.id }}</code>
              </div>
              <div class="plugin-event-plugin-actions">
                <span class="plugin-badge" :class="`status-${view.instance?.status || view.plugin.status}`">
                  {{ statusLabel(view.instance?.status || view.plugin.status) }}
                </span>
                <span class="plugin-badge" :class="`health-${view.instance?.healthStatus || 'unknown'}`">
                  {{ healthLabel(view.instance?.healthStatus || 'unknown') }}
                </span>
                <button class="secondary" type="button" :disabled="!canTestEvent(view)" @click="testEvent(view)">
                  {{ testingEvent === view.plugin.id ? '发送中…' : '发送测试事件' }}
                </button>
              </div>
            </div>

            <div class="plugin-event-subscriptions">
              <span>端点</span>
              <code>{{ endpointFor(view)?.endpoint || view.image?.manifest?.endpoints?.events || '/events' }}</code>
              <span>订阅</span>
              <template v-if="subscriptionsFor(view).length > 0">
                <code v-for="subscription in subscriptionsFor(view)" :key="subscription">{{ subscription }}</code>
              </template>
              <em v-else>未声明 events，不会收到推送</em>
            </div>

            <div v-if="endpointFor(view)" class="plugin-event-stats">
              <span><strong>{{ endpointFor(view)!.stats.queued }}</strong> 已入队</span>
              <span><strong>{{ endpointFor(view)!.stats.delivered }}</strong> 已投递</span>
              <span><strong>{{ endpointFor(view)!.stats.failed }}</strong> 失败</span>
              <span><strong>{{ endpointFor(view)!.stats.dropped }}</strong> 丢弃</span>
              <span>最近投递 {{ formatDate(endpointFor(view)!.stats.lastDeliveredAt) }}</span>
            </div>

            <div v-if="endpointFor(view)?.deliveries.length" class="plugin-event-deliveries">
              <div class="plugin-event-delivery-row plugin-event-delivery-head">
                <span>事件</span><span>结果</span><span>尝试</span><span>时间</span><span>说明</span>
              </div>
              <div v-for="delivery in endpointFor(view)!.deliveries.slice(-5).reverse()" :key="delivery.at + delivery.eventId" class="plugin-event-delivery-row">
                <code>{{ delivery.type }}</code>
                <span class="plugin-event-delivery-status" :class="`delivery-${delivery.status}`">{{ delivery.status === 'delivered' ? '成功' : delivery.status === 'failed' ? '失败' : '丢弃' }}</span>
                <span>{{ delivery.attempts || '—' }}</span>
                <span>{{ formatDate(delivery.at) }}</span>
                <span class="plugin-event-delivery-error" :title="delivery.error">{{ delivery.error || `HTTP ${delivery.statusCode || 200}` }}</span>
              </div>
            </div>
            <p v-else class="plugin-step-hint">还没有投递记录。启动插件后点击“发送测试事件”，可验证端点、签名和幂等处理。</p>
          </article>
        </div>
      </section>
    </section>
  </section>

  <PluginInstallDialog v-if="installOpen" :images="images" :runtime="runtime" @close="installOpen = false" @installed="installed" />
  <PluginDetail v-if="detailId" :plugin-id="detailId" @close="detailId = ''" @changed="refresh" />
</template>
