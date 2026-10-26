export interface ConfirmOptions { title?: string; message: string; confirmText?: string; danger?: boolean }
export interface ConfirmTextOptions extends ConfirmOptions { requiredText: string }
let queue: Promise<unknown> = Promise.resolve()

function enqueue<T>(show: () => Promise<T>): Promise<T> {
  const result = queue.then(show)
  queue = result.catch(() => undefined)
  return result
}
function createDialog(title: string, message: string) {
  const previousFocus = document.activeElement as HTMLElement | null
  const dialog = document.createElement('dialog')
  dialog.className = 'app-dialog confirm-dialog'
  dialog.setAttribute('aria-labelledby', 'confirmTitle')
  dialog.setAttribute('aria-describedby', 'confirmMessage')
  return { dialog, previousFocus }
}
function blockDrop(dialog: HTMLDialogElement) {
  for (const name of ['dragenter', 'dragover', 'dragleave', 'drop']) dialog.addEventListener(name, event => { event.preventDefault(); event.stopPropagation() })
}

export function confirm({ title = '确认操作', message, confirmText = '确定', danger = false }: ConfirmOptions): Promise<boolean> {
  return enqueue(() => new Promise(resolve => {
    const { dialog, previousFocus } = createDialog(title, message)
    dialog.innerHTML = '<h2 id="confirmTitle"></h2><p id="confirmMessage" class="confirm-message"></p><div class="dialog-actions"><button class="secondary" type="button" autofocus>取消</button><button class="primary" type="button"></button></div>'
    dialog.querySelector('h2')!.textContent = title
    dialog.querySelector('p')!.textContent = message
    const [cancel, accept] = dialog.querySelectorAll('button')
    accept.textContent = confirmText; accept.classList.toggle('danger', danger)
    let accepted = false
    cancel.addEventListener('click', () => dialog.close())
    accept.addEventListener('click', () => { accepted = true; dialog.close() })
    dialog.addEventListener('close', () => { dialog.remove(); if (previousFocus?.isConnected) previousFocus.focus(); resolve(accepted) }, { once: true })
    blockDrop(dialog); document.body.append(dialog); dialog.showModal()
  }))
}

export function confirmText({ title = '需要输入确认信息', message, requiredText, confirmText = '确认', danger = true }: ConfirmTextOptions): Promise<boolean> {
  return enqueue(() => new Promise(resolve => {
    const { dialog, previousFocus } = createDialog(title, message)
    dialog.innerHTML = '<h2 id="confirmTitle"></h2><p id="confirmMessage" class="confirm-message"></p><label class="confirm-input-label">请输入 <strong></strong><input class="confirm-input" type="text" autocomplete="off" spellcheck="false" /></label><div class="dialog-actions"><button class="secondary" type="button" autofocus>取消</button><button class="primary" type="button" disabled></button></div>'
    dialog.querySelector('h2')!.textContent = title; dialog.querySelector('p')!.textContent = message
    dialog.querySelector('strong')!.textContent = requiredText
    const input = dialog.querySelector<HTMLInputElement>('input')!
    input.setAttribute('aria-label', `请输入${requiredText}`)
    const [cancel, accept] = dialog.querySelectorAll('button')
    accept.textContent = confirmText; accept.classList.toggle('danger', danger)
    input.addEventListener('input', () => { accept.disabled = input.value !== requiredText })
    let accepted = false
    cancel.addEventListener('click', () => dialog.close())
    accept.addEventListener('click', () => { if (input.value !== requiredText) return; accepted = true; dialog.close() })
    dialog.addEventListener('close', () => { dialog.remove(); if (previousFocus?.isConnected) previousFocus.focus(); resolve(accepted) }, { once: true })
    blockDrop(dialog); document.body.append(dialog); dialog.showModal(); input.focus()
  }))
}
