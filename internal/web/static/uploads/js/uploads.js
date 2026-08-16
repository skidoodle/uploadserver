import { FileDeleteDialog } from "../../admin/js/dialogs/file-dialog.js";
import { ContextMenu } from "../../shared/js/context-menu.js";
import { DialogController } from "../../shared/js/dialog.js";
import { $ } from "../../shared/js/dom.js";
import { MediaModal } from "./media-modal.js";

/**
 * Controller for the Uploads gallery page.
 */
export class UploadsPage {
	constructor() {
		DialogController.init();
		ContextMenu.init();
		FileDeleteDialog.init();

		this.mediaModal = new MediaModal();
		this.#initSearchBox();
	}

	#initSearchBox() {
		const searchForm = $("#searchForm");
		const searchInput = $("#searchInput");
		const searchClear = $("#searchClear");
		const modal = $("#imgModal");

		const doClear = () => {
			window.location.href = window.location.pathname;
		};

		searchClear?.addEventListener("click", doClear);

		searchForm?.addEventListener("command", (e) => {
			if (e.command === "--clear-search") {
				doClear();
			}
		});

		if (searchForm && searchInput) {
			searchForm.addEventListener("submit", (e) => {
				if (!searchInput.value.trim()) {
					e.preventDefault();
					doClear();
				}
			});
		}

		if (searchInput) {
			document.addEventListener("keydown", (e) => {
				if (modal?.open) return;
				if (
					e.key === "/" &&
					document.activeElement !== searchInput &&
					!["INPUT", "TEXTAREA", "SELECT"].includes(
						document.activeElement.tagName,
					)
				) {
					e.preventDefault();
					searchInput.focus();
				} else if (
					e.key === "Escape" &&
					document.activeElement === searchInput
				) {
					if (searchInput.value) {
						window.location.href = window.location.pathname;
					} else {
						searchInput.blur();
					}
				}
			});
		}
	}
}

document.addEventListener("DOMContentLoaded", () => new UploadsPage());
