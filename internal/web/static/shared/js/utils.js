/**
 * Shared utility functions.
 */

export function formatBytes(bytes) {
	if (bytes === 0) return "0 B";
	const k = 1024;
	const sizes = ["B", "KB", "MB", "GB", "TB"];
	const i = Math.floor(Math.log(bytes) / Math.log(k));
	return `${parseFloat((bytes / k ** i).toFixed(1))} ${sizes[i]}`;
}

export function formatTime(sec) {
	if (Number.isNaN(sec) || !Number.isFinite(sec) || sec < 0) return "0:00";
	const s = Math.floor(sec % 60);
	const m = Math.floor((sec / 60) % 60);
	const h = Math.floor(sec / 3600);
	const ss = s < 10 ? `0${s}` : s;
	if (h > 0) {
		const mm = m < 10 ? `0${m}` : m;
		return `${h}:${mm}:${ss}`;
	}
	return `${m}:${ss}`;
}

export function debounce(fn, wait = 100) {
	let timeout;
	return function (...args) {
		clearTimeout(timeout);
		timeout = setTimeout(() => fn.apply(this, args), wait);
	};
}
