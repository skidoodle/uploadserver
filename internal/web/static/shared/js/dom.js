/**
 * Lightweight DOM utility helpers.
 */

export const $ = (selector, context = document) =>
	context?.querySelector(selector);
export const $$ = (selector, context = document) =>
	Array.from(context?.querySelectorAll(selector) || []);

export function on(target, event, selectorOrHandler, handler) {
	if (typeof selectorOrHandler === "function") {
		target.addEventListener(event, selectorOrHandler);
		return () => target.removeEventListener(event, selectorOrHandler);
	}

	const delegatedHandler = (e) => {
		const match = e.target.closest(selectorOrHandler);
		if (match && target.contains(match)) {
			handler.call(match, e, match);
		}
	};

	target.addEventListener(event, delegatedHandler);
	return () => target.removeEventListener(event, delegatedHandler);
}

export function preventAutofocus(target) {
	setTimeout(() => {
		if (
			document.activeElement &&
			["INPUT", "TEXTAREA", "SELECT"].includes(document.activeElement.tagName)
		) {
			document.activeElement.blur();
		}
		if (target && typeof target.focus === "function") {
			try {
				target.focus({ preventScroll: true });
			} catch (_) {}
		}
	}, 0);
}

export function getInvoker(e) {
	return e.source || e.invoker;
}
