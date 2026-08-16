/**
 * Manager for uploaded media discovery, indexing, and multi-page pre-fetching.
 */
export class GalleryManager {
	constructor(fileList) {
		this.fileList = fileList;
		this.initialPage = parseInt(fileList?.dataset?.page || "1", 10);
		this.totalPages = parseInt(fileList?.dataset?.totalPages || "1", 10);
		this.tokenId = fileList?.dataset?.tokenId || "";
		this.query = fileList?.dataset?.query || "";

		this.mediaItems = this.extractMediaFromDocument(document, this.initialPage);
		this.loadedPages = new Set([this.initialPage]);
		this.lowestLoadedPage = this.initialPage;
		this.highestLoadedPage = this.initialPage;
		this.isFetching = false;
	}

	extractMediaFromDocument(doc, pageNum) {
		const previews = doc.querySelectorAll(
			'.file-preview[data-is-media="true"]',
		);
		const items = [];
		previews.forEach((p) => {
			items.push({
				url: p.dataset.previewUrl,
				name: p.dataset.fileName,
				rawName: p.dataset.rawName || p.dataset.fileName,
				type: p.dataset.mediaType || "image",
				page: pageNum,
			});
		});
		return items;
	}

	findIndexByUrl(url) {
		return this.mediaItems.findIndex((item) => item.url === url);
	}

	getItem(index) {
		return this.mediaItems[index] || null;
	}

	get length() {
		return this.mediaItems.length;
	}

	canGoPrev(currentIndex) {
		return currentIndex > 0 || this.lowestLoadedPage > 1;
	}

	canGoNext(currentIndex) {
		return (
			currentIndex < this.mediaItems.length - 1 ||
			this.highestLoadedPage < this.totalPages
		);
	}

	maybePrefetch(currentIndex) {
		if (this.isFetching) return;
		if (
			currentIndex >= this.mediaItems.length - 3 &&
			this.highestLoadedPage < this.totalPages &&
			!this.loadedPages.has(this.highestLoadedPage + 1)
		) {
			this.fetchPage(this.highestLoadedPage + 1, null);
		}
		if (
			currentIndex <= 2 &&
			this.lowestLoadedPage > 1 &&
			!this.loadedPages.has(this.lowestLoadedPage - 1)
		) {
			this.fetchPage(this.lowestLoadedPage - 1, null);
		}
	}

	async fetchPage(pageNum, navDirection, onComplete) {
		if (this.loadedPages.has(pageNum) || this.isFetching) return;
		this.isFetching = true;

		try {
			const url = `/_/uploads/${encodeURIComponent(this.tokenId)}?page=${pageNum}&q=${encodeURIComponent(this.query)}`;
			const resp = await fetch(url);
			if (!resp.ok) throw new Error("Page fetch failed");
			const html = await resp.text();
			const doc = new DOMParser().parseFromString(html, "text/html");
			const newItems = this.extractMediaFromDocument(doc, pageNum);

			this.loadedPages.add(pageNum);
			let indexDelta = 0;

			if (pageNum > this.highestLoadedPage) {
				this.mediaItems = this.mediaItems.concat(newItems);
				this.highestLoadedPage = pageNum;
				if (navDirection === "next" && newItems.length > 0) {
					indexDelta = 1;
				}
			} else if (pageNum < this.lowestLoadedPage) {
				this.mediaItems = newItems.concat(this.mediaItems);
				this.lowestLoadedPage = pageNum;
				indexDelta = newItems.length;
				if (navDirection === "prev" && newItems.length > 0) {
					indexDelta = newItems.length - 1;
				}
			}

			if (onComplete) onComplete(indexDelta);
		} catch (err) {
			console.error("Failed to load page in background:", err);
			if (onComplete) onComplete(0);
		} finally {
			this.isFetching = false;
		}
	}
}
