const $ = (selector) => document.querySelector(selector);
const dropZone = $("#dropZone");
const fileInput = $("#fileInput");
const queue = $("#queue");
const progressBar = $("#progressBar");
const textInput = $("#textInput");
const accessUrl = $("#accessUrl");
const accessAddress = $("#accessAddress");
const qrCode = $("#qrCode");
let toastTimer;
let refreshTimer;
let fileEventSource;
let localActionsAvailable = false;
let textsByID = new Map();

function makeIcon(pathData) {
	const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
	const iconPath = document.createElementNS(
		"http://www.w3.org/2000/svg",
		"path",
	);
	svg.setAttribute("viewBox", "0 0 24 24");
	iconPath.setAttribute("d", pathData);
	svg.append(iconPath);
	return svg;
}

function formatBytes(bytes) {
	if (!bytes) return "0 B";
	const units = ["B", "KB", "MB", "GB", "TB"];
	const index = Math.min(
		Math.floor(Math.log(bytes) / Math.log(1024)),
		units.length - 1,
	);
	const value = bytes / 1024 ** index;
	return `${value.toFixed(index === 0 || value >= 10 ? 0 : 1)} ${units[index]}`;
}

function fileKind(name) {
	const extension = name.includes(".") ? name.split(".").pop() : "FILE";
	return extension.slice(0, 4) || "FILE";
}

function formatTime(value) {
	const date = new Date(value);
	const today = new Date();
	const sameDay = date.toDateString() === today.toDateString();
	return sameDay
		? `今天 ${date.toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit" })}`
		: date.toLocaleDateString("zh-CN", {
				month: "short",
				day: "numeric",
				hour: "2-digit",
				minute: "2-digit",
			});
}

function notify(message, error = false) {
	const toast = $("#toast");
	toast.textContent = message;
	toast.className = `toast show${error ? " error" : ""}`;
	clearTimeout(toastTimer);
	toastTimer = setTimeout(() => {
		toast.className = "toast";
	}, 2600);
}

async function loadFiles() {
	try {
		const response = await fetch("/api/files", { cache: "no-store" });
		if (!response.ok) throw new Error("读取失败");
		const { files } = await response.json();
		renderFiles(files);
	} catch {
		$("#summary").textContent = "无法连接到服务";
		notify("无法读取文件列表", true);
	}
}

async function loadTexts() {
	try {
		const response = await fetch("/api/texts", { cache: "no-store" });
		if (!response.ok) throw new Error("读取失败");
		const { texts } = await response.json();
		renderTexts(texts);
	} catch {
		$("#textSummary").textContent = "无法连接到服务";
		notify("无法读取共享文本", true);
	}
}

function renderTexts(texts) {
	const list = $("#textList");
	textsByID = new Map(texts.map((item) => [item.id, item.content]));
	$("#textSummary").textContent = texts.length
		? `${texts.length} 条文本 · 点击即可复制`
		: "等待文字传入";
	$("#textEmptyState").classList.toggle("hidden", texts.length > 0);
	list.replaceChildren(
		...texts.map((item, index) => {
			const row = document.createElement("article");
			row.className = "text-row";
			row.style.animationDelay = `${Math.min(index * 25, 150)}ms`;

			const content = document.createElement("pre");
			content.className = "text-content";
			content.textContent = item.content;
			const foot = document.createElement("div");
			foot.className = "text-row-foot";
			const created = document.createElement("time");
			created.dateTime = item.created;
			created.textContent = formatTime(item.created);
			const actions = document.createElement("div");
			actions.className = "actions";

			const copy = document.createElement("button");
			copy.type = "button";
			copy.className = "copy-btn";
			copy.dataset.textAction = "copy";
			copy.dataset.id = item.id;
			copy.title = "复制文本";
			copy.setAttribute("aria-label", copy.title);
			copy.append(makeIcon("M8 8h11v11H8zM5 16H4V5h11v1"));

			const remove = document.createElement("button");
			remove.type = "button";
			remove.dataset.textAction = "delete";
			remove.dataset.id = item.id;
			remove.title = "删除文本";
			remove.setAttribute("aria-label", remove.title);
			remove.append(makeIcon("M4 7h16M9 7V4h6v3m3 0-1 13H7L6 7m4 4v5m4-5v5"));
			actions.append(copy, remove);
			foot.append(created, actions);
			row.append(content, foot);
			return row;
		}),
	);
}

async function copyText(content) {
	try {
		if (navigator.clipboard?.writeText) {
			await navigator.clipboard.writeText(content);
			return true;
		}
	} catch {}
	const helper = document.createElement("textarea");
	helper.value = content;
	helper.style.position = "fixed";
	helper.style.opacity = "0";
	document.body.append(helper);
	helper.focus();
	helper.select();
	const copied = document.execCommand("copy");
	helper.remove();
	return copied;
}

function renderAccessQRCode(url) {
	accessAddress.textContent = url;
	accessAddress.href = url;
	try {
		qrCode.replaceChildren(QRCode.createSVG(url));
		qrCode.className = "qr-code";
		qrCode.setAttribute("aria-label", `扫描后访问 ${url}`);
	} catch (error) {
		qrCode.textContent = error.message || "二维码生成失败";
		qrCode.className = "qr-code error";
	}
}

async function loadAccessInfo() {
	let lanUrls = [];
	try {
		const response = await fetch("/api/access", { cache: "no-store" });
		if (!response.ok) throw new Error("读取访问地址失败");
		const data = await response.json();
		if (Array.isArray(data.urls)) lanUrls = data.urls;
		localActionsAvailable = data.local === true;
	} catch {}

	const currentOrigin = window.location.origin;
	const loopbackHosts = new Set(["localhost", "127.0.0.1", "::1"]);
	const candidates = loopbackHosts.has(window.location.hostname)
		? lanUrls
		: [currentOrigin, ...lanUrls];
	const urls = [
		...new Set(candidates.filter((url) => /^https?:\/\//.test(url))),
	];
	urls.sort((left, right) => {
		const leftPreferred = /^https?:\/\/192\./.test(left);
		const rightPreferred = /^https?:\/\/192\./.test(right);
		return Number(rightPreferred) - Number(leftPreferred);
	});

	accessUrl.replaceChildren();
	for (const url of urls) {
		const option = document.createElement("option");
		option.value = url;
		option.textContent = url;
		accessUrl.append(option);
	}
	accessUrl.disabled = urls.length < 2;
	accessUrl.classList.toggle("hidden", urls.length < 2);
	if (urls.length) {
		renderAccessQRCode(urls[0]);
	} else {
		accessAddress.textContent = "未找到可访问地址";
		accessAddress.removeAttribute("href");
		qrCode.textContent = "未找到可访问地址";
		qrCode.className = "qr-code error";
	}
}

function renderFiles(files) {
	const list = $("#fileList");
	const total = files.reduce((sum, file) => sum + file.size, 0);
	$("#summary").textContent = files.length
		? `${files.length} 个文件 · ${formatBytes(total)}`
		: "等待文件传入";
	$("#emptyState").classList.toggle("hidden", files.length > 0);
	list.replaceChildren(
		...files.map((file, index) => {
			const row = document.createElement("article");
			row.className = "file-row";
			row.style.animationDelay = `${Math.min(index * 25, 150)}ms`;

			const icon = document.createElement("div");
			icon.className = "file-icon";
			icon.textContent = fileKind(file.name);

			const main = document.createElement("div");
			main.className = "file-main";
			const name = document.createElement("div");
			name.className = "file-name";
			name.title = file.name;
			name.textContent = file.name;
			const meta = document.createElement("div");
			meta.className = "file-meta";
			meta.textContent = `${formatBytes(file.size)} · ${formatTime(file.modified)}`;
			main.append(name, meta);

			const actions = document.createElement("div");
			actions.className = "actions";
			if (localActionsAvailable) {
				const open = document.createElement("button");
				open.type = "button";
				open.className = "local-action";
				open.dataset.fileAction = "open";
				open.dataset.file = encodeURIComponent(file.name);
				open.title = `在本机打开 ${file.name}`;
				open.setAttribute("aria-label", open.title);
				open.append(makeIcon("M14 4h6v6m0-6-9 9M5 7v12h12v-6"));

				const folder = document.createElement("button");
				folder.type = "button";
				folder.className = "local-action";
				folder.dataset.fileAction = "folder";
				folder.dataset.file = encodeURIComponent(file.name);
				folder.title = `打开 ${file.name} 所在文件夹`;
				folder.setAttribute("aria-label", folder.title);
				folder.append(makeIcon("M3 6h7l2 2h9v10H3z"));
				actions.append(open, folder);
			}
			const download = document.createElement("a");
			download.href = `/files/${encodeURIComponent(file.name)}`;
			download.title = `下载 ${file.name}`;
			download.setAttribute("aria-label", download.title);
			download.append(makeIcon("M12 4v11m0 0 4-4m-4 4-4-4M5 19h14"));
			const remove = document.createElement("button");
			remove.type = "button";
			remove.dataset.delete = encodeURIComponent(file.name);
			remove.dataset.name = file.name;
			remove.title = `删除 ${file.name}`;
			remove.setAttribute("aria-label", remove.title);
			remove.append(makeIcon("M4 7h16M9 7V4h6v3m3 0-1 13H7L6 7m4 4v5m4-5v5"));
			actions.append(download, remove);

			row.append(icon, main, actions);
			return row;
		}),
	);
}

function upload(files) {
	if (!files.length) return;
	const form = new FormData();
	[...files].forEach((file) => form.append("files", file));
	const xhr = new XMLHttpRequest();
	queue.classList.remove("hidden");
	$("#queueTitle").textContent =
		files.length === 1 ? "正在传送文件" : `正在传送 ${files.length} 个文件`;
	$("#queueDetail").textContent = [...files]
		.map((file) => file.name)
		.join("、");
	progressBar.style.width = "0%";
	$("#queuePercent").textContent = "0%";

	xhr.upload.addEventListener("progress", (event) => {
		if (!event.lengthComputable) return;
		const percent = Math.round((event.loaded / event.total) * 100);
		progressBar.style.width = `${percent}%`;
		$("#queuePercent").textContent = `${percent}%`;
	});
	xhr.addEventListener("load", async () => {
		if (xhr.status >= 200 && xhr.status < 300) {
			progressBar.style.width = "100%";
			$("#queuePercent").textContent = "完成";
			notify(
				files.length === 1 ? "文件已传送" : `${files.length} 个文件已传送`,
			);
			await loadFiles();
			setTimeout(() => queue.classList.add("hidden"), 900);
		} else {
			let message = "上传失败";
			try {
				message = JSON.parse(xhr.responseText).error || message;
			} catch {}
			notify(message, true);
			$("#queueTitle").textContent = "传送失败";
		}
		fileInput.value = "";
	});
	xhr.addEventListener("error", () => notify("网络中断，请重试", true));
	xhr.open("POST", "/api/upload");
	xhr.send(form);
}

function updateTextCount() {
	const bytes = new TextEncoder().encode(textInput.value).length;
	const count = $("#textCount");
	count.textContent = `${formatBytes(bytes)} / 64 KiB`;
	count.classList.toggle("over-limit", bytes > 64 * 1024);
	return bytes;
}

async function shareText() {
	const content = textInput.value;
	const bytes = updateTextCount();
	if (!content.trim()) {
		notify("请输入要共享的文本", true);
		textInput.focus();
		return;
	}
	if (bytes > 64 * 1024) {
		notify("文本超过 64 KiB 限制", true);
		return;
	}

	const button = $("#shareTextBtn");
	button.disabled = true;
	button.textContent = "正在发布…";
	try {
		const response = await fetch("/api/texts", {
			method: "POST",
			headers: { "Content-Type": "application/json" },
			body: JSON.stringify({ content }),
		});
		if (!response.ok) {
			const body = await response.json().catch(() => ({}));
			throw new Error(body.error || "发布失败");
		}
		textInput.value = "";
		updateTextCount();
		notify("文本已共享");
		await loadTexts();
	} catch (error) {
		notify(error.message || "发布失败", true);
	} finally {
		button.disabled = false;
		button.textContent = "发布文本";
	}
}

function switchMode(mode) {
	const isFile = mode === "file";
	dropZone.classList.toggle("hidden", !isFile);
	$("#textPanel").classList.toggle("hidden", isFile);
	document.querySelectorAll("[data-mode]").forEach((button) => {
		const active = button.dataset.mode === mode;
		button.classList.toggle("active", active);
		button.setAttribute("aria-pressed", String(active));
	});
	if (!isFile) textInput.focus();
}

function loadAll() {
	return Promise.all([loadFiles(), loadTexts()]);
}

async function shutdownApp() {
	if (!confirm("确定关闭 Lan Copy 吗？关闭后，所有设备都将无法继续访问。")) {
		return;
	}
	const button = $("#shutdownBtn");
	button.disabled = true;
	try {
		const response = await fetch("/api/shutdown", {
			method: "POST",
			headers: { "X-Lan-Copy-Action": "shutdown" },
		});
		if (!response.ok) throw new Error("关闭失败");
		clearInterval(refreshTimer);
		fileEventSource?.close();
		const status = $("#serviceStatus");
		status.classList.add("offline");
		status.querySelector(".status-label").textContent = "服务已关闭";
		button.querySelector("span").textContent = "已关闭";
		notify("Lan Copy 已关闭，可以关闭此页面");
	} catch {
		button.disabled = false;
		notify("关闭失败，请重试", true);
	}
}

$("#chooseBtn").addEventListener("click", (event) => {
	event.stopPropagation();
	fileInput.click();
});
dropZone.addEventListener("click", () => fileInput.click());
fileInput.addEventListener("change", () => upload(fileInput.files));
["dragenter", "dragover"].forEach((type) =>
	dropZone.addEventListener(type, (event) => {
		event.preventDefault();
		dropZone.classList.add("dragging");
	}),
);
["dragleave", "drop"].forEach((type) =>
	dropZone.addEventListener(type, (event) => {
		event.preventDefault();
		dropZone.classList.remove("dragging");
	}),
);
dropZone.addEventListener("drop", (event) => upload(event.dataTransfer.files));
document.querySelectorAll("[data-mode]").forEach((button) => {
	button.addEventListener("click", () => switchMode(button.dataset.mode));
});
textInput.addEventListener("input", updateTextCount);
textInput.addEventListener("keydown", (event) => {
	if (event.key === "Enter" && (event.ctrlKey || event.metaKey)) {
		event.preventDefault();
		shareText();
	}
});
$("#shareTextBtn").addEventListener("click", shareText);
$("#refreshBtn").addEventListener("click", loadAll);
$("#shutdownBtn").addEventListener("click", shutdownApp);
accessUrl.addEventListener("change", () => renderAccessQRCode(accessUrl.value));
$("#copyAccessUrl").addEventListener("click", async () => {
	const copied = await copyText(accessUrl.value);
	notify(copied ? "访问地址已复制" : "复制失败，请手动选择", !copied);
});
$("#fileList").addEventListener("click", async (event) => {
	const actionButton = event.target.closest("[data-file-action]");
	if (actionButton) {
		const action = actionButton.dataset.fileAction;
		actionButton.disabled = true;
		try {
			const response = await fetch(
				`/api/files/${actionButton.dataset.file}/${action}`,
				{
					method: "POST",
					headers: { "X-Lan-Copy-Action": action },
				},
			);
			if (!response.ok) throw new Error("本机操作失败");
			notify(action === "open" ? "已在本机打开文件" : "已打开文件所在目录");
		} catch {
			notify("本机操作失败", true);
		} finally {
			actionButton.disabled = false;
		}
		return;
	}

	const button = event.target.closest("[data-delete]");
	if (!button || !confirm(`确定删除“${button.dataset.name}”吗？`)) return;
	const response = await fetch(`/api/files/${button.dataset.delete}`, {
		method: "DELETE",
	});
	if (response.ok) {
		notify("文件已删除");
		loadFiles();
	} else {
		notify("删除失败", true);
	}
});
$("#textList").addEventListener("click", async (event) => {
	const button = event.target.closest("[data-text-action]");
	if (!button) return;
	if (button.dataset.textAction === "copy") {
		const copied = await copyText(textsByID.get(button.dataset.id) || "");
		notify(copied ? "文本已复制" : "复制失败，请手动选择", !copied);
		return;
	}
	if (!confirm("确定删除这段共享文本吗？")) return;
	const response = await fetch(`/api/texts/${button.dataset.id}`, {
		method: "DELETE",
	});
	if (response.ok) {
		notify("共享文本已删除");
		loadTexts();
	} else {
		notify("删除失败", true);
	}
});

function watchFileChanges() {
	if (!window.EventSource) return;
	fileEventSource = new EventSource("/api/events");
	fileEventSource.addEventListener("files", loadFiles);
}

async function initialize() {
	await loadAccessInfo();
	await loadAll();
}

watchFileChanges();
initialize();
refreshTimer = setInterval(loadAll, 15000);
