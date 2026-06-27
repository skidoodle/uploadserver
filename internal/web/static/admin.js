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
    this.#initLabelValidation();
    this.#initSecretCard();
    this.#initDeleteDialog();
    this.#initLimitsDialog();
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

  #initLabelValidation() {
    if (!this.#labelInput || !this.#errorContainer || !this.#form) {
      return;
    }

    this.#labelInput.addEventListener("input", () => this.validateLabel());

    this.#form.addEventListener("submit", (event) => {
      if (!this.validateLabel()) {
        event.preventDefault();
      }
    });
  }

  validateLabel() {
    if (!this.#labelInput || !this.#errorContainer) {
      return true;
    }

    const value = this.#labelInput.value;

    if (value === "") {
      this.#errorContainer.hidden = true;
      this.#labelInput.classList.remove("invalid");
      return true;
    }

    if (!this.#labelPattern.test(value)) {
      this.#errorContainer.textContent = this.#labelInput.title;
      this.#errorContainer.hidden = false;
      this.#labelInput.classList.add("invalid");
      return false;
    }

    this.#errorContainer.hidden = true;
    this.#labelInput.classList.remove("invalid");
    return true;
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
}

document.addEventListener("DOMContentLoaded", () => new AdminDashboard());
