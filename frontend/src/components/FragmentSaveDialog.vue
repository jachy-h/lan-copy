<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { ApiError, requestJSON } from '../api/client'
import { notify } from '../ui/fileUi'
import AppDialog from './AppDialog.vue'
import FileBreadcrumbs from './FileBreadcrumbs.vue'
import FileTree from './FileTree.vue'

interface SaveImage { name?: string }
interface SaveItem { id: string; kind?: 'text' | 'image' | 'mixed'; name?: string; content?: string; images?: SaveImage[]; created?: string }
interface FolderEntry { name: string; path: string; directory: boolean }
interface SavedFile { path: string; name: string }
const props = defineProps<{ open: boolean; item: SaveItem | null }>()
const emit = defineEmits<{ 'update:open': [value: boolean]; saved: [path: string] }>()
const LAST_PATH_KEY = 'lan-copy:fragment-save-path'
const path = ref('')
const fileName = ref('')
const folders = ref<FolderEntry[]>([])
const loading = ref(false)
const pending = ref(false)
const loadError = ref('')
const saveError = ref('')
let controller: AbortController | null = null
let requestVersion = 0
const breadcrumbs = computed(() => {
  const parts = path.value ? path.value.split('/') : []
  return [{ name: '全部文件', path: '' }, ...parts.map((name, index) => ({ name, path: parts.slice(0, index + 1).join('/') }))]
})
const parentPath = computed(() => path.value.includes('/') ? path.value.slice(0, path.value.lastIndexOf('/')) : '')
const destination = computed(() => path.value ? `全部文件 / ${path.value.split('/').join(' / ')}` : '全部文件')
const hasText = computed(() => Boolean(props.item?.content?.trim()))
const images = computed(() => props.item?.images || [])
const hasImages = computed(() => images.value.length > 0)
const nameEditable = computed(() => hasText.value || images.value.length <= 1)
const itemLabel = computed(() => hasText.value && hasImages.value ? '这条碎片' : hasText.value || !hasImages.value ? '这段文字' : images.value.length === 1 ? '这张图片' : '这些图片')
const nameHint = computed(() => {
  if (hasText.value && hasImages.value) return '文字部分使用此文件名；图片保留原名称，同名文件会自动编号'
  if (hasImages.value) return images.value.length > 1 ? '多张图片将保留各自名称保存' : '保留原图片格式；同名文件会自动编号'
  return '保存为 UTF-8 文本；同名文件会自动编号'
})

function rememberedPath() {
  try { return localStorage.getItem(LAST_PATH_KEY) || '' } catch { return '' }
}
function rememberPath(value: string) {
  try { localStorage.setItem(LAST_PATH_KEY, value) } catch { /* storage may be unavailable in private mode */ }
}
function forgetPath() {
  try { localStorage.removeItem(LAST_PATH_KEY) } catch { /* ignore */ }
}
function defaultFileName(item: SaveItem) {
  if (!item.content?.trim() && (item.images?.length || 0) === 1) return item.images?.[0]?.name || item.name || '图片.png'
  const date = item.created ? new Date(item.created) : new Date()
  const valid = Number.isFinite(date.getTime()) ? date : new Date()
  const pad = (value: number) => String(value).padStart(2, '0')
  const stamp = `${valid.getFullYear()}${pad(valid.getMonth() + 1)}${pad(valid.getDate())}-${pad(valid.getHours())}${pad(valid.getMinutes())}`
  return `碎片-${stamp}.txt`
}
async function loadFolders(fallbackToRoot = false) {
  controller?.abort()
  const current = new AbortController(); controller = current
  const version = ++requestVersion
  loading.value = true; loadError.value = ''
  try {
    const result = await requestJSON<{ entries?: FolderEntry[] }>(`/api/filesystem?${new URLSearchParams({ path: path.value })}`, { cache: 'no-store', signal: current.signal })
    if (version !== requestVersion || current.signal.aborted) return
    folders.value = (result.entries || []).filter(entry => entry.directory)
  } catch (err) {
    if (version !== requestVersion || current.signal.aborted) return
    if (fallbackToRoot && path.value && err instanceof ApiError && (err.status === 404 || err.status === 400)) {
      path.value = ''; forgetPath()
      return loadFolders(false)
    }
    loadError.value = err instanceof Error ? err.message : '无法读取文件夹'
    folders.value = []
  } finally {
    if (version === requestVersion) loading.value = false
    if (controller === current) controller = null
  }
}
function navigate(next: string) {
  if (pending.value) return
  path.value = next
  void loadFolders()
}
function close() { if (!pending.value) emit('update:open', false) }
function clearSaveError() { saveError.value = '' }
async function save() {
  if (!props.item || pending.value || loading.value) return
  const name = nameEditable.value ? fileName.value.trim() : ''
  if (nameEditable.value && !name) { saveError.value = '请输入文件名'; return }
  if (/[\\/]/.test(name)) { saveError.value = '文件名不能包含斜杠'; return }
  pending.value = true; saveError.value = ''
  try {
    const result = await requestJSON<{ path?: string; name?: string; files?: SavedFile[] }>(`/api/texts/${encodeURIComponent(props.item.id)}/save`, {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ path: path.value, name }),
    })
    const saved = result.files?.length ? result.files : result.path ? [{ path: result.path, name: result.name || '' }] : []
    if (!saved.length) throw new Error('没有文件被保存')
    rememberPath(path.value)
    notify(saved.length === 1 ? `已保存到 ${saved[0].path}` : `已保存 ${saved.length} 个文件到 ${destination.value}`)
    emit('saved', saved[0].path)
    emit('update:open', false)
  } catch (err) { saveError.value = err instanceof Error ? err.message : '保存失败' }
  finally { pending.value = false }
}
watch(() => [props.open, props.item?.id], () => {
  if (!props.open) { controller?.abort(); return }
  path.value = rememberedPath()
  fileName.value = props.item ? defaultFileName(props.item) : ''
  folders.value = []; loadError.value = ''; saveError.value = ''
  void loadFolders(true)
}, { immediate: true })
</script>

<template>
  <AppDialog :open="open && Boolean(item)" dialog-class="fs-dialog fragment-save-dialog" labelledby="fragmentSaveTitle" :close-on-backdrop="!pending" @update:open="emit('update:open', $event)">
    <form class="fragment-save-form" @submit.prevent="save">
      <h2 id="fragmentSaveTitle">存入内网空间</h2>
      <p class="file-meta fragment-save-intro">将{{ itemLabel }}保存为普通共享文件。选择目标文件夹后，可修改文件名。</p>
      <div class="fragment-save-layout">
        <aside class="fragment-save-tree" aria-label="选择保存文件夹">
          <div class="fragment-save-tree-head">选择文件夹</div>
          <FileTree :active-path="path" directories-only label="保存位置目录树" @navigate="navigate" />
        </aside>
        <section class="fragment-save-main">
          <div class="fragment-save-path-head">
            <FileBreadcrumbs :items="breadcrumbs" label="目标文件夹路径" :current="true" @navigate="navigate" />
            <button v-if="path" class="secondary fragment-save-up" type="button" :disabled="pending" @click="navigate(parentPath)">↑ 上一级</button>
          </div>
          <div class="move-folders fragment-folder-list" :aria-busy="loading">
            <div v-if="loading" class="fragment-folder-skeleton" aria-label="正在读取文件夹"><span></span><span></span><span></span></div>
            <div v-else-if="loadError" class="fragment-save-load-error">
              <p role="alert">{{ loadError }}</p>
              <button class="secondary" type="button" @click="loadFolders()">重试</button>
            </div>
            <template v-else>
              <button v-for="folder in folders" :key="folder.path" class="folder-choice" type="button" :disabled="pending" @click="navigate(folder.path)"><span class="fragment-folder-icon" aria-hidden="true">▱</span><span>{{ folder.name }}</span><span class="fragment-folder-enter" aria-hidden="true">›</span></button>
              <p v-if="!folders.length" class="file-meta fragment-folder-empty">当前文件夹没有子文件夹，可以直接保存到这里。</p>
            </template>
          </div>
          <label v-if="nameEditable" class="fragment-save-name"><span>文件名</span><input v-model="fileName" maxlength="120" autocomplete="off" spellcheck="false" :disabled="pending" aria-describedby="fragmentSaveNameHint" @input="clearSaveError"><small id="fragmentSaveNameHint">{{ nameHint }}</small></label>
          <p v-else class="file-meta fragment-save-note">将保存 {{ images.length }} 张图片，文件名保留原名称。</p>
          <div class="fragment-save-destination"><span>保存到</span><strong>{{ destination }}</strong></div>
          <p v-if="saveError" class="dialog-error" role="alert">{{ saveError }}</p>
          <div class="dialog-actions">
            <button class="secondary" type="button" :disabled="pending" @click="close">取消</button>
            <button class="primary" type="submit" :disabled="pending || loading">{{ pending ? '正在保存…' : '保存到这里' }}</button>
          </div>
        </section>
      </div>
    </form>
  </AppDialog>
</template>
