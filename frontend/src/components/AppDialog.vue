<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { raiseToast } from '../ui/fileUi'
const props = withDefaults(defineProps<{ open: boolean; labelledby: string; dialogClass?: string; closeOnBackdrop?: boolean }>(), { closeOnBackdrop: true })
const emit = defineEmits<{ 'update:open': [value: boolean]; opened: [] }>()
const dialog = ref<HTMLDialogElement | null>(null)
watch(() => props.open, async open => {
  if (open) { await nextTick(); if (dialog.value && !dialog.value.open) dialog.value.showModal(); raiseToast(); emit('opened') }
  else if (dialog.value?.open) dialog.value.close()
}, { immediate: true })
function onClose() { emit('update:open', false) }
function onCancel(event: Event) { event.preventDefault(); dialog.value?.close() }
function onBackdrop(event: MouseEvent) {
  if (!props.closeOnBackdrop || event.target !== event.currentTarget) return
  emit('update:open', false)
}
onBeforeUnmount(() => { if (dialog.value?.open) dialog.value.close() })
</script>

<template>
  <dialog v-if="open" ref="dialog" class="app-dialog" :class="dialogClass" :aria-labelledby="labelledby" @close="onClose" @cancel="onCancel" @click="onBackdrop"><slot /></dialog>
</template>
