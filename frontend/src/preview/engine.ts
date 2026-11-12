import { previewFailure } from './errors'
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
  const toolsHost = root.querySelector<HTMLElement>("#previewTools");
  const mountTools = (node: HTMLElement) => { if (toolsHost) toolsHost.append(node); else content.before(node); };
  const resource = `/preview-files/${encodeURIComponent(path)}`;
  const controller = new AbortController();
  const textLimit = 256 * 1024;
  let pdfTask: PDFLoadingTask | undefined;
  let renderTask: PDFRenderTask | undefined;
  let resizeTimer: ReturnType<typeof setTimeout> | undefined;
  let docxObserver: ResizeObserver | undefined;
  let spreadsheetWorker: Worker | undefined;
  let spreadsheetTabs: HTMLElement | undefined;
  let spreadsheetRequestID = 0;
  const spreadsheetRequests = new Map<number, { resolve(value: SheetResult): void; reject(error: Error): void }>();
  let textBytes: Uint8Array = new Uint8Array();
  let textTruncated = false;
  let stage = "请求文件";
  let failureDetails: HTMLDetailsElement | undefined;

  function message(text: string) { if (!controller.signal.aborted) status.textContent = text; }
  function failure(error: unknown, failedStage = stage) {
    if (controller.signal.aborted) return;
    const result = previewFailure(failedStage, error);
    status.hidden = false;
    status.classList.add("preview-status-error");
    message(result.summary);
    if (!failureDetails) {
      failureDetails = document.createElement("details");
      failureDetails.className = "preview-error-details";
      content.before(failureDetails);
    }
    const summary = document.createElement("summary"); summary.textContent = "查看错误详情";
    const detail = document.createElement("pre");
    detail.textContent = `阶段：${failedStage}\n原因：${result.detail}\n浏览器：${navigator.userAgent}\n连接：${location.protocol} / ${window.isSecureContext ? "安全上下文" : "普通局域网连接"}`;
    failureDetails.replaceChildren(summary, detail);
  }
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
    stage = "加载 PDF 组件";
    const pdfjs = await loadPDF();
    if (controller.signal.aborted) return;
    pdfjs.GlobalWorkerOptions.workerSrc = "/vendor/pdfjs/legacy/build/pdf.worker.min.mjs";
    stage = "启动 PDF 解析并读取文档";
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
      failure(error);
      return;
    }
    if (controller.signal.aborted) return;
    const tools = document.createElement("nav");
    tools.className = "preview-pdf-tools";
    tools.setAttribute("aria-label", "PDF 翻页与缩放");
    const pageNumber = document.createElement("input");
    pageNumber.type = "number"; pageNumber.min = "1"; pageNumber.max = String(pdf.numPages);
    pageNumber.setAttribute("aria-label", "PDF 页码");
    const total = document.createElement("span"); total.textContent = `/ ${pdf.numPages} 页`;
    const zoom = document.createElement("select"); zoom.setAttribute("aria-label", "PDF 缩放");
    [["fit", "适合宽度"], ["1", "100%"], ["1.5", "150%"], ["2", "200%"]].forEach(([value, text]) => {
      const option = document.createElement("option"); option.value = value; option.textContent = text; zoom.append(option);
    });
    tools.setAttribute("aria-label", "PDF 连续阅读与缩放");
    const hint = document.createElement("span"); hint.textContent = "连续阅读";
    tools.append(pageNumber, total, hint, zoom);
    mountTools(tools);
    content.classList.add("pdf-canvas-container");
    let generation = 0, busy = false;
    const wanted = new Set<number>();
    const frames = Array.from({length: pdf.numPages}, (_, i) => {
      const frame = document.createElement("div");
      frame.className = "preview-pdf-page";
      frame.dataset.page = String(i + 1);
      frame.setAttribute("aria-label", `PDF 第 ${i + 1} 页`);
      content.append(frame);
      return frame;
    });
    let baseWidth = 612, baseHeight = 792;
    const sizes = new Map<number, {width: number; height: number}>();
    function scaleFor(width: number) {
      const style = getComputedStyle(content);
      const available = content.clientWidth - parseFloat(style.paddingLeft || "0") - parseFloat(style.paddingRight || "0");
      return zoom.value === "fit" ? Math.max(.1, Math.min(available / width, 2)) : Number(zoom.value);
    }
    function sizeFrame(index: number) {
      const size = sizes.get(index) || {width: baseWidth, height: baseHeight};
      const scale = scaleFor(size.width);
      frames[index].style.width = `${size.width * scale}px`;
      frames[index].style.height = `${size.height * scale}px`;
    }
    function currentPage() {
      const top = content.getBoundingClientRect().top;
      // Pick the page that owns most of the reading viewport. A fixed 200px
      // threshold misreports the last page when the viewport is taller than a
      // scaled page and the container cannot scroll it all the way to the top.
      const focus = top + Math.min(320, content.clientHeight * 0.4);
      const index = frames.findIndex(frame => frame.getBoundingClientRect().bottom > focus);
      return Math.max(0, index);
    }
    function updatePosition() {
      pageNumber.value = String(currentPage() + 1);
    }
    async function pump() {
      if (busy || controller.signal.aborted) return;
      busy = true;
      try {
        for (const index of wanted) {
          const frame = frames[index];
          if (frame.childElementCount) continue;
          const version = generation;
          let page: PDFPage | undefined;
          try {
            page = await pdf.getPage(index + 1);
            if (controller.signal.aborted) return;
            if (version !== generation || !wanted.has(index)) continue;
            const base = page.getViewport({scale: 1});
            sizes.set(index, base); sizeFrame(index);
            const viewport = page.getViewport({scale: scaleFor(base.width)});
            const pixels = Math.min(window.devicePixelRatio || 1, 2, Math.sqrt(8000000 / (viewport.width * viewport.height)));
            const canvas = document.createElement("canvas");
            canvas.width = Math.ceil(viewport.width * pixels); canvas.height = Math.ceil(viewport.height * pixels);
            canvas.style.width = `${viewport.width}px`; canvas.style.height = `${viewport.height}px`;
            canvas.setAttribute("role", "img"); canvas.setAttribute("aria-label", `PDF 第 ${index + 1} 页，共 ${pdf.numPages} 页`);
            const context = canvas.getContext("2d");
            if (!context) throw new Error("Canvas 不可用");
            renderTask = page.render({canvasContext: context, viewport, transform: [pixels, 0, 0, pixels, 0, 0]});
            await renderTask.promise;
            if (controller.signal.aborted) return;
            if (version === generation && wanted.has(index)) {
              frame.replaceChildren(canvas);
              if (!failureDetails) {
                message(`第 ${currentPage() + 1} / ${pdf.numPages} 页 · 连续阅读`);
                status.hidden = true;
              }
            }
          } catch (error) {
            if (!controller.signal.aborted && version === generation && wanted.has(index)) {
              frame.textContent = `第 ${index + 1} 页无法显示，请下载原文件。`;
              failure(error, `渲染 PDF 第 ${index + 1} 页`);
            }
          } finally { renderTask = undefined; page?.cleanup(); }
        }
      } finally { busy = false; }
      // A zoom/resize may invalidate a page while its render is pending.
      if (!controller.signal.aborted && [...wanted].some(index => !frames[index].childNodes.length)) void pump();
    }
    stage = "读取 PDF 首页";
    const first = await pdf.getPage(1);
    if (controller.signal.aborted) { first.cleanup(); return; }
    const base = first.getViewport({scale: 1}); baseWidth = base.width; baseHeight = base.height;
    first.cleanup();
    frames.forEach((_, index) => sizeFrame(index));
    pageNumber.value = "1";
    stage = "初始化 PDF 连续阅读";
    function updateVisiblePages() {
      const bounds = content.getBoundingClientRect();
      frames.forEach((frame, index) => {
        const rect = frame.getBoundingClientRect();
        if (rect.bottom >= bounds.top - 400 && rect.top <= bounds.bottom + 400) wanted.add(index);
        else { wanted.delete(index); frame.replaceChildren(); }
      });
      void pump();
    }
    if (typeof IntersectionObserver !== "undefined") {
      const observer = new IntersectionObserver(entries => {
        for (const entry of entries) {
          const index = Number((entry.target as HTMLElement).dataset.page) - 1;
          if (entry.isIntersecting) wanted.add(index);
          else { wanted.delete(index); frames[index].replaceChildren(); }
        }
        void pump();
      }, {root: content, rootMargin: "400px 0px"});
      frames.forEach(frame => observer.observe(frame));
      controller.signal.addEventListener("abort", () => observer.disconnect(), {once: true});
    } else {
      content.addEventListener("scroll", updateVisiblePages, {signal: controller.signal, passive: true});
      updateVisiblePages();
    }
    content.addEventListener("scroll", updatePosition, {signal: controller.signal, passive: true});
    pageNumber.addEventListener("change", () => {
      const value = Number(pageNumber.value);
      if (!Number.isInteger(value) || value < 1 || value > pdf.numPages) { updatePosition(); return; }
      content.scrollTop += frames[value - 1].getBoundingClientRect().top - content.getBoundingClientRect().top;
      pageNumber.value = String(value);
    }, {signal: controller.signal});
    function relayout() {
      const index = currentPage();
      const fraction = (content.getBoundingClientRect().top - frames[index].getBoundingClientRect().top) / Math.max(1, frames[index].offsetHeight);
      generation++; renderTask?.cancel();
      frames.forEach((frame, i) => { frame.replaceChildren(); sizeFrame(i); });
      content.scrollTop += frames[index].getBoundingClientRect().top - content.getBoundingClientRect().top + fraction * frames[index].offsetHeight;
      if (typeof IntersectionObserver === "undefined") updateVisiblePages();
      else void pump();
    }
    zoom.addEventListener("change", relayout, {signal: controller.signal});
    window.addEventListener("resize", () => { clearTimeout(resizeTimer); resizeTimer = setTimeout(relayout, 150); }, {signal: controller.signal});
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
    // Preserve paper layout while reserving only its scaled visual size.
    const pages = [...docxMount.querySelectorAll<HTMLElement>("section.docx")].map(page => {
      const frame = document.createElement("div");
      frame.className = "preview-docx-page";
      page.before(frame);
      frame.append(page);
      return {page, frame};
    });
    function fitPages() {
      if (controller.signal.aborted) return;
      const wrapper = docxMount.querySelector<HTMLElement>(".docx-wrapper");
      if (!wrapper) return;
      for (const {page, frame} of pages) {
        const scale = Math.min(1, wrapper.clientWidth / Math.max(1, page.offsetWidth));
        page.style.transformOrigin = "top left";
        page.style.transform = `scale(${scale})`;
        frame.style.width = `${page.offsetWidth * scale}px`;
        frame.style.height = `${page.offsetHeight * scale}px`;
      }
    }
    fitPages();
    if (typeof ResizeObserver !== "undefined") {
      docxObserver = new ResizeObserver(fitPages);
      docxObserver.observe(content);
      pages.forEach(({page}) => docxObserver!.observe(page));
    }
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
      mountTools(spreadsheetTabs);
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
        failure(new Error(`HTTP ${response.status}：${response.status === 404 ? "文件已被移动或删除" : response.status === 415 ? "此文件类型不支持预览" : response.statusText || "文件请求失败"}`));
        return;
      }
      const type = response.headers.get("Content-Type") || "";
      stage = "读取或解析文件";
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
      if (!(error instanceof Error && error.name === "AbortError")) failure(error);
    }
  }
  function destroy() {
    if (controller.signal.aborted) return;
    controller.abort();
    failureDetails?.remove();
    status.hidden = false;
    status.classList.remove("preview-status-error");
    clearTimeout(resizeTimer);
    docxObserver?.disconnect();
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
