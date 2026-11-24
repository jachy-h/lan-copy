<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import type { PluginDialog } from '../api/plugins'

const props = defineProps<{ dialog: PluginDialog }>()
const emit = defineEmits<{
  close: []
  complete: [message: string, refresh: boolean]
}>()

const element = ref<HTMLDialogElement | null>(null)
const frame = ref<HTMLIFrameElement | null>(null)
const loaded = ref(false)
let finished = false

function close() {
  if (finished) return
  element.value?.close()
}

function onClose() {
  if (finished) return
  finished = true
  emit('close')
}

function onBackdrop(event: MouseEvent) {
  if (event.target === event.currentTarget) close()
}

function onMessage(event: MessageEvent) {
  if (event.origin !== location.origin || event.source !== frame.value?.contentWindow) return
  if (!event.data || typeof event.data !== 'object') return
  const data = event.data as { type?: unknown; message?: unknown; refresh?: unknown }
  if (data.type === 'lan-copy:plugin-dialog:close') {
    close()
    return
  }
  if (data.type === 'lan-copy:plugin-dialog:complete') {
    const message = typeof data.message === 'string' ? data.message : ''
    finished = true
    emit('complete', message, data.refresh !== false)
    element.value?.close()
  }
}

onMounted(async () => {
  window.addEventListener('message', onMessage)
  await nextTick()
  if (element.value && !element.value.open) element.value.showModal()
})

onBeforeUnmount(() => {
  window.removeEventListener('message', onMessage)
  if (element.value?.open) element.value.close()
})
</script>

<template>
  <dialog ref="element" class="plugin-app-dialog" :aria-label="dialog.title" @cancel.prevent="close" @close="onClose" @click="onBackdrop">
    <header class="plugin-app-dialog-head">
      <div class="plugin-app-dialog-copy">
        <p class="plugin-app-dialog-plugin">{{ dialog.pluginName || dialog.pluginId }}</p>
        <h2>{{ dialog.title }}</h2>
      </div>
      <button class="plugin-app-dialog-close" type="button" aria-label="关闭插件页面" title="关闭" @click="close">×</button>
    </header>
    <div class="plugin-app-dialog-frame">
      <p v-if="!loaded" class="plugin-app-dialog-loading" role="status">正在加载插件页面…</p>
      <iframe
        ref="frame"
        :src="dialog.url"
        :title="dialog.title"
        referrerpolicy="no-referrer"
        @load="loaded = true"
      ></iframe>
    </div>
  </dialog>
</template>
