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

  const PER_PAGE = 50;
  const list = document.getElementById('fileList');
  const summary = document.getElementById('pagerSummary');
  const controls = document.getElementById('pagerControls');
  const pagerBar = document.getElementById('pagerBar');
  const searchInput = document.getElementById('searchInput');
  const searchClear = document.getElementById('searchClear');
  const emptySearch = document.getElementById('emptySearch');
  const searchQueryText = document.getElementById('searchQueryText');

  if (!list) return;

  const allRows = Array.from(list.querySelectorAll('[data-page-item]'));
  const totalFiles = allRows.length;
  let matchingRows = allRows;
  let current = 1;

  function filterRows() {
    const rawVal = searchInput ? searchInput.value : '';
    const q = rawVal.trim().toLowerCase();

    if (searchClear) {
      searchClear.hidden = !q;
    }

    if (!q) {
      matchingRows = allRows;
      if (emptySearch) emptySearch.hidden = true;
    } else {
      matchingRows = allRows.filter(row => {
        const name = (row.dataset.fullName || '').toLowerCase();
        const ext = (row.dataset.ext || '').toLowerCase();
        return name.includes(q) || ext.includes(q);
      });

      if (emptySearch) {
        if (matchingRows.length === 0) {
          if (searchQueryText) searchQueryText.textContent = rawVal;
          emptySearch.hidden = false;
        } else {
          emptySearch.hidden = true;
        }
      }
    }

    allRows.forEach(row => { row.hidden = true; });

    current = 1;
    showPage(1);
  }

  function showPage(page) {
    current = page;
    const totalMatching = matchingRows.length;
    const totalPages = Math.ceil(totalMatching / PER_PAGE);

    const start = (page - 1) * PER_PAGE;
    const end = start + PER_PAGE;

    matchingRows.forEach((row, i) => {
      row.hidden = i < start || i >= end;
    });

    renderPager(totalMatching, totalPages);
  }

  function renderPager(totalMatching, totalPages) {
    if (!pagerBar || !summary || !controls) return;

    if (totalFiles === 0 || totalMatching === 0) {
      pagerBar.style.display = 'none';
      return;
    }

    const q = searchInput ? searchInput.value.trim() : '';

    if (totalFiles <= PER_PAGE && !q) {
      pagerBar.style.display = 'none';
      return;
    }

    pagerBar.style.display = '';

    const startNum = (current - 1) * PER_PAGE + 1;
    const endNum = Math.min(current * PER_PAGE, totalMatching);

    if (q) {
      summary.innerHTML = `Showing <strong>${startNum}–${endNum}</strong> of <strong>${totalMatching}</strong> matching file${totalMatching !== 1 ? 's' : ''} (filtered from ${totalFiles})`;
    } else {
      summary.innerHTML = `Showing <strong>${startNum}–${endNum}</strong> of <strong>${totalFiles}</strong> files`;
    }

    controls.innerHTML = '';
    if (totalPages <= 1) return;

    const prev = btn('‹ Prev', current > 1, () => showPage(current - 1));
    controls.appendChild(prev);

    const visible = pageRange(current, totalPages);
    let last = 0;
    for (const p of visible) {
      if (p - last > 1) {
        const dots = document.createElement('span');
        dots.className = 'pager-dots';
        dots.textContent = '…';
        controls.appendChild(dots);
      }
      const b = btn(String(p), true, () => showPage(p));
      if (p === current) b.classList.add('active');
      controls.appendChild(b);
      last = p;
    }

    const next = btn('Next ›', current < totalPages, () => showPage(current + 1));
    controls.appendChild(next);
  }

  function btn(label, enabled, onclick) {
    const b = document.createElement('button');
    b.type = 'button';
    b.className = 'pager-btn';
    b.textContent = label;
    b.disabled = !enabled;
    if (enabled) b.addEventListener('click', onclick);
    return b;
  }

  function pageRange(cur, max) {
    const delta = 2;
    const range = [];
    for (let i = Math.max(2, cur - delta); i <= Math.min(max - 1, cur + delta); i++) {
      range.push(i);
    }
    if (max <= 1) return [1];
    return [1, ...range, max];
  }

  if (searchInput) {
    searchInput.addEventListener('input', filterRows);

    if (searchClear) {
      searchClear.addEventListener('click', () => {
        searchInput.value = '';
        searchInput.focus();
        filterRows();
      });
    }

    document.addEventListener('keydown', (e) => {
      if (e.key === '/' && document.activeElement !== searchInput && !['INPUT', 'TEXTAREA', 'SELECT'].includes(document.activeElement.tagName)) {
        e.preventDefault();
        searchInput.focus();
      } else if (e.key === 'Escape' && document.activeElement === searchInput) {
        if (searchInput.value) {
          searchInput.value = '';
          filterRows();
        } else {
          searchInput.blur();
        }
      }
    });
  }

  filterRows();
})();
