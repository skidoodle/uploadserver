import { $, getInvoker } from "../../../shared/js/dom.js";
import { QuotaValidator } from "../quota-validator.js";

/**
 * Controller for token quota and limits modal dialog (#limdlg).
 */
export class LimitsDialog {
	static init() {
		const dialog = $("#limdlg");
		const form = $("#limForm");
		const errorEl = $("#lim-err");
		const target = $("#limTarget");

		if (form) {
			form.elements.bypass?.addEventListener("change", () =>
				LimitsDialog.applyExemptState(form, errorEl),
			);
		}

		dialog?.addEventListener("command", (e) => {
			if (e.command !== "show-modal") return;
			const invoker = getInvoker(e);
			if (!invoker?.dataset.limitId) return;

			const id = invoker.dataset.limitId;
			if (target) target.textContent = id;
			if (form) {
				form.action = `/_/tokens/${id}/limits`;
				if (form.elements.max_bytes)
					form.elements.max_bytes.value = invoker.dataset.maxBytes || "";
				if (form.elements.max_uploads)
					form.elements.max_uploads.value = invoker.dataset.maxUploads || "";
				if (form.elements.monthly_bytes)
					form.elements.monthly_bytes.value =
						invoker.dataset.monthlyBytes || "";
				if (form.elements.monthly_uploads)
					form.elements.monthly_uploads.value =
						invoker.dataset.monthlyUploads || "";
				if (form.elements.invites)
					form.elements.invites.value = invoker.dataset.invites || "";
				if (form.elements.bypass)
					form.elements.bypass.checked = invoker.dataset.bypass === "1";
				QuotaValidator.resetQuotaForm(form, errorEl);
				LimitsDialog.applyExemptState(form, errorEl);
			}
		});
	}

	static applyExemptState(form, errorEl) {
		const exempt = form.elements.bypass?.checked;
		form.querySelectorAll("input[data-kind]").forEach((input) => {
			input.disabled = exempt;
			if (exempt) input.classList.remove("invalid");
		});
		form.classList.toggle("exempt", exempt);
		if (exempt && errorEl) errorEl.textContent = "";
	}
}
