(function () {
  const modal = document.getElementById('imgModal');
  const modalImg = document.getElementById('modalImg');
  const modalVideo = document.getElementById('modalVideo');
  const modalAudioBox = document.getElementById('modalAudioBox');
  const modalAudio = document.getElementById('modalAudio');
  const modalAudioName = document.getElementById('modalAudioName');
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

  function prepareModal(index) {
    if (index < 0 || index >= mediaItems.length) return;
    currentIndex = index;
    updateModalContent();
    document.body.style.overflow = 'hidden';
    maybePrefetch();
  }

  function openModal(index) {
    prepareModal(index);
    if (!modal.open) {
      try {
        modal.showModal();
      } catch (_) { }
    }
  }

  function stopPlayback() {
    if (currentTextController) {
      currentTextController.abort();
      currentTextController = null;
    }
    currentLoadedText = '';

    if (modalVideo) {
      modalVideo.pause();
      modalVideo.removeAttribute('src');
      modalVideo.load();
      modalVideo.classList.add('hidden');
    }
    if (modalAudio) {
      modalAudio.pause();
      modalAudio.removeAttribute('src');
      modalAudio.load();
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
    document.body.style.overflow = '';
    stopPlayback();
    if (modal && modal.open) modal.close();
  }

  function updateModalContent() {
    if (currentIndex < 0 || currentIndex >= mediaItems.length) return;
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
        if (modalVideo) {
          modalVideo.src = item.url;
          modalVideo.classList.remove('hidden');
        }
        break;

      case 'audio':
        if (modalAudioBox && modalAudio) {
          modalAudio.src = item.url;
          if (modalAudioName) modalAudioName.textContent = item.rawName || item.name;
          modalAudioBox.classList.remove('hidden');
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
    stopPlayback();
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

  modalPrev?.addEventListener('click', () => navigate('prev'));
  modalNext?.addEventListener('click', () => navigate('next'));
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
      document.body.style.overflow = '';
      stopPlayback();
    });
  }

  document.addEventListener('keydown', (e) => {
    if (modal && modal.open) {
      if (e.target.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(e.target.tagName)) {
        if (e.key === 'Escape') closeModal();
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
