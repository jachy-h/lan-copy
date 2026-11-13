<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { requestJSON } from '../api/client'
import { copyText, notify } from '../ui/fileUi'
import { confirm as confirmDialog } from '../ui/dialog'
import { register as registerContextMenu, registerPage as registerPageContextMenu } from '../ui/contextMenu'
import AppDialog from './AppDialog.vue'
import EmptyState from './EmptyState.vue'
import FragmentSaveDialog from './FragmentSaveDialog.vue'

interface FragmentImage {
  name?: string
  mime?: string
  size?: number
  file?: string
  width?: number
  height?: number
}
interface SharedFragment {
  id: string
  kind?: 'text' | 'image' | 'mixed'
  content?: string
  name?: string
  mime?: string
  size?: number
  file?: string
  images?: FragmentImage[]
  created: string
  expiresAt?: string
}
interface TextResult { texts: SharedFragment[] }
interface PendingImage { id: string; file: File; url: string }
interface PreviewImage { url: string; alt: string }

const text = defineModel<string>({ default: '' })
const texts = ref<SharedFragment[]>([])
const hasLoaded = ref(false)
const loadError = ref('')
const stopped = ref(false)
const publishing = ref(false)
const dragging = ref(false)
const fileInput = ref<HTMLInputElement | null>(null)
const pendingImages = ref<PendingImage[]>([])
const savingItem = ref<SharedFragment | null>(null)
const saveOpen = ref(false)
const previewImage = ref<PreviewImage | null>(null)
const now = ref(Date.now())
const expandedText = ref(new Set<string>())
const overflowingText = ref(new Set<string>())
const imageRatios = ref(new Map<string, number>())
const MAX_TEXT_CHARS = 100000
const MAX_IMAGE_BYTES = 20 * 1024 * 1024
const MAX_IMAGES_PER_FRAGMENT = 6
const isApplePlatform = /Mac|iPhone|iPad|iPod/.test(navigator.platform || '') || /Mac OS X/.test(navigator.userAgent || '')
const saveShortcut = isApplePlatform ? '⌘ + Enter' : 'Ctrl + Enter'
const charCount = computed(() => Array.from(text.value).length)
const countLabel = computed(() => `${charCount.value.toLocaleString('zh-CN')} / ${MAX_TEXT_CHARS.toLocaleString('zh-CN')} 字`)
const summary = computed(() => loadError.value || (!hasLoaded.value ? '正在读取…' : texts.value.length ? `${texts.value.length} 条碎片 · 到期后自动清理` : '等待内容传入'))
const fragmentsByID = computed(() => new Map(texts.value.map(item => [item.id, item])))
let controller: AbortController | null = null
let eventSource: EventSource | null = null
let fallbackTimer: ReturnType<typeof setInterval> | undefined
let refreshTimer: ReturnType<typeof setTimeout> | undefined
let clockTimer: ReturnType<typeof setInterval> | undefined
let resizeTimer: ReturnType<typeof setTimeout> | undefined
let refreshVersion = 0
let submitting = false
let dragDepth = 0
let pendingSequence = 0

function isImageFile(file: File) { return file.type.startsWith('image/') || /\.(png|jpe?g|gif|webp|bmp)$/i.test(file.name) }
function hasText(item: SharedFragment) { return Boolean(item.content?.trim()) }
function imageList(item: SharedFragment): FragmentImage[] {
  if (item.images?.length) return item.images
  if (item.file) return [{ name: item.name, mime: item.mime, size: item.size, file: item.file }]
  return []
}
function imageURL(item: SharedFragment, index: number) { return `/api/texts/${encodeURIComponent(item.id)}/images/${index}` }
function imageBytes(item: SharedFragment) { return imageList(item).reduce((sum, image) => sum + (Number(image.size) || 0), 0) }

const HTML_ESCAPES: Record<string, string> = { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }
function escapeHTML(value: string) { return value.replace(/[&<>"']/g, char => HTML_ESCAPES[char]) }
// A deliberately small Markdown subset: emphasis, inline code, explicit links
// and bare URLs. Everything is escaped before the small set of tags is added.
function renderText(content: string) {
  let html = escapeHTML((content || '').replace(/\u0000/g, ''))
  const tokens: string[] = []
  const stash = (value: string) => { tokens.push(value); return `\u0000${tokens.length - 1}\u0000` }
  html = html.replace(/`([^`\n]+)`/g, (_match, body: string) => stash(`<code>${body}</code>`))
  html = html.replace(/\[([^\]\n]+)\]\((https?:\/\/[^)\s]+)\)/g, (_match, label: string, href: string) => stash(`<a href="${href}" target="_blank" rel="noopener noreferrer">${label}</a>`))
  html = html.replace(/(?:https?:\/\/|www\.)[^\s\u0000，。；：！？、（）【】《》「」『』]+/g, match => {
    const trailing = match.match(/[.,;:!?，。；：！？、]+$/)?.[0] || ''
    const url = trailing ? match.slice(0, -trailing.length) : match
    if (!url) return match
    const href = url.startsWith('www.') ? `https://${url}` : url
    return stash(`<a href="${href}" target="_blank" rel="noopener noreferrer">${url}</a>`) + trailing
  })
  html = html.replace(/\*\*([^*\n]+)\*\*/g, '<strong>$1</strong>')
  html = html.replace(/(^|[^*])\*([^*\n]+)\*(?!\*)/g, '$1<em>$2</em>')
  html = html.replace(/\u0000(\d+)\u0000/g, (_match, index: string) => tokens[Number(index)] || '')
  return html
}
function formatTime(value: string) {
  const date = new Date(value), today = new Date()
  return date.toDateString() === today.toDateString()
    ? `今天 ${date.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })}`
    : date.toLocaleDateString('zh-CN', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })
}
function formatBytes(bytes = 0) {
  if (bytes < 1024) return `${bytes} B`
  const units = ['KiB', 'MiB', 'GiB']; let value = bytes
  for (const unit of units) { value /= 1024; if (value < 1024 || unit === 'GiB') return `${value.toFixed(value < 10 ? 1 : 0)} ${unit}` }
  return `${bytes} B`
}
function expiryLabel(item: SharedFragment) {
  if (!item.expiresAt) return '长期有效'
  const remaining = Date.parse(item.expiresAt) - now.value
  if (!Number.isFinite(remaining) || remaining <= 0) return '已到期'
  const totalMinutes = Math.ceil(remaining / 60000)
  if (totalMinutes < 1440) {
    const hours = Math.floor(totalMinutes / 60)
    const minutes = totalMinutes % 60
    return `${String(hours).padStart(2, '0')}:${String(minutes).padStart(2, '0')} 后到期`
  }
  return `${Math.ceil(remaining / 86400000)} 天后到期`
}
function expiryClock(value?: string) {
  if (!value) return ''
  const date = new Date(value)
  return Number.isFinite(date.getTime()) ? date.toLocaleString('zh-CN', { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' }) : ''
}
function expiryPercent(item: SharedFragment) {
  if (!item.expiresAt) return 100
  const start = Date.parse(item.created), end = Date.parse(item.expiresAt)
  if (!Number.isFinite(start) || !Number.isFinite(end) || end <= start) return 0
  return Math.min(100, Math.max(0, Math.round((end - now.value) / (end - start) * 100)))
}
function lifetimeClass(item: SharedFragment) {
  const percent = expiryPercent(item)
  return percent <= 10 ? 'fragment-lifetime-urgent' : percent <= 35 ? 'fragment-lifetime-soon' : ''
}
function expiryTitle(value?: string) { return value ? `到期时间：${new Date(value).toLocaleString('zh-CN')}` : '此内容不会自动到期' }
function imageRatio(item: SharedFragment, index: number) {
  const image = imageList(item)[index]
  if (image?.width && image?.height) return image.width / image.height
  return imageRatios.value.get(`${item.id}:${index}`) || 0
}
function rememberImageRatio(item: SharedFragment, index: number, event: Event) {
  const image = event.target as HTMLImageElement
  if (!image.naturalWidth || !image.naturalHeight) return
  imageRatios.value = new Map(imageRatios.value).set(`${item.id}:${index}`, image.naturalWidth / image.naturalHeight)
  checkTextOverflow()
}
function textIsLong(content: string) {
  const trimmed = content.trim()
  if (!trimmed) return false
  if (trimmed.split(/\r?\n/).length > 4) return true
  let weight = 0
  for (const char of trimmed) weight += /[\u3000-\u9fff\uff00-\uffef]/.test(char) ? 2 : 1
  return weight > 200
}
function isWide(item: SharedFragment) {
  const images = imageList(item)
  if (images.length && hasText(item)) return true
  if (images.length > 1) return true
  if (images.length === 1 && imageRatio(item, 0) >= 1.35) return true
  return textIsLong(item.content || '')
}
function toggleText(id: string) {
  const next = new Set(expandedText.value)
  if (next.has(id)) next.delete(id); else next.add(id)
  expandedText.value = next
  void nextTick(checkTextOverflow)
}
function checkTextOverflow() {
  void nextTick(() => {
    const present = new Set<string>()
    const next = new Set(overflowingText.value)
    document.querySelectorAll<HTMLElement>('.fragment-row .text-content[data-text-id]').forEach(element => {
      const id = element.dataset.textId
      if (!id) return
      present.add(id)
      if (expandedText.value.has(id) || element.scrollHeight > element.clientHeight + 1) next.add(id)
      else next.delete(id)
    })
    for (const id of [...next]) if (!present.has(id)) next.delete(id)
    overflowingText.value = next
  })
}
function checkExpirations() {
  now.value = Date.now()
  if (texts.value.some(item => item.expiresAt && Date.parse(item.expiresAt) <= now.value)) scheduleRefresh()
}
function stopFallback() { if (fallbackTimer) clearInterval(fallbackTimer); fallbackTimer = undefined }
function startFallback() { if (!stopped.value && !document.hidden && !fallbackTimer) fallbackTimer = setInterval(scheduleRefresh, 60000) }
async function loadTexts() {
  if (stopped.value || document.hidden) return
  controller?.abort()
  const current = new AbortController(); controller = current; const version = ++refreshVersion
  loadError.value = ''
  try {
    const result = await requestJSON<TextResult>('/api/texts', { cache: 'no-store', signal: current.signal })
    if (version !== refreshVersion || current.signal.aborted) return
    hasLoaded.value = true
    if (eventSource?.readyState === EventSource.OPEN) stopFallback()
    texts.value = result.texts
    now.value = Date.now()
    checkTextOverflow()
    window.dispatchEvent(new Event('lan-copy:data-updated'))
  } catch (error) {
    if (version !== refreshVersion || current.signal.aborted || (error instanceof Error && error.name === 'AbortError')) return
    startFallback(); loadError.value = '无法连接到服务'; window.dispatchEvent(new Event('lan-copy:data-failed')); notify('无法读取内容', true)
  } finally { if (controller === current) controller = null }
}
function scheduleRefresh() {
  if (stopped.value || document.hidden) return
  if (refreshTimer) clearTimeout(refreshTimer)
  refreshTimer = setTimeout(() => { refreshTimer = undefined; void loadTexts() }, 100)
}
function textareaElement() { return document.querySelector<HTMLTextAreaElement>('#textInput') }
function objectURL(file: File) {
  try { return typeof URL.createObjectURL === 'function' ? URL.createObjectURL(file) : '' } catch { return '' }
}
function revokeURL(url: string) { try { if (url) URL.revokeObjectURL(url) } catch { /* ignore */ } }
function attachmentPreviewFailed(image: PendingImage) {
  if (image.url.startsWith('data:')) return
  const reader = new FileReader()
  reader.onload = () => {
    const nextURL = typeof reader.result === 'string' ? reader.result : ''
    if (!nextURL) return
    pendingImages.value = pendingImages.value.map(candidate => candidate.id === image.id ? { ...candidate, url: nextURL } : candidate)
    if (image.url) revokeURL(image.url)
  }
  reader.readAsDataURL(image.file)
}
function addPendingImages(files: File[]) {
  if (publishing.value) return
  const images = files.filter(isImageFile)
  if (!images.length) { notify('请选择图片文件', true); return }
  const remaining = MAX_IMAGES_PER_FRAGMENT - pendingImages.value.length
  if (remaining <= 0) { notify(`每条碎片最多 ${MAX_IMAGES_PER_FRAGMENT} 张图片`, true); return }
  if (images.length > remaining) notify(`每条碎片最多 ${MAX_IMAGES_PER_FRAGMENT} 张图片，已添加前 ${remaining} 张`, true)
  const next = [...pendingImages.value]
  for (const file of images.slice(0, remaining)) {
    if (file.size > MAX_IMAGE_BYTES) { notify(`图片不能超过 20 MiB：${file.name || '粘贴的图片'}`, true); continue }
    next.push({ id: `pending-${++pendingSequence}`, file, url: objectURL(file) })
  }
  pendingImages.value = next
  for (const image of next) if (!image.url) attachmentPreviewFailed(image)
}
function removePendingImage(id: string) {
  const image = pendingImages.value.find(item => item.id === id)
  if (image) revokeURL(image.url)
  pendingImages.value = pendingImages.value.filter(item => item.id !== id)
}
function clearPendingImages() {
  pendingImages.value.forEach(image => revokeURL(image.url))
  pendingImages.value = []
}
function pickImages() { if (!publishing.value) fileInput.value?.click() }
function onImagePicker(event: Event) {
  const input = event.target as HTMLInputElement
  const files = Array.from(input.files || [])
  input.value = ''
  if (files.length) addPendingImages(files)
}
function clipboardImages(data: DataTransfer | null) {
  if (!data) return []
  const fromFiles = Array.from(data.files || []).filter(isImageFile)
  const fromItems: File[] = []
  for (const item of Array.from(data.items || [])) {
    if (item.kind !== 'file' || !item.type.startsWith('image/')) continue
    const file = item.getAsFile()
    if (file && isImageFile(file)) fromItems.push(file)
  }
  // Browsers may expose the same clipboard image through both files and
  // items. Prefer files and only fall back to items when files is empty.
  const candidates = fromFiles.length ? fromFiles : fromItems
  const seen = new Set<string>()
  return candidates.filter(file => {
    const key = `${file.name}:${file.size}:${file.type}`
    if (seen.has(key)) return false
    seen.add(key)
    return true
  })
}
function onDocumentPaste(event: ClipboardEvent) {
  const target = event.target instanceof Element ? event.target : null
  if (target?.closest('dialog[open]') || document.querySelector('dialog[open]')) return
  const files = clipboardImages(event.clipboardData)
  if (!files.length) {
    const hasImage = Array.from(event.clipboardData?.items || []).some(item => item.kind === 'file' && item.type.startsWith('image/'))
    if (hasImage) notify('暂不支持剪贴板中的这种图片格式', true)
    return
  }
  event.preventDefault()
  addPendingImages(files)
}
function hasFiles(event: DragEvent) { return Array.from(event.dataTransfer?.types || []).includes('Files') }
function onDragEnter(event: DragEvent) {
  if (!hasFiles(event)) return
  event.preventDefault(); dragDepth++; dragging.value = true
}
function onDragOver(event: DragEvent) { if (hasFiles(event)) event.preventDefault() }
function onDragLeave(event: DragEvent) {
  if (!hasFiles(event) && !dragging.value) return
  event.preventDefault()
  dragDepth = Math.max(0, dragDepth - 1)
  if (!dragDepth) dragging.value = false
}
function onDrop(event: DragEvent) {
  if (!hasFiles(event)) return
  event.preventDefault(); dragDepth = 0; dragging.value = false
  const files = Array.from(event.dataTransfer?.files || [])
  if (files.length) addPendingImages(files)
}
async function publish() {
  if (submitting) return
  const content = text.value.trim() ? text.value : ''
  const images = pendingImages.value.slice()
  if (!content && !images.length) { notify('请输入文字或添加图片', true); textareaElement()?.focus(); return }
  if (charCount.value > MAX_TEXT_CHARS) { notify(`文本超过 ${MAX_TEXT_CHARS.toLocaleString('zh-CN')} 字限制`, true); return }
  submitting = true; publishing.value = true
  try {
    if (images.length) {
      const form = new FormData()
      form.append('content', content)
      images.forEach((image, index) => form.append('images', image.file, image.file.name || `图片-${index + 1}.png`))
      await requestJSON('/api/texts/fragments', { method: 'POST', body: form })
    } else {
      await requestJSON('/api/texts', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ content }) })
    }
    const hadImages = images.length > 0
    text.value = ''
    clearPendingImages()
    notify(hadImages && content ? '图文碎片已保存' : hadImages ? '图片碎片已保存' : '碎片已保存')
    scheduleRefresh()
  } catch (error) { notify(error instanceof Error ? error.message : '保存失败', true) }
  finally { submitting = false; publishing.value = false }
}
async function copy(content = '') {
  const copied = await copyText(content)
  notify(copied ? '文本已复制' : '复制失败，请手动选择', !copied)
}
function openSave(item: SharedFragment) { savingItem.value = item; saveOpen.value = true }
let previewTrigger: HTMLElement | null = null
function openImagePreview(image: PreviewImage, trigger: HTMLElement) {
  previewTrigger = trigger
  previewImage.value = image
}
function openPendingImagePreview(image: PendingImage, trigger: HTMLElement) {
  openImagePreview({ url: image.url, alt: image.file.name || '待发送图片' }, trigger)
}
function openPublishedImagePreview(item: SharedFragment, index: number, trigger: HTMLElement) {
  const image = imageList(item)[index]
  openImagePreview({ url: imageURL(item, index), alt: image?.name || `共享图片 ${index + 1}` }, trigger)
}
function closeImagePreview() {
  previewImage.value = null
  const trigger = previewTrigger
  previewTrigger = null
  void nextTick(() => trigger?.focus())
}
function onPreviewOpen(value: boolean) { if (!value) closeImagePreview() }
async function deleteText(id: string) {
  if (!fragmentsByID.value.has(id)) { scheduleRefresh(); notify('这条碎片已不存在，列表已刷新', true); return }
  if (!await confirmDialog({ title: '删除碎片？', message: '确定删除这条碎片吗？删除后将无法恢复。', confirmText: '删除', danger: true })) return
  try { await requestJSON(`/api/texts/${encodeURIComponent(id)}`, { method: 'DELETE' }); notify('碎片已删除'); scheduleRefresh() }
  catch { notify('删除失败', true) }
}
function contextMenu(event: MouseEvent, row: Element) {
  const id = row.querySelector<HTMLElement>('[data-text-id]')?.dataset.textId
  const item = id ? fragmentsByID.value.get(id) : undefined
  if (!item) return []
  const actions = []
  if (hasText(item)) actions.push({ label: '复制文本', action: () => copy(item.content || '') })
  actions.push({ label: '存入内网空间', action: () => openSave(item) })
  actions.push({ label: '删除碎片', danger: true, action: () => deleteText(item.id) })
  return actions
}
function pageMenu() {
  return [
    { label: '刷新碎片', action: loadTexts },
    { label: '输入要保存的文本', action: () => textareaElement()?.focus() },
    { label: '添加图片', action: pickImages },
    { label: '复制当前页面地址', action: async () => { const copied = await copyText(location.href); notify(copied ? '页面地址已复制' : '复制失败，请手动选择', !copied) } },
  ]
}
function onVisibility() {
  if (document.hidden) { controller?.abort(); if (refreshTimer) clearTimeout(refreshTimer); refreshTimer = undefined; stopFallback() }
  else if (!stopped.value) { if (!eventSource || eventSource.readyState !== EventSource.OPEN) startFallback(); scheduleRefresh() }
}
function onShutdown() { stopped.value = true; stopFallback(); if (refreshTimer) clearTimeout(refreshTimer); if (clockTimer) clearInterval(clockTimer); controller?.abort(); eventSource?.close() }
function onPageHide() { controller?.abort(); eventSource?.close(); stopFallback() }
function onResize() {
  if (resizeTimer) clearTimeout(resizeTimer)
  resizeTimer = setTimeout(() => { resizeTimer = undefined; checkTextOverflow() }, 120)
}
function onTextKey(event: KeyboardEvent) {
  if (event.key === 'Enter' && (event.ctrlKey || event.metaKey)) { event.preventDefault(); void publish() }
}
function onRowClick(event: MouseEvent) {
  const button = (event.target as Element).closest<HTMLButtonElement>('[data-text-action]')
  if (!button) return
  const id = button.dataset.textId || ''
  const item = fragmentsByID.value.get(id)
  if (!item) { scheduleRefresh(); return }
  if (button.dataset.textAction === 'copy') void copy(item.content || '')
  else if (button.dataset.textAction === 'save') openSave(item)
  else void deleteText(id)
}
const menuCleanup: (() => void)[] = []
onMounted(() => {
  const unregisterRows = registerContextMenu('.text-row', contextMenu)
  const unregisterPage = registerPageContextMenu(pageMenu)
  menuCleanup.push(unregisterRows, unregisterPage)
  document.addEventListener('visibilitychange', onVisibility)
  document.addEventListener('paste', onDocumentPaste)
  window.addEventListener('resize', onResize)
  clockTimer = setInterval(checkExpirations, 60000)
  window.addEventListener('lan-copy:shutdown', onShutdown)
  window.addEventListener('pagehide', onPageHide, { once: true })
  if (window.EventSource) {
    eventSource = new EventSource('/api/events')
    eventSource.addEventListener('change', event => { let change: { texts?: boolean }; try { change = JSON.parse((event as MessageEvent).data) } catch { change = { texts: true } }; if (change.texts) scheduleRefresh() })
    eventSource.onopen = () => { stopFallback(); scheduleRefresh() }
    eventSource.onerror = startFallback
  } else startFallback()
  void document.fonts?.ready.then(checkTextOverflow).catch(() => {})
  void loadTexts()
})
onBeforeUnmount(() => {
  menuCleanup.forEach(cleanup => cleanup?.())
  document.removeEventListener('visibilitychange', onVisibility)
  document.removeEventListener('paste', onDocumentPaste)
  window.removeEventListener('resize', onResize)
  window.removeEventListener('lan-copy:shutdown', onShutdown)
  window.removeEventListener('pagehide', onPageHide)
  if (refreshTimer) clearTimeout(refreshTimer); if (clockTimer) clearInterval(clockTimer); if (resizeTimer) clearTimeout(resizeTimer)
  stopFallback(); controller?.abort(); eventSource?.close(); clearPendingImages()
})
</script>

<template>
    <section
      class="text-card fragment-compose" id="textPanel" :class="{ 'fragment-dragging': dragging }"
      @dragenter="onDragEnter" @dragover="onDragOver" @dragleave="onDragLeave" @drop="onDrop"
    >
      <textarea id="textInput" v-model="text" rows="4" placeholder="写点文字，也可以粘贴图片…" aria-label="要保存的文本" @keydown="onTextKey"></textarea>
      <div v-if="pendingImages.length" class="fragment-attachments" aria-label="待发送图片">
        <div v-for="(image, index) in pendingImages" :key="image.id" class="fragment-attachment">
          <button class="fragment-attachment-open" type="button" :aria-label="`查看图片 ${index + 1}`" title="查看图片" @click="openPendingImagePreview(image, $event.currentTarget as HTMLElement)"><img :src="image.url" :alt="image.file.name || `待发送图片 ${index + 1}`" @error="attachmentPreviewFailed(image)"></button>
          <button class="fragment-attachment-remove" type="button" :aria-label="`移除图片 ${index + 1}`" title="移除图片" @click="removePendingImage(image.id)">×</button>
        </div>
      </div>
      <input ref="fileInput" class="fragment-image-input" type="file" accept="image/png,image/jpeg,image/gif,image/webp,image/bmp" multiple hidden @change="onImagePicker">
      <div class="fragment-compose-actions">
        <button class="secondary fragment-image-picker" type="button" :disabled="publishing" title="添加图片（单张最大 20 MiB）" aria-label="添加图片（单张最大 20 MiB）" @click="pickImages">{{ pendingImages.length ? `＋ 图片 ${pendingImages.length}/${MAX_IMAGES_PER_FRAGMENT}` : '＋ 图片' }}</button>
        <div class="fragment-compose-submit">
          <span id="textCount" :class="{ 'over-limit': charCount > MAX_TEXT_CHARS }" aria-live="polite">{{ countLabel }}</span>
          <button class="primary fragment-save" id="shareTextBtn" type="button" :disabled="publishing" @click="publish"><span>{{ publishing ? '正在保存…' : '保存' }}</span><small v-if="!publishing" class="fragment-save-shortcut">{{ saveShortcut }}</small></button>
        </div>
      </div>
      <div v-if="dragging" class="fragment-drop-hint" aria-hidden="true">松开即可添加图片</div>
    </section>
    <section class="files-section text-section">
      <div class="section-head text-section-head"><p id="textSummary" aria-live="polite">{{ summary }}</p><button class="icon-btn" id="refreshBtn" type="button" title="刷新" aria-label="刷新碎片" @click="loadTexts"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M20 11a8.1 8.1 0 0 0-15.5-3M4 4v4h4m-4 5a8.1 8.1 0 0 0 15.5 3M20 20v-4h-4"/></svg></button></div>
      <div class="text-list" id="textList" @click="onRowClick">
        <article v-for="item in texts" :key="item.id" class="text-row fragment-row" :class="{ wide: isWide(item) }">
          <div v-if="hasText(item)" class="text-content" :class="{ expanded: expandedText.has(item.id) }" :data-text-id="item.id" v-html="renderText(item.content || '')"></div>
          <div v-if="imageList(item).length" class="fragment-image-grid" :class="{ single: imageList(item).length === 1 }">
            <button v-for="(image, index) in imageList(item)" :key="`${item.id}-${index}`" class="fragment-image-open" type="button" :data-text-id="!hasText(item) && index === 0 ? item.id : undefined" :aria-label="image.name || `查看图片 ${index + 1}`" title="查看图片" @click="openPublishedImagePreview(item, index, $event.currentTarget as HTMLElement)"><img :src="imageURL(item, index)" :alt="image.name || `共享图片 ${index + 1}`" loading="lazy" @load="rememberImageRatio(item, index, $event)"></button>
          </div>
          <div class="fragment-card-foot">
            <span class="fragment-meta">
              <button v-if="overflowingText.has(item.id)" class="fragment-expand" type="button" @click="toggleText(item.id)">{{ expandedText.has(item.id) ? '收起' : '展开' }}</button>
              <time :datetime="item.created">{{ formatTime(item.created) }}</time>
              <span v-if="imageList(item).length">{{ formatBytes(imageBytes(item)) }}</span>
              <span class="fragment-expiry" :title="expiryTitle(item.expiresAt)">{{ expiryLabel(item) }}</span>
            </span>
            <div class="fragment-actions">
              <button type="button" data-text-action="save" :data-text-id="item.id" title="存入内网空间" aria-label="存入内网空间"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 3v11m0 0 4-4m-4 4-4-4"/><path d="M5 16v4h14v-4"/></svg><span>存入内网</span></button>
              <button v-if="hasText(item)" type="button" data-text-action="copy" :data-text-id="item.id" title="复制文本" aria-label="复制文本"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M8 8h11v11H8zM5 16H4V5h11v1"/></svg><span>复制</span></button>
              <button class="danger" type="button" data-text-action="delete" :data-text-id="item.id" title="删除碎片" aria-label="删除碎片"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 7h16M9 7V4h6v3m3 0-1 13H7L6 7m4 4v5m4-5v5"/></svg><span>删除</span></button>
            </div>
          </div>
          <div v-if="item.expiresAt" class="fragment-lifetime" :class="lifetimeClass(item)" :title="expiryTitle(item.expiresAt)" role="progressbar" aria-valuemin="0" aria-valuemax="100" :aria-valuenow="expiryPercent(item)" :aria-valuetext="`${expiryLabel(item)}，${expiryClock(item.expiresAt)} 到期`"><span :style="{ width: `${expiryPercent(item)}%` }"></span></div>
        </article>
      </div>
      <EmptyState id="textEmptyState" :loaded="hasLoaded" loading-title="正在读取内容" empty-title="还没有保存碎片" empty-description="保存文字或图片后，局域网内的设备可以查看和下载" icon="◇" :hidden="texts.length>0||Boolean(loadError)" />
    </section>
    <FragmentSaveDialog v-model:open="saveOpen" :item="savingItem" @saved="scheduleRefresh" />
    <AppDialog :open="Boolean(previewImage)" dialog-class="fragment-image-preview-dialog" labelledby="fragmentImagePreviewTitle" @update:open="onPreviewOpen">
      <h2 id="fragmentImagePreviewTitle">图片预览</h2>
      <img v-if="previewImage" class="fragment-image-preview" :src="previewImage.url" :alt="previewImage.alt">
      <div class="dialog-actions"><button class="secondary" type="button" @click="closeImagePreview">关闭</button></div>
    </AppDialog>
</template>
