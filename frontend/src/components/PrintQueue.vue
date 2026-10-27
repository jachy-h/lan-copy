<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { requestJSON } from '../api/client'
import DocumentPrint from './DocumentPrint.vue'
import type { PrintJob } from '../printing/document'

defineProps<{ sharedPath?: string }>()
const emit = defineEmits<{ localFile: [] }>()

interface Printer { name: string; default: boolean }
type Job = PrintJob
interface PrinterResult { available: boolean; reason: string; printers: Printer[] }
const printers = ref<Printer[]>([])
const selected = ref('')
const printerRevision = ref(0)
const documentBusy = ref(false)
const available = ref(false)
const reason = ref('')
const loading = ref(true)
const sending = ref(false)
const error = ref('')
const jobsError = ref('')
const notice = ref('')
const jobs = ref<Job[]>([])
const jobsLoading = ref(true)
const offline = ref(false)
const pending = computed(() => jobs.value.some(job => job.status === 'preparing' || job.status === 'queued' || job.status === 'processing'))
const canPrint = computed(() => available.value && printers.value.some(p => p.name === selected.value) && !loading.value && !jobsLoading.value && !jobsError.value && !sending.value && !pending.value && !offline.value)
const statusText = { preparing: '正在接收', queued: '等待提交', processing: '正在提交', submitted: '已提交', failed: '提交失败' }
const controller = new AbortController()
let timer: ReturnType<typeof setTimeout> | undefined
let disposed = false
let readingJobs = false
let jobsRevision = 0

async function loadPrinters() {
  if (offline.value || disposed) return
  loading.value = true
  error.value = ''
  try {
    const result = await requestJSON<PrinterResult>('/api/printing', { cache: 'no-store', signal: controller.signal })
    printers.value = result.printers
    printerRevision.value++
    available.value = result.available
    reason.value = result.reason
    if (!result.printers.some(p => p.name === selected.value)) selected.value = result.printers.find(p => p.default)?.name || result.printers[0]?.name || ''
  } catch (cause) {
    if (!controller.signal.aborted) { available.value = false; error.value = cause instanceof Error ? cause.message : '读取打印机失败' }
  } finally { loading.value = false }
}

async function loadJobs() {
  if (disposed || offline.value || readingJobs) return
  readingJobs = true
  const revision = jobsRevision
  clearTimeout(timer)
  try {
    const result = await requestJSON<{ jobs: Job[] }>('/api/printing/jobs', { cache: 'no-store', signal: controller.signal })
    if (revision !== jobsRevision) return
    jobs.value = result.jobs
    jobsError.value = ''
  } catch {
    if (!controller.signal.aborted) jobsError.value = '任务状态暂时无法更新，将自动重试。'
  } finally {
    readingJobs = false
    jobsLoading.value = false
    if (!disposed && !offline.value) timer = setTimeout(() => void loadJobs(), pending.value ? 1500 : 5000)
  }
}

async function printTest() {
  if (!canPrint.value || documentBusy.value) return
  sending.value = true
  error.value = ''; notice.value = ''
  try {
    const job = await requestJSON<Job>('/api/printing/test', {
      method: 'POST', signal: controller.signal,
      headers: { 'Content-Type': 'application/json', 'X-Lan-Copy-Action': 'print-test' },
      body: JSON.stringify({ printer: selected.value }),
    })
    jobsRevision++
    if (!jobs.value.some(existing => existing.id === job.id)) jobs.value.unshift(job)
    notice.value = '测试任务已创建，将打印 1 张测试页。'
    void loadJobs()
  } catch (cause) {
    if (!controller.signal.aborted) {
      error.value = `${cause instanceof Error ? cause.message : '测试请求失败'}。请先核对任务记录和 Windows 打印队列，再决定是否重试。`
      void loadJobs()
    }
  } finally { sending.value = false }
}
function onDocumentSubmitted(job: Job) {
  jobsRevision++
  if (!jobs.value.some(existing => existing.id === job.id)) jobs.value.unshift(job)
  void loadJobs()
}
function shutdown() { offline.value = true; controller.abort(); clearTimeout(timer) }
function formatTime(value: string) { return new Date(value).toLocaleString('zh-CN', { hour12: false }) }
onMounted(() => { void loadPrinters(); void loadJobs(); window.addEventListener('lan-copy:shutdown', shutdown) })
onBeforeUnmount(() => { disposed = true; controller.abort(); clearTimeout(timer); window.removeEventListener('lan-copy:shutdown', shutdown) })
</script>

<template>
  <section class="print-page" aria-labelledby="print-title">
    <div class="print-page-heading"><div><span class="print-eyebrow">局域网打印</span><h2 id="print-title">打印队列</h2><p>通过运行 Lan Copy 的电脑，连接你的打印机。</p></div><span class="print-version-badge">文件打印</span></div>
    <p v-if="offline" class="print-alert" role="alert">服务已关闭，请重新启动 Lan Copy 后刷新页面。</p>
    <div class="print-layout">
      <section class="print-card print-setup" aria-labelledby="printer-title" :aria-busy="loading">
        <div class="print-card-heading"><h3 id="printer-title">选择打印机</h3><button class="print-text-button" type="button" :disabled="loading || offline || documentBusy" @click="loadPrinters">{{ loading ? '读取中…' : '刷新列表' }}</button></div>
        <p class="print-description">读取服务主机中已安装的打印机。</p>
        <label class="print-label" for="print-printer">打印机</label>
        <select id="print-printer" v-model="selected" :disabled="loading || !available || !printers.length || sending || offline || documentBusy">
          <option v-if="!printers.length" value="">{{ loading ? '正在读取打印机…' : '暂无可用打印机' }}</option>
          <option v-for="printer in printers" :key="printer.name" :value="printer.name">{{ printer.name }}{{ printer.default ? '（系统默认）' : '' }}</option>
        </select>
        <p v-if="!loading && !error && !available" class="print-help">{{ reason }}。请在连接打印机的 Windows 电脑上运行 Lan Copy，再从任意设备访问。</p>
        <p v-else-if="!loading && available && !printers.length" class="print-help">未找到实体打印机。请在主机的 Windows 设置中安装打印机，并确认能够正常打印，再刷新列表。</p>
        <button class="primary print-test-button" type="button" :disabled="!canPrint || documentBusy" @click="printTest">{{ sending ? '正在提交测试页…' : pending ? '打印任务处理中…' : '打印测试页' }}</button>
        <p v-if="error" class="print-alert" role="alert">{{ error }}</p>
        <p v-if="notice" class="print-notice" role="status">{{ notice }}</p>
        <p class="print-footnote">每次点击将发送 1 张测试页，请在打印机处确认出纸。</p>
      </section>
      <DocumentPrint :shared-path="sharedPath" @local-file="emit('localFile')" :printer="available ? selected : ''" :enabled="canPrint && !sending" :offline="offline" :revision="printerRevision" @submitted="onDocumentSubmitted" @busy="documentBusy = $event" />
    </div>
    <div class="print-task-section">
      <section class="print-card print-queue-card" aria-labelledby="print-jobs-title">
        <div class="print-card-heading"><h3 id="print-jobs-title">任务记录</h3><span class="print-count">{{ jobs.length }} 条</span></div>
        <p class="print-description">本次启动后最近 50 条打印任务，重启后清空。</p>
        <p v-if="jobsError" class="print-alert" role="status">{{ jobsError }}</p>
        <div v-if="!jobs.length" class="print-empty"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M7 8V3h10v5M7 17H4V8h16v9h-3"/><path d="M7 14h10v7H7zM17 11h.01"/></svg><strong>{{ jobsLoading ? '正在读取任务…' : jobsError ? '暂时无法读取任务' : '还没有打印任务' }}</strong><p>选择文件并确认预览<br>打印任务将在这里显示</p></div>
        <ol v-else class="print-jobs" aria-label="打印任务">
          <li v-for="job in jobs" :key="job.id" class="print-job"><div class="print-job-top"><strong>{{ job.name || 'Lan Copy 测试页' }}</strong><span class="print-job-status" :class="job.status">{{ statusText[job.status] }}</span></div><p class="print-job-printer">{{ job.printer }}</p><div class="print-job-meta"><time :datetime="job.created">{{ formatTime(job.created) }}</time><span>{{ job.pages || 1 }} 页 · 1 份<span v-if="job.systemId"> · 系统任务 #{{ job.systemId }}</span></span></div><p v-if="job.error" class="print-alert">{{ job.error }}。请检查 Windows 打印队列后再重试。</p><p v-if="job.warning" class="print-alert">{{ job.warning }}</p></li>
        </ol>
        <p class="print-queue-note">“已提交”表示已交给 Windows 打印队列，实际出纸情况请在打印机或主机上确认。</p>
      </section>
    </div>

  </section>
</template>
