import { $, getInvoker } from "../../../shared/js/dom.js";

/**
 * Controller for file deletion modal dialog (#filedel-dlg).
 */
export class FileDeleteDialog {
	static init() {
		const dialog = $("#filedel-dlg");
		const input = $("#filedel-input");
		const message = $("#filedel-msg");
		if (!dialog || !input || !message) return;

		dialog.addEventListener("command", (e) => {
			if (e.command !== "show-modal") return;
			const invoker = getInvoker(e);
			if (!invoker) return;

			let filename = invoker.dataset.deleteFilename;
			if (!filename && invoker.id === "modalDeleteBtn") {
				const modalDeleteFilename = $("#modalDeleteFilename");
				filename = modalDeleteFilename ? modalDeleteFilename.value : "";
			}
			if (!filename) return;

			input.value = filename;
			message.textContent = `Delete file "${filename}" permanently? It will be removed from disk.`;
		});
	}
}
