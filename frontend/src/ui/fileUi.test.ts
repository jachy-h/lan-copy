import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { notify, raiseToast } from './fileUi'

let toast: HTMLElement
let show: ReturnType<typeof vi.fn>
let hide: ReturnType<typeof vi.fn>
let open: boolean

beforeEach(() => {
  vi.useFakeTimers()
  document.body.innerHTML = '<div id="toast" class="toast" role="status"></div>'
  toast = document.querySelector<HTMLElement>('#toast')!
  open = false
  show = vi.fn(() => { open = true })
  hide = vi.fn(() => { open = false })
  Object.defineProperty(toast, 'showPopover', { configurable: true, value: show })
  Object.defineProperty(toast, 'hidePopover', { configurable: true, value: hide })
})

afterEach(() => {
  vi.useRealTimers()
  document.body.replaceChildren()
})

describe('toast layer', () => {
  it('shows as a popover, raises over dialogs and closes after hiding', () => {
    notify('已保存')
    expect(show).toHaveBeenCalledOnce()
    expect(open).toBe(true)
    expect(toast.textContent).toBe('已保存')
    expect(toast.className).toBe('toast show')

    notify('已更新', true)
    expect(hide).toHaveBeenCalledOnce()
    expect(show).toHaveBeenCalledTimes(2)
    expect(toast.className).toBe('toast show error')
    expect(toast.textContent).toBe('已更新')

    vi.advanceTimersByTime(3000)
    expect(toast.className).toBe('toast')
    expect(open).toBe(true)
    vi.advanceTimersByTime(240)
    expect(hide).toHaveBeenCalledTimes(2)
    expect(open).toBe(false)
  })

  it('only re-raises a toast that is currently visible', () => {
    raiseToast()
    expect(show).not.toHaveBeenCalled()
    notify('提示')
    raiseToast()
    expect(hide).toHaveBeenCalledOnce()
    expect(show).toHaveBeenCalledTimes(2)
  })
})
