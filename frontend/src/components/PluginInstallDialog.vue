<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import {
  installPlugin, waitForTask,
  type PluginConfig, type PluginImage, type PluginRuntime, type PluginTask,
} from '../api/plugins'
import { notify } from '../ui/fileUi'

const props = defineProps<{ images: PluginImage[]; runtime: PluginRuntime | null }>()
const emit = defineEmits<{ close: []; installed: [] }>()

interface EnvRow { name: string; value: string; secret: boolean }
interface MountRow { hostPath: string; containerPath: string; readOnly: boolean }

const dialogEl = ref<HTMLDialogElement | null>(null)
const step = ref<1 | 2 | 3>(1)
const selectedId = ref('')
const envRows = ref<EnvRow[]>([])
const mountRows = ref<MountRow[]>([])
const cpuLimit = ref(0.5)
const memoryMB = ref(512)
const pidsLimit = ref(128)
const restartPolicy = ref('no')
const requestTimeoutSeconds = ref(30)
const startupTimeoutSeconds = ref(60)
const startNow = ref(true)
const error = ref('')
const busy = ref(false)
const task = ref<PluginTask | null>(null)
let taskController: AbortController | null = null

const selected = computed(() => props.images.find(image => image.id === selectedId.value) || null)
const manifest = computed(() => selected.value?.manifest || null)
const dataRoot = computed(() => props.runtime?.dataRoots?.[0] || '')
const grouped = computed(() => {
  const sorted = [...props.images].sort((left, right) =>
    left.pluginId.localeCompare(right.pluginId) || left.createdAt.localeCompare(right.createdAt))
  return sorted
})

function message(value: unknown, fallback = '安装失败') {
  return value instanceof Error ? value.message : fallback
}

function shortDigest(digest: string) {
  return digest.length > 34 ? `${digest.slice(0, 18)}…${digest.slice(-12)}` : digest
}

function selectImage(image: PluginImage) {
  selectedId.value = image.id
  const defaults = image.manifest.defaults || {}
  cpuLimit.value = defaults.cpuLimit || 0.5
  memoryMB.value = defaults.memoryMB || 512
  pidsLimit.value = defaults.pidsLimit || 128
  requestTimeoutSeconds.value = defaults.requestTimeoutSeconds || 30
  startupTimeoutSeconds.value = defaults.startupTimeoutSeconds || 60
  error.value = ''
}

function next() {
  if (step.value === 1) {
    if (!selectedId.value) { error.value = '请先选择镜像'; return }
    step.value = 2
    return
  }
  if (step.value === 2) {
    step.value = 3
  }
}

function previous() {
  error.value = ''
  if (step.value > 1) step.value = (step.value - 1) as 1 | 2
}

function addEnv() {
  if (envRows.value.length >= 128) return
  envRows.value.push({ name: '', value: '', secret: false })
}

function addMount() {
  if (mountRows.value.length >= 8) return
  mountRows.value.push({ hostPath: '', containerPath: '/data', readOnly: false })
}

function useDefaultMount(row: MountRow) {
  if (!dataRoot.value || !manifest.value) return
  const root = dataRoot.value.replace(/[\\/]+$/, '')
  row.hostPath = `${root}/${manifest.value.id}-data`
}

function buildConfig(): PluginConfig {
  return {
    env: envRows.value
      .filter(row => row.name.trim())
      .map(row => ({ name: row.name.trim(), value: row.value, secret: row.secret })),
    cpuLimit: Number(cpuLimit.value),
    memoryMB: Number(memoryMB.value),
    pidsLimit: Number(pidsLimit.value),
    restartPolicy: restartPolicy.value,
    mounts: mountRows.value
      .filter(row => row.hostPath.trim() && row.containerPath.trim())
      .map(row => ({ hostPath: row.hostPath.trim(), containerPath: row.containerPath.trim(), readOnly: row.readOnly })),
    healthCheck: {
      path: manifest.value?.endpoints.health || '/health',
      intervalSeconds: 15,
      timeoutSeconds: 3,
      retries: 3,
      startupTimeoutSeconds: Number(startupTimeoutSeconds.value),
    },
    requestTimeoutSeconds: Number(requestTimeoutSeconds.value),
    logMaxSizeMB: 10,
    logMaxFiles: 2,
  }
}

async function install() {
  if (!selected.value || !manifest.value || busy.value) return
  error.value = ''
  busy.value = true
  taskController = new AbortController()
  try {
    const { taskId } = await installPlugin({
      imageId: selected.value.id,
      expectedPluginId: manifest.value.id,
      config: buildConfig(),
      start: startNow.value,
    })
    await waitForTask(taskId, {
      signal: taskController.signal,
      onUpdate: update => { task.value = update },
    })
    notify(startNow.value ? '插件已安装并启动' : '插件已安装')
    emit('installed')
    dialogEl.value?.close()
  } catch (failure) {
    if (!taskController.signal.aborted) {
      error.value = message(failure)
    }
  } finally {
    busy.value = false
    taskController = null
  }
}

function close() {
  if (busy.value) return
  dialogEl.value?.close()
}

function onNativeClose() {
  emit('close')
}

onMounted(async () => {
  if (grouped.value.length > 0) selectImage(grouped.value[0])
  await nextTick()
  dialogEl.value?.showModal()
})
onBeforeUnmount(() => { taskController?.abort() })
</script>

<template>
  <dialog ref="dialogEl" class="plugin-dialog plugin-install-dialog" aria-labelledby="pluginInstallTitle" @close="onNativeClose" @cancel.prevent="close">
    <header class="plugin-dialog-head">
      <div>
        <span class="plugins-eyebrow">安装插件</span>
        <h2 id="pluginInstallTitle">{{ manifest?.name || '选择插件镜像' }}</h2>
      </div>
      <button class="settings-close" type="button" aria-label="关闭" :disabled="busy" @click="close">×</button>
    </header>

    <ol class="plugin-steps" aria-label="安装步骤">
      <li :class="{ active: step === 1, done: step > 1 }"><span>1</span>选择镜像</li>
      <li :class="{ active: step === 2, done: step > 2 }"><span>2</span>确认协议</li>
      <li :class="{ active: step === 3 }"><span>3</span>运行配置</li>
    </ol>

    <div class="plugin-dialog-body">
      <section v-if="step === 1" class="plugin-step-panel" aria-label="选择镜像">
        <p class="plugin-step-hint">只显示已通过插件 Manifest 校验的离线镜像。上传镜像不会自动安装。</p>
        <div v-if="grouped.length === 0" class="plugin-empty compact">
          <strong>没有可安装的镜像</strong>
          <p>请先在“镜像”中上传符合插件协议的 Docker 镜像包。</p>
        </div>
        <label v-for="image in grouped" :key="image.id" class="plugin-image-option" :class="{ selected: selectedId === image.id }">
          <input type="radio" name="plugin-image" :value="image.id" :checked="selectedId === image.id" @change="selectImage(image)">
          <span class="plugin-image-option-main">
            <strong>{{ image.manifest.name }}</strong>
            <small><code>{{ image.pluginId }}</code> · {{ image.manifest.version }} · {{ image.os }}/{{ image.architecture }}</small>
            <small>{{ image.imageName }}:{{ image.imageTag || 'latest' }} · {{ shortDigest(image.digest) }}</small>
          </span>
        </label>
      </section>

      <section v-else-if="step === 2 && manifest" class="plugin-step-panel" aria-label="确认插件协议">
        <dl class="plugin-summary">
          <div><dt>插件 ID</dt><dd><code>{{ manifest.id }}</code></dd></div>
          <div><dt>名称</dt><dd>{{ manifest.name }}</dd></div>
          <div><dt>版本</dt><dd>{{ manifest.version }} · 协议 {{ manifest.protocolVersion }}</dd></div>
          <div><dt>容器端口</dt><dd>{{ manifest.containerPort }}</dd></div>
          <div><dt>健康检查</dt><dd>{{ manifest.endpoints.health }}</dd></div>
          <div><dt>Manifest</dt><dd>{{ manifest.endpoints.manifest }}</dd></div>
        </dl>
        <p v-if="manifest.description" class="plugin-step-hint">{{ manifest.description }}</p>
        <div class="plugin-capabilities">
          <span v-for="capability in manifest.capabilities" :key="capability" class="plugin-capability">{{ capability }}</span>
          <span v-if="manifest.capabilities.length === 0" class="plugin-capability muted">未声明能力</span>
        </div>
        <div class="plugin-security-note">
          <strong>安全边界</strong>
          <p>容器将以非 root、只读根文件系统、cap-drop ALL、no-new-privileges 运行，不挂载 Docker Socket，不使用 host 网络。资源限制和挂载在下一步配置。</p>
        </div>
      </section>

      <section v-else-if="step === 3 && manifest" class="plugin-step-panel" aria-label="运行配置">
        <div class="plugin-config-grid">
          <label>CPU 限制<input v-model.number="cpuLimit" type="number" min="0.1" max="8" step="0.1" inputmode="decimal"></label>
          <label>内存（MB）<input v-model.number="memoryMB" type="number" min="64" max="16384" step="64" inputmode="numeric"></label>
          <label>PID 限制<input v-model.number="pidsLimit" type="number" min="16" max="4096" step="16" inputmode="numeric"></label>
          <label>重启策略
            <select v-model="restartPolicy">
              <option value="no">不自动重启</option>
              <option value="on-failure">失败时重启</option>
              <option value="on-failure:3">失败时最多重启 3 次</option>
            </select>
          </label>
          <label>请求超时（秒）<input v-model.number="requestTimeoutSeconds" type="number" min="1" max="300" inputmode="numeric"></label>
          <label>启动超时（秒）<input v-model.number="startupTimeoutSeconds" type="number" min="10" max="300" inputmode="numeric"></label>
        </div>

        <div class="plugin-form-section">
          <div class="plugin-form-head"><strong>环境变量</strong><button class="secondary" type="button" @click="addEnv">添加变量</button></div>
          <p class="plugin-step-hint">敏感值会加密保存在应用私有目录，接口不会回显明文。</p>
          <div v-if="envRows.length === 0" class="plugin-empty-line">未配置环境变量</div>
          <div v-for="(row, index) in envRows" :key="index" class="plugin-env-row">
            <input v-model="row.name" type="text" placeholder="NAME" aria-label="变量名" autocomplete="off" spellcheck="false">
            <input v-model="row.value" :type="row.secret ? 'password' : 'text'" placeholder="值" aria-label="变量值" autocomplete="off" spellcheck="false">
            <label class="plugin-check"><input v-model="row.secret" type="checkbox"> 敏感</label>
            <button class="secondary plugin-danger" type="button" @click="envRows.splice(index, 1)">移除</button>
          </div>
        </div>

        <div class="plugin-form-section">
          <div class="plugin-form-head"><strong>持久化目录</strong><button class="secondary" type="button" @click="addMount">添加目录</button></div>
          <p class="plugin-step-hint">宿主机路径必须位于允许的插件数据根目录内：<code>{{ dataRoot || '应用数据目录/plugins/data' }}</code>。默认不挂载任何宿主机目录。</p>
          <div v-if="mountRows.length === 0" class="plugin-empty-line">未配置持久化目录</div>
          <div v-for="(row, index) in mountRows" :key="index" class="plugin-mount-row">
            <input v-model="row.hostPath" type="text" placeholder="宿主机绝对路径" aria-label="宿主机路径" spellcheck="false">
            <input v-model="row.containerPath" type="text" placeholder="/data" aria-label="容器路径" spellcheck="false">
            <label class="plugin-check"><input v-model="row.readOnly" type="checkbox"> 只读</label>
            <button class="secondary" type="button" @click="useDefaultMount(row)">用默认路径</button>
            <button class="secondary plugin-danger" type="button" @click="mountRows.splice(index, 1)">移除</button>
          </div>
        </div>

        <label class="plugin-check plugin-start-now"><input v-model="startNow" type="checkbox"> 安装完成后立即启动并等待健康检查</label>
        <p class="plugin-step-hint">如果是首次安装，启动可能需要几十秒。失败原因会保留在插件详情和日志中。</p>
      </section>

      <div v-if="task" class="plugin-task">
        <div class="plugin-task-head">
          <strong>{{ task.message || '正在安装' }}</strong>
          <span>{{ task.progress >= 0 ? `${task.progress}%` : '处理中' }}</span>
        </div>
        <div class="plugin-task-track"><span :style="{ width: `${task.progress >= 0 ? task.progress : 15}%` }"></span></div>
      </div>
      <p v-if="error" class="plugin-inline-error" role="alert">{{ error }}</p>
    </div>

    <footer class="plugin-dialog-actions">
      <button class="secondary" type="button" :disabled="busy" @click="step === 1 ? close() : previous()">{{ step === 1 ? '取消' : '上一步' }}</button>
      <button v-if="step < 3" class="primary" type="button" :disabled="!selectedId" @click="next">下一步</button>
      <button v-else class="primary" type="button" :disabled="busy || !selectedId" @click="install">{{ busy ? '正在安装…' : '确认安装' }}</button>
    </footer>
  </dialog>
</template>
