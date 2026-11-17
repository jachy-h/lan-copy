<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import {
  deletePluginImage, getPluginRuntime, healthLabel, listPluginImages, listPlugins,
  pluginAction, statusLabel, uninstallPlugin, uploadPluginImage, waitForTask,
  type PluginImage, type PluginRuntime, type PluginTask, type PluginView,
} from '../api/plugins'
import { ApiError } from '../api/client'
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
const tab = ref<'plugins' | 'images'>('plugins')
const activeTask = ref<PluginTask | null>(null)
const taskError = ref('')
const installOpen = ref(false)
const detailId = ref('')
const uploading = ref(false)
const uploadPercent = ref(0)
const uploadError = ref('')
const fileInput = ref<HTMLInputElement | null>(null)
let refreshController: AbortController | null = null
let taskController: AbortController | null = null
let refreshTimer: ReturnType<typeof setInterval> | undefined
let refreshAgain = false

const MAX_IMAGE_BYTES = 2 * 1024 * 1024 * 1024
const taskActive = computed(() => activeTask.value !== null)
const canManage = computed(() => runtime.value?.available === true && !taskActive.value)

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
      if (error instanceof ApiError && error.status === 403) {
        runtime.value = {
          type: 'docker', available: false, networkMode: 'loopback', network: '',
          error: '插件管理仅限运行 Lan Copy 的本机使用',
        }
        plugins.value = []
        images.value = []
        loadError.value = ''
      } else {
        loadError.value = message(error, '无法读取插件状态')
      }
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
    message: `将删除镜像记录并从 Docker 移除 ${image.imageName}:${image.imageTag || 'latest'}。正在使用的镜像不能删除。`,
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

function installed() {
  installOpen.value = false
  tab.value = 'plugins'
  void refresh()
}

onMounted(() => {
  void refresh()
  refreshTimer = setInterval(() => { if (!document.hidden) void refresh() }, 5000)
})
onBeforeUnmount(() => {
  if (refreshTimer) clearInterval(refreshTimer)
  refreshController?.abort()
  taskController?.abort()
})
</script>

<template>
  <section class="plugins-page">
    <div class="plugins-heading">
      <div>
        <span class="plugins-eyebrow">本地服务 · Docker</span>
        <h2>插件服务</h2>
        <p>安装、运行并调用独立的本地插件。插件以受限容器运行，仅本机可管理。</p>
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
        <strong v-if="runtime?.available">Docker {{ runtime.version || '已连接' }}</strong>
        <strong v-else-if="runtime">Docker 不可用</strong>
        <strong v-else>正在检测 Docker…</strong>
        <span v-if="runtime?.available">{{ runtime.os }}/{{ runtime.architecture }} · 网络 {{ runtime.networkMode === 'container' ? '容器 DNS' : '本机回环' }} · {{ runtime.network }}</span>
        <span v-else-if="runtime?.error">{{ runtime.error }}</span>
        <span v-else>插件管理使用 Docker Engine API，不执行 docker 命令行。</span>
      </div>
      <a v-if="runtime?.available && tab === 'images'" class="plugin-runtime-link" href="#plugin-images">上传镜像</a>
    </div>

    <div v-if="activeTask" class="plugin-task" aria-live="polite">
      <div class="plugin-task-head">
        <strong>{{ activeTask.message || '正在执行' }}</strong>
        <span>{{ activeTask.progress >= 0 ? `${activeTask.progress}%` : '处理中' }}</span>
      </div>
      <div class="plugin-task-track"><span :style="{ width: `${activeTask.progress >= 0 ? activeTask.progress : 15}%` }"></span></div>
    </div>
    <p v-if="taskError" class="plugin-inline-error" role="alert">{{ taskError }}</p>
    <p v-if="loadError" class="plugin-inline-error" role="alert">{{ loadError }}</p>

    <div class="plugin-tabs" role="tablist" aria-label="插件与镜像">
      <button type="button" role="tab" :aria-selected="tab === 'plugins'" @click="tab = 'plugins'">插件<span>{{ plugins.length }}</span></button>
      <button type="button" role="tab" :aria-selected="tab === 'images'" @click="tab = 'images'">镜像<span>{{ images.length }}</span></button>
    </div>

    <section v-if="tab === 'plugins'" class="plugin-panel" aria-label="插件列表">
      <div v-if="loading" class="plugin-empty"><strong>正在读取插件…</strong><p>请稍候</p></div>
      <div v-else-if="plugins.length === 0" class="plugin-empty">
        <div class="plugin-empty-icon" aria-hidden="true">◇</div>
        <strong>还没有安装插件</strong>
        <p>先在“镜像”中上传 Docker 镜像包，然后选择镜像安装插件。</p>
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
            <button class="secondary" type="button" :disabled="!canManage || isBusy(view) || !view.instance" @click="lifecycle(view, 'start')">启动</button>
            <button class="secondary" type="button" :disabled="!canManage || isBusy(view) || !view.instance" @click="lifecycle(view, 'stop')">停止</button>
            <button class="secondary" type="button" :disabled="!canManage || isBusy(view) || !view.instance" @click="lifecycle(view, 'restart')">重启</button>
            <button class="secondary" type="button" :disabled="!view.instance" @click="detailId = view.plugin.id">详情</button>
            <button class="secondary plugin-danger" type="button" :disabled="!canManage || isBusy(view) || !view.instance" @click="uninstall(view)">卸载</button>
          </div>
        </div>
      </div>
    </section>

    <section v-else id="plugin-images" class="plugin-panel" aria-label="镜像管理">
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
  </section>

  <PluginInstallDialog v-if="installOpen" :images="images" :runtime="runtime" @close="installOpen = false" @installed="installed" />
  <PluginDetail v-if="detailId" :plugin-id="detailId" @close="detailId = ''" @changed="refresh" />
</template>
