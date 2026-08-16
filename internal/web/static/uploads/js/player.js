import { formatTime } from "../../shared/js/utils.js";

/**
 * Custom media player controller for HTML5 audio and video playback.
 */
export class CustomMediaPlayer {
	constructor(opts) {
		this.opts = opts;
		const {
			mediaEl,
			containerEl,
			playBtn,
			backBtn,
			fwdBtn,
			timeline,
			bufferedEl,
			progressEl,
			thumbEl,
			curTimeEl,
			durTimeEl,
			volSlider,
			muteBtn,
			rateBtn,
			loopBtn,
			pipBtn,
			fullscreenBtn,
			centerFlashEl,
			discEl,
		} = opts;

		if (!mediaEl) return;

		this.mediaEl = mediaEl;
		this.containerEl = containerEl;
		this.playBtn = playBtn;
		this.backBtn = backBtn;
		this.fwdBtn = fwdBtn;
		this.timeline = timeline;
		this.bufferedEl = bufferedEl;
		this.progressEl = progressEl;
		this.thumbEl = thumbEl;
		this.curTimeEl = curTimeEl;
		this.durTimeEl = durTimeEl;
		this.volSlider = volSlider;
		this.muteBtn = muteBtn;
		this.rateBtn = rateBtn;
		this.loopBtn = loopBtn;
		this.pipBtn = pipBtn;
		this.fullscreenBtn = fullscreenBtn;
		this.centerFlashEl = centerFlashEl;
		this.discEl = discEl;

		this.isScrubbing = false;
		this.idleTimer = null;
		this.rates = [1, 1.25, 1.5, 2, 0.5];
		this.rateIdx = 0;

		const savedVol = parseFloat(
			localStorage.getItem("uploadserver_media_vol") ?? "1",
		);
		const savedMuted =
			localStorage.getItem("uploadserver_media_muted") === "true";
		this.mediaEl.volume = Number.isNaN(savedVol)
			? 1
			: Math.max(0, Math.min(1, savedVol));
		this.mediaEl.muted = savedMuted;
		if (this.volSlider)
			this.volSlider.value = this.mediaEl.muted ? 0 : this.mediaEl.volume;
		this.updateMuteIcon();

		this.#initEvents();
	}

	updateMuteIcon() {
		const isMuted = this.mediaEl.muted || this.mediaEl.volume === 0;
		this.muteBtn
			?.querySelector(".vol-high-icon")
			?.classList.toggle("hidden", isMuted);
		this.muteBtn
			?.querySelector(".vol-mute-icon")
			?.classList.toggle("hidden", !isMuted);
		if (this.volSlider) {
			this.volSlider.value = isMuted ? 0 : this.mediaEl.volume;
			const pct = isMuted ? 0 : Math.round(this.mediaEl.volume * 100);
			this.volSlider.style.setProperty("--vol-percent", `${pct}%`);
		}
	}

	updatePlayBtn() {
		const isPaused = this.mediaEl.paused || this.mediaEl.ended;
		this.playBtn
			?.querySelector(".play-icon")
			?.classList.toggle("hidden", !isPaused);
		this.playBtn
			?.querySelector(".pause-icon")
			?.classList.toggle("hidden", isPaused);
		if (this.discEl) {
			this.discEl.classList.toggle("playing", !isPaused);
		}
	}

	flashCenter(isPlay) {
		if (!this.centerFlashEl) return;
		const playIcon = this.centerFlashEl.querySelector(".flash-play");
		const pauseIcon = this.centerFlashEl.querySelector(".flash-pause");
		if (playIcon) playIcon.classList.toggle("hidden", !isPlay);
		if (pauseIcon) pauseIcon.classList.toggle("hidden", isPlay);
		this.centerFlashEl.classList.add("flash");
		setTimeout(() => this.centerFlashEl.classList.remove("flash"), 300);
	}

	togglePlay(showFlash = false) {
		if (this.mediaEl.paused || this.mediaEl.ended) {
			this.mediaEl.play().catch(() => {});
			if (showFlash) this.flashCenter(true);
		} else {
			this.mediaEl.pause();
			if (showFlash) this.flashCenter(false);
		}
	}

	updateProgress() {
		if (this.isScrubbing) return;
		const cur = this.mediaEl.currentTime || 0;
		const dur = this.mediaEl.duration || 0;
		const pct = dur > 0 ? (cur / dur) * 100 : 0;
		if (this.progressEl) this.progressEl.style.width = `${pct}%`;
		if (this.thumbEl) this.thumbEl.style.left = `${pct}%`;
		if (this.curTimeEl) this.curTimeEl.textContent = formatTime(cur);
		if (this.durTimeEl && dur > 0) this.durTimeEl.textContent = formatTime(dur);
	}

	updateBuffered() {
		if (!this.bufferedEl || !this.mediaEl.duration) return;
		if (this.mediaEl.buffered.length > 0) {
			const end = this.mediaEl.buffered.end(this.mediaEl.buffered.length - 1);
			const pct = (end / this.mediaEl.duration) * 100;
			this.bufferedEl.style.width = `${Math.min(100, pct)}%`;
		}
	}

	seekTo(pct) {
		if (!this.mediaEl.duration) return;
		const clamped = Math.max(0, Math.min(1, pct));
		this.mediaEl.currentTime = clamped * this.mediaEl.duration;
		if (this.progressEl) this.progressEl.style.width = `${clamped * 100}%`;
		if (this.thumbEl) this.thumbEl.style.left = `${clamped * 100}%`;
		if (this.curTimeEl)
			this.curTimeEl.textContent = formatTime(this.mediaEl.currentTime);
	}

	seekDelta(sec) {
		if (this.mediaEl.duration) {
			this.mediaEl.currentTime = Math.max(
				0,
				Math.min(this.mediaEl.duration, this.mediaEl.currentTime + sec),
			);
		}
	}

	toggleMute() {
		this.mediaEl.muted = !this.mediaEl.muted;
		localStorage.setItem(
			"uploadserver_media_muted",
			this.mediaEl.muted.toString(),
		);
		this.updateMuteIcon();
	}

	#handleScrub(e) {
		if (!this.timeline) return;
		const rect = this.timeline.getBoundingClientRect();
		const clientX = e.clientX ?? (e.touches?.[0] ? e.touches[0].clientX : 0);
		const pct = (clientX - rect.left) / rect.width;
		this.seekTo(pct);
	}

	isFullscreenActive() {
		return !!(
			document.fullscreenElement ||
			document.webkitFullscreenElement ||
			document.mozFullScreenElement ||
			document.msFullscreenElement ||
			this.mediaEl?.webkitDisplayingFullscreen
		);
	}

	enterVideoFullscreenFallback() {
		if (
			this.mediaEl &&
			typeof this.mediaEl.webkitEnterFullscreen === "function"
		) {
			this.mediaEl.webkitEnterFullscreen();
		} else if (
			this.mediaEl &&
			typeof this.mediaEl.requestFullscreen === "function"
		) {
			this.mediaEl.requestFullscreen().catch(() => {});
		} else if (
			this.mediaEl &&
			typeof this.mediaEl.webkitRequestFullscreen === "function"
		) {
			this.mediaEl.webkitRequestFullscreen();
		}
	}

	toggleFullscreen() {
		if (this.isFullscreenActive()) {
			if (document.exitFullscreen) {
				document.exitFullscreen().catch(() => {});
			} else if (document.webkitExitFullscreen) {
				document.webkitExitFullscreen();
			} else if (document.mozCancelFullScreen) {
				document.mozCancelFullScreen();
			} else if (document.msExitFullscreen) {
				document.msExitFullscreen();
			} else if (
				this.mediaEl &&
				typeof this.mediaEl.webkitExitFullscreen === "function"
			) {
				this.mediaEl.webkitExitFullscreen();
			}
		} else {
			const req =
				this.containerEl?.requestFullscreen ||
				this.containerEl?.webkitRequestFullscreen ||
				this.containerEl?.mozRequestFullScreen ||
				this.containerEl?.msRequestFullscreen;
			if (req) {
				try {
					const res = req.call(this.containerEl);
					if (res && typeof res.catch === "function") {
						res.catch(() => this.enterVideoFullscreenFallback());
					}
				} catch (_) {
					this.enterVideoFullscreenFallback();
				}
			} else {
				this.enterVideoFullscreenFallback();
			}
		}
	}

	async togglePip() {
		try {
			if (document.pictureInPictureElement) {
				await document.exitPictureInPicture();
			} else if (this.mediaEl.requestPictureInPicture) {
				await this.mediaEl.requestPictureInPicture();
			}
		} catch (_) {}
	}

	reset() {
		this.mediaEl.pause();
		this.mediaEl.removeAttribute("src");
		this.mediaEl.load();
		if (this.progressEl) this.progressEl.style.width = "0%";
		if (this.bufferedEl) this.bufferedEl.style.width = "0%";
		if (this.thumbEl) this.thumbEl.style.left = "0%";
		if (this.curTimeEl) this.curTimeEl.textContent = "0:00";
		if (this.durTimeEl) this.durTimeEl.textContent = "0:00";
		if (this.rateBtn) this.rateBtn.textContent = "1x";
		this.rateIdx = 0;
		this.mediaEl.playbackRate = 1;
		this.mediaEl.loop = false;
		this.loopBtn?.classList.remove("active");
		this.updatePlayBtn();
	}

	#initEvents() {
		const handlePointerMove = (e) => {
			if (this.isScrubbing) this.#handleScrub(e);
		};

		const handlePointerUp = (e) => {
			if (this.isScrubbing) {
				this.#handleScrub(e);
				this.isScrubbing = false;
				this.timeline?.classList.remove("scrubbing");
				window.removeEventListener("pointermove", handlePointerMove);
				window.removeEventListener("pointerup", handlePointerUp);
			}
		};

		this.timeline?.addEventListener("pointerdown", (e) => {
			this.isScrubbing = true;
			this.timeline.classList.add("scrubbing");
			this.#handleScrub(e);
			window.addEventListener("pointermove", handlePointerMove);
			window.addEventListener("pointerup", handlePointerUp);
		});

		this.playBtn?.addEventListener("click", () => this.togglePlay());
		this.backBtn?.addEventListener("click", () => {
			this.mediaEl.currentTime = Math.max(0, this.mediaEl.currentTime - 10);
		});
		this.fwdBtn?.addEventListener("click", () => {
			this.mediaEl.currentTime = Math.min(
				this.mediaEl.duration || 0,
				this.mediaEl.currentTime + 10,
			);
		});

		this.volSlider?.addEventListener("input", (e) => {
			const val = parseFloat(e.target.value);
			this.mediaEl.volume = val;
			this.mediaEl.muted = val === 0;
			localStorage.setItem("uploadserver_media_vol", val.toString());
			localStorage.setItem(
				"uploadserver_media_muted",
				this.mediaEl.muted.toString(),
			);
			this.updateMuteIcon();
		});

		this.muteBtn?.addEventListener("click", () => {
			this.mediaEl.muted = !this.mediaEl.muted;
			if (!this.mediaEl.muted && this.mediaEl.volume === 0) {
				this.mediaEl.volume = 0.5;
			}
			localStorage.setItem(
				"uploadserver_media_muted",
				this.mediaEl.muted.toString(),
			);
			this.updateMuteIcon();
		});

		this.rateBtn?.addEventListener("click", () => {
			this.rateIdx = (this.rateIdx + 1) % this.rates.length;
			const r = this.rates[this.rateIdx];
			this.mediaEl.playbackRate = r;
			this.rateBtn.textContent = `${r}x`;
		});

		this.loopBtn?.addEventListener("click", () => {
			this.mediaEl.loop = !this.mediaEl.loop;
			this.loopBtn.classList.toggle("active", this.mediaEl.loop);
		});

		this.pipBtn?.addEventListener("click", () => this.togglePip());

		this.fullscreenBtn?.addEventListener("click", (e) => {
			e.stopPropagation();
			this.toggleFullscreen();
		});

		const updateFullscreenIcon = () => {
			const isFs = this.isFullscreenActive();
			this.fullscreenBtn
				?.querySelector(".fs-enter-icon")
				?.classList.toggle("hidden", isFs);
			this.fullscreenBtn
				?.querySelector(".fs-exit-icon")
				?.classList.toggle("hidden", !isFs);
		};

		document.addEventListener("fullscreenchange", updateFullscreenIcon);
		document.addEventListener("webkitfullscreenchange", updateFullscreenIcon);
		document.addEventListener("mozfullscreenchange", updateFullscreenIcon);
		document.addEventListener("MSFullscreenChange", updateFullscreenIcon);
		this.mediaEl.addEventListener(
			"webkitbeginfullscreen",
			updateFullscreenIcon,
		);
		this.mediaEl.addEventListener("webkitendfullscreen", updateFullscreenIcon);

		this.mediaEl.addEventListener("timeupdate", () => this.updateProgress());
		this.mediaEl.addEventListener("progress", () => this.updateBuffered());
		this.mediaEl.addEventListener("play", () => this.updatePlayBtn());
		this.mediaEl.addEventListener("pause", () => this.updatePlayBtn());
		this.mediaEl.addEventListener("ended", () => {
			this.updatePlayBtn();
			if (!this.mediaEl.loop) {
				if (this.progressEl) this.progressEl.style.width = "0%";
				if (this.thumbEl) this.thumbEl.style.left = "0%";
			}
		});
		this.mediaEl.addEventListener("loadedmetadata", () => {
			this.updateProgress();
			this.updateBuffered();
			this.updatePlayBtn();
			if (this.durTimeEl && this.mediaEl.duration) {
				this.durTimeEl.textContent = formatTime(this.mediaEl.duration);
			}
		});

		if (this.containerEl && this.mediaEl.tagName === "VIDEO") {
			this.mediaEl.addEventListener("click", (e) => {
				e.stopPropagation();
				this.togglePlay(true);
			});
			this.mediaEl.addEventListener("dblclick", (e) => {
				e.stopPropagation();
				this.toggleFullscreen();
			});

			const controls = this.containerEl.querySelector(".video-controls");
			const resetIdle = () => {
				if (!controls) return;
				controls.classList.remove("idle-hidden");
				clearTimeout(this.idleTimer);
				if (!this.mediaEl.paused && !this.mediaEl.ended) {
					this.idleTimer = setTimeout(() => {
						controls.classList.add("idle-hidden");
					}, 2500);
				}
			};
			this.containerEl.addEventListener("mousemove", resetIdle);
			this.containerEl.addEventListener("touchstart", resetIdle, {
				passive: true,
			});
			this.mediaEl.addEventListener("play", resetIdle);
			this.mediaEl.addEventListener("pause", () => {
				clearTimeout(this.idleTimer);
				controls?.classList.remove("idle-hidden");
			});
		}
	}
}
