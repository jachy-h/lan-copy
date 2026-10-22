const $ = (selector) => document.querySelector(selector);
const textInput = $("#textInput");
const copyText = LanCopyFileUI.copyText;
let toastTimer;
let refreshTimer;
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

async function loadTexts() {
	try {
		const response = await fetch("/api/texts", { cache: "no-store" });
		if (!response.ok) throw new Error("读取失败");
		const { texts } = await response.json();
		renderTexts(texts);
		window.dispatchEvent(new Event("lan-copy:data-updated"));
	} catch {
		window.dispatchEvent(new Event("lan-copy:data-failed"));
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

textInput.addEventListener("input", updateTextCount);
textInput.addEventListener("keydown", (event) => {
	if (event.key === "Enter" && (event.ctrlKey || event.metaKey)) {
		event.preventDefault();
		shareText();
	}
});
$("#shareTextBtn").addEventListener("click", shareText);
$("#refreshBtn").addEventListener("click", loadTexts);
let stopped = false;
window.addEventListener("lan-copy:shutdown", () => { stopped = true; clearInterval(refreshTimer); });
document.addEventListener("visibilitychange", () => { if (!document.hidden && !stopped) loadTexts(); });
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

updateTextCount();
loadTexts();
refreshTimer = setInterval(loadTexts, 60000);
