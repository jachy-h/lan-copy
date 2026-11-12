type ToastElement = HTMLElement & { showPopover?: () => void; hidePopover?: () => void }

let toastTimer: ReturnType<typeof setTimeout> | undefined
let toastCloseTimer: ReturnType<typeof setTimeout> | undefined
let toastVisible = false

function toastElement() { return document.querySelector<ToastElement>('#toast') }

function showToastLayer(toast: ToastElement) {
  if (typeof toast.showPopover !== 'function') { toastVisible = true; return }
  if (toastVisible) { try { toast.hidePopover?.() } catch {} }
  try {
    toast.showPopover()
    toastVisible = true
  } catch { toastVisible = false }
}

function hideToastLayer(toast: ToastElement) {
  if (!toastVisible) return
  toastVisible = false
  if (typeof toast.hidePopover === 'function') { try { toast.hidePopover() } catch {} }
}

/** Keep an active toast above a dialog that just entered the top layer. */
export function raiseToast() {
  const toast = toastElement()
  if (!toast || !toastVisible) return
  showToastLayer(toast)
}

export function notify(message: string, error = false) {
  const toast = toastElement()
  if (!toast) return
  if (toastCloseTimer) { clearTimeout(toastCloseTimer); toastCloseTimer = undefined }
  showToastLayer(toast)
  toast.textContent = message
  toast.className = `toast show${error ? ' error' : ''}`
  if (toastTimer) clearTimeout(toastTimer)
  toastTimer = setTimeout(() => {
    toast.className = 'toast'
    toastCloseTimer = setTimeout(() => {
      toastCloseTimer = undefined
      hideToastLayer(toast)
    }, 240)
  }, 3000)
}

export async function copyText(content: string) {
  try {
    if (navigator.clipboard?.writeText) { await navigator.clipboard.writeText(content); return true }
  } catch {}
  const helper = document.createElement('textarea')
  helper.value = content; helper.style.position = 'fixed'; helper.style.opacity = '0'
  document.body.append(helper); helper.focus(); helper.select()
  const copied = document.execCommand('copy'); helper.remove()
  return copied
}
