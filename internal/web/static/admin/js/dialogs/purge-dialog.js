import { $, getInvoker } from "../../../shared/js/dom.js";

/**
 * Controller for token media purging modal dialog (#purgedlg).
 */
export class PurgeDialog {
	static init() {
		const dialog = $("#purgedlg");
		const form = $("#purgeForm");
		const message = $("#purgedlgmsg");
		const targetPhraseEl = $("#purgeTargetPhrase");
		const input = $("#purgeConfirmInput");
		const pasteWarning = $("#purgePasteWarning");
		const submitBtn = $("#purgeSubmitBtn");

		if (!dialog || !form) return;

		let expectedPhrase = "";
		let step = "typing"; // "typing" | "countdown" | "ready" | "confirming"
		let countdownTimer = null;
		let confirmTimer = null;

		const resetPurgeState = () => {
			if (countdownTimer) {
				clearInterval(countdownTimer);
				countdownTimer = null;
			}
			if (confirmTimer) {
				clearTimeout(confirmTimer);
				confirmTimer = null;
			}
			step = "typing";
			if (input) input.value = "";
			if (pasteWarning) pasteWarning.hidden = true;
			if (submitBtn) {
				submitBtn.disabled = true;
				submitBtn.textContent = "Purge";
			}
		};

		const doPurgeAction = () => {
			if (step === "ready") {
				step = "confirming";
				if (submitBtn) submitBtn.textContent = "Really purge?";
				if (confirmTimer) clearTimeout(confirmTimer);
				confirmTimer = setTimeout(() => {
					if (step === "confirming") {
						step = "ready";
						if (submitBtn) submitBtn.textContent = "Purge";
					}
				}, 6000);
			} else if (step === "confirming") {
				if (confirmTimer) clearTimeout(confirmTimer);
				if (input && input.value.trim() === expectedPhrase) {
					form.submit();
				}
			}
		};

		dialog.addEventListener("command", (e) => {
			if (e.command === "show-modal") {
				const invoker = getInvoker(e);
				if (!invoker?.dataset.purgeId) return;

				const id = invoker.dataset.purgeId;
				expectedPhrase = `PURGE ${id}`;
				if (message) {
					message.textContent = `Schedule permanent deletion of all media for token ${id}? A 10-minute cancellation window will begin.`;
				}
				if (targetPhraseEl) {
					targetPhraseEl.textContent = expectedPhrase;
				}
				if (input) {
					input.placeholder = expectedPhrase;
				}
				form.action = `/_/tokens/${id}/purge-media`;
				resetPurgeState();
			} else if (e.command === "--submit-purge") {
				e.preventDefault();
				doPurgeAction();
			}
		});

		dialog.addEventListener("close", resetPurgeState);

		if (input) {
			input.addEventListener("paste", (e) => {
				e.preventDefault();
				if (pasteWarning) pasteWarning.hidden = false;
			});

			input.addEventListener("drop", (e) => {
				e.preventDefault();
				if (pasteWarning) pasteWarning.hidden = false;
			});

			input.addEventListener("input", () => {
				if (pasteWarning) pasteWarning.hidden = true;
				const val = input.value.trim();

				if (val === expectedPhrase) {
					if (step === "typing") {
						step = "countdown";
						let remaining = 5;
						if (submitBtn) {
							submitBtn.disabled = true;
							submitBtn.textContent = `Wait ${remaining}s`;
						}
						if (countdownTimer) clearInterval(countdownTimer);
						countdownTimer = setInterval(() => {
							remaining--;
							if (remaining > 0) {
								if (submitBtn) submitBtn.textContent = `Wait ${remaining}s`;
							} else {
								clearInterval(countdownTimer);
								countdownTimer = null;
								step = "ready";
								if (submitBtn) {
									submitBtn.disabled = false;
									submitBtn.textContent = "Purge";
								}
							}
						}, 1000);
					}
				} else {
					if (step !== "typing") {
						resetPurgeState();
					}
				}
			});
		}

		form.addEventListener("submit", (e) => {
			e.preventDefault();
			doPurgeAction();
		});
	}
}
