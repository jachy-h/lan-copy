<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { requestJSON } from '../api/client'
import { loadPrintSource, loadSharedPrintFile, prepareDocument, type PrintJob, type PrintLayout, type PrintSource } from '../printing/document'

const props = defineProps<{ printer: string; enabled: boolean; offline: boolean; revision: number; sharedPath?: string }>()
const emit = defineEmits<{ submitted: [job: PrintJob]; busy: [value: boolean]; localFile: [] }>()
const file = shallowRef<File>()
const loadedSharedPath = ref('')
const source = shallowRef<PrintSource>()
const layout = shallowRef<PrintLayout>()
const canvas = ref<HTMLCanvasElement>()
const page = ref(1)
const zoom = ref(1)
const loadingFile = ref(false)
const loadingLayout = ref(false)
const rendering = ref(false)
const previewReady = ref(false)
const working = ref(false)
const phase = ref<'preparing' | 'uploading'>('preparing')
const preparedPage = ref(0)
const error = ref('')
const layoutError = ref('')
const message = ref('')
let disposed = false
let fileControl = new AbortController()
let sharedControl = new AbortController()
let layoutControl = new AbortController()
let renderControl = new AbortController()
let submitControl: AbortController | undefined
let renderSequence = 0
let renderChain = Promise.resolve()
const paperStyle = computed(() => layout.value ? { aspectRatio: `${layout.value.width} / ${layout.value.height}`, ...(zoom.value > 1 ? { width: `${260 * zoom.value}px`, maxWidth: 'none' } : {}) } : {})
const areaStyle = computed(() => {
  const l = layout.value
  return l ? { left: `${l.left / l.width * 100}%`, top: `${l.top / l.height * 100}%`, width: `${l.printableWidth / l.width * 100}%`, height: `${l.printableHeight / l.height * 100}%` } : {}
})
const canSubmit = computed(() => props.enabled && previewReady.value && !!source.value && !!layout.value && !working.value && !loadingFile.value && !loadingLayout.value && !rendering.value && !props.offline)
const mm = (value: number) => Math.round(value * 10) / 10

async function refreshLayout() {
  layoutControl.abort(); layoutControl = new AbortController()
  const control = layoutControl
  layout.value = undefined; layoutError.value = ''; previewReady.value = false; loadingLayout.value = false
  if (!props.printer || props.offline) return
  loadingLayout.value = true
  try {
    const result = await requestJSON<PrintLayout>(`/api/printing/layout?printer=${encodeURIComponent(props.printer)}`, { cache: 'no-store', signal: control.signal })
    if (!control.signal.aborted && !disposed) layout.value = result
  } catch (cause) {
    if (!control.signal.aborted) layoutError.value = cause instanceof Error ? cause.message : '无法读取打印版面'
  } finally { if (control === layoutControl) loadingLayout.value = false }
}

function resetFile() {
  fileControl.abort(); fileControl = new AbortController()
  source.value?.close(); source.value = undefined; file.value = undefined
  page.value = 1; zoom.value = 1; error.value = ''; message.value = ''; previewReady.value = false; loadingFile.value = false
}
async function openFile(next: File) {
  resetFile()
  const control = fileControl
  file.value = next; loadingFile.value = true
  try {
    const result = await loadPrintSource(next, control.signal)
    if (control.signal.aborted || disposed) { result.close(); return }
    source.value = result
  } catch (cause) {
    if (!control.signal.aborted) error.value = cause instanceof Error ? cause.message : '无法读取文件'
  } finally { if (control === fileControl) loadingFile.value = false }
}
async function selectFile(event: Event) {
  const input = event.target as HTMLInputElement
  const next = input.files?.[0]
  input.value = ''
  if (!next) return
  sharedControl.abort(); loadedSharedPath.value = ''
  emit('localFile')
  await openFile(next)
}
async function loadSharedFile(path: string) {
  sharedControl.abort(); sharedControl = new AbortController()
  const control = sharedControl
  if (!path) {
    if (loadedSharedPath.value) resetFile()
    loadedSharedPath.value = ''
    return
  }
  resetFile(); loadedSharedPath.value = path
  if (props.offline) return
  loadingFile.value = true
  try {
    const selectedFile = await loadSharedPrintFile(path, control.signal)
    if (control.signal.aborted || disposed) return
    await openFile(selectedFile)
  } catch (cause) {
    if (!control.signal.aborted) error.value = cause instanceof Error ? cause.message : '无法读取共享文件'
  } finally { if (control === sharedControl && !control.signal.aborted) loadingFile.value = false }
}

function renderPreview() {
  const sequence = ++renderSequence
  renderControl.abort(); renderControl = new AbortController()
  const control = renderControl
  previewReady.value = false; rendering.value = false
  const currentSource = source.value, currentLayout = layout.value, index = page.value
  renderChain = renderChain.catch(() => {}).then(async () => {
    if (!currentSource || !currentLayout || disposed || control.signal.aborted || working.value) return
    rendering.value = true
    try {
      await nextTick()
      if (!canvas.value || control.signal.aborted) return
      await currentSource.render(index, canvas.value, currentLayout, control.signal)
      if (sequence === renderSequence && !control.signal.aborted) previewReady.value = true
    } catch (cause) {
      if (!control.signal.aborted) error.value = cause instanceof Error ? cause.message : '无法生成打印预览'
    } finally { if (sequence === renderSequence) rendering.value = false }
  })
}

async function submit() {
  const document = source.value, paper = layout.value, selectedFile = file.value
  if (!canSubmit.value || !document || !paper || !selectedFile) return
  working.value = true; phase.value = 'preparing'; preparedPage.value = 0
  emit('busy', true); error.value = ''; message.value = ''
  submitControl = new AbortController()
  const control = submitControl
  renderControl.abort()
  let sent = false
  try {
    await renderChain
    const body = await prepareDocument(document, selectedFile.name, props.printer, paper, control.signal, value => { preparedPage.value = value })
    if (control.signal.aborted) return
    phase.value = 'uploading'; sent = true
    const job = await requestJSON<PrintJob>('/api/printing/documents', { method: 'POST', headers: { 'X-Lan-Copy-Action': 'print-document' }, body, signal: control.signal })
    emit('submitted', job)
    message.value = `已创建 ${document.pages} 页打印任务，请在任务记录中查看提交状态。`
  } catch (cause) {
    if (!disposed && !props.offline) {
      if (control.signal.aborted && !sent) message.value = '已取消页面准备，未提交打印。'
      else error.value = `${cause instanceof Error ? cause.message : '打印请求失败'}。${sent ? '请先核对任务记录和 Windows 打印队列，再决定是否重试。' : '尚未提交到打印机。'}`
    }
  } finally {
    working.value = false; emit('busy', false)
    if (!disposed && !props.offline) renderPreview()
  }
}
watch(() => props.sharedPath || '', path => { submitControl?.abort(); void loadSharedFile(path) }, { immediate: true })
watch(() => [props.printer, props.revision], () => { submitControl?.abort(); void refreshLayout() }, { immediate: true })
watch([source, layout, page], renderPreview)
watch(() => props.offline, value => {
  if (value) { sharedControl.abort(); fileControl.abort(); layoutControl.abort(); renderControl.abort(); submitControl?.abort(); previewReady.value = false }
})
onBeforeUnmount(() => {
  disposed = true; sharedControl.abort(); fileControl.abort(); layoutControl.abort(); renderControl.abort(); submitControl?.abort()
  source.value?.close(); emit('busy', false)
  if (canvas.value) canvas.value.width = canvas.value.height = 0
})
</script>

<template>
  <section class="print-card document-print" aria-labelledby="document-print-title">
    <div class="print-card-heading"><h3 id="document-print-title">文件打印与预览</h3><span class="print-count">PDF · JPG · PNG</span></div>
    <p class="print-description">选择文件，确认版面后发送到主机打印。</p>
    <label class="print-file-picker" :class="{disabled: working || offline}"><input type="file" accept=".pdf,.jpg,.jpeg,.png" aria-label="选择打印文件" :disabled="working || offline" @change="selectFile"><span>{{ file ? '更换文件' : '选择文件' }}</span><strong>{{ file?.name || 'PDF、JPG/JPEG 或 PNG' }}</strong></label>
    <p v-if="loadedSharedPath" class="document-shared-origin">共享空间 · {{ loadedSharedPath }}</p>
    <p class="print-description">单个文件最多 50 MiB，PDF 最多 50 页。</p>
    <div class="document-preview-heading"><strong>打印预览</strong><button type="button" class="print-text-button" :disabled="!printer || working || loadingLayout || offline" @click="refreshLayout">{{ loadingLayout ? '读取版面…' : '刷新预览' }}</button></div>
    <div v-if="layout && source" class="document-zoom-controls" aria-label="预览缩放"><button class="print-text-button" type="button" aria-label="缩小打印预览" :disabled="zoom <= 1" @click="zoom = Math.max(1, zoom - .5)">缩小</button><span>{{ zoom === 1 ? '适应纸张' : `${zoom * 100}%` }}</span><button class="print-text-button" type="button" aria-label="放大打印预览" :disabled="zoom >= 3" @click="zoom = Math.min(3, zoom + .5)">放大</button></div>
    <p v-if="layoutError" class="print-alert" role="alert">{{ layoutError }}</p>
    <div class="document-preview-stage" :aria-busy="loadingFile || loadingLayout || rendering">
      <div v-if="layout && source" class="document-preview-paper" :style="paperStyle" aria-label="纸张与可打印区域">
        <div class="document-printable-area" :style="areaStyle"><canvas ref="canvas" :aria-label="`打印预览，第 ${page} 页，共 ${source.pages} 页`" role="img"></canvas></div>
      </div>
      <div v-else class="document-preview-empty"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M6 3h8l4 4v14H6zM14 3v5h4M9 12h6M9 16h6"/></svg><strong>{{ loadingFile ? '正在读取文件…' : loadingLayout ? '正在读取打印版面…' : file && !source ? '文件暂时无法预览' : !printer ? '先选择打印机' : '选择文件，查看打印效果' }}</strong><p>按设备纸张显示版面<br>等比缩放，不裁切内容</p></div>
      <span v-if="rendering" class="document-rendering" role="status">正在生成预览…</span>
    </div>
    <div v-if="source" class="document-page-controls"><button type="button" class="secondary" aria-label="打印预览上一页" :disabled="page <= 1 || rendering || working" @click="page--">上一页</button><span>第 {{ page }} / {{ source.pages }} 页</span><button type="button" class="secondary" aria-label="打印预览下一页" :disabled="page >= source.pages || rendering || working" @click="page++">下一页</button></div>
    <p v-if="layout" class="document-layout-info">纸张 {{ mm(layout.width) }} × {{ mm(layout.height) }} mm · {{ layout.width > layout.height ? '横向' : '纵向' }} · 1 份 · 单面<br>虚线内为可打印区域，边界线不会印出。</p>
    <p v-if="error" class="print-alert" role="alert">{{ error }}</p><p v-if="message" class="print-notice" role="status">{{ message }}</p>
    <button type="button" class="primary document-submit" :disabled="!canSubmit" @click="submit">{{ working ? phase === 'preparing' ? `正在准备 ${preparedPage} / ${source?.pages} 页…` : '正在发送到主机…' : `确认打印${source ? `全部 ${source.pages} 页` : ''}` }}</button>
    <button v-if="working && phase === 'preparing'" class="print-text-button document-cancel" type="button" @click="submitControl?.abort()">取消准备</button>
    <p class="print-footnote">按 200 DPI 准备页面，纸张与方向跟随主机默认设置；修改设置后请刷新预览。颜色和实际出纸效果以设备为准。</p>
  </section>
</template>
