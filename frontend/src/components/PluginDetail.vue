<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import {
  getPlugin, getPluginConfig, getPluginLogs, getPluginStatus, healthLabel,
  statusLabel, updatePluginConfig,
  type EnvVar, type PluginConfig, type PluginDetail, type PluginStatusView,
} from '../api/plugins'
import { notify } from '../ui/fileUi'

const props = defineProps<{ pluginId: string }>()
const emit = defineEmits<{ close: []; changed: [] }>()

interface EnvRow { name: string; value: string; secret: boolean }

const dialogEl = ref<HTMLDialogElement | null>(null)
const detail = ref<PluginDetail | null>(null)
const status = ref<PluginStatusView | null>(null)
const tab = ref<'overview' | 'config' | 'logs' | 'manifest'>('overview')
const loading = ref(true)
const error = ref('')
const logs = ref('')
const logsLoading = ref(false)
const logTail = ref(200)
const autoRefreshLogs = ref(false)
const config = ref<PluginConfig | null>(null)
const envRows = ref<EnvRow[]>([])
const configBusy = ref(false)
const configError = ref('')
let statusTimer: ReturnType<typeof setInterval> | undefined
let logTimer: ReturnType<typeof setInterval> | undefined
let statusController: AbortController | null = null
let logController: AbortController | null = null

const running = computed(() => {
  const value = status.value?.status || detail.value?.instance?.status
  return value === 'running' || value === 'unhealthy'
})
const manifestJSON = computed(() => detail.value ? JSON.stringify(detail.value.manifest, null, 2) : '')
const capabilities = computed(() => detail.value?.manifest.capabilities || [])
const healthRecords = computed(() => (status.value?.healthRecords || detail.value?.healthRecords || []).slice(-20).reverse())

function message(value: unknown, fallback = '操作失败') {
  return value instanceof Error ? value.message : fallback
}

function formatDate(value?: string) {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString('zh-CN', { hour12: false })
}

function formatBytes(bytes?: number) {
  if (!bytes || bytes <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB']
  const index = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1)
  return `${(bytes / 1024 ** index).toFixed(1)} ${units[index]}`
}

function fillConfig(value: PluginConfig) {
  value = { ...value, env: value.env || [], mounts: value.mounts || [] }
  config.value = value
  envRows.value = value.env.map(variable => ({
    name: variable.name,
    value: variable.secret ? '' : (variable.value || ''),
    secret: variable.secret === true,
  }))
}

async function load() {
  statusController?.abort()
  const controller = new AbortController()
  statusController = controller
  try {
    const [detailResult, statusResult] = await Promise.all([
      getPlugin(props.pluginId, controller.signal),
      getPluginStatus(props.pluginId, controller.signal),
    ])
    if (controller.signal.aborted) return
    detail.value = detailResult
    status.value = statusResult
    error.value = ''
  } catch (failure) {
    if (!controller.signal.aborted) error.value = message(failure, '无法读取插件详情')
  } finally {
    if (!controller.signal.aborted) loading.value = false
  }
}

async function loadLogs() {
  if (logsLoading.value) return
  logController?.abort()
  const controller = new AbortController()
  logController = controller
  logsLoading.value = true
  try {
    const text = await getPluginLogs(props.pluginId, logTail.value, controller.signal)
    if (!controller.signal.aborted) logs.value = text || '（暂无日志）'
  } catch (failure) {
    if (!controller.signal.aborted) logs.value = message(failure, '无法读取日志')
  } finally {
    if (logController === controller) logController = null
    if (!controller.signal.aborted) logsLoading.value = false
  }
}

function addEnv() {
  envRows.value.push({ name: '', value: '', secret: false })
}

const dataRoot = computed(() => detail.value?.runtime.dataRoots?.[0] || '')

function addMount() {
  if (!config.value || config.value.mounts.length >= 8) return
  config.value.mounts.push({ hostPath: '', containerPath: '/data', readOnly: false })
}

function useDefaultMount(mount: { hostPath: string }) {
  if (!dataRoot.value) return
  const root = dataRoot.value.replace(/[\\/]+$/, '')
  mount.hostPath = `${root}/${props.pluginId}-data`
}

async function saveConfig() {
  if (!config.value || configBusy.value) return
  if (running.value) { configError.value = '请先停止插件再修改配置'; return }
  configBusy.value = true
  configError.value = ''
  const env: EnvVar[] = envRows.value
    .filter(row => row.name.trim())
    .map(row => ({ name: row.name.trim(), value: row.value, secret: row.secret }))
  try {
    const updated = await updatePluginConfig(props.pluginId, { ...config.value, env })
    fillConfig(updated)
    notify('插件配置已保存，下次启动生效')
    emit('changed')
    await load()
  } catch (failure) {
    configError.value = message(failure, '无法保存配置')
  } finally {
    configBusy.value = false
  }
}

function switchTab(next: 'overview' | 'config' | 'logs' | 'manifest') {
  if (next !== 'logs' && logTimer) {
    clearInterval(logTimer)
    logTimer = undefined
    autoRefreshLogs.value = false
  }
  tab.value = next
  if (next === 'logs') void loadLogs()
  if (next === 'config' && !config.value) {
    void getPluginConfig(props.pluginId).then(fillConfig).catch(failure => { configError.value = message(failure) })
  }
}

function toggleLogAutoRefresh() {
  if (logTimer) {
    clearInterval(logTimer)
    logTimer = undefined
  }
  if (autoRefreshLogs.value) logTimer = setInterval(() => { void loadLogs() }, 2000)
}

function close() { dialogEl.value?.close() }
function onNativeClose() { emit('close') }

onMounted(async () => {
  await load()
  await nextTick()
  dialogEl.value?.showModal()
  statusTimer = setInterval(() => { if (!document.hidden && tab.value !== 'logs') void load() }, 4000)
})
onBeforeUnmount(() => {
  if (statusTimer) clearInterval(statusTimer)
  if (logTimer) clearInterval(logTimer)
  statusController?.abort()
  logController?.abort()
})
</script>

<template>
  <dialog ref="dialogEl" class="plugin-dialog plugin-detail-dialog" aria-labelledby="pluginDetailTitle" @close="onNativeClose" @cancel.prevent="close">
    <header class="plugin-dialog-head">
      <div>
        <span class="plugins-eyebrow">{{ detail?.plugin.id || pluginId }}</span>
        <h2 id="pluginDetailTitle">{{ detail?.plugin.name || '插件详情' }}</h2>
        <div v-if="status" class="plugin-detail-status">
          <span class="plugin-badge" :class="`status-${status.status}`">{{ statusLabel(status.status) }}</span>
          <span class="plugin-badge" :class="`health-${status.healthStatus}`">{{ healthLabel(status.healthStatus) }}</span>
          <span v-if="status.address" class="plugin-address">{{ status.address }}</span>
        </div>
      </div>
      <button class="settings-close" type="button" aria-label="关闭" @click="close">×</button>
    </header>

    <nav class="plugin-detail-tabs" aria-label="详情分区">
      <button type="button" :aria-selected="tab === 'overview'" @click="switchTab('overview')">概况</button>
      <button type="button" :aria-selected="tab === 'config'" @click="switchTab('config')">配置</button>
      <button type="button" :aria-selected="tab === 'logs'" @click="switchTab('logs')">日志</button>
      <button type="button" :aria-selected="tab === 'manifest'" @click="switchTab('manifest')">Manifest</button>
    </nav>

    <div class="plugin-dialog-body">
      <p v-if="error" class="plugin-inline-error" role="alert">{{ error }}</p>
      <div v-if="loading" class="plugin-empty compact"><strong>正在读取插件详情…</strong></div>

      <section v-else-if="tab === 'overview' && detail" class="plugin-detail-panel">
        <p v-if="status?.statusReason" class="plugin-reason-box">{{ status.statusReason }}</p>
        <p v-for="warning in status?.warnings || []" :key="warning" class="plugin-warning-box">{{ warning }}</p>
        <dl class="plugin-summary">
          <div><dt>插件 ID</dt><dd><code>{{ detail.plugin.id }}</code></dd></div>
          <div><dt>当前版本</dt><dd>{{ detail.plugin.currentVersion || detail.plugin.status }}</dd></div>
          <div><dt>协议版本</dt><dd>{{ detail.plugin.protocolVersion }}</dd></div>
          <div><dt>容器</dt><dd>{{ detail.instance?.containerName || '未创建' }}</dd></div>
          <div><dt>最近启动</dt><dd>{{ formatDate(status?.startedAt || detail.instance?.startedAt) }}</dd></div>
          <div><dt>最近健康检查</dt><dd>{{ formatDate(status?.lastHealthAt || detail.instance?.lastHealthAt) }}</dd></div>
          <div><dt>重启次数</dt><dd>{{ status?.restartCount ?? detail.instance?.restartCount ?? 0 }}</dd></div>
          <div><dt>镜像</dt><dd>{{ detail.images[0]?.imageName }}:{{ detail.images[0]?.imageTag || 'latest' }}</dd></div>
        </dl>

        <div class="plugin-metric-grid">
          <div><span>CPU</span><strong>{{ status?.stats ? `${status.stats.cpuPercent.toFixed(1)}%` : '—' }}</strong></div>
          <div><span>内存</span><strong>{{ status?.stats ? `${formatBytes(status.stats.memoryBytes)} / ${formatBytes(status.stats.memoryLimitBytes)}` : '—' }}</strong></div>
          <div><span>调用</span><strong>{{ status?.metrics.calls ?? detail.metrics.calls }}</strong></div>
          <div><span>调用错误</span><strong>{{ status?.metrics.errors ?? detail.metrics.errors }}</strong></div>
          <div><span>最近耗时</span><strong>{{ status?.metrics.lastDurationMs ?? detail.metrics.lastDurationMs }} ms</strong></div>
        </div>

        <div class="plugin-form-section">
          <div class="plugin-form-head"><strong>能力</strong></div>
          <div class="plugin-capabilities">
            <span v-for="capability in capabilities" :key="capability" class="plugin-capability">{{ capability }}</span>
            <span v-if="capabilities.length === 0" class="plugin-capability muted">未声明能力</span>
          </div>
        </div>

        <div class="plugin-form-section">
          <div class="plugin-form-head"><strong>健康检查记录</strong><span>{{ healthRecords.length }} 条</span></div>
          <div v-if="healthRecords.length === 0" class="plugin-empty-line">暂无健康检查记录</div>
          <div v-else class="plugin-health-list">
            <div v-for="record in healthRecords" :key="record.at" class="plugin-health-row">
              <span class="plugin-badge" :class="record.status === 'healthy' ? 'health-healthy' : 'health-unhealthy'">{{ record.status === 'healthy' ? '健康' : '异常' }}</span>
              <span>{{ formatDate(record.at) }}</span>
              <span>{{ record.latencyMs }} ms</span>
              <span v-if="record.statusCode">HTTP {{ record.statusCode }}</span>
              <span class="plugin-health-error">{{ record.error || '' }}</span>
            </div>
          </div>
        </div>

        <div v-if="detail.instance?.config.mounts.length" class="plugin-form-section">
          <div class="plugin-form-head"><strong>持久化目录</strong></div>
          <div v-for="mount in detail.instance.config.mounts" :key="mount.containerPath" class="plugin-mount-view">
            <code>{{ mount.hostPath }}</code><span>→</span><code>{{ mount.containerPath }}</code><span v-if="mount.readOnly">只读</span>
          </div>
        </div>
      </section>

      <section v-else-if="tab === 'config'" class="plugin-detail-panel">
        <div v-if="!config" class="plugin-empty compact"><strong>正在读取配置…</strong></div>
        <template v-else>
          <p class="plugin-step-hint">运行中的插件不能修改配置。修改后将在下次启动时生效，证书、Token 等敏感值不会回显。</p>
          <div class="plugin-config-grid">
            <label>CPU 限制<input v-model.number="config.cpuLimit" type="number" min="0.1" max="8" step="0.1"></label>
            <label>内存（MB）<input v-model.number="config.memoryMB" type="number" min="64" max="16384" step="64"></label>
            <label>PID 限制<input v-model.number="config.pidsLimit" type="number" min="16" max="4096" step="16"></label>
            <label>重启策略
              <select v-model="config.restartPolicy">
                <option value="no">不自动重启</option>
                <option value="on-failure">失败时重启</option>
                <option value="on-failure:3">失败时最多重启 3 次</option>
              </select>
            </label>
            <label>请求超时（秒）<input v-model.number="config.requestTimeoutSeconds" type="number" min="1" max="300"></label>
            <label>健康检查路径<input v-model="config.healthCheck.path" type="text"></label>
          </div>
          <div class="plugin-form-section">
            <div class="plugin-form-head"><strong>环境变量</strong><button class="secondary" type="button" :disabled="running" @click="addEnv">添加变量</button></div>
            <div v-if="envRows.length === 0" class="plugin-empty-line">未配置环境变量</div>
            <div v-for="(row, index) in envRows" :key="index" class="plugin-env-row">
              <input v-model="row.name" type="text" placeholder="NAME" aria-label="变量名" :disabled="running">
              <input v-model="row.value" :type="row.secret ? 'password' : 'text'" :placeholder="row.secret ? '留空保留原值' : '值'" aria-label="变量值" :disabled="running">
              <label class="plugin-check"><input v-model="row.secret" type="checkbox" :disabled="running"> 敏感</label>
              <button class="secondary plugin-danger" type="button" :disabled="running" @click="envRows.splice(index, 1)">移除</button>
            </div>
          </div>
          <div class="plugin-form-section">
            <div class="plugin-form-head"><strong>持久化目录</strong><button class="secondary" type="button" :disabled="running" @click="addMount">添加目录</button></div>
            <p class="plugin-step-hint">宿主机路径必须位于允许的根目录内：<code>{{ dataRoot || '应用数据目录/plugins/data' }}</code>。修改后下次启动会重建容器。</p>
            <div v-if="config.mounts.length === 0" class="plugin-empty-line">未配置持久化目录</div>
            <div v-for="(mount, index) in config.mounts" :key="index" class="plugin-mount-row">
              <input v-model="mount.hostPath" type="text" placeholder="宿主机绝对路径" aria-label="宿主机路径" :disabled="running" spellcheck="false">
              <input v-model="mount.containerPath" type="text" placeholder="/data" aria-label="容器路径" :disabled="running" spellcheck="false">
              <label class="plugin-check"><input v-model="mount.readOnly" type="checkbox" :disabled="running"> 只读</label>
              <button class="secondary" type="button" :disabled="running" @click="useDefaultMount(mount)">用默认路径</button>
              <button class="secondary plugin-danger" type="button" :disabled="running" @click="config.mounts.splice(index, 1)">移除</button>
            </div>
          </div>
          <p v-if="configError" class="plugin-inline-error" role="alert">{{ configError }}</p>
          <div class="plugin-dialog-actions">
            <button class="primary" type="button" :disabled="configBusy || running" @click="saveConfig">{{ configBusy ? '正在保存…' : '保存配置' }}</button>
          </div>
        </template>
      </section>

      <section v-else-if="tab === 'logs'" class="plugin-detail-panel">
        <div class="plugin-log-toolbar">
          <label>最近
            <select v-model.number="logTail" @change="loadLogs">
              <option :value="100">100 行</option>
              <option :value="200">200 行</option>
              <option :value="500">500 行</option>
              <option :value="1000">1000 行</option>
            </select>
          </label>
          <label class="plugin-check"><input v-model="autoRefreshLogs" type="checkbox" @change="toggleLogAutoRefresh"> 自动刷新</label>
          <button class="secondary" type="button" :disabled="logsLoading" @click="loadLogs">{{ logsLoading ? '读取中…' : '刷新日志' }}</button>
        </div>
        <pre class="plugin-logs" tabindex="0">{{ logs || '（暂无日志）' }}</pre>
      </section>

      <section v-else-if="tab === 'manifest'" class="plugin-detail-panel">
        <pre class="plugin-manifest">{{ manifestJSON }}</pre>
      </section>
    </div>

    <footer class="plugin-dialog-actions">
      <button class="secondary" type="button" @click="close">关闭</button>
    </footer>
  </dialog>
</template>
