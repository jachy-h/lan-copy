<script setup lang="ts">
import { computed } from 'vue'
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
</script>

<template>
  <header class="topbar">
    <div class="brand"><img class="logo" :src="'/logo/lan-copy-logo.svg'" width="30" height="30" alt=""><div><h1>Lan Copy</h1></div></div>
    <nav class="page-nav" aria-label="页面导航">
      <a :href="filesHref" :aria-current="page === 'files' ? 'page' : undefined" @click="emit('navigate', $event, 'files')">共享空间</a>
      <a href="/transfer-text" :aria-current="page === 'transfer' ? 'page' : undefined" @click="emit('navigate', $event, 'transfer')">文本快传</a>
    </nav>
    <div class="hero-actions">
      <button v-if="transferVisible" class="share-access-button transfer-access-button" :class="[uploadProgress.phase, { active: transferActive }]" type="button" :style="{ '--transfer-progress': `${uploadProgress.progress ?? 0}%` }" :aria-label="transferLabel" :title="transferLabel" @click="openUpload"><span class="transfer-button-ring" aria-hidden="true"></span><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 16V5"/><path d="m7 10 5-5 5 5"/><path d="M6 19h12"/></svg><span v-if="uploadProgress.phase === 'complete'" class="transfer-button-dot" aria-hidden="true"></span><span v-else-if="uploadProgress.phase === 'error'" class="transfer-button-dot error" aria-hidden="true"></span></button>
      <a class="share-access-button print-access-button" href="/print-queue" aria-label="打印队列" title="打印队列" :aria-current="page === 'print' ? 'page' : undefined" @click="emit('navigate', $event, 'print')"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M7 8V3h10v5M7 17H4V8h16v9h-3"/><path d="M7 14h10v7H7zM17 11h.01"/></svg></a>
      <SharePopover /><SettingsDialog /><ServiceStatus />
    </div>
  </header>
</template>
