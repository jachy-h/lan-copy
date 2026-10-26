<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { usePopover } from '../composables/usePopover'
const { open, pinned, setOpen, onPointerEnter, onPointerLeave, onFocusOut } = usePopover('statusAccess', 'serviceStatus', positionPanel)
const offline = ref(false)
const closed = ref(false)
const updatedAt = ref('尚未成功更新')
const updatedDateTime = ref('')
const label = () => closed.value ? '服务已关闭' : offline.value ? '连接异常' : '局域网在线'
const hint = () => closed.value ? '服务已关闭，已停止自动更新' : offline.value ? '实时连接异常，页面显示时每分钟重试' : '文件和文本变化时实时更新'
function onUpdated() { if (closed.value) return; const now = new Date(); updatedDateTime.value = now.toISOString(); updatedAt.value = now.toLocaleString('zh-CN', { year:'numeric',month:'2-digit',day:'2-digit',hour:'2-digit',minute:'2-digit',second:'2-digit',hourCycle:'h23' }); offline.value = false }
function onFailed() { if (!closed.value) offline.value = true }
function onShutdown() { closed.value = true; offline.value = true }
function positionPanel() { const button=document.querySelector<HTMLElement>('#serviceStatus'),panel=document.querySelector<HTMLElement>('#serviceStatusPopover'); if (!button||!panel) return; const rect=button.getBoundingClientRect(); panel.style.left=`${Math.max(16,Math.min(rect.right-panel.offsetWidth,innerWidth-panel.offsetWidth-16))}px`; panel.style.top=`${rect.bottom+10}px` }
onMounted(() => { window.addEventListener('lan-copy:data-updated', onUpdated); window.addEventListener('lan-copy:data-failed', onFailed); window.addEventListener('lan-copy:shutdown', onShutdown) })
onBeforeUnmount(() => { window.removeEventListener('lan-copy:data-updated', onUpdated); window.removeEventListener('lan-copy:data-failed', onFailed); window.removeEventListener('lan-copy:shutdown', onShutdown) })
</script>

<template>
  <div id="statusAccess" class="status-access" @pointerenter="onPointerEnter" @pointerleave="onPointerLeave" @focusin="setOpen(true)" @focusout="onFocusOut">
    <button class="status" id="serviceStatus" type="button" :class="{offline}" :aria-label="label()" :aria-expanded="open" aria-describedby="serviceStatusPopover" @click="pinned=!pinned;setOpen(pinned)"><span class="status-dot"></span><span class="status-label">{{ label() }}</span></button>
    <div class="status-popover" id="serviceStatusPopover" role="tooltip" :hidden="!open"><span class="status-popover-label">数据更新时间</span><time id="dataUpdatedAt" :datetime="updatedDateTime">{{ updatedAt }}</time><span class="status-refresh-hint" id="dataRefreshHint">{{ hint() }}</span></div>
  </div>
</template>
