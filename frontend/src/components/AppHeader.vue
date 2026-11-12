<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { requestJSON } from '../api/client'
import { openUpload, uploadProgress } from '../upload/progress'
import SharePopover from './SharePopover.vue'
import SettingsDialog from './SettingsDialog.vue'
import ServiceStatus from './ServiceStatus.vue'
defineProps<{ page: 'files' | 'transfer' | 'print'; filesHref: string }>()
const emit = defineEmits<{ navigate: [event: MouseEvent, page: 'files' | 'transfer' | 'print'] }>()
const transferVisible = computed(() => uploadProgress.visible && !uploadProgress.open)
const transferActive = computed(() => uploadProgress.phase === 'reading' || uploadProgress.phase === 'uploading')
const transferLabel = computed(() => {
  if (transferActive.value) return uploadProgress.progress === null ? '正在准备上传，点击查看进度' : `正在上传 ${uploadProgress.progress}%，点击查看进度`
  if (uploadProgress.phase === 'complete') return '上传完成，点击查看详情'
  if (uploadProgress.phase === 'error') return '上传失败，点击查看详情'
  if (uploadProgress.phase === 'cancelled') return '上传已取消，点击查看详情'
  return '查看上传进度'
})

interface StorageUsage { bytes: number; files: number }
const usage = ref<StorageUsage | null>(null)
const usageLoading = ref(false)
const usageError = ref(false)
let usageFetchedAt = 0
let usageController: AbortController | null = null
function formatBytes(bytes: number) {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const index = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1)
  const value = bytes / 1024 ** index
  return `${value.toFixed(index === 0 || value >= 10 ? 0 : 1)} ${units[index]}`
}
const usageLabel = computed(() => usage.value ? `${formatBytes(usage.value.bytes)} · ${usage.value.files} 个文件` : usageLoading.value ? '正在计算…' : usageError.value ? '暂时无法读取' : '等待读取')
async function loadUsage(force = false) {
  if (usageLoading.value) return
  if (!force && usage.value && Date.now() - usageFetchedAt < 30000) return
  usageController?.abort()
  const controller = new AbortController(); usageController = controller
  usageLoading.value = true; usageError.value = false
  try {
    const result = await requestJSON<StorageUsage>('/api/storage', { cache: 'no-store', signal: controller.signal })
    if (controller.signal.aborted) return
    usage.value = result
    usageFetchedAt = Date.now()
  } catch {
    if (!controller.signal.aborted) usageError.value = true
  } finally {
    if (usageController === controller) usageController = null
    if (!controller.signal.aborted) usageLoading.value = false
  }
}
onMounted(() => { void loadUsage() })
onBeforeUnmount(() => { usageController?.abort() })
</script>

<template>
  <header class="topbar">
    <div class="brand" tabindex="0" aria-describedby="storageTooltip" @mouseenter="loadUsage()" @focusin="loadUsage()">
      <img class="logo" :src="'/logo/lan-copy-logo.svg'" width="30" height="30" alt=""><div><h1>Lan Copy</h1></div>
      <div id="storageTooltip" class="storage-tooltip" role="tooltip">
        <span>内网直连</span>
        <span class="storage-tooltip-usage"><span>共享文件占用</span><b>{{ usageLabel }}</b></span>
        <span class="storage-tooltip-note">已开启端到端加密</span>
      </div>
    </div>
    <nav class="page-nav" aria-label="页面导航">
      <a :href="filesHref" :aria-current="page === 'files' ? 'page' : undefined" @click="emit('navigate', $event, 'files')">内网空间</a>
      <a href="/transfer-text" :aria-current="page === 'transfer' ? 'page' : undefined" @click="emit('navigate', $event, 'transfer')">文本快传</a>
    </nav>
    <div class="hero-actions">
      <button v-if="transferVisible" class="share-access-button transfer-access-button" :class="[uploadProgress.phase, { active: transferActive }]" type="button" :style="{ '--transfer-progress': `${uploadProgress.progress ?? 0}%` }" :aria-label="transferLabel" :title="transferLabel" @click="openUpload"><span class="transfer-button-ring" aria-hidden="true"></span><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 16V5"/><path d="m7 10 5-5 5 5"/><path d="M6 19h12"/></svg><span v-if="uploadProgress.phase === 'complete'" class="transfer-button-dot" aria-hidden="true"></span><span v-else-if="uploadProgress.phase === 'error'" class="transfer-button-dot error" aria-hidden="true"></span></button>
      <a class="share-access-button print-access-button" href="/print-queue" aria-label="打印队列" title="打印队列" :aria-current="page === 'print' ? 'page' : undefined" @click="emit('navigate', $event, 'print')"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M7 8V3h10v5M7 17H4V8h16v9h-3"/><path d="M7 14h10v7H7zM17 11h.01"/></svg></a>
      <SharePopover /><SettingsDialog /><ServiceStatus />
    </div>
  </header>
</template>
