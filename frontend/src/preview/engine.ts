import { loadPDF, type PDFDocument, type PDFLoadingTask, type PDFPage, type PDFRenderTask } from './pdf'

interface DocxRenderer {
  renderAsync(data: ArrayBuffer, body: HTMLElement, styles: HTMLElement, options: Record<string, boolean>): Promise<void>
}
declare global { interface Window { docx?: DocxRenderer } }
interface Sheet { name: string; rows: string[][]; columns: string[]; startRow: number; truncatedRows: boolean; truncatedColumns: boolean }
interface SheetResult { id: number; error?: string; names: string[]; sheet?: Sheet; hasMoreSheets: boolean }
function errorMessage(error: unknown) { return error instanceof Error ? error.message : String(error) }
let docxRendererPromise: Promise<DocxRenderer> | undefined;
const maxOfficePreviewBytes = 32 * 1024 * 1024;

function loadDOCXRenderer() {
  if (!docxRendererPromise) {
    docxRendererPromise = (async () => {
      const loadScript = (src: string) => new Promise<void>((resolve, reject) => {
        const script = document.createElement("script");
        script.src = src;
        script.onload = () => resolve();
        script.onerror = () => { script.remove(); reject(new Error("DOCX 预览组件加载失败")); };
        document.head.append(script);
      });
      await loadScript("/vendor/docx-preview/jszip.min.js");
      await loadScript("/vendor/docx-preview/docx-preview.min.js");
      if (!window.docx?.renderAsync) throw new Error("DOCX 预览组件不可用");
      return window.docx;
    })().catch(error => {
      docxRendererPromise = undefined;
      throw error;
    });
  }
  return docxRendererPromise;
}

export function mountPreview(root: Document | HTMLElement, path: string) {
  const $ = <T extends HTMLElement = HTMLElement>(selector: string): T => {
    const node = root.querySelector<T>(selector);
    if (!node) throw new Error(`预览容器缺少 ${selector}`);
    return node;
  };
  const content = $("#previewContent");
  const status = $("#previewStatus");
  const resource = `/preview-files/${encodeURIComponent(path)}`;
  const controller = new AbortController();
  const textLimit = 256 * 1024;
  let pdfTask: PDFLoadingTask | undefined;
  let renderTask: PDFRenderTask | undefined;
  let resizeTimer: ReturnType<typeof setTimeout> | undefined;
  let spreadsheetWorker: Worker | undefined;
  let spreadsheetTabs: HTMLElement | undefined;
  let spreadsheetRequestID = 0;
  const spreadsheetRequests = new Map<number, { resolve(value: SheetResult): void; reject(error: Error): void }>();
  let textBytes: Uint8Array = new Uint8Array();
  let textTruncated = false;

  function message(text: string) { if (!controller.signal.aborted) status.textContent = text; }
  function getSpreadsheetWorker() {
    if (spreadsheetWorker) return spreadsheetWorker;
    spreadsheetWorker = new Worker("/vendor/sheetjs/worker.js");
    spreadsheetWorker.onmessage = (event: MessageEvent<SheetResult>) => {
      const request = spreadsheetRequests.get(event.data.id);
      if (!request) return;
      spreadsheetRequests.delete(event.data.id);
      if (event.data.error) request.reject(new Error(event.data.error));
      else request.resolve(event.data);
    };
    spreadsheetWorker.onerror = () => {
      spreadsheetWorker?.terminate();
      spreadsheetWorker = undefined;
      for (const request of spreadsheetRequests.values()) request.reject(new Error("表格解析失败"));
      spreadsheetRequests.clear();
    };
    return spreadsheetWorker;
  }
  function requestSpreadsheet(type: 'load' | 'sheet', payload: { buffer?: ArrayBuffer; sheetName?: string } = {}, transfer: Transferable[] = []) {
    const id = ++spreadsheetRequestID;
    return new Promise<SheetResult>((resolve, reject) => {
      if (controller.signal.aborted) { reject(new Error("预览已关闭")); return; }
      spreadsheetRequests.set(id, {resolve, reject});
      getSpreadsheetWorker().postMessage({id, type, ...payload}, transfer);
    });
  }
  function decodeText() {
    const text = new TextDecoder($<HTMLSelectElement>("#textEncoding").value).decode(textBytes, {stream: textTruncated});
    const pre = document.createElement("pre");
    pre.className = "preview-text";
    pre.textContent = text;
    content.replaceChildren(pre);
    message(textTruncated ? "文件较大，仅显示前 256 KiB；下载可查看完整内容。" : "文本预览 · 乱码时可切换编码");
  }
  $<HTMLSelectElement>("#textEncoding").addEventListener("change", decodeText, {signal: controller.signal});

  async function showPDF() {
    message("正在载入 PDF…");
    const pdfjs = await loadPDF();
    if (controller.signal.aborted) return;
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
    let pdf: PDFDocument;
    try { pdf = await pdfTask.promise; }
    catch (error) {
      message(error instanceof Error && error.name === "PasswordException" ? "此 PDF 需要密码，请下载后打开。" : "PDF 无法读取，文件可能已损坏，请下载后打开。");
      return;
    }
    if (controller.signal.aborted) return;
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
    let pageIndex = 1, busy = false, renderedPage: PDFPage | undefined;
    async function render() {
      if (busy || controller.signal.aborted) return;
      busy = true;
      [previous, next, pageNumber, zoom].forEach(node => node.disabled = true);
      pageNumber.value = String(pageIndex);
      message(`正在显示第 ${pageIndex} 页…`);
      try {
        renderedPage?.cleanup();
        renderedPage = await pdf.getPage(pageIndex);
        if (controller.signal.aborted) { renderedPage.cleanup(); return; }
        const base = renderedPage.getViewport({scale: 1});
        const scale = zoom.value === "fit" ? Math.min((content.clientWidth - 24) / base.width, 2) : Number(zoom.value);
        const viewport = renderedPage.getViewport({scale: Math.max(0.1, scale)});
        const pixels = Math.min(window.devicePixelRatio || 1, 2, Math.sqrt(16000000 / (viewport.width * viewport.height)));
        canvas.width = Math.ceil(viewport.width * pixels);
        canvas.height = Math.ceil(viewport.height * pixels);
        canvas.style.width = `${viewport.width}px`; canvas.style.height = `${viewport.height}px`;
        canvas.setAttribute("aria-label", `PDF 第 ${pageIndex} 页，共 ${pdf.numPages} 页`);
        const context = canvas.getContext("2d");
        if (!context) throw new Error("Canvas 不可用");
        renderTask = renderedPage.render({canvasContext: context, viewport, transform: [pixels, 0, 0, pixels, 0, 0]});
        await renderTask.promise;
        renderTask = undefined;
        if (controller.signal.aborted) return;
        message(`第 ${pageIndex} / ${pdf.numPages} 页 · 可翻页或缩放查看`);
        content.scrollTop = 0; content.scrollLeft = 0;
      } catch (error) {
        if (!controller.signal.aborted) message("此页无法显示，请尝试其他页面，或下载原文件。");
      } finally {
        busy = false;
        if (!controller.signal.aborted) {
          previous.disabled = pageIndex <= 1; next.disabled = pageIndex >= pdf.numPages;
          pageNumber.disabled = false; zoom.disabled = false;
        }
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
    window.addEventListener("resize", () => { clearTimeout(resizeTimer); resizeTimer = setTimeout(() => { if (zoom.value === "fit") render(); }, 150); }, {signal: controller.signal});
    await render();
  }

  async function showDOCX(response: Response) {
    const size = Number(response.headers.get("Content-Length"));
    if (Number.isFinite(size) && size > maxOfficePreviewBytes) {
      message("DOCX 文件超过 32 MiB 预览限制，请下载后打开。");
      return;
    }
    message("正在读取 Word 文档…");
    const data = await response.arrayBuffer();
    if (controller.signal.aborted) return;
    if (data.byteLength > maxOfficePreviewBytes) {
      message("DOCX 文件超过 32 MiB 预览限制，请下载后打开。");
      return;
    }
    const renderer = await loadDOCXRenderer();
    if (controller.signal.aborted) return;
    content.classList.add("preview-docx-container");
    const docxMount = document.createElement("div");
    content.replaceChildren(docxMount);
    message("正在解析 Word 文档…");
    await renderer.renderAsync(data, docxMount, docxMount, {
      breakPages: true,
      ignoreLastRenderedPageBreak: false,
      renderAltChunks: false,
      renderChanges: false,
      renderComments: false,
      useBase64URL: true,
    });
    if (controller.signal.aborted) return;
    for (const anchor of docxMount.querySelectorAll<HTMLAnchorElement>("a[href]")) {
      const href = anchor.getAttribute("href");
      if (!href) continue;
      if (href.startsWith("#")) continue;
      try {
        const target = new URL(href, location.href);
        if (!["http:", "https:", "mailto:", "tel:"].includes(target.protocol)) {
          anchor.removeAttribute("href");
          continue;
        }
        anchor.href = target.href;
        anchor.target = "_blank";
        anchor.rel = "noopener noreferrer";
      } catch {
        anchor.removeAttribute("href");
      }
    }
    if (!controller.signal.aborted) message("Word 在线预览 · 字体与分页效果可能因浏览器略有差异");
  }

  async function showSpreadsheet(response: Response) {
    const size = Number(response.headers.get("Content-Length"));
    if (Number.isFinite(size) && size > maxOfficePreviewBytes) {
      message("表格文件超过 32 MiB 预览限制，请下载后打开。");
      return;
    }
    message("正在读取表格文件…");
    const data = await response.arrayBuffer();
    if (controller.signal.aborted) return;
    if (data.byteLength > maxOfficePreviewBytes) {
      message("表格文件超过 32 MiB 预览限制，请下载后打开。");
      return;
    }
    const result = await requestSpreadsheet("load", {buffer: data}, [data]);
    if (controller.signal.aborted) return;
    content.classList.add("preview-spreadsheet-container");
    if (!result.names.length) {
      message("工作簿中没有可预览的工作表。");
      return;
    }
    if (result.names.length > 1) {
      spreadsheetTabs = document.createElement("nav");
      spreadsheetTabs.className = "preview-sheet-tabs";
      spreadsheetTabs.setAttribute("aria-label", "工作表");
      content.before(spreadsheetTabs);
      for (const name of result.names) {
        const button = document.createElement("button");
        button.type = "button";
        button.className = "secondary";
        button.textContent = name;
        button.setAttribute("aria-current", String(name === result.sheet?.name));
        button.addEventListener("click", () => loadSheet(name), {signal: controller.signal});
        spreadsheetTabs.append(button);
      }
    }
    if (result.sheet) renderSheet(result.sheet, result.hasMoreSheets);
  }

  async function loadSheet(name: string) {
    message(`正在读取工作表「${name}」…`);
    spreadsheetTabs?.querySelectorAll("button").forEach(button => {
      button.setAttribute("aria-current", String(button.textContent === name));
    });
    try {
      const result = await requestSpreadsheet("sheet", {sheetName: name});
      const selected = spreadsheetTabs?.querySelector('button[aria-current="true"]')?.textContent;
      if (!controller.signal.aborted && selected === name && result.sheet) renderSheet(result.sheet, false);
    } catch (error) {
      if (!controller.signal.aborted) message(errorMessage(error) || "工作表无法显示。");
    }
  }

  function renderSheet(sheet: Sheet, hasMoreSheets: boolean) {
    const wrapper = document.createElement("div");
    wrapper.className = "preview-sheet-table-scroll";
    const table = document.createElement("table");
    table.className = "preview-sheet-table";
    const head = document.createElement("thead");
    const heading = document.createElement("tr");
    const corner = document.createElement("th");
    corner.scope = "col";
    heading.append(corner);
    sheet.columns.forEach(column => {
      const cell = document.createElement("th");
      cell.scope = "col";
      cell.textContent = column;
      heading.append(cell);
    });
    head.append(heading);
    const body = document.createElement("tbody");
    sheet.rows.forEach((row, index) => {
      const line = document.createElement("tr");
      const number = document.createElement("th");
      number.scope = "row";
      number.textContent = String(sheet.startRow + index);
      line.append(number);
      row.forEach(value => {
        const cell = document.createElement("td");
        cell.textContent = value;
        line.append(cell);
      });
      body.append(line);
    });
    table.append(head, body);
    wrapper.append(table);
    content.replaceChildren(wrapper);
    content.classList.add("preview-spreadsheet-container");
    if (!sheet.rows.length) message(`「${sheet.name}」为空工作表。`);
    else {
      const limits = [];
      if (sheet.truncatedRows) limits.push("仅显示前 250 行");
      if (sheet.truncatedColumns) limits.push("仅显示前 50 列");
      if (hasMoreSheets) limits.push("仅展示前 20 个工作表");
      message(`工作表「${sheet.name}」 · ${sheet.rows.length} 行 × ${sheet.columns.length} 列${limits.length ? ` · ${limits.join("、")}` : ""}`);
    }
  }

  async function load() {
    if (!path) { message("未指定文件，请返回文件列表选择文件。"); return; }
    try {
      const isDOCX = /\.docx$/i.test(path);
      const isSpreadsheet = /\.(?:xlsx|xls)$/i.test(path);
      const response = await fetch(resource, {method: isDOCX || isSpreadsheet ? "GET" : "HEAD", cache: "no-store", signal: controller.signal});
      if (controller.signal.aborted) return;
      if (!response.ok) {
        message(response.status === 415 ? "此文件暂不支持浏览器预览，请下载后打开。" : response.status === 404 ? "文件已被移动或删除，请返回文件列表刷新。" : "无法读取文件，请返回文件列表重试。");
        return;
      }
      const type = response.headers.get("Content-Type") || "";
      if (isDOCX && type.startsWith("application/vnd.openxmlformats-officedocument.wordprocessingml.document")) {
        await showDOCX(response);
      } else if (isSpreadsheet && (type === "application/vnd.ms-excel" || type.startsWith("application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"))) {
        await showSpreadsheet(response);
      } else if (type.startsWith("image/")) {
        const image = document.createElement("img");
        image.className = "preview-image";
        image.alt = path.split("/").pop() || path;
        image.addEventListener("load", () => message("点击图片切换原始尺寸，可滚动查看细节。"), {signal: controller.signal});
        image.addEventListener("error", () => message("图片无法显示，可能格式不受浏览器支持或文件已损坏，请下载后打开。"), {signal: controller.signal});
        image.tabIndex = 0;
        image.setAttribute("role", "button");
        image.setAttribute("aria-label", `${path.split("/").pop()}，切换原始尺寸`);
        image.setAttribute("aria-pressed", "false");
        function toggleSize() { image.setAttribute("aria-pressed", String(image.classList.toggle("original-size"))); }
        image.addEventListener("click", toggleSize, {signal: controller.signal});
        image.addEventListener("keydown", event => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); toggleSize(); } }, {signal: controller.signal});
        image.src = resource;
        content.append(image);
      } else if (type.startsWith("video/") || type.startsWith("audio/")) {
        const media = document.createElement(type.startsWith("video/") ? "video" : "audio");
        media.controls = true;
        media.preload = "metadata";
        media.setAttribute("playsinline", "");
        media.className = "preview-media";
        media.addEventListener("loadedmetadata", () => message("使用播放器播放、调节音量或拖动进度。"), {signal: controller.signal});
        media.addEventListener("error", () => message("浏览器无法播放此文件的编码或文件已损坏，请下载后打开。"), {signal: controller.signal});
        media.src = resource;
        content.append(media);
      } else if (type.startsWith("application/pdf")) {
        await showPDF();
      } else if (type.startsWith("text/plain")) {
        const result = await fetch(resource, {headers: {Range: `bytes=0-${textLimit - 1}`}, cache: "no-store", signal: controller.signal});
        if (!result.ok) throw new Error("text");
        // The server honors Range, keeping large logs out of browser memory.
        const bytes = await result.arrayBuffer();
        if (controller.signal.aborted) return;
        textBytes = new Uint8Array(bytes);
        textTruncated = Number(response.headers.get("Content-Length")) > textBytes.length;
        if (textBytes[0] === 0xff && textBytes[1] === 0xfe) $<HTMLSelectElement>("#textEncoding").value = "utf-16le";
        else if (textBytes[0] === 0xfe && textBytes[1] === 0xff) $<HTMLSelectElement>("#textEncoding").value = "utf-16be";
        $("#textTools").classList.remove("hidden");
        decodeText();
      } else {
        message("此文件暂不支持浏览器预览，请下载后打开。");
      }
    } catch (error) {
      if (!(error instanceof Error && error.name === "AbortError")) message("读取失败，请刷新重试，或下载原文件。");
    }
  }
  function destroy() {
    if (controller.signal.aborted) return;
    controller.abort();
    clearTimeout(resizeTimer);
    renderTask?.cancel();
    pdfTask?.destroy().catch(() => {});
    spreadsheetWorker?.terminate();
    spreadsheetWorker = undefined;
    for (const request of spreadsheetRequests.values()) request.reject(new Error("预览已关闭"));
    spreadsheetRequests.clear();
    spreadsheetTabs?.remove();
    spreadsheetTabs = undefined;
    content.querySelectorAll<HTMLMediaElement>("video, audio").forEach(media => { media.pause(); media.removeAttribute("src"); media.load(); });
    content.replaceChildren();
    content.classList.remove("preview-docx-container", "preview-spreadsheet-container", "pdf-canvas-container");
    content.parentElement?.querySelector(".preview-pdf-tools")?.remove();
  }
  load();
  return {destroy};
}
