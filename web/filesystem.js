const $ = (selector) => document.querySelector(selector);
let currentPath = new URLSearchParams(location.hash.slice(1)).get("path") || "";
let entries = [];
let selectedPaths = new Set();
let selectionAnchor = null;
let focusedPath = null;
let sortKey = "name";
let sortDirection = "ascending";
try {
  const savedSort = localStorage.getItem("lan-copy.files.sort");
  const savedDirection = localStorage.getItem("lan-copy.files.direction");
  if (["name", "modified", "type", "size"].includes(savedSort)) sortKey = savedSort;
  if (["ascending", "descending"].includes(savedDirection)) sortDirection = savedDirection;
} catch {}
let menuReturnFocus = null;
let editing = null;
let editingExtension = "";
let moving = null;
let destination = "";
let toastTimer;
let requestVersion = 0;
let directoryController = null;
let moveVersion = 0;
let mutationPending = false;
let lastListing = "";
let searchTimer;
let eventRefreshTimer;
let stopped = false;
let events;
let fallbackRefreshTimer;
let freshTagTimer;
let previewInstance = null;
let previewReturnFocus = null;
const localhostAccess = ["localhost", "127.0.0.1", "::1", "[::1]"].includes(location.hostname);

function element(tag, className, content) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (content !== undefined) node.textContent = content;
  return node;
}
function button(label, onClick, className = "secondary") {
  const node = element("button", className, label);
  node.type = "button";
  node.addEventListener("click", onClick);
  return node;
}
function notify(message, error = false) {
  $("#toast").textContent = message;
  $("#toast").className = `toast show${error ? " error" : ""}`;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => $("#toast").className = "toast", 3000);
}
async function api(url, data, signal) {
  const options = data ? {method: "POST", headers: {"Content-Type": "application/json"}, body: JSON.stringify(data)} : {cache: "no-store"};
  if (signal) options.signal = signal;
  const response = await fetch(url, options);
  if (!response.ok) {
    const body = await response.json().catch(() => ({}));
    const error = new Error(body.error || "无法连接到服务，请重试");
    error.status = response.status;
    error.requiresConfirmation = body.requiresConfirmation === true;
    throw error;
  }
  return response.status === 204 ? null : response.json();
}
function formatBytes(bytes) {
  if (bytes < 1024) return `${bytes} B`;
  const unit = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), 4);
  return `${(bytes / 1024 ** unit).toFixed(1)} ${["B", "KB", "MB", "GB", "TB"][unit]}`;
}
function crumbs(container, value, navigate, droppable = false) {
  container.replaceChildren();
  const segments = value ? value.split("/") : [];
  ["全部文件", ...segments].forEach((name, index) => {
    const target = segments.slice(0, Math.max(0, index - 1)).join("/");
    if (index) container.append(element("span", "", "/"));
    const item = button(name, () => navigate(target), "crumb");
    if (index === segments.length) item.setAttribute("aria-current", "location");
    if (droppable) dropTarget(item, target);
    container.append(item);
  });
}
function navigate(value) {
  if (value === currentPath) return;
  directoryController?.abort();
  currentPath = value;
  lastListing = "";
  entries = [];
  selectedPaths.clear(); selectionAnchor = null; focusedPath = null;
  $("#entries").replaceChildren();
  $("#directoryEmpty").classList.add("hidden");
  $("#entrySummary").textContent = "正在读取…";
  history.pushState(null, "", `#${new URLSearchParams({path: value})}`);
  loadDirectory();
}
async function loadDirectory() {
  if (stopped || document.hidden) return;
  clearTimeout(searchTimer);
  searchTimer = null;
  directoryController?.abort();
  const controller = new AbortController();
  directoryController = controller;
  const version = ++requestVersion;
  try {
    const query = $("#search").value.trim();
    const result = await api(`/api/filesystem?${new URLSearchParams({path: currentPath, q: query})}`, null, controller.signal);
    if (version !== requestVersion || controller.signal.aborted) return;
    if (events && events.readyState === window.EventSource.OPEN) stopFallbackRefresh();
    const signature = JSON.stringify({query, result});
    if (signature === lastListing) { window.dispatchEvent(new Event("lan-copy:data-updated")); return; }
    lastListing = signature;
    entries = result.entries;
    const valid = new Set(entries.map(item => item.path));
    selectedPaths = new Set([...selectedPaths].filter(path => valid.has(path)));
    crumbs($("#breadcrumbs"), currentPath, navigate, true);
    renderEntries();
    window.dispatchEvent(new Event("lan-copy:data-updated"));
  } catch (error) {
    if (version !== requestVersion || controller.signal.aborted || error.name === "AbortError") return;
    startFallbackRefresh();
    if (currentPath && error.status === 404) { notify("原文件夹已被移动，已返回全部文件"); navigate(""); return; }
    window.dispatchEvent(new Event("lan-copy:data-failed"));
    lastListing = "";
    entries = [];
    $("#entrySummary").textContent = "读取失败，请重新载入页面";
    $("#entries").replaceChildren();
    $("#directoryEmpty").classList.add("hidden");
    notify(error.message, true);
  } finally {
    if (directoryController === controller) directoryController = null;
  }
}
function renderEntries() {
  const query = $("#search").value.trim();
  const collator = new Intl.Collator(undefined, {numeric: true, sensitivity: "base"});
  const visible = [...entries].sort((a, b) => {
    if (a.directory !== b.directory) return a.directory ? -1 : 1;
    let result;
    if (sortKey === "modified") result = new Date(a.modified) - new Date(b.modified);
    else if (sortKey === "size") result = (a.size || 0) - (b.size || 0);
    else if (sortKey === "type") result = collator.compare(a.directory ? "文件夹" : (a.name.split(".").pop() || ""), b.directory ? "文件夹" : (b.name.split(".").pop() || ""));
    else result = collator.compare(a.name, b.name);
    return (sortDirection === "ascending" ? 1 : -1) * (result || collator.compare(a.name, b.name));
  });
  const folders = entries.filter(item => item.directory).length;
  $("#entrySummary").textContent = query ? `${visible.length} 个匹配项（含子文件夹）` : `${folders} 个文件夹 · ${entries.length - folders} 个文件`;
  $("#goParent").disabled = !currentPath;
  $("#clearSearch").hidden = !query;
  document.querySelectorAll("#listHeader [data-sort]").forEach(header => {
    const active = header.dataset.sort === sortKey;
    header.parentElement.setAttribute("aria-sort", active ? sortDirection : "none");
    header.querySelector("span").textContent = active ? (sortDirection === "ascending" ? "↑" : "↓") : "";
  });
  $("#directoryEmpty").classList.toggle("hidden", visible.length > 0);
  $("#emptyTitle").textContent = query ? "没有匹配的文件或文件夹" : "这个文件夹还是空的";
  $("#entries").replaceChildren(...visible.map(item => {
    const row = element("article", `file-row explorer-row${query ? " search-result" : ""}${selectedPaths.has(item.path) ? " selected" : ""}`);
    row.dataset.path = item.path; row.setAttribute("role", "row"); row.setAttribute("aria-selected", selectedPaths.has(item.path)); row.tabIndex = item.path === focusedPath || (!focusedPath && visible[0] === item) ? 0 : -1;
    row.draggable = true;
    row.addEventListener("dragstart", event => { event.dataTransfer.setData("application/x-lan-copy-path", item.path); event.dataTransfer.effectAllowed = "move"; });
    row.addEventListener("click", event => selectRow(item, event, visible));
    row.addEventListener("dblclick", event => { if (event.target.closest("button,a")) return; openEntry(item, row); });
    row.addEventListener("contextmenu", event => { event.preventDefault(); selectContextItem(item, event); showFileMenu(event.clientX, event.clientY, row); });
    const icon = element("div", `file-icon${item.directory ? " folder-icon" : ""}`, item.directory ? "▱" : item.name.split(".").pop().slice(0, 4));
    icon.setAttribute("aria-hidden", "true");
    const main = element("div", "file-main name-cell");
    const name = item.directory ? button(item.name, event => { event.stopPropagation(); navigate(item.path); }, "file-name folder-name") : element("span", "file-name", item.name);
    name.title = item.name;
    LanCopyFileUI.highlight(name, item.name, query);
    LanCopyFileUI.thumbnail(icon, item);
    main.prepend(icon); main.append(name);
    if (!item.directory) {
      const tag = LanCopyFileUI.tag("新", "最近 4 小时内修改");
      tag.classList.add("recent-tag");
      tag.hidden = !isRecent(item.modified);
      main.append(tag);
      row.dataset.modified = item.modified || "";
    }
    const date = element("div", "file-cell modified-cell", new Date(item.modified).toLocaleString("zh-CN", {year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit"}));
    const type = element("div", "file-cell type-cell", item.directory ? "文件夹" : (item.name.includes(".") ? `${item.name.split(".").pop().toUpperCase()} 文件` : "文件"));
    const size = element("div", "file-cell size-cell", item.directory ? "—" : formatBytes(item.size));
    [main,date,type,size].forEach(cell => cell.setAttribute("role", "gridcell"));
    const actions = element("div", "explorer-actions");
    const more = button("⋯", event => { event.stopPropagation(); selectContextItem(item, event); showFileMenuAtButton(event.currentTarget, row); }, "secondary row-menu-button");
    more.setAttribute("aria-label", `更多操作：${item.name}`); more.setAttribute("aria-haspopup", "menu"); actions.append(more);
    if (item.directory) dropTarget(row, item.path);
    row.append(main, date, type, size, actions);
    return row;
  }));
  updateFreshTags();
}
const RECENT_WINDOW_MS = 4 * 60 * 60 * 1000;
function isRecent(value, now = Date.now()) {
  const modified = Date.parse(value);
  const age = now - modified;
  return Number.isFinite(modified) && age >= 0 && age < RECENT_WINDOW_MS;
}
function updateFreshTags() {
  const now = Date.now();
  document.querySelectorAll(".explorer-row[data-modified]").forEach(row => {
    const tag = row.querySelector(".recent-tag");
    if (tag) tag.hidden = !isRecent(row.dataset.modified, now);
  });
}
function clearSelection() {
  selectedPaths.clear(); selectionAnchor = null;
  document.querySelectorAll(".explorer-row").forEach(row => { row.classList.remove("selected"); row.setAttribute("aria-selected", "false"); });
}
function itemByPath(path) { return entries.find(item => item.path === path); }
function selectedItems() { return [...selectedPaths].map(itemByPath).filter(Boolean); }
function selectRow(item, event, visible) {
  focusedPath = item.path;
  const row = event.currentTarget;
  if (event.shiftKey && selectionAnchor) {
    const start = visible.findIndex(entry => entry.path === selectionAnchor);
    const end = visible.findIndex(entry => entry.path === item.path);
    if (start >= 0 && end >= 0) {
      if (!event.ctrlKey && !event.metaKey) selectedPaths.clear();
      visible.slice(Math.min(start, end), Math.max(start, end) + 1).forEach(entry => selectedPaths.add(entry.path));
    }
  } else if (event.ctrlKey || event.metaKey) {
    selectedPaths.has(item.path) ? selectedPaths.delete(item.path) : selectedPaths.add(item.path);
    selectionAnchor = item.path;
  } else {
    selectedPaths = new Set([item.path]); selectionAnchor = item.path;
  }
  document.querySelectorAll(".explorer-row").forEach(node => {
    const selected = selectedPaths.has(node.dataset.path);
    node.classList.toggle("selected", selected); node.setAttribute("aria-selected", String(selected));
  });
  document.querySelectorAll(".explorer-row").forEach(node => node.tabIndex = node.dataset.path === item.path ? 0 : -1);
}
function selectContextItem(item, event) {
  focusedPath = item.path;
  if (!selectedPaths.has(item.path)) {
    selectedPaths = new Set([item.path]); selectionAnchor = item.path;
    document.querySelectorAll(".explorer-row").forEach(node => {
      const selected = selectedPaths.has(node.dataset.path);
      node.classList.toggle("selected", selected); node.setAttribute("aria-selected", String(selected));
    });
  }
}
function openEntry(item, trigger = document.activeElement) {
  if (item.directory) navigate(item.path);
  else if (item.previewType) openPreview(item.path, trigger);
  else location.href = `/files/${encodeURIComponent(item.path)}`;
}
function openPreview(path, trigger = document.activeElement) {
  const dialog = $("#previewDialog");
  if (!dialog || !window.LanCopyPreview) return;
  previewInstance?.destroy();
  previewReturnFocus = trigger;
  $("#previewTitle").textContent = path.split("/").pop();
  const download = $("#previewDownload");
  download.href = `/files/${encodeURIComponent(path)}`;
  download.download = path.split("/").pop();
  download.classList.remove("hidden");
  $("#textTools").classList.add("hidden");
  $("#previewStatus").textContent = "正在读取文件…";
  if (!dialog.open) dialog.showModal();
  previewInstance = window.LanCopyPreview.mount(dialog, path);
  $("#previewClose").focus();
}
window.LanCopyOpenPreview = openPreview;
function closePreview() {
  previewInstance?.destroy(); previewInstance = null;
  const dialog = $("#previewDialog");
  if (dialog.open) dialog.close();
  if (previewReturnFocus?.isConnected) previewReturnFocus.focus();
  else $("#entries").focus();
}
function closeFileMenu(restore = true) {
  const menu = $("#fileMenu");
  if (menu.hidden) return;
  menu.hidden = true;
  if (restore && menuReturnFocus?.isConnected) menuReturnFocus.focus();
}
function addMenuAction(label, action, disabled = false) {
  const node = element("button", "file-menu-item", label); node.type = "button"; node.setAttribute("role", "menuitem"); node.disabled = disabled;
  node.addEventListener("click", () => { closeFileMenu(true); action(); }); $("#fileMenu").append(node);
}
function populateFileMenu() {
  const menu = $("#fileMenu"), items = selectedItems(); menu.replaceChildren();
  if (items.length === 1) {
    const item = items[0];
    addMenuAction(item.directory ? "打开" : (item.previewType ? "预览" : "下载"), () => openEntry(item));
    if (!item.directory) {
      if (localhostAccess) {
        const row = menuReturnFocus?.closest?.(".explorer-row");
        const trigger = row?.querySelector(".row-menu-button") || menuReturnFocus;
        addMenuAction("打开所在文件夹", () => openContainingFolder(item, trigger));
      }
      else addMenuAction("下载", () => { const a = document.createElement("a"); a.href = `/files/${encodeURIComponent(item.path)}`; a.download = item.name; a.click(); });
    }
    addMenuAction("移动…", () => openMove(item));
    addMenuAction("重命名", () => openEdit(item));
    addMenuAction("删除", () => deleteEntry(item));
  }
  addMenuAction(items.length > 1 ? "复制所选路径" : "复制路径", async () => {
    const ok = await LanCopyFileUI.copyText(items.map(item => item.path).join("\n"));
    notify(ok ? `已复制${items.length > 1 ? `${items.length} 个` : ""}路径` : "无法访问剪贴板，请检查浏览器权限", !ok);
  });
}
function showFileMenu(x, y, trigger) {
  menuReturnFocus = trigger; populateFileMenu();
  const menu = $("#fileMenu"); menu.hidden = false;
  const rect = menu.getBoundingClientRect(); menu.style.left = `${Math.max(8, Math.min(x, innerWidth - rect.width - 8))}px`; menu.style.top = `${Math.max(8, Math.min(y, innerHeight - rect.height - 8))}px`;
  menu.querySelector("button:not(:disabled)")?.focus();
}
function showFileMenuAtButton(buttonNode, row) { const rect = buttonNode.getBoundingClientRect(); showFileMenu(rect.right, rect.bottom, buttonNode); }
async function openContainingFolder(item, trigger) {
  trigger.disabled = true;
  try {
    const response = await fetch(`/api/files/${encodeURIComponent(item.path)}/folder`, {
      method: "POST", headers: {"X-Lan-Copy-Action": "folder"}
    });
    if (!response.ok) {
      const body = await response.json().catch(() => ({}));
      throw new Error(body.error || "无法打开文件所在文件夹");
    }
    notify("已打开文件所在文件夹");
  } catch (error) { notify(error.message || "无法打开文件所在文件夹", true); }
  finally { trigger.disabled = false; }
}
function dropTarget(node, target) {
  node.addEventListener("dragover", event => {
    if (!event.dataTransfer.types.includes("application/x-lan-copy-path")) return;
    event.preventDefault(); event.dataTransfer.dropEffect = "move"; node.classList.add("drop-target");
  });
  node.addEventListener("dragleave", () => node.classList.remove("drop-target"));
  node.addEventListener("drop", async event => {
    if (!event.dataTransfer.types.includes("application/x-lan-copy-path")) return;
    event.preventDefault(); event.stopPropagation(); node.classList.remove("drop-target");
    const source = event.dataTransfer.getData("application/x-lan-copy-path");
    if (source) await mutate("move", {path: source, destination: target});
  });
}
async function mutate(action, data) {
  if (mutationPending) return false;
  mutationPending = true;
  try {
    await api(`/api/filesystem/${action}`, data);
    notify({mkdir: "文件夹已创建", move: "已移动", rename: "已重命名", delete: "已删除"}[action]);
    await loadDirectory();
    return true;
  } catch (error) {
    if ($("#editDialog").open) $("#editError").textContent = error.message;
    if ($("#moveDialog").open) $("#moveError").textContent = error.message;
    notify(error.message, true);
    return false;
  }
  finally { mutationPending = false; }
}
function openEdit(item = null) {
  editing = item;
  const dot = item && !item.directory ? item.name.lastIndexOf(".") : -1;
  editingExtension = dot > 0 ? item.name.slice(dot) : "";
  $("#editTitle").textContent = item ? "重命名" : "新建文件夹";
  $("#entryName").value = item ? item.name.slice(0, item.name.length - editingExtension.length) : "";
  $("#entryExtension").textContent = editingExtension ? `后缀名 ${editingExtension} 将自动保留` : "";
  $("#entryExtension").classList.toggle("hidden", !editingExtension);
  $("#editError").textContent = "";
  $("#editDialog").showModal();
  $("#entryName").focus(); $("#entryName").select();
}
$("#editForm").addEventListener("submit", async event => {
  event.preventDefault();
  const baseName = $("#entryName").value.trim();
  if (!baseName || /[\\/:*?"<>|\x00-\x1f\x7f]/.test(baseName) || baseName === "." || baseName === ".." || baseName.endsWith(".")) { $("#editError").textContent = "请输入有效名称，不可包含斜线或特殊字符"; return; }
  const name = baseName + editingExtension;
  if (editing && name === editing.name) { $("#editDialog").close(); return; }
  const submit = event.submitter;
  submit.disabled = true;
  const ok = await mutate(editing ? "rename" : "mkdir", editing ? {path: editing.path, destination: editing.path.includes("/") ? editing.path.slice(0, editing.path.lastIndexOf("/")) : "", name} : {path: currentPath, name});
  submit.disabled = false;
  if (ok) $("#editDialog").close();
});
function openMove(item) {
  moving = item;
  $("#moveSource").textContent = `将“${item.name}”移动到：`;
  $("#moveDialog").showModal();
  loadMoveFolders(currentPath);
}
async function loadMoveFolders(value) {
  const version = ++moveVersion;
  $("#confirmMove").disabled = true;
  $("#moveError").textContent = "";
  $("#moveFolders").textContent = "正在读取…";
  try {
    const result = await api(`/api/filesystem?${new URLSearchParams({path: value})}`);
    if (version !== moveVersion) return;
    destination = value;
    crumbs($("#moveBreadcrumbs"), value, loadMoveFolders);
    const folders = result.entries.filter(item => item.directory && item.path !== moving.path && !item.path.startsWith(moving.path + "/"));
    $("#moveFolders").replaceChildren(...folders.map(item => button(`▱  ${item.name}  ›`, () => loadMoveFolders(item.path), "folder-choice")));
    if (!folders.length) $("#moveFolders").append(element("p", "file-meta", "没有可继续进入的子文件夹"));
    const parent = moving.path.includes("/") ? moving.path.slice(0, moving.path.lastIndexOf("/")) : "";
    $("#confirmMove").disabled = destination === parent;
  } catch (error) { if (version === moveVersion) $("#moveError").textContent = error.message; }
}
$("#confirmMove").addEventListener("click", async () => {
  $("#confirmMove").disabled = true;
  if (await mutate("move", {path: moving.path, destination})) $("#moveDialog").close();
  else $("#confirmMove").disabled = false;
});
async function deleteEntry(item) {
  if (mutationPending) return;
  mutationPending = true;
  try {
    if (!await window.LanCopyDialog.confirm({title: "删除文件夹或文件？", message: `确定删除“${item.name}”吗？${item.directory ? "" : "文件将从共享电脑中删除。"}`, confirmText: "删除", danger: true})) return;
    try {
      await api("/api/filesystem/delete", {path: item.path});
    } catch (error) {
      if (!item.directory || !error.requiresConfirmation) throw error;
      if (!await window.LanCopyDialog.confirm({title: "删除整个文件夹？", message: `“${item.name}”包含内容。确定删除整个文件夹及其中所有文件和子文件夹吗？此操作无法恢复。`, confirmText: "删除全部内容", danger: true})) return;
      await api("/api/filesystem/delete", {path: item.path, recursive: true});
    }
    notify("已删除");
    await loadDirectory();
  } catch (error) {
    notify(error.message, true);
    await loadDirectory();
  } finally { mutationPending = false; }
}
document.querySelectorAll("[data-close]").forEach(node => node.addEventListener("click", () => document.getElementById(node.dataset.close).close()));
$("#search").addEventListener("input", () => {
 clearTimeout(searchTimer);
 directoryController?.abort();
 ++requestVersion;
 $("#entrySummary").textContent = "正在搜索…";
 searchTimer = setTimeout(loadDirectory, 200);
});
$("#clearSearch").addEventListener("click", () => { $("#search").value = ""; loadDirectory(); $("#search").focus(); });
$("#goParent").addEventListener("click", () => navigate(currentPath.includes("/") ? currentPath.slice(0, currentPath.lastIndexOf("/")) : ""));
$("#previewClose").addEventListener("click", closePreview);
$("#previewDialog").addEventListener("click", event => { if (event.target === event.currentTarget) closePreview(); });
$("#previewDialog").addEventListener("cancel", event => { event.preventDefault(); closePreview(); });
$("#previewDialog").addEventListener("close", () => { if (previewInstance) { previewInstance.destroy(); previewInstance = null; } });
document.querySelectorAll("#listHeader [data-sort]").forEach(header => header.addEventListener("click", () => {
  if (sortKey === header.dataset.sort) sortDirection = sortDirection === "ascending" ? "descending" : "ascending";
  else { sortKey = header.dataset.sort; sortDirection = "ascending"; }
  try { localStorage.setItem("lan-copy.files.sort", sortKey); localStorage.setItem("lan-copy.files.direction", sortDirection); } catch {}
  renderEntries();
}));
$("#entries").addEventListener("keydown", event => {
  const rows = [...$("#entries").querySelectorAll(".explorer-row")];
  if (!rows.length) return;
  const current = event.target.closest(".explorer-row");
  let index = rows.indexOf(current);
  if (event.key === "ArrowDown" || event.key === "ArrowUp") {
    event.preventDefault(); index = Math.max(0, Math.min(rows.length - 1, index + (event.key === "ArrowDown" ? 1 : -1)));
    focusedPath = rows[index].dataset.path; rows.forEach(row => row.tabIndex = row === rows[index] ? 0 : -1); rows[index].focus();
  } else if (event.key === "Enter" && current && !event.target.closest("button,a")) { event.preventDefault(); openEntry(itemByPath(current.dataset.path), current); }
  else if (event.key === " " && !event.target.closest("button,a")) { event.preventDefault(); if (current) selectRow(itemByPath(current.dataset.path), {currentTarget: current, ctrlKey: true, metaKey: false, shiftKey: false}, rows.map(row => itemByPath(row.dataset.path))); }
});
document.addEventListener("keydown", async event => {
  if ($("#fileMenu").hidden) return;
  const menu = $("#fileMenu"), items = [...menu.querySelectorAll("button:not(:disabled)")], active = items.indexOf(document.activeElement);
  if (event.key === "Escape") { event.preventDefault(); closeFileMenu(); }
  else if (event.key === "ArrowDown" || event.key === "ArrowUp") { event.preventDefault(); items[(active + (event.key === "ArrowDown" ? 1 : items.length - 1)) % items.length]?.focus(); }
  else if (event.key === "Home") { event.preventDefault(); items[0]?.focus(); }
  else if (event.key === "End") { event.preventDefault(); items.at(-1)?.focus(); }
});
document.addEventListener("click", event => { if (!event.target.closest("#fileMenu") && !event.target.closest(".row-menu-button")) closeFileMenu(false); });
document.addEventListener("keydown", async event => {
  if (event.defaultPrevented || event.target.closest("input,textarea,select,[contenteditable=true],dialog") || !$("#fileMenu").hidden) return;
  const command = event.ctrlKey || event.metaKey;
  if (event.key === "Escape") { if (selectedPaths.size) clearSelection(); return; }
  if (!$("#entries").contains(event.target) && !event.target.closest(".explorer-row")) return;
  if (command && event.key.toLowerCase() === "a") { event.preventDefault(); selectedPaths = new Set(entries.map(item => item.path)); renderEntries(); return; }
  if (event.key === "F2" && selectedPaths.size === 1) { event.preventDefault(); openEdit(selectedItems()[0]); }
  else if (event.key === "Delete" && selectedPaths.size === 1) { event.preventDefault(); deleteEntry(selectedItems()[0]); }
  else if (command && event.key.toLowerCase() === "c" && selectedPaths.size) {
    event.preventDefault(); const ok = await LanCopyFileUI.copyText(selectedItems().map(item => item.path).join("\n"));
    notify(ok ? "已复制路径" : "无法访问剪贴板，请检查浏览器权限", !ok);
  }
});
function scheduleDirectoryRefresh() {
 if (stopped || document.hidden) return;
 clearTimeout(searchTimer);
 searchTimer = null;
 clearTimeout(eventRefreshTimer);
 eventRefreshTimer = setTimeout(() => { eventRefreshTimer = null; loadDirectory(); }, 100);
}
function startFallbackRefresh() {
 if (stopped || document.hidden || fallbackRefreshTimer) return;
 fallbackRefreshTimer = setInterval(scheduleDirectoryRefresh, 60000);
}
function stopFallbackRefresh() { clearInterval(fallbackRefreshTimer); fallbackRefreshTimer = null; }
if (window.EventSource) {
 events = new EventSource("/api/events");
 events.addEventListener("change", event => {
  let change;
  try { change = JSON.parse(event.data); } catch { change = {files: true}; }
  if (change.files) scheduleDirectoryRefresh();
 });
 events.onopen = () => { stopFallbackRefresh(); scheduleDirectoryRefresh(); };
 events.onerror = startFallbackRefresh;
} else startFallbackRefresh();
window.addEventListener("popstate", () => {
  currentPath = new URLSearchParams(location.hash.slice(1)).get("path") || "";
  selectedPaths.clear(); selectionAnchor = null; focusedPath = null; lastListing = ""; loadDirectory();
});
document.addEventListener("visibilitychange", () => {
 if (document.hidden) {
  directoryController?.abort(); clearTimeout(searchTimer); clearTimeout(eventRefreshTimer); eventRefreshTimer = null; stopFallbackRefresh();
 } else if (!stopped) { if (typeof updateFreshTags === "function") updateFreshTags(); if (!events || events.readyState !== EventSource.OPEN) startFallbackRefresh(); scheduleDirectoryRefresh(); }
});
window.addEventListener("lan-copy:shutdown", () => {
 stopped = true; clearTimeout(searchTimer); clearTimeout(eventRefreshTimer); stopFallbackRefresh(); directoryController?.abort(); events?.close();
  if (typeof freshTagTimer !== "undefined") clearInterval(freshTagTimer);
  if (typeof previewInstance !== "undefined") previewInstance?.destroy();
});
window.addEventListener("pagehide", () => { clearInterval(freshTagTimer); previewInstance?.destroy(); }, {once: true});
loadDirectory();
freshTagTimer = setInterval(() => { if (!document.hidden) updateFreshTags(); }, 60000);

let uploading = false;
let dragDepth = 0;
const explorer = $(".explorer");
function externalFiles(event) { return event.dataTransfer?.types.includes("Files"); }
document.addEventListener("dragenter", event => {
 if (!externalFiles(event)) return;
 event.preventDefault();
 dragDepth++;
 explorer.classList.add("upload-dragging");
});
document.addEventListener("dragover", event => { if (externalFiles(event)) { event.preventDefault(); event.dataTransfer.dropEffect = "copy"; } });
document.addEventListener("dragleave", event => {
 if (!externalFiles(event)) return;
 if (--dragDepth <= 0) { dragDepth = 0; explorer.classList.remove("upload-dragging"); }
});
document.addEventListener("drop", event => {
 if (!externalFiles(event)) return;
 event.preventDefault();
 dragDepth = 0;
 explorer.classList.remove("upload-dragging");
 // Capture handles synchronously: the drag data store is protected after drop.
 const items = [...(event.dataTransfer.items || [])].filter(item => item.kind === "file");
 const roots = items.map(item => item.getAsEntry?.() || item.webkitGetAsEntry?.());
 const files = [...event.dataTransfer.files];
 if (roots.some(Boolean) && !roots.every(Boolean)) { notify("部分拖入内容无法读取，请检查权限后重试", true); return; }
 uploadToFolder(progress => roots.length && roots.every(Boolean)
  ? LanCopyUploadTree.fromEntries(roots, progress)
  : LanCopyUploadTree.fromFiles(files, false, progress));
});
async function uploadToFolder(collect) {
 if (uploading) { notify("正在上传，请等待本次完成后再拖入", true); return; }
 uploading = true;
 const uploadPath = currentPath;
 $("#spaceUpload").classList.remove("hidden", "complete");
 $("#spaceUploadProgress").removeAttribute("value");
 $("#spaceUploadLabel").textContent = "正在读取文件和文件夹…";
 try {
  const entries = await collect(count => { $("#spaceUploadLabel").textContent = `正在读取文件和文件夹… ${count} 项`; });
  if (!entries.length) throw new Error("未读取到文件或文件夹，请检查浏览器权限后重试");
  const fileCount = entries.filter(entry => !entry.directory).length;
  const folderCount = entries.length - fileCount;
  const summary = `${fileCount} 个文件${folderCount ? `、${folderCount} 个文件夹` : ""}`;
  const replace = new Set();
  const send = async () => {
   const preflight = await fetch(`/api/upload-tree/preflight?${new URLSearchParams({path: uploadPath})}`, {method:"POST", headers:{"Content-Type":"application/json"}, body:JSON.stringify({entries:entries.map(({path, directory}) => ({path, directory}))})});
   const checked = await preflight.json();
   if (!preflight.ok) throw new Error(checked.error || "无法检查上传目标");
   const unresolved = (checked.conflicts || []).filter(name => !replace.has(name));
   if (unresolved.length) {
    await fetch(`/api/upload-tree/session/${encodeURIComponent(checked.session)}`, {method:"DELETE"});
    const error = new Error(checked.error || "目标位置已存在同名文件或文件夹");
    error.conflicts = unresolved;
    throw error;
   }
   const form = new FormData();
   form.append("manifest", JSON.stringify({entries: entries.map(({path, directory}) => ({path, directory})), replace: [...replace]}));
   entries.forEach((entry, index) => { if (!entry.directory) form.append(`file-${index}`, entry.file, entry.file.name); });
   $("#spaceUploadLabel").textContent = `正在上传 ${summary}到 ${uploadPath || "全部文件"}`;
   $("#spaceUploadProgress").value = 0;
   await new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.upload.addEventListener("progress", event => { if (event.lengthComputable) $("#spaceUploadProgress").value = Math.min(99, Math.round(event.loaded / event.total * 100)); });
    xhr.addEventListener("load", () => {
     if (xhr.status >= 200 && xhr.status < 300) { resolve(); return; }
     let message = "上传失败，请重试";
     let conflicts;
     try { const body = JSON.parse(xhr.responseText); message = body.error || message; conflicts = body.conflicts; } catch {}
     const error = new Error(message);
     if (xhr.status === 409 && Array.isArray(conflicts) && conflicts.length) error.conflicts = conflicts;
     reject(error);
    });
    xhr.addEventListener("error", () => reject(new Error("网络中断，请重新拖入文件或文件夹重试")));
    xhr.addEventListener("abort", () => reject(new Error("上传已中止，请重新上传")));
    xhr.open("POST", `/api/upload-tree?${new URLSearchParams({path: uploadPath, session: checked.session})}`);
    xhr.send(form);
   }).finally(() => fetch(`/api/upload-tree/session/${encodeURIComponent(checked.session)}`, {method:"DELETE"}).catch(() => {}));
  };
  for (;;) {
   try { await send(); break; }
   catch (error) {
    if (!error.conflicts) throw error;
    for (const name of error.conflicts) {
     const folder = entries.some(entry => entry.path.startsWith(name + "/") || (entry.path === name && entry.directory));
     if (!await window.LanCopyDialog.confirm({title: "替换同名内容？", message: `目标位置已存在“${name}”，是否替换？${folder ? "\n将替换整个同名文件或文件夹，原有内容将被删除。" : "\n原文件或同名文件夹将被替换。"}\n取消将停止本次上传，保留原有内容。`, confirmText: "替换", danger: true})) {
      $("#spaceUploadProgress").value = 0;
      $("#spaceUploadLabel").textContent = "已取消上传，原有内容已保留";
      notify("已取消上传");
      return;
     }
     replace.add(name);
    }
   }
  }
  $("#spaceUploadProgress").value = 100;
  $("#spaceUpload").classList.add("complete");
  $("#spaceUploadLabel").textContent = `${summary}已上传到 ${uploadPath || "全部文件"}`;
  notify("上传完成");
  await loadDirectory();
 } catch (error) {
  $("#spaceUploadProgress").value = 0;
  $("#spaceUploadLabel").textContent = error.message || "无法读取文件夹，请检查权限后重试";
  notify($("#spaceUploadLabel").textContent, true);
 } finally { uploading = false; }
}
