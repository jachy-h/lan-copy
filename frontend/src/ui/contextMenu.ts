export interface MenuItem { label?: string; danger?: boolean; disabled?: boolean; separator?: boolean; action?: () => void | Promise<void> }
type Provider = { selector: string; resolve: (event: MouseEvent, row: Element) => MenuItem[] }
const ignored = "input,textarea,select,[contenteditable]:not([contenteditable='false']),a,dialog,iframe,video,audio,canvas"
let menu: HTMLElement | null = null
let returnFocus: Element | null = null
let providers: Provider[] = []
let pageProvider: (() => MenuItem[]) | null = null
let listenersInstalled = false

function ensureMenu() { if (!menu?.isConnected) menu = document.querySelector<HTMLElement>('#fileMenu'); return menu }
export function close(restore = true) {
  const node = ensureMenu()
  if (!node || node.hidden) return
  node.hidden = true
  if (restore && returnFocus?.isConnected && !returnFocus.matches('body')) (returnFocus as HTMLElement).focus()
}
export function open(items: MenuItem[], x: number, y: number, trigger: Element | null) {
  const node = ensureMenu()
  if (!node || !items?.length) return
  close(false); returnFocus = trigger; node.replaceChildren()
  for (const item of items) {
    if (item.separator) { const separator = document.createElement('div'); separator.className = 'context-menu-separator'; separator.setAttribute('role', 'separator'); node.append(separator); continue }
    const button = document.createElement('button'); button.type = 'button'; button.className = 'file-menu-item'; button.setAttribute('role', 'menuitem'); button.textContent = item.label || ''; button.disabled = Boolean(item.disabled)
    if (item.danger) button.classList.add('danger')
    button.addEventListener('click', () => { close(true); void item.action?.() })
    node.append(button)
  }
  node.hidden = false; node.style.left = '0px'; node.style.top = '0px'
  const rect = node.getBoundingClientRect()
  node.style.left = `${Math.max(8, Math.min(x, innerWidth - rect.width - 8))}px`
  node.style.top = `${Math.max(8, Math.min(y, innerHeight - rect.height - 8))}px`
  node.querySelector<HTMLButtonElement>('button:not(:disabled)')?.focus()
}
export function register(selector: string, resolve: Provider['resolve']) {
  const provider = { selector, resolve }
  providers = [...providers.filter(item => item.selector !== selector), provider]
  return () => { providers = providers.filter(item => item !== provider) }
}
export function registerPage(resolve: () => MenuItem[]) { pageProvider = resolve; return () => { if (pageProvider === resolve) pageProvider = null } }
function install() {
  if (listenersInstalled || typeof document === 'undefined') return
  listenersInstalled = true
  document.addEventListener('contextmenu', event => {
    const target = event.target
    if (!(target instanceof Element) || getSelection()?.toString()) return
    for (const provider of providers) {
      const row = target.closest(provider.selector)
      if (!row) continue
      const items = provider.resolve(event, row)
      if (items?.length) { event.preventDefault(); open(items, event.clientX, event.clientY, row) }
      return
    }
    if (target.closest(ignored) || target.closest('#fileMenu') || !pageProvider) return
    const items = pageProvider()
    if (items.length) { event.preventDefault(); open(items, event.clientX, event.clientY, null) }
  })
  document.addEventListener('click', event => { if (event.target instanceof Element && !event.target.closest('#fileMenu')) close(false) })
  document.addEventListener('keydown', event => {
    const node = ensureMenu(); if (!node || node.hidden) return
    const items = [...node.querySelectorAll<HTMLButtonElement>('button:not(:disabled)')], active = items.indexOf(document.activeElement as HTMLButtonElement)
    if (event.key === 'Escape') { event.preventDefault(); close(true) }
    else if (event.key === 'ArrowDown' || event.key === 'ArrowUp') { event.preventDefault(); items[(active + (event.key === 'ArrowDown' ? 1 : items.length - 1)) % items.length]?.focus() }
    else if (event.key === 'Home') { event.preventDefault(); items[0]?.focus() }
    else if (event.key === 'End') { event.preventDefault(); items.at(-1)?.focus() }
  })
  window.addEventListener('scroll', () => close(false), true); window.addEventListener('blur', () => close(false)); window.addEventListener('lan-copy:data-updated', () => close(false)); window.addEventListener('lan-copy:context-menu-close', () => close(false))
}
install()
