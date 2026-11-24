<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import {
  lockAndUpdateTable, queryTable, saveTableLayout,
  type TableLayout, type TableQueryResult, type TableRow, type TableValue,
} from '../api/tables'
import { ApiError } from '../api/client'
import { confirm } from '../ui/dialog'
import { copyText, notify } from '../ui/fileUi'

const props = defineProps<{ path: string }>()
const emit = defineEmits<{ close: []; preview: [] }>()

const result = ref<TableQueryResult | null>(null)
const actionError = ref('')
const actionNotice = ref('')
const q = ref('')
const matchedQuery = ref('')
const sortColumn = ref('')
const sortOrder = ref<'asc' | 'desc'>('asc')
const pageSize = ref(50)
const offset = ref(0)
const editingRow = ref<number | null>(null)
const draft = ref<TableValue>({})
const newRow = ref<TableValue | null>(null)
const saving = ref(false)
const loading = ref(true)
const layoutOrder = ref<string[]>([])
const hiddenColumns = ref<string[]>([])
const columnPanelOpen = ref(false)
const savingLayout = ref(false)
let controller: AbortController | null = null
let loadAgain = false
let layoutDirty = false
let layoutRevision = 0
let searchTimer: ReturnType<typeof setTimeout> | undefined

const fileName = computed(() => props.path.split('/').pop() || props.path)
const downloadHref = computed(() => `/files/${encodeURIComponent(props.path)}`)
const page = computed(() => Math.floor(offset.value / pageSize.value) + 1)
const totalPages = computed(() => result.value ? Math.max(1, Math.ceil(result.value.total / pageSize.value)) : 1)
const allColumns = computed(() => result.value?.columns || [])
const orderedColumns = computed(() => {
  const seen = new Set<string>()
  const order: string[] = []
  for (const name of layoutOrder.value) {
    const actual = allColumns.value.find(column => column.toLowerCase() === name.toLowerCase())
    const key = (actual || name).toLowerCase()
    if (!actual || seen.has(key)) continue
    seen.add(key)
    order.push(actual)
  }
  for (const name of allColumns.value) {
    const key = name.toLowerCase()
    if (seen.has(key)) continue
    seen.add(key)
    order.push(name)
  }
  return order
})
const hiddenSet = computed(() => new Set(hiddenColumns.value.map(name => name.toLowerCase())))
const columns = computed(() => orderedColumns.value.filter(name => !hiddenSet.value.has(name.toLowerCase())))
const rows = computed(() => result.value?.rows || [])
const keyColumn = computed(() => {
  const tableColumns = result.value?.columns || []
  return tableColumns.find(column => column.toLowerCase() === 'id') || tableColumns[0] || ''
})
const newRowColumns = computed(() => orderedColumns.value.filter(name => !isColumnHidden(name) || name === keyColumn.value))

function message(value: unknown, fallback = '操作失败') {
  return value instanceof Error ? value.message : fallback
}

function applyLayoutToState(layout?: TableLayout) {
  layoutOrder.value = Array.isArray(layout?.order) ? [...layout!.order] : []
  hiddenColumns.value = Array.isArray(layout?.hidden) ? [...layout!.hidden] : []
}

function isColumnHidden(column: string) {
  return hiddenSet.value.has(column.toLowerCase())
}

async function persistLayout() {
  if (!result.value) return
  layoutDirty = true
  if (savingLayout.value) return
  savingLayout.value = true
  try {
    while (layoutDirty) {
      layoutDirty = false
      const order = [...orderedColumns.value]
      const hidden = [...hiddenColumns.value]
      try {
        const saved = await saveTableLayout({
          path: props.path,
          order,
          hidden,
        })
        if (!layoutDirty) applyLayoutToState(saved.layout)
      } catch (value) {
        actionError.value = message(value, '保存列偏好失败')
        break
      }
    }
  } finally {
    savingLayout.value = false
  }
}

function moveColumn(index: number, delta: number) {
  const order = [...orderedColumns.value]
  const target = index + delta
  if (target < 0 || target >= order.length) return
  ;[order[index], order[target]] = [order[target], order[index]]
  layoutOrder.value = order
  layoutRevision++
  void persistLayout()
}

function clearColumnConditions(column: string) {
  if (sortColumn.value && sortColumn.value.toLowerCase() === column.toLowerCase()) {
    sortColumn.value = ''
    sortOrder.value = 'asc'
    return true
  }
  return false
}

function setColumnVisible(column: string, visible: boolean) {
  const hidden = isColumnHidden(column)
  if (visible === !hidden) return
  if (visible) {
    hiddenColumns.value = hiddenColumns.value.filter(name => name.toLowerCase() !== column.toLowerCase())
  } else {
    hiddenColumns.value = [...hiddenColumns.value, column]
    if (clearColumnConditions(column)) {
      offset.value = 0
      void loadTable()
    }
  }
  layoutRevision++
  void persistLayout()
}

function showAllColumns() {
  if (hiddenColumns.value.length === 0) return
  hiddenColumns.value = []
  layoutRevision++
  void persistLayout()
}

async function loadTable(reset = false) {
  if (!props.path) return
  if (reset) offset.value = 0
  if (controller) {
    loadAgain = true
    return
  }
  const active = new AbortController()
  const revision = layoutRevision
  controller = active
  loading.value = true
  actionError.value = ''
  try {
    const queryText = q.value.trim()
    const queryResult = await queryTable({
      path: props.path,
      q: queryText || undefined,
      sort: sortColumn.value ? { column: sortColumn.value, order: sortOrder.value } : undefined,
      limit: pageSize.value,
      offset: offset.value,
    }, active.signal)
    if (active.signal.aborted) return
    matchedQuery.value = queryText
    result.value = queryResult
    if (revision === layoutRevision && !layoutDirty && !savingLayout.value) applyLayoutToState(queryResult.layout)
    if (offset.value >= queryResult.total && queryResult.total > 0) {
      offset.value = Math.max(0, (Math.ceil(queryResult.total / pageSize.value) - 1) * pageSize.value)
      loadAgain = true
    }
  } catch (value) {
    if (!active.signal.aborted) actionError.value = message(value, '无法读取表格')
  } finally {
    if (controller === active) {
      controller = null
      loading.value = false
      if (loadAgain) {
        loadAgain = false
        void loadTable()
      }
    }
  }
}

interface HighlightPart {
  text: string
  match: boolean
}

function highlightParts(value: string): HighlightPart[] {
  const needle = matchedQuery.value
  if (!needle || !value) return [{ text: value, match: false }]
  const escaped = needle.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  const matcher = new RegExp(escaped, 'gi')
  const parts: HighlightPart[] = []
  let last = 0
  for (const match of value.matchAll(matcher)) {
    const index = match.index ?? 0
    if (index > last) parts.push({ text: value.slice(last, index), match: false })
    parts.push({ text: match[0], match: true })
    last = index + match[0].length
  }
  if (last < value.length) parts.push({ text: value.slice(last), match: false })
  return parts.length ? parts : [{ text: value, match: false }]
}

function searchNow() {
  if (searchTimer) {
    clearTimeout(searchTimer)
    searchTimer = undefined
  }
  offset.value = 0
  void loadTable()
}

function toggleSort(column: string) {
  if (sortColumn.value === column) {
    sortOrder.value = sortOrder.value === 'asc' ? 'desc' : 'asc'
  } else {
    sortColumn.value = column
    sortOrder.value = 'asc'
  }
  offset.value = 0
  void loadTable()
}

function changePage(delta: number) {
  const next = offset.value + delta * pageSize.value
  if (next < 0 || (result.value && next >= result.value.total)) return
  offset.value = next
  editingRow.value = null
  newRow.value = null
  void loadTable()
}

function startEdit(row: TableRow) {
  if (!result.value) return
  editingRow.value = row._row
  draft.value = { ...row.values }
  newRow.value = null
  actionError.value = ''
}

function cancelEdit() {
  editingRow.value = null
  draft.value = {}
}

function startNewRow() {
  if (!result.value) return
  const values: TableValue = {}
  for (const column of orderedColumns.value) values[column] = ''
  newRow.value = values
  editingRow.value = null
  actionError.value = ''
}

async function runUpdate(operations: Parameters<typeof lockAndUpdateTable>[0]['operations'], success: string) {
  if (!result.value || saving.value) return
  saving.value = true
  actionError.value = ''
  actionNotice.value = ''
  try {
    const updated = await lockAndUpdateTable({
      path: props.path,
      expectedVersion: result.value.version,
      keyColumn: keyColumn.value,
      operations,
    })
    notify(success)
    actionNotice.value = `${success}，新版本 ${updated.version.slice(0, 19)}…`
    editingRow.value = null
    draft.value = {}
    newRow.value = null
    await loadTable()
  } catch (value) {
    if (value instanceof ApiError && value.status === 409) {
      actionError.value = `${value.message} 已重新加载，请确认后重试。`
      await loadTable()
    } else {
      actionError.value = message(value, '更新表格失败')
    }
  } finally {
    saving.value = false
  }
}

async function saveEdit(row: TableRow) {
  const changes: TableValue = {}
  for (const column of columns.value) {
    if (column !== keyColumn.value && draft.value[column] !== row.values[column]) {
      changes[column] = draft.value[column] ?? ''
    }
  }
  if (Object.keys(changes).length === 0) {
    cancelEdit()
    return
  }
  const key = row.values[keyColumn.value]
  if (!key) {
    actionError.value = `第 ${row._row} 行的主键列为空，无法更新`
    return
  }
  await runUpdate([{ kind: 'update', key, changes }], '行已更新')
}

async function saveNewRow() {
  if (!newRow.value) return
  await runUpdate([{ kind: 'insert', values: { ...newRow.value } }], '行已新增')
}

async function deleteRow(row: TableRow) {
  const key = row.values[keyColumn.value]
  if (!key) {
    actionError.value = `第 ${row._row} 行的主键列为空，无法删除`
    return
  }
  const accepted = await confirm({
    title: '删除表格行',
    message: `确定删除主键为「${key}」的行吗？此操作会立即写回 CSV 文件。`,
    confirmText: '删除',
    danger: true,
  })
  if (!accepted) return
  await runUpdate([{ kind: 'delete', key }], '行已删除')
}

function pluginExample() {
  return `curl -X POST \\
  -H "Authorization: Bearer $LAN_COPY_API_TOKEN" \\
  -H "Content-Type: application/json" \\
  -d '{"path":"${props.path}","expectedVersion":"<version>","keyColumn":"${keyColumn.value || 'id'}","operations":[{"kind":"update","key":"A-1","changes":{"status":"done"}}]}' \\
  "$LAN_COPY_API_URL/api/plugin-api/tables/lock-and-update"`
}

async function copyExample() {
  const copied = await copyText(pluginExample())
  notify(copied ? '插件调用示例已复制' : '复制失败，请检查浏览器权限', !copied)
}

watch(pageSize, () => {
  offset.value = 0
  void loadTable()
})

watch(q, () => {
  if (searchTimer) clearTimeout(searchTimer)
  searchTimer = setTimeout(() => {
    searchTimer = undefined
    offset.value = 0
    void loadTable()
  }, 300)
})

onMounted(() => {
  void loadTable()
})
onBeforeUnmount(() => {
  if (searchTimer) clearTimeout(searchTimer)
  controller?.abort()
})
</script>

<template>
  <section class="tables-page tables-inline" aria-label="智能表格">
    <header class="preview-pane-toolbar inline-table-toolbar">
      <button id="tableBack" class="secondary" type="button" aria-label="返回文件列表" @click="emit('close')">← 返回列表</button>
      <div class="inline-table-title">
        <h2 :title="path">{{ fileName }}</h2>
        <span>{{ path }}</span>
      </div>
      <div class="inline-table-actions">
        <button class="secondary" type="button" :disabled="loading" @click="loadTable()">刷新</button>
        <button class="secondary" type="button" @click="emit('preview')">预览原文件</button>
        <a class="secondary" :href="downloadHref" :download="fileName">下载</a>
        <button class="secondary inline-table-close" type="button" aria-label="关闭表格" @click="emit('close')">×</button>
      </div>
    </header>

    <p v-if="actionError" class="plugin-inline-error" role="alert">{{ actionError }}</p>
    <p v-if="actionNotice" class="plugin-event-notice" role="status">{{ actionNotice }}</p>

    <div v-if="!result && !actionError" class="plugin-empty">
      <strong>正在读取表格…</strong>
      <p>共享目录中的 CSV 会按第一行解析列名。</p>
    </div>

    <template v-else-if="result">
      <div class="tables-toolbar">
        <div class="tables-query">
          <input
            v-model="q"
            type="search"
            placeholder="搜索全部列，输入后自动搜索…"
            aria-label="搜索全部列"
            @keyup.enter="searchNow"
          >
          <span v-if="loading && q.trim()" class="tables-search-status">正在搜索…</span>
        </div>
        <div class="tables-write-actions">
          <button class="secondary" type="button" :aria-expanded="columnPanelOpen" @click="columnPanelOpen = !columnPanelOpen">列设置</button>
          <button class="secondary" type="button" :disabled="saving" @click="startNewRow">新增行</button>
        </div>
      </div>

      <div v-if="columnPanelOpen" class="tables-column-panel" role="group" aria-label="列设置">
        <div class="tables-column-panel-head">
          <div>
            <strong>列设置</strong>
            <span>调整显示顺序，取消勾选可隐藏列。隐藏只影响当前视图，不修改 CSV。</span>
          </div>
          <div class="tables-column-panel-actions">
            <button class="secondary" type="button" :disabled="hiddenColumns.length === 0" @click="showAllColumns">全部显示</button>
            <button class="secondary" type="button" @click="columnPanelOpen = false">完成</button>
          </div>
        </div>
        <ol class="tables-column-list">
          <li v-for="(column, index) in orderedColumns" :key="column" class="tables-column-row">
            <span class="tables-column-name" :title="column">{{ column }}</span>
            <span class="tables-column-order">
              <button class="secondary" type="button" :disabled="index === 0" :aria-label="`上移 ${column}`" @click="moveColumn(index, -1)">↑</button>
              <button class="secondary" type="button" :disabled="index === orderedColumns.length - 1" :aria-label="`下移 ${column}`" @click="moveColumn(index, 1)">↓</button>
            </span>
            <label class="tables-column-visible">
              <input
                type="checkbox"
                :aria-label="`显示 ${column}`"
                :checked="!isColumnHidden(column)"
                @change="setColumnVisible(column, ($event.target as HTMLInputElement).checked)"
              >
              显示
            </label>
          </li>
        </ol>
        <p class="tables-column-hint">隐藏只影响当前视图，不修改 CSV。隐藏当前排序列时会同时清除排序条件。</p>
      </div>

      <div class="tables-meta">
        <span>{{ result.total }} 行 · 第 {{ page }} / {{ totalPages }} 页</span>
        <span>版本 <code>{{ result.version.slice(0, 19) }}…</code></span>
        <span v-if="loading">正在刷新…</span>
      </div>

      <div class="tables-scroll">
        <table class="tables-grid">
          <thead>
            <tr>
              <th class="tables-row-number" scope="col">#</th>
              <th v-for="column in columns" :key="column" scope="col">
                <button type="button" class="tables-sort" @click="toggleSort(column)">
                  {{ column }}<span v-if="sortColumn === column">{{ sortOrder === 'asc' ? '↑' : '↓' }}</span>
                </button>
              </th>
              <th class="tables-row-actions" scope="col">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="row in rows" :key="`${row._row}-${row.values[keyColumn]}`">
              <td class="tables-row-number">{{ row._row }}</td>
              <td v-for="column in columns" :key="column">
                <input
                  v-if="editingRow === row._row"
                  v-model="draft[column]"
                  :aria-label="`${column} 第 ${row._row} 行`"
                  :disabled="column === keyColumn"
                >
                <span v-else class="tables-cell" :title="row.values[column]">
                  <template v-for="(part, index) in highlightParts(row.values[column] || '')" :key="index">
                    <mark v-if="part.match" class="tables-highlight">{{ part.text }}</mark>
                    <template v-else>{{ part.text }}</template>
                  </template>
                </span>
              </td>
              <td class="tables-row-actions">
                <template v-if="editingRow === row._row">
                  <button class="secondary" type="button" :disabled="saving" @click="saveEdit(row)">保存</button>
                  <button class="secondary" type="button" :disabled="saving" @click="cancelEdit">取消</button>
                </template>
                <template v-else>
                  <button class="secondary" type="button" :disabled="saving" @click="startEdit(row)">编辑</button>
                  <button class="secondary plugin-danger" type="button" :disabled="saving" @click="deleteRow(row)">删除</button>
                </template>
              </td>
            </tr>
            <tr v-if="newRow" class="tables-new-row">
              <td class="tables-row-number">新</td>
              <td v-for="column in newRowColumns" :key="column">
                <input v-model="newRow[column]" :aria-label="`新行 ${column}`" :placeholder="column">
              </td>
              <td class="tables-row-actions">
                <button class="secondary" type="button" :disabled="saving" @click="saveNewRow">新增</button>
                <button class="secondary" type="button" :disabled="saving" @click="newRow = null">取消</button>
              </td>
            </tr>
            <tr v-if="rows.length === 0 && !newRow">
              <td :colspan="columns.length + 2" class="tables-empty-cell">没有匹配的行</td>
            </tr>
          </tbody>
        </table>
      </div>

      <div class="tables-pagination">
        <label>每页
          <select v-model.number="pageSize">
            <option :value="20">20</option>
            <option :value="50">50</option>
            <option :value="100">100</option>
            <option :value="200">200</option>
          </select>
        </label>
        <button class="secondary" type="button" :disabled="offset === 0 || loading" @click="changePage(-1)">上一页</button>
        <span>第 {{ page }} / {{ totalPages }} 页</span>
        <button class="secondary" type="button" :disabled="!result || offset + pageSize >= result.total || loading" @click="changePage(1)">下一页</button>
      </div>

      <details class="tables-plugin-help">
        <summary>插件如何查询和更新这张表</summary>
        <p>插件安装时会获得 <code>LAN_COPY_API_URL</code> 和 <code>LAN_COPY_API_TOKEN</code>。表格接口只接受有效令牌，令牌仅授权表格 API。</p>
        <p>查询：<code>POST $LAN_COPY_API_URL/api/plugin-api/tables/query</code></p>
        <p>原子更新：<code>POST $LAN_COPY_API_URL/api/plugin-api/tables/lock-and-update</code>，必须携带当前 <code>version</code> 和 <code>keyColumn</code>；冲突返回 409。</p>
        <div class="tables-example">
          <pre>{{ pluginExample() }}</pre>
          <button class="secondary" type="button" @click="copyExample">复制示例</button>
        </div>
      </details>
    </template>
  </section>
</template>
