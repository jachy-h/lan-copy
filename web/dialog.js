(() => {
  // Queue confirmations so concurrent actions cannot overwrite an open question.
  let queue = Promise.resolve();
  function confirm({title = '确认操作', message, confirmText = '确定', danger = false}) {
    const result = queue.then(() => new Promise(resolve => {
      const previousFocus = document.activeElement;
      const dialog = document.createElement('dialog');
      dialog.className = 'app-dialog confirm-dialog';
      dialog.setAttribute('aria-labelledby', 'confirmTitle');
      dialog.setAttribute('aria-describedby', 'confirmMessage');
      dialog.innerHTML = `<h2 id="confirmTitle"></h2><p id="confirmMessage" class="confirm-message"></p><div class="dialog-actions"><button class="secondary" type="button" autofocus>取消</button><button class="primary" type="button"></button></div>`;
      dialog.querySelector('h2').textContent = title;
      dialog.querySelector('p').textContent = message;
      const [cancel, accept] = dialog.querySelectorAll('button');
      accept.textContent = confirmText;
      accept.classList.toggle('danger', danger);
      let accepted = false;
      cancel.addEventListener('click', () => dialog.close());
      accept.addEventListener('click', () => { accepted = true; dialog.close(); });
      dialog.addEventListener('close', () => {
        dialog.remove();
        if (previousFocus?.isConnected) previousFocus.focus();
        resolve(accepted);
      }, {once: true});
      for (const name of ['dragenter', 'dragover', 'dragleave', 'drop']) {
        dialog.addEventListener(name, event => { event.preventDefault(); event.stopPropagation(); });
      }
      document.body.append(dialog);
      dialog.showModal();
    }));
    queue = result.catch(() => {});
    return result;
  }
  window.LanCopyDialog = {confirm};
})();
