<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { mountPreview } from '../preview/engine'

const params = new URLSearchParams(location.search)
const path = params.get('path') || ''
const title = path.split('/').pop() || '文件预览'
const parentPath = path.includes('/') ? path.slice(0, path.lastIndexOf('/')) : ''
const backHref = params.get('from') === 'transfer' ? '/transfer-text' : `/#${new URLSearchParams({ path: parentPath })}`
const downloadHref = path ? `/files/${encodeURIComponent(path)}` : undefined
const root = ref<HTMLElement | null>(null)
let preview: { destroy(): void } | null = null
onMounted(() => {
  document.title = `${title} · Lan Copy`
  if (root.value && path) preview = mountPreview(root.value, path)
})
onBeforeUnmount(() => { preview?.destroy(); preview = null })
</script>

<template>
  <main ref="root" class="preview-shell">
    <header class="preview-toolbar">
      <a id="previewBack" class="secondary" :href="backHref">返回文件列表</a>
      <h1 id="previewTitle">{{ title }}</h1>
      <a id="previewDownload" class="secondary" :class="{hidden:!downloadHref}" :href="downloadHref" :download="title">下载原文件</a>
    </header>
    <p id="previewStatus" class="preview-status" role="status">正在读取文件…</p>
    <div id="textTools" class="preview-text-tools hidden">
      <label for="textEncoding">文本编码</label>
      <select id="textEncoding"><option value="utf-8">UTF-8</option><option value="gb18030">GB18030 / GBK</option><option value="utf-16le">UTF-16 LE</option><option value="utf-16be">UTF-16 BE</option></select>
    </div>
    <section id="previewContent" class="preview-content" aria-label="文件内容"></section>
    <p class="preview-footnote">文件通过局域网直接读取 · 预览失败时可下载原文件</p>
  </main>
</template>
