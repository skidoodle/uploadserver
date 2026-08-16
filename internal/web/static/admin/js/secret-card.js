import { $, getInvoker } from "../../shared/js/dom.js";

/**
 * Controller for the one-time generated secret key card (#secretCard).
 */
export class SecretCard {
	static #COMMAND_HANDLERS = {
		"--dismiss": (actions) => actions.doDismiss(),
		"--toggle-blur": (actions) => actions.doToggleBlur(),
		"--copy": (actions) => actions.doCopy(),
		"--download-sxcu": (actions, invoker) => actions.doDownloadSxcu(invoker),
	};

	static init() {
		const card = $("#secretCard");
		if (!card) return;

		const secretValue = $("#sv", card);
		const revealButton = $("#reveal", card);
		const copyButton = $("#cp", card);
		const dismissButton = card.querySelector(".secret-close");
		const downloadButton = $("#dl-sxcu", card);

		const actions = {
			doDismiss: () => card.remove(),
			doToggleBlur: () => {
				const isBlurred = secretValue?.classList.toggle("blurred");
				if (revealButton)
					revealButton.textContent = isBlurred ? "Show" : "Hide";
			},
			doCopy: () => {
				if (!secretValue) return;
				navigator.clipboard.writeText(secretValue.textContent).then(() => {
					if (copyButton) copyButton.textContent = "Copied";
					setTimeout(() => {
						if (copyButton) copyButton.textContent = "Copy";
					}, 1500);
				});
			},
			doDownloadSxcu: (invoker) => {
				const tokenId =
					invoker?.dataset?.tokenId || downloadButton?.dataset?.tokenId || "";
				const secret = secretValue?.textContent || "";
				const requestUrl = `${window.location.origin}/`;
				const sxcu = {
					Version: "17.0.0",
					Name: "uploadserver",
					DestinationType: "ImageUploader, TextUploader, FileUploader",
					RequestMethod: "POST",
					RequestURL: requestUrl,
					Headers: {
						Authorization: `Bearer ${secret}`,
					},
					Body: "MultipartFormData",
					FileFormName: "file",
					URL: "{response}",
					ErrorMessage: "{response}",
				};
				const blob = new Blob([JSON.stringify(sxcu, null, 2)], {
					type: "application/json",
				});
				const url = URL.createObjectURL(blob);
				const a = document.createElement("a");
				a.href = url;
				a.download = `${tokenId}.sxcu`;
				document.body.appendChild(a);
				a.click();
				a.remove();
				URL.revokeObjectURL(url);
			},
		};

		dismissButton?.addEventListener("click", actions.doDismiss);
		revealButton?.addEventListener("click", actions.doToggleBlur);
		copyButton?.addEventListener("click", actions.doCopy);
		downloadButton?.addEventListener("click", (e) =>
			actions.doDownloadSxcu(e.currentTarget),
		);

		card.addEventListener("command", (e) => {
			const handler = SecretCard.#COMMAND_HANDLERS[e.command];
			if (handler) {
				handler(actions, getInvoker(e));
			}
		});

		secretValue?.addEventListener("click", () => {
			if (secretValue.classList.contains("blurred")) {
				secretValue.classList.remove("blurred");
				if (revealButton) revealButton.textContent = "Hide";
			}
		});
	}
}
