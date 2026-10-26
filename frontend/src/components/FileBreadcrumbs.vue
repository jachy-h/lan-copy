<script setup lang="ts">
defineProps<{ items: { name: string; path: string }[]; label?: string; current?: boolean }>()
const emit = defineEmits<{ navigate: [path: string]; dragover: [event: DragEvent, path: string]; dragleave: [event: DragEvent]; drop: [event: DragEvent, path: string] }>()
</script>

<template>
  <nav class="breadcrumbs" :aria-label="label || '文件夹路径'"><template v-for="(crumb, index) in items" :key="crumb.path"><span v-if="index" aria-hidden="true">/</span><button class="crumb" type="button" :aria-current="current && index===items.length-1?'location':undefined" @click="emit('navigate',crumb.path)" @dragover="emit('dragover',$event,crumb.path)" @dragleave="emit('dragleave',$event)" @drop="emit('drop',$event,crumb.path)">{{ crumb.name }}</button></template></nav>
</template>
