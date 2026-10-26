<script setup lang="ts">
defineProps<{ sortKey: string; sortDirection: 'ascending' | 'descending' }>()
const emit = defineEmits<{ sort: [key: 'name' | 'modified' | 'type' | 'size'] }>()
const columns = [{ key: 'name', label: '名称' }, { key: 'modified', label: '修改日期' }, { key: 'type', label: '类型' }, { key: 'size', label: '大小' }] as const
</script>

<template>
  <div class="file-list-header" role="row"><div v-for="column in columns" :key="column.key" role="columnheader" :aria-sort="sortKey===column.key?sortDirection:'none'"><button type="button" :data-sort="column.key" @click="emit('sort',column.key)">{{ column.label }} <span>{{ sortKey===column.key?(sortDirection==='ascending'?'↑':'↓'):'' }}</span></button></div><span aria-hidden="true" /></div>
</template>
