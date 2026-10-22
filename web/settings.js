(() => {
 const gear = document.createElement('button');
 gear.type = 'button'; gear.className = 'share-access-button'; gear.title = '设置'; gear.setAttribute('aria-label', '设置'); gear.setAttribute('aria-haspopup', 'dialog');
 gear.innerHTML = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="m9 3-.6 2.2-2 .9-2-.6-2 3.5 1.5 1.6v2.3L2.4 15l2 3.5 2.1-.6 2 .9L9 21h4l.6-2.2 2-.9 2 .6 2-3.5-1.5-1.6v-2.3L19.6 9l-2-3.5-2.1.6-2-.9L13 3Z"/><circle cx="11" cy="12" r="3"/></svg>';
 document.querySelector('.hero-actions').prepend(gear);
 const modal = document.createElement('dialog');
 modal.className = 'app-dialog settings-dialog'; modal.setAttribute('aria-labelledby', 'settingsTitle');
 modal.innerHTML = `
  <header class="settings-header"><h2 id="settingsTitle">设置</h2><button type="button" class="settings-close" aria-label="关闭设置">×</button></header>
  <div class="settings-layout">
   <nav class="settings-menu" aria-label="设置菜单" role="tablist" aria-orientation="vertical">
    <button type="button" role="tab" id="aboutTab" aria-selected="true" aria-controls="aboutSettings">关于</button>
    <button type="button" role="tab" id="generalTab" aria-selected="false" aria-controls="generalSettings" tabindex="-1">通用</button>
    <button type="button" role="tab" id="updateTab" aria-selected="false" aria-controls="updateSettings" tabindex="-1">更新</button>
   </nav>
   <div class="settings-content">
    <section id="aboutSettings" role="tabpanel" aria-labelledby="aboutTab">
     <img src="/logo/lan-copy-logo.svg" width="56" height="56" alt="" />
     <h3>Lan Copy</h3><p>一个文件，整个局域网互传。</p>
     <p class="settings-version">正在读取版本…</p>
     <a href="https://github.com/jachy-h/lan-copy" target="_blank" rel="noopener noreferrer">项目主页 ↗</a>
    </section>
    <section id="generalSettings" role="tabpanel" aria-labelledby="generalTab" hidden>
     <div class="shutdown-setting">
      <h3>关闭软件</h3>
      <p>关闭后，所有设备都将无法继续访问。共享文件和文本会保留。</p>
      <button class="secondary danger" id="shutdownBtn" type="button" disabled aria-describedby="shutdownHint">关闭 Lan Copy</button>
      <p id="shutdownHint" role="status" aria-live="polite">正在检查关闭权限…</p>
     </div>
    </section>
    <section id="updateSettings" role="tabpanel" aria-labelledby="updateTab" hidden>
     <h3>更新 Lan Copy</h3><p class="settings-version">正在读取版本…</p>
     <p class="update-explanation">拖入本机系统对应的新版发布包，校验通过后自动安装并重启。共享文件和文字会保留，请先等待其他文件传输完成。</p>
     <button class="update-dropzone" type="button" disabled><strong>拖入更新包，或点击选择</strong><span>支持 .zip / .tar.gz · 最大 128 MiB</span></button>
     <input class="update-picker" type="file" accept=".zip,.gz,.tgz" hidden />
     <p class="update-trust"></p><progress class="update-progress" max="100" value="0" hidden aria-label="更新包上传进度"></progress>
     <p class="update-status" role="status" aria-live="polite">正在检查更新条件…</p>
    </section>
   </div>
  </div>`;
 document.body.append(modal);
 const $ = selector => modal.querySelector(selector);
 const tabs = [...modal.querySelectorAll('[role="tab"]')];
 let info, busy = false, shutdownPending = false, stopped = false;
 const shutdownButton = $('#shutdownBtn'), shutdownHint = $('#shutdownHint');
 function showShutdownAvailability() {
  shutdownButton.disabled = !info?.local || busy || shutdownPending || stopped;
  shutdownHint.textContent = stopped ? '服务已关闭，可以关闭此页面。' : shutdownPending ? '正在关闭…' : !info?.local ? '仅运行 Lan Copy 的电脑可以关闭软件。' : busy ? '请等待更新完成后再关闭软件。' : '仅运行 Lan Copy 的电脑可以操作，关闭前需要确认。';
 }
 shutdownButton.addEventListener('click', async () => {
  if (!info?.local || busy || shutdownPending || stopped) return;
  shutdownPending = true;
  shutdownButton.disabled = true;
  try {
   const accepted = await window.LanCopyDialog.confirm({title: '关闭 Lan Copy？', message: '关闭后，所有设备都将无法继续访问。共享文件和文本会保留，可在此电脑上重新启动 Lan Copy。', confirmText: '关闭软件', danger: true});
   if (!accepted) return;
   shutdownHint.textContent = '正在关闭…';
   const response = await fetch('/api/shutdown', {method: 'POST', headers: {'X-Lan-Copy-Action': 'shutdown'}});
   if (!response.ok) { const result = await response.json(); throw new Error(result.error || '关闭失败，请重试'); }
   stopped = true;
   window.dispatchEvent(new Event('lan-copy:shutdown'));
   shutdownButton.textContent = '已关闭';
   zone.disabled = true;
   window.LanCopyFileUI.notify('Lan Copy 已关闭，可以关闭此页面');
  } catch (error) {
   shutdownHint.textContent = error.message || '关闭失败，请重试';
   return;
  } finally {
   shutdownPending = false;
   shutdownButton.disabled = !info?.local || busy || stopped;
   if (modal.open && !shutdownButton.disabled) shutdownButton.focus();
  }
  showShutdownAvailability();
 });
 const zone = $('.update-dropzone'), picker = $('.update-picker'), status = $('.update-status'), progress = $('.update-progress');
 const showStatus = (message, error = false) => { status.textContent = message; status.classList.toggle('error', error); };
 function selectTab(index, focus = false) {
  tabs.forEach((tab, i) => {
   tab.setAttribute('aria-selected', String(index === i)); tab.tabIndex = index === i ? 0 : -1;
   document.getElementById(tab.getAttribute('aria-controls')).hidden = i !== index;
  });
  if (focus) tabs[index].focus();
 }
 tabs.forEach((tab, i) => {
  tab.addEventListener('click', () => selectTab(i));
  tab.addEventListener('keydown', event => {
   if (['ArrowUp','ArrowDown','ArrowLeft','ArrowRight','Home','End'].includes(event.key)) {
    event.preventDefault();
    selectTab(event.key === 'Home' ? 0 : event.key === 'End' ? tabs.length - 1 : (i + (['ArrowUp','ArrowLeft'].includes(event.key) ? -1 : 1) + tabs.length) % tabs.length, true);
   }
  });
 });
 async function loadInfo() {
  try {
   const response = await fetch('/api/app', {cache:'no-store'});
   if (!response.ok) throw new Error('无法读取应用信息');
   info = await response.json();
   showShutdownAvailability();
   modal.querySelectorAll('.settings-version').forEach(el => el.textContent = `当前版本 ${info.version} · ${info.os} / ${info.arch}`);
   zone.disabled = !info.updateEnabled || busy;
   $('.update-trust').textContent = info.signedUpdates ? '所有更新包必须通过内置可信公钥的签名验证。' : '尚未配置可信更新公钥，自动更新已禁用。';
   if (!busy) showStatus(info.updateEnabled ? '更新期间服务会短暂离线，完成后此页面将自动刷新。' : info.updateDisabledReason);
  } catch (error) { info = null; showShutdownAvailability(); zone.disabled = true; showStatus(error.message, true); }
 }
 gear.addEventListener('click', () => { modal.showModal(); if (!stopped) { loadInfo(); } });
 $('.settings-close').addEventListener('click', () => modal.close());
 modal.addEventListener('click', event => { if (event.target === modal) { const r=modal.getBoundingClientRect(); if (event.clientX<r.left || event.clientX>r.right || event.clientY<r.top || event.clientY>r.bottom) modal.close(); } });
 // Consume all modal drops so the shared-space uploader never receives a package.
 for (const name of ['dragenter','dragover','dragleave','drop']) modal.addEventListener(name, event => { event.preventDefault(); event.stopPropagation(); });
 zone.addEventListener('click', () => picker.click());
 picker.addEventListener('change', () => { const files=[...picker.files]; picker.value=''; install(files); });
 zone.addEventListener('dragover', event => { if (!zone.disabled) {zone.classList.add('dragging');event.dataTransfer.dropEffect='copy';} });
 zone.addEventListener('dragleave', () => zone.classList.remove('dragging'));
 zone.addEventListener('drop', event => { zone.classList.remove('dragging'); if (!zone.disabled) install([...event.dataTransfer.files]); });
 function upload(file) {
  return new Promise((resolve,reject) => {
   const xhr=new XMLHttpRequest();xhr.open('POST','/api/update');xhr.setRequestHeader('X-Lan-Copy-Action','update');xhr.setRequestHeader('Content-Type','application/octet-stream');
   xhr.upload.onprogress = event => { if (event.lengthComputable) { progress.value=event.loaded/event.total*100;showStatus(progress.value===100 ? '正在校验更新包…' : `正在上传 ${Math.round(progress.value)}%`); } };
   xhr.onload = () => { let result;try {result=JSON.parse(xhr.responseText);} catch {reject(new Error('更新服务返回了无效响应'));return;} if (xhr.status!==202) {reject(new Error(result.error || '更新失败'));return;} resolve(result); };
   xhr.onerror = () => reject(new Error('连接中断。请稍后重新打开设置，检查当前版本与服务状态。'));
   xhr.send(file);
  });
 }
 async function waitForRestart(result) {
  const deadline=Date.now()+90000;
  while (Date.now()<deadline) {
   await new Promise(resolve => setTimeout(resolve,1000));
   try {
    const response=await fetch('/api/app', {cache:'no-store',signal:AbortSignal.timeout(2000)});
    if (!response.ok) continue;
    const current=await response.json();
    if (current.updateID===result.updateID) { showStatus(`已更新至 ${current.version}，正在刷新…`);location.reload();return true; }
    if (current.updateID===`rollback-${result.updateID}`) { showStatus('新版本启动失败，已恢复旧版本。详情请查看程序目录下的 lan-copy-update.log。',true);return false; }
   } catch { /* The service is expected to be temporarily offline. */ }
  }
  showStatus('重启未能在预期时间内完成。请查看程序目录下的 lan-copy-update.log，或手动启动程序。',true);
  return false;
 }
 async function install(files) {
  if (busy || shutdownPending || stopped || !info?.updateEnabled) return;
  if (files.length!==1 || !/\.(zip|tar\.gz|tgz)$/i.test(files[0].name)) {showStatus('请选择一个 .zip 或 .tar.gz 发布包。',true);return;}
  if (files[0].size>128*1024*1024) {showStatus('更新包不能超过 128 MiB。',true);return;}
  busy=true;showShutdownAvailability();zone.disabled=true;progress.hidden=false;progress.value=0;
  // Keep the modal open while installing, but allow closing and reopening it.
  try {const result=await upload(files[0]);progress.hidden=true;showStatus('校验通过，正在安装并重启…');await waitForRestart(result);}
  catch(error){showStatus(error.message,true);}
  finally {busy=false;showShutdownAvailability();progress.hidden=true;await refreshAvailability();}
 }
 async function refreshAvailability() {
  try {const response=await fetch('/api/app',{cache:'no-store',signal:AbortSignal.timeout(2000)});if(response.ok){info=await response.json();zone.disabled=!info.updateEnabled;modal.querySelectorAll('.settings-version').forEach(el=>el.textContent=`当前版本 ${info.version} · ${info.os} / ${info.arch}`);}}
  catch {zone.disabled=true;}
 }
})();
