<script setup lang="ts">
defineProps<{ label: string; progress: number | null; complete?: boolean }>()
</script>

<template>
  <div class="space-upload" :class="{ complete }" aria-live="polite"><span>{{ label }}</span><progress max="100" :value="progress ?? undefined" aria-label="上传进度" /></div>
</template>
