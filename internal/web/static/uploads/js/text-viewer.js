import { formatBytes } from "../../shared/js/utils.js";

/**
 * Controller for plain text / code file previews in the media lightbox.
 */
export class TextViewer {
	constructor(opts) {
		const {
			modalTextBox,
			textTypeBadge,
			textStats,
			textWrapBtn,
			textCopyBtn,
			textLoading,
			textError,
			textErrorMsg,
			textScrollArea,
			textLineNumbers,
			modalTextPre,
			modalTextCode,
		} = opts;

		this.modalTextBox = modalTextBox;
		this.textTypeBadge = textTypeBadge;
		this.textStats = textStats;
		this.textWrapBtn = textWrapBtn;
		this.textCopyBtn = textCopyBtn;
		this.textLoading = textLoading;
		this.textError = textError;
		this.textErrorMsg = textErrorMsg;
		this.textScrollArea = textScrollArea;
		this.textLineNumbers = textLineNumbers;
		this.modalTextPre = modalTextPre;
		this.modalTextCode = modalTextCode;

		this.currentController = null;
		this.currentLoadedText = "";
		this.isTextWrapped = false;

		this.#initButtons();
	}

	load(item, ext) {
		if (!this.modalTextBox) return;

		this.modalTextBox.classList.remove("hidden");
		if (this.textTypeBadge)
			this.textTypeBadge.textContent = (ext || "TXT").toUpperCase();
		if (this.textStats) this.textStats.textContent = "Loading…";
		if (this.textLoading) this.textLoading.classList.remove("hidden");
		if (this.textError) this.textError.classList.add("hidden");
		if (this.textScrollArea) this.textScrollArea.classList.add("hidden");

		this.stop();

		this.currentController = new AbortController();
		const controller = this.currentController;

		(async () => {
			try {
				const resp = await fetch(item.url, { signal: controller.signal });
				if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
				const text = await resp.text();
				if (controller.signal.aborted) return;

				this.currentLoadedText = text;
				if (this.modalTextCode) {
					this.modalTextCode.textContent = text;
				} else if (this.modalTextPre) {
					this.modalTextPre.textContent = text;
				}

				const lines = text.split("\n");
				const lineCount = lines.length;
				const byteSize = new Blob([text]).size;
				if (this.textStats) {
					this.textStats.textContent = `${lineCount.toLocaleString()} line${lineCount !== 1 ? "s" : ""} · ${formatBytes(byteSize)}`;
				}

				if (this.textLineNumbers) {
					let lineHtml = "";
					for (let i = 1; i <= lineCount; i++) {
						lineHtml += `<span>${i}</span>\n`;
					}
					this.textLineNumbers.innerHTML = lineHtml;
				}

				if (this.textScrollArea) {
					this.textScrollArea.scrollTop = 0;
					this.textScrollArea.scrollLeft = 0;
				}

				if (this.textLoading) this.textLoading.classList.add("hidden");
				if (this.textError) this.textError.classList.add("hidden");
				if (this.textScrollArea) this.textScrollArea.classList.remove("hidden");
			} catch (err) {
				if (controller.signal.aborted) return;
				console.error("Failed to load text preview:", err);
				if (this.textLoading) this.textLoading.classList.add("hidden");
				if (this.textError) this.textError.classList.remove("hidden");
				if (this.textErrorMsg)
					this.textErrorMsg.textContent = `Unable to display text preview (${err.message || "network error"}).`;
				if (this.textStats) this.textStats.textContent = "Error";
			}
		})();
	}

	stop() {
		if (this.currentController) {
			this.currentController.abort();
			this.currentController = null;
		}
		this.currentLoadedText = "";
		if (this.modalTextBox) {
			this.modalTextBox.classList.add("hidden");
		}
	}

	#initButtons() {
		this.textCopyBtn?.addEventListener("click", async () => {
			if (!this.currentLoadedText) return;
			try {
				await navigator.clipboard.writeText(this.currentLoadedText);
				const orig = this.textCopyBtn.textContent;
				this.textCopyBtn.textContent = "✓ Copied";
				this.textCopyBtn.classList.add("btn-success");
				setTimeout(() => {
					this.textCopyBtn.textContent = orig;
					this.textCopyBtn.classList.remove("btn-success");
				}, 2000);
			} catch (err) {
				console.error("Clipboard copy failed:", err);
			}
		});

		this.textWrapBtn?.addEventListener("click", () => {
			this.isTextWrapped = !this.isTextWrapped;
			this.textWrapBtn.textContent = this.isTextWrapped
				? "Wrap: On"
				: "Wrap: Off";
			if (this.isTextWrapped) {
				this.modalTextPre?.classList.add("wrapped");
				this.textLineNumbers?.classList.add("wrapped");
			} else {
				this.modalTextPre?.classList.remove("wrapped");
				this.textLineNumbers?.classList.remove("wrapped");
			}
		});
	}
}
