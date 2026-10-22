(() => {
 const status = document.querySelector("#serviceStatus");
 const statusAccess = document.querySelector("#statusAccess");
 const panel = document.querySelector("#serviceStatusPopover");
 const updatedAt = document.querySelector("#dataUpdatedAt");
 const hint = document.querySelector("#dataRefreshHint");
 let pinned = false, closeTimer, stopped = false;
 function setOpen(open) {
  clearTimeout(closeTimer);
  panel.hidden = !open;
  status.setAttribute("aria-expanded", String(open));
  if (open) {
   const rect = status.getBoundingClientRect();
   panel.style.left = `${Math.max(16, Math.min(rect.right - panel.offsetWidth, window.innerWidth - panel.offsetWidth - 16))}px`;
   panel.style.top = `${rect.bottom + 10}px`;
  }
 }
 statusAccess.addEventListener("pointerenter", event => { if (event.pointerType !== "touch") setOpen(true); });
 statusAccess.addEventListener("pointerleave", () => { if (!pinned) closeTimer = setTimeout(() => setOpen(false), 200); });
 statusAccess.addEventListener("focusin", () => setOpen(true));
 statusAccess.addEventListener("focusout", event => { if (!statusAccess.contains(event.relatedTarget)) { pinned = false; setOpen(false); } });
 status.addEventListener("click", () => { pinned = !pinned; setOpen(pinned); });
 document.addEventListener("click", event => { if (!statusAccess.contains(event.target)) { pinned = false; setOpen(false); } });
 document.addEventListener("keydown", event => { if (event.key === "Escape" && !panel.hidden) { pinned = false; setOpen(false); } });
 window.addEventListener("resize", () => { if (!panel.hidden) setOpen(true); });
 window.addEventListener("lan-copy:data-updated", () => {
  if (stopped) return;
  const now = new Date();
  updatedAt.dateTime = now.toISOString();
  updatedAt.textContent = now.toLocaleString("zh-CN", {year:"numeric", month:"2-digit", day:"2-digit", hour:"2-digit", minute:"2-digit", second:"2-digit", hourCycle:"h23"});
  status.classList.remove("offline");
  status.setAttribute("aria-label", "局域网在线");
  status.querySelector(".status-label").textContent = "局域网在线";
  hint.textContent = "每分钟自动更新";
 });
 window.addEventListener("lan-copy:data-failed", () => {
  if (stopped) return;
  status.classList.add("offline");
  status.setAttribute("aria-label", "连接异常");
  status.querySelector(".status-label").textContent = "连接异常";
  hint.textContent = "最近刷新失败，每分钟自动重试";
 });
 window.addEventListener("lan-copy:shutdown", () => {
  stopped = true;
  status.classList.add("offline");
  status.setAttribute("aria-label", "服务已关闭");
  status.querySelector(".status-label").textContent = "服务已关闭";
  hint.textContent = "服务已关闭，已停止自动更新";
 });
})();
