(() => {
  let toastTimer;
  function notify(message, error = false) {
    const toast = document.querySelector("#toast");
    toast.textContent = message;
    toast.className = `toast show${error ? " error" : ""}`;
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => toast.className = "toast", 3000);
  }
  function previewName(file, from = "") {
    const supported = Boolean(file.previewType);
    const node = document.createElement(supported ? "a" : "button");
    node.className = `file-name download-name${supported ? "" : " preview-unavailable"}`;
    node.textContent = file.name;
    node.title = supported ? file.name : `${file.name}（此文件格式暂不支持预览）`;
    if (supported) {
      const params = new URLSearchParams({path: file.path || file.name});
      if (from) params.set("from", from);
      node.href = `/preview.html?${params}`;
      node.addEventListener("click", event => {
        if (typeof window.LanCopyOpenPreview !== "function") return;
        event.preventDefault();
        window.LanCopyOpenPreview(file.path || file.name, node);
      });
    } else {
      node.type = "button";
      node.addEventListener("click", () => notify("此文件格式暂不支持预览"));
    }
    return node;
  }
  function tag(label, description = "") {
    const node = document.createElement("span");
    node.className = "file-tag";
    node.textContent = label;
    if (description) node.title = description;
    if (description) node.setAttribute("aria-label", `${label}：${description}`);
    return node;
  }
  function thumbnail(icon, file) {
    if (!file.previewType?.startsWith("image/")) return;
    const image = document.createElement("img");
    image.alt = "";
    image.loading = "lazy";
    image.decoding = "async";
    image.draggable = false;
    image.width = 128; image.height = 128;
    image.src = `/thumbnails/${encodeURIComponent(file.path || file.name)}?${new URLSearchParams({v: `${file.modified}-${file.size}`})}`;
    // Keep the extension icon if the image is damaged or exceeds decode limits.
    image.addEventListener("load", () => { icon.classList.add("has-thumbnail"); });
    image.addEventListener("error", () => image.remove());
    icon.append(image);
  }
  function highlight(node, name, query) {
    node.replaceChildren();
    if (!query) { node.textContent = name; return; }
    const pattern = new RegExp(query.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"), "giu");
    let position = 0;
    for (const match of name.matchAll(pattern)) {
      node.append(document.createTextNode(name.slice(position, match.index)));
      const mark = document.createElement("mark");
      mark.textContent = match[0];
      node.append(mark);
      position = match.index + match[0].length;
    }
    node.append(document.createTextNode(name.slice(position)));
  }
  async function copyText(content) {
    try { if (navigator.clipboard?.writeText) { await navigator.clipboard.writeText(content); return true; } } catch {}
    const helper = document.createElement("textarea");
    helper.value = content; helper.style.position = "fixed"; helper.style.opacity = "0";
    document.body.append(helper); helper.focus(); helper.select();
    const copied = document.execCommand("copy"); helper.remove(); return copied;
  }
  window.LanCopyFileUI = {previewName, thumbnail, highlight, copyText, notify, tag};
})();
