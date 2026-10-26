<script setup lang="ts">
import { ref } from 'vue'
import AppDialog from './AppDialog.vue'
const props = defineProps<{ open: boolean; title: string; value: string; extension: string; error: string; pending: boolean }>()
const emit = defineEmits<{ 'update:open': [value: boolean]; 'update:value': [value: string]; submit: [] }>()
const nameInput = ref<HTMLInputElement | null>(null)
function focusName() { nameInput.value?.focus(); nameInput.value?.select() }
</script>

<template>
  <AppDialog :open="open" dialog-class="fs-dialog" labelledby="editTitle" @update:open="emit('update:open',$event)" @opened="focusName">
    <form @submit.prevent="emit('submit')"><h2 id="editTitle">{{ title }}</h2><label for="entryName">名称</label><input id="entryName" ref="nameInput" :value="value" required maxlength="200" autocomplete="off" aria-describedby="entryExtension" @input="emit('update:value',($event.target as HTMLInputElement).value)"><p id="entryExtension" class="file-meta" :class="{hidden:!extension}">{{ extension?`后缀名 ${extension} 将自动保留`:'' }}</p><p class="dialog-error" role="alert">{{ error }}</p><div class="dialog-actions"><button class="secondary" type="button" @click="emit('update:open',false)">取消</button><button class="primary" type="submit" :disabled="pending">保存</button></div></form>
  </AppDialog>
</template>
