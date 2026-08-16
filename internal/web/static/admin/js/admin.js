import { ContextMenu } from "../../shared/js/context-menu.js";
import { DialogController } from "../../shared/js/dialog.js";
import { $ } from "../../shared/js/dom.js";
import { SelectDropdown } from "../../shared/js/dropdown.js";
import { DeleteDialog } from "./dialogs/delete-dialog.js";
import { FileDeleteDialog } from "./dialogs/file-dialog.js";
import { LimitsDialog } from "./dialogs/limits-dialog.js";
import { PurgeDialog } from "./dialogs/purge-dialog.js";
import { RenameDialog } from "./dialogs/rename-dialog.js";
import { RoleDialog } from "./dialogs/role-dialog.js";
import { QuotaValidator } from "./quota-validator.js";
import { SecretCard } from "./secret-card.js";

/**
 * Main controller for the Admin Dashboard and User management pages.
 */
export class AdminDashboard {
	#labelInput;
	#errorContainer;
	#form;

	constructor() {
		DialogController.init();
		SelectDropdown.init();
		ContextMenu.init();

		this.#labelInput = $('input[name="label"]');
		this.#errorContainer = $("#label-err");
		this.#form = this.#labelInput?.form;

		this.#initLabelValidation();
		SecretCard.init();
		DeleteDialog.init();
		PurgeDialog.init();
		FileDeleteDialog.init();
		RoleDialog.init();
		RenameDialog.init();
		LimitsDialog.init();
		this.#initGiveawayDialog();
		this.#initInvitePolicyForm();
		this.#initSearchBox();

		QuotaValidator.initQuotaForm($("#globalForm"), $("#global-err"));
		QuotaValidator.initQuotaForm($("#limForm"), $("#lim-err"));

		$(".quota-details > summary")?.addEventListener("mousedown", (e) => {
			if (e.detail > 1) e.preventDefault();
		});
	}

	#initLabelValidation() {
		QuotaValidator.setupInPlaceLabelValidation(
			this.#labelInput,
			this.#errorContainer,
			this.#form,
		);

		const inviteInput = $("#inviteInput");
		const inviteErr = $("#invite-err");
		const inviteForm = $("#inviteForm");
		QuotaValidator.setupInPlaceLabelValidation(
			inviteInput,
			inviteErr,
			inviteForm,
		);
	}

	validateLabel() {
		return QuotaValidator.setupInPlaceLabelValidation(
			this.#labelInput,
			this.#errorContainer,
			this.#form,
		)();
	}

	#initGiveawayDialog() {
		const modeSelect = $("#giveawayMode");
		const poolGroup = $("#giveawayPoolGroup");

		if (modeSelect && poolGroup) {
			modeSelect.addEventListener("change", () => {
				poolGroup.hidden = modeSelect.value !== "random";
			});
		}
	}

	#initInvitePolicyForm() {
		const modeSelect = $("#schedModeSelect");
		const poolGroup = $("#schedPoolGroup");
		if (!modeSelect || !poolGroup) return;

		modeSelect.addEventListener("change", () => {
			poolGroup.hidden = modeSelect.value !== "random";
		});
	}

	#initSearchBox() {
		const searchForm = $("#searchForm");
		const searchInput = $("#searchInput");
		const searchClear = $("#searchClear");

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
	}
}

document.addEventListener("DOMContentLoaded", () => new AdminDashboard());
