<script setup lang="ts">
import AppDialog from './AppDialog.vue'
import FileBreadcrumbs from './FileBreadcrumbs.vue'
import type { FileEntry } from './FileExplorerRow.vue'
defineProps<{ open: boolean; itemName: string; breadcrumbs: { name: string; path: string }[]; folders: FileEntry[]; loading: boolean; error: string; disabled: boolean }>()
const emit = defineEmits<{ 'update:open': [value: boolean]; navigate: [path: string]; confirm: [] }>()
</script>

<template>
  <AppDialog :open="open" dialog-class="fs-dialog" labelledby="moveTitle" @update:open="emit('update:open',$event)">
    <h2 id="moveTitle">移动到文件夹</h2><p class="file-meta">将“{{ itemName }}”移动到：</p><FileBreadcrumbs :items="breadcrumbs" label="目标文件夹路径" @navigate="emit('navigate',$event)" /><div class="move-folders"><p v-if="loading">正在读取…</p><template v-else><button v-for="folder in folders" :key="folder.path" class="folder-choice" type="button" @click="emit('navigate',folder.path)">▱ {{ folder.name }} ›</button><p v-if="!folders.length" class="file-meta">没有可继续进入的子文件夹</p></template></div><p class="dialog-error" role="alert">{{ error }}</p><div class="dialog-actions"><button class="secondary" type="button" @click="emit('update:open',false)">取消</button><button class="primary" type="button" :disabled="disabled" @click="emit('confirm')">移动到这里</button></div>
  </AppDialog>
</template>
