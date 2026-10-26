// A tab left open during an upgrade can request a chunk from the previous build.
// Reload once; repeated failures must remain visible instead of forming a loop.
export function recoverFromPreloadError(event: Event, reload = () => location.reload()) {
  event.preventDefault()
  const key = 'lan-copy:asset-reload'
  let recent = false
  try {
    recent = Date.now() - Number(sessionStorage.getItem(key) || 0) < 10000
    sessionStorage.setItem(key, String(Date.now()))
  } catch { recent = true }
  if (!recent) { reload(); return }
  if (document.querySelector('#asset-recovery')) return
  const alert = document.createElement('p')
  alert.id = 'asset-recovery'; alert.setAttribute('role', 'alert')
  alert.textContent = '页面加载失败，请检查连接后刷新重试。 '
  const retry = document.createElement('button')
  retry.type = 'button'; retry.className = 'secondary'; retry.textContent = '刷新页面'
  retry.addEventListener('click', reload)
  alert.append(retry); document.body.prepend(alert)
}
window.addEventListener('vite:preloadError', recoverFromPreloadError)
