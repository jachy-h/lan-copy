let toastTimer: ReturnType<typeof setTimeout> | undefined

export function notify(message: string, error = false) {
  const toast = document.querySelector<HTMLElement>('#toast')
  if (!toast) return
  toast.textContent = message
  toast.className = `toast show${error ? ' error' : ''}`
  if (toastTimer) clearTimeout(toastTimer)
  toastTimer = setTimeout(() => { toast.className = 'toast' }, 3000)
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

