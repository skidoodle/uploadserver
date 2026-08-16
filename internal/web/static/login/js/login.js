const $ = (selector, context = document) => context?.querySelector(selector);

export class LoginForm {
	constructor() {
		const token = $('input[name="token"]');
		const form = token?.form;
		if (!token || !form) return;

		form.addEventListener("submit", () => {
			token.value = token.value.trim();
		});
	}
}

document.addEventListener("DOMContentLoaded", () => new LoginForm());
