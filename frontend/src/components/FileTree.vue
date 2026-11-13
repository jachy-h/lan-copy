<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { requestJSON } from '../api/client'

interface Entry { name: string; path: string; directory: boolean }
interface TreeRow { key: string; label: string; path: string; depth: number; directory: boolean; status?: 'loading' | 'error' | 'empty'; error?: string }
const props = defineProps<{ activePath: string | null; directoriesOnly?: boolean; label?: string }>()
const emit = defineEmits<{ navigate: [path: string]; open: [path: string, trigger: HTMLElement] }>()

const entries = ref(new Map<string, Entry[]>())
const expanded = ref(new Set<string>(['']))
const loading = ref(new Set<string>())
const errors = ref(new Map<string, string>())
const versions = new Map<string, number>()
const controllers = new Map<string, AbortController>()
let stopped = false

const rows = computed<TreeRow[]>(() => {
  const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: 'base' })
  const result: TreeRow[] = [{ key: 'tree-root', label: '全部文件', path: '', depth: 0, directory: true }]
  function append(path: string, depth: number) {
    if (!expanded.value.has(path)) return
    if (loading.value.has(path)) {
      result.push({ key: `${path}::loading`, label: '正在读取…', path, depth, directory: true, status: 'loading' })
      return
    }
    const error = errors.value.get(path)
    if (error) {
      result.push({ key: `${path}::error`, label: error, path, depth, directory: true, status: 'error', error })
      return
    }
    const items = [...(entries.value.get(path) || [])]
      .filter(item => item.directory || !props.directoriesOnly)
      .sort((a, b) => a.directory !== b.directory ? (a.directory ? -1 : 1) : collator.compare(a.name, b.name))
    if (!items.length) {
      result.push({ key: `${path}::empty`, label: '空文件夹', path, depth, directory: true, status: 'empty' })
      return
    }
    for (const item of items) {
      result.push({ key: item.path, label: item.name, path: item.path, depth, directory: item.directory })
      if (item.directory) append(item.path, depth + 1)
    }
  }
  append('', 1)
  return result
})
function message(error: unknown, fallback: string) { return error instanceof Error ? error.message : fallback }
function setLoading(path: string, value: boolean) {
  const next = new Set(loading.value)
  if (value) next.add(path); else next.delete(path)
  loading.value = next
}
function setError(path: string, error = '') {
  const next = new Map(errors.value)
  if (error) next.set(path, error); else next.delete(path)
  errors.value = next
}
async function loadDirectory(path: string, force = false) {
  if (stopped || (!force && entries.value.has(path))) return
  controllers.get(path)?.abort()
  const controller = new AbortController(); controllers.set(path, controller)
  const version = (versions.get(path) || 0) + 1
  versions.set(path, version)
  setLoading(path, true); setError(path)
  try {
    const result = await requestJSON<{ entries?: Entry[] }>(`/api/filesystem?${new URLSearchParams({ path })}`, { cache: 'no-store', signal: controller.signal })
    if (stopped || version !== versions.get(path) || controller.signal.aborted) return
    entries.value = new Map(entries.value).set(path, result.entries || [])
  } catch (error) {
    if (stopped || version !== versions.get(path) || controller.signal.aborted) return
    setError(path, message(error, '读取失败'))
  } finally {
    if (version === versions.get(path)) setLoading(path, false)
    if (controllers.get(path) === controller) controllers.delete(path)
  }
}
async function reveal(path: string) {
  const parts = path ? path.split('/') : []
  const directories = ['', ...parts.map((_, index) => parts.slice(0, index + 1).join('/'))]
  const next = new Set(expanded.value)
  directories.forEach(directory => next.add(directory))
  expanded.value = next
  await Promise.all(directories.filter(directory => !entries.value.has(directory)).map(directory => loadDirectory(directory)))
}
async function refresh() {
  const paths = new Set(['', ...[...expanded.value].filter(path => entries.value.has(path))])
  await Promise.all([...paths].map(path => loadDirectory(path, true)))
}
function toggle(path: string) {
  const next = new Set(expanded.value)
  if (next.has(path)) next.delete(path)
  else { next.add(path); void loadDirectory(path) }
  expanded.value = next
}
function activate(row: TreeRow, trigger: HTMLElement) {
  if (row.status) {
    if (row.status === 'error') void loadDirectory(row.path, true)
    else if (row.status === 'empty') toggle(row.path)
    return
  }
  if (!row.directory) { emit('open', row.path, trigger); return }
  if (props.activePath === row.path) toggle(row.path)
  else emit('navigate', row.path)
}
function isActive(row: TreeRow) { return !row.status && row.directory && props.activePath !== null && row.path === props.activePath }

watch(() => props.activePath, value => {
  if (value === null) { void loadDirectory(''); return }
  void reveal(value)
}, { immediate: true })
onBeforeUnmount(() => {
  stopped = true
  controllers.forEach(controller => controller.abort())
  controllers.clear()
})
defineExpose({ refresh, reveal })
</script>

<template>
  <div class="explorer-tree file-tree" role="tree" :aria-label="label || '内网空间目录树'">
    <div v-for="row in rows" :key="row.key" class="tree-row" :class="{ active: isActive(row), 'tree-row-status': Boolean(row.status), 'tree-row-file': !row.directory, 'tree-row-error': row.status === 'error' }" role="treeitem" :aria-level="row.depth + 1" :aria-expanded="row.directory && !row.status ? expanded.has(row.path) : undefined" :aria-selected="isActive(row)" :style="{ paddingLeft: `${8 + row.depth * 14}px` }">
      <button v-if="row.directory && !row.status" class="tree-twisty" type="button" :aria-label="`${expanded.has(row.path) ? '收起' : '展开'}目录：${row.label}`" @click.stop="toggle(row.path)">{{ expanded.has(row.path) ? '▾' : '▸' }}</button>
      <span v-else class="tree-twisty-spacer" aria-hidden="true" />
      <button v-if="row.status === 'error'" class="tree-status tree-status-error" type="button" :title="row.error" @click="activate(row, $event.currentTarget as HTMLElement)">{{ row.label }}</button>
      <span v-else-if="row.status" class="tree-status">{{ row.label }}</span>
      <button v-else class="tree-label" type="button" :title="row.path || '全部文件'" @click="activate(row, $event.currentTarget as HTMLElement)">
        <span class="tree-icon" :class="{ 'tree-folder': row.directory }" aria-hidden="true">{{ row.directory ? '▱' : (row.label.split('.').pop()?.slice(0, 3) || '文件') }}</span>
        <span class="tree-name">{{ row.label }}</span>
      </button>
    </div>
  </div>
</template>
