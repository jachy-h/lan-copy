const $ = (selector) => document.querySelector(selector);
let currentPath = new URLSearchParams(location.hash.slice(1)).get("path") || "";
let entries = [];
let editing = null;
let moving = null;
let destination = "";
let toastTimer;
let requestVersion = 0;
let moveVersion = 0;
let mutationPending = false;
let lastListing = "";
let searchTimer;
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
async function api(url, data) {
  const response = await fetch(url, data ? {method: "POST", headers: {"Content-Type": "application/json"}, body: JSON.stringify(data)} : {cache: "no-store"});
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
    const target = segments.slice(0, index).join("/");
    if (index) container.append(element("span", "", "/"));
    const item = button(name, () => navigate(target), "crumb");
    if (index === segments.length) item.setAttribute("aria-current", "location");
    if (droppable) dropTarget(item, target);
    container.append(item);
  });
}
function navigate(value) {
  currentPath = value;
  lastListing = "";
  entries = [];
  $("#entries").replaceChildren();
  $("#directoryEmpty").classList.add("hidden");
  $("#entrySummary").textContent = "正在读取…";
  $("#search").value = "";
  history.replaceState(null, "", `#${new URLSearchParams({path: value})}`);
  loadDirectory();
}
async function loadDirectory() {
  const version = ++requestVersion;
  try {
    const query = $("#search").value.trim();
    const result = await api(`/api/filesystem?${new URLSearchParams({path: currentPath, q: query})}`);
    if (version !== requestVersion) return;
    const signature = JSON.stringify({query, result});
    if (signature === lastListing) { window.dispatchEvent(new Event("lan-copy:data-updated")); return; }
    lastListing = signature;
    entries = result.entries;
    crumbs($("#breadcrumbs"), currentPath, navigate, true);
    renderEntries();
    window.dispatchEvent(new Event("lan-copy:data-updated"));
  } catch (error) {
    if (version !== requestVersion) return;
    if (currentPath && error.status === 404) { notify("原文件夹已被移动，已返回全部文件"); navigate(""); return; }
    window.dispatchEvent(new Event("lan-copy:data-failed"));
    lastListing = "";
    entries = [];
    $("#entrySummary").textContent = "读取失败，请重新载入页面";
    $("#entries").replaceChildren();
    $("#directoryEmpty").classList.add("hidden");
    notify(error.message, true);
  }
}
function renderEntries() {
  const query = $("#search").value.trim();
  const visible = entries;
  const folders = entries.filter(item => item.directory).length;
  $("#entrySummary").textContent = query ? `${visible.length} 个匹配项（含子文件夹）` : `${folders} 个文件夹 · ${entries.length - folders} 个文件`;
  $("#directoryEmpty").classList.toggle("hidden", visible.length > 0);
  $("#emptyTitle").textContent = query ? "没有匹配的文件或文件夹" : "这个文件夹还是空的";
  $("#entries").replaceChildren(...visible.map(item => {
    const row = element("article", `file-row explorer-row${query ? " search-result" : ""}`);
    row.draggable = true;
    row.addEventListener("dragstart", event => { event.dataTransfer.setData("application/x-lan-copy-path", item.path); event.dataTransfer.effectAllowed = "move"; });
    const icon = element("div", `file-icon${item.directory ? " folder-icon" : ""}`, item.directory ? "▱" : item.name.split(".").pop().slice(0, 4));
    icon.setAttribute("aria-hidden", "true");
    const main = element("div", "file-main");
    const name = item.directory ? button(item.name, () => navigate(item.path), "file-name folder-name") : LanCopyFileUI.previewName(item);
    if (item.directory) name.title = item.name;
    LanCopyFileUI.highlight(name, item.name, query);
    LanCopyFileUI.thumbnail(icon, item);
    main.append(name, element("div", "file-meta", `${query ? `位于 ${item.path.includes("/") ? item.path.slice(0, item.path.lastIndexOf("/")) : "全部文件"} · ` : ""}${item.directory ? "文件夹" : formatBytes(item.size)} · ${new Date(item.modified).toLocaleString("zh-CN", {month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit"})}`));
    const actions = element("div", "explorer-actions");
    if (!item.directory) {
      if (localhostAccess) {
        actions.append(button("打开文件夹", event => openContainingFolder(item, event.currentTarget)));
      } else {
        const download = element("a", "secondary", "下载");
        download.href = `/files/${encodeURIComponent(item.path)}`;
        download.download = item.name;
        actions.append(download);
      }
    }
    actions.append(button("移动", () => openMove(item)), button("重命名", () => openEdit(item)), button("删除", () => deleteEntry(item), "secondary danger"));
    if (item.directory) dropTarget(row, item.path);
    row.append(icon, main, actions);
    return row;
  }));
}
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
  $("#editTitle").textContent = item ? "重命名" : "新建文件夹";
  $("#entryName").value = item ? item.name : "";
  $("#editError").textContent = "";
  $("#editDialog").showModal();
  $("#entryName").focus(); $("#entryName").select();
}
$("#editForm").addEventListener("submit", async event => {
  event.preventDefault();
  const name = $("#entryName").value.trim();
  if (!name || /[\\/:*?"<>|\x00-\x1f\x7f]/.test(name) || name === "." || name === ".." || name.endsWith(".")) { $("#editError").textContent = "请输入有效名称，不可包含斜线或特殊字符"; return; }
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
$("#mkdir").addEventListener("click", () => openEdit());
$("#search").addEventListener("input", () => {
 clearTimeout(searchTimer);
 ++requestVersion;
 $("#entrySummary").textContent = "正在搜索…";
 searchTimer = setTimeout(loadDirectory, 200);
});
let events;
if (window.EventSource) { events = new EventSource("/api/events"); events.addEventListener("files", loadDirectory); }
window.addEventListener("hashchange", () => { currentPath = new URLSearchParams(location.hash.slice(1)).get("path") || ""; loadDirectory(); });
const refreshTimer = setInterval(loadDirectory, 60000);
let stopped = false;
document.addEventListener("visibilitychange", () => { if (!document.hidden && !stopped) loadDirectory(); });
window.addEventListener("lan-copy:shutdown", () => { stopped = true; clearInterval(refreshTimer); clearTimeout(searchTimer); events?.close(); });
loadDirectory();

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
const folderInput = $("#folderUploadInput");
$("#uploadFolder").addEventListener("click", () => {
 if (uploading) { notify("正在上传，请等待本次完成", true); return; }
 if (!("webkitdirectory" in folderInput)) { notify("此浏览器不支持选择文件夹，请使用电脑浏览器拖拽上传", true); return; }
 folderInput.click();
});
folderInput.addEventListener("change", () => {
 const files = [...folderInput.files];
 folderInput.value = "";
 if (files.length) uploadToFolder(progress => LanCopyUploadTree.fromFiles(files, true, progress));
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
    xhr.open("POST", `/api/upload-tree?${new URLSearchParams({path: uploadPath})}`);
    xhr.send(form);
   });
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
