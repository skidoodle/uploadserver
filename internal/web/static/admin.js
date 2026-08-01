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

    this.#initRoleSelector();
    this.#initContextMenus();
    this.#initLabelValidation();
    this.#initSecretCard();
    this.#initDeleteDialog();
    this.#initRoleDialog();
    this.#initRenameDialog();
    this.#initLimitsDialog();
    this.#initGiveawayDialog();
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

  #initRoleSelector() {
    if (
      !this.#roleSelector ||
      !this.#roleButton ||
      !this.#roleOptionsContainer ||
      !this.#roleHiddenInput
    ) {
      return;
    }

    this.#roleButton.addEventListener("click", (event) => {
      event.preventDefault();
      this.#roleSelector.classList.toggle("open");
    });

    this.#roleOptionsContainer.addEventListener("click", (event) => {
      const option = event.target.closest(".csel-opt");
      if (!option) return;

      this.#roleOptionsContainer
        .querySelectorAll(".csel-opt")
        .forEach((opt) => {
          opt.classList.remove("active");
        });

      option.classList.add("active");
      this.#roleHiddenInput.value = option.dataset.value;
      this.#roleButton.textContent = option.textContent;
      this.#roleSelector.classList.remove("open");
    });

    document.addEventListener("click", (event) => {
      if (!this.#roleSelector.contains(event.target)) {
        this.#roleSelector.classList.remove("open");
      }
    });

    document.addEventListener("keydown", (event) => {
      if (event.key === "Escape") {
        this.#roleSelector.classList.remove("open");
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
          menu.style.top = `${rect.bottom + 4}px`;
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
    const dialog = document.getElementById("dlg");
    const form = document.getElementById("dlgForm");
    const message = document.getElementById("dlgmsg");
    if (!dialog || !form || !message) return;

    document.querySelectorAll("button[data-delete-id]").forEach((button) => {
      button.addEventListener("click", () => {
        const id = button.dataset.deleteId;
        message.textContent = `Delete token ${id}? Uploads using it stop working immediately.`;
        form.action = `/tokens/${id}/delete`;
        dialog.showModal();
      });
    });

    dialog
      .querySelector("[data-cancel]")
      ?.addEventListener("click", () => dialog.close());
  }

  #initRoleDialog() {
    const dialog = document.getElementById("roledlg");
    const form = document.getElementById("roleForm");
    const targetInput = document.getElementById("roleTargetInput");
    const message = document.getElementById("roledlgmsg");
    if (!dialog || !form || !targetInput || !message) return;

    document.querySelectorAll("button[data-role-id]").forEach((button) => {
      button.addEventListener("click", () => {
        const id = button.dataset.roleId;
        const targetRole = button.dataset.roleTarget;
        const label = button.dataset.roleLabel;

        targetInput.value = targetRole;
        message.textContent = `${label} for token ${id}?`;
        form.action = `/tokens/${id}/role`;
        dialog.showModal();
      });
    });

    dialog
      .querySelector("[data-cancel]")
      ?.addEventListener("click", () => dialog.close());
  }

  #initRenameDialog() {
    const dialog = document.getElementById("renamedlg");
    const form = document.getElementById("renameForm");
    const target = document.getElementById("renameTarget");
    const input = document.getElementById("renameInput");
    const errorEl = document.getElementById("rename-err");
    if (!dialog || !form || !target || !input) return;

    this.#setupInPlaceLabelValidation(input, errorEl, form);

    document.querySelectorAll("button[data-rename-id]").forEach((button) => {
      button.addEventListener("click", () => {
        const id = button.dataset.renameId;
        target.textContent = id;
        input.value = button.dataset.label || "";
        input.classList.remove("invalid");
        if (errorEl) errorEl.hidden = true;
        form.action = `/tokens/${id}/label`;
        dialog.showModal();
      });
    });

    dialog
      .querySelector("[data-cancel]")
      ?.addEventListener("click", () => dialog.close());
  }

  #initLimitsDialog() {
    const dialog = document.getElementById("limdlg");
    const form = document.getElementById("limForm");
    const target = document.getElementById("limTarget");
    if (!dialog || !form || !target) return;

    const errorEl = document.getElementById("lim-err");

    form.elements["bypass"]?.addEventListener("change", () =>
      this.#applyExemptState(form, errorEl),
    );

    document.querySelectorAll("button[data-limit-id]").forEach((button) => {
      button.addEventListener("click", () => {
        const id = button.dataset.limitId;
        target.textContent = id;
        form.action = `/tokens/${id}/limits`;
        form.elements["max_bytes"].value = button.dataset.maxBytes || "";
        form.elements["max_uploads"].value = button.dataset.maxUploads || "";
        form.elements["monthly_bytes"].value =
          button.dataset.monthlyBytes || "";
        form.elements["monthly_uploads"].value =
          button.dataset.monthlyUploads || "";
        if (form.elements["invites"]) {
          form.elements["invites"].value = button.dataset.invites || "";
        }
        form.elements["bypass"].checked = button.dataset.bypass === "1";
        this.#resetQuotaForm(form, errorEl);
        this.#applyExemptState(form, errorEl);
        dialog.showModal();
      });
    });

    dialog
      .querySelector("[data-cancel]")
      ?.addEventListener("click", () => dialog.close());
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

    button.addEventListener("click", () => {
      dialog.showModal();
    });

    dialog.querySelector("[data-cancel]")?.addEventListener("click", () => {
      dialog.close();
    });
  }
}

document.addEventListener("DOMContentLoaded", () => new AdminDashboard());
