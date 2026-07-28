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

  if (!list || !controls || !pagerBar) return;

  const rows = Array.from(list.querySelectorAll('[data-page-item]'));
  const total = rows.length;
  const pages = Math.ceil(total / PER_PAGE);
  let current = 1;

  if (total <= PER_PAGE) {
    pagerBar.style.display = 'none';
    return;
  }

  function show(page) {
    current = page;
    const start = (page - 1) * PER_PAGE;
    const end = start + PER_PAGE;
    rows.forEach((row, i) => {
      row.hidden = i < start || i >= end;
    });
    render();
  }

  function render() {
    const startNum = (current - 1) * PER_PAGE + 1;
    const endNum = Math.min(current * PER_PAGE, total);
    if (summary) {
      summary.innerHTML = `Showing <strong>${startNum}–${endNum}</strong> of <strong>${total}</strong> files`;
    }

    controls.innerHTML = '';

    const prev = btn('‹ Prev', current > 1, () => show(current - 1));
    controls.appendChild(prev);

    const visible = pageRange(current, pages);
    let last = 0;
    for (const p of visible) {
      if (p - last > 1) {
        const dots = document.createElement('span');
        dots.className = 'pager-dots';
        dots.textContent = '…';
        controls.appendChild(dots);
      }
      const b = btn(String(p), true, () => show(p));
      if (p === current) b.classList.add('active');
      controls.appendChild(b);
      last = p;
    }

    const next = btn('Next ›', current < pages, () => show(current + 1));
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

  show(1);
})();
