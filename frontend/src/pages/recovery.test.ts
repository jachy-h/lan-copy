import { afterEach, expect, it, vi } from 'vitest'
import { recoverFromPreloadError } from './recovery'
afterEach(() => { sessionStorage.clear(); document.querySelector('#asset-recovery')?.remove() })
it('reloads once after a missing old chunk and offers manual retry on repeated failure', () => {
  const reload = vi.fn(), first = new Event('vite:preloadError', { cancelable: true })
  recoverFromPreloadError(first, reload)
  expect(first.defaultPrevented).toBe(true)
  expect(reload).toHaveBeenCalledOnce()
  recoverFromPreloadError(new Event('vite:preloadError'), reload)
  expect(reload).toHaveBeenCalledOnce()
  expect(document.querySelector('[role="alert"]')!.textContent).toContain('刷新重试')
  document.querySelector<HTMLButtonElement>('#asset-recovery button')!.click()
  expect(reload).toHaveBeenCalledTimes(2)
})
