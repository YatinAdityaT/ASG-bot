(() => {
  const RETRY_MS = 30_000;
  const JOIN_DELAY_MS = 20_000;
  const LAST_RELOAD_KEY = 'asg-headless-last-reload';
  const startedAt = Date.now();
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
    const expandButton = [...document.querySelectorAll('button')]
      .find(button =>
        (button.getAttribute('aria-label') || button.innerText || '').trim() ===
        'Expand'
      );
    if (expandButton) {
      expandButton.click();
      return;
    }
    if (window.MMMeetCall?.isInCall(document) ||
        Date.now() - startedAt < JOIN_DELAY_MS) return;

    const text = pageText();
    if (text.includes('you are offline') ||
        text.includes("we've encountered a problem")) {
      reloadAfterFailure();
      return;
    }

    const controls = [...document.querySelectorAll(
      'button, [role="button"], [role="menuitem"]'
    )];
    if (new URL(location.href).searchParams.get('companion') === '1') {
      const joinButton = controls.find(control =>
        /^join now$/i.test((control.innerText || '').trim())
      );
      if (!joinButton || Date.now() - lastJoinAttempt < RETRY_MS) return;
      lastJoinAttempt = Date.now();
      joinButton.removeAttribute('disabled');
      joinButton.disabled = false;
      joinButton.click();
      return;
    }
    const companionButton = controls
      .find(control =>
        /(?:use|start) companion mode/i.test(control.innerText || '')
      );
    if (!companionButton) {
      controls
        .find(control => /other ways to join/i.test(control.innerText || ''))
        ?.click();
      return;
    }
    if (Date.now() - lastJoinAttempt < RETRY_MS) return;

    lastJoinAttempt = Date.now();
    companionButton.removeAttribute('disabled');
    companionButton.disabled = false;
    companionButton.click();
  };

  ensureJoined();
  setInterval(ensureJoined, 2_000);
})();
