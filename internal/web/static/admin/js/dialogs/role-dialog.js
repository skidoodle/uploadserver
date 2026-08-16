import { $, getInvoker } from "../../../shared/js/dom.js";

/**
 * Controller for token role change modal dialog (#roledlg).
 */
export class RoleDialog {
	static init() {
		const dialog = $("#roledlg");
		const form = $("#roleForm");
		const targetInput = $("#roleTargetInput");
		const message = $("#roledlgmsg");
		if (!dialog || !form || !targetInput || !message) return;

		dialog.addEventListener("command", (e) => {
			if (e.command !== "show-modal") return;
			const invoker = getInvoker(e);
			if (!invoker?.dataset.roleId) return;

			const id = invoker.dataset.roleId;
			const targetRole = invoker.dataset.roleTarget;
			const label = invoker.dataset.roleLabel;

			targetInput.value = targetRole;
			message.textContent = `${label} for token ${id}?`;
			form.action = `/_/tokens/${id}/role`;
		});
	}
}
