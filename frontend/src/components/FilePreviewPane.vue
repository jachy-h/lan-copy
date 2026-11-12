<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { mountPreview } from '../preview/engine'

const props = defineProps<{ path: string }>()
const emit = defineEmits<{ close: [] }>()
const root = ref<HTMLElement | null>(null)
let preview: { destroy(): void } | null = null
const title = computed(() => props.path.split('/').pop() || props.path)
const downloadHref = computed(() => `/files/${encodeURIComponent(props.path)}`)

onMounted(() => {
  if (root.value && props.path) preview = mountPreview(root.value, props.path)
})
onBeforeUnmount(() => { preview?.destroy(); preview = null })
</script>

<template>
  <section ref="root" class="inline-preview" aria-label="文件预览">
    <header class="preview-toolbar preview-pane-toolbar">
      <button id="previewBack" class="secondary" type="button" @click="emit('close')">← 返回列表</button>
      <h2 id="previewTitle" :title="path">{{ title }}</h2>
      <a id="previewDownload" class="secondary" :href="downloadHref" :download="title">下载原文件</a>
      <button id="previewClose" class="secondary preview-close" type="button" aria-label="关闭预览" @click="emit('close')">×</button>
    </header>
    <p id="previewStatus" class="preview-status" role="status">正在读取文件…</p>
    <div id="textTools" class="preview-text-tools hidden">
      <label for="textEncoding">文本编码</label>
      <select id="textEncoding"><option value="utf-8">UTF-8</option><option value="gb18030">GB18030 / GBK</option><option value="utf-16le">UTF-16 LE</option><option value="utf-16be">UTF-16 BE</option></select>
    </div>
    <section id="previewContent" class="preview-content" aria-label="文件内容"></section>
  </section>
</template>
