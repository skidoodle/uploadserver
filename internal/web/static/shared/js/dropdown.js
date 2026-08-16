import { $$, getInvoker } from "./dom.js";

/**
 * Controller for custom select dropdown components (.csel).
 */
export class SelectDropdown {
	static init() {
		const closeAll = () => {
			$$(".csel.open").forEach((c) => {
				c.classList.remove("open");
			});
		};

		const toggleSelect = (btn) => {
			const container = btn?.closest(".csel");
			if (!container) return;
			const isOpen = container.classList.contains("open");
			closeAll();
			if (!isOpen) {
				container.classList.add("open");
			}
		};

		document.addEventListener("command", (e) => {
			if (e.command === "--toggle-select") {
				toggleSelect(getInvoker(e));
			}
		});

		document.addEventListener("click", (event) => {
			const btn = event.target.closest(".csel-btn");
			if (btn) {
				event.preventDefault();
				toggleSelect(btn);
				return;
			}

			const option = event.target.closest(".csel-opt");
			if (option) {
				const container = option.closest(".csel");
				const button = container?.querySelector(".csel-btn");
				const optionsContainer = option.closest(".csel-opts");
				const hiddenInput =
					container?.querySelector('input[type="hidden"]') ||
					container?.nextElementSibling;

				optionsContainer?.querySelectorAll(".csel-opt").forEach((opt) => {
					opt.classList.remove("active");
				});
				option.classList.add("active");

				if (hiddenInput && hiddenInput.tagName === "INPUT") {
					const oldVal = hiddenInput.value;
					hiddenInput.value = option.dataset.value;
					if (oldVal !== option.dataset.value) {
						hiddenInput.dispatchEvent(new Event("change", { bubbles: true }));
					}
				}
				if (button) button.textContent = option.textContent;
				closeAll();
				return;
			}

			if (!event.target.closest(".csel")) {
				closeAll();
			}
		});

		document.addEventListener("keydown", (event) => {
			if (event.key === "Escape") {
				closeAll();
			}
		});
	}
}
