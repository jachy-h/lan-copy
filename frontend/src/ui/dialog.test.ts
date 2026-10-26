import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { confirm, confirmText } from './dialog'

const tick = () => new Promise(resolve => setTimeout(resolve, 0))

beforeEach(() => {
  Object.defineProperty(HTMLDialogElement.prototype, 'showModal', { configurable: true, value() { this.open = true } })
  Object.defineProperty(HTMLDialogElement.prototype, 'close', { configurable: true, value() { this.open = false; this.dispatchEvent(new Event('close')) } })
  document.body.innerHTML = '<button id="trigger">open</button>'
})
afterEach(() => { document.body.replaceChildren() })

describe('confirmation dialog', () => {
  it('queues concurrent prompts, resolves cancellation, and returns focus', async () => {
    const trigger = document.querySelector<HTMLButtonElement>('#trigger')!; trigger.focus()
    const first = confirm({ title: '<unsafe>', message: 'first' })
    const second = confirm({ message: 'second' })
    await tick()
    expect(document.querySelectorAll('dialog')).toHaveLength(1)
    expect(document.querySelector('dialog h2')?.textContent).toBe('<unsafe>')
    expect(document.querySelector('dialog img')).toBeNull()
    document.querySelector<HTMLButtonElement>('dialog .secondary')!.click()
    expect(await first).toBe(false)
    await tick()
    expect(document.querySelector('dialog p')?.textContent).toBe('second')
    document.querySelector<HTMLButtonElement>('dialog .primary')!.click()
    expect(await second).toBe(true)
    expect(trigger).toBe(document.activeElement)
    expect(document.querySelector('dialog')).toBeNull()
  })

  it('requires exact typed confirmation and prevents file drops into the modal', async () => {
    const answer = confirmText({ message: 'close service', requiredText: '强制关闭', confirmText: '强制关闭' })
    await tick()
    const input = document.querySelector<HTMLInputElement>('.confirm-input')!
    const accept = document.querySelector<HTMLButtonElement>('.dialog-actions .primary')!
    expect(accept.disabled).toBe(true)
    const drop = new Event('drop', { cancelable: true, bubbles: true })
    document.querySelector('dialog')!.dispatchEvent(drop)
    expect(drop.defaultPrevented).toBe(true)
    input.value = '强制关闭 '; input.dispatchEvent(new Event('input', { bubbles: true }))
    expect(accept.disabled).toBe(true)
    input.value = '强制关闭'; input.dispatchEvent(new Event('input', { bubbles: true }))
    expect(accept.disabled).toBe(false)
    accept.click()
    expect(await answer).toBe(true)
  })
})
