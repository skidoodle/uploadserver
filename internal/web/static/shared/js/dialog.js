import { getInvoker, preventAutofocus } from "./dom.js";

/**
 * Controller for HTMLDialogElement modal management, scroll locking, and CommandEvent polyfill.
 */
export class DialogController {
	static #savedScrollY = 0;
	static #isScrollLocked = false;
	static #initialized = false;

	static init() {
		if (DialogController.#initialized) return;
		DialogController.#initialized = true;

		window._cmdInvoker = getInvoker;
		window._updateScrollLock =
			DialogController.updateScrollLock.bind(DialogController);

		DialogController.#initScrollLocking();
		DialogController.#initDialogPrototypes();
		DialogController.#initCommandPolyfill();
	}

	static onTouchMoveModal(e) {
		const scrollable = e.target.closest(
			".text-scroll-area, .font-sample-scroll",
		);
		if (!scrollable) {
			if (e.cancelable) e.preventDefault();
			return;
		}
		if (
			scrollable.scrollHeight <= scrollable.clientHeight &&
			scrollable.scrollWidth <= scrollable.clientWidth
		) {
			if (e.cancelable) e.preventDefault();
		}
	}

	static updateScrollLock() {
		const openDialogs = Array.from(document.querySelectorAll("dialog")).filter(
			(d) => d.open,
		);
		if (openDialogs.length > 0) {
			if (!DialogController.#isScrollLocked) {
				DialogController.#savedScrollY =
					window.scrollY ||
					window.pageYOffset ||
					document.documentElement.scrollTop ||
					0;
				document.body.style.position = "fixed";
				document.body.style.top = `-${DialogController.#savedScrollY}px`;
				document.body.style.left = "0";
				document.body.style.right = "0";
				document.body.style.width = "100%";
				document.body.style.overflow = "hidden";
				document.documentElement.style.overflow = "hidden";
				document.documentElement.classList.add("modal-open");
				document.body.classList.add("modal-open");
				document.addEventListener(
					"touchmove",
					DialogController.onTouchMoveModal,
					{ passive: false },
				);
				DialogController.#isScrollLocked = true;
			}
		} else {
			if (DialogController.#isScrollLocked) {
				const restoreY = DialogController.#savedScrollY;
				document.body.style.position = "";
				document.body.style.top = "";
				document.body.style.left = "";
				document.body.style.right = "";
				document.body.style.width = "";
				document.body.style.overflow = "";
				document.documentElement.style.overflow = "";
				document.documentElement.classList.remove("modal-open");
				document.body.classList.remove("modal-open");
				document.removeEventListener(
					"touchmove",
					DialogController.onTouchMoveModal,
					{ passive: false },
				);
				DialogController.#isScrollLocked = false;
				window.scrollTo(0, restoreY);
			}
		}
	}

	static #initScrollLocking() {
		try {
			const dialogObserver = new MutationObserver(() => {
				DialogController.updateScrollLock();
			});
			dialogObserver.observe(document.documentElement, {
				attributes: true,
				attributeFilter: ["open"],
				subtree: true,
			});
		} catch (_) {}

		document.addEventListener(
			"close",
			() => DialogController.updateScrollLock(),
			true,
		);
		document.addEventListener(
			"cancel",
			() => DialogController.updateScrollLock(),
			true,
		);
		document.addEventListener(
			"toggle",
			() => DialogController.updateScrollLock(),
			true,
		);

		document.addEventListener(
			"click",
			(e) => {
				const btn = e.target.closest(
					"button[commandfor], button[data-commandfor], [command]",
				);
				if (btn) {
					setTimeout(() => DialogController.updateScrollLock(), 0);
					setTimeout(() => DialogController.updateScrollLock(), 50);
				}
			},
			true,
		);

		document.addEventListener("command", (e) => {
			if (e.command === "show-modal") {
				const target = e.target;
				DialogController.updateScrollLock();
				preventAutofocus(target);
			} else if (e.command === "close") {
				setTimeout(() => DialogController.updateScrollLock(), 0);
			}
		});
	}

	static #initDialogPrototypes() {
		if (typeof HTMLDialogElement === "undefined") return;

		const origShowModal = HTMLDialogElement.prototype.showModal;
		const self = DialogController;
		HTMLDialogElement.prototype.showModal = function (...args) {
			origShowModal.apply(this, args);
			self.updateScrollLock();
			preventAutofocus(this);
		};

		const origClose = HTMLDialogElement.prototype.close;
		HTMLDialogElement.prototype.close = function (...args) {
			origClose.apply(this, args);
			self.updateScrollLock();
		};
	}

	static #initCommandPolyfill() {
		if (!("CommandEvent" in window)) {
			document.addEventListener("click", (e) => {
				const button = e.target.closest(
					"button[commandfor], button[data-commandfor]",
				);
				if (!button) return;
				const targetId =
					button.getAttribute("commandfor") || button.dataset.commandfor;
				const command =
					button.getAttribute("command") || button.dataset.command;
				if (!targetId || !command) return;

				const target = document.getElementById(targetId);
				if (!target) return;

				const event = new CustomEvent("command", {
					bubbles: true,
					cancelable: true,
				});
				event.command = command;
				event.source = button;
				event.invoker = button;
				const proceed = target.dispatchEvent(event);

				if (proceed) {
					if (
						command === "show-modal" &&
						typeof target.showModal === "function"
					) {
						target.showModal();
					} else if (
						command === "close" &&
						typeof target.close === "function"
					) {
						target.close();
					}
				}
			});
		}
	}
}
