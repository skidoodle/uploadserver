import { $$, getInvoker } from "./dom.js";

/**
 * Controller for context menu popup components (.ctx).
 */
export class ContextMenu {
	static init() {
		const closeAllMenus = () => {
			$$(".ctx.open").forEach((ctx) => {
				ctx.classList.remove("open");
			});
		};

		const toggleMenu = (trigger) => {
			const container = trigger?.closest(".ctx");
			if (!container) return;

			const isOpen = container.classList.contains("open");
			closeAllMenus();
			if (!isOpen) {
				container.classList.add("open");
				const menu = container.querySelector(".ctx-menu");
				if (menu) {
					const rect = trigger.getBoundingClientRect();
					menu.style.position = "fixed";
					const menuHeight = menu.offsetHeight || 180;
					let top = rect.bottom + 4;
					if (top + menuHeight > window.innerHeight - 10) {
						top = rect.top - 4 - menuHeight;
					}
					menu.style.top = `${top}px`;
					const rightDist = window.innerWidth - rect.right;
					menu.style.right = `${Math.max(10, rightDist)}px`;
					menu.style.left = "auto";
					menu.style.zIndex = "9999";
				}
			}
		};

		document.addEventListener("command", (e) => {
			if (e.command === "--toggle-menu") {
				toggleMenu(getInvoker(e));
			}
		});

		document.addEventListener("click", (e) => {
			const trigger = e.target.closest(".ctx-trigger");
			if (trigger) {
				e.preventDefault();
				toggleMenu(trigger);
				return;
			}

			const item = e.target.closest(".ctx-item");
			if (item) {
				closeAllMenus();
				return;
			}
			if (!e.target.closest(".ctx")) {
				closeAllMenus();
			}
		});

		window.addEventListener("scroll", closeAllMenus, true);
		window.addEventListener("resize", closeAllMenus);
		document.addEventListener("keydown", (e) => {
			if (e.key === "Escape") closeAllMenus();
		});
	}
}
