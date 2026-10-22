(() => {
  const $ = selector => document.querySelector(selector);
  const params = new URLSearchParams(location.search);
  const path = params.get("path") || "";
  const content = $("#previewContent");
  const status = $("#previewStatus");
  const resource = `/preview-files/${encodeURIComponent(path)}`;
  const controller = new AbortController();
  const textLimit = 256 * 1024;
  let pdfTask;
  let textBytes;
  let textTruncated = false;

  // Return only to known local pages; preserve the folder without trusting a URL.
  $("#previewBack").href = params.get("from") === "transfer" ? "/transfer.html" : `/#${new URLSearchParams({path: path.includes("/") ? path.slice(0, path.lastIndexOf("/")) : ""})}`;
  if (path) {
    $("#previewTitle").textContent = path.split("/").pop();
    document.title = `${path.split("/").pop()} · Lan Copy`;
    const download = $("#previewDownload");
    download.href = `/files/${encodeURIComponent(path)}`;
    download.download = path.split("/").pop();
    download.classList.remove("hidden");
  }
  function message(text) { status.textContent = text; }
  function decodeText() {
    const text = new TextDecoder($("#textEncoding").value).decode(textBytes, {stream: textTruncated});
    const pre = document.createElement("pre");
    pre.className = "preview-text";
    pre.textContent = text;
    content.replaceChildren(pre);
    message(textTruncated ? "文件较大，仅显示前 256 KiB；下载可查看完整内容。" : "文本预览 · 乱码时可切换编码");
  }
  $("#textEncoding").addEventListener("change", decodeText);

  async function showPDF() {
    message("正在载入 PDF…");
    const pdfjs = await import("/vendor/pdfjs/legacy/build/pdf.min.mjs");
    pdfjs.GlobalWorkerOptions.workerSrc = "/vendor/pdfjs/legacy/build/pdf.worker.min.mjs";
    pdfTask = pdfjs.getDocument({
      url: resource,
      cMapUrl: "/vendor/pdfjs/cmaps/", cMapPacked: true,
      standardFontDataUrl: "/vendor/pdfjs/standard_fonts/",
      wasmUrl: "/vendor/pdfjs/wasm/",
      isEvalSupported: false,
      // Fetch pages on demand instead of prefetching the entire document.
      disableAutoFetch: true, disableStream: true,
    });
    let pdf;
    try { pdf = await pdfTask.promise; }
    catch (error) {
      message(error.name === "PasswordException" ? "此 PDF 需要密码，请下载后打开。" : "PDF 无法读取，文件可能已损坏，请下载后打开。");
      return;
    }
    const tools = document.createElement("nav");
    tools.className = "preview-pdf-tools";
    tools.setAttribute("aria-label", "PDF 翻页与缩放");
    const previous = document.createElement("button");
    previous.className = "secondary"; previous.textContent = "上一页";
    const next = document.createElement("button");
    next.className = "secondary"; next.textContent = "下一页";
    const pageNumber = document.createElement("input");
    pageNumber.type = "number"; pageNumber.min = "1"; pageNumber.max = String(pdf.numPages);
    pageNumber.setAttribute("aria-label", "PDF 页码");
    const total = document.createElement("span"); total.textContent = `/ ${pdf.numPages} 页`;
    const zoom = document.createElement("select"); zoom.setAttribute("aria-label", "PDF 缩放");
    [["fit", "适合宽度"], ["1", "100%"], ["1.5", "150%"], ["2", "200%"]].forEach(([value, text]) => {
      const option = document.createElement("option"); option.value = value; option.textContent = text; zoom.append(option);
    });
    tools.append(previous, pageNumber, total, next, zoom);
    content.before(tools);
    content.classList.add("pdf-canvas-container");
    const canvas = document.createElement("canvas");
    canvas.setAttribute("role", "img");
    content.append(canvas);
    let pageIndex = 1, busy = false, renderedPage;
    async function render() {
      if (busy || controller.signal.aborted) return;
      busy = true;
      [previous, next, pageNumber, zoom].forEach(node => node.disabled = true);
      pageNumber.value = String(pageIndex);
      message(`正在显示第 ${pageIndex} 页…`);
      try {
        renderedPage?.cleanup();
        renderedPage = await pdf.getPage(pageIndex);
        const base = renderedPage.getViewport({scale: 1});
        const scale = zoom.value === "fit" ? Math.min((content.clientWidth - 24) / base.width, 2) : Number(zoom.value);
        const viewport = renderedPage.getViewport({scale: Math.max(0.1, scale)});
        const pixels = Math.min(window.devicePixelRatio || 1, 2, Math.sqrt(16000000 / (viewport.width * viewport.height)));
        canvas.width = Math.ceil(viewport.width * pixels);
        canvas.height = Math.ceil(viewport.height * pixels);
        canvas.style.width = `${viewport.width}px`; canvas.style.height = `${viewport.height}px`;
        canvas.setAttribute("aria-label", `PDF 第 ${pageIndex} 页，共 ${pdf.numPages} 页`);
        await renderedPage.render({canvasContext: canvas.getContext("2d"), viewport, transform: [pixels, 0, 0, pixels, 0, 0]}).promise;
        message(`第 ${pageIndex} / ${pdf.numPages} 页 · 可翻页或缩放查看`);
        content.scrollTop = 0; content.scrollLeft = 0;
      } catch (error) {
        if (!controller.signal.aborted) message("此页无法显示，请尝试其他页面，或下载原文件。");
      } finally {
        busy = false;
        previous.disabled = pageIndex <= 1; next.disabled = pageIndex >= pdf.numPages;
        pageNumber.disabled = false; zoom.disabled = false;
      }
    }
    previous.addEventListener("click", () => { if (!busy && pageIndex > 1) { pageIndex--; render(); } });
    next.addEventListener("click", () => { if (!busy && pageIndex < pdf.numPages) { pageIndex++; render(); } });
    pageNumber.addEventListener("change", () => {
      const value = Number(pageNumber.value);
      if (!Number.isInteger(value) || value < 1 || value > pdf.numPages) { pageNumber.value = String(pageIndex); return; }
      pageIndex = value; render();
    });
    zoom.addEventListener("change", render);
    let resizeTimer;
    window.addEventListener("resize", () => { clearTimeout(resizeTimer); resizeTimer = setTimeout(() => { if (zoom.value === "fit") render(); }, 150); }, {signal: controller.signal});
    await render();
  }

  async function load() {
    if (!path) { message("未指定文件，请返回文件列表选择文件。"); return; }
    try {
      const response = await fetch(resource, {method: "HEAD", cache: "no-store", signal: controller.signal});
      if (!response.ok) {
        message(response.status === 415 ? "此文件暂不支持浏览器预览，请下载后打开。" : response.status === 404 ? "文件已被移动或删除，请返回文件列表刷新。" : "无法读取文件，请返回文件列表重试。");
        return;
      }
      const type = response.headers.get("Content-Type") || "";
      if (type.startsWith("image/")) {
        const image = document.createElement("img");
        image.className = "preview-image";
        image.alt = path.split("/").pop();
        image.addEventListener("load", () => message("点击图片切换原始尺寸，可滚动查看细节。"));
        image.addEventListener("error", () => message("图片无法显示，可能格式不受浏览器支持或文件已损坏，请下载后打开。"));
        image.tabIndex = 0;
        image.setAttribute("role", "button");
        image.setAttribute("aria-label", `${path.split("/").pop()}，切换原始尺寸`);
        image.setAttribute("aria-pressed", "false");
        function toggleSize() { image.setAttribute("aria-pressed", String(image.classList.toggle("original-size"))); }
        image.addEventListener("click", toggleSize);
        image.addEventListener("keydown", event => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); toggleSize(); } });
        image.src = resource;
        content.append(image);
      } else if (type.startsWith("video/") || type.startsWith("audio/")) {
        const media = document.createElement(type.startsWith("video/") ? "video" : "audio");
        media.controls = true;
        media.preload = "metadata";
        media.setAttribute("playsinline", "");
        media.className = "preview-media";
        media.addEventListener("loadedmetadata", () => message("使用播放器播放、调节音量或拖动进度。"));
        media.addEventListener("error", () => message("浏览器无法播放此文件的编码或文件已损坏，请下载后打开。"));
        media.src = resource;
        content.append(media);
      } else if (type.startsWith("application/pdf")) {
        await showPDF();
      } else if (type.startsWith("text/plain")) {
        const result = await fetch(resource, {headers: {Range: `bytes=0-${textLimit - 1}`}, cache: "no-store", signal: controller.signal});
        if (!result.ok) throw new Error("text");
        // The server honors Range, keeping large logs out of browser memory.
        textBytes = new Uint8Array(await result.arrayBuffer());
        textTruncated = Number(response.headers.get("Content-Length")) > textBytes.length;
        if (textBytes[0] === 0xff && textBytes[1] === 0xfe) $("#textEncoding").value = "utf-16le";
        else if (textBytes[0] === 0xfe && textBytes[1] === 0xff) $("#textEncoding").value = "utf-16be";
        $("#textTools").classList.remove("hidden");
        decodeText();
      } else {
        message("此文件暂不支持浏览器预览，请下载后打开。");
      }
    } catch (error) {
      if (error.name !== "AbortError") message("读取失败，请刷新重试，或下载原文件。");
    }
  }
  window.addEventListener("pagehide", () => {
    controller.abort();
    pdfTask?.destroy().catch(() => {});
    content.querySelectorAll("video, audio").forEach(media => { media.pause(); media.removeAttribute("src"); media.load(); });
  });
  // Back/forward cache restores the page after its pending fetch was aborted.
  window.addEventListener("pageshow", event => { if (event.persisted) location.reload(); });
  load();
})();
