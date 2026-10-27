<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import AppHeader from './components/AppHeader.vue'
import AppFooter from './components/AppFooter.vue'
import FileExplorer from './components/FileExplorer.vue'
import TextTransfer from './components/TextTransfer.vue'
import FilePreviewDialog from './components/FilePreviewDialog.vue'
import PrintQueue from './components/PrintQueue.vue'
import { close as closeContextMenu } from './ui/contextMenu'

type Page = 'files' | 'transfer' | 'print'
function currentPage(): Page { return location.pathname === '/print-queue' || location.pathname === '/print.html' ? 'print' : location.pathname === '/transfer-text' ? 'transfer' : 'files' }
const page = ref<Page>(currentPage())
function returnFilesHref() {
  const from = new URLSearchParams(location.search).get('from') || ''
  return /^\/(?:[?#]|$)/.test(from) ? from : '/'
}
const filesHref = ref(page.value === 'files' ? location.pathname + location.search + location.hash : returnFilesHref())
const sharedPrintPath = ref(new URLSearchParams(location.search).get('file') || '')
const draft = ref('')
let shutdown = false
function onShutdown() { shutdown = true }
function updateTitle() { document.title = `${{files:'共享空间', transfer:'文本快传', print:'打印队列'}[page.value]} · Lan Copy` }
async function syncRoute() {
  closeContextMenu(false)
  page.value = currentPage()
  sharedPrintPath.value = page.value === 'print' ? new URLSearchParams(location.search).get('file') || '' : ''
  if (page.value === 'files') filesHref.value = location.pathname + location.search + location.hash
  updateTitle()
  await nextTick()
  if (shutdown) window.dispatchEvent(new Event('lan-copy:shutdown'))
}
function navigate(event: MouseEvent, target: Page) {
  if (event.button !== 0 || event.ctrlKey || event.metaKey || event.shiftKey || event.altKey) return
  event.preventDefault()
  if (page.value === target) return
  if (page.value === 'files') filesHref.value = location.pathname + location.search + location.hash
  history.pushState(null, '', target === 'files' ? filesHref.value : target === 'print' ? '/print-queue' : '/transfer-text')
  void syncRoute()
}
function printSharedFile(path: string) {
  if (page.value === 'files') filesHref.value = location.pathname + location.search + location.hash
  history.pushState(null, '', `/print-queue?${new URLSearchParams({ file: path, from: filesHref.value })}`)
  void syncRoute()
}
function clearSharedPrintFile() {
  const url = new URL(location.href)
  url.searchParams.delete('file')
  history.replaceState(null, '', url.pathname + url.search + url.hash)
  sharedPrintPath.value = ''
}
onMounted(() => { updateTitle(); window.addEventListener('popstate', syncRoute); window.addEventListener('lan-copy:shutdown', onShutdown) })
onBeforeUnmount(() => { window.removeEventListener('popstate', syncRoute); window.removeEventListener('lan-copy:shutdown', onShutdown) })
</script>

<template>
  <AppHeader :page="page" :files-href="filesHref" @navigate="navigate" />
  <FilePreviewDialog />
  <main class="shell">
    <FileExplorer v-if="page === 'files'" @print="printSharedFile" />
    <TextTransfer v-else-if="page === 'transfer'" v-model="draft" />
    <PrintQueue v-else :shared-path="sharedPrintPath" @local-file="clearSharedPrintFile" />
    <AppFooter />
  </main>
  <div id="fileMenu" class="file-menu" role="menu" hidden></div>
  <div class="toast" id="toast" role="status"></div>
</template>
