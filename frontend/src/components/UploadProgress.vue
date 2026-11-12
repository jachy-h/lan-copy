<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { dismissUpload, minimizeUpload, uploadProgress as transfer } from '../upload/progress'

const dialog = ref<HTMLElement | null>(null)
const active = computed(() => transfer.phase === 'reading' || transfer.phase === 'uploading')
const titles = { idle: '', reading: '准备上传', uploading: '正在传输文件', complete: '上传完成', error: '上传失败', cancelled: '上传已取消' } as const
const title = computed(() => titles[transfer.phase])
const percent = computed(() => transfer.progress === null ? '' : `${transfer.progress}%`)
const status = computed(() => {
  if (transfer.phase === 'reading') return '准备中'
  if (transfer.phase === 'complete') return '已刷新列表'
  if (transfer.phase === 'error') return '失败'
  if (transfer.phase === 'cancelled') return '已取消'
  return percent.value
})
const meta = computed(() => {
  if (transfer.phase === 'reading') return '正在读取文件列表，请保持页面打开'
  if (transfer.phase === 'uploading') {
    const amount = transfer.total > 0 ? `${formatBytes(transfer.loaded)} / ${formatBytes(transfer.total)}` : '正在计算文件大小…'
    return transfer.speed > 0 ? `${amount} · ${formatBytes(transfer.speed)}/s` : amount
  }
  if (transfer.phase === 'complete') return transfer.total > 0 ? `共 ${formatBytes(transfer.total)} · 文件列表已更新` : '文件列表已更新'
  if (transfer.phase === 'error') return '请检查网络或权限后重新拖入文件'
  if (transfer.phase === 'cancelled') return '原有内容已保留'
  return ''
})
function formatBytes(bytes: number) {
  if (bytes < 1024) return `${Math.round(bytes)} B`
  const unit = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), 4)
  return `${(bytes / 1024 ** unit).toFixed(1)} ${['B', 'KB', 'MB', 'GB', 'TB'][unit]}`
}
function collapse() { active.value ? minimizeUpload() : dismissUpload() }
watch(() => transfer.open, async open => {
  if (!open) return
  await nextTick()
  dialog.value?.focus()
})
</script>

<template>
  <Teleport to="body">
    <section v-if="transfer.visible && transfer.open" ref="dialog" class="transfer-dialog" :class="transfer.phase" role="dialog" aria-modal="false" aria-labelledby="transferDialogTitle" tabindex="-1" @keydown.esc.prevent.stop="collapse">
      <header class="transfer-dialog-head">
        <span class="transfer-dialog-mark" :class="transfer.phase" aria-hidden="true">
          <svg v-if="transfer.phase === 'complete'" viewBox="0 0 24 24"><path d="m5 12.5 4.2 4.2L19 7"/></svg>
          <svg v-else-if="transfer.phase === 'error'" viewBox="0 0 24 24"><path d="M12 7v6"/><path d="M12 17h.01"/></svg>
          <svg v-else-if="transfer.phase === 'cancelled'" viewBox="0 0 24 24"><path d="M7 12h10"/></svg>
          <svg v-else viewBox="0 0 24 24"><path d="M12 16V5"/><path d="m7 10 5-5 5 5"/><path d="M6 19h12"/></svg>
        </span>
        <div class="transfer-dialog-copy">
          <h2 id="transferDialogTitle" aria-live="polite">{{ title }}</h2>
          <p class="transfer-dialog-label">{{ transfer.label }}</p>
        </div>
        <button class="transfer-dialog-collapse" type="button" :aria-label="active ? '最小化上传进度' : '关闭上传进度'" :title="active ? '最小化到顶部传输按钮' : '关闭上传进度'" @click="collapse">
          <svg v-if="active" viewBox="0 0 24 24" aria-hidden="true"><path d="m6 9 6 6 6-6"/></svg>
          <svg v-else viewBox="0 0 24 24" aria-hidden="true"><path d="m6 6 12 12M18 6 6 18"/></svg>
          <span>{{ active ? '最小化' : '关闭' }}</span>
        </button>
      </header>
      <div class="transfer-progress" :class="{ indeterminate: transfer.progress === null }" role="progressbar" aria-label="上传进度" aria-valuemin="0" aria-valuemax="100" :aria-valuenow="transfer.progress ?? undefined" :aria-valuetext="status">
        <span v-if="transfer.progress === null" class="transfer-progress-indeterminate"></span>
        <span v-else class="transfer-progress-fill" :style="{ width: `${transfer.progress}%` }"></span>
      </div>
      <footer class="transfer-dialog-meta" aria-live="polite"><span>{{ meta }}</span><strong>{{ status }}</strong></footer>
    </section>
  </Teleport>
</template>
