import { nextTick, onBeforeUnmount, onMounted, ref } from 'vue'

// Shared pointer, keyboard and outside-click behavior for header popovers.
export function usePopover(rootID: string, buttonID: string, position?: () => void) {
  const open = ref(false)
  const pinned = ref(false)
  let closeTimer: ReturnType<typeof setTimeout> | undefined
  function setOpen(value: boolean) {
    if (closeTimer) clearTimeout(closeTimer)
    open.value = value
    if (value && position) void nextTick(position)
  }
  function onPointerEnter(event: PointerEvent) { if (event.pointerType !== 'touch') setOpen(true) }
  function onPointerLeave() { if (!pinned.value) closeTimer = setTimeout(() => setOpen(false), 200) }
  function onFocusOut(event: FocusEvent) {
    if (!(event.currentTarget as HTMLElement).contains(event.relatedTarget as Node)) { pinned.value = false; setOpen(false) }
  }
  function onDocumentClick(event: MouseEvent) {
    if (!(event.target instanceof Node) || !document.getElementById(rootID)?.contains(event.target)) { pinned.value = false; setOpen(false) }
  }
  function onKey(event: KeyboardEvent) {
    if (event.key === 'Escape' && open.value) { document.getElementById(buttonID)?.focus(); pinned.value = false; setOpen(false) }
  }
  function onResize() { if (open.value && position) position() }
  onMounted(() => { document.addEventListener('click', onDocumentClick); document.addEventListener('keydown', onKey); window.addEventListener('resize', onResize) })
  onBeforeUnmount(() => { document.removeEventListener('click', onDocumentClick); document.removeEventListener('keydown', onKey); window.removeEventListener('resize', onResize); if (closeTimer) clearTimeout(closeTimer) })
  return { open, pinned, setOpen, onPointerEnter, onPointerLeave, onFocusOut }
}
