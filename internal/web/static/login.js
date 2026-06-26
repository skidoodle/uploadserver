document.addEventListener('DOMContentLoaded', () => {
  const token = document.querySelector('input[name="token"]');
  const form = token?.form;
  if (!token || !form) return;

  form.addEventListener('submit', () => {
    token.value = token.value.trim();
  });
});
