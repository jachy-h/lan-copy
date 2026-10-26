<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import AppHeader from './components/AppHeader.vue'
import AppFooter from './components/AppFooter.vue'
import FileExplorer from './components/FileExplorer.vue'
import TextTransfer from './components/TextTransfer.vue'
import FilePreviewDialog from './components/FilePreviewDialog.vue'
import { close as closeContextMenu } from './ui/contextMenu'

const page = ref<'files' | 'transfer'>(location.pathname === '/transfer-text' ? 'transfer' : 'files')
const filesHref = ref(page.value === 'files' ? location.pathname + location.search + location.hash : '/')
const draft = ref('')
let shutdown = false
function onShutdown() { shutdown = true }
function updateTitle() { document.title = `${page.value === 'files' ? '共享空间' : '文本快传'} · Lan Copy` }
async function syncRoute() {
  closeContextMenu(false)
  page.value = location.pathname === '/transfer-text' ? 'transfer' : 'files'
  if (page.value === 'files') filesHref.value = location.pathname + location.search + location.hash
  updateTitle()
  await nextTick()
  if (shutdown) window.dispatchEvent(new Event('lan-copy:shutdown'))
}
function navigate(event: MouseEvent, target: 'files' | 'transfer') {
  if (event.button !== 0 || event.ctrlKey || event.metaKey || event.shiftKey || event.altKey) return
  event.preventDefault()
  if (page.value === target) return
  if (page.value === 'files') filesHref.value = location.pathname + location.search + location.hash
  history.pushState(null, '', target === 'files' ? filesHref.value : '/transfer-text')
  void syncRoute()
}
onMounted(() => { updateTitle(); window.addEventListener('popstate', syncRoute); window.addEventListener('lan-copy:shutdown', onShutdown) })
onBeforeUnmount(() => { window.removeEventListener('popstate', syncRoute); window.removeEventListener('lan-copy:shutdown', onShutdown) })
</script>

<template>
  <AppHeader :page="page" :files-href="filesHref" @navigate="navigate" />
  <FilePreviewDialog />
  <main class="shell">
    <FileExplorer v-if="page === 'files'" />
    <TextTransfer v-else v-model="draft" />
    <AppFooter />
  </main>
  <div id="fileMenu" class="file-menu" role="menu" hidden></div>
  <div class="toast" id="toast" role="status"></div>
</template>
