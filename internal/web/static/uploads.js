(function () {
  const modal = document.getElementById('imgModal');
  const modalImg = document.getElementById('modalImg');
  const modalTitle = document.getElementById('modalTitle');
  const modalDownload = document.getElementById('modalDownload');
  const modalClose = document.getElementById('modalClose');

  function closeModal() {
    document.body.style.overflow = '';
    if (modal) modal.close();
  }

  if (modal && modalClose) {
    modalClose.addEventListener('click', closeModal);
    modal.addEventListener('click', (e) => {
      if (e.target === modal) closeModal();
    });
    modal.addEventListener('cancel', () => {
      document.body.style.overflow = '';
    });
  }

  document.querySelectorAll('.file-preview').forEach(preview => {
    preview.addEventListener('click', () => {
      const img = preview.querySelector('img');
      if (!img || !modal) return;
      const url = preview.dataset.previewUrl;
      const name = preview.dataset.fileName;
      modalImg.src = url;
      modalTitle.textContent = name;
      modalDownload.href = url;
      document.body.style.overflow = 'hidden';
      modal.showModal();
    });
  });

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
