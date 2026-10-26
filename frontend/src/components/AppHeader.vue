<script setup lang="ts">
import SharePopover from './SharePopover.vue'
import SettingsDialog from './SettingsDialog.vue'
import ServiceStatus from './ServiceStatus.vue'
defineProps<{ page: 'files' | 'transfer'; filesHref: string }>()
const emit = defineEmits<{ navigate: [event: MouseEvent, page: 'files' | 'transfer'] }>()
</script>

<template>
  <header class="topbar">
    <div class="brand"><img class="logo" :src="'/logo/lan-copy-logo.svg'" width="30" height="30" alt=""><div><h1>Lan Copy</h1></div></div>
    <nav class="page-nav" aria-label="页面导航">
      <a :href="filesHref" :aria-current="page === 'files' ? 'page' : undefined" @click="emit('navigate', $event, 'files')">共享空间</a>
      <a href="/transfer-text" :aria-current="page === 'transfer' ? 'page' : undefined" @click="emit('navigate', $event, 'transfer')">文本快传</a>
    </nav>
    <div class="hero-actions"><SharePopover /><SettingsDialog /><ServiceStatus /></div>
  </header>
</template>
