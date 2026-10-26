import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { close, open, register, registerPage } from './contextMenu'

beforeEach(() => { document.body.innerHTML = '<div id="fileMenu" role="menu" hidden></div><button id="row">row</button><input id="input">' })
afterEach(() => { close(false); document.body.replaceChildren() })

describe('context menu', () => {
  it('opens the matching row menu, supports keyboard close, and restores focus', () => {
    const action = vi.fn(), row = document.querySelector<HTMLButtonElement>('#row')!
    register('#row', () => [{ label: 'Action', action }])
    row.focus(); row.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: 20, clientY: 20 }))
    const menu = document.querySelector<HTMLElement>('#fileMenu')!
    expect(menu.hidden).toBe(false)
    expect(document.activeElement?.textContent).toBe('Action')
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }))
    expect(menu.hidden).toBe(true)
    expect(document.activeElement).toBe(row)
  })

  it('disposes stale providers without removing a newer registration', () => {
    const old = vi.fn(() => [{ label: 'Old' }])
    const current = vi.fn(() => [{ label: 'Current' }])
    const disposeOld = register('#row', old)
    const disposeCurrent = register('#row', current)
    const disposePage = registerPage(() => [])
    disposeOld()
    document.querySelector('#row')!.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true }))
    expect(current).toHaveBeenCalledOnce()
    expect(old).not.toHaveBeenCalled()
    close(false); disposeCurrent(); disposePage()
    const event = new MouseEvent('contextmenu', { bubbles: true, cancelable: true })
    document.querySelector('#row')!.dispatchEvent(event)
    expect(event.defaultPrevented).toBe(false)
  })

  it('leaves native input menus alone and clamps explicit menus to the viewport', () => {
    const resolver = vi.fn(() => [{ label: 'Page' }])
    registerPage(resolver)
    const input = document.querySelector<HTMLInputElement>('#input')!
    const event = new MouseEvent('contextmenu', { bubbles: true, cancelable: true })
    input.dispatchEvent(event)
    expect(event.defaultPrevented).toBe(false)
    expect(resolver).not.toHaveBeenCalled()
    const menu = document.querySelector<HTMLElement>('#fileMenu')!
    menu.getBoundingClientRect = () => ({ width: 100, height: 50 } as DOMRect)
    open([{ label: 'Page' }], 9999, 9999, null)
    expect(menu.style.left).toBe(`${Math.max(8, window.innerWidth - 108)}px`)
    expect(menu.style.top).toBe(`${Math.max(8, window.innerHeight - 58)}px`)
  })
})
