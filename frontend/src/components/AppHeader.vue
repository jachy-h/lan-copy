<script setup lang="ts">
import SharePopover from './SharePopover.vue'
import SettingsDialog from './SettingsDialog.vue'
import ServiceStatus from './ServiceStatus.vue'
defineProps<{ page: 'files' | 'transfer' | 'print'; filesHref: string }>()
const emit = defineEmits<{ navigate: [event: MouseEvent, page: 'files' | 'transfer' | 'print'] }>()
</script>

<template>
  <header class="topbar">
    <div class="brand"><img class="logo" :src="'/logo/lan-copy-logo.svg'" width="30" height="30" alt=""><div><h1>Lan Copy</h1></div></div>
    <nav class="page-nav" aria-label="页面导航">
      <a :href="filesHref" :aria-current="page === 'files' ? 'page' : undefined" @click="emit('navigate', $event, 'files')">共享空间</a>
      <a href="/transfer-text" :aria-current="page === 'transfer' ? 'page' : undefined" @click="emit('navigate', $event, 'transfer')">文本快传</a>
    </nav>
    <div class="hero-actions">
      <a class="share-access-button print-access-button" href="/print-queue" aria-label="打印队列" title="打印队列" :aria-current="page === 'print' ? 'page' : undefined" @click="emit('navigate', $event, 'print')"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M7 8V3h10v5M7 17H4V8h16v9h-3"/><path d="M7 14h10v7H7zM17 11h.01"/></svg></a>
      <SharePopover /><SettingsDialog /><ServiceStatus />
    </div>
  </header>
</template>
