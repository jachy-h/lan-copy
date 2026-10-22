(() => {
const $ = selector => document.querySelector(selector);
const accessUrl = $("#accessUrl");
const accessAddress = $("#accessAddress");
const qrCode = $("#qrCode");
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
	let accessInfo = {};
	try {
		const response = await fetch("/api/access", { cache: "no-store" });
		if (!response.ok) throw new Error("读取访问地址失败");
		const data = await response.json();
		if (Array.isArray(data.urls)) lanUrls = data.urls;
		accessInfo = data;
	} catch {}

	const currentOrigin = window.location.origin;
	const loopbackHosts = new Set(["localhost", "127.0.0.1", "::1", "[::1]"]);
	const candidates = loopbackHosts.has(window.location.hostname)
		? (lanUrls.length ? lanUrls : [currentOrigin])
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
        if (!lanUrls.length && loopbackHosts.has(window.location.hostname)) {
            $("#accessHint").textContent = "当前仅本机访问。请监听所有网卡（-listen :8080）并允许防火墙访问，手机才能扫码连接。";
            qrCode.setAttribute("aria-label", "本机访问二维码，暂不可用于手机连接");
        }
	} else {
		accessAddress.textContent = "未找到可访问地址";
		accessAddress.removeAttribute("href");
		qrCode.textContent = "未找到可访问地址";
		qrCode.className = "qr-code error";
	}
	return accessInfo;
}


accessUrl.addEventListener("change", () => renderAccessQRCode(accessUrl.value));
$("#copyAccessUrl").addEventListener("click", async () => {
 const copied = await LanCopyFileUI.copyText(accessUrl.value);
 LanCopyFileUI.notify(copied ? "访问地址已复制" : "复制失败，请手动选择", !copied);
});
const root = $("#shareAccess"), button = $("#accessButton"), panel = $("#accessPanel");
let pinned = false, closeTimer;
function setOpen(open) {
 clearTimeout(closeTimer);
 if (open) panel.style.setProperty("--access-top", `${$(".topbar").getBoundingClientRect().bottom + 8}px`);
 panel.hidden = !open;
 button.setAttribute("aria-expanded", String(open));
}
root.addEventListener("pointerenter", event => { if (event.pointerType !== "touch") setOpen(true); });
root.addEventListener("pointerleave", () => { if (!pinned) closeTimer = setTimeout(() => setOpen(false), 200); });
root.addEventListener("focusin", () => setOpen(true));
root.addEventListener("focusout", event => { if (!root.contains(event.relatedTarget)) { pinned = false; setOpen(false); } });
button.addEventListener("click", () => { pinned = !pinned; setOpen(pinned); });
document.addEventListener("click", event => { if (!root.contains(event.target)) { pinned = false; setOpen(false); } });
document.addEventListener("keydown", event => { if (event.key === "Escape" && !panel.hidden) { button.focus(); pinned = false; setOpen(false); } });
window.addEventListener("resize", () => { if (!panel.hidden) setOpen(true); });
window.lanCopyAccessReady = loadAccessInfo();
})();
