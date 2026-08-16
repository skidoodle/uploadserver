/**
 * Validator helper for quota inputs (size and counts) and labels.
 */
export class QuotaValidator {
	static LABEL_PATTERN = /^[a-zA-Z0-9]([a-zA-Z0-9_-]{0,7}[a-zA-Z0-9])?$/;
	static SIZE_PATTERN = /^\d+(\.\d+)?\s*(b|kb|kib|mb|mib|gb|gib|tb|tib)?$/i;
	static CLEAR_WORD = /^(0|off|none|unlimited)$/i;

	static setupInPlaceLabelValidation(input, errorEl, form) {
		if (!input || !errorEl || !form) return () => true;

		const validate = () => {
			const val = input.value;
			if (val === "") {
				errorEl.hidden = true;
				input.classList.remove("invalid");
				return true;
			}
			if (!QuotaValidator.LABEL_PATTERN.test(val)) {
				errorEl.textContent =
					input.title ||
					"1-9 characters. Must start and end with a letter or number.";
				errorEl.hidden = false;
				input.classList.add("invalid");
				return false;
			}
			errorEl.hidden = true;
			input.classList.remove("invalid");
			return true;
		};

		input.addEventListener("input", validate);
		form.addEventListener("submit", (e) => {
			if (!validate()) {
				e.preventDefault();
			}
		});

		return validate;
	}

	static initQuotaForm(form, errorEl) {
		if (!form) return;

		form.querySelectorAll("input[data-kind]").forEach((input) => {
			input.addEventListener("input", () =>
				QuotaValidator.validateQuotaForm(form, errorEl),
			);
		});

		form.addEventListener("submit", (event) => {
			if (!QuotaValidator.validateQuotaForm(form, errorEl)) {
				event.preventDefault();
			}
		});
	}

	static validateQuotaForm(form, errorEl) {
		let firstError = null;
		form.querySelectorAll("input[data-kind]").forEach((input) => {
			if (input.disabled) {
				input.classList.remove("invalid");
				return;
			}
			const message = QuotaValidator.quotaFieldError(input);
			if (message) {
				input.classList.add("invalid");
				if (!firstError) firstError = message;
			} else {
				input.classList.remove("invalid");
			}
		});
		if (errorEl) {
			errorEl.textContent = firstError ?? "";
		}
		return !firstError;
	}

	static quotaFieldError(input) {
		const value = input.value.trim();
		if (value === "" || QuotaValidator.CLEAR_WORD.test(value)) {
			return null;
		}
		if (input.dataset.kind === "size") {
			return QuotaValidator.SIZE_PATTERN.test(value)
				? null
				: "Enter a size like 500MB or 5GB, or 0 for unlimited.";
		}
		return /^\d+$/.test(value.replace(/,/g, ""))
			? null
			: "Enter a whole number of uploads, or 0 for unlimited.";
	}

	static resetQuotaForm(form, errorEl) {
		form.querySelectorAll("input.invalid").forEach((input) => {
			input.classList.remove("invalid");
		});
		if (errorEl) {
			errorEl.textContent = "";
		}
	}
}
