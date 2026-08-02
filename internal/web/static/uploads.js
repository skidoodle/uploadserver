(function () {
  const modal = document.getElementById('imgModal');
  const modalImg = document.getElementById('modalImg');
  const modalVideo = document.getElementById('modalVideo');
  const modalTitle = document.getElementById('modalTitle');
  const modalDownload = document.getElementById('modalDownload');
  const modalClose = document.getElementById('modalClose');
  const modalPrev = document.getElementById('modalPrev');
  const modalNext = document.getElementById('modalNext');
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

  function extractMediaFromDocument(doc, pageNum) {
    const previews = doc.querySelectorAll('.file-preview[data-is-media="true"]');
    const items = [];
    previews.forEach(p => {
      items.push({
        url: p.dataset.previewUrl,
        name: p.dataset.fileName,
        type: p.dataset.mediaType || 'image',
        page: pageNum
      });
    });
    return items;
  }

  mediaItems = extractMediaFromDocument(document, initialPage);

  function openModal(index) {
    if (index < 0 || index >= mediaItems.length) return;
    currentIndex = index;
    updateModalContent();
    document.body.style.overflow = 'hidden';
    if (!modal.open) modal.showModal();
    maybePrefetch();
  }

  function stopPlayback() {
    if (modalVideo) {
      modalVideo.pause();
      modalVideo.src = '';
      modalVideo.classList.add('hidden');
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

    if (item.type === 'video') {
      if (modalImg) modalImg.classList.add('hidden');
      if (modalVideo) {
        modalVideo.pause();
        modalVideo.src = item.url;
        modalVideo.classList.remove('hidden');
      }
    } else {
      stopPlayback();
      if (modalImg) {
        modalImg.src = item.url;
        modalImg.classList.remove('hidden');
      }
    }

    modalTitle.textContent = item.name;
    modalDownload.href = item.url;

    if (modalPrev) {
      const canPrev = currentIndex > 0 || lowestLoadedPage > 1;
      modalPrev.disabled = !canPrev;
    }
    if (modalNext) {
      const canNext = currentIndex < mediaItems.length - 1 || highestLoadedPage < totalPages;
      modalNext.disabled = !canNext;
    }
  }

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

  function bindPreviews() {
    document.querySelectorAll('.file-preview[data-is-media="true"]').forEach((preview) => {
      preview.addEventListener('click', () => {
        const url = preview.dataset.previewUrl;
        const idx = mediaItems.findIndex(item => item.url === url);
        if (idx !== -1) {
          openModal(idx);
        }
      });
    });
  }

  bindPreviews();

  if (modalClose) modalClose.addEventListener('click', closeModal);
  if (modalPrev) modalPrev.addEventListener('click', () => navigate('prev'));
  if (modalNext) modalNext.addEventListener('click', () => navigate('next'));

  if (modal) {
    modal.addEventListener('click', (e) => {
      if (e.target === modal) closeModal();
    });
    modal.addEventListener('cancel', () => {
      closeModal();
    });
  }

  document.addEventListener('keydown', (e) => {
    if (modal && modal.open) {
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

  if (searchClear) {
    searchClear.addEventListener('click', () => {
      window.location.href = window.location.pathname;
    });
  }

  if (searchForm && searchInput) {
    searchForm.addEventListener('submit', (e) => {
      if (!searchInput.value.trim()) {
        e.preventDefault();
        window.location.href = window.location.pathname;
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
