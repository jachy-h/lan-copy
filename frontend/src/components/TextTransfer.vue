<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { requestJSON } from '../api/client'
import { copyText, notify } from '../ui/fileUi'
import { confirm as confirmDialog } from '../ui/dialog'
import { register as registerContextMenu, registerPage as registerPageContextMenu } from '../ui/contextMenu'

interface SharedText { id: string; content: string; created: string }
interface TextResult { texts: SharedText[] }
const text = defineModel<string>({ default: '' })
const texts = ref<SharedText[]>([])
const hasLoaded = ref(false)
const loadError = ref('')
const stopped = ref(false)
const publishing = ref(false)
const byteCount = computed(() => new TextEncoder().encode(text.value).length)
const countLabel = computed(() => `${formatBytes(byteCount.value)} / 64 KiB`)
const summary = computed(() => loadError.value || (!hasLoaded.value ? '正在读取…' : texts.value.length ? `${texts.value.length} 条文本 · 点击即可复制` : '等待文字传入'))
const textsByID = computed(() => new Map(texts.value.map(item => [item.id, item.content])))
let controller: AbortController | null = null
let eventSource: EventSource | null = null
let fallbackTimer: ReturnType<typeof setInterval> | undefined
let refreshTimer: ReturnType<typeof setTimeout> | undefined
let refreshVersion = 0
let submitting = false

function formatBytes(bytes: number) {
  if (!bytes) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const index = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1)
  const value = bytes / 1024 ** index
  return `${value.toFixed(index === 0 || value >= 10 ? 0 : 1)} ${units[index]}`
}
function formatTime(value: string) {
  const date = new Date(value), today = new Date()
  return date.toDateString() === today.toDateString()
    ? `今天 ${date.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })}`
    : date.toLocaleDateString('zh-CN', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })
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
    window.dispatchEvent(new Event('lan-copy:data-updated'))
  } catch (error) {
    if (version !== refreshVersion || current.signal.aborted || (error instanceof Error && error.name === 'AbortError')) return
    startFallback(); loadError.value = '无法连接到服务'; window.dispatchEvent(new Event('lan-copy:data-failed')); notify('无法读取共享文本', true)
  } finally { if (controller === current) controller = null }
}
function scheduleRefresh() {
  if (stopped.value || document.hidden) return
  if (refreshTimer) clearTimeout(refreshTimer)
  refreshTimer = setTimeout(() => { refreshTimer = undefined; void loadTexts() }, 100)
}
async function publish() {
  if (submitting) return
  if (!text.value.trim()) { notify('请输入要共享的文本', true); document.querySelector<HTMLTextAreaElement>('#textInput')?.focus(); return }
  if (byteCount.value > 64 * 1024) { notify('文本超过 64 KiB 限制', true); return }
  submitting = true; publishing.value = true
  try {
    await requestJSON('/api/texts', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ content: text.value }) })
    text.value = ''; notify('文本已共享'); scheduleRefresh()
  } catch (error) { notify(error instanceof Error ? error.message : '发布失败', true) }
  finally { submitting = false; publishing.value = false }
}
async function copy(content: string) {
  const copied = await copyText(content)
  notify(copied ? '文本已复制' : '复制失败，请手动选择', !copied)
}
async function deleteText(id: string) {
  if (!textsByID.value.has(id)) { scheduleRefresh(); notify('这段文本已不存在，列表已刷新', true); return }
  if (!await confirmDialog({ title: '删除共享文本？', message: '确定删除这段共享文本吗？删除后将无法恢复。', confirmText: '删除', danger: true })) return
  try { await requestJSON(`/api/texts/${encodeURIComponent(id)}`, { method: 'DELETE' }); notify('共享文本已删除'); scheduleRefresh() }
  catch { notify('删除失败', true) }
}
function contextMenu(event: MouseEvent, row: Element) {
  const id = row.querySelector<HTMLElement>('[data-text-id]')?.dataset.textId
  if (!id || !textsByID.value.has(id)) return []
  return [
    { label: '复制文本', action: () => copy(textsByID.value.get(id) || '') },
    { label: '删除文本', danger: true, action: () => deleteText(id) },
  ]
}
function pageMenu() {
  return [
    { label: '刷新共享文本', action: loadTexts },
    { label: '输入要共享的文本', action: () => document.querySelector<HTMLTextAreaElement>('#textInput')?.focus() },
    { label: '复制当前页面地址', action: async () => { const copied = await copyText(location.href); notify(copied ? '页面地址已复制' : '复制失败，请手动选择', !copied) } },
  ]
}
function onVisibility() {
  if (document.hidden) { controller?.abort(); if (refreshTimer) clearTimeout(refreshTimer); refreshTimer = undefined; stopFallback() }
  else if (!stopped.value) { if (!eventSource || eventSource.readyState !== EventSource.OPEN) startFallback(); scheduleRefresh() }
}
function onShutdown() { stopped.value = true; stopFallback(); if (refreshTimer) clearTimeout(refreshTimer); controller?.abort(); eventSource?.close() }
function onPageHide() { controller?.abort(); eventSource?.close(); stopFallback() }
function onTextKey(event: KeyboardEvent) { if (event.key === 'Enter' && (event.ctrlKey || event.metaKey)) { event.preventDefault(); void publish() } }
function onRowClick(event: MouseEvent) {
  const button = (event.target as Element).closest<HTMLButtonElement>('[data-text-action]')
  if (!button) return
  const id = button.dataset.textId || ''
  if (button.dataset.textAction === 'copy') void copy(textsByID.value.get(id) || '')
  else void deleteText(id)
}
const menuCleanup: (() => void)[] = []
onMounted(() => {
  const unregisterRows = registerContextMenu('.text-row', contextMenu)
  const unregisterPage = registerPageContextMenu(pageMenu)
  menuCleanup.push(unregisterRows, unregisterPage)
  document.addEventListener('visibilitychange', onVisibility)
  window.addEventListener('lan-copy:shutdown', onShutdown)
  window.addEventListener('pagehide', onPageHide, { once: true })
  if (window.EventSource) {
    eventSource = new EventSource('/api/events')
    eventSource.addEventListener('change', event => { let change: { texts?: boolean }; try { change = JSON.parse((event as MessageEvent).data) } catch { change = { texts: true } }; if (change.texts) scheduleRefresh() })
    eventSource.onopen = () => { stopFallback(); scheduleRefresh() }
    eventSource.onerror = startFallback
  } else startFallback()
  void loadTexts()
})
onBeforeUnmount(() => {
  menuCleanup.forEach(cleanup => cleanup?.())
  document.removeEventListener('visibilitychange', onVisibility)
  window.removeEventListener('lan-copy:shutdown', onShutdown)
  window.removeEventListener('pagehide', onPageHide)
  if (refreshTimer) clearTimeout(refreshTimer); stopFallback(); controller?.abort(); eventSource?.close()
})
</script>

<template>
    <section class="text-card" id="textPanel">
      <div class="text-compose-head"><div class="text-icon" aria-hidden="true">Aa</div><div><h2>共享一段文本</h2><p>链接、验证码、命令或临时笔记</p></div></div>
      <textarea id="textInput" v-model="text" rows="7" placeholder="在这里粘贴或输入文字…" aria-label="要共享的文本" @keydown="onTextKey"></textarea>
      <div class="text-compose-foot"><span id="textCount" :class="{'over-limit':byteCount>65536}" aria-live="polite">{{ countLabel }}</span><button class="primary" id="shareTextBtn" type="button" :disabled="publishing" @click="publish">{{ publishing?'正在发布…':'发布文本' }}</button></div>
    </section>
    <section class="files-section text-section">
      <div class="section-head"><div><h2>共享文本</h2><p id="textSummary" aria-live="polite">{{ summary }}</p></div><button class="icon-btn" id="refreshBtn" type="button" title="刷新" aria-label="刷新共享内容" @click="loadTexts"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M20 11a8.1 8.1 0 0 0-15.5-3M4 4v4h4m-4 5a8.1 8.1 0 0 0 15.5 3M20 20v-4h-4"/></svg></button></div>
      <div class="text-list" id="textList" @click="onRowClick">
        <article v-for="item in texts" :key="item.id" class="text-row">
          <pre class="text-content">{{ item.content }}</pre><div class="text-row-foot"><time :datetime="item.created">{{ formatTime(item.created) }}</time><div class="actions"><button class="copy-btn" type="button" data-text-action="copy" :data-text-id="item.id" title="复制文本" aria-label="复制文本"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M8 8h11v11H8zM5 16H4V5h11v1"/></svg></button><button type="button" data-text-action="delete" :data-text-id="item.id" title="删除文本" aria-label="删除文本"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 7h16M9 7V4h6v3m3 0-1 13H7L6 7m4 4v5m4-5v5"/></svg></button></div></div>
        </article>
      </div>
      <div class="empty" :class="{hidden:texts.length>0||Boolean(loadError)}" id="textEmptyState" role="status" aria-live="polite"><template v-if="!hasLoaded"><div aria-hidden="true">…</div><strong>正在读取共享文本</strong><p>请稍候</p></template><template v-else><div>“ ”</div><strong>还没有共享文本</strong><p>发布后，局域网内的设备可以一键复制</p></template></div>
    </section>
</template>
