<script setup lang="ts">
defineProps<{ loaded: boolean; loadingTitle: string; emptyTitle: string; emptyDescription: string; icon: string; hidden?: boolean; id?: string }>()
</script>

<template>
  <div :id="id" class="empty" :class="{ hidden }" role="status" aria-live="polite">
    <template v-if="!loaded"><div aria-hidden="true">…</div><strong>{{ loadingTitle }}</strong><p>请稍候</p></template>
    <template v-else><div aria-hidden="true">{{ icon }}</div><strong>{{ emptyTitle }}</strong><p>{{ emptyDescription }}</p></template>
  </div>
</template>
