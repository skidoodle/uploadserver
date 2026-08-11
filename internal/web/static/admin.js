(function () {
  function getInvoker(e) {
    return e.source || e.invoker;
  }
  window._cmdInvoker = getInvoker;

  let savedScrollY = 0;
  let isScrollLocked = false;

  function onTouchMoveModal(e) {
    const scrollable = e.target.closest('.text-scroll-area, .font-sample-scroll');
    if (!scrollable) {
      if (e.cancelable) e.preventDefault();
      return;
    }
    if (scrollable.scrollHeight <= scrollable.clientHeight && scrollable.scrollWidth <= scrollable.clientWidth) {
      if (e.cancelable) e.preventDefault();
    }
  }

  function updateScrollLock() {
    const openDialogs = Array.from(document.querySelectorAll('dialog')).filter((d) => d.open);
    if (openDialogs.length > 0) {
      if (!isScrollLocked) {
        savedScrollY = window.scrollY || window.pageYOffset || document.documentElement.scrollTop || 0;
        document.body.style.position = 'fixed';
        document.body.style.top = `-${savedScrollY}px`;
        document.body.style.left = '0';
        document.body.style.right = '0';
        document.body.style.width = '100%';
        document.body.style.overflow = 'hidden';
        document.documentElement.style.overflow = 'hidden';
        document.documentElement.classList.add('modal-open');
        document.body.classList.add('modal-open');
        document.addEventListener('touchmove', onTouchMoveModal, { passive: false });
        isScrollLocked = true;
      }
    } else {
      if (isScrollLocked) {
        const restoreY = savedScrollY;
        document.body.style.position = '';
        document.body.style.top = '';
        document.body.style.left = '';
        document.body.style.right = '';
        document.body.style.width = '';
        document.body.style.overflow = '';
        document.documentElement.style.overflow = '';
        document.documentElement.classList.remove('modal-open');
        document.body.classList.remove('modal-open');
        document.removeEventListener('touchmove', onTouchMoveModal, { passive: false });
        isScrollLocked = false;
        window.scrollTo(0, restoreY);
      }
    }
  }
  window._updateScrollLock = updateScrollLock;

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

  try {
    const dialogObserver = new MutationObserver(() => {
      updateScrollLock();
    });
    dialogObserver.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ['open'],
      subtree: true,
    });
  } catch (_) { }

  const origShowModal = HTMLDialogElement.prototype.showModal;
  HTMLDialogElement.prototype.showModal = function (...args) {
    origShowModal.apply(this, args);
    updateScrollLock();
    preventAutofocus(this);
  };

  const origClose = HTMLDialogElement.prototype.close;
  HTMLDialogElement.prototype.close = function (...args) {
    origClose.apply(this, args);
    updateScrollLock();
  };

  document.addEventListener('close', updateScrollLock, true);
  document.addEventListener('cancel', updateScrollLock, true);
  document.addEventListener('toggle', updateScrollLock, true);

  document.addEventListener('click', (e) => {
    const btn = e.target.closest('button[commandfor], button[data-commandfor], [command]');
    if (btn) {
      setTimeout(updateScrollLock, 0);
      setTimeout(updateScrollLock, 50);
    }
  }, true);

  document.addEventListener('command', (e) => {
    if (e.command === 'show-modal') {
      const target = e.target;
      updateScrollLock();
      preventAutofocus(target);
    } else if (e.command === 'close') {
      setTimeout(updateScrollLock, 0);
    }
  });

  if (!('CommandEvent' in window)) {
    document.addEventListener('click', (e) => {
      const button = e.target.closest('button[commandfor], button[data-commandfor]');
      if (!button) return;
      const targetId = button.getAttribute('commandfor') || button.dataset.commandfor;
      const command = button.getAttribute('command') || button.dataset.command;
      if (!targetId || !command) return;

      const target = document.getElementById(targetId);
      if (!target) return;

      const event = new CustomEvent('command', { bubbles: true, cancelable: true });
      event.command = command;
      event.source = button;
      event.invoker = button;
      const proceed = target.dispatchEvent(event);

      if (proceed) {
        if (command === 'show-modal' && typeof target.showModal === 'function') {
          target.showModal();
        } else if (command === 'close' && typeof target.close === 'function') {
          target.close();
        }
      }
    });
  }
})();

class AdminDashboard {
  #labelInput;
  #errorContainer;
  #form;

  #labelPattern = /^[a-zA-Z0-9]([a-zA-Z0-9_-]{0,7}[a-zA-Z0-9])?$/;
  #sizePattern = /^\d+(\.\d+)?\s*(b|kb|kib|mb|mib|gb|gib|tb|tib)?$/i;
  #clearWord = /^(0|off|none|unlimited)$/i;

  constructor() {
    this.#labelInput = document.querySelector('input[name="label"]');
    this.#errorContainer = document.getElementById("label-err");
    this.#form = this.#labelInput?.form;

    this.#initCustomSelectors();
    this.#initContextMenus();
    this.#initLabelValidation();
    this.#initSecretCard();
    this.#initDeleteDialog();
    this.#initPurgeDialog();
    this.#initFileDeleteDialog();
    this.#initRoleDialog();
    this.#initRenameDialog();
    this.#initLimitsDialog();
    this.#initGiveawayDialog();
    this.#initInvitePolicyForm();
    this.#initSearchBox();
    this.#initQuotaForm(
      document.getElementById("globalForm"),
      document.getElementById("global-err"),
    );
    this.#initQuotaForm(
      document.getElementById("limForm"),
      document.getElementById("lim-err"),
    );
    document
      .querySelector(".quota-details > summary")
      ?.addEventListener("mousedown", (e) => {
        if (e.detail > 1) e.preventDefault();
      });
  }

  #initCustomSelectors() {
    const closeAllSelects = () => {
      document.querySelectorAll(".csel.open").forEach((c) => c.classList.remove("open"));
    };

    const toggleSelect = (btn) => {
      const container = btn?.closest(".csel");
      if (!container) return;
      const isOpen = container.classList.contains("open");
      closeAllSelects();
      if (!isOpen) {
        container.classList.add("open");
      }
    };

    document.addEventListener("command", (e) => {
      if (e.command === "--toggle-select") {
        toggleSelect(_cmdInvoker(e));
      }
    });

    document.addEventListener("click", (event) => {
      const btn = event.target.closest(".csel-btn");
      if (btn) {
        event.preventDefault();
        toggleSelect(btn);
        return;
      }

      const option = event.target.closest(".csel-opt");
      if (option) {
        const container = option.closest(".csel");
        const button = container?.querySelector(".csel-btn");
        const optionsContainer = option.closest(".csel-opts");
        const hiddenInput =
          container?.querySelector('input[type="hidden"]') ||
          container?.nextElementSibling;

        optionsContainer
          ?.querySelectorAll(".csel-opt")
          .forEach((opt) => opt.classList.remove("active"));
        option.classList.add("active");

        if (hiddenInput && hiddenInput.tagName === "INPUT") {
          const oldVal = hiddenInput.value;
          hiddenInput.value = option.dataset.value;
          if (oldVal !== option.dataset.value) {
            hiddenInput.dispatchEvent(new Event("change", { bubbles: true }));
          }
        }
        if (button) button.textContent = option.textContent;
        closeAllSelects();
        return;
      }

      if (!event.target.closest(".csel")) {
        closeAllSelects();
      }
    });

    document.addEventListener("keydown", (event) => {
      if (event.key === "Escape") {
        closeAllSelects();
      }
    });
  }

  #initContextMenus() {
    const closeAllMenus = () => {
      document.querySelectorAll(".ctx.open").forEach((ctx) => {
        ctx.classList.remove("open");
      });
    };

    const toggleMenu = (trigger) => {
      const container = trigger?.closest(".ctx");
      if (!container) return;

      const isOpen = container.classList.contains("open");
      closeAllMenus();
      if (!isOpen) {
        container.classList.add("open");
        const menu = container.querySelector(".ctx-menu");
        if (menu) {
          const rect = trigger.getBoundingClientRect();
          menu.style.position = "fixed";
          const menuHeight = menu.offsetHeight || 180;
          let top = rect.bottom + 4;
          if (top + menuHeight > window.innerHeight - 10) {
            top = rect.top - 4 - menuHeight;
          }
          menu.style.top = `${top}px`;
          const rightDist = window.innerWidth - rect.right;
          menu.style.right = `${Math.max(10, rightDist)}px`;
          menu.style.left = "auto";
          menu.style.zIndex = "9999";
        }
      }
    };

    document.addEventListener("command", (e) => {
      if (e.command === "--toggle-menu") {
        toggleMenu(_cmdInvoker(e));
      }
    });

    document.addEventListener("click", (e) => {
      const trigger = e.target.closest(".ctx-trigger");
      if (trigger) {
        e.preventDefault();
        toggleMenu(trigger);
        return;
      }

      const item = e.target.closest(".ctx-item");
      if (item) {
        closeAllMenus();
        return;
      }
      if (!e.target.closest(".ctx")) {
        closeAllMenus();
      }
    });

    window.addEventListener("scroll", closeAllMenus, true);
    window.addEventListener("resize", closeAllMenus);
    document.addEventListener("keydown", (e) => {
      if (e.key === "Escape") closeAllMenus();
    });
  }

  #initLabelValidation() {
    this.#setupInPlaceLabelValidation(
      this.#labelInput,
      this.#errorContainer,
      this.#form,
    );

    const inviteInput = document.getElementById("inviteInput");
    const inviteErr = document.getElementById("invite-err");
    const inviteForm = document.getElementById("inviteForm");
    this.#setupInPlaceLabelValidation(inviteInput, inviteErr, inviteForm);
  }

  #setupInPlaceLabelValidation(input, errorEl, form) {
    if (!input || !errorEl || !form) return () => true;

    const validate = () => {
      const val = input.value;
      if (val === "") {
        errorEl.hidden = true;
        input.classList.remove("invalid");
        return true;
      }
      if (!this.#labelPattern.test(val)) {
        errorEl.textContent =
          input.title ||
          "1-9 characters. Must start and end with a letter or number.";
        errorEl.hidden = false;
        input.classList.add("invalid");
        return false;
      }
      errorEl.hidden = true;
      input.classList.remove("invalid");
      return true;
    };

    input.addEventListener("input", validate);
    form.addEventListener("submit", (e) => {
      if (!validate()) {
        e.preventDefault();
      }
    });

    return validate;
  }

  validateLabel() {
    return this.#setupInPlaceLabelValidation(
      this.#labelInput,
      this.#errorContainer,
      this.#form,
    )();
  }

  #initSecretCard() {
    const card = document.getElementById("secretCard");
    if (!card) return;

    const secretValue = document.getElementById("sv");
    const revealButton = document.getElementById("reveal");
    const copyButton = document.getElementById("cp");
    const dismissButton = card.querySelector(".secret-close");
    const downloadButton = document.getElementById("dl-sxcu");

    const doDismiss = () => card.remove();
    const doToggleBlur = () => {
      const isBlurred = secretValue?.classList.toggle("blurred");
      if (revealButton) revealButton.textContent = isBlurred ? "Show" : "Hide";
    };
    const doCopy = () => {
      if (!secretValue) return;
      navigator.clipboard.writeText(secretValue.textContent).then(() => {
        if (copyButton) copyButton.textContent = "Copied";
        setTimeout(() => {
          if (copyButton) copyButton.textContent = "Copy";
        }, 1500);
      });
    };
    const doDownloadSxcu = (invoker) => {
      const tokenId = invoker?.dataset?.tokenId || downloadButton?.dataset?.tokenId || "";
      const secret = secretValue?.textContent || "";
      const requestUrl = window.location.origin + "/";
      const sxcu = {
        Version: "17.0.0",
        Name: "uploadserver",
        DestinationType: "ImageUploader, TextUploader, FileUploader",
        RequestMethod: "POST",
        RequestURL: requestUrl,
        Headers: {
          Authorization: "Bearer " + secret,
        },
        Body: "MultipartFormData",
        FileFormName: "file",
        URL: "{response}",
        ErrorMessage: "{response}",
      };
      const blob = new Blob([JSON.stringify(sxcu, null, 2)], {
        type: "application/json",
      });
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = tokenId + ".sxcu";
      document.body.appendChild(a);
      a.click();
      a.remove();
      URL.revokeObjectURL(url);
    };

    dismissButton?.addEventListener("click", doDismiss);
    revealButton?.addEventListener("click", doToggleBlur);
    copyButton?.addEventListener("click", doCopy);
    downloadButton?.addEventListener("click", (e) => doDownloadSxcu(e.currentTarget));

    card.addEventListener("command", (e) => {
      if (e.command === "--dismiss") {
        doDismiss();
      } else if (e.command === "--toggle-blur") {
        doToggleBlur();
      } else if (e.command === "--copy") {
        doCopy();
      } else if (e.command === "--download-sxcu") {
        doDownloadSxcu(_cmdInvoker(e));
      }
    });

    secretValue?.addEventListener("click", () => {
      if (secretValue.classList.contains("blurred")) {
        secretValue.classList.remove("blurred");
        if (revealButton) revealButton.textContent = "Hide";
      }
    });
  }

  #initDeleteDialog() {
    const dialog = document.getElementById("dlg");
    const form = document.getElementById("dlgForm");
    const message = document.getElementById("dlgmsg");
    if (!dialog || !form || !message) return;

    dialog.addEventListener("command", (e) => {
      if (e.command !== "show-modal") return;
      const invoker = _cmdInvoker(e);
      if (!invoker || !invoker.dataset.deleteId) return;

      const id = invoker.dataset.deleteId;
      const isSelf = invoker.dataset.isSelf === "true";
      if (isSelf) {
        message.textContent = `Delete your account (${id})? All your uploaded media will be permanently deleted and your access token will be removed.`;
      } else {
        message.textContent = `Delete token ${id}? All uploaded media for this token will be permanently deleted.`;
      }
      form.action = `/_/tokens/${id}/delete`;
    });
  }

  #initPurgeDialog() {
    const dialog = document.getElementById("purgedlg");
    const form = document.getElementById("purgeForm");
    const message = document.getElementById("purgedlgmsg");
    const targetPhraseEl = document.getElementById("purgeTargetPhrase");
    const input = document.getElementById("purgeConfirmInput");
    const pasteWarning = document.getElementById("purgePasteWarning");
    const submitBtn = document.getElementById("purgeSubmitBtn");

    if (!dialog || !form) return;

    let expectedPhrase = "";
    let step = "typing"; // "typing" | "countdown" | "ready" | "confirming"
    let countdownTimer = null;
    let confirmTimer = null;

    const resetPurgeState = () => {
      if (countdownTimer) {
        clearInterval(countdownTimer);
        countdownTimer = null;
      }
      if (confirmTimer) {
        clearTimeout(confirmTimer);
        confirmTimer = null;
      }
      step = "typing";
      if (input) input.value = "";
      if (pasteWarning) pasteWarning.hidden = true;
      if (submitBtn) {
        submitBtn.disabled = true;
        submitBtn.textContent = "Purge";
      }
    };

    const doPurgeAction = () => {
      if (step === "ready") {
        step = "confirming";
        if (submitBtn) submitBtn.textContent = "Really purge?";
        if (confirmTimer) clearTimeout(confirmTimer);
        confirmTimer = setTimeout(() => {
          if (step === "confirming") {
            step = "ready";
            if (submitBtn) submitBtn.textContent = "Purge";
          }
        }, 6000);
      } else if (step === "confirming") {
        if (confirmTimer) clearTimeout(confirmTimer);
        if (input && input.value.trim() === expectedPhrase) {
          form.submit();
        }
      }
    };

    dialog.addEventListener("command", (e) => {
      if (e.command === "show-modal") {
        const invoker = _cmdInvoker(e);
        if (!invoker || !invoker.dataset.purgeId) return;

        const id = invoker.dataset.purgeId;
        expectedPhrase = `PURGE ${id}`;
        if (message) {
          message.textContent = `Purge all uploaded media for token ${id}? Files will be permanently deleted.`;
        }
        if (targetPhraseEl) {
          targetPhraseEl.textContent = expectedPhrase;
        }
        if (input) {
          input.placeholder = expectedPhrase;
        }
        form.action = `/_/tokens/${id}/purge-media`;
        resetPurgeState();
      } else if (e.command === "--submit-purge") {
        e.preventDefault();
        doPurgeAction();
      }
    });

    dialog.addEventListener("close", resetPurgeState);

    if (input) {
      input.addEventListener("paste", (e) => {
        e.preventDefault();
        if (pasteWarning) pasteWarning.hidden = false;
      });

      input.addEventListener("drop", (e) => {
        e.preventDefault();
        if (pasteWarning) pasteWarning.hidden = false;
      });

      input.addEventListener("input", () => {
        if (pasteWarning) pasteWarning.hidden = true;
        const val = input.value.trim();

        if (val === expectedPhrase) {
          if (step === "typing") {
            step = "countdown";
            let remaining = 5;
            if (submitBtn) {
              submitBtn.disabled = true;
              submitBtn.textContent = `Wait ${remaining}s`;
            }
            if (countdownTimer) clearInterval(countdownTimer);
            countdownTimer = setInterval(() => {
              remaining--;
              if (remaining > 0) {
                if (submitBtn) submitBtn.textContent = `Wait ${remaining}s`;
              } else {
                clearInterval(countdownTimer);
                countdownTimer = null;
                step = "ready";
                if (submitBtn) {
                  submitBtn.disabled = false;
                  submitBtn.textContent = "Purge";
                }
              }
            }, 1000);
          }
        } else {
          if (step !== "typing") {
            resetPurgeState();
          }
        }
      });
    }

    form.addEventListener("submit", (e) => {
      e.preventDefault();
      doPurgeAction();
    });
  }

  #initFileDeleteDialog() {
    const dialog = document.getElementById("filedel-dlg");
    const input = document.getElementById("filedel-input");
    const message = document.getElementById("filedel-msg");
    if (!dialog || !input || !message) return;

    dialog.addEventListener("command", (e) => {
      if (e.command !== "show-modal") return;
      const invoker = _cmdInvoker(e);
      if (!invoker) return;

      let filename = invoker.dataset.deleteFilename;
      if (!filename && invoker.id === "modalDeleteBtn") {
        const modalDeleteFilename = document.getElementById(
          "modalDeleteFilename",
        );
        filename = modalDeleteFilename ? modalDeleteFilename.value : "";
      }
      if (!filename) return;

      input.value = filename;
      message.textContent = `Delete file "${filename}" permanently? It will be removed from disk.`;
    });
  }

  #initRoleDialog() {
    const dialog = document.getElementById("roledlg");
    const form = document.getElementById("roleForm");
    const targetInput = document.getElementById("roleTargetInput");
    const message = document.getElementById("roledlgmsg");
    if (!dialog || !form || !targetInput || !message) return;

    dialog.addEventListener("command", (e) => {
      if (e.command !== "show-modal") return;
      const invoker = _cmdInvoker(e);
      if (!invoker || !invoker.dataset.roleId) return;

      const id = invoker.dataset.roleId;
      const targetRole = invoker.dataset.roleTarget;
      const label = invoker.dataset.roleLabel;

      targetInput.value = targetRole;
      message.textContent = `${label} for token ${id}?`;
      form.action = `/_/tokens/${id}/role`;
    });
  }

  #initRenameDialog() {
    const dialog = document.getElementById("renamedlg");
    const form = document.getElementById("renameForm");
    const input = document.getElementById("renameInput");
    const errorEl = document.getElementById("rename-err");
    const target = document.getElementById("renameTarget");

    if (form && input) {
      this.#setupInPlaceLabelValidation(input, errorEl, form);
    }

    dialog?.addEventListener("command", (e) => {
      if (e.command !== "show-modal") return;
      const invoker = _cmdInvoker(e);
      if (!invoker || !invoker.dataset.renameId) return;

      const id = invoker.dataset.renameId;
      if (target) target.textContent = id;
      if (input) {
        input.value = invoker.dataset.label || "";
        input.classList.remove("invalid");
      }
      if (errorEl) errorEl.hidden = true;
      if (form) form.action = `/_/tokens/${id}/label`;
    });
  }

  #initLimitsDialog() {
    const dialog = document.getElementById("limdlg");
    const form = document.getElementById("limForm");
    const errorEl = document.getElementById("lim-err");
    const target = document.getElementById("limTarget");

    if (form) {
      form.elements["bypass"]?.addEventListener("change", () =>
        this.#applyExemptState(form, errorEl),
      );
    }

    dialog?.addEventListener("command", (e) => {
      if (e.command !== "show-modal") return;
      const invoker = _cmdInvoker(e);
      if (!invoker || !invoker.dataset.limitId) return;

      const id = invoker.dataset.limitId;
      if (target) target.textContent = id;
      if (form) {
        form.action = `/_/tokens/${id}/limits`;
        if (form.elements["max_bytes"])
          form.elements["max_bytes"].value = invoker.dataset.maxBytes || "";
        if (form.elements["max_uploads"])
          form.elements["max_uploads"].value = invoker.dataset.maxUploads || "";
        if (form.elements["monthly_bytes"])
          form.elements["monthly_bytes"].value =
            invoker.dataset.monthlyBytes || "";
        if (form.elements["monthly_uploads"])
          form.elements["monthly_uploads"].value =
            invoker.dataset.monthlyUploads || "";
        if (form.elements["invites"])
          form.elements["invites"].value = invoker.dataset.invites || "";
        if (form.elements["bypass"])
          form.elements["bypass"].checked = invoker.dataset.bypass === "1";
        this.#resetQuotaForm(form, errorEl);
        this.#applyExemptState(form, errorEl);
      }
    });
  }

  #applyExemptState(form, errorEl) {
    const exempt = form.elements["bypass"]?.checked;
    form.querySelectorAll("input[data-kind]").forEach((input) => {
      input.disabled = exempt;
      if (exempt) input.classList.remove("invalid");
    });
    form.classList.toggle("exempt", exempt);
    if (exempt && errorEl) errorEl.textContent = "";
  }

  #initQuotaForm(form, errorEl) {
    if (!form) return;

    form.querySelectorAll("input[data-kind]").forEach((input) => {
      input.addEventListener("input", () =>
        this.#validateQuotaForm(form, errorEl),
      );
    });

    form.addEventListener("submit", (event) => {
      if (!this.#validateQuotaForm(form, errorEl)) {
        event.preventDefault();
      }
    });
  }

  #validateQuotaForm(form, errorEl) {
    let firstError = null;
    form.querySelectorAll("input[data-kind]").forEach((input) => {
      if (input.disabled) {
        input.classList.remove("invalid");
        return;
      }
      const message = this.#quotaFieldError(input);
      if (message) {
        input.classList.add("invalid");
        if (!firstError) firstError = message;
      } else {
        input.classList.remove("invalid");
      }
    });
    if (errorEl) {
      errorEl.textContent = firstError ?? "";
    }
    return !firstError;
  }

  #quotaFieldError(input) {
    const value = input.value.trim();
    if (value === "" || this.#clearWord.test(value)) {
      return null;
    }
    if (input.dataset.kind === "size") {
      return this.#sizePattern.test(value)
        ? null
        : "Enter a size like 500MB or 5GB, or 0 for unlimited.";
    }
    return /^\d+$/.test(value.replace(/,/g, ""))
      ? null
      : "Enter a whole number of uploads, or 0 for unlimited.";
  }

  #resetQuotaForm(form, errorEl) {
    form
      .querySelectorAll("input.invalid")
      .forEach((input) => input.classList.remove("invalid"));
    if (errorEl) {
      errorEl.textContent = "";
    }
  }

  #initGiveawayDialog() {
    const modeSelect = document.getElementById("giveawayMode");
    const poolGroup = document.getElementById("giveawayPoolGroup");

    if (modeSelect && poolGroup) {
      modeSelect.addEventListener("change", () => {
        poolGroup.hidden = modeSelect.value !== "random";
      });
    }
  }

  #initInvitePolicyForm() {
    const modeSelect = document.getElementById("schedModeSelect");
    const poolGroup = document.getElementById("schedPoolGroup");
    if (!modeSelect || !poolGroup) return;

    modeSelect.addEventListener("change", () => {
      poolGroup.hidden = modeSelect.value !== "random";
    });
  }

  #initSearchBox() {
    const searchForm = document.getElementById("searchForm");
    const searchInput = document.getElementById("searchInput");
    const searchClear = document.getElementById("searchClear");

    const doClear = () => {
      window.location.href = window.location.pathname;
    };

    searchClear?.addEventListener("click", doClear);

    searchForm?.addEventListener("command", (e) => {
      if (e.command === "--clear-search") {
        doClear();
      }
    });

    if (searchForm && searchInput) {
      searchForm.addEventListener("submit", (e) => {
        if (!searchInput.value.trim()) {
          e.preventDefault();
          window.location.href = window.location.pathname;
        }
      });
    }
  }
}

document.addEventListener("DOMContentLoaded", () => new AdminDashboard());
