import { $, getInvoker } from "../../../shared/js/dom.js";

/**
 * Controller for token/account deletion modal dialog (#dlg).
 */
export class DeleteDialog {
	static init() {
		const dialog = $("#dlg");
		const form = $("#dlgForm");
		const message = $("#dlgmsg");
		const targetPhraseEl = $("#deleteTargetPhrase");
		const input = $("#deleteConfirmInput");
		const pasteWarning = $("#deletePasteWarning");
		const submitBtn = $("#deleteSubmitBtn");

		if (!dialog || !form) return;

		let expectedPhrase = "";
		let step = "typing"; // "typing" | "countdown" | "ready" | "confirming"
		let countdownTimer = null;
		let confirmTimer = null;

		const resetDeleteState = () => {
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
				submitBtn.textContent = "Delete";
			}
		};

		const doDeleteAction = () => {
			if (step === "ready") {
				step = "confirming";
				if (submitBtn) submitBtn.textContent = "Really delete?";
				if (confirmTimer) clearTimeout(confirmTimer);
				confirmTimer = setTimeout(() => {
					if (step === "confirming") {
						step = "ready";
						if (submitBtn) submitBtn.textContent = "Delete";
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
				if (!invoker?.dataset.deleteId) return;

				const id = invoker.dataset.deleteId;
				const isSelf = invoker.dataset.isSelf === "true";
				expectedPhrase = `DELETE ${id}`;
				if (message) {
					if (isSelf) {
						message.textContent = `Delete your account (${id})? All your uploaded media will be permanently deleted and your access token will be removed.`;
					} else {
						message.textContent = `Delete token ${id}? All uploaded media for this token will be permanently deleted.`;
					}
				}
				if (targetPhraseEl) {
					targetPhraseEl.textContent = expectedPhrase;
				}
				if (input) {
					input.placeholder = expectedPhrase;
				}
				form.action = `/_/tokens/${id}/delete`;
				resetDeleteState();
			} else if (e.command === "--submit-delete") {
				e.preventDefault();
				doDeleteAction();
			}
		});

		dialog.addEventListener("close", resetDeleteState);

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
									submitBtn.textContent = "Delete";
								}
							}
						}, 1000);
					}
				} else {
					if (step !== "typing") {
						resetDeleteState();
					}
				}
			});
		}

		form.addEventListener("submit", (e) => {
			e.preventDefault();
			doDeleteAction();
		});
	}
}
