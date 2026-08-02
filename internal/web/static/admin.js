class AdminDashboard {
  #labelInput;
  #errorContainer;
  #form;
  #roleSelector;
  #roleButton;
  #roleOptionsContainer;
  #roleHiddenInput;

  #labelPattern = /^[a-zA-Z0-9]([a-zA-Z0-9_-]{0,7}[a-zA-Z0-9])?$/;
  #sizePattern = /^\d+(\.\d+)?\s*(b|kb|kib|mb|mib|gb|gib|tb|tib)?$/i;
  #clearWord = /^(0|off|none|unlimited)$/i;

  constructor() {
    this.#labelInput = document.querySelector('input[name="label"]');
    this.#errorContainer = document.getElementById("label-err");
    this.#form = this.#labelInput?.form;

    this.#roleSelector = document.getElementById("roleSel");
    this.#roleButton = document.getElementById("roleBtn");
    this.#roleOptionsContainer = document.getElementById("roleOpts");
    this.#roleHiddenInput = document.getElementById("role");

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
    document.querySelectorAll(".csel").forEach((container) => {
      const button = container.querySelector(".csel-btn");
      const optionsContainer = container.querySelector(".csel-opts");
      if (!button || !optionsContainer) return;

      const hiddenInput =
        container.querySelector('input[type="hidden"]') ||
        container.nextElementSibling;

      button.addEventListener("click", (event) => {
        event.preventDefault();
        event.stopPropagation();
        document.querySelectorAll(".csel.open").forEach((c) => {
          if (c !== container) c.classList.remove("open");
        });
        container.classList.toggle("open");
      });

      optionsContainer.addEventListener("click", (event) => {
        event.stopPropagation();
        const option = event.target.closest(".csel-opt");
        if (!option) return;

        optionsContainer.querySelectorAll(".csel-opt").forEach((opt) => {
          opt.classList.remove("active");
        });

        option.classList.add("active");
        if (hiddenInput && hiddenInput.tagName === "INPUT") {
          const oldVal = hiddenInput.value;
          hiddenInput.value = option.dataset.value;
          if (oldVal !== option.dataset.value) {
            hiddenInput.dispatchEvent(new Event("change", { bubbles: true }));
          }
        }
        button.textContent = option.textContent;
        container.classList.remove("open");
      });
    });

    document.addEventListener("click", (event) => {
      if (!event.target.closest(".csel")) {
        document.querySelectorAll(".csel.open").forEach((c) => {
          c.classList.remove("open");
        });
      }
    });

    document.addEventListener("keydown", (event) => {
      if (event.key === "Escape") {
        document.querySelectorAll(".csel.open").forEach((c) => {
          c.classList.remove("open");
        });
      }
    });
  }

  #initContextMenus() {
    const closeAll = () => {
      document.querySelectorAll('.ctx.open').forEach(ctx => {
        ctx.classList.remove('open');
      });
    };

    document.querySelectorAll('.ctx').forEach(ctx => {
      const trigger = ctx.querySelector('.ctx-trigger');
      const menu = ctx.querySelector('.ctx-menu');
      if (!trigger || !menu) return;

      trigger.addEventListener('click', (e) => {
        e.stopPropagation();
        const isOpen = ctx.classList.contains('open');
        closeAll();
        if (!isOpen) {
          ctx.classList.add('open');
          const rect = trigger.getBoundingClientRect();
          menu.style.position = 'fixed';
          const menuHeight = menu.offsetHeight;
          let top = rect.bottom + 4;
          if (top + menuHeight > window.innerHeight - 10) {
            top = rect.top - 4 - menuHeight;
          }
          menu.style.top = `${top}px`;
          const rightDist = window.innerWidth - rect.right;
          menu.style.right = `${Math.max(10, rightDist)}px`;
          menu.style.left = 'auto';
          menu.style.zIndex = '9999';
        }
      });

      menu.querySelectorAll('.ctx-item').forEach(item => {
        if (item.tagName === 'A' || item.type === 'button') {
          item.addEventListener('click', () => {
            closeAll();
          });
        }
      });
    });

    document.addEventListener('click', (e) => {
      if (!e.target.closest('.ctx-menu')) {
        closeAll();
      }
    });

    window.addEventListener('scroll', closeAll, true);
    window.addEventListener('resize', closeAll);

    document.addEventListener('keydown', (e) => {
      if (e.key === 'Escape') {
        closeAll();
      }
    });
  }

  #initLabelValidation() {
    this.#setupInPlaceLabelValidation(this.#labelInput, this.#errorContainer, this.#form);

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
        errorEl.textContent = input.title || "1-9 characters. Must start and end with a letter or number.";
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
    return this.#setupInPlaceLabelValidation(this.#labelInput, this.#errorContainer, this.#form)();
  }

  #initSecretCard() {
    const card = document.querySelector(".secret");
    if (!card) return;

    card
      .querySelector(".secret-close")
      ?.addEventListener("click", () => card.remove());

    const secretValue = document.getElementById("sv");

    const revealButton = document.getElementById("reveal");
    revealButton?.addEventListener("click", () => {
      const isBlurred = secretValue.classList.toggle("blurred");
      revealButton.textContent = isBlurred ? "Show" : "Hide";
    });

    secretValue?.addEventListener("click", () => {
      if (secretValue.classList.contains("blurred")) {
        secretValue.classList.remove("blurred");
        if (revealButton) revealButton.textContent = "Hide";
      }
    });

    const copyButton = document.getElementById("cp");
    copyButton?.addEventListener("click", () => {
      navigator.clipboard.writeText(secretValue.textContent).then(() => {
        copyButton.textContent = "Copied";
        setTimeout(() => (copyButton.textContent = "Copy"), 1500);
      });
    });

    const dlButton = document.getElementById("dl-sxcu");
    dlButton?.addEventListener("click", () => {
      const tokenId = dlButton.dataset.tokenId;
      const secret = secretValue.textContent;
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
    });
  }

  #initDeleteDialog() {
    document.addEventListener("click", (e) => {
      const button = e.target.closest("button[data-delete-id]");
      if (!button) return;
      const dialog = document.getElementById("dlg");
      const form = document.getElementById("dlgForm");
      const message = document.getElementById("dlgmsg");
      if (!dialog || !form || !message) return;

      const id = button.dataset.deleteId;
      const isSelf = button.dataset.isSelf === "true";
      if (isSelf) {
        message.textContent = `Delete your account (${id})? All your uploaded media will be permanently deleted and your access token will be removed.`;
      } else {
        message.textContent = `Delete token ${id}? All uploaded media for this token will be permanently deleted.`;
      }
      form.action = `/_/tokens/${id}/delete`;
      dialog.showModal();
    });

    document.querySelectorAll("dialog#dlg [data-cancel]").forEach((btn) => {
      btn.addEventListener("click", () => document.getElementById("dlg")?.close());
    });
  }

  #initPurgeDialog() {
    document.addEventListener("click", (e) => {
      const button = e.target.closest("button[data-purge-id]");
      if (!button) return;
      const dialog = document.getElementById("purgedlg");
      const form = document.getElementById("purgeForm");
      const message = document.getElementById("purgedlgmsg");
      if (!dialog || !form || !message) return;

      const id = button.dataset.purgeId;
      message.textContent = `Purge ALL uploaded media for token ${id}? All files will be permanently deleted from disk.`;
      form.action = `/_/tokens/${id}/purge-media`;
      dialog.showModal();
    });

    document.querySelectorAll("dialog#purgedlg [data-cancel]").forEach((btn) => {
      btn.addEventListener("click", () => document.getElementById("purgedlg")?.close());
    });
  }

  #initFileDeleteDialog() {
    document.addEventListener("click", (e) => {
      const button = e.target.closest("button[data-delete-filename]");
      const modalBtn = e.target.closest("#modalDeleteBtn");
      if (!button && !modalBtn) return;

      const dialog = document.getElementById("filedel-dlg");
      const input = document.getElementById("filedel-input");
      const message = document.getElementById("filedel-msg");
      if (!dialog || !input || !message) return;

      let filename = "";
      if (button) {
        filename = button.dataset.deleteFilename;
      } else if (modalBtn) {
        const modalDeleteFilename = document.getElementById("modalDeleteFilename");
        filename = modalDeleteFilename ? modalDeleteFilename.value : "";
      }

      if (!filename) return;
      input.value = filename;
      message.textContent = `Delete file "${filename}" permanently? It will be removed from disk.`;
      dialog.showModal();
    });

    document.querySelectorAll("dialog#filedel-dlg [data-cancel]").forEach((btn) => {
      btn.addEventListener("click", () => document.getElementById("filedel-dlg")?.close());
    });
  }

  #initRoleDialog() {
    document.addEventListener("click", (e) => {
      const button = e.target.closest("button[data-role-id]");
      if (!button) return;
      const dialog = document.getElementById("roledlg");
      const form = document.getElementById("roleForm");
      const targetInput = document.getElementById("roleTargetInput");
      const message = document.getElementById("roledlgmsg");
      if (!dialog || !form || !targetInput || !message) return;

      const id = button.dataset.roleId;
      const targetRole = button.dataset.roleTarget;
      const label = button.dataset.roleLabel;

      targetInput.value = targetRole;
      message.textContent = `${label} for token ${id}?`;
      form.action = `/_/tokens/${id}/role`;
      dialog.showModal();
    });

    document.querySelectorAll("dialog#roledlg [data-cancel]").forEach((btn) => {
      btn.addEventListener("click", () => document.getElementById("roledlg")?.close());
    });
  }

  #initRenameDialog() {
    const form = document.getElementById("renameForm");
    const input = document.getElementById("renameInput");
    const errorEl = document.getElementById("rename-err");

    if (form && input) {
      this.#setupInPlaceLabelValidation(input, errorEl, form);
    }

    document.addEventListener("click", (e) => {
      const button = e.target.closest("button[data-rename-id]");
      if (!button) return;
      const dialog = document.getElementById("renamedlg");
      const f = document.getElementById("renameForm");
      const target = document.getElementById("renameTarget");
      const inp = document.getElementById("renameInput");
      const errEl = document.getElementById("rename-err");
      if (!dialog || !f || !target || !inp) return;

      const id = button.dataset.renameId;
      target.textContent = id;
      inp.value = button.dataset.label || "";
      inp.classList.remove("invalid");
      if (errEl) errEl.hidden = true;
      f.action = `/_/tokens/${id}/label`;
      dialog.showModal();
    });

    document.querySelectorAll("dialog#renamedlg [data-cancel]").forEach((btn) => {
      btn.addEventListener("click", () => document.getElementById("renamedlg")?.close());
    });
  }

  #initLimitsDialog() {
    const form = document.getElementById("limForm");
    const errorEl = document.getElementById("lim-err");

    if (form) {
      form.elements["bypass"]?.addEventListener("change", () =>
        this.#applyExemptState(form, errorEl),
      );
    }

    document.addEventListener("click", (e) => {
      const button = e.target.closest("button[data-limit-id]");
      if (!button) return;
      const dialog = document.getElementById("limdlg");
      const f = document.getElementById("limForm");
      const target = document.getElementById("limTarget");
      if (!dialog || !f || !target) return;

      const id = button.dataset.limitId;
      target.textContent = id;
      f.action = `/_/tokens/${id}/limits`;
      if (f.elements["max_bytes"]) f.elements["max_bytes"].value = button.dataset.maxBytes || "";
      if (f.elements["max_uploads"]) f.elements["max_uploads"].value = button.dataset.maxUploads || "";
      if (f.elements["monthly_bytes"]) f.elements["monthly_bytes"].value = button.dataset.monthlyBytes || "";
      if (f.elements["monthly_uploads"]) f.elements["monthly_uploads"].value = button.dataset.monthlyUploads || "";
      if (f.elements["invites"]) f.elements["invites"].value = button.dataset.invites || "";
      if (f.elements["bypass"]) f.elements["bypass"].checked = button.dataset.bypass === "1";
      this.#resetQuotaForm(f, errorEl);
      this.#applyExemptState(f, errorEl);
      dialog.showModal();
    });

    document.querySelectorAll("dialog#limdlg [data-cancel]").forEach((btn) => {
      btn.addEventListener("click", () => document.getElementById("limdlg")?.close());
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
    const dialog = document.getElementById("giveawaydlg");
    const button = document.getElementById("giveawayBtn");
    if (!dialog || !button) return;

    const modeSelect = document.getElementById("giveawayMode");
    const poolGroup = document.getElementById("giveawayPoolGroup");

    if (modeSelect && poolGroup) {
      modeSelect.addEventListener("change", () => {
        poolGroup.hidden = modeSelect.value !== "random";
      });
    }

    button.addEventListener("click", () => {
      dialog.showModal();
    });

    dialog.querySelector("[data-cancel]")?.addEventListener("click", () => {
      dialog.close();
    });
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

    if (searchClear) {
      searchClear.addEventListener("click", () => {
        window.location.href = window.location.pathname;
      });
    }

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
