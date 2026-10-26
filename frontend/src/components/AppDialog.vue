<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, watch } from 'vue'
const props = defineProps<{ open: boolean; labelledby: string; dialogClass?: string }>()
const emit = defineEmits<{ 'update:open': [value: boolean]; opened: [] }>()
const dialog = ref<HTMLDialogElement | null>(null)
watch(() => props.open, async open => {
  if (open) { await nextTick(); if (dialog.value && !dialog.value.open) dialog.value.showModal(); emit('opened') }
  else if (dialog.value?.open) dialog.value.close()
}, { immediate: true })
function onClose() { emit('update:open', false) }
function onCancel(event: Event) { event.preventDefault(); dialog.value?.close() }
onBeforeUnmount(() => { if (dialog.value?.open) dialog.value.close() })
</script>

<template>
  <dialog v-if="open" ref="dialog" class="app-dialog" :class="dialogClass" :aria-labelledby="labelledby" @close="onClose" @cancel="onCancel"><slot /></dialog>
</template>
