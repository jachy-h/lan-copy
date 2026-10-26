<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { ApiError, requestJSON } from '../api/client'
import { mountPreview } from '../preview/engine'
import { copyText, notify } from '../ui/fileUi'
import { confirm as confirmDialog } from '../ui/dialog'
import { fromEntries, fromFiles, type UploadEntry } from '../upload/uploadTree'
import { close as closeContextMenu, open as openContextMenu, register as registerContextMenu, registerPage as registerPageContextMenu } from '../ui/contextMenu'

interface FileEntry { name: string; path: string; directory: boolean; modified: string; size: number; previewType?: string }
interface FilesystemResult { entries: FileEntry[] }
interface MenuItem { label?: string; danger?: boolean; separator?: boolean; action?: () => void | Promise<void> }
type Mutation = 'mkdir' | 'move' | 'rename' | 'delete'

const currentPath = ref(new URLSearchParams(location.hash.slice(1)).get('path') || '')
const search = ref('')
const entries = ref<FileEntry[]>([])
const selectedPaths = ref(new Set<string>())
const selectionAnchor = ref<string | null>(null)
const focusedPath = ref<string | null>(null)
const sortKey = ref<'name' | 'modified' | 'type' | 'size'>('name')
const sortDirection = ref<'ascending' | 'descending'>('ascending')
const hasLoaded = ref(false)
const loadError = ref('')
const summary = ref('正在读取…')
const now = ref(Date.now())
const uploadVisible = ref(false)
const uploadComplete = ref(false)
const uploadLabel = ref('')
const uploadProgress = ref<number | null>(null)
const mutationPending = ref(false)
const editOpen = ref(false)
const editDialog = ref<HTMLDialogElement | null>(null)
const editName = ref<HTMLInputElement | null>(null)
const editError = ref('')
const editTitle = ref('新建文件夹')
const editValue = ref('')
const editExtension = ref('')
const editItem = ref<FileEntry | null>(null)
const editDirectory = ref('')
const moving = ref<FileEntry | null>(null)
const moveOpen = ref(false)
const moveDialog = ref<HTMLDialogElement | null>(null)
const movePath = ref('')
const moveFolders = ref<FileEntry[]>([])
const moveError = ref('')
const moveLoading = ref(false)
const movePending = ref(false)
const visibleItems = computed(() => {
  const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: 'base' })
  return [...entries.value].sort((a, b) => {
    if (a.directory !== b.directory) return a.directory ? -1 : 1
    let result = 0
    if (sortKey.value === 'modified') result = Date.parse(a.modified) - Date.parse(b.modified)
    else if (sortKey.value === 'size') result = (a.size || 0) - (b.size || 0)
    else if (sortKey.value === 'type') result = collator.compare(a.directory ? '文件夹' : (a.name.split('.').pop() || ''), b.directory ? '文件夹' : (b.name.split('.').pop() || ''))
    else result = collator.compare(a.name, b.name)
    return (sortDirection.value === 'ascending' ? 1 : -1) * (result || collator.compare(a.name, b.name))
  })
})
const folderCount = computed(() => entries.value.filter(item => item.directory).length)
const displaySummary = computed(() => search.value.trim() ? `${visibleItems.value.length} 个匹配项（含子文件夹）` : `${folderCount.value} 个文件夹 · ${entries.value.length - folderCount.value} 个文件`)
const breadcrumbs = computed(() => {
  const parts = currentPath.value ? currentPath.value.split('/') : []
  return [{ name: '全部文件', path: '' }, ...parts.map((name, index) => ({ name, path: parts.slice(0, index + 1).join('/') }))]
})
const moveBreadcrumbs = computed(() => {
  const parts = movePath.value ? movePath.value.split('/') : []
  return [{ name: '全部文件', path: '' }, ...parts.map((name, index) => ({ name, path: parts.slice(0, index + 1).join('/') }))]
})
const localhostAccess = ['localhost', '127.0.0.1', '::1', '[::1]'].includes(location.hostname)
const RECENT_WINDOW_MS = 4 * 60 * 60 * 1000
let searchTimer: ReturnType<typeof setTimeout> | undefined
let eventRefreshTimer: ReturnType<typeof setTimeout> | undefined
let fallbackRefreshTimer: ReturnType<typeof setInterval> | undefined
let freshTagTimer: ReturnType<typeof setInterval> | undefined
let directoryController: AbortController | null = null
let requestVersion = 0
let moveVersion = 0
let lastListing = ''
let events: EventSource | null = null
let stopped = false
let uploadActive = false
let dragDepth = 0
const previewEvents = new AbortController()
let previewInstance: { destroy(): void } | null = null
let previewReturnFocus: HTMLElement | null = null

function formatBytes(bytes: number) {
  if (bytes < 1024) return `${bytes} B`
  const unit = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), 4)
  return `${(bytes / 1024 ** unit).toFixed(1)} ${['B', 'KB', 'MB', 'GB', 'TB'][unit]}`
}
function isRecent(value: string) { const age = now.value - Date.parse(value); return Number.isFinite(Date.parse(value)) && age >= 0 && age < RECENT_WINDOW_MS }
function dateLabel(value: string) { return new Date(value).toLocaleString('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' }) }
function highlightParts(name: string) {
  const query = search.value.trim()
  if (!query) return [{ text: name, mark: false }]
  const pattern = new RegExp(query.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'), 'giu')
  const parts: { text: string; mark: boolean }[] = []
  let position = 0
  for (const match of name.matchAll(pattern)) {
    if (match.index > position) parts.push({ text: name.slice(position, match.index), mark: false })
    parts.push({ text: match[0], mark: true }); position = match.index + match[0].length
  }
  if (!parts.length) return [{ text: name, mark: false }]
  if (position < name.length) parts.push({ text: name.slice(position), mark: false })
  return parts
}
function isSelected(item: FileEntry) { return selectedPaths.value.has(item.path) }
function entryType(item: FileEntry) { return item.directory ? '文件夹' : item.name.includes('.') ? `${item.name.split('.').pop()!.toUpperCase()} 文件` : '文件' }
function entryIcon(item: FileEntry) { return item.directory ? '▱' : item.name.split('.').pop()?.slice(0, 4) || '文件' }
function previewHref(item: FileEntry) { const params = new URLSearchParams({ path: item.path, from: location.pathname + location.search + location.hash }); return `/preview.html?${params}` }
function thumbnailHref(item: FileEntry) { return `/thumbnails/${encodeURIComponent(item.path)}?${new URLSearchParams({ v: `${item.modified}-${item.size}` })}` }
function requestPath(path: string) { return `/api/filesystem?${new URLSearchParams({ path, q: search.value.trim() })}` }

async function loadDirectory() {
  if (stopped || document.hidden) return
  if (searchTimer) clearTimeout(searchTimer)
  directoryController?.abort()
  const controller = new AbortController(); directoryController = controller
  const version = ++requestVersion
  loadError.value = ''
  try {
    const result = await requestJSON<FilesystemResult>(requestPath(currentPath.value), { cache: 'no-store', signal: controller.signal })
    if (version !== requestVersion || controller.signal.aborted) return
    hasLoaded.value = true
    if (events?.readyState === EventSource.OPEN) stopFallbackRefresh()
    const signature = JSON.stringify({ query: search.value.trim(), result })
    if (signature === lastListing) { window.dispatchEvent(new Event('lan-copy:data-updated')); return }
    lastListing = signature
    entries.value = result.entries
    const valid = new Set(entries.value.map(item => item.path))
    selectedPaths.value = new Set([...selectedPaths.value].filter(path => valid.has(path)))
    summary.value = displaySummary.value
    window.dispatchEvent(new Event('lan-copy:data-updated'))
  } catch (error) {
    if (version !== requestVersion || controller.signal.aborted || (error instanceof Error && error.name === 'AbortError')) return
    startFallbackRefresh()
    if (currentPath.value && error instanceof ApiError && error.status === 404) { notify('原文件夹已被移动，已返回全部文件'); navigate(''); return }
    window.dispatchEvent(new Event('lan-copy:data-failed'))
    lastListing = ''; entries.value = []; loadError.value = message(error, '读取失败，请重新载入页面'); summary.value = '读取失败，请重新载入页面'; notify(loadError.value, true)
  } finally {
    if (directoryController === controller) directoryController = null
  }
}
function message(error: unknown, fallback: string) { return error instanceof Error ? error.message : fallback }
function navigate(path: string) {
  if (path === currentPath.value) return
  closeContextMenu(false); directoryController?.abort()
  currentPath.value = path; lastListing = ''; hasLoaded.value = false; entries.value = []; selectedPaths.value.clear(); selectionAnchor.value = null; focusedPath.value = null; summary.value = '正在读取…'
  history.pushState(null, '', `#${new URLSearchParams({ path })}`)
  void loadDirectory()
}
function goParent() { navigate(currentPath.value.includes('/') ? currentPath.value.slice(0, currentPath.value.lastIndexOf('/')) : '') }
function setSort(key: 'name' | 'modified' | 'type' | 'size') {
  if (sortKey.value === key) sortDirection.value = sortDirection.value === 'ascending' ? 'descending' : 'ascending'
  else { sortKey.value = key; sortDirection.value = 'ascending' }
  try { localStorage.setItem('lan-copy.files.sort', key); localStorage.setItem('lan-copy.files.direction', sortDirection.value) } catch {}
}
function selectRow(item: FileEntry, event: MouseEvent | KeyboardEvent) {
  focusedPath.value = item.path
  if (event.shiftKey && selectionAnchor.value) {
    const start = visibleItems.value.findIndex(entry => entry.path === selectionAnchor.value), end = visibleItems.value.findIndex(entry => entry.path === item.path)
    if (start >= 0 && end >= 0) {
      const selection = new Set(event.ctrlKey || event.metaKey ? selectedPaths.value : [])
      visibleItems.value.slice(Math.min(start, end), Math.max(start, end) + 1).forEach(entry => selection.add(entry.path)); selectedPaths.value = selection
    }
  } else if (event.ctrlKey || event.metaKey) {
    const selection = new Set(selectedPaths.value)
    selection.has(item.path) ? selection.delete(item.path) : selection.add(item.path)
    selectedPaths.value = selection; selectionAnchor.value = item.path
  } else { selectedPaths.value = new Set([item.path]); selectionAnchor.value = item.path }
}
function selectContextItem(item: FileEntry) {
  focusedPath.value = item.path
  if (!selectedPaths.value.has(item.path)) { selectedPaths.value = new Set([item.path]); selectionAnchor.value = item.path }
}
function clearSelection() { selectedPaths.value = new Set(); selectionAnchor.value = null }
const selectedItems = computed(() => entries.value.filter(item => selectedPaths.value.has(item.path)))

function openEntry(item: FileEntry, trigger: HTMLElement | null = document.activeElement as HTMLElement) {
  if (item.directory) navigate(item.path)
  else if (item.previewType) openPreview(item.path, trigger)
  else notify('该文件格式暂不支持预览')
}
function openPreview(path: string, trigger: HTMLElement | null) {
  const dialog = document.querySelector<HTMLDialogElement>('#previewDialog')
  if (!dialog) return
  previewInstance?.destroy(); previewReturnFocus = trigger
  const title = document.querySelector<HTMLElement>('#previewTitle')!, download = document.querySelector<HTMLAnchorElement>('#previewDownload')!
  title.textContent = path.split('/').pop() || path; download.href = `/files/${encodeURIComponent(path)}`; download.download = path.split('/').pop() || path; download.classList.remove('hidden')
  document.querySelector('#textTools')?.classList.add('hidden')
  const status = document.querySelector<HTMLElement>('#previewStatus'); if (status) status.textContent = '正在读取文件…'
  if (!dialog.open) dialog.showModal()
  previewInstance = mountPreview(dialog, path)
  document.querySelector<HTMLElement>('#previewClose')?.focus()
}
function closePreview() {
  previewInstance?.destroy(); previewInstance = null
  const dialog = document.querySelector<HTMLDialogElement>('#previewDialog'); if (dialog?.open) dialog.close()
  if (previewReturnFocus?.isConnected) previewReturnFocus.focus(); else document.querySelector<HTMLElement>('#entries')?.focus()
}
function itemAt(path: string) { return entries.value.find(item => item.path === path) }
function fileMenuItems(trigger: HTMLElement | null): MenuItem[] {
  const items = selectedItems.value, item = items.length === 1 ? items[0] : null
  const actionTrigger = trigger?.matches('.explorer-row') ? trigger.querySelector<HTMLElement>('.row-menu-button') || trigger : trigger
  const actions: MenuItem[] = [{ label: item?.directory ? '在此文件夹中新建文件夹' : '新建文件夹', action: () => openEdit(null, item?.directory ? item.path : currentPath.value) }]
  if (items.length === 1 && item) {
    actions.push({ label: item.directory ? '打开' : item.previewType ? '预览' : '下载', action: () => openEntry(item, trigger) })
    if (!item.directory && localhostAccess) actions.push({ label: '打开所在文件夹', action: () => openContainingFolder(item, actionTrigger) })
    actions.push({ label: '移动…', action: () => openMove(item) }, { label: '重命名', action: () => openEdit(item) }, { label: '删除', danger: true, action: () => deleteEntry(item) })
  }
  actions.push({ label: items.length > 1 ? '复制所选路径' : '复制路径', action: async () => { const ok = await copyText(items.map(entry => entry.path).join('\n')); notify(ok ? `已复制${items.length > 1 ? `${items.length} 个` : ''}路径` : '无法访问剪贴板，请检查浏览器权限', !ok) } })
  return actions
}
function openRowMenu(event: MouseEvent, item: FileEntry) {
  event.stopPropagation(); selectContextItem(item)
  const button = event.currentTarget as HTMLElement, rect = button.getBoundingClientRect()
  openContextMenu(fileMenuItems(button), rect.right, rect.bottom, button)
}
async function openContainingFolder(item: FileEntry, trigger: HTMLElement | null) {
  if (trigger instanceof HTMLButtonElement) trigger.disabled = true
  try {
    await requestJSON(`/api/files/${encodeURIComponent(item.path)}/folder`, { method: 'POST', headers: { 'X-Lan-Copy-Action': 'folder' } })
    notify('已打开文件所在文件夹')
  } catch (error) { notify(message(error, '无法打开文件所在文件夹'), true) }
  finally { if (trigger instanceof HTMLButtonElement) trigger.disabled = false }
}
async function mutate(action: Mutation, data: Record<string, unknown>) {
  if (mutationPending.value) return false
  mutationPending.value = true
  try {
    await requestJSON(`/api/filesystem/${action}`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(data) })
    notify({ mkdir: '文件夹已创建', move: '已移动', rename: '已重命名', delete: '已删除' }[action])
    if (action === 'mkdir' && data.path !== currentPath.value) navigate(String(data.path)); else await loadDirectory()
    return true
  } catch (error) {
    if (editOpen.value) editError.value = message(error, '操作失败')
    if (moveOpen.value) moveError.value = message(error, '操作失败')
    notify(message(error, '操作失败'), true); return false
  } finally { mutationPending.value = false }
}
function openEdit(item: FileEntry | null = null, directory = currentPath.value) {
  editItem.value = item; editDirectory.value = directory; editError.value = ''
  const dot = item && !item.directory ? item.name.lastIndexOf('.') : -1
  editExtension.value = dot > 0 ? item!.name.slice(dot) : ''
  editTitle.value = item ? '重命名' : directory ? `在“${directory.split('/').at(-1)}”中新建文件夹` : '新建文件夹'
  editValue.value = item ? item.name.slice(0, item.name.length - editExtension.value.length) : ''
  editOpen.value = true
}
watch(editOpen, async open => { if (!open) return; await nextTick(); if (editDialog.value && !editDialog.value.open) editDialog.value.showModal(); editName.value?.focus(); editName.value?.select() })
function submitEdit() {
  const baseName = editValue.value.trim()
  if (!baseName || /[\\/:*?"<>|\x00-\x1f\x7f]/.test(baseName) || baseName === '.' || baseName === '..' || baseName.endsWith('.')) { editError.value = '请输入有效名称，不可包含斜线或特殊字符'; return }
  const name = baseName + editExtension.value, item = editItem.value
  if (item && name === item.name) { editDialog.value?.close(); return }
  const data = item ? { path: item.path, destination: item.path.includes('/') ? item.path.slice(0, item.path.lastIndexOf('/')) : '', name } : { path: editDirectory.value, name }
  void mutate(item ? 'rename' : 'mkdir', data).then(ok => { if (ok) editDialog.value?.close() })
}
function openMove(item: FileEntry) {
  moving.value = item; movePath.value = currentPath.value; moveError.value = ''; moveOpen.value = true
  void loadMoveFolders(movePath.value)
}
watch(moveOpen, async open => { if (open) { await nextTick(); if (moveDialog.value && !moveDialog.value.open) moveDialog.value.showModal() } })
async function loadMoveFolders(path: string) {
  if (!moving.value) return
  const version = ++moveVersion; moveLoading.value = true; moveError.value = ''; movePath.value = path
  try {
    const result = await requestJSON<FilesystemResult>(`/api/filesystem?${new URLSearchParams({ path })}`, { cache: 'no-store' })
    if (version !== moveVersion) return
    moveFolders.value = result.entries.filter(item => item.directory && item.path !== moving.value!.path && !item.path.startsWith(moving.value!.path + '/'))
  } catch (error) { if (version === moveVersion) moveError.value = message(error, '无法读取目标文件夹') }
  finally { if (version === moveVersion) moveLoading.value = false }
}
const moveParent = computed(() => moving.value?.path.includes('/') ? moving.value.path.slice(0, moving.value.path.lastIndexOf('/')) : '')
const moveDisabled = computed(() => moveLoading.value || movePending.value || !moving.value || movePath.value === moveParent.value)
async function confirmMove() {
  if (!moving.value || moveDisabled.value) return
  movePending.value = true
  if (await mutate('move', { path: moving.value.path, destination: movePath.value })) moveDialog.value?.close()
  movePending.value = false
}
async function deleteEntry(item: FileEntry) {
  if (mutationPending.value) return
  mutationPending.value = true
  try {
    if (!await confirmDialog({ title: '删除文件夹或文件？', message: `确定删除“${item.name}”吗？${item.directory ? '' : '文件将从共享电脑中删除。'}`, confirmText: '删除', danger: true })) return
    try { await requestJSON('/api/filesystem/delete', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ path: item.path }) }) }
    catch (error) {
      if (!item.directory || !(error instanceof ApiError) || !error.body.requiresConfirmation) throw error
      if (!await confirmDialog({ title: '删除整个文件夹？', message: `“${item.name}”包含内容。确定删除整个文件夹及其中所有文件和子文件夹吗？此操作无法恢复。`, confirmText: '删除全部内容', danger: true })) return
      await requestJSON('/api/filesystem/delete', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ path: item.path, recursive: true }) })
    }
    notify('已删除'); await loadDirectory()
  } catch (error) { notify(message(error, '删除失败'), true); await loadDirectory() }
  finally { mutationPending.value = false }
}
function clearSearch() { search.value = ''; void nextTick(() => document.querySelector<HTMLInputElement>('.search-box input')?.focus()) }
watch(search, () => {
  if (searchTimer) clearTimeout(searchTimer)
  directoryController?.abort(); ++requestVersion; hasLoaded.value = false; summary.value = '正在搜索…'
  searchTimer = setTimeout(() => void loadDirectory(), 200)
})
function onRowKey(event: KeyboardEvent, item: FileEntry) {
  const index = visibleItems.value.findIndex(entry => entry.path === item.path)
  if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
    event.preventDefault(); const next = Math.max(0, Math.min(visibleItems.value.length - 1, index + (event.key === 'ArrowDown' ? 1 : -1)))
    focusedPath.value = visibleItems.value[next]?.path || null
    nextTick(() => document.querySelector<HTMLElement>(`.explorer-row[data-path="${CSS.escape(focusedPath.value || '')}"]`)?.focus())
  } else if (event.key === 'Enter' && !(event.target as Element).closest('button,a')) { event.preventDefault(); openEntry(item, event.currentTarget as HTMLElement) }
  else if (event.key === ' ' && !(event.target as Element).closest('button,a')) { event.preventDefault(); selectRow(item, { shiftKey: false, ctrlKey: true, metaKey: false } as KeyboardEvent) }
}
function onDocumentKey(event: KeyboardEvent) {
  const target = event.target as Element
  if (event.defaultPrevented || target.closest('input,textarea,select,[contenteditable=true],dialog') || !document.querySelector<HTMLElement>('#fileMenu')?.hidden) return
  const command = event.ctrlKey || event.metaKey
  if (event.key === 'Escape') { if (selectedPaths.value.size) clearSelection(); return }
  if (!target.closest('#entries,.explorer-row')) return
  if (command && event.key.toLowerCase() === 'a') { event.preventDefault(); selectedPaths.value = new Set(entries.value.map(item => item.path)); return }
  if (event.key === 'F2' && selectedItems.value.length === 1) { event.preventDefault(); openEdit(selectedItems.value[0]) }
  else if (event.key === 'Delete' && selectedItems.value.length === 1) { event.preventDefault(); void deleteEntry(selectedItems.value[0]) }
  else if (command && event.key.toLowerCase() === 'c' && selectedItems.value.length) {
    event.preventDefault(); void copyText(selectedItems.value.map(item => item.path).join('\n')).then(ok => notify(ok ? '已复制路径' : '无法访问剪贴板，请检查浏览器权限', !ok))
  }
}
function onPopState() { if (location.pathname === '/transfer-text') return; currentPath.value = new URLSearchParams(location.hash.slice(1)).get('path') || ''; clearSelection(); focusedPath.value = null; lastListing = ''; hasLoaded.value = false; entries.value = []; summary.value = '正在读取…'; void loadDirectory() }
function scheduleDirectoryRefresh() {
  if (stopped || document.hidden) return
  if (searchTimer) clearTimeout(searchTimer); searchTimer = undefined
  if (eventRefreshTimer) clearTimeout(eventRefreshTimer)
  eventRefreshTimer = setTimeout(() => { eventRefreshTimer = undefined; void loadDirectory() }, 100)
}
function startFallbackRefresh() { if (!stopped && !document.hidden && !fallbackRefreshTimer) fallbackRefreshTimer = setInterval(scheduleDirectoryRefresh, 60000) }
function stopFallbackRefresh() { if (fallbackRefreshTimer) clearInterval(fallbackRefreshTimer); fallbackRefreshTimer = undefined }
function onVisibility() {
  if (document.hidden) { directoryController?.abort(); if (searchTimer) clearTimeout(searchTimer); if (eventRefreshTimer) clearTimeout(eventRefreshTimer); eventRefreshTimer = undefined; stopFallbackRefresh() }
  else if (!stopped) { now.value = Date.now(); if (!events || events.readyState !== EventSource.OPEN) startFallbackRefresh(); scheduleDirectoryRefresh() }
}
function onShutdown() { stopped = true; if (searchTimer) clearTimeout(searchTimer); if (eventRefreshTimer) clearTimeout(eventRefreshTimer); stopFallbackRefresh(); directoryController?.abort(); events?.close(); if (freshTagTimer) clearInterval(freshTagTimer); previewInstance?.destroy() }
function onPageHide() { if (freshTagTimer) clearInterval(freshTagTimer); previewInstance?.destroy() }

function internalDragOver(event: DragEvent) {
  if (!event.dataTransfer?.types.includes('application/x-lan-copy-path')) return
  event.preventDefault(); event.dataTransfer.dropEffect = 'move'; (event.currentTarget as HTMLElement).classList.add('drop-target')
}
function internalDragLeave(event: DragEvent) { (event.currentTarget as HTMLElement).classList.remove('drop-target') }
function internalDrop(event: DragEvent, target: string) {
  if (!event.dataTransfer?.types.includes('application/x-lan-copy-path')) return
  event.preventDefault(); event.stopPropagation(); (event.currentTarget as HTMLElement).classList.remove('drop-target')
  const source = event.dataTransfer.getData('application/x-lan-copy-path'); if (source) void mutate('move', { path: source, destination: target })
}
function externalFiles(event: DragEvent) { return event.dataTransfer?.types.includes('Files') }
function onExternalDragEnter(event: DragEvent) { if (!externalFiles(event)) return; event.preventDefault(); dragDepth++; document.querySelector('.explorer')?.classList.add('upload-dragging') }
function onExternalDragOver(event: DragEvent) { if (externalFiles(event)) { event.preventDefault(); if (event.dataTransfer) event.dataTransfer.dropEffect = 'copy' } }
function onExternalDragLeave(event: DragEvent) { if (!externalFiles(event)) return; if (--dragDepth <= 0) { dragDepth = 0; document.querySelector('.explorer')?.classList.remove('upload-dragging') } }
function onExternalDrop(event: DragEvent) {
  if (!externalFiles(event)) return
  event.preventDefault(); dragDepth = 0; document.querySelector('.explorer')?.classList.remove('upload-dragging')
  const items = [...(event.dataTransfer?.items || [])].filter(item => item.kind === 'file')
  const roots = items.map(item => (item as DataTransferItem & { getAsEntry?: () => FileSystemEntry | null; webkitGetAsEntry?: () => FileSystemEntry | null }).getAsEntry?.() || (item as DataTransferItem & { webkitGetAsEntry?: () => FileSystemEntry | null }).webkitGetAsEntry?.())
  const files = [...(event.dataTransfer?.files || [])]
  if (roots.some(Boolean) && !roots.every(Boolean)) { notify('部分拖入内容无法读取，请检查权限后重试', true); return }
  void uploadToFolder(progress => roots.length && roots.every(Boolean) ? fromEntries(roots as FileSystemEntry[], progress) : Promise.resolve(fromFiles(files, false, progress)))
}
async function uploadToFolder(collect: (progress: (count: number) => void) => Promise<UploadEntry[]>) {
  if (uploadActive) { notify('正在上传，请等待本次完成后再拖入', true); return }
  uploadActive = true; const uploadPath = currentPath.value
  uploadVisible.value = true; uploadComplete.value = false; uploadProgress.value = null; uploadLabel.value = '正在读取文件和文件夹…'
  try {
    const uploadEntries = await collect(count => { uploadLabel.value = `正在读取文件和文件夹… ${count} 项` })
    if (!uploadEntries.length) throw new Error('未读取到文件或文件夹，请检查浏览器权限后重试')
    const fileCount = uploadEntries.filter(entry => !entry.directory).length, folderCount = uploadEntries.length - fileCount
    const description = `${fileCount} 个文件${folderCount ? `、${folderCount} 个文件夹` : ''}`, replace = new Set<string>()
    const send = async () => {
      const preflight = await fetch(`/api/upload-tree/preflight?${new URLSearchParams({ path: uploadPath })}`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ entries: uploadEntries.map(({ path, directory }) => ({ path, directory })) }) })
      const checked = await preflight.json()
      if (!preflight.ok) throw new Error(checked.error || '无法检查上传目标')
      const unresolved = (checked.conflicts || []).filter((name: string) => !replace.has(name))
      if (unresolved.length) {
        await fetch(`/api/upload-tree/session/${encodeURIComponent(checked.session)}`, { method: 'DELETE' })
        const error = new Error(checked.error || '目标位置已存在同名文件或文件夹') as Error & { conflicts?: string[] }; error.conflicts = unresolved; throw error
      }
      const form = new FormData()
      form.append('manifest', JSON.stringify({ entries: uploadEntries.map(({ path, directory }) => ({ path, directory })), replace: [...replace] }))
      uploadEntries.forEach((entry, index) => { if (!entry.directory && entry.file) form.append(`file-${index}`, entry.file, entry.file.name) })
      uploadLabel.value = `正在上传 ${description}到 ${uploadPath || '全部文件'}`; uploadProgress.value = 0
      await new Promise<void>((resolve, reject) => {
        const xhr = new XMLHttpRequest()
        xhr.upload.addEventListener('progress', event => { if (event.lengthComputable) uploadProgress.value = Math.min(99, Math.round(event.loaded / event.total * 100)) })
        xhr.addEventListener('load', () => {
          if (xhr.status >= 200 && xhr.status < 300) { resolve(); return }
          let errorMessage = '上传失败，请重试', conflicts: string[] | undefined
          try { const body = JSON.parse(xhr.responseText); errorMessage = body.error || errorMessage; conflicts = body.conflicts } catch {}
          const error = new Error(errorMessage) as Error & { conflicts?: string[] }
          if (xhr.status === 409 && Array.isArray(conflicts) && conflicts.length) error.conflicts = conflicts
          reject(error)
        })
        xhr.addEventListener('error', () => reject(new Error('网络中断，请重新拖入文件或文件夹重试')))
        xhr.addEventListener('abort', () => reject(new Error('上传已中止，请重新上传')))
        xhr.open('POST', `/api/upload-tree?${new URLSearchParams({ path: uploadPath, session: checked.session })}`); xhr.send(form)
      }).finally(() => fetch(`/api/upload-tree/session/${encodeURIComponent(checked.session)}`, { method: 'DELETE' }).catch(() => {}))
    }
    for (;;) {
      try { await send(); break }
      catch (error) {
        const conflicts = (error as Error & { conflicts?: string[] }).conflicts
        if (!conflicts) throw error
        for (const name of conflicts) {
          const folder = uploadEntries.some(entry => entry.path.startsWith(name + '/') || (entry.path === name && entry.directory))
          if (!await confirmDialog({ title: '替换同名内容？', message: `目标位置已存在“${name}”，是否替换？${folder ? '\n将替换整个同名文件或文件夹，原有内容将被删除。' : '\n原文件或同名文件夹将被替换。'}\n取消将停止本次上传，保留原有内容。`, confirmText: '替换', danger: true })) {
            uploadProgress.value = 0; uploadLabel.value = '已取消上传，原有内容已保留'; notify('已取消上传'); return
          }
          replace.add(name)
        }
      }
    }
    uploadProgress.value = 100; uploadComplete.value = true; uploadLabel.value = `${description}已上传到 ${uploadPath || '全部文件'}`; notify('上传完成'); await loadDirectory()
  } catch (error) { uploadProgress.value = 0; uploadLabel.value = message(error, '无法读取文件夹，请检查权限后重试'); notify(uploadLabel.value, true) }
  finally { uploadActive = false }
}

const menuCleanup: (() => void)[] = []
onMounted(() => {
  try {
    const savedSort = localStorage.getItem('lan-copy.files.sort'), savedDirection = localStorage.getItem('lan-copy.files.direction')
    if (['name', 'modified', 'type', 'size'].includes(savedSort || '')) sortKey.value = savedSort as 'name' | 'modified' | 'type' | 'size'
    if (['ascending', 'descending'].includes(savedDirection || '')) sortDirection.value = savedDirection as typeof sortDirection.value
  } catch {}
  const unregisterRows = registerContextMenu('.explorer-row', (event, row) => {
    const item = itemAt((row as HTMLElement).dataset.path || '')
    if (!item) return []
    selectContextItem(item); return fileMenuItems(row as HTMLElement)
  })
  const unregisterPage = registerPageContextMenu(() => [
    { label: '新建文件夹', action: () => openEdit(null, currentPath.value) }, { separator: true }, { label: '刷新文件列表', action: loadDirectory },
    ...(currentPath.value ? [{ label: '返回全部文件', action: () => navigate('') }] : []),
    { label: '复制当前页面地址', action: async () => { const ok = await copyText(location.href); notify(ok ? '页面地址已复制' : '复制失败，请检查浏览器权限', !ok) } },
  ])
  menuCleanup.push(unregisterRows, unregisterPage)
  document.addEventListener('keydown', onDocumentKey); document.addEventListener('visibilitychange', onVisibility)
  window.addEventListener('popstate', onPopState); window.addEventListener('lan-copy:shutdown', onShutdown); window.addEventListener('pagehide', onPageHide, { once: true })
  const previewDialog = document.querySelector<HTMLDialogElement>('#previewDialog')
  document.querySelector('#previewClose')?.addEventListener('click', closePreview, { signal: previewEvents.signal })
  previewDialog?.addEventListener('click', event => { if (event.target === event.currentTarget) closePreview() }, { signal: previewEvents.signal })
  previewDialog?.addEventListener('cancel', event => { event.preventDefault(); closePreview() }, { signal: previewEvents.signal })
  previewDialog?.addEventListener('close', () => { previewInstance?.destroy(); previewInstance = null }, { signal: previewEvents.signal })
  if (window.EventSource) {
    events = new EventSource('/api/events')
    events.addEventListener('change', event => { let change: { files?: boolean }; try { change = JSON.parse((event as MessageEvent).data) } catch { change = { files: true } }; if (change.files) scheduleDirectoryRefresh() })
    events.onopen = () => { stopFallbackRefresh(); scheduleDirectoryRefresh() }; events.onerror = startFallbackRefresh
  } else startFallbackRefresh()
  document.addEventListener('dragenter', onExternalDragEnter); document.addEventListener('dragover', onExternalDragOver); document.addEventListener('dragleave', onExternalDragLeave); document.addEventListener('drop', onExternalDrop)
  void loadDirectory(); freshTagTimer = setInterval(() => { if (!document.hidden) now.value = Date.now() }, 60000)
})
onBeforeUnmount(() => {
  closePreview()
  menuCleanup.forEach(cleanup => cleanup?.())
  previewEvents.abort()
  document.removeEventListener('keydown', onDocumentKey); document.removeEventListener('visibilitychange', onVisibility)
  window.removeEventListener('popstate', onPopState); window.removeEventListener('lan-copy:shutdown', onShutdown); window.removeEventListener('pagehide', onPageHide)
  document.removeEventListener('dragenter', onExternalDragEnter); document.removeEventListener('dragover', onExternalDragOver); document.removeEventListener('dragleave', onExternalDragLeave); document.removeEventListener('drop', onExternalDrop)
  if (searchTimer) clearTimeout(searchTimer); if (eventRefreshTimer) clearTimeout(eventRefreshTimer); stopFallbackRefresh(); if (freshTagTimer) clearInterval(freshTagTimer)
  directoryController?.abort(); events?.close(); previewInstance?.destroy()
})
</script>

<template>
  <section class="files-section explorer" aria-label="共享空间文件列表">
    <div class="explorer-navigation">
      <button class="secondary" type="button" aria-label="返回上一级" :disabled="!currentPath" @click="goParent">↑ 上一级</button>
      <nav class="breadcrumbs" aria-label="文件夹路径">
        <template v-for="(crumb, index) in breadcrumbs" :key="crumb.path">
          <span v-if="index" aria-hidden="true">/</span>
          <button class="crumb" type="button" :aria-current="index===breadcrumbs.length-1?'location':undefined" @click="navigate(crumb.path)" @dragover="internalDragOver" @dragleave="internalDragLeave" @drop="internalDrop($event,crumb.path)">{{ crumb.name }}</button>
        </template>
      </nav>
      <div class="search-box"><input v-model="search" type="search" placeholder="搜索当前文件夹及子文件夹…" aria-label="搜索当前文件夹及子文件夹"><button class="search-clear" type="button" aria-label="清除搜索" :hidden="!search.trim()" @click="clearSearch">×</button></div>
    </div>
    <div class="file-list-header" role="row">
      <div v-for="column in [{key:'name',label:'名称'},{key:'modified',label:'修改日期'},{key:'type',label:'类型'},{key:'size',label:'大小'}]" :key="column.key" role="columnheader" :aria-sort="sortKey===column.key?sortDirection:'none'"><button type="button" :data-sort="column.key" @click="setSort(column.key as 'name'|'modified'|'type'|'size')">{{ column.label }} <span>{{ sortKey===column.key?(sortDirection==='ascending'?'↑':'↓'):'' }}</span></button></div><span aria-hidden="true"></span>
    </div>
    <div id="entries" class="file-list" role="grid" aria-label="共享文件列表" aria-describedby="entrySummary" tabindex="0">
      <article v-for="item in visibleItems" :key="item.path" class="file-row explorer-row" :class="{selected:isSelected(item), 'search-result':Boolean(search.trim())}" :data-path="item.path" role="row" :aria-selected="isSelected(item)" :tabindex="item.path===focusedPath||(!focusedPath&&item===visibleItems[0])?0:-1" draggable="true" @click="selectRow(item,$event)" @dblclick="!($event.target as Element).closest('button,a')&&openEntry(item,$event.currentTarget as HTMLElement)" @keydown="onRowKey($event,item)" @dragstart="$event.dataTransfer?.setData('application/x-lan-copy-path',item.path)" @dragover="item.directory&&internalDragOver($event)" @dragleave="internalDragLeave" @drop="item.directory&&internalDrop($event,item.path)">
        <div class="file-main name-cell" role="gridcell"><div class="file-icon" :class="{'folder-icon':item.directory}" aria-hidden="true">{{ entryIcon(item) }}<img v-if="!item.directory&&item.previewType?.startsWith('image/')" :src="thumbnailHref(item)" alt="" loading="lazy" decoding="async" width="128" height="128" draggable="false" @load="($event.currentTarget as HTMLImageElement).parentElement?.classList.add('has-thumbnail')" @error="($event.currentTarget as HTMLImageElement).remove()"></div><button v-if="item.directory" class="file-name folder-name" type="button" :title="item.name" @click.stop="navigate(item.path)"><template v-for="(part,index) in highlightParts(item.name)" :key="index"><mark v-if="part.mark">{{ part.text }}</mark><template v-else>{{ part.text }}</template></template></button><a v-else-if="item.previewType" class="file-name download-name" :href="previewHref(item)" :title="item.name" @click="$event.preventDefault();openEntry(item,$event.currentTarget as HTMLElement)"><template v-for="(part,index) in highlightParts(item.name)" :key="index"><mark v-if="part.mark">{{ part.text }}</mark><template v-else>{{ part.text }}</template></template></a><a v-else class="file-name download-name preview-unavailable" href="#" :title="`${item.name}（该文件格式暂不支持预览）`" @click.prevent="openEntry(item,$event.currentTarget as HTMLElement)"><template v-for="(part,index) in highlightParts(item.name)" :key="index"><mark v-if="part.mark">{{ part.text }}</mark><template v-else>{{ part.text }}</template></template></a><span v-if="!item.directory&&isRecent(item.modified)" class="file-tag recent-tag" title="最近 4 小时内修改" aria-label="新：最近 4 小时内修改">新</span></div>
        <div class="file-cell modified-cell" role="gridcell">{{ dateLabel(item.modified) }}</div><div class="file-cell type-cell" role="gridcell">{{ entryType(item) }}</div><div class="file-cell size-cell" role="gridcell">{{ item.directory?'—':formatBytes(item.size) }}</div><div class="explorer-actions"><button class="secondary row-menu-button" type="button" :aria-label="`更多操作：${item.name}`" aria-haspopup="menu" @click="openRowMenu($event,item)">⋯</button></div>
      </article>
    </div>
    <div class="empty" :class="{hidden:visibleItems.length>0||Boolean(loadError)}" role="status" aria-live="polite"><template v-if="!hasLoaded"><div aria-hidden="true">…</div><strong>正在读取文件列表</strong><p>请稍候</p></template><template v-else><div>▱</div><strong>{{ search.trim()?'没有匹配的文件或文件夹':'这个文件夹还是空的' }}</strong><p>将其他位置的文件移动到这里，或拖入文件上传</p></template></div>
    <p v-if="loadError" class="file-meta" role="alert">{{ loadError }}</p>
    <div v-if="uploadVisible" class="space-upload" :class="{complete:uploadComplete}" aria-live="polite"><span>{{ uploadLabel }}</span><progress max="100" :value="uploadProgress ?? undefined" aria-label="上传进度"/></div>
    <div class="upload-drop-message" aria-hidden="true">松开文件或文件夹，上传到当前文件夹</div>
    <p class="explorer-hint"><span>拖入文件或文件夹上传到当前文件夹 · 拖动已共享内容到文件夹即可移动</span><span id="entrySummary" aria-live="polite">{{ summary }}</span></p>
  </section>
  <Teleport to="body">
    <dialog v-if="editOpen" ref="editDialog" class="app-dialog fs-dialog" aria-labelledby="editTitle" @close="editOpen=false" @cancel.prevent="editDialog?.close()">
      <form @submit.prevent="submitEdit"><h2 id="editTitle">{{ editTitle }}</h2><label for="entryName">名称</label><input id="entryName" ref="editName" v-model="editValue" required maxlength="200" autocomplete="off" aria-describedby="entryExtension"><p id="entryExtension" class="file-meta" :class="{hidden:!editExtension}">{{ editExtension?`后缀名 ${editExtension} 将自动保留`:'' }}</p><p class="dialog-error" role="alert">{{ editError }}</p><div class="dialog-actions"><button class="secondary" type="button" @click="editDialog?.close()">取消</button><button class="primary" type="submit" :disabled="mutationPending">保存</button></div></form>
    </dialog>
    <dialog v-if="moveOpen" ref="moveDialog" class="app-dialog fs-dialog" aria-labelledby="moveTitle" @close="moveOpen=false" @cancel.prevent="moveDialog?.close()">
      <h2 id="moveTitle">移动到文件夹</h2><p class="file-meta">将“{{ moving?.name }}”移动到：</p><nav class="breadcrumbs" aria-label="目标文件夹路径"><template v-for="(crumb,index) in moveBreadcrumbs" :key="crumb.path"><span v-if="index">/</span><button class="crumb" type="button" @click="loadMoveFolders(crumb.path)">{{ crumb.name }}</button></template></nav><div class="move-folders"><p v-if="moveLoading">正在读取…</p><template v-else><button v-for="folder in moveFolders" :key="folder.path" class="folder-choice" type="button" @click="loadMoveFolders(folder.path)">▱ {{ folder.name }} ›</button><p v-if="!moveFolders.length" class="file-meta">没有可继续进入的子文件夹</p></template></div><p class="dialog-error" role="alert">{{ moveError }}</p><div class="dialog-actions"><button class="secondary" type="button" @click="moveDialog?.close()">取消</button><button class="primary" type="button" :disabled="moveDisabled" @click="confirmMove">移动到这里</button></div>
    </dialog>
  </Teleport>
</template>
