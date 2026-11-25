(() => {
  const RETRY_MS = 30_000;
  const LAST_RELOAD_KEY = 'asg-headless-last-reload';
  let lastJoinAttempt = 0;

  const pageText = () => (document.body?.innerText || '').toLowerCase();

  const reloadAfterFailure = () => {
    const now = Date.now();
    const lastReload = Number(sessionStorage.getItem(LAST_RELOAD_KEY) || 0);
    if (now - lastReload < RETRY_MS) return;
    sessionStorage.setItem(LAST_RELOAD_KEY, String(now));
    location.reload();
  };

  const ensureJoined = () => {
    if (window.MMMeetCall?.isInCall(document)) return;

    const text = pageText();
    if (text.includes('you are offline') ||
        text.includes("we've encountered a problem")) {
      reloadAfterFailure();
      return;
    }

    const companionButton = [...document.querySelectorAll('button')]
      .find(button => /use companion mode/i.test(button.innerText || ''));
    if (!companionButton || Date.now() - lastJoinAttempt < RETRY_MS) return;

    lastJoinAttempt = Date.now();
    companionButton.removeAttribute('disabled');
    companionButton.disabled = false;
    companionButton.click();
  };

  ensureJoined();
  setInterval(ensureJoined, 2_000);
})();
