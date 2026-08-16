import { DialogController } from "../../shared/js/dialog.js";
import { $, getInvoker, preventAutofocus } from "../../shared/js/dom.js";
import { FontViewer } from "./font-viewer.js";
import { GalleryManager } from "./gallery.js";
import { CustomMediaPlayer } from "./player.js";
import { TextViewer } from "./text-viewer.js";

/**
 * Controller for the lightbox modal (#imgModal) and viewer carousel.
 */
export class MediaModal {
	static #COMMAND_HANDLERS = {
		"show-modal": (modal, e) => {
			const invoker = getInvoker(e);
			if (invoker?.dataset?.previewUrl) {
				const idx = modal.gallery.findIndexByUrl(invoker.dataset.previewUrl);
				if (idx !== -1) modal.prepareModal(idx);
			}
		},
		"--prev": (modal) => modal.navigate("prev"),
		"--next": (modal) => modal.navigate("next"),
		close: (modal) => modal.closeModal(),
	};

	static #KEY_ACTIONS = {
		" ": (modal, isVideo, isAudio) => {
			if (isVideo) modal.videoPlayer?.togglePlay(true);
			else if (isAudio) modal.audioPlayer?.togglePlay();
		},
		k: (modal, isVideo, isAudio) => {
			if (isVideo) modal.videoPlayer?.togglePlay(true);
			else if (isAudio) modal.audioPlayer?.togglePlay();
		},
		K: (modal, isVideo, isAudio) => {
			if (isVideo) modal.videoPlayer?.togglePlay(true);
			else if (isAudio) modal.audioPlayer?.togglePlay();
		},
		m: (modal, isVideo, isAudio) => {
			if (isVideo) modal.videoPlayer?.toggleMute();
			else if (isAudio) modal.audioPlayer?.toggleMute();
		},
		M: (modal, isVideo, isAudio) => {
			if (isVideo) modal.videoPlayer?.toggleMute();
			else if (isAudio) modal.audioPlayer?.toggleMute();
		},
		j: (modal, isVideo, isAudio) => {
			if (isVideo) modal.videoPlayer?.seekDelta(-10);
			else if (isAudio) modal.audioPlayer?.seekDelta(-10);
		},
		J: (modal, isVideo, isAudio) => {
			if (isVideo) modal.videoPlayer?.seekDelta(-10);
			else if (isAudio) modal.audioPlayer?.seekDelta(-10);
		},
		l: (modal, isVideo, isAudio) => {
			if (isVideo) modal.videoPlayer?.seekDelta(10);
			else if (isAudio) modal.audioPlayer?.seekDelta(10);
		},
		L: (modal, isVideo, isAudio) => {
			if (isVideo) modal.videoPlayer?.seekDelta(10);
			else if (isAudio) modal.audioPlayer?.seekDelta(10);
		},
		f: (modal, isVideo) => {
			if (isVideo) modal.videoPlayer?.toggleFullscreen();
		},
		F: (modal, isVideo) => {
			if (isVideo) modal.videoPlayer?.toggleFullscreen();
		},
		p: (modal, isVideo) => {
			if (isVideo) modal.videoPlayer?.togglePip();
		},
		P: (modal, isVideo) => {
			if (isVideo) modal.videoPlayer?.togglePip();
		},
		ArrowLeft: (modal) => modal.navigate("prev"),
		a: (modal) => modal.navigate("prev"),
		A: (modal) => modal.navigate("prev"),
		ArrowRight: (modal) => modal.navigate("next"),
		d: (modal) => modal.navigate("next"),
		D: (modal) => modal.navigate("next"),
		Escape: (modal) => modal.closeModal(),
	};

	constructor() {
		this.modal = $("#imgModal");
		this.modalImg = $("#modalImg");
		this.modalVideoBox = $("#modalVideoBox");
		this.modalVideo = $("#modalVideo");
		this.modalAudioBox = $("#modalAudioBox");
		this.modalAudio = $("#modalAudio");
		this.modalAudioName = $("#modalAudioName");
		this.audioDisc = $("#audioDisc");
		this.modalTextBox = $("#modalTextBox");
		this.textTypeBadge = $("#textTypeBadge");
		this.textStats = $("#textStats");
		this.textWrapBtn = $("#textWrapBtn");
		this.textCopyBtn = $("#textCopyBtn");
		this.textLoading = $("#textLoading");
		this.textError = $("#textError");
		this.textErrorMsg = $("#textErrorMsg");
		this.textScrollArea = $("#textScrollArea");
		this.textLineNumbers = $("#textLineNumbers");
		this.modalTextPre = $("#modalTextPre");
		this.modalTextCode = $("#modalTextCode");
		this.modalPdf = $("#modalPdf");
		this.modalFontBox = $("#modalFontBox");
		this.fontFamilyName = $("#fontFamilyName");
		this.fontFormatBadge = $("#fontFormatBadge");
		this.modalTitle = $("#modalTitle");
		this.modalDownload = $("#modalDownload");
		this.modalPrev = $("#modalPrev");
		this.modalNext = $("#modalNext");
		this.modalDeleteFilename = $("#modalDeleteFilename");
		this.modalClose = $("#modalClose");
		this.fileList = $("#fileList");

		if (!this.fileList || !this.modal) return;

		this.gallery = new GalleryManager(this.fileList);
		this.currentIndex = -1;

		this.videoPlayer = new CustomMediaPlayer({
			mediaEl: this.modalVideo,
			containerEl: this.modalVideoBox,
			playBtn: $("#videoPlayBtn"),
			backBtn: $("#videoBackBtn"),
			fwdBtn: $("#videoFwdBtn"),
			timeline: $("#videoTimeline"),
			bufferedEl: $("#videoBuffered"),
			progressEl: $("#videoProgress"),
			thumbEl: $("#videoThumb"),
			curTimeEl: $("#videoCurTime"),
			durTimeEl: $("#videoDurTime"),
			volSlider: $("#videoVolSlider"),
			muteBtn: $("#videoMuteBtn"),
			rateBtn: $("#videoRateBtn"),
			loopBtn: $("#videoLoopBtn"),
			pipBtn: $("#videoPipBtn"),
			fullscreenBtn: $("#videoFullscreenBtn"),
			centerFlashEl: $("#videoCenterFlash"),
		});

		this.audioPlayer = new CustomMediaPlayer({
			mediaEl: this.modalAudio,
			containerEl: this.modalAudioBox,
			playBtn: $("#audioPlayBtn"),
			backBtn: $("#audioBackBtn"),
			fwdBtn: $("#audioFwdBtn"),
			timeline: $("#audioTimeline"),
			bufferedEl: $("#audioBuffered"),
			progressEl: $("#audioProgress"),
			thumbEl: $("#audioThumb"),
			curTimeEl: $("#audioCurTime"),
			durTimeEl: $("#audioDurTime"),
			volSlider: $("#audioVolSlider"),
			muteBtn: $("#audioMuteBtn"),
			rateBtn: $("#audioRateBtn"),
			loopBtn: $("#audioLoopBtn"),
			discEl: this.audioDisc,
		});

		this.textViewer = new TextViewer({
			modalTextBox: this.modalTextBox,
			textTypeBadge: this.textTypeBadge,
			textStats: this.textStats,
			textWrapBtn: this.textWrapBtn,
			textCopyBtn: this.textCopyBtn,
			textLoading: this.textLoading,
			textError: this.textError,
			textErrorMsg: this.textErrorMsg,
			textScrollArea: this.textScrollArea,
			textLineNumbers: this.textLineNumbers,
			modalTextPre: this.modalTextPre,
			modalTextCode: this.modalTextCode,
		});

		this.fontViewer = new FontViewer({
			modalFontBox: this.modalFontBox,
			fontFamilyName: this.fontFamilyName,
			fontFormatBadge: this.fontFormatBadge,
		});

		this.#initEvents();
	}

	prepareModal(index) {
		if (index < 0 || index >= this.gallery.length) return;
		this.currentIndex = index;
		this.updateModalContent();
		DialogController.updateScrollLock();
		preventAutofocus(this.modal);
		this.gallery.maybePrefetch(this.currentIndex);
	}

	openModal(index) {
		this.prepareModal(index);
		if (!this.modal.open) {
			try {
				this.modal.showModal();
			} catch (_) {}
		}
		DialogController.updateScrollLock();
		preventAutofocus(this.modal);
	}

	stopPlayback() {
		this.textViewer.stop();
		this.fontViewer.stop();
		this.videoPlayer?.reset();
		this.audioPlayer?.reset();

		if (this.modalVideoBox) this.modalVideoBox.classList.add("hidden");
		if (this.modalAudioBox) this.modalAudioBox.classList.add("hidden");
		if (this.modalImg) {
			this.modalImg.src = "";
			this.modalImg.classList.add("hidden");
		}
		if (this.modalPdf) {
			this.modalPdf.src = "about:blank";
			this.modalPdf.classList.add("hidden");
		}
	}

	closeModal() {
		this.stopPlayback();
		if (this.modal?.open) this.modal.close();
		DialogController.updateScrollLock();
	}

	updateModalContent() {
		if (this.gallery.length === 0) return;
		if (this.currentIndex < 0) this.currentIndex = 0;
		if (this.currentIndex >= this.gallery.length)
			this.currentIndex = this.gallery.length - 1;

		const item = this.gallery.getItem(this.currentIndex);
		if (!item) return;

		this.stopPlayback();

		if (this.modalTitle)
			this.modalTitle.textContent = item.rawName || item.name;
		if (this.modalDownload) this.modalDownload.href = item.url;
		if (this.modalDeleteFilename) {
			this.modalDeleteFilename.value = item.rawName || item.name;
		}

		if (this.modalPrev) {
			this.modalPrev.disabled = !this.gallery.canGoPrev(this.currentIndex);
		}
		if (this.modalNext) {
			this.modalNext.disabled = !this.gallery.canGoNext(this.currentIndex);
		}

		const ext = (
			(item.rawName || item.name).split(".").pop() || ""
		).toLowerCase();

		switch (item.type) {
			case "video":
				if (this.modalVideoBox && this.modalVideo) {
					this.modalVideo.src = item.url;
					this.modalVideoBox.classList.remove("hidden");
					this.modalVideo.play().catch(() => {});
				}
				break;

			case "audio":
				if (this.modalAudioBox && this.modalAudio) {
					this.modalAudio.src = item.url;
					if (this.modalAudioName)
						this.modalAudioName.textContent = item.rawName || item.name;
					this.modalAudioBox.classList.remove("hidden");
					this.modalAudio.play().catch(() => {});
				}
				break;

			case "pdf":
				if (this.modalPdf) {
					this.modalPdf.src = item.url;
					this.modalPdf.classList.remove("hidden");
				}
				break;

			case "font":
				this.fontViewer.load(item, ext);
				break;

			case "text":
				this.textViewer.load(item, ext);
				break;
			default:
				if (this.modalImg) {
					this.modalImg.src = item.url;
					this.modalImg.classList.remove("hidden");
				}
				break;
		}
	}

	navigate(direction) {
		if (direction === "next") {
			if (this.currentIndex < this.gallery.length - 1) {
				this.currentIndex++;
				this.updateModalContent();
				this.gallery.maybePrefetch(this.currentIndex);
			} else if (this.gallery.highestLoadedPage < this.gallery.totalPages) {
				if (this.modalNext) this.modalNext.disabled = true;
				this.gallery.fetchPage(
					this.gallery.highestLoadedPage + 1,
					"next",
					(delta) => {
						this.currentIndex += delta;
						this.updateModalContent();
					},
				);
			}
		} else if (direction === "prev") {
			if (this.currentIndex > 0) {
				this.currentIndex--;
				this.updateModalContent();
				this.gallery.maybePrefetch(this.currentIndex);
			} else if (this.gallery.lowestLoadedPage > 1) {
				if (this.modalPrev) this.modalPrev.disabled = true;
				this.gallery.fetchPage(
					this.gallery.lowestLoadedPage - 1,
					"prev",
					(delta) => {
						this.currentIndex = Math.max(0, this.currentIndex + delta);
						this.updateModalContent();
					},
				);
			}
		}
	}

	#initEvents() {
		document.addEventListener("click", (e) => {
			const preview = e.target.closest('.file-preview[data-is-media="true"]');
			if (preview && !preview.hasAttribute("commandfor")) {
				const url = preview.dataset.previewUrl;
				const idx = this.gallery.findIndexByUrl(url);
				if (idx !== -1) {
					this.openModal(idx);
				}
			}
		});

		this.modalClose?.addEventListener("click", () => this.closeModal());

		this.modal.addEventListener("command", (e) => {
			const handler = MediaModal.#COMMAND_HANDLERS[e.command];
			if (handler) {
				handler(this, e);
			}
		});

		this.modal.addEventListener("click", (e) => {
			if (e.target === this.modal) this.closeModal();
		});
		this.modal.addEventListener("cancel", () => {
			this.closeModal();
		});
		this.modal.addEventListener("close", () => {
			this.stopPlayback();
			DialogController.updateScrollLock();
		});

		this.#initKeyboardNav();
		this.#initTouchSwipe();
	}

	#initKeyboardNav() {
		document.addEventListener("keydown", (e) => {
			if (!this.modal?.open) return;

			if (
				e.target.isContentEditable ||
				["INPUT", "TEXTAREA", "SELECT"].includes(e.target.tagName)
			) {
				if (e.key === "Escape") this.closeModal();
				return;
			}

			const handler = MediaModal.#KEY_ACTIONS[e.key];
			if (handler) {
				e.preventDefault();
				const activeItem = this.gallery.getItem(this.currentIndex);
				const isVideo = activeItem?.type === "video";
				const isAudio = activeItem?.type === "audio";
				handler(this, isVideo, isAudio);
			}
		});
	}

	#initTouchSwipe() {
		let touchStartX = 0;
		let touchStartY = 0;

		this.modal.addEventListener(
			"touchstart",
			(e) => {
				if (e.touches.length === 1) {
					touchStartX = e.touches[0].clientX;
					touchStartY = e.touches[0].clientY;
				}
			},
			{ passive: true },
		);

		this.modal.addEventListener(
			"touchend",
			(e) => {
				if (e.changedTouches.length === 1) {
					const deltaX = e.changedTouches[0].clientX - touchStartX;
					const deltaY = e.changedTouches[0].clientY - touchStartY;
					if (Math.abs(deltaX) > 40 && Math.abs(deltaY) < 60) {
						if (deltaX < 0) {
							this.navigate("next");
						} else {
							this.navigate("prev");
						}
					}
				}
			},
			{ passive: true },
		);
	}
}
