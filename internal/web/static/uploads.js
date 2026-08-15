(function () {
  const modal = document.getElementById('imgModal');
  const modalImg = document.getElementById('modalImg');
  const modalVideoBox = document.getElementById('modalVideoBox');
  const modalVideo = document.getElementById('modalVideo');
  const modalAudioBox = document.getElementById('modalAudioBox');
  const modalAudio = document.getElementById('modalAudio');
  const modalAudioName = document.getElementById('modalAudioName');
  const audioDisc = document.getElementById('audioDisc');
  const modalTextBox = document.getElementById('modalTextBox');
  const textTypeBadge = document.getElementById('textTypeBadge');
  const textStats = document.getElementById('textStats');
  const textWrapBtn = document.getElementById('textWrapBtn');
  const textCopyBtn = document.getElementById('textCopyBtn');
  const textLoading = document.getElementById('textLoading');
  const textError = document.getElementById('textError');
  const textErrorMsg = document.getElementById('textErrorMsg');
  const textScrollArea = document.getElementById('textScrollArea');
  const textLineNumbers = document.getElementById('textLineNumbers');
  const modalTextPre = document.getElementById('modalTextPre');
  const modalTextCode = document.getElementById('modalTextCode');
  const modalPdf = document.getElementById('modalPdf');
  const modalFontBox = document.getElementById('modalFontBox');
  const fontFamilyName = document.getElementById('fontFamilyName');
  const fontFormatBadge = document.getElementById('fontFormatBadge');
  const modalTitle = document.getElementById('modalTitle');
  const modalDownload = document.getElementById('modalDownload');
  const modalPrev = document.getElementById('modalPrev');
  const modalNext = document.getElementById('modalNext');
  const modalDeleteFilename = document.getElementById('modalDeleteFilename');
  const fileList = document.getElementById('fileList');

  if (!fileList || !modal) return;

  const initialPage = parseInt(fileList.dataset.page || '1', 10);
  const totalPages = parseInt(fileList.dataset.totalPages || '1', 10);
  const tokenId = fileList.dataset.tokenId || '';
  const query = fileList.dataset.query || '';

  let mediaItems = [];
  let currentIndex = -1;
  let loadedPages = new Set([initialPage]);
  let lowestLoadedPage = initialPage;
  let highestLoadedPage = initialPage;
  let isFetching = false;
  let currentTextController = null;
  let currentLoadedText = '';
  let isTextWrapped = false;

  function formatBytes(bytes) {
    if (bytes === 0) return '0 B';
    const k = 1024;
    const sizes = ['B', 'KB', 'MB', 'GB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i];
  }

  function extractMediaFromDocument(doc, pageNum) {
    const previews = doc.querySelectorAll('.file-preview[data-is-media="true"]');
    const items = [];
    previews.forEach(p => {
      items.push({
        url: p.dataset.previewUrl,
        name: p.dataset.fileName,
        rawName: p.dataset.rawName || p.dataset.fileName,
        type: p.dataset.mediaType || 'image',
        page: pageNum
      });
    });
    return items;
  }

  mediaItems = extractMediaFromDocument(document, initialPage);

  function createCustomPlayer(opts) {
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

    if (!mediaEl) return null;

    let isScrubbing = false;
    let idleTimer = null;
    const rates = [1, 1.25, 1.5, 2, 0.5];
    let rateIdx = 0;

    const savedVol = parseFloat(localStorage.getItem('uploadserver_media_vol') ?? '1');
    const savedMuted = localStorage.getItem('uploadserver_media_muted') === 'true';
    mediaEl.volume = isNaN(savedVol) ? 1 : Math.max(0, Math.min(1, savedVol));
    mediaEl.muted = savedMuted;
    if (volSlider) volSlider.value = mediaEl.muted ? 0 : mediaEl.volume;
    updateMuteIcon();

    function formatTime(sec) {
      if (isNaN(sec) || !isFinite(sec) || sec < 0) return '0:00';
      const s = Math.floor(sec % 60);
      const m = Math.floor((sec / 60) % 60);
      const h = Math.floor(sec / 3600);
      const ss = s < 10 ? '0' + s : s;
      if (h > 0) {
        const mm = m < 10 ? '0' + m : m;
        return `${h}:${mm}:${ss}`;
      }
      return `${m}:${ss}`;
    }

    function updateMuteIcon() {
      const isMuted = mediaEl.muted || mediaEl.volume === 0;
      muteBtn?.querySelector('.vol-high-icon')?.classList.toggle('hidden', isMuted);
      muteBtn?.querySelector('.vol-mute-icon')?.classList.toggle('hidden', !isMuted);
      if (volSlider) {
        volSlider.value = isMuted ? 0 : mediaEl.volume;
        const pct = isMuted ? 0 : Math.round(mediaEl.volume * 100);
        volSlider.style.setProperty('--vol-percent', `${pct}%`);
      }
    }

    function updatePlayBtn() {
      const isPaused = mediaEl.paused || mediaEl.ended;
      playBtn?.querySelector('.play-icon')?.classList.toggle('hidden', !isPaused);
      playBtn?.querySelector('.pause-icon')?.classList.toggle('hidden', isPaused);
      if (discEl) {
        discEl.classList.toggle('playing', !isPaused);
      }
    }

    function flashCenter(isPlay) {
      if (!centerFlashEl) return;
      const playIcon = centerFlashEl.querySelector('.flash-play');
      const pauseIcon = centerFlashEl.querySelector('.flash-pause');
      if (playIcon) playIcon.classList.toggle('hidden', !isPlay);
      if (pauseIcon) pauseIcon.classList.toggle('hidden', isPlay);
      centerFlashEl.classList.add('flash');
      setTimeout(() => centerFlashEl.classList.remove('flash'), 300);
    }

    function togglePlay(showFlash = false) {
      if (mediaEl.paused || mediaEl.ended) {
        mediaEl.play().catch(() => { });
        if (showFlash) flashCenter(true);
      } else {
        mediaEl.pause();
        if (showFlash) flashCenter(false);
      }
    }

    function updateProgress() {
      if (isScrubbing) return;
      const cur = mediaEl.currentTime || 0;
      const dur = mediaEl.duration || 0;
      const pct = dur > 0 ? (cur / dur) * 100 : 0;
      if (progressEl) progressEl.style.width = `${pct}%`;
      if (thumbEl) thumbEl.style.left = `${pct}%`;
      if (curTimeEl) curTimeEl.textContent = formatTime(cur);
      if (durTimeEl && dur > 0) durTimeEl.textContent = formatTime(dur);
    }

    function updateBuffered() {
      if (!bufferedEl || !mediaEl.duration) return;
      if (mediaEl.buffered.length > 0) {
        const end = mediaEl.buffered.end(mediaEl.buffered.length - 1);
        const pct = (end / mediaEl.duration) * 100;
        bufferedEl.style.width = `${Math.min(100, pct)}%`;
      }
    }

    function seekTo(pct) {
      if (!mediaEl.duration) return;
      const clamped = Math.max(0, Math.min(1, pct));
      mediaEl.currentTime = clamped * mediaEl.duration;
      if (progressEl) progressEl.style.width = `${clamped * 100}%`;
      if (thumbEl) thumbEl.style.left = `${clamped * 100}%`;
      if (curTimeEl) curTimeEl.textContent = formatTime(mediaEl.currentTime);
    }

    function handleScrub(e) {
      if (!timeline) return;
      const rect = timeline.getBoundingClientRect();
      const clientX = e.clientX ?? (e.touches && e.touches[0] ? e.touches[0].clientX : 0);
      const pct = (clientX - rect.left) / rect.width;
      seekTo(pct);
    }

    timeline?.addEventListener('pointerdown', (e) => {
      isScrubbing = true;
      timeline.classList.add('scrubbing');
      handleScrub(e);
      window.addEventListener('pointermove', handlePointerMove);
      window.addEventListener('pointerup', handlePointerUp);
    });

    function handlePointerMove(e) {
      if (isScrubbing) handleScrub(e);
    }

    function handlePointerUp(e) {
      if (isScrubbing) {
        handleScrub(e);
        isScrubbing = false;
        timeline?.classList.remove('scrubbing');
        window.removeEventListener('pointermove', handlePointerMove);
        window.removeEventListener('pointerup', handlePointerUp);
      }
    }

    playBtn?.addEventListener('click', () => togglePlay());
    backBtn?.addEventListener('click', () => {
      mediaEl.currentTime = Math.max(0, mediaEl.currentTime - 10);
    });
    fwdBtn?.addEventListener('click', () => {
      mediaEl.currentTime = Math.min(mediaEl.duration || 0, mediaEl.currentTime + 10);
    });

    volSlider?.addEventListener('input', (e) => {
      const val = parseFloat(e.target.value);
      mediaEl.volume = val;
      mediaEl.muted = val === 0;
      localStorage.setItem('uploadserver_media_vol', val.toString());
      localStorage.setItem('uploadserver_media_muted', mediaEl.muted.toString());
      updateMuteIcon();
    });

    muteBtn?.addEventListener('click', () => {
      mediaEl.muted = !mediaEl.muted;
      if (!mediaEl.muted && mediaEl.volume === 0) {
        mediaEl.volume = 0.5;
      }
      localStorage.setItem('uploadserver_media_muted', mediaEl.muted.toString());
      updateMuteIcon();
    });

    rateBtn?.addEventListener('click', () => {
      rateIdx = (rateIdx + 1) % rates.length;
      const r = rates[rateIdx];
      mediaEl.playbackRate = r;
      rateBtn.textContent = `${r}x`;
    });

    loopBtn?.addEventListener('click', () => {
      mediaEl.loop = !mediaEl.loop;
      loopBtn.classList.toggle('active', mediaEl.loop);
    });

    pipBtn?.addEventListener('click', async () => {
      try {
        if (document.pictureInPictureElement) {
          await document.exitPictureInPicture();
        } else if (mediaEl.requestPictureInPicture) {
          await mediaEl.requestPictureInPicture();
        }
      } catch (_) { }
    });

    function isFullscreenActive() {
      return !!(
        document.fullscreenElement ||
        document.webkitFullscreenElement ||
        document.mozFullScreenElement ||
        document.msFullscreenElement ||
        (mediaEl && mediaEl.webkitDisplayingFullscreen)
      );
    }

    function enterVideoFullscreenFallback() {
      if (mediaEl && typeof mediaEl.webkitEnterFullscreen === 'function') {
        mediaEl.webkitEnterFullscreen();
      } else if (mediaEl && typeof mediaEl.requestFullscreen === 'function') {
        mediaEl.requestFullscreen().catch(() => { });
      } else if (mediaEl && typeof mediaEl.webkitRequestFullscreen === 'function') {
        mediaEl.webkitRequestFullscreen();
      }
    }

    function toggleFullscreen() {
      if (isFullscreenActive()) {
        if (document.exitFullscreen) {
          document.exitFullscreen().catch(() => { });
        } else if (document.webkitExitFullscreen) {
          document.webkitExitFullscreen();
        } else if (document.mozCancelFullScreen) {
          document.mozCancelFullScreen();
        } else if (document.msExitFullscreen) {
          document.msExitFullscreen();
        } else if (mediaEl && typeof mediaEl.webkitExitFullscreen === 'function') {
          mediaEl.webkitExitFullscreen();
        }
      } else {
        const req = containerEl?.requestFullscreen ||
          containerEl?.webkitRequestFullscreen ||
          containerEl?.mozRequestFullScreen ||
          containerEl?.msRequestFullscreen;
        if (req) {
          try {
            const res = req.call(containerEl);
            if (res && typeof res.catch === 'function') {
              res.catch(() => enterVideoFullscreenFallback());
            }
          } catch (_) {
            enterVideoFullscreenFallback();
          }
        } else {
          enterVideoFullscreenFallback();
        }
      }
    }

    fullscreenBtn?.addEventListener('click', (e) => {
      e.stopPropagation();
      toggleFullscreen();
    });

    function updateFullscreenIcon() {
      const isFs = isFullscreenActive();
      fullscreenBtn?.querySelector('.fs-enter-icon')?.classList.toggle('hidden', isFs);
      fullscreenBtn?.querySelector('.fs-exit-icon')?.classList.toggle('hidden', !isFs);
    }

    document.addEventListener('fullscreenchange', updateFullscreenIcon);
    document.addEventListener('webkitfullscreenchange', updateFullscreenIcon);
    document.addEventListener('mozfullscreenchange', updateFullscreenIcon);
    document.addEventListener('MSFullscreenChange', updateFullscreenIcon);
    mediaEl.addEventListener('webkitbeginfullscreen', updateFullscreenIcon);
    mediaEl.addEventListener('webkitendfullscreen', updateFullscreenIcon);

    mediaEl.addEventListener('timeupdate', updateProgress);
    mediaEl.addEventListener('progress', updateBuffered);
    mediaEl.addEventListener('play', updatePlayBtn);
    mediaEl.addEventListener('pause', updatePlayBtn);
    mediaEl.addEventListener('ended', () => {
      updatePlayBtn();
      if (!mediaEl.loop) {
        if (progressEl) progressEl.style.width = '0%';
        if (thumbEl) thumbEl.style.left = '0%';
      }
    });
    mediaEl.addEventListener('loadedmetadata', () => {
      updateProgress();
      updateBuffered();
      updatePlayBtn();
      if (durTimeEl && mediaEl.duration) {
        durTimeEl.textContent = formatTime(mediaEl.duration);
      }
    });

    if (containerEl && mediaEl.tagName === 'VIDEO') {
      mediaEl.addEventListener('click', (e) => {
        e.stopPropagation();
        togglePlay(true);
      });
      mediaEl.addEventListener('dblclick', (e) => {
        e.stopPropagation();
        toggleFullscreen();
      });

      const controls = containerEl.querySelector('.video-controls');
      function resetIdle() {
        if (!controls) return;
        controls.classList.remove('idle-hidden');
        clearTimeout(idleTimer);
        if (!mediaEl.paused && !mediaEl.ended) {
          idleTimer = setTimeout(() => {
            controls.classList.add('idle-hidden');
          }, 2500);
        }
      }
      containerEl.addEventListener('mousemove', resetIdle);
      containerEl.addEventListener('touchstart', resetIdle, { passive: true });
      mediaEl.addEventListener('play', resetIdle);
      mediaEl.addEventListener('pause', () => {
        clearTimeout(idleTimer);
        controls?.classList.remove('idle-hidden');
      });
    }

    function reset() {
      mediaEl.pause();
      mediaEl.removeAttribute('src');
      mediaEl.load();
      if (progressEl) progressEl.style.width = '0%';
      if (bufferedEl) bufferedEl.style.width = '0%';
      if (thumbEl) thumbEl.style.left = '0%';
      if (curTimeEl) curTimeEl.textContent = '0:00';
      if (durTimeEl) durTimeEl.textContent = '0:00';
      if (rateBtn) rateBtn.textContent = '1x';
      rateIdx = 0;
      mediaEl.playbackRate = 1;
      mediaEl.loop = false;
      loopBtn?.classList.remove('active');
      updatePlayBtn();
    }

    return {
      togglePlay,
      seekDelta: (sec) => {
        if (mediaEl.duration) {
          mediaEl.currentTime = Math.max(0, Math.min(mediaEl.duration, mediaEl.currentTime + sec));
        }
      },
      toggleMute: () => {
        mediaEl.muted = !mediaEl.muted;
        localStorage.setItem('uploadserver_media_muted', mediaEl.muted.toString());
        updateMuteIcon();
      },
      toggleFullscreen,
      togglePip: async () => {
        try {
          if (document.pictureInPictureElement) {
            await document.exitPictureInPicture();
          } else if (mediaEl.requestPictureInPicture) {
            await mediaEl.requestPictureInPicture();
          }
        } catch (_) { }
      },
      reset,
    };
  }

  const videoPlayer = createCustomPlayer({
    mediaEl: modalVideo,
    containerEl: modalVideoBox,
    playBtn: document.getElementById('videoPlayBtn'),
    backBtn: document.getElementById('videoBackBtn'),
    fwdBtn: document.getElementById('videoFwdBtn'),
    timeline: document.getElementById('videoTimeline'),
    bufferedEl: document.getElementById('videoBuffered'),
    progressEl: document.getElementById('videoProgress'),
    thumbEl: document.getElementById('videoThumb'),
    curTimeEl: document.getElementById('videoCurTime'),
    durTimeEl: document.getElementById('videoDurTime'),
    volSlider: document.getElementById('videoVolSlider'),
    muteBtn: document.getElementById('videoMuteBtn'),
    rateBtn: document.getElementById('videoRateBtn'),
    loopBtn: document.getElementById('videoLoopBtn'),
    pipBtn: document.getElementById('videoPipBtn'),
    fullscreenBtn: document.getElementById('videoFullscreenBtn'),
    centerFlashEl: document.getElementById('videoCenterFlash'),
  });

  const audioPlayer = createCustomPlayer({
    mediaEl: modalAudio,
    containerEl: modalAudioBox,
    playBtn: document.getElementById('audioPlayBtn'),
    backBtn: document.getElementById('audioBackBtn'),
    fwdBtn: document.getElementById('audioFwdBtn'),
    timeline: document.getElementById('audioTimeline'),
    bufferedEl: document.getElementById('audioBuffered'),
    progressEl: document.getElementById('audioProgress'),
    thumbEl: document.getElementById('audioThumb'),
    curTimeEl: document.getElementById('audioCurTime'),
    durTimeEl: document.getElementById('audioDurTime'),
    volSlider: document.getElementById('audioVolSlider'),
    muteBtn: document.getElementById('audioMuteBtn'),
    rateBtn: document.getElementById('audioRateBtn'),
    loopBtn: document.getElementById('audioLoopBtn'),
    discEl: audioDisc,
  });

  function updateScrollLock() {
    if (typeof window._updateScrollLock === 'function') {
      window._updateScrollLock();
      return;
    }
    const openDialogs = document.querySelectorAll('dialog[open]').length > 0;
    if (openDialogs) {
      document.documentElement.classList.add('modal-open');
      document.body.classList.add('modal-open');
    } else {
      document.documentElement.classList.remove('modal-open');
      document.body.classList.remove('modal-open');
    }
  }

  function preventAutofocus(target) {
    setTimeout(() => {
      if (document.activeElement && ['INPUT', 'TEXTAREA', 'SELECT'].includes(document.activeElement.tagName)) {
        document.activeElement.blur();
      }
      if (target && typeof target.focus === 'function') {
        try { target.focus({ preventScroll: true }); } catch (_) { }
      }
    }, 0);
  }

  function prepareModal(index) {
    if (index < 0 || index >= mediaItems.length) return;
    currentIndex = index;
    updateModalContent();
    updateScrollLock();
    preventAutofocus(modal);
    maybePrefetch();
  }

  function openModal(index) {
    prepareModal(index);
    if (!modal.open) {
      try {
        modal.showModal();
      } catch (_) { }
    }
    updateScrollLock();
    preventAutofocus(modal);
  }

  function stopPlayback() {
    if (currentTextController) {
      currentTextController.abort();
      currentTextController = null;
    }
    currentLoadedText = '';

    videoPlayer?.reset();
    audioPlayer?.reset();

    if (modalVideoBox) {
      modalVideoBox.classList.add('hidden');
    }
    if (modalAudioBox) {
      modalAudioBox.classList.add('hidden');
    }
    if (modalImg) {
      modalImg.src = '';
      modalImg.classList.add('hidden');
    }
    if (modalPdf) {
      modalPdf.src = 'about:blank';
      modalPdf.classList.add('hidden');
    }
    if (modalTextBox) {
      modalTextBox.classList.add('hidden');
    }
    if (modalFontBox) {
      modalFontBox.classList.add('hidden');
    }
  }

  function closeModal() {
    stopPlayback();
    if (modal && modal.open) modal.close();
    updateScrollLock();
  }

  function updateModalContent() {
    if (mediaItems.length === 0) return;
    if (currentIndex < 0) currentIndex = 0;
    if (currentIndex >= mediaItems.length) currentIndex = mediaItems.length - 1;
    const item = mediaItems[currentIndex];
    stopPlayback();

    modalTitle.textContent = item.rawName || item.name;
    modalDownload.href = item.url;
    if (modalDeleteFilename) {
      modalDeleteFilename.value = item.rawName || item.name;
    }

    if (modalPrev) {
      const canPrev = currentIndex > 0 || lowestLoadedPage > 1;
      modalPrev.disabled = !canPrev;
    }
    if (modalNext) {
      const canNext = currentIndex < mediaItems.length - 1 || highestLoadedPage < totalPages;
      modalNext.disabled = !canNext;
    }

    const ext = ((item.rawName || item.name).split('.').pop() || '').toLowerCase();

    switch (item.type) {
      case 'video':
        if (modalVideoBox && modalVideo) {
          modalVideo.src = item.url;
          modalVideoBox.classList.remove('hidden');
          modalVideo.play().catch(() => { });
        }
        break;

      case 'audio':
        if (modalAudioBox && modalAudio) {
          modalAudio.src = item.url;
          if (modalAudioName) modalAudioName.textContent = item.rawName || item.name;
          modalAudioBox.classList.remove('hidden');
          modalAudio.play().catch(() => { });
        }
        break;

      case 'pdf':
        if (modalPdf) {
          modalPdf.src = item.url;
          modalPdf.classList.remove('hidden');
        }
        break;

      case 'font':
        if (modalFontBox) {
          modalFontBox.classList.remove('hidden');
          if (fontFamilyName) fontFamilyName.textContent = item.name;
          if (fontFormatBadge) fontFormatBadge.textContent = (ext || 'FONT').toUpperCase();
          const fontId = 'font-preview-' + Math.random().toString(36).substring(2, 9);
          const fontFace = new FontFace(fontId, `url("${item.url}")`);
          fontFace.load().then((loadedFace) => {
            document.fonts.add(loadedFace);
            const scrollArea = modalFontBox.querySelector('.font-sample-scroll');
            if (scrollArea) scrollArea.style.fontFamily = `"${fontId}", system-ui, sans-serif`;
          }).catch((err) => {
            console.error('Failed to load font specimen:', err);
          });
        }
        break;

      case 'text':
        if (modalTextBox) {
          modalTextBox.classList.remove('hidden');
          if (textTypeBadge) textTypeBadge.textContent = (ext || 'TXT').toUpperCase();
          if (textStats) textStats.textContent = 'Loading…';
          if (textLoading) textLoading.classList.remove('hidden');
          if (textError) textError.classList.add('hidden');
          if (textScrollArea) textScrollArea.classList.add('hidden');

          currentTextController = new AbortController();
          const controller = currentTextController;

          (async () => {
            try {
              const resp = await fetch(item.url, { signal: controller.signal });
              if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
              const text = await resp.text();
              if (controller.signal.aborted) return;

              currentLoadedText = text;
              if (modalTextCode) {
                modalTextCode.textContent = text;
              } else if (modalTextPre) {
                modalTextPre.textContent = text;
              }

              const lines = text.split('\n');
              const lineCount = lines.length;
              const byteSize = new Blob([text]).size;
              if (textStats) {
                textStats.textContent = `${lineCount.toLocaleString()} line${lineCount !== 1 ? 's' : ''} · ${formatBytes(byteSize)}`;
              }

              if (textLineNumbers) {
                let lineHtml = '';
                for (let i = 1; i <= lineCount; i++) {
                  lineHtml += `<span>${i}</span>\n`;
                }
                textLineNumbers.innerHTML = lineHtml;
              }

              if (textScrollArea) {
                textScrollArea.scrollTop = 0;
                textScrollArea.scrollLeft = 0;
              }

              if (textLoading) textLoading.classList.add('hidden');
              if (textError) textError.classList.add('hidden');
              if (textScrollArea) textScrollArea.classList.remove('hidden');
            } catch (err) {
              if (controller.signal.aborted) return;
              console.error('Failed to load text preview:', err);
              if (textLoading) textLoading.classList.add('hidden');
              if (textError) textError.classList.remove('hidden');
              if (textErrorMsg) textErrorMsg.textContent = 'Unable to display text preview (' + (err.message || 'network error') + ').';
              if (textStats) textStats.textContent = 'Error';
            }
          })();
        }
        break;

      case 'image':
      default:
        if (modalImg) {
          modalImg.src = item.url;
          modalImg.classList.remove('hidden');
        }
        break;
    }
  }

  textCopyBtn?.addEventListener('click', async () => {
    if (!currentLoadedText) return;
    try {
      await navigator.clipboard.writeText(currentLoadedText);
      const orig = textCopyBtn.textContent;
      textCopyBtn.textContent = '✓ Copied';
      textCopyBtn.classList.add('btn-success');
      setTimeout(() => {
        textCopyBtn.textContent = orig;
        textCopyBtn.classList.remove('btn-success');
      }, 2000);
    } catch (err) {
      console.error('Clipboard copy failed:', err);
    }
  });

  textWrapBtn?.addEventListener('click', () => {
    isTextWrapped = !isTextWrapped;
    textWrapBtn.textContent = isTextWrapped ? 'Wrap: On' : 'Wrap: Off';
    if (isTextWrapped) {
      modalTextPre?.classList.add('wrapped');
      textLineNumbers?.classList.add('wrapped');
    } else {
      modalTextPre?.classList.remove('wrapped');
      textLineNumbers?.classList.remove('wrapped');
    }
  });

  function navigate(direction) {
    if (direction === 'next') {
      if (currentIndex < mediaItems.length - 1) {
        currentIndex++;
        updateModalContent();
        maybePrefetch();
      } else if (highestLoadedPage < totalPages) {
        fetchPage(highestLoadedPage + 1, 'next');
      }
    } else if (direction === 'prev') {
      if (currentIndex > 0) {
        currentIndex--;
        updateModalContent();
        maybePrefetch();
      } else if (lowestLoadedPage > 1) {
        fetchPage(lowestLoadedPage - 1, 'prev');
      }
    }
  }

  function maybePrefetch() {
    if (isFetching) return;
    if (currentIndex >= mediaItems.length - 3 && highestLoadedPage < totalPages && !loadedPages.has(highestLoadedPage + 1)) {
      fetchPage(highestLoadedPage + 1, null);
    }
    if (currentIndex <= 2 && lowestLoadedPage > 1 && !loadedPages.has(lowestLoadedPage - 1)) {
      fetchPage(lowestLoadedPage - 1, null);
    }
  }

  async function fetchPage(pageNum, navDirection) {
    if (loadedPages.has(pageNum) || isFetching) return;
    isFetching = true;
    if (modalNext && navDirection === 'next') modalNext.disabled = true;
    if (modalPrev && navDirection === 'prev') modalPrev.disabled = true;

    try {
      const url = `/_/uploads/${encodeURIComponent(tokenId)}?page=${pageNum}&q=${encodeURIComponent(query)}`;
      const resp = await fetch(url);
      if (!resp.ok) throw new Error('Page fetch failed');
      const html = await resp.text();
      const doc = new DOMParser().parseFromString(html, 'text/html');
      const newItems = extractMediaFromDocument(doc, pageNum);

      loadedPages.add(pageNum);
      if (pageNum > highestLoadedPage) {
        mediaItems = mediaItems.concat(newItems);
        highestLoadedPage = pageNum;
        if (navDirection === 'next' && newItems.length > 0) {
          currentIndex++;
        }
      } else if (pageNum < lowestLoadedPage) {
        mediaItems = newItems.concat(mediaItems);
        lowestLoadedPage = pageNum;
        currentIndex += newItems.length;
        if (navDirection === 'prev' && newItems.length > 0) {
          currentIndex--;
        }
      }
    } catch (err) {
      console.error('Failed to load page in background:', err);
    } finally {
      isFetching = false;
      updateModalContent();
    }
  }

  document.addEventListener('click', (e) => {
    const preview = e.target.closest('.file-preview[data-is-media="true"]');
    if (preview && !preview.hasAttribute('commandfor')) {
      const url = preview.dataset.previewUrl;
      const idx = mediaItems.findIndex(item => item.url === url);
      if (idx !== -1) {
        openModal(idx);
      }
    }
  });

  modalClose?.addEventListener('click', () => closeModal());

  modal.addEventListener('command', (e) => {
    if (e.command === 'show-modal') {
      const invoker = _cmdInvoker(e);
      if (invoker && invoker.dataset.previewUrl) {
        const url = invoker.dataset.previewUrl;
        const idx = mediaItems.findIndex(item => item.url === url);
        if (idx !== -1) {
          prepareModal(idx);
        }
      }
    } else if (e.command === '--prev') {
      navigate('prev');
    } else if (e.command === '--next') {
      navigate('next');
    } else if (e.command === 'close') {
      closeModal();
    }
  });

  if (modal) {
    modal.addEventListener('click', (e) => {
      if (e.target === modal) closeModal();
    });
    modal.addEventListener('cancel', () => {
      closeModal();
    });
    modal.addEventListener('close', () => {
      stopPlayback();
      updateScrollLock();
    });
  }

  document.addEventListener('keydown', (e) => {
    if (modal && modal.open) {
      if (e.target.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(e.target.tagName)) {
        if (e.key === 'Escape') closeModal();
        return;
      }

      const activeItem = currentIndex >= 0 && currentIndex < mediaItems.length ? mediaItems[currentIndex] : null;
      const isMediaAudio = activeItem && activeItem.type === 'audio';
      const isMediaVideo = activeItem && activeItem.type === 'video';

      if (e.key === ' ' || e.key === 'k' || e.key === 'K') {
        if (isMediaVideo) {
          e.preventDefault();
          videoPlayer?.togglePlay(true);
          return;
        } else if (isMediaAudio) {
          e.preventDefault();
          audioPlayer?.togglePlay();
          return;
        }
      } else if (e.key === 'm' || e.key === 'M') {
        if (isMediaVideo) {
          e.preventDefault();
          videoPlayer?.toggleMute();
          return;
        } else if (isMediaAudio) {
          e.preventDefault();
          audioPlayer?.toggleMute();
          return;
        }
      } else if (e.key === 'j' || e.key === 'J') {
        if (isMediaVideo) {
          e.preventDefault();
          videoPlayer?.seekDelta(-10);
          return;
        } else if (isMediaAudio) {
          e.preventDefault();
          audioPlayer?.seekDelta(-10);
          return;
        }
      } else if (e.key === 'l' || e.key === 'L') {
        if (isMediaVideo) {
          e.preventDefault();
          videoPlayer?.seekDelta(10);
          return;
        } else if (isMediaAudio) {
          e.preventDefault();
          audioPlayer?.seekDelta(10);
          return;
        }
      } else if ((e.key === 'f' || e.key === 'F') && isMediaVideo) {
        e.preventDefault();
        videoPlayer?.toggleFullscreen();
        return;
      } else if ((e.key === 'p' || e.key === 'P') && isMediaVideo) {
        e.preventDefault();
        videoPlayer?.togglePip();
        return;
      }

      if (e.key === 'ArrowLeft' || e.key === 'a' || e.key === 'A') {
        e.preventDefault();
        navigate('prev');
      } else if (e.key === 'ArrowRight' || e.key === 'd' || e.key === 'D') {
        e.preventDefault();
        navigate('next');
      } else if (e.key === 'Escape') {
        closeModal();
      }
    }
  });

  let touchStartX = 0;
  let touchStartY = 0;
  if (modal) {
    modal.addEventListener('touchstart', (e) => {
      if (e.touches.length === 1) {
        touchStartX = e.touches[0].clientX;
        touchStartY = e.touches[0].clientY;
      }
    }, { passive: true });

    modal.addEventListener('touchend', (e) => {
      if (e.changedTouches.length === 1) {
        const deltaX = e.changedTouches[0].clientX - touchStartX;
        const deltaY = e.changedTouches[0].clientY - touchStartY;
        if (Math.abs(deltaX) > 40 && Math.abs(deltaY) < 60) {
          if (deltaX < 0) {
            navigate('next');
          } else {
            navigate('prev');
          }
        }
      }
    }, { passive: true });
  }

  const searchForm = document.getElementById('searchForm');
  const searchInput = document.getElementById('searchInput');
  const searchClear = document.getElementById('searchClear');

  const doClear = () => {
    window.location.href = window.location.pathname;
  };

  searchClear?.addEventListener('click', doClear);

  searchForm?.addEventListener('command', (e) => {
    if (e.command === '--clear-search') {
      doClear();
    }
  });

  if (searchForm && searchInput) {
    searchForm.addEventListener('submit', (e) => {
      if (!searchInput.value.trim()) {
        e.preventDefault();
        doClear();
      }
    });
  }

  if (searchInput) {
    document.addEventListener('keydown', (e) => {
      if (modal && modal.open) return;
      if (e.key === '/' && document.activeElement !== searchInput && !['INPUT', 'TEXTAREA', 'SELECT'].includes(document.activeElement.tagName)) {
        e.preventDefault();
        searchInput.focus();
      } else if (e.key === 'Escape' && document.activeElement === searchInput) {
        if (searchInput.value) {
          window.location.href = window.location.pathname;
        } else {
          searchInput.blur();
        }
      }
    });
  }
})();
