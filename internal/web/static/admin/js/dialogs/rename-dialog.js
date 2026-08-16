import { $, getInvoker } from "../../../shared/js/dom.js";
import { QuotaValidator } from "../quota-validator.js";

/**
 * Controller for token rename modal dialog (#renamedlg).
 */
export class RenameDialog {
	static init() {
		const dialog = $("#renamedlg");
		const form = $("#renameForm");
		const input = $("#renameInput");
		const errorEl = $("#rename-err");
		const target = $("#renameTarget");

		if (form && input) {
			QuotaValidator.setupInPlaceLabelValidation(input, errorEl, form);
		}

		dialog?.addEventListener("command", (e) => {
			if (e.command !== "show-modal") return;
			const invoker = getInvoker(e);
			if (!invoker?.dataset.renameId) return;

			const id = invoker.dataset.renameId;
			if (target) target.textContent = id;
			if (input) {
				input.value = invoker.dataset.label || "";
				input.classList.remove("invalid");
			}
			if (errorEl) errorEl.hidden = true;
			if (form) form.action = `/_/tokens/${id}/label`;
		});
	}
}
