/**
 * Controller for font specimen previews in the media lightbox.
 */
export class FontViewer {
	constructor(opts) {
		const { modalFontBox, fontFamilyName, fontFormatBadge } = opts;
		this.modalFontBox = modalFontBox;
		this.fontFamilyName = fontFamilyName;
		this.fontFormatBadge = fontFormatBadge;
	}

	load(item, ext) {
		if (!this.modalFontBox) return;

		this.modalFontBox.classList.remove("hidden");
		if (this.fontFamilyName) this.fontFamilyName.textContent = item.name;
		if (this.fontFormatBadge)
			this.fontFormatBadge.textContent = (ext || "FONT").toUpperCase();

		const fontId = `font-preview-${Math.random().toString(36).substring(2, 9)}`;
		const fontFace = new FontFace(fontId, `url("${item.url}")`);
		fontFace
			.load()
			.then((loadedFace) => {
				document.fonts.add(loadedFace);
				const scrollArea = this.modalFontBox.querySelector(
					".font-sample-scroll",
				);
				if (scrollArea)
					scrollArea.style.fontFamily = `"${fontId}", system-ui, sans-serif`;
			})
			.catch((err) => {
				console.error("Failed to load font specimen:", err);
			});
	}

	stop() {
		if (this.modalFontBox) {
			this.modalFontBox.classList.add("hidden");
		}
	}
}
